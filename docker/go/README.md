# BashUpload Golang 版本 - Docker 部署指南

这是 BashUpload 的高性能 Golang 版本，使用 S3 API 访问 Cloudflare R2 存储。

## 📁 目录结构

```
docker/go/
├── main.go                # 主应用程序
├── go.mod                 # Go 模块依赖
├── Dockerfile             # Docker 镜像构建文件（多阶段）
├── docker-compose.yml     # Docker Compose 配置
├── .env.example           # 环境变量配置示例
├── start.sh               # 启动脚本
└── README.md              # 本文档
```

## 快速开始

### 1. 准备 R2 配置

在 Cloudflare 控制台中：

1. 创建 R2 存储桶
2. 生成 R2 API Token
3. 记录 `Account ID`, `Access Key ID`, `Secret Access Key`

### 2. 配置环境变量

```bash
cd docker/go
cp .env.example .env
```

编辑 `.env` 文件：

```env
R2_ACCOUNT_ID=your_account_id
R2_ACCESS_KEY_ID=your_access_key_id
R2_SECRET_ACCESS_KEY=your_secret_access_key
R2_BUCKET_NAME=your_bucket_name
```

### 3. 启动服务

```bash

# 直接使用 docker-compose
docker-compose up -d

# 查看日志
docker-compose logs -f
```

服务将在 `http://localhost:3000` 上运行。

### 4. 测试服务

```bash
# 测试文件上传
curl http://localhost:3000 -T test.txt

# 测试带过期时间的上传
curl -H "X-Expiration-Seconds: 3600" http://localhost:3000 -T test.txt

# 测试短链接上传
curl http://localhost:3000/short -T test.txt
```

## 技术特性

### 核心优势

1. **原生并发**: 使用 Goroutines 实现高效并发处理
2. **低内存占用**: 静态编译的二进制文件，无运行时依赖
3. **快速启动**: 毫秒级启动时间
4. **高性能**: 原生性能，无解释器开销
5. **小镜像**: 多阶段构建，最终镜像仅 ~20MB

### 实现细节

- **S3 客户端**: 使用 AWS SDK for Go v1
- **MIME 检测**: gabriel-vasile/mimetype 库
- **流式处理**: 直接流式传输文件，不占用内存
- **异步删除**: 使用 Goroutine 异步删除一次性文件
- **定时任务**: 内置 Ticker 每 5 分钟清理过期文件
- **并发清理**: 使用 Goroutines 并发处理文件清理

### 安全特性

- **非 root 用户**: 容器内使用专用用户运行
- **静态编译**: 无动态链接库依赖
- **最小镜像**: 基于 Alpine Linux
- **健康检查**: 内置健康检查端点

## 配置说明

### 必需配置

- `R2_ACCOUNT_ID`: Cloudflare 账户 ID
- `R2_ACCESS_KEY_ID`: R2 API Token 的 Access Key
- `R2_SECRET_ACCESS_KEY`: R2 API Token 的 Secret Key
- `R2_BUCKET_NAME`: R2 存储桶名称

### 可选配置

- `MAX_UPLOAD_SIZE`: 最大上传大小（字节），默认 5GB
- `MAX_AGE`: 文件最大保存时间（秒），默认 3600（1小时）
- `MAX_AGE_FOR_MULTIDOWNLOAD`: 多次下载模式下的最大保存时间（秒），默认 86400（24小时）
- `ENABLE_SHORT_URL`: 是否启用短链接，默认 `false`
- `ALLOW_LIFETIME_OVER_MAX_AGE`: 是否允许超过 MAX_AGE 的过期时间，默认 `false`
- `PASSWORD`: 上传密码保护（可选）
- `SHORT_URL_SERVICE`: 短链接服务地址，默认 `https://suosuo.de/short`
- `PORT`: 服务端口，默认 3000

## 性能调优

### 资源限制

在 `docker-compose.yml` 中已配置资源限制：

```yaml
deploy:
  resources:
    limits:
      cpus: '2'
      memory: 512M
    reservations:
      cpus: '0.5'
      memory: 128M
```

### 并发优化

Go 运行时会自动使用所有可用 CPU 核心。如需限制：

```yaml
environment:
  - GOMAXPROCS=4  # 限制使用 4 个 CPU 核心
```

### 内存优化

Go 的垃圾回收器会自动管理内存。如需调整：

```yaml
environment:
  - GOGC=100  # GC 触发阈值（默认 100）
```

## 生产部署

### 1. 使用 Nginx 反向代理

```nginx
upstream bashupload {
    server localhost:3000;
    keepalive 32;
}

server {
    listen 80;
    server_name bashupload.example.com;

    client_max_body_size 5G;
    client_body_timeout 300s;

    location / {
        proxy_pass http://bashupload;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        proxy_read_timeout 300s;
        proxy_send_timeout 300s;
    }
}
```

### 2. HTTPS 配置

```bash
certbot --nginx -d bashupload.example.com
```

### 3. 负载均衡

运行多个实例：

```yaml
services:
  bashupload-go-1:
    <<: *bashupload-template
    ports:
      - "3001:3000"

  bashupload-go-2:
    <<: *bashupload-template
    ports:
      - "3002:3000"
```

配置 Nginx 负载均衡：

```nginx
upstream bashupload {
    least_conn;
    server localhost:3001;
    server localhost:3002;
    keepalive 64;
}
```

## 本地开发

### 不使用 Docker

```bash
# 安装依赖
go mod download

# 配置环境变量
export R2_ACCOUNT_ID=your_account_id
export R2_ACCESS_KEY_ID=your_access_key_id
export R2_SECRET_ACCESS_KEY=your_secret_access_key
export R2_BUCKET_NAME=your_bucket_name

# 运行
go run main.go

# 编译
go build -o bashupload main.go
./bashupload
```

### 热重载开发

```bash
# 安装 air
go install github.com/cosmtrek/air@latest

# 运行
air
```

## 构建与部署

### 手动构建

```bash
# 构建 Docker 镜像
docker build -t bashupload-go:latest -f Dockerfile ../..

# 运行容器
docker run -d \
  --name bashupload-go \
  -p 3000:3000 \
  --env-file .env \
  bashupload-go:latest
```

### 交叉编译

```bash
# Linux AMD64
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bashupload-linux-amd64

# Linux ARM64
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bashupload-linux-arm64

# macOS
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o bashupload-darwin-amd64

# Windows
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o bashupload-windows-amd64.exe
```

## 监控与日志

### 查看日志

```bash
# 实时日志
docker-compose logs -f bashupload-go

# 最近 100 行
docker-compose logs --tail=100 bashupload-go
```

### 容器状态

```bash
# 查看状态
docker-compose ps

# 查看资源使用
docker stats bashupload-go

# 健康检查
docker inspect bashupload-go | grep -A 10 Health
```

### 性能分析

Go 内置 pprof 性能分析工具。添加到 main.go：

```go
import _ "net/http/pprof"

func main() {
    go func() {
        log.Println(http.ListenAndServe("localhost:6060", nil))
    }()
    // ... 其他代码
}
```

访问 http://localhost:6060/debug/pprof/

## 故障排查

### 无法连接到 R2

检查：
```bash
# 测试网络连接
docker exec bashupload-go wget -O- https://${R2_ACCOUNT_ID}.r2.cloudflarestorage.com

# 查看详细日志
docker-compose logs bashupload-go | grep -i error
```

### 内存不足

调整资源限制：
```yaml
deploy:
  resources:
    limits:
      memory: 1G  # 增加内存限制
```

### 并发问题

查看 goroutine 数量：
```bash
# 启用 pprof
curl http://localhost:6060/debug/pprof/goroutine?debug=1
```

## 性能基准测试

### 上传测试

```bash
# 单文件上传
time curl http://localhost:3000 -T largefile.bin

# 并发上传测试
seq 1 100 | xargs -P 10 -I {} curl http://localhost:3000 -T test.txt
```

### 下载测试

```bash
# 使用 ab (Apache Bench)
ab -n 1000 -c 100 http://localhost:3000/testfile.txt

# 使用 wrk
wrk -t4 -c100 -d30s http://localhost:3000/testfile.txt
```

### 预期性能

在标准配置下（2 CPU, 512MB RAM）：
- **并发上传**: 500+ req/s
- **并发下载**: 1000+ req/s
- **内存占用**: 20-50MB
- **CPU 使用**: 10-30%

## 更新服务

```bash
# 拉取最新代码
git pull

# 重新构建
docker-compose build

# 重启服务（零停机）
docker-compose up -d

# 清理旧镜像
docker image prune -f
```

## 从 Node.js 迁移

### 无缝迁移

1. 两个版本 API 完全兼容
2. 使用相同的 R2 存储桶
3. 元数据格式一致
4. 配置参数相同

### 迁移步骤

```bash
# 1. 停止 Node.js 版本
cd docker
docker-compose down

# 2. 启动 Go 版本
cd go
cp ../env .env
docker-compose up -d

# 3. 验证功能
curl http://localhost:3000 -T test.txt
```

## 安全建议

1. **设置密码**: 配置 `PASSWORD` 环境变量
2. **非 root 运行**: 已默认配置
3. **最小权限**: 容器仅暴露必要端口
4. **定期更新**: 保持 Go 版本和依赖最新
5. **日志监控**: 定期检查异常访问

## 对比 Node.js 版本

| 特性 | Go 版本 | Node.js 版本 |
|-----|---------|--------------|
| 内存占用 | 15-30MB | 50-100MB |
| 镜像大小 | ~20MB | ~200MB |
| 启动时间 | <100ms | ~1-2s |
| 并发性能 | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ |
| CPU 效率 | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ |
| 开发体验 | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ |

## 贡献与反馈

如有问题或建议，欢迎提交 Issue 或 PR。

## License

与主项目保持一致
