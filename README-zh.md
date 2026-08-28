# BashUpload-R2

[English](README.md) | 中文

基于 Cloudflare Workers 和 Cloudflare R2 对象存储构建，适合命令行和浏览器的简单文件上传服务。

[![Deploy to Cloudflare](https://deploy.workers.cloudflare.com/button)](https://deploy.workers.cloudflare.com/?url=https://github.com/DullJZ/bashupload-r2)

直接使用：[bashupload.app](https://bashupload.app)

感谢 [bashupload.com](https://bashupload.com) 及其作者 [@mrcrypster](https://github.com/mrcrypster) 提供的灵感。

## 快速开始

```sh
# 上传文件并返回普通链接
curl bashupload.app -T file.txt

# 上传文本内容（保存为 .txt 文件）
curl bashupload.app -d "你的长文本内容"

# 上传并返回短链接
curl bashupload.app/short -T file.txt

# 上传并设置有效期（86400秒=24小时，允许多次下载）
curl -H "X-Expiration-Seconds: 86400" bashupload.app -T file.txt
```

使用命令行别名快速设置

```sh
alias bashupload='curl bashupload.app -T'
alias bashuploadtext='curl bashupload.app -d'
alias bashuploadshort='curl bashupload.app/short -T'
alias bashuploadexpire='curl -H "X-Expiration-Seconds: 3600" bashupload.app -T'
bashupload file.txt            # 返回普通链接
bashuploadtext "你的文本内容"   # 上传文本内容
bashuploadshort file.txt       # 返回短链接
bashuploadexpire file.txt      # 返回1小时有效期链接
```

要使别名永久生效，请将其添加到你的 shell 配置文件中。

```sh
echo "alias bashupload='curl bashupload.app -T'" >> ~/.bashrc
echo "alias bashuploadtext='curl bashupload.app -d'" >> ~/.bashrc
echo "alias bashuploadshort='curl bashupload.app/short -T'" >> ~/.bashrc
echo "alias bashuploadexpire='curl -H \"X-Expiration-Seconds: 3600\" bashupload.app -T'" >> ~/.bashrc
source ~/.bashrc
```

## 浏览器上传

- 拖拽文件或点击选择文件
- 设置文件的有效期
- 直接下载链接
- 无需注册

## 特性

- 简单的命令行接口
- 快速文本分享
- 浏览器拖拽上传
- 无需注册
- 直接下载链接
- 隐私保护：文件在下载后自动删除
- 安全的文件存储，仅限一次下载
- 支持自定义有效期：可设置文件有效期，在指定时间内允许多次下载
- 支持最大 5GB 的文件（自部署可调整）
- 支持自部署设置密码
- 单 IP 上传限流（默认每分钟 10 次），防止滥用

**隐私注意：** 为了您的隐私和安全，文件在下载后会立即从我们的服务器上删除。每个文件**默认只能下载一次**，**除非您设置了有效期**。设置有效期后，文件可以在有效期内多次下载。下载后请务必将文件保存在本地，因为链接在首次下载后（一次性下载）或过期后（有效期下载）将不再有效。


## 自部署到Cloudflare

点击上方的 "Deploy to Cloudflare" 按钮，修改配置。

其中，`MAX_UPLOAD_SIZE`单位为字节（默认为 5GB），`MAX_AGE`单位为秒（默认为 1小时），可以根据需要进行调整。

`MAX_AGE_FOR_MULTIDOWNLOAD` 是允许多次下载的最大有效期时间，单位为秒（默认值是86400，即24小时）。用户可以设置不超过此限制的自定义有效期。此限制在服务端强制执行：超过限制的有效期（例如直接通过 `X-Expiration-Seconds` 头设置）会被自动调整为此值，除非 `ALLOW_LIFETIME_OVER_MAX_AGE` 设置为 `true`。

`ENABLE_DEDUP` 控制带有效期上传的 SHA-256 内容哈希去重，默认值为 `true`。浏览器始终完整上传文件，服务端接收完成后计算哈希并进行存储去重。只有同时配置 `DEDUP_SECRET` 时去重才会生效；一次性下载仍使用随机对象 key。

`DEDUP_SECRET` 是启用安全去重的必需服务端私钥，建议使用至少 32 字节的随机值。服务端完整接收带有效期的上传后计算 SHA-256，通过 `HMAC-SHA256(DEDUP_SECRET, 内容哈希)` 派生内部 blob key。相同字节共享一份不可变的 `b/` blob；每次上传都会生成新的随机 `a/` alias，独立保存有效期、Content-Type 和 blob 引用。下载 URL 只暴露 alias，因此一个上传过期不会使相同内容的其他 alias 提前失效。过期后只删除 alias。共享 blob 通过下文的冻结写入维护流程回收；日常清理器会跳过 `b/`，因为上传进行时无法证明共享 blob 已经没有 alias 引用。缺少 secret 时回退为随机 key 上传，不启用去重。

`X-Content-SHA256` 仅是可选的完整性校验声明，不是去重前提；服务端会自行计算并校验内容哈希。

Worker 请勿将 `DEDUP_SECRET` 写入 `wrangler.toml`，使用 `npx wrangler secret put DEDUP_SECRET` 配置；本地开发写入 `.dev.vars`，不要提交该文件。轮换 secret 会建立新的去重 namespace；已有 alias 保存了完整 blob 引用，因此仍可使用到各自过期。

早期去重格式创建的 `c/` 链接继续兼容下载，并按原有过期元数据清理。

### 共享 blob 垃圾回收（维护窗口）

Go 命令会在应用流量完全停止后完成 `a/` 到 `b/` 的引用扫描。先从入口摘除服务并优雅排空正在进行的上传和下载，再停止所有应用实例及其他 R2 写入者。运行 `./bashupload gc --offline --dry-run` 查看报告，确认没有扫描错误后，再显式运行 `./bashupload gc --offline --delete`。GC 默认在 alias 到期后继续保护 blob 15 分钟，以容忍小幅时钟偏差；运行前仍应确认维护节点时间已经同步。完整的停流、grace 配置、dry-run、删除、重启和请求成本流程见 [Go 部署指南](docker/go/README.md#shared-blob-garbage-collection-maintenance-window)。

维护窗口必须包括所有 Go/Worker 实例、旧版本二进制、上传脚本和会修改 R2 的定时任务；标准方案也会停止应用下载流量。维护命令只扫描 `a/` 和 `b/`，按批删除没有引用的 `b/` 对象，不删除过期 alias。日常清理仍负责 alias、临时对象和旧 `c/` 对象；已有 `c/` 链接继续兼容。如果有任何写入者无法停止，不要使用 `--delete`。

`SHORT_URL_SERVICE` 是短链接服务的 API 端点（默认为 `https://suosuo.de/short`），如果需要，可以将其更改为您自己的短链接服务。仅支持 [MyUrls](https://github.com/CareyWang/MyUrls)。

`PASSWORD` 环境变量为上传、下载必须提供的密码。如果不需要密码保护，可以将其留空。

上传按客户端 IP 限流（默认每 60 秒 10 次，超限请求返回 HTTP 429）。Worker 版本通过 `wrangler.toml` 中的 `[[ratelimits]]` 绑定配置（删除绑定即禁用；需较新的 wrangler，旧版 4.x 需改用等价的 `[[unsafe.bindings]]`、`type = "ratelimit"` 语法）；Docker 版本使用 `UPLOAD_RATE_LIMIT` / `UPLOAD_RATE_LIMIT_WINDOW` 环境变量（`UPLOAD_RATE_LIMIT=0` 禁用），服务直接暴露公网（无反向代理）时需设置 `TRUST_PROXY_HEADERS=false` 防止伪造头绕过限流。

编译部署最后一步可能会出现部署失败的错误，原因是默认使用了配置文件中的 bashupload.app 作为域名。事实上项目已经部署成功，在Worker项目设置中进行域名绑定即可。

## 高级功能

### 自定义有效期

通过使用 `X-Expiration-Seconds` 头部，您可以为上传的文件设置自定义有效期。这允许文件在过期前被多次下载，过期后文件将自动删除。

示例：
```sh
# 设置1小时有效期（文件可多次下载1小时）
curl -H "X-Expiration-Seconds: 3600" bashupload.app -T file.txt

# 设置24小时有效期
curl -H "X-Expiration-Seconds: 86400" bashupload.app -T file.txt

# 设置7天有效期
curl -H "X-Expiration-Seconds: 604800" bashupload.app -T file.txt
```

**重要说明：**
- 不设置有效期时，文件只能下载一次（一次性下载）
- 设置有效期后，文件在有效期内可多次下载
- 最大允许的有效期由 `MAX_AGE_FOR_MULTIDOWNLOAD` 控制（默认：24小时）
- 浏览器上传也通过UI支持设置有效期

### 快速文本分享

您可以快速分享长文本片段、代码、日志或任何文本内容，无需先创建文件。只需使用 `curl -d` 直接上传文本，它将自动保存为 `.txt` 文件。

示例：
```sh
# 分享快速文本片段
curl bashupload.app -d "这是我遇到的错误信息..."

# 分享代码片段
curl bashupload.app -d "$(cat script.sh)"

# 分享命令输出
curl bashupload.app -d "$(ls -la)"

# 设置有效期以便多次查看
curl -H "X-Expiration-Seconds: 3600" bashupload.app -d "今天的会议记录..."

# 结合短链接方便分享
curl bashupload.app/short -d "你的文本内容"
```

### 密码保护

要启用密码保护，请在 Cloudflare Worker 设置中设置 `PASSWORD` 环境变量。当设置 PASSWORD 后，上传和下载都需要在 Authorization 头中提供密码。

使用 curl 的示例：
```sh
# 带密码上传
curl -H "Authorization: yourpassword" bashupload.app -T file.txt

# 带密码下载
curl -H "Authorization: yourpassword" https://bashupload.app/yourfile.txt
```

设置含密码的alias别名：
```sh
echo "alias bashupload='curl -H \"Authorization: yourpassword\" bashupload.app -T'" >> ~/.bashrc
echo "alias bashuploadshort='curl -H \"Authorization: yourpassword\" bashupload.app/short -T'" >> ~/.bashrc
source ~/.bashrc
```
