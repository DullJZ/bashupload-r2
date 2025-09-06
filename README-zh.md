# BashUpload-R2

[English](README.md) | 中文

基于 Cloudflare Workers 和 Cloudflare R2 对象存储构建，适合命令行和浏览器的简单文件上传服务。

[![Deploy to Cloudflare](https://deploy.workers.cloudflare.com/button)](https://deploy.workers.cloudflare.com/?url=https://github.com/DullJZ/bashupload-r2)

直接使用：[bashupload.app](https://bashupload.app)

感谢 [bashupload.com](https://bashupload.com) 及其作者 [@mrcrypster](https://github.com/mrcrypster) 提供的灵感。

## 快速开始

```sh
# 上传并返回普通链接
curl bashupload.app -T file.txt

# 上传并返回短链接
curl bashupload.app/short -T file.txt

# 上传并设置有效期（86400秒=24小时，允许多次下载）
curl -H "X-Expiration-Seconds: 86400" bashupload.app -T file.txt
```

使用命令行别名快速设置

```sh
alias bashupload='curl bashupload.app -T'
alias bashuploadshort='curl bashupload.app/short -T'
alias bashuploadexpire='curl -H "X-Expiration-Seconds: 3600" bashupload.app -T'
bashupload file.txt        # 返回普通链接
bashuploadshort file.txt     # 返回短链接
bashuploadexpire file.txt  # 返回1小时有效期链接
```

要使别名永久生效，请将其添加到你的 shell 配置文件中。

```sh
echo "alias bashupload='curl bashupload.app -T'" >> ~/.bashrc
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
- 浏览器拖拽上传
- 无需注册
- 直接下载链接
- 隐私保护：文件在下载后自动删除
- 安全的文件存储，仅限一次下载
- 支持自定义有效期：可设置文件有效期，在指定时间内允许多次下载
- 支持最大 5GB 的文件（自部署可调整）
- 支持自部署设置密码

**隐私注意：** 为了您的隐私和安全，文件在下载后会立即从我们的服务器上删除。每个文件**默认只能下载一次**，**除非您设置了有效期**。设置有效期后，文件可以在有效期内多次下载。下载后请务必将文件保存在本地，因为链接在首次下载后（一次性下载）或过期后（有效期下载）将不再有效。


## 自部署到Cloudflare

点击上方的 "Deploy to Cloudflare" 按钮，修改配置。

其中，`MAX_UPLOAD_SIZE`单位为字节（默认为 5GB），`MAX_AGE`单位为秒（默认为 1小时），可以根据需要进行调整。

`MAX_AGE_FOR_MULTIDOWNLOAD` 是允许多次下载的最大有效期时间，单位为秒（默认值是86400，即24小时）。用户可以设置不超过此限制的自定义有效期。

`SHORT_URL_SERVICE` 是短链接服务的 API 端点（默认为 `https://suosuo.de/short`），如果需要，可以将其更改为您自己的短链接服务。仅支持 [MyUrls](https://github.com/CareyWang/MyUrls)。

`PASSWORD` 环境变量为上传、下载必须提供的密码。如果不需要密码保护，可以将其留空。

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
