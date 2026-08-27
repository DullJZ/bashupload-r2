package main

import (
	"bytes"
	"crypto/hmac"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/gabriel-vasile/mimetype"
)

type configResponse struct {
	MaxAgeForMultiDownload int64 `json:"maxAgeForMultiDownload"`
	MaxUploadSize          int64 `json:"maxUploadSize"`
	MaxAge                 int64 `json:"maxAge"`
	NeedPassword           bool  `json:"needPassword"`
	EnableDedup            bool  `json:"enableDedup"`
}

type aliasRecord struct {
	Version     int    `json:"version"`
	BlobKey     string `json:"blobKey"`
	CreatedAt   string `json:"createdAt"`
	ExpiresAt   string `json:"expiresAt"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
}

var (
	s3Client                *s3.S3
	uploader                *s3manager.Uploader
	bucketName              string
	maxUploadSize           int64
	maxAge                  int64
	maxAgeForMultiDownload  int64
	enableShortURL          bool
	allowLifetimeOverMaxAge bool
	password                string
	shortURLService         string
	port                    string
	disableCleanup          bool
	uploadRateLimit         int64
	uploadRateLimitWindow   int64
	trustProxyHeaders       bool
	uploadLimiter           *rateLimiter
	enableDedup             bool
	dedupSecret             string
)

//go:embed public/*
var embeddedStaticFiles embed.FS

func init() {
	rand.Seed(time.Now().UnixNano())

	// 读取环境变量
	accountID := os.Getenv("R2_ACCOUNT_ID")
	accessKeyID := os.Getenv("R2_ACCESS_KEY_ID")
	secretAccessKey := os.Getenv("R2_SECRET_ACCESS_KEY")
	bucketName = os.Getenv("R2_BUCKET_NAME")

	maxUploadSize = parseInt64(os.Getenv("MAX_UPLOAD_SIZE"), 5368709120)
	maxAge = parseInt64(os.Getenv("MAX_AGE"), 3600)
	maxAgeForMultiDownload = parseInt64(os.Getenv("MAX_AGE_FOR_MULTIDOWNLOAD"), 86400)
	enableShortURL = os.Getenv("ENABLE_SHORT_URL") == "true"
	allowLifetimeOverMaxAge = os.Getenv("ALLOW_LIFETIME_OVER_MAX_AGE") == "true"
	enableDedup = os.Getenv("ENABLE_DEDUP") != "false"
	dedupSecret = os.Getenv("DEDUP_SECRET")
	password = os.Getenv("PASSWORD")
	shortURLService = os.Getenv("SHORT_URL_SERVICE")
	if shortURLService == "" {
		shortURLService = "https://suosuo.de/short"
	}
	port = os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	if envNoCleanup := os.Getenv("NO_CLEANUP"); envNoCleanup != "" {
		disableCleanup = envNoCleanup == "1" || strings.EqualFold(envNoCleanup, "true")
	}

	// 上传限流配置（0 = 禁用）
	uploadRateLimit = parseInt64(os.Getenv("UPLOAD_RATE_LIMIT"), 10)
	uploadRateLimitWindow = parseInt64(os.Getenv("UPLOAD_RATE_LIMIT_WINDOW"), 60)
	// 裸跑无反代时应设为 false，防止伪造代理头绕过限流
	trustProxyHeaders = !strings.EqualFold(os.Getenv("TRUST_PROXY_HEADERS"), "false")
	if uploadRateLimit > 0 && uploadRateLimitWindow > 0 {
		uploadLimiter = newRateLimiter(int(uploadRateLimit), time.Duration(uploadRateLimitWindow)*time.Second)
	}

	// 配置 S3 客户端
	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountID)
	sess := session.Must(session.NewSession(&aws.Config{
		Region:      aws.String("auto"),
		Endpoint:    aws.String(endpoint),
		Credentials: credentials.NewStaticCredentials(accessKeyID, secretAccessKey, ""),
	}))

	s3Client = s3.New(sess)
	uploader = s3manager.NewUploaderWithClient(s3Client)

	log.Printf("BashUpload Go server initialized")
	log.Printf("R2 Bucket: %s", bucketName)
	log.Printf("Max upload size: %s", formatBytes(maxUploadSize))
	log.Printf("Max age: %ds", maxAge)
	if uploadLimiter != nil {
		log.Printf("Upload rate limit: %d requests per %ds per IP", uploadRateLimit, uploadRateLimitWindow)
	} else {
		log.Printf("Upload rate limit: disabled")
	}
	if enableDedup && dedupSecret == "" {
		log.Printf("WARNING: ENABLE_DEDUP=true but DEDUP_SECRET is empty; expiration deduplication is disabled")
	}
	log.Printf("Expiration-mode hash deduplication: %t", enableDedup && dedupSecret != "")
}

func main() {
	// 启动定时清理任务
	if disableCleanup {
		log.Printf("Scheduled cleanup disabled via NO_CLEANUP")
	} else {
		go func() {
			// 启动后10秒执行第一次清理
			time.Sleep(10 * time.Second)
			cleanupExpiredFiles()

			// 每5分钟清理一次
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()

			for range ticker.C {
				cleanupExpiredFiles()
			}
		}()
	}

	// 设置路由
	http.HandleFunc("/", handleRequest)
	http.HandleFunc("/api/config", handleConfig)
	http.HandleFunc("/short", handleShort)
	http.HandleFunc("/short/", handleShort)

	log.Printf("Server starting on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}

func handleConfig(w http.ResponseWriter, r *http.Request) {
	config := configResponse{
		MaxAgeForMultiDownload: maxAgeForMultiDownload,
		MaxUploadSize:          maxUploadSize,
		MaxAge:                 maxAge,
		NeedPassword:           password != "",
		EnableDedup:            enableDedup && dedupSecret != "",
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	json.NewEncoder(w).Encode(config)
}

func handleShort(w http.ResponseWriter, r *http.Request) {
	if r.Method == "PUT" || r.Method == "POST" {
		handleUpload(w, r, true)
	} else {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func handleRequest(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	switch r.Method {
	case "GET":
		if path == "/" {
			handleRoot(w, r)
		} else {
			// 静态文件或下载文件
			handleGetFile(w, r)
		}
	case "PUT", "POST":
		handleUpload(w, r, false)
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	userAgent := r.Header.Get("User-Agent")
	if strings.Contains(strings.ToLower(userAgent), "curl") {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, `bashupload.app - 一次性文件分享服务 | One-time File Sharing Service

使用方法 Usage:
  curl bashupload.app -T file.txt                    # 上传文件 / Upload file
  curl bashupload.app -d "text content"              # 上传文本 / Upload text (saved as .txt)
  curl bashupload.app/short -T file.txt              # 返回短链接 / Short URL
  curl -H "X-Expiration-Seconds: 3600" bashupload.app -T file.txt   # 设置有效期 / Set expiration time

特性 Features:
  • 文件只能下载一次 / Files can only be downloaded once (默认 default)
  • 可以设置有效期 / Can set expiration time for multiple downloads
  • 下载后自动删除 / Auto-delete after download or expiration
  • 保护隐私安全 / Privacy protection

有效期示例 Expiration Examples:
  • 3600 秒 (1小时) / 3600s (1 hour)
  • 7200 秒 (2小时) / 7200s (2 hours)
  • 86400 秒 (24小时) / 86400s (24 hours)
`)
	} else {
		http.Redirect(w, r, "/index.html", http.StatusFound)
	}
}

func handleGetFile(w http.ResponseWriter, r *http.Request) {
	fileName := strings.TrimPrefix(r.URL.Path, "/")

	// 静态文件处理
	staticFiles := []string{"index.html", "style.css", "upload.js"}
	for _, sf := range staticFiles {
		if fileName == sf {
			serveStaticFile(w, r, fileName)
			return
		}
	}

	// 密码检查
	if password != "" && !checkPassword(r) {
		w.Header().Set("WWW-Authenticate", `Basic realm="Password Required"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if strings.HasPrefix(fileName, "a/") {
		downloadAlias(w, r, fileName)
		return
	}
	if strings.HasPrefix(fileName, "b/") || strings.HasPrefix(fileName, "t/") {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	// 从 R2 下载文件
	downloadFile(w, r, fileName)
}

func downloadAlias(w http.ResponseWriter, r *http.Request, aliasKey string) {
	record, err := getAliasRecord(aliasKey)
	if err != nil {
		if isObjectNotFound(err) {
			http.Error(w, "File not found", http.StatusNotFound)
		} else {
			log.Printf("Alias lookup failed alias=%s err=%v", aliasKey, err)
			http.Error(w, "Error downloading file", http.StatusInternalServerError)
		}
		return
	}

	expiresAt, err := time.Parse(time.RFC3339, record.ExpiresAt)
	if err != nil || time.Now().After(expiresAt) {
		deleteObject(aliasKey)
		http.Error(w, "File not found (expired)", http.StatusNotFound)
		return
	}
	if !strings.HasPrefix(record.BlobKey, "b/") {
		log.Printf("Invalid alias target alias=%s target=%q", aliasKey, record.BlobKey)
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	getOutput, err := s3Client.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(record.BlobKey),
	})
	if err != nil {
		if isObjectNotFound(err) {
			http.Error(w, "File not found", http.StatusNotFound)
		} else {
			http.Error(w, "Error downloading file", http.StatusInternalServerError)
		}
		return
	}
	defer getOutput.Body.Close()

	contentType := record.ContentType
	if _, _, err := mime.ParseMediaType(contentType); err != nil {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Expiration-Download", "true")
	w.Header().Set("X-Expiration-Time", record.ExpiresAt)
	if record.Size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(record.Size, 10))
	}
	if _, err := io.Copy(w, getOutput.Body); err != nil {
		log.Printf("Alias download failed alias=%s blob=%s err=%v", aliasKey, record.BlobKey, err)
	}
}

func serveStaticFile(w http.ResponseWriter, r *http.Request, fileName string) {
	filePath := "public/" + fileName
	data, err := embeddedStaticFiles.ReadFile(filePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	mtype := mimetype.Lookup(fileName)
	if mtype != nil {
		w.Header().Set("Content-Type", mtype.String())
	}

	reader := bytes.NewReader(data)
	http.ServeContent(w, r, fileName, time.Now(), reader)
}

func downloadFile(w http.ResponseWriter, r *http.Request, fileName string) {
	// 获取文件元数据
	headInput := &s3.HeadObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(fileName),
	}

	headOutput, err := s3Client.HeadObject(headInput)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	// 检查是否是一次性下载
	isOneTime := true
	if headOutput.Metadata["Onetime"] != nil {
		isOneTime = *headOutput.Metadata["Onetime"] == "true"
	}

	// 检查过期时间
	if headOutput.Metadata["Expirationtime"] != nil {
		expirationTime, err := time.Parse(time.RFC3339, *headOutput.Metadata["Expirationtime"])
		if err == nil {
			lastModified := time.Now()
			if headOutput.LastModified != nil {
				lastModified = *headOutput.LastModified
			}
			expirationTime = capExpirationTime(expirationTime, headOutput.Metadata, lastModified)
		}
		if err == nil && time.Now().After(expirationTime) {
			// 文件已过期，删除
			deleteInput := &s3.DeleteObjectInput{
				Bucket: aws.String(bucketName),
				Key:    aws.String(fileName),
			}
			s3Client.DeleteObject(deleteInput)
			log.Printf("[Expired Download] Deleted expired file: %s", fileName)
			http.Error(w, "File not found (expired)", http.StatusNotFound)
			return
		}
	}

	// 获取文件内容
	getInput := &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(fileName),
	}

	getOutput, err := s3Client.GetObject(getInput)
	if err != nil {
		http.Error(w, "Error downloading file", http.StatusInternalServerError)
		return
	}
	defer getOutput.Body.Close()

	// 设置响应头
	contentType := "application/octet-stream"
	if strings.HasPrefix(fileName, "c/") && headOutput.ContentType != nil && *headOutput.ContentType != "" {
		contentType = *headOutput.ContentType
	} else if mtype := mimetype.Lookup(fileName); mtype != nil {
		contentType = mtype.String()
	} else if headOutput.ContentType != nil {
		contentType = *headOutput.ContentType
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	if isOneTime {
		w.Header().Set("X-One-Time-Download", "true")
	} else {
		w.Header().Set("X-Expiration-Download", "true")
		if headOutput.Metadata["Expirationtime"] != nil {
			w.Header().Set("X-Expiration-Time", *headOutput.Metadata["Expirationtime"])
		}
	}

	// 流式传输文件
	io.Copy(w, getOutput.Body)

	// 一次性下载模式：异步删除文件
	if isOneTime {
		go func() {
			time.Sleep(100 * time.Millisecond)
			deleteInput := &s3.DeleteObjectInput{
				Bucket: aws.String(bucketName),
				Key:    aws.String(fileName),
			}
			_, err := s3Client.DeleteObject(deleteInput)
			if err != nil {
				log.Printf("[One-Time Download] Failed to delete file %s: %v", fileName, err)
			} else {
				log.Printf("[One-Time Download] Deleted file: %s", fileName)
			}
		}()
	}
}

func handleUpload(w http.ResponseWriter, r *http.Request, forceShortURL bool) {
	// 上传限流：单 IP 固定窗口计数
	if uploadLimiter != nil {
		clientIP := getClientIP(r)
		if allowed, retryAfter := uploadLimiter.Allow(clientIP); !allowed {
			log.Printf("[Rate Limit] Blocked upload from ip=%s", clientIP)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprintf(w, "Upload rate limit exceeded, please retry after %d seconds.\n上传过于频繁，请在 %d 秒后重试。\n", retryAfter, retryAfter)
			return
		}
	}

	// 密码检查
	if password != "" && !checkPassword(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	defer r.Body.Close()

	// 检查文件大小
	contentLength := r.ContentLength
	if contentLength > maxUploadSize {
		http.Error(w, fmt.Sprintf("Upload failed: file too large. Max size is %s.", formatBytes(maxUploadSize)), http.StatusRequestEntityTooLarge)
		return
	}
	limitedBody := http.MaxBytesReader(w, r.Body, maxUploadSize)
	declaredSHA := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Content-SHA256")))
	if declaredSHA != "" && !isValidSHA256Hex(declaredSHA) {
		http.Error(w, "Invalid X-Content-SHA256; expected 64 hexadecimal characters.", http.StatusBadRequest)
		return
	}

	// 获取过期时间
	expirationHeader := r.Header.Get("X-Expiration-Seconds")
	var expirationTime *time.Time
	var expirationSeconds int64
	hasExpiration := false
	expirationLimited := false

	if expirationHeader != "" {
		expSec, err := strconv.ParseInt(expirationHeader, 10, 64)
		if err == nil && expSec > 0 {
			hasExpiration = true
			expirationSeconds = expSec
			// 服务端强制限制有效期上限，防止绕过前端设置超长有效期
			if !allowLifetimeOverMaxAge && expirationSeconds > maxAgeForMultiDownload {
				expirationSeconds = maxAgeForMultiDownload
				expirationLimited = true
			}
			expTime := time.Now().Add(time.Duration(expirationSeconds) * time.Second)
			expirationTime = &expTime
		}
	}

	// 生成文件名
	randomID := generateRandomID()
	contentType := r.Header.Get("Content-Type")

	if contentType == "" {
		contentType = "application/octet-stream"
	}

	var ext string
	// 如果是 POST 请求（curl -d），强制使用 .txt 扩展名和 text/plain content-type
	if r.Method == "POST" {
		contentType = "text/plain; charset=utf-8"
		ext = ".txt"
	} else {
		// PUT 请求：使用 mimetype 根据 Content-Type 获取扩展名
		baseContentType := sanitizeContentType(contentType)
		ext = determineExtension(baseContentType)
		if ext == "" {
			ext = determineExtension(contentType)
		}
		if ext == "" && baseContentType == "application/octet-stream" {
			ext = ".bin"
		}
		if baseContentType != "" {
			contentType = baseContentType
		}
	}

	fileName := randomID + ext

	// 准备元数据
	metadata := map[string]*string{
		"Uploadtime": aws.String(time.Now().Format(time.RFC3339)),
	}

	if hasExpiration {
		metadata["Onetime"] = aws.String("false")
		metadata["Expirationtime"] = aws.String(expirationTime.Format(time.RFC3339))
		metadata["Expirationseconds"] = aws.String(strconv.FormatInt(expirationSeconds, 10))
	} else {
		metadata["Onetime"] = aws.String("true")
	}

	if enableDedup && dedupSecret != "" && hasExpiration {
		handleDedupUpload(w, r, limitedBody, declaredSHA, contentType, metadata, expirationTime, expirationSeconds, expirationLimited, forceShortURL, contentLength)
		return
	}

	// 上传到 R2 (使用流式上传，不需要将整个文件加载到内存)
	counter := &byteCounter{}
	hasher := sha256.New()
	bodyReader := io.TeeReader(limitedBody, io.MultiWriter(counter, hasher))

	uploadInput := &s3manager.UploadInput{
		Bucket:      aws.String(bucketName),
		Key:         aws.String(fileName),
		Body:        bodyReader,
		ContentType: aws.String(contentType),
		Metadata:    metadata,
	}

	_, err := uploader.Upload(uploadInput)
	if err != nil {
		if isRequestBodyTooLarge(err) {
			http.Error(w, fmt.Sprintf("Upload failed: file too large. Max size is %s.", formatBytes(maxUploadSize)), http.StatusRequestEntityTooLarge)
			return
		}

		log.Printf("Upload error: %v", err)
		http.Error(w, fmt.Sprintf("Upload failed: %v", err), http.StatusInternalServerError)
		return
	}
	if declaredSHA != "" && hex.EncodeToString(hasher.Sum(nil)) != declaredSHA {
		deleteObject(fileName)
		log.Printf("[Hash Mismatch] key=%s", fileName)
		http.Error(w, "SHA-256 mismatch; uploaded content does not match X-Content-SHA256.", http.StatusConflict)
		return
	}

	uploadedBytes := counter.Total()
	if uploadedBytes == 0 && contentLength > 0 {
		uploadedBytes = contentLength
	}
	clientIP := getClientIP(r)
	log.Printf("[Upload] key=%s size=%s ip=%s oneTime=%t", fileName, formatBytes(uploadedBytes), clientIP, !hasExpiration)

	// 生成文件 URL
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	fileURL := fmt.Sprintf("%s://%s/%s", scheme, r.Host, fileName)

	// 生成短链接
	if forceShortURL || enableShortURL {
		shortURL, err := generateShortURL(fileURL)
		if err == nil && shortURL != "" {
			fileURL = shortURL
			log.Printf("Generated short URL: %s", fileURL)
		} else if err != nil {
			log.Printf("Failed to generate short URL: %v", err)
		}
	}

	// 生成响应文本
	var responseText string
	if hasExpiration {
		hours := expirationSeconds / 3600
		minutes := (expirationSeconds % 3600) / 60
		var expirationString string
		if hours > 0 {
			expirationString = fmt.Sprintf("%d小时", hours)
			if minutes > 0 {
				expirationString += fmt.Sprintf("%d分钟", minutes)
			}
		} else {
			expirationString = fmt.Sprintf("%d分钟", minutes)
		}
		responseText = fmt.Sprintf("\n\n%s\n\n🕐 注意：此文件将在 %s 后过期，期间可以多次下载。\n   Note: This file will expire after %s and can be downloaded multiple times.\n", fileURL, expirationString, expirationString)
		if expirationLimited {
			responseText += fmt.Sprintf("⚠️  请求的有效期超过服务器上限，已调整为 %s。\n   Requested expiration exceeded the server limit and was reduced to %s.\n", expirationString, expirationString)
		}
	} else {
		responseText = fmt.Sprintf("\n\n%s\n\n⚠️  注意：此文件只能下载一次，下载后将自动删除！\n   Note: This file can only be downloaded once!\n", fileURL)
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if hasExpiration {
		w.Header().Set("X-One-Time-Upload", "false")
	} else {
		w.Header().Set("X-One-Time-Upload", "true")
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, responseText)
}

func handleDedupUpload(w http.ResponseWriter, r *http.Request, body io.Reader, declaredSHA, contentType string, metadata map[string]*string, requestedExpiration *time.Time, expirationSeconds int64, expirationLimited bool, forceShortURL bool, contentLength int64) {
	tempKey, err := tempObjectKey()
	if err != nil {
		http.Error(w, "Upload failed: could not generate a secure temporary key.", http.StatusInternalServerError)
		return
	}
	uploadedBytes, actualSHA, ok := uploadAndHash(w, body, tempKey, contentType, metadata, contentLength)
	if !ok {
		deleteObject(tempKey)
		return
	}
	if declaredSHA != "" && actualSHA != declaredSHA {
		deleteObject(tempKey)
		log.Printf("[Dedup Hash Mismatch] temp=%s", tempKey)
		http.Error(w, "SHA-256 mismatch; uploaded content does not match X-Content-SHA256.", http.StatusConflict)
		return
	}

	blobKey, err := contentHashKey(actualSHA)
	if err != nil {
		deleteObject(tempKey)
		http.Error(w, "Upload failed: secure deduplication is not configured.", http.StatusInternalServerError)
		return
	}
	dedupHit := false
	if _, err := headObject(blobKey); err == nil {
		dedupHit = true
		deleteObject(tempKey)
	} else if !isObjectNotFound(err) {
		deleteObject(tempKey)
		http.Error(w, fmt.Sprintf("Upload failed: %v", err), http.StatusInternalServerError)
		return
	} else {
		blobMetadata := map[string]*string{
			"Uploadtime": aws.String(time.Now().UTC().Format(time.RFC3339)),
		}
		if err := copyObject(tempKey, blobKey, "application/octet-stream", blobMetadata); err != nil {
			deleteObject(tempKey)
			http.Error(w, fmt.Sprintf("Upload failed: %v", err), http.StatusInternalServerError)
			return
		}
		deleteObject(tempKey)
	}

	aliasKey, err := createAliasRecord(blobKey, *requestedExpiration, contentType, uploadedBytes)
	if err != nil {
		http.Error(w, fmt.Sprintf("Upload failed: could not create download alias: %v", err), http.StatusInternalServerError)
		return
	}
	log.Printf("[Dedup Upload] alias=%s blob=%s size=%s ip=%s hit=%t", aliasKey, blobKey, formatBytes(uploadedBytes), getClientIP(r), dedupHit)
	writeDedupUploadResponse(w, r, aliasKey, expirationSeconds, expirationLimited, forceShortURL, dedupHit)
}

func uploadAndHash(w http.ResponseWriter, body io.Reader, key, contentType string, metadata map[string]*string, contentLength int64) (int64, string, bool) {
	counter := &byteCounter{}
	h := sha256.New()
	bodyReader := io.TeeReader(body, io.MultiWriter(counter, h))

	_, err := uploader.Upload(&s3manager.UploadInput{
		Bucket:      aws.String(bucketName),
		Key:         aws.String(key),
		Body:        bodyReader,
		ContentType: aws.String(contentType),
		Metadata:    metadata,
	})
	if err != nil {
		if isRequestBodyTooLarge(err) {
			http.Error(w, fmt.Sprintf("Upload failed: file too large. Max size is %s.", formatBytes(maxUploadSize)), http.StatusRequestEntityTooLarge)
			return 0, "", false
		}
		log.Printf("Upload error: %v", err)
		http.Error(w, fmt.Sprintf("Upload failed: %v", err), http.StatusInternalServerError)
		return 0, "", false
	}

	uploadedBytes := counter.Total()
	if uploadedBytes == 0 && contentLength > 0 {
		uploadedBytes = contentLength
	}
	return uploadedBytes, hex.EncodeToString(h.Sum(nil)), true
}

func writeDedupUploadResponse(w http.ResponseWriter, r *http.Request, key string, expirationSeconds int64, expirationLimited bool, forceShortURL bool, hit bool) {
	fileURL := absoluteFileURL(r, key)
	if forceShortURL || enableShortURL {
		shortURL, err := generateShortURL(fileURL)
		if err == nil && shortURL != "" {
			fileURL = shortURL
			log.Printf("Generated short URL: %s", fileURL)
		} else if err != nil {
			log.Printf("Failed to generate short URL: %v", err)
		}
	}

	expirationString := formatExpirationDuration(expirationSeconds)
	statusLine := "♻️  检测到相同文件，已复用现有存储对象。\n   Deduplication hit: reused the existing stored file.\n"
	if !hit {
		statusLine = "✅ 文件已按内容哈希保存，后续相同文件可复用。\n   File saved by content hash; matching future uploads can be reused.\n"
	}

	responseText := fmt.Sprintf("\n\n%s\n\n%s🕐 注意：此文件将在 %s 后过期，期间可以多次下载。\n   Note: This file will expire after %s and can be downloaded multiple times.\n", fileURL, statusLine, expirationString, expirationString)
	if expirationLimited {
		responseText += fmt.Sprintf("⚠️  请求的有效期超过服务器上限，已调整为 %s。\n   Requested expiration exceeded the server limit and was reduced to %s.\n", expirationString, expirationString)
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-One-Time-Upload", "false")
	w.Header().Set("X-Dedup-Hit", strconv.FormatBool(hit))
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, responseText)
}

func generateShortURL(longURL string) (string, error) {
	base64URL := base64.StdEncoding.EncodeToString([]byte(longURL))
	data := url.Values{}
	data.Set("longUrl", base64URL)

	resp, err := http.PostForm(shortURLService, data)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("short URL service returned status %d", resp.StatusCode)
	}

	var result struct {
		Code     int    `json:"Code"`
		ShortURL string `json:"ShortUrl"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if result.Code == 1 && result.ShortURL != "" {
		return result.ShortURL, nil
	}

	return "", fmt.Errorf("invalid response from short URL service")
}

func cleanupExpiredFiles() {
	now := time.Now()
	log.Printf("[Scheduled Task] Start cleaning expired files, MAX_AGE: %ds", maxAge)

	deletedCount := 0
	checkedCount := 0
	var cleanupWG sync.WaitGroup
	var countMu sync.Mutex
	incrementDeleted := func() {
		countMu.Lock()
		deletedCount++
		countMu.Unlock()
	}

	listInput := &s3.ListObjectsV2Input{
		Bucket:  aws.String(bucketName),
		MaxKeys: aws.Int64(1000),
	}

	err := s3Client.ListObjectsV2Pages(listInput, func(page *s3.ListObjectsV2Output, lastPage bool) bool {
		for _, obj := range page.Contents {
			checkedCount++
			if obj.Key == nil {
				continue
			}
			if strings.HasPrefix(*obj.Key, "b/") {
				// Blobs are shared and immutable. Reference-aware GC is not implemented yet.
				continue
			}
			if strings.HasPrefix(*obj.Key, "t/") {
				if obj.LastModified != nil && now.Sub(*obj.LastModified) > time.Hour {
					deleteInput := &s3.DeleteObjectInput{
						Bucket: aws.String(bucketName),
						Key:    obj.Key,
					}
					if _, err := s3Client.DeleteObject(deleteInput); err == nil {
						log.Printf("[Scheduled Task] Deleted stale temp object: %s", *obj.Key)
						incrementDeleted()
					}
				}
				continue
			}
			if obj.LastModified == nil {
				continue
			}

			cleanupWG.Add(1)
			go func(key string, lastModified time.Time) {
				defer cleanupWG.Done()
				// 获取文件元数据
				headInput := &s3.HeadObjectInput{
					Bucket: aws.String(bucketName),
					Key:    aws.String(key),
				}

				headOutput, err := s3Client.HeadObject(headInput)
				if err != nil {
					log.Printf("[Scheduled Task] Error getting file metadata %s: %v", key, err)
					return
				}

				// 检查自定义过期时间
				if headOutput.Metadata["Expirationtime"] != nil {
					expirationTime, err := time.Parse(time.RFC3339, *headOutput.Metadata["Expirationtime"])
					if err == nil {
						expirationTime = capExpirationTime(expirationTime, headOutput.Metadata, lastModified)
					}
					if err == nil && now.After(expirationTime) {
						deleteInput := &s3.DeleteObjectInput{
							Bucket: aws.String(bucketName),
							Key:    aws.String(key),
						}
						_, err := s3Client.DeleteObject(deleteInput)
						if err == nil {
							log.Printf("[Scheduled Task] Deleted expired file: %s, expiration: %s", key, *headOutput.Metadata["Expirationtime"])
							incrementDeleted()
						}
						return
					}
					return
				}

				// 检查文件年龄
				var uploadTime time.Time
				if headOutput.Metadata["Uploadtime"] != nil {
					uploadTime, _ = time.Parse(time.RFC3339, *headOutput.Metadata["Uploadtime"])
				} else {
					uploadTime = lastModified
				}

				age := now.Sub(uploadTime)
				if age.Seconds() > float64(maxAge) {
					deleteInput := &s3.DeleteObjectInput{
						Bucket: aws.String(bucketName),
						Key:    aws.String(key),
					}
					_, err := s3Client.DeleteObject(deleteInput)
					if err == nil {
						log.Printf("[Scheduled Task] Deleted expired file: %s, age: %.0fs", key, age.Seconds())
						incrementDeleted()
					}
				}
			}(*obj.Key, *obj.LastModified)
		}
		return true
	})

	if err != nil {
		log.Printf("[Scheduled Task] Error during cleanup: %v", err)
		return
	}

	cleanupWG.Wait()
	log.Printf("[Scheduled Task] Cleanup complete: checked %d files, deleted %d expired files", checkedCount, deletedCount)
}

func checkPassword(r *http.Request) bool {
	if password == "" {
		return true
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return false
	}

	if strings.HasPrefix(authHeader, "Basic ") {
		encoded := strings.TrimPrefix(authHeader, "Basic ")
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return false
		}
		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) == 2 {
			return parts[1] == password
		}
	}

	return authHeader == password
}

func generateRandomID() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	result := make([]byte, 6)
	for i := range result {
		result[i] = chars[rand.Intn(len(chars))]
	}
	return string(result)
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%dB", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	sizes := []string{"B", "KB", "MB", "GB", "TB"}
	return fmt.Sprintf("%.2f%s", float64(bytes)/float64(div), sizes[exp+1])
}

// capExpirationTime 按上传时间 + MAX_AGE_FOR_MULTIDOWNLOAD 强制封顶，
// 用于兜底处理限制调整前上传的超长有效期文件
func capExpirationTime(expireAt time.Time, metadata map[string]*string, lastModified time.Time) time.Time {
	if allowLifetimeOverMaxAge {
		return expireAt
	}
	uploadTime := lastModified
	if metadata["Uploadtime"] != nil {
		if t, err := time.Parse(time.RFC3339, *metadata["Uploadtime"]); err == nil {
			uploadTime = t
		}
	}
	capAt := uploadTime.Add(time.Duration(maxAgeForMultiDownload) * time.Second)
	if capAt.Before(expireAt) {
		return capAt
	}
	return expireAt
}

func parseInt64(s string, defaultValue int64) int64 {
	if s == "" {
		return defaultValue
	}
	val, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return defaultValue
	}
	return val
}

func sanitizeContentType(contentType string) string {
	ct := strings.TrimSpace(contentType)
	if ct == "" {
		return ""
	}

	if mediaType, _, err := mime.ParseMediaType(ct); err == nil {
		return mediaType
	}

	if idx := strings.Index(ct, ";"); idx != -1 {
		return strings.TrimSpace(ct[:idx])
	}

	return ct
}

func determineExtension(contentType string) string {
	if contentType == "" {
		return ""
	}

	if exts, err := mime.ExtensionsByType(contentType); err == nil && len(exts) > 0 {
		return exts[0]
	}

	if m := mimetype.Lookup(contentType); m != nil {
		return m.Extension()
	}

	return ""
}

func isValidSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func contentHashKey(shaHex string) (string, error) {
	if dedupSecret == "" {
		return "", errors.New("DEDUP_SECRET is required for deduplication")
	}

	normalizedHash := strings.ToLower(shaHex)
	mac := hmac.New(sha256.New, []byte(dedupSecret))
	mac.Write([]byte(normalizedHash))
	return "b/" + hex.EncodeToString(mac.Sum(nil)), nil
}

func tempObjectKey() (string, error) {
	var token [24]byte
	if _, err := cryptorand.Read(token[:]); err != nil {
		log.Printf("Secure temp key generation failed: %v", err)
		return "", err
	}
	return "t/" + hex.EncodeToString(token[:]), nil
}

func aliasObjectKey() (string, error) {
	var token [24]byte
	if _, err := cryptorand.Read(token[:]); err != nil {
		log.Printf("Secure alias key generation failed: %v", err)
		return "", err
	}
	return "a/" + hex.EncodeToString(token[:]), nil
}

func createAliasRecord(blobKey string, expiresAt time.Time, contentType string, size int64) (string, error) {
	aliasKey, err := aliasObjectKey()
	if err != nil {
		return "", err
	}
	createdAt := time.Now().UTC()
	record := aliasRecord{
		Version:     1,
		BlobKey:     blobKey,
		CreatedAt:   createdAt.Format(time.RFC3339),
		ExpiresAt:   expiresAt.UTC().Format(time.RFC3339),
		ContentType: contentType,
		Size:        size,
	}
	body, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	_, err = s3Client.PutObject(&s3.PutObjectInput{
		Bucket:      aws.String(bucketName),
		Key:         aws.String(aliasKey),
		Body:        bytes.NewReader(body),
		ContentType: aws.String("application/json"),
		Metadata: map[string]*string{
			"Uploadtime":        aws.String(record.CreatedAt),
			"Onetime":           aws.String("false"),
			"Expirationtime":    aws.String(record.ExpiresAt),
			"Expirationseconds": aws.String(strconv.FormatInt(int64(expiresAt.Sub(createdAt).Seconds()), 10)),
		},
	})
	if err != nil {
		return "", err
	}
	return aliasKey, nil
}

func getAliasRecord(aliasKey string) (aliasRecord, error) {
	var record aliasRecord
	output, err := s3Client.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(aliasKey),
	})
	if err != nil {
		return record, err
	}
	defer output.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(output.Body, 64*1024))
	if err := decoder.Decode(&record); err != nil {
		return record, err
	}
	if record.Version != 1 || !isValidBlobKey(record.BlobKey) || record.Size < 0 {
		return record, errors.New("invalid alias record")
	}
	if _, err := time.Parse(time.RFC3339, record.ExpiresAt); err != nil {
		return record, errors.New("invalid alias expiration")
	}
	if _, _, err := mime.ParseMediaType(record.ContentType); err != nil {
		return record, errors.New("invalid alias content type")
	}
	return record, nil
}

func isValidBlobKey(key string) bool {
	if !strings.HasPrefix(key, "b/") || len(key) != 66 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(key, "b/"))
	return err == nil
}

func headObject(key string) (*s3.HeadObjectOutput, error) {
	return s3Client.HeadObject(&s3.HeadObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
}

func deleteObject(key string) {
	_, err := s3Client.DeleteObject(&s3.DeleteObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		log.Printf("Delete object failed key=%s err=%v", key, err)
	}
}

func copyObject(sourceKey, destKey, contentType string, metadata map[string]*string) error {
	copySource := url.PathEscape(bucketName) + "/" + strings.ReplaceAll(url.PathEscape(sourceKey), "%2F", "/")
	_, err := s3Client.CopyObject(&s3.CopyObjectInput{
		Bucket:            aws.String(bucketName),
		Key:               aws.String(destKey),
		CopySource:        aws.String(copySource),
		ContentType:       aws.String(contentType),
		Metadata:          metadata,
		MetadataDirective: aws.String("REPLACE"),
	})
	return err
}

func liveExpiration(headOutput *s3.HeadObjectOutput) (time.Time, bool) {
	if headOutput == nil || headOutput.Metadata == nil || headOutput.Metadata["Expirationtime"] == nil {
		return time.Time{}, false
	}
	expirationTime, err := time.Parse(time.RFC3339, *headOutput.Metadata["Expirationtime"])
	if err != nil {
		return time.Time{}, false
	}
	lastModified := time.Now()
	if headOutput.LastModified != nil {
		lastModified = *headOutput.LastModified
	}
	expirationTime = capExpirationTime(expirationTime, headOutput.Metadata, lastModified)
	return expirationTime, time.Now().Before(expirationTime)
}

func absoluteFileURL(r *http.Request, key string) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s/%s", scheme, r.Host, key)
}

func formatExpirationDuration(expirationSeconds int64) string {
	hours := expirationSeconds / 3600
	minutes := (expirationSeconds % 3600) / 60
	seconds := expirationSeconds % 60
	if hours > 0 {
		result := fmt.Sprintf("%d小时", hours)
		if minutes > 0 {
			result += fmt.Sprintf("%d分钟", minutes)
		}
		return result
	}
	if minutes > 0 {
		return fmt.Sprintf("%d分钟", minutes)
	}
	return fmt.Sprintf("%d秒", seconds)
}

type byteCounter struct {
	total int64
}

func (c *byteCounter) Write(p []byte) (int, error) {
	c.total += int64(len(p))
	return len(p), nil
}

func (c *byteCounter) Total() int64 {
	return c.total
}

func isRequestBodyTooLarge(err error) bool {
	if err == nil {
		return false
	}

	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return true
	}

	if awsErr, ok := err.(awserr.Error); ok {
		return isRequestBodyTooLarge(awsErr.OrigErr())
	}

	return false
}

func isObjectNotFound(err error) bool {
	if err == nil {
		return false
	}
	if awsErr, ok := err.(awserr.Error); ok {
		code := awsErr.Code()
		return code == s3.ErrCodeNoSuchKey || code == "NotFound" || code == "NoSuchKey" || code == "404"
	}
	return false
}

func getClientIP(r *http.Request) string {
	// X-Real-IP 由 nginx sidecar 以覆盖方式设置，比追加式的 X-Forwarded-For 更可信；
	// 无反代直接暴露时应设 TRUST_PROXY_HEADERS=false，仅使用连接地址
	if trustProxyHeaders {
		if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
			return realIP
		}

		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if len(parts) > 0 {
				if ip := strings.TrimSpace(parts[0]); ip != "" {
					return ip
				}
			}
		}
	}

	if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && ip != "" {
		return ip
	}

	return r.RemoteAddr
}

type rateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	entries map[string]*rateLimitEntry
}

type rateLimitEntry struct {
	count       int
	windowStart time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	rl := &rateLimiter{
		limit:   limit,
		window:  window,
		entries: make(map[string]*rateLimitEntry),
	}
	go rl.cleanupLoop()
	return rl
}

// Allow 报告该 IP 当前窗口内是否还可上传；拒绝时返回距窗口重置的秒数
func (rl *rateLimiter) Allow(ip string) (bool, int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	entry, ok := rl.entries[ip]
	if !ok || now.Sub(entry.windowStart) >= rl.window {
		rl.entries[ip] = &rateLimitEntry{count: 1, windowStart: now}
		return true, 0
	}

	if entry.count >= rl.limit {
		retryAfter := int((rl.window - now.Sub(entry.windowStart) + time.Second - 1) / time.Second)
		if retryAfter < 1 {
			retryAfter = 1
		}
		return false, retryAfter
	}

	entry.count++
	return true, 0
}

// 定期清理过期窗口条目，防止 map 无限增长
func (rl *rateLimiter) cleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		rl.mu.Lock()
		for ip, entry := range rl.entries {
			if now.Sub(entry.windowStart) >= rl.window {
				delete(rl.entries, ip)
			}
		}
		rl.mu.Unlock()
	}
}
