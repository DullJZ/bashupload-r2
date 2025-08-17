# BashUpload-R2

English | [中文](README-zh.md)

Simple file upload service based on Cloudflare Workers and Cloudflare R2 object storage for the command line and browser.

[![Deploy to Cloudflare](https://deploy.workers.cloudflare.com/button)](https://deploy.workers.cloudflare.com/?url=https://github.com/DullJZ/bashupload-r2)


Directly Use: [bashupload.app](https://bashupload.app)

Thanks to [bashupload.com](https://bashupload.com) and its author [@mrcrypster](https://github.com/mrcrypster) for the inspiration.

## Quick Start

```sh
$ curl bashupload.app -T file.txt
```

Use `alias` in bash to set quick upload

```sh
alias bashupload='curl bashupload.app -T'
bashupload file.txt
```

## Browser Upload

- Drag & drop files or click to select files (Max 5GB)
- Direct download links
- No registration required

## Features

- Simple command-line interface
- Browser-based drag & drop upload
- No registration required
- Direct download links
- Privacy-focused: Files are automatically deleted after download
- Secure file storage with one-time download
- Supports files up to 5GB in size (self-hosting can adjust this limit)

## Examples

```sh
# Upload a file
$ curl bashupload.app -T myfile.pdf
https://bashupload.app/abc123_myfile.pdf
```

**Privacy Notice:** For your privacy and security, files are automatically deleted from our servers immediately after they are downloaded. Each file can only be downloaded once. Make sure to save the file locally after downloading, as the link will no longer work after the first download.

