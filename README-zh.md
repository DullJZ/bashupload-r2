# BashUpload-R2

[English](README.md) | 中文

基于 Cloudflare Workers 和 Cloudflare R2 对象存储构建，适合命令行和浏览器的简单文件上传服务。

[![Deploy to Cloudflare](https://deploy.workers.cloudflare.com/button)](https://deploy.workers.cloudflare.com/?url=https://github.com/DullJZ/bashupload-r2)

直接使用：[bashupload.app](https://bashupload.app)

感谢 [bashupload.com](https://bashupload.com) 及其作者 [@mrcrypster](https://github.com/mrcrypster) 提供的灵感。

## 快速开始

```sh
curl bashupload.app -T file.txt
```

使用命令行别名快速设置

```sh
alias bashupload='curl bashupload.app -T'
bashupload file.txt
```

要使别名永久生效，请将其添加到你的 shell 配置文件中。

```sh
echo \'alias bashupload=\'curl bashupload.app -T\'\' >> ~/.bashrc
source ~/.bashrc
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
curl bashupload.app -T myfile.pdf
https://bashupload.app/abc123_myfile.pdf
```

**隐私注意：** 为了您的隐私和安全，文件在下载后会立即从我们的服务器上删除。每个文件只能下载一次。下载后请务必将文件保存在本地，因为链接在首次下载后将不再有效。


## 自部署到Cloudflare

点击上方的 "Deploy to Cloudflare" 按钮，修改配置。

其中，`MAX_UPLOAD_SIZE`单位为字节（默认为 5GB），`MAX_AGE`单位为秒（默认为 1小时），可以根据需要进行调整。
