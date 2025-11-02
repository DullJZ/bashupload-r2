package main

import (
	"bytes"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
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

	// 从 R2 下载文件
	downloadFile(w, r, fileName)
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
	if mtype := mimetype.Lookup(fileName); mtype != nil {
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
	// 密码检查
	if password != "" && !checkPassword(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// 检查文件大小
	contentLength := r.ContentLength
	if contentLength > maxUploadSize {
		http.Error(w, fmt.Sprintf("Upload failed: file too large. Max size is %s.", formatBytes(maxUploadSize)), http.StatusRequestEntityTooLarge)
		return
	}

	// 获取过期时间
	expirationHeader := r.Header.Get("X-Expiration-Seconds")
	var expirationTime *time.Time
	var expirationSeconds int64
	hasExpiration := false

	if expirationHeader != "" {
		expSec, err := strconv.ParseInt(expirationHeader, 10, 64)
		if err == nil && expSec > 0 {
			hasExpiration = true
			expirationSeconds = expSec
			expTime := time.Now().Add(time.Duration(expSec) * time.Second)
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

	// 上传到 R2 (使用流式上传，不需要将整个文件加载到内存)
	uploadInput := &s3manager.UploadInput{
		Bucket:      aws.String(bucketName),
		Key:         aws.String(fileName),
		Body:        r.Body,
		ContentType: aws.String(contentType),
		Metadata:    metadata,
	}

	_, err := uploader.Upload(uploadInput)
	if err != nil {
		log.Printf("Upload error: %v", err)
		http.Error(w, fmt.Sprintf("Upload failed: %v", err), http.StatusInternalServerError)
		return
	}

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

	listInput := &s3.ListObjectsV2Input{
		Bucket:  aws.String(bucketName),
		MaxKeys: aws.Int64(1000),
	}

	err := s3Client.ListObjectsV2Pages(listInput, func(page *s3.ListObjectsV2Output, lastPage bool) bool {
		for _, obj := range page.Contents {
			checkedCount++

			go func(key string, lastModified time.Time) {
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
					if err == nil && now.After(expirationTime) {
						deleteInput := &s3.DeleteObjectInput{
							Bucket: aws.String(bucketName),
							Key:    aws.String(key),
						}
						_, err := s3Client.DeleteObject(deleteInput)
						if err == nil {
							log.Printf("[Scheduled Task] Deleted expired file: %s, expiration: %s", key, *headOutput.Metadata["Expirationtime"])
							deletedCount++
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
						deletedCount++
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

	time.Sleep(2 * time.Second) // 等待异步删除完成
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
