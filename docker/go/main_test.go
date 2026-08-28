package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

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
	pages     []*s3.ListObjectsV2Output
	listCalls int
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

func (*scriptedMaintenanceStore) DeleteObjects(*s3.DeleteObjectsInput) (*s3.DeleteObjectsOutput, error) {
	return nil, fmt.Errorf("unexpected DeleteObjects request")
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
	return runOfflineGCWithStore(client, store.bucket, time.Now().UTC(), deleteMode)
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
			name: "key outside prefix",
			pages: []*s3.ListObjectsV2Output{{
				IsTruncated: &falseValue,
				Contents:    []*s3.Object{{Key: aws.String("b/not-an-alias"), Size: aws.Int64(1)}},
			}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &scriptedMaintenanceStore{pages: tc.pages}
			var stats offlineGCStats
			if _, err := listOfflineObjects(store, offlineTestBucket, "a/", &stats); err == nil {
				t.Fatal("malformed pagination unexpectedly succeeded")
			}
			if stats.deleteObjectsRequests != 0 {
				t.Fatal("malformed pagination issued a delete")
			}
		})
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
		args       []string
		wantDelete bool
		wantError  bool
	}{
		{args: []string{"--offline"}},
		{args: []string{"--offline", "--dry-run"}},
		{args: []string{"--offline", "--delete"}, wantDelete: true},
		{args: []string{"--delete"}, wantError: true},
		{args: []string{"--offline", "--dry-run", "--delete"}, wantError: true},
		{args: []string{"--offline", "--delete", "--dry-run"}, wantError: true},
	}

	for _, tc := range tests {
		gotDelete, err := parseOfflineGCArgs(tc.args)
		if (err != nil) != tc.wantError {
			t.Errorf("parseOfflineGCArgs(%v) error = %v, wantError=%t", tc.args, err, tc.wantError)
		}
		if gotDelete != tc.wantDelete {
			t.Errorf("parseOfflineGCArgs(%v) delete = %t, want %t", tc.args, gotDelete, tc.wantDelete)
		}
	}
}
