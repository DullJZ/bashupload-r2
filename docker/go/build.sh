# 判断是否在go目录下
if [ "$(basename "$PWD")" != "go" ]; then
  echo "请在docker/go目录下运行此脚本"
  exit 1
fi

# docker编译
docker buildx build --platform linux/amd64,linux/arm64 \
  -f Dockerfile \
  -t dulljz/bashupload-go:latest \
  --push \
  .