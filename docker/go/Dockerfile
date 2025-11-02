FROM --platform=$BUILDPLATFORM golang:1.21-alpine AS builder

ARG TARGETOS
ARG TARGETARCH
ARG BUILDPLATFORM

WORKDIR /build

# 安装构建依赖
RUN apk add --no-cache git

# 复制 go mod 文件
COPY go.mod go.sum* ./

# 下载依赖
RUN go mod download

# 复制源代码
COPY main.go ./

# 复制静态文件（从项目根目录）
COPY public/ ./public/

# 编译二进制文件（静态链接）
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
  go build -a -installsuffix cgo -ldflags="-w -s" -o bashupload .

# 运行阶段：使用最小镜像
FROM alpine:latest

# 安装 ca-certificates 用于 HTTPS 请求
RUN apk --no-cache add ca-certificates wget

WORKDIR /app

# 从构建阶段复制二进制文件
COPY --from=builder /build/bashupload .

# 创建非 root 用户
RUN addgroup -g 1000 appuser && \
    adduser -D -u 1000 -G appuser appuser && \
    chown -R appuser:appuser /app

USER appuser

# 暴露端口
EXPOSE 3000

# 健康检查
HEALTHCHECK --interval=30s --timeout=10s --start-period=40s --retries=3 \
  CMD wget --quiet --tries=1 --spider http://localhost:3000/ || exit 1

# 运行应用
ENTRYPOINT ["/app/bashupload"]
