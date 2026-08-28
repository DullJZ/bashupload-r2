package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

func TestHTTPHandlerDrainState(t *testing.T) {
	var ready atomic.Bool
	ready.Store(true)
	handler := newHTTPHandler(&ready)

	request := func(method, path, remoteAddr string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = remoteAddr
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	if got := request(http.MethodGet, "/readyz", "192.0.2.1:1234").Code; got != http.StatusOK {
		t.Fatalf("ready status = %d, want %d", got, http.StatusOK)
	}
	if got := request(http.MethodPost, "/-/drain", "192.0.2.1:1234").Code; got != http.StatusForbidden {
		t.Fatalf("remote drain status = %d, want %d", got, http.StatusForbidden)
	}
	if got := request(http.MethodPost, "/-/drain", "127.0.0.1:1234").Code; got != http.StatusAccepted {
		t.Fatalf("local drain status = %d, want %d", got, http.StatusAccepted)
	}
	if got := request(http.MethodGet, "/readyz", "192.0.2.1:1234").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("draining readiness status = %d, want %d", got, http.StatusServiceUnavailable)
	}
	if got := request(http.MethodGet, "/api/config", "192.0.2.1:1234").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("new request while draining status = %d, want %d", got, http.StatusServiceUnavailable)
	}
	if got := request(http.MethodGet, "/healthz", "192.0.2.1:1234").Code; got != http.StatusOK {
		t.Fatalf("liveness while draining status = %d, want %d", got, http.StatusOK)
	}
}

func TestServeUntilShutdownWaitsForHandlerAndCleanup(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(requestStarted)
		<-releaseRequest
		w.WriteHeader(http.StatusNoContent)
	})}

	ctx, cancel := context.WithCancel(context.Background())
	cleanupCtx, stopCleanup := context.WithCancel(ctx)
	cleanupStarted := make(chan struct{})
	releaseCleanup := make(chan struct{})
	cleanupDone := startScheduledCleanup(cleanupCtx, false, time.Millisecond, time.Hour, func() {
		close(cleanupStarted)
		<-releaseCleanup
	})
	var ready atomic.Bool
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- serveUntilShutdown(ctx, listener, server, &ready, stopCleanup, cleanupDone)
	}()

	select {
	case <-cleanupStarted:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not start")
	}
	requestDone := make(chan error, 1)
	go func() {
		response, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			response.Body.Close()
		}
		requestDone <- err
	}()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("request did not reach handler")
	}

	cancel()
	eventually(t, time.Second, func() bool { return !ready.Load() }, "readiness did not fail during shutdown")
	select {
	case err := <-serveDone:
		t.Fatalf("server returned before active work completed: %v", err)
	default:
	}

	close(releaseRequest)
	select {
	case err := <-serveDone:
		t.Fatalf("server returned before cleanup completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseCleanup)

	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("serveUntilShutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not finish after active work completed")
	}
	if err := <-requestDone; err != nil {
		t.Fatalf("in-flight request failed: %v", err)
	}
}

func eventually(t *testing.T, timeout time.Duration, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal(message)
		}
		time.Sleep(time.Millisecond)
	}
}

const offlineTestBucket = "offline-gc-test"

type offlineTestObject struct {
	body         []byte
	etag         string
	lastModified time.Time
}

type offlineTestS3 struct {
	mu sync.Mutex

	bucket  string
	objects map[string]offlineTestObject

	failListPrefix string
	failGetKey     string

	listPrefixes   []string
	getKeys        []string
	deleteRequests [][]string
}

type scriptedMaintenanceStore struct {
	pages         []*s3.ListObjectsV2Output
	listCalls     int
	deleteOutputs []*s3.DeleteObjectsOutput
	deleteErrs    []error
	deleteInputs  []*s3.DeleteObjectsInput
	deleteCalls   int
}

func (s *scriptedMaintenanceStore) ListObjectsV2(*s3.ListObjectsV2Input) (*s3.ListObjectsV2Output, error) {
	if s.listCalls >= len(s.pages) {
		return nil, fmt.Errorf("unexpected LIST request %d", s.listCalls+1)
	}
	page := s.pages[s.listCalls]
	s.listCalls++
	return page, nil
}

func (*scriptedMaintenanceStore) GetObject(*s3.GetObjectInput) (*s3.GetObjectOutput, error) {
	return nil, fmt.Errorf("unexpected GET request")
}

func (s *scriptedMaintenanceStore) DeleteObjects(input *s3.DeleteObjectsInput) (*s3.DeleteObjectsOutput, error) {
	call := s.deleteCalls
	s.deleteCalls++
	s.deleteInputs = append(s.deleteInputs, input)
	var output *s3.DeleteObjectsOutput
	if call < len(s.deleteOutputs) {
		output = s.deleteOutputs[call]
	}
	var err error
	if call < len(s.deleteErrs) {
		err = s.deleteErrs[call]
	}
	return output, err
}

func newOfflineTestS3() (*offlineTestS3, *httptest.Server, *s3.S3) {
	store := &offlineTestS3{
		bucket:  offlineTestBucket,
		objects: make(map[string]offlineTestObject),
	}
	server := httptest.NewServer(http.HandlerFunc(store.serveHTTP))
	sess := session.Must(session.NewSession(&aws.Config{
		Region:           aws.String("auto"),
		Endpoint:         aws.String(server.URL),
		S3ForcePathStyle: aws.Bool(true),
		Credentials:      credentials.NewStaticCredentials("test-access", "test-secret", ""),
	}))
	return store, server, s3.New(sess)
}

func runOfflineTestGC(store *offlineTestS3, client *s3.S3, deleteMode bool) (offlineGCStats, error) {
	return runOfflineGCWithStore(client, store.bucket, time.Now().UTC(), offlineGCOptions{
		deleteMode:  deleteMode,
		expiryGrace: defaultOfflineGCExpiryGrace,
	})
}

func (s *offlineTestS3) put(key string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = offlineTestObject{
		body:         append([]byte(nil), body...),
		etag:         key + "-etag",
		lastModified: time.Now().UTC(),
	}
}

func (s *offlineTestS3) has(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[key]
	return ok
}

func (s *offlineTestS3) deletedKeyCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, request := range s.deleteRequests {
		count += len(request)
	}
	return count
}

func (s *offlineTestS3) serveHTTP(w http.ResponseWriter, r *http.Request) {
	key, ok := s.objectKey(r)
	if !ok {
		s.writeError(w, http.StatusBadRequest, "InvalidRequest", "invalid bucket path")
		return
	}

	if r.Method == http.MethodPost && r.URL.Query().Has("delete") {
		s.serveDeleteObjects(w, r)
		return
	}
	if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
		s.serveList(w, r)
		return
	}

	s.mu.Lock()
	if r.Method == http.MethodGet {
		s.getKeys = append(s.getKeys, key)
	}
	failGet := s.failGetKey == key
	object, exists := s.objects[key]
	s.mu.Unlock()
	if failGet {
		s.writeError(w, http.StatusInternalServerError, "InternalError", "injected GET failure")
		return
	}
	if !exists {
		s.writeError(w, http.StatusNotFound, "NoSuchKey", "object not found")
		return
	}

	w.Header().Set("ETag", fmt.Sprintf("%q", object.etag))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not supported")
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(object.body)
}

func (s *offlineTestS3) objectKey(r *http.Request) (string, bool) {
	path := strings.TrimPrefix(r.URL.Path, "/"+s.bucket)
	if path == r.URL.Path {
		return "", false
	}
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return "", true
	}
	key, err := url.PathUnescape(path)
	return key, err == nil
}

func (s *offlineTestS3) serveList(w http.ResponseWriter, r *http.Request) {
	prefix := r.URL.Query().Get("prefix")
	start := 0
	if token := r.URL.Query().Get("continuation-token"); token != "" {
		var err error
		start, err = strconv.Atoi(token)
		if err != nil || start < 0 {
			s.writeError(w, http.StatusBadRequest, "InvalidArgument", "invalid continuation token")
			return
		}
	}
	maxKeys := 1000
	if value := r.URL.Query().Get("max-keys"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			maxKeys = parsed
		}
	}

	s.mu.Lock()
	if s.failListPrefix == prefix {
		s.mu.Unlock()
		s.writeError(w, http.StatusInternalServerError, "InternalError", "injected LIST failure")
		return
	}
	s.listPrefixes = append(s.listPrefixes, prefix)
	keys := make([]string, 0)
	for key := range s.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	s.mu.Unlock()
	sort.Strings(keys)

	if start > len(keys) {
		s.writeError(w, http.StatusBadRequest, "InvalidArgument", "continuation token out of range")
		return
	}
	end := start + maxKeys
	if end > len(keys) {
		end = len(keys)
	}
	truncated := end < len(keys)

	type listContent struct {
		Key          string `xml:"Key"`
		LastModified string `xml:"LastModified"`
		ETag         string `xml:"ETag"`
		Size         int    `xml:"Size"`
		StorageClass string `xml:"StorageClass"`
	}
	type listResult struct {
		XMLName               xml.Name      `xml:"ListBucketResult"`
		Name                  string        `xml:"Name"`
		Prefix                string        `xml:"Prefix"`
		KeyCount              int           `xml:"KeyCount"`
		MaxKeys               int           `xml:"MaxKeys"`
		IsTruncated           bool          `xml:"IsTruncated"`
		NextContinuationToken string        `xml:"NextContinuationToken,omitempty"`
		Contents              []listContent `xml:"Contents"`
	}
	result := listResult{
		Name:        s.bucket,
		Prefix:      prefix,
		KeyCount:    end - start,
		MaxKeys:     maxKeys,
		IsTruncated: truncated,
		Contents:    make([]listContent, 0, end-start),
	}
	if truncated {
		result.NextContinuationToken = strconv.Itoa(end)
	}
	s.mu.Lock()
	for _, key := range keys[start:end] {
		object := s.objects[key]
		result.Contents = append(result.Contents, listContent{
			Key:          key,
			LastModified: object.lastModified.Format(time.RFC3339),
			ETag:         fmt.Sprintf("%q", object.etag),
			Size:         len(object.body),
			StorageClass: "STANDARD",
		})
	}
	s.mu.Unlock()

	s.writeXML(w, http.StatusOK, result)
}

func (s *offlineTestS3) serveDeleteObjects(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Objects []struct {
			Key string `xml:"Key"`
		} `xml:"Object"`
	}
	body, err := io.ReadAll(r.Body)
	if err == nil {
		err = xml.Unmarshal(body, &request)
	}
	if err != nil || len(request.Objects) == 0 {
		s.writeError(w, http.StatusBadRequest, "MalformedXML", "invalid DeleteObjects request")
		return
	}

	keys := make([]string, 0, len(request.Objects))
	s.mu.Lock()
	for _, object := range request.Objects {
		keys = append(keys, object.Key)
		delete(s.objects, object.Key)
	}
	s.deleteRequests = append(s.deleteRequests, keys)
	s.mu.Unlock()

	type deleted struct {
		Key string `xml:"Key"`
	}
	type result struct {
		XMLName xml.Name  `xml:"DeleteResult"`
		Deleted []deleted `xml:"Deleted"`
	}
	deletedObjects := make([]deleted, 0, len(keys))
	for _, key := range keys {
		deletedObjects = append(deletedObjects, deleted{Key: key})
	}
	s.writeXML(w, http.StatusOK, result{Deleted: deletedObjects})
}

func (s *offlineTestS3) writeXML(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>`))
	_ = xml.NewEncoder(w).Encode(value)
}

func (s *offlineTestS3) writeError(w http.ResponseWriter, status int, code, message string) {
	type errorResponse struct {
		XMLName xml.Name `xml:"Error"`
		Code    string   `xml:"Code"`
		Message string   `xml:"Message"`
	}
	s.writeXML(w, status, errorResponse{Code: code, Message: message})
}

func addOfflineAlias(store *offlineTestS3, key, blobKey string, expiresAt time.Time) {
	record := aliasRecord{
		Version:     1,
		BlobKey:     blobKey,
		CreatedAt:   expiresAt.Add(-time.Hour).Format(time.RFC3339),
		ExpiresAt:   expiresAt.Format(time.RFC3339),
		ContentType: "application/octet-stream",
		Size:        1,
	}
	body, err := jsonMarshal(record)
	if err != nil {
		panic(err)
	}
	store.put(key, body)
}

func jsonMarshal(value any) ([]byte, error) {
	return json.Marshal(value)
}

func validOfflineBlobKey(seed byte) string {
	return "b/" + strings.Repeat(fmt.Sprintf("%x", seed&0x0f), 64)
}

func TestOfflineGCDryRunDoesNotDelete(t *testing.T) {
	store, server, client := newOfflineTestS3()
	defer server.Close()

	blobKey := validOfflineBlobKey('a')
	store.put(blobKey, []byte("orphan"))
	addOfflineAlias(store, "a/expired", blobKey, time.Now().UTC().Add(-time.Hour))

	stats, err := runOfflineTestGC(store, client, false)
	if err != nil {
		t.Fatalf("runOfflineGC dry-run: %v", err)
	}
	if stats.expiredAliases != 1 || stats.unreferencedBlobs != 1 {
		t.Fatalf("unexpected dry-run stats: %+v", stats)
	}
	if stats.unreferencedBytes != int64(len("orphan")) {
		t.Fatalf("unexpected reclaimable bytes: %d", stats.unreferencedBytes)
	}
	if store.deletedKeyCount() != 0 {
		t.Fatalf("dry-run issued DeleteObjects requests: %d", store.deletedKeyCount())
	}
	if !store.has("a/expired") || !store.has(blobKey) {
		t.Fatal("dry-run removed an object")
	}
}

func TestOfflineGCLiveAndExpiredAliases(t *testing.T) {
	store, server, client := newOfflineTestS3()
	defer server.Close()

	sharedBlob := validOfflineBlobKey('a')
	expiredOnlyBlob := validOfflineBlobKey('b')
	store.put(sharedBlob, []byte("shared"))
	store.put(expiredOnlyBlob, []byte("expired"))
	addOfflineAlias(store, "a/live", sharedBlob, time.Now().UTC().Add(time.Hour))
	addOfflineAlias(store, "a/expired-shared", sharedBlob, time.Now().UTC().Add(-time.Hour))
	addOfflineAlias(store, "a/expired-only", expiredOnlyBlob, time.Now().UTC().Add(-time.Hour))

	stats, err := runOfflineTestGC(store, client, true)
	if err != nil {
		t.Fatalf("runOfflineGC delete: %v", err)
	}
	if stats.liveAliases != 1 || stats.expiredAliases != 2 || stats.unreferencedBlobs != 1 {
		t.Fatalf("unexpected live/expired stats: %+v", stats)
	}
	if !store.has("a/live") || !store.has(sharedBlob) {
		t.Fatal("live alias or its blob was deleted")
	}
	if !store.has("a/expired-shared") || !store.has("a/expired-only") {
		t.Fatal("offline GC should leave alias objects for the regular alias cleaner")
	}
	if store.has(expiredOnlyBlob) {
		t.Fatal("blob referenced only by expired alias was not deleted")
	}
}

func TestOfflineGCExpiryGraceIsConsistentAcrossModes(t *testing.T) {
	now := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	grace := 15 * time.Minute

	for _, deleteMode := range []bool{false, true} {
		t.Run(fmt.Sprintf("delete=%t", deleteMode), func(t *testing.T) {
			store, server, client := newOfflineTestS3()
			defer server.Close()

			withinGraceBlob := validOfflineBlobKey('a')
			expiredBlob := validOfflineBlobKey('b')
			store.put(withinGraceBlob, []byte("within-grace"))
			store.put(expiredBlob, []byte("expired"))
			addOfflineAlias(store, "a/within-grace", withinGraceBlob, now.Add(-grace).Add(time.Second))
			addOfflineAlias(store, "a/expired-at-boundary", expiredBlob, now.Add(-grace))

			stats, err := runOfflineGCWithStore(client, store.bucket, now, offlineGCOptions{
				deleteMode:  deleteMode,
				expiryGrace: grace,
			})
			if err != nil {
				t.Fatalf("runOfflineGCWithStore: %v", err)
			}
			if stats.liveAliases != 1 || stats.expiredAliases != 1 || stats.unreferencedBlobs != 1 {
				t.Fatalf("unexpected grace stats: %+v", stats)
			}
			if !store.has(withinGraceBlob) {
				t.Fatal("blob whose alias is within expiry grace was deleted")
			}
			if got := store.has(expiredBlob); got == deleteMode {
				t.Fatalf("expired blob presence = %t, deleteMode = %t", got, deleteMode)
			}
		})
	}
}

func TestOfflineGCRejectsNegativeExpiryGraceBeforeScanning(t *testing.T) {
	store, server, client := newOfflineTestS3()
	defer server.Close()

	store.put(validOfflineBlobKey('a'), []byte("blob"))
	stats, err := runOfflineGCWithStore(client, store.bucket, time.Now().UTC(), offlineGCOptions{
		deleteMode:  true,
		expiryGrace: -time.Second,
	})
	if err == nil {
		t.Fatal("runOfflineGCWithStore accepted a negative expiry grace")
	}
	if stats.listRequests != 0 || store.deletedKeyCount() != 0 {
		t.Fatalf("invalid grace performed work: stats=%+v deletes=%d", stats, store.deletedKeyCount())
	}
}

func TestOfflineGCScanFailuresFailClosed(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*offlineTestS3)
	}{
		{
			name: "list failure",
			setup: func(store *offlineTestS3) {
				store.failListPrefix = "a/"
			},
		},
		{
			name: "blob list failure",
			setup: func(store *offlineTestS3) {
				store.failListPrefix = "b/"
			},
		},
		{
			name: "get failure",
			setup: func(store *offlineTestS3) {
				blobKey := validOfflineBlobKey('a')
				store.put(blobKey, []byte("blob"))
				addOfflineAlias(store, "a/bad-get", blobKey, time.Now().UTC().Add(time.Hour))
				store.failGetKey = "a/bad-get"
			},
		},
		{
			name: "malformed alias",
			setup: func(store *offlineTestS3) {
				store.put("a/malformed", []byte("not-json"))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, server, client := newOfflineTestS3()
			defer server.Close()
			tc.setup(store)

			if _, err := runOfflineTestGC(store, client, true); err == nil {
				t.Fatal("runOfflineGC unexpectedly succeeded")
			}
			if store.deletedKeyCount() != 0 {
				t.Fatalf("fail-closed scan issued deletes: %d", store.deletedKeyCount())
			}
		})
	}
}

func TestOfflineGCLiveReferenceMissingBlobFailsClosed(t *testing.T) {
	store, server, client := newOfflineTestS3()
	defer server.Close()

	addOfflineAlias(store, "a/missing", validOfflineBlobKey('a'), time.Now().UTC().Add(time.Hour))
	if _, err := runOfflineTestGC(store, client, true); err == nil {
		t.Fatal("runOfflineGC accepted an alias with a missing blob")
	}
	if store.deletedKeyCount() != 0 {
		t.Fatalf("missing live blob scan issued deletes: %d", store.deletedKeyCount())
	}
}

func TestOfflineGCAcceptsWorkerAliasContentType(t *testing.T) {
	store, server, client := newOfflineTestS3()
	defer server.Close()

	blobKey := validOfflineBlobKey('a')
	store.put(blobKey, []byte("live"))
	now := time.Now().UTC()
	workerAlias := map[string]any{
		"version":     1,
		"blobKey":     blobKey,
		"createdAt":   now.Add(-time.Hour).Format(time.RFC3339Nano),
		"expiresAt":   now.Add(time.Hour).Format(time.RFC3339Nano),
		"contentType": "application/x-worker; profile",
		"size":        4,
	}
	body, err := json.Marshal(workerAlias)
	if err != nil {
		t.Fatalf("marshal Worker alias: %v", err)
	}
	store.put("a/worker", body)

	stats, err := runOfflineTestGC(store, client, true)
	if err != nil {
		t.Fatalf("runOfflineGC with Worker alias: %v", err)
	}
	if stats.liveAliases != 1 || stats.unreferencedBlobs != 0 {
		t.Fatalf("unexpected Worker alias stats: %+v", stats)
	}
	if !store.has(blobKey) {
		t.Fatal("blob protected by Worker alias was deleted")
	}
}

func TestOfflineGCRejectsEmptyAliasContentType(t *testing.T) {
	store, server, client := newOfflineTestS3()
	defer server.Close()

	blobKey := validOfflineBlobKey('a')
	store.put(blobKey, []byte("live"))
	record := aliasRecord{
		Version:   1,
		BlobKey:   blobKey,
		CreatedAt: time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
		ExpiresAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		Size:      4,
	}
	body, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal alias: %v", err)
	}
	store.put("a/empty-content-type", body)

	if _, err := runOfflineTestGC(store, client, true); err == nil {
		t.Fatal("runOfflineGC accepted an empty alias content type")
	}
	if store.deletedKeyCount() != 0 {
		t.Fatalf("invalid alias scan issued deletes: %d", store.deletedKeyCount())
	}
}

func TestOfflineGCPaginatesAliasListing(t *testing.T) {
	store, server, client := newOfflineTestS3()
	defer server.Close()

	blobKey := validOfflineBlobKey('a')
	store.put(blobKey, []byte("blob"))
	for i := 0; i < 1001; i++ {
		addOfflineAlias(store, fmt.Sprintf("a/%04d", i), blobKey, time.Now().UTC().Add(time.Hour))
	}

	stats, err := runOfflineTestGC(store, client, false)
	if err != nil {
		t.Fatalf("runOfflineGC paginated dry-run: %v", err)
	}
	if stats.aliasesScanned != 1001 || stats.liveAliases != 1001 {
		t.Fatalf("unexpected alias stats: %+v", stats)
	}
	if stats.listRequests != 3 {
		t.Fatalf("expected two alias LISTs plus one blob LIST, got %d", stats.listRequests)
	}
	if stats.getRequests != 1001 {
		t.Fatalf("expected one GET per alias, got %d", stats.getRequests)
	}
}

func TestOfflineGCExternalMergeAndWorkDirectoryCleanup(t *testing.T) {
	store, server, client := newOfflineTestS3()
	defer server.Close()

	now := time.Now().UTC()
	for i := 0; i < 70; i++ {
		blobKey := fmt.Sprintf("b/%064x", i+1)
		store.put(blobKey, []byte("live"))
		addOfflineAlias(store, fmt.Sprintf("a/%04d", i), blobKey, now.Add(time.Hour))
		if i%10 == 0 {
			addOfflineAlias(store, fmt.Sprintf("a/duplicate-%04d", i), blobKey, now.Add(time.Hour))
		}
	}
	orphanKey := fmt.Sprintf("b/%064x", 1000)
	store.put(orphanKey, []byte("orphan"))
	workDir := t.TempDir()

	stats, err := runOfflineGCWithStore(client, store.bucket, now, offlineGCOptions{
		expiryGrace:      defaultOfflineGCExpiryGrace,
		workDir:          workDir,
		sortChunkEntries: 1,
	})
	if err != nil {
		t.Fatalf("runOfflineGCWithStore: %v", err)
	}
	if stats.liveAliases != 77 || stats.blobsScanned != 71 || stats.unreferencedBlobs != 1 {
		t.Fatalf("unexpected external merge stats: %+v", stats)
	}
	entries, err := os.ReadDir(workDir)
	if err != nil {
		t.Fatalf("read work directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary GC files were not cleaned up: %v", entries)
	}
}

func TestOfflineGCMissingLiveBlobAfterOrphanStillFailsBeforeDelete(t *testing.T) {
	store, server, client := newOfflineTestS3()
	defer server.Close()

	orphanKey := fmt.Sprintf("b/%064x", 1)
	missingKey := fmt.Sprintf("b/%064x", 2)
	store.put(orphanKey, []byte("orphan"))
	addOfflineAlias(store, "a/live-missing", missingKey, time.Now().UTC().Add(time.Hour))

	stats, err := runOfflineGCWithStore(client, store.bucket, time.Now().UTC(), offlineGCOptions{
		deleteMode:       true,
		expiryGrace:      defaultOfflineGCExpiryGrace,
		sortChunkEntries: 1,
	})
	if err == nil || !strings.Contains(err.Error(), missingKey) {
		t.Fatalf("missing live blob error = %v", err)
	}
	if stats.unreferencedBlobs != 1 || store.deletedKeyCount() != 0 || !store.has(orphanKey) {
		t.Fatalf("validation failure was not fail-closed: stats=%+v deletes=%d", stats, store.deletedKeyCount())
	}
}

func TestOfflineGCRejectsMalformedPagination(t *testing.T) {
	trueValue := true
	falseValue := false
	tests := []struct {
		name  string
		pages []*s3.ListObjectsV2Output
	}{
		{name: "nil page", pages: []*s3.ListObjectsV2Output{nil}},
		{name: "missing truncation flag", pages: []*s3.ListObjectsV2Output{{}}},
		{name: "missing next token", pages: []*s3.ListObjectsV2Output{{IsTruncated: &trueValue}}},
		{
			name: "repeated next token",
			pages: []*s3.ListObjectsV2Output{
				{IsTruncated: &trueValue, NextContinuationToken: aws.String("same")},
				{IsTruncated: &trueValue, NextContinuationToken: aws.String("same")},
			},
		},
		{
			name: "non-adjacent token loop",
			pages: []*s3.ListObjectsV2Output{
				{IsTruncated: &trueValue, NextContinuationToken: aws.String("A")},
				{IsTruncated: &trueValue, NextContinuationToken: aws.String("B")},
				{IsTruncated: &trueValue, NextContinuationToken: aws.String("A")},
			},
		},
		{
			name: "key outside prefix",
			pages: []*s3.ListObjectsV2Output{{
				IsTruncated: &falseValue,
				Contents:    []*s3.Object{{Key: aws.String("b/not-an-alias"), Size: aws.Int64(1)}},
			}},
		},
		{
			name: "keys out of order",
			pages: []*s3.ListObjectsV2Output{{
				IsTruncated: &falseValue,
				Contents: []*s3.Object{
					{Key: aws.String("a/z"), Size: aws.Int64(1)},
					{Key: aws.String("a/a"), Size: aws.Int64(1)},
				},
			}},
		},
		{
			name: "duplicate key",
			pages: []*s3.ListObjectsV2Output{{
				IsTruncated: &falseValue,
				Contents: []*s3.Object{
					{Key: aws.String("a/same"), Size: aws.Int64(1)},
					{Key: aws.String("a/same"), Size: aws.Int64(1)},
				},
			}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &scriptedMaintenanceStore{pages: tc.pages}
			var stats offlineGCStats
			if err := walkOfflineObjects(store, offlineTestBucket, "a/", t.TempDir(), &stats, func(offlineObject) error {
				return nil
			}); err == nil {
				t.Fatal("malformed pagination unexpectedly succeeded")
			}
			if store.listCalls != len(tc.pages) {
				t.Fatalf("LIST calls = %d, want %d", store.listCalls, len(tc.pages))
			}
			if stats.deleteObjectsRequests != 0 {
				t.Fatal("malformed pagination issued a delete")
			}
		})
	}
}

func successfulDeleteOutput(keys []string) *s3.DeleteObjectsOutput {
	output := &s3.DeleteObjectsOutput{Deleted: make([]*s3.DeletedObject, 0, len(keys))}
	for _, key := range keys {
		output.Deleted = append(output.Deleted, &s3.DeletedObject{Key: aws.String(key)})
	}
	return output
}

func deleteTestKeys(count int) []string {
	keys := make([]string, count)
	for i := range keys {
		keys[i] = fmt.Sprintf("b/%064x", i)
	}
	return keys
}

func TestOfflineGCDeleteObjectsSecondBatchFailureStopsLaterBatches(t *testing.T) {
	keys := deleteTestKeys(2001)
	secondBatch := successfulDeleteOutput(keys[1000:2000])
	secondBatch.Deleted = secondBatch.Deleted[1:]
	secondBatch.Errors = []*s3.Error{{
		Key: aws.String(keys[1000]), Code: aws.String("AccessDenied"), Message: aws.String("denied"),
	}}
	store := &scriptedMaintenanceStore{deleteOutputs: []*s3.DeleteObjectsOutput{
		successfulDeleteOutput(keys[:1000]), secondBatch,
	}}
	var stats offlineGCStats
	err := deleteOfflineObjects(store, offlineTestBucket, keys, &stats)
	if err == nil || !strings.Contains(err.Error(), keys[1000]) {
		t.Fatalf("second-batch error = %v", err)
	}
	if store.deleteCalls != 2 {
		t.Fatalf("DeleteObjects calls = %d, want 2", store.deleteCalls)
	}
	if stats.deleteAttempted != 2000 || stats.deleteConfirmed != 1999 || stats.deleteFailed != 1 || stats.deleteUnknown != 0 || stats.deleteUnstarted != 1 {
		t.Fatalf("unexpected delete stats: %+v", stats)
	}
}

func TestOfflineGCDeleteObjectsReportsAllPartialFailures(t *testing.T) {
	keys := deleteTestKeys(4)
	output := successfulDeleteOutput([]string{keys[0], keys[3]})
	output.Errors = []*s3.Error{
		{Key: aws.String(keys[1]), Code: aws.String("AccessDenied"), Message: aws.String("denied")},
		{Key: aws.String(keys[2]), Code: aws.String("ObjectLocked"), Message: aws.String("locked")},
	}
	store := &scriptedMaintenanceStore{deleteOutputs: []*s3.DeleteObjectsOutput{output}}
	var stats offlineGCStats
	err := deleteOfflineObjects(store, offlineTestBucket, keys, &stats)
	if err == nil || !strings.Contains(err.Error(), keys[1]) || !strings.Contains(err.Error(), keys[2]) {
		t.Fatalf("partial failure did not include every object error: %v", err)
	}
	if stats.deleteAttempted != 4 || stats.deleteConfirmed != 2 || stats.deleteFailed != 2 || stats.deleteUnknown != 0 || stats.deleteUnstarted != 0 {
		t.Fatalf("unexpected delete stats: %+v", stats)
	}
}

func TestOfflineGCDeleteObjectsRejectsOmittedKey(t *testing.T) {
	keys := deleteTestKeys(2)
	store := &scriptedMaintenanceStore{deleteOutputs: []*s3.DeleteObjectsOutput{successfulDeleteOutput(keys[:1])}}
	var stats offlineGCStats
	err := deleteOfflineObjects(store, offlineTestBucket, keys, &stats)
	if err == nil || !strings.Contains(err.Error(), keys[1]) {
		t.Fatalf("omitted-key error = %v", err)
	}
	if stats.deleteAttempted != 2 || stats.deleteConfirmed != 1 || stats.deleteFailed != 0 || stats.deleteUnknown != 1 || stats.deleteUnstarted != 0 {
		t.Fatalf("unexpected delete stats: %+v", stats)
	}
}

func TestOfflineGCDeleteObjectsRejectsDuplicateKeyResult(t *testing.T) {
	keys := deleteTestKeys(2)
	output := successfulDeleteOutput([]string{keys[0], keys[0], keys[1]})
	store := &scriptedMaintenanceStore{deleteOutputs: []*s3.DeleteObjectsOutput{output}}
	var stats offlineGCStats
	err := deleteOfflineObjects(store, offlineTestBucket, keys, &stats)
	if err == nil || !strings.Contains(err.Error(), keys[0]) {
		t.Fatalf("duplicate-key error = %v", err)
	}
	if stats.deleteAttempted != 2 || stats.deleteConfirmed != 1 || stats.deleteFailed != 0 || stats.deleteUnknown != 1 || stats.deleteUnstarted != 0 {
		t.Fatalf("unexpected delete stats: %+v", stats)
	}
}

func TestOfflineGCDeleteObjectsTransportFailureMarksBatchUnknown(t *testing.T) {
	keys := deleteTestKeys(1001)
	store := &scriptedMaintenanceStore{deleteErrs: []error{fmt.Errorf("injected network failure")}}
	var stats offlineGCStats
	if err := deleteOfflineObjects(store, offlineTestBucket, keys, &stats); err == nil {
		t.Fatal("network failure unexpectedly succeeded")
	}
	if store.deleteCalls != 1 || stats.deleteAttempted != 1000 || stats.deleteConfirmed != 0 || stats.deleteFailed != 0 || stats.deleteUnknown != 1000 || stats.deleteUnstarted != 1 {
		t.Fatalf("unexpected delete stats: calls=%d stats=%+v", store.deleteCalls, stats)
	}
}

func TestOfflineGCDeleteObjectsSuccessfulVerifiedResponses(t *testing.T) {
	keys := deleteTestKeys(1001)
	store := &scriptedMaintenanceStore{deleteOutputs: []*s3.DeleteObjectsOutput{
		successfulDeleteOutput(keys[:1000]), successfulDeleteOutput(keys[1000:]),
	}}
	var stats offlineGCStats
	if err := deleteOfflineObjects(store, offlineTestBucket, keys, &stats); err != nil {
		t.Fatalf("DeleteObjects success: %v", err)
	}
	if stats.deleteAttempted != 1001 || stats.deleteConfirmed != 1001 || stats.deleteFailed != 0 || stats.deleteUnknown != 0 || stats.deleteUnstarted != 0 {
		t.Fatalf("unexpected delete stats: %+v", stats)
	}
	for i, input := range store.deleteInputs {
		if input.Delete == nil || input.Delete.Quiet == nil || aws.BoolValue(input.Delete.Quiet) {
			t.Fatalf("DeleteObjects request %d did not explicitly request a non-quiet response", i+1)
		}
	}
}

func TestOfflineGCBatchesMoreThanThousandDeletes(t *testing.T) {
	store, server, client := newOfflineTestS3()
	defer server.Close()

	blobKeys := make([]string, 0, 1001)
	for i := 0; i < 1001; i++ {
		key := fmt.Sprintf("b/%064x", i)
		blobKeys = append(blobKeys, key)
		store.put(key, []byte("blob"))
	}

	stats, err := runOfflineTestGC(store, client, true)
	if err != nil {
		t.Fatalf("runOfflineGC batch delete: %v", err)
	}
	if stats.unreferencedBlobs != 1001 {
		t.Fatalf("unexpected unreferenced count: %+v", stats)
	}
	if stats.deleteObjectsRequests != 2 {
		t.Fatalf("expected two DeleteObjects requests, got %d", stats.deleteObjectsRequests)
	}
	if stats.deleteAttempted != 1001 || stats.deleteConfirmed != 1001 || stats.deleteFailed != 0 || stats.deleteUnknown != 0 || stats.deleteUnstarted != 0 {
		t.Fatalf("unexpected delete accounting: %+v", stats)
	}
	for _, key := range blobKeys {
		if store.has(key) {
			t.Fatalf("blob %s survived batch delete", key)
		}
	}
}

func TestRunGCCommandValidatesArguments(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"--unknown"},
		{"--offline", "--unknown"},
	} {
		if got := runGCCommand(args); got != 2 {
			t.Errorf("runGCCommand(%v) = %d, want 2", args, got)
		}
	}
}

func TestParseOfflineGCArgsDefaultsToDryRun(t *testing.T) {
	tests := []struct {
		args            []string
		wantDelete      bool
		wantGrace       time.Duration
		wantWorkDir     string
		wantSortEntries int
		wantError       bool
	}{
		{args: []string{"--offline"}, wantGrace: defaultOfflineGCExpiryGrace},
		{args: []string{"--offline", "--dry-run"}, wantGrace: defaultOfflineGCExpiryGrace},
		{args: []string{"--offline", "--delete"}, wantDelete: true, wantGrace: defaultOfflineGCExpiryGrace},
		{args: []string{"--offline", "--expiry-grace", "30m"}, wantGrace: 30 * time.Minute},
		{args: []string{"--offline", "--delete", "--expiry-grace=1h"}, wantDelete: true, wantGrace: time.Hour},
		{args: []string{"--offline", "--expiry-grace=0"}},
		{args: []string{"--offline", "--work-dir", "/tmp/gc", "--sort-chunk-entries", "17"}, wantGrace: defaultOfflineGCExpiryGrace, wantWorkDir: "/tmp/gc", wantSortEntries: 17},
		{args: []string{"--offline", "--work-dir=/tmp/gc", "--sort-chunk-entries=23"}, wantGrace: defaultOfflineGCExpiryGrace, wantWorkDir: "/tmp/gc", wantSortEntries: 23},
		{args: []string{"--delete"}, wantError: true},
		{args: []string{"--offline", "--dry-run", "--delete"}, wantError: true},
		{args: []string{"--offline", "--delete", "--dry-run"}, wantError: true},
		{args: []string{"--offline", "--expiry-grace"}, wantError: true},
		{args: []string{"--offline", "--expiry-grace="}, wantError: true},
		{args: []string{"--offline", "--expiry-grace", "soon"}, wantError: true},
		{args: []string{"--offline", "--expiry-grace=-1s"}, wantError: true},
		{args: []string{"--offline", "--work-dir="}, wantError: true},
		{args: []string{"--offline", "--work-dir", "--delete"}, wantError: true},
		{args: []string{"--offline", "--sort-chunk-entries", "0"}, wantError: true},
		{args: []string{"--offline", "--sort-chunk-entries=-1"}, wantError: true},
		{args: []string{"--offline", "--sort-chunk-entries=100001"}, wantError: true},
	}

	for _, tc := range tests {
		got, err := parseOfflineGCArgs(tc.args)
		if (err != nil) != tc.wantError {
			t.Errorf("parseOfflineGCArgs(%v) error = %v, wantError=%t", tc.args, err, tc.wantError)
		}
		if got.deleteMode != tc.wantDelete {
			t.Errorf("parseOfflineGCArgs(%v) delete = %t, want %t", tc.args, got.deleteMode, tc.wantDelete)
		}
		if !tc.wantError && got.expiryGrace != tc.wantGrace {
			t.Errorf("parseOfflineGCArgs(%v) grace = %s, want %s", tc.args, got.expiryGrace, tc.wantGrace)
		}
		if !tc.wantError {
			wantSortEntries := tc.wantSortEntries
			if wantSortEntries == 0 {
				wantSortEntries = defaultOfflineGCSortChunkEntries
			}
			if got.workDir != tc.wantWorkDir || got.sortChunkEntries != wantSortEntries {
				t.Errorf("parseOfflineGCArgs(%v) workDir=%q sortChunkEntries=%d, want %q/%d", tc.args, got.workDir, got.sortChunkEntries, tc.wantWorkDir, wantSortEntries)
			}
		}
	}
}
