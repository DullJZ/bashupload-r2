# BashUpload-R2

English | [中文](README-zh.md)

Simple file upload service based on Cloudflare Workers and Cloudflare R2 object storage for the command line and browser.

[![Deploy to Cloudflare](https://deploy.workers.cloudflare.com/button)](https://deploy.workers.cloudflare.com/?url=https://github.com/DullJZ/bashupload-r2)

Directly Use: [bashupload.app](https://bashupload.app)

Thanks to [bashupload.com](https://bashupload.com) and its author [@mrcrypster](https://github.com/mrcrypster) for the inspiration.

## Quick Start

```sh
# Upload with normal URL
curl bashupload.app -T file.txt

# Upload with short URL
curl bashupload.app/short -T file.txt
```

Use `alias` in bash to set quick upload

```sh
alias bashupload='curl bashupload.app -T'
alias bashuploadshort='curl bashupload.app/short -T'
bashupload file.txt        # Returns normal URL
bashuploadshort file.txt   # Returns short URL
```

To make the alias persistent, add it to your shell configuration file.

```sh
echo "alias bashupload='curl bashupload.app -T'" >> ~/.bashrc
echo "alias bashuploadshort='curl bashupload.app/short -T'" >> ~/.bashrc
source ~/.bashrc
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
- Support password setting for self-hosting

**Privacy Notice:** For your privacy and security, files are automatically deleted from our servers immediately after they are downloaded. Each file can only be downloaded once. Make sure to save the file locally after downloading, as the link will no longer work after the first download.

## Self-Hosting to Cloudflare

Click the "Deploy to Cloudflare" button above to modify the configuration.

`MAX_UPLOAD_SIZE` is in bytes (default is 5GB), and `MAX_AGE` is in seconds (default is 1 hour). You can adjust these values as needed.

`SHORT_URL_SERVICE` is the short URL service API endpoint (default is `https://suosuo.de/short`), you can change it to your own short URL service if needed. Only support [MyUrls](https://github.com/CareyWang/MyUrls).

`PASSWORD` environment variable is the password that must be provided for upload and download. If password protection is not needed, it can be left blank.

The final step of deployment may show a deployment failure error because the default configuration uses `bashupload.app` as the domain. In fact, the project has already been deployed successfully. You just need to bind your own domain in the Worker project settings.

## Password Protection

To enable password protection, set the `PASSWORD` environment variable in your Cloudflare Worker settings. When PASSWORD is set, both uploads and downloads will require the password to be provided in the Authorization header.

Example with curl:
```sh
# Upload with password
curl -H "Authorization: yourpassword" bashupload.app -T file.txt

# Download with password
curl -H "Authorization: yourpassword" https://bashupload.app/yourfile.txt -o downloaded.txt
```

Setting aliases with password:
```sh
echo "alias bashupload='curl -H \"Authorization: yourpassword\" bashupload.app -T'" >> ~/.bashrc
echo "alias bashuploadshort='curl -H \"Authorization: yourpassword\" bashupload.app/short -T'" >> ~/.bashrc
source ~/.bashrc
```