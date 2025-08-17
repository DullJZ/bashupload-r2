# BashUpload-R2

[English](README.md) | 中文

基于 Cloudflare Workers 和 Cloudflare R2 对象存储构建，适合命令行和浏览器的简单文件上传服务。

[![Deploy to Cloudflare](https://deploy.workers.cloudflare.com/button)](https://deploy.workers.cloudflare.com/?url=https://github.com/DullJZ/bashupload-r2)

直接使用：[bashupload.app](https://bashupload.app)

感谢 [bashupload.com](https://bashupload.com) 及其作者 [@mrcrypster](https://github.com/mrcrypster) 提供的灵感。

## 快速开始

```sh
$ curl bashupload.app -T file.txt
```

使用命令行别名快速设置

```sh
alias bashupload='curl bashupload.app -T'
bashupload file.txt
```

## 浏览器上传

- 拖拽文件或点击选择文件（最大 5GB）
- 直接下载链接
- 无需注册

## 特性

- 简单的命令行接口
- 浏览器拖拽上传
- 无需注册
- 直接下载链接
- 隐私保护：文件在下载后自动删除
- 安全的文件存储，仅限一次下载
- 支持最大 5GB 的文件（自部署可调整）

## 示例

```sh
# 上传文件
$ curl bashupload.app -T myfile.pdf
https://bashupload.app/abc123_myfile.pdf
```

**隐私注意：** 为了您的隐私和安全，文件在下载后会立即从我们的服务器上删除。每个文件只能下载一次。下载后请务必将文件保存在本地，因为链接在首次下载后将不再有效。


## 自部署到Cloudflare

点击上方的 "Deploy to Cloudflare" 按钮，或按照以下步骤手动部署：

1. **创建 Cloudflare R2 存储桶**：在 Cloudflare 仪表盘中创建一个新的 R2 存储桶，用于存储上传的文件。名称可以是 `bashupload` 或其他您喜欢的名称。
2. **Fork存储库**：点击Fork按钮，将 BashUpload-R2 的代码Fork到您的 GitHub。
3. **在 Cloudflare 仪表盘中创建 Workers**：在 Cloudflare 仪表盘中创建一个新的 Workers 服务，在设置中将 Fork 的代码仓库链接到该服务。
4. **修改配置文件**：在 Fork 的代码仓库中，找到 `wrangler.jsonc` 配置文件，设置 `r2_buckets` 中的 `bucket_name` 为您的 R2 存储桶名称；设置 `routes` 中的域名或路由（参见[文档](https://developers.cloudflare.com/workers/configuration/routing/)）。

   如果您需要更大的单文件上传限制，可以在 `main.go` 中设置 `const maxFileSize = 5 * 1024 * 1024 * 1024`
5. **上传并部署**：提交你的更改到 GitHub，Cloudflare 会自动检测到更改并部署 Workers 服务。