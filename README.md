# BashUpload-R2

English | [中文](README-zh.md)

Simple file upload service based on Cloudflare Workers and Cloudflare R2 object storage for the command line and browser.

[![Deploy to Cloudflare](https://deploy.workers.cloudflare.com/button)](https://deploy.workers.cloudflare.com/?url=https://github.com/DullJZ/bashupload-r2)

Directly Use: [bashupload.app](https://bashupload.app)

Thanks to [bashupload.com](https://bashupload.com) and its author [@mrcrypster](https://github.com/mrcrypster) for the inspiration.

## Quick Start

```sh
# Upload file with normal URL
curl bashupload.app -T file.txt

# Upload text content (saved as .txt file)
curl bashupload.app -d "Your long text content here"

# Upload with short URL
curl bashupload.app/short -T file.txt

# Upload with custom expiration time (86400 seconds = 24 hours, allows multiple downloads)
curl -H "X-Expiration-Seconds: 86400" bashupload.app -T file.txt
```

Use `alias` in bash to set quick upload

```sh
alias bashupload='curl bashupload.app -T'
alias bashuploadtext='curl bashupload.app -d'
alias bashuploadshort='curl bashupload.app/short -T'
alias bashuploadexpire='curl -H "X-Expiration-Seconds: 3600" bashupload.app -T'
bashupload file.txt              # Returns normal URL
bashuploadtext "your text here"  # Upload text content
bashuploadshort file.txt         # Returns short URL
bashuploadexpire file.txt        # Returns URL with 1 hour expiration
```

To make the alias persistent, add it to your shell configuration file.

```sh
echo "alias bashupload='curl bashupload.app -T'" >> ~/.bashrc
echo "alias bashuploadtext='curl bashupload.app -d'" >> ~/.bashrc
echo "alias bashuploadshort='curl bashupload.app/short -T'" >> ~/.bashrc
echo "alias bashuploadexpire='curl -H \"X-Expiration-Seconds: 3600\" bashupload.app -T'" >> ~/.bashrc
source ~/.bashrc
```

## Browser Upload

- Drag & drop files or click to select files
- Set custom expiration time for files
- Direct download links
- No registration required

## Features

- Simple command-line interface
- Quick text sharing
- Browser-based drag & drop upload
- No registration required
- Direct download links
- Privacy-focused: Files are automatically deleted after download
- Secure file storage with one-time download
- Custom expiration time support: Set file expiration to allow multiple downloads within specified time
- Supports files up to 5GB in size (self-hosting can adjust this limit)
- Support password setting for self-hosting
- Per-IP upload rate limiting (default: 10 uploads per minute) to prevent abuse

**Privacy Notice:** For your privacy and security, files are automatically deleted from our servers immediately after they are downloaded. Each file can only be downloaded once, **unless you set an expiration time**. When an expiration time is set, the file can be downloaded multiple times until it expires. Make sure to save the file locally after downloading, as the link will no longer work after the first download (for one-time downloads) or after expiration (for time-limited downloads).

## Self-Hosting to Cloudflare

Click the "Deploy to Cloudflare" button above to modify the configuration.

`MAX_UPLOAD_SIZE` is in bytes (default is 5GB), and `MAX_AGE` is in seconds (default is 1 hour). You can adjust these values as needed.

`MAX_AGE_FOR_MULTIDOWNLOAD` is the maximum expiration time allowed for multiple downloads in seconds (default is 86400, which is 24 hours). Users can set custom expiration times up to this limit. This limit is enforced server-side: expiration times exceeding it (e.g. sent directly via `X-Expiration-Seconds`) are automatically reduced to this value unless `ALLOW_LIFETIME_OVER_MAX_AGE` is `true`.

`ENABLE_DEDUP` controls SHA-256 content-hash deduplication for uploads with expiration times. It defaults to `true`; the browser always uploads the complete file, and the server computes the hash and deduplicates storage after receiving it. Deduplication is active only when `DEDUP_SECRET` is also configured. One-time downloads continue to use random object keys.

`DEDUP_SECRET` is required to enable secure deduplication. Use a random value of at least 32 bytes. When set, the server computes SHA-256 after fully receiving an expiration upload and derives an internal blob key with `HMAC-SHA256(DEDUP_SECRET, content hash)`. Identical bytes share one immutable `b/` blob, while every upload receives a new random `a/` alias containing its own expiration time, content type, and blob reference. Download URLs expose only aliases, so expiring one upload does not invalidate another alias for the same content. Alias objects are deleted after expiration. Shared blobs are reclaimed by the write-freezing maintenance procedure below; routine cleanup deliberately skips `b/` because it cannot prove that a shared blob has no aliases while uploads are active. If the secret is missing, uploads fall back to random keys without deduplication.

`X-Content-SHA256` is an optional integrity declaration. It is not required for deduplication; the server computes and verifies the content hash itself.

For Workers, keep `DEDUP_SECRET` out of `wrangler.toml` and configure it with `npx wrangler secret put DEDUP_SECRET`. For local development, put it in `.dev.vars` and do not commit that file. Rotating the secret creates a new deduplication namespace; existing aliases remain usable because they retain their complete blob reference.

Existing `c/` links created by the earlier deduplication format remain downloadable and follow their original expiration metadata until cleanup removes them.

### Shared-blob garbage collection (maintenance window)

The Go command performs a complete `a/` to `b/` reference scan while every writer is stopped. Run `./bashupload gc --offline --dry-run` first, review the report, then explicitly run `./bashupload gc --offline --delete` only after resolving all scan errors. The full stop, dry-run, delete, restart, and request-cost runbook is documented in [the Go deployment guide](docker/go/README.md#shared-blob-garbage-collection-maintenance-window).

The freeze must include every Go/Worker instance, old binary, upload script, and scheduled job that can mutate R2; downloads can remain read-only. The maintenance command only scans `a/` and `b/`, deletes unreferenced `b/` objects in batches, and does not delete expired aliases. Routine cleanup continues to handle aliases, temporary objects, and legacy `c/` objects. Existing `c/` links remain compatible. If any writer cannot be stopped, do not use `--delete`.

`SHORT_URL_SERVICE` is the short URL service API endpoint (default is `https://suosuo.de/short`), you can change it to your own short URL service if needed. Only support [MyUrls](https://github.com/CareyWang/MyUrls).

`PASSWORD` environment variable is the password that must be provided for upload and download. If password protection is not needed, it can be left blank.

Uploads are rate-limited per client IP (default: 10 uploads per 60 seconds; requests over the limit receive HTTP 429). The Worker version configures this via the `[[ratelimits]]` binding in `wrangler.toml` (remove the binding to disable; requires a recent wrangler — older 4.x versions need the equivalent `[[unsafe.bindings]]` with `type = "ratelimit"` syntax). The Docker versions use the `UPLOAD_RATE_LIMIT` / `UPLOAD_RATE_LIMIT_WINDOW` environment variables (`UPLOAD_RATE_LIMIT=0` disables), plus `TRUST_PROXY_HEADERS=false` when the service is exposed directly without a reverse proxy.

The final step of deployment may show a deployment failure error because the default configuration uses `bashupload.app` as the domain. In fact, the project has already been deployed successfully. You just need to bind your own domain in the Worker project settings.

## Advanced Features

### Custom Expiration Time

You can set a custom expiration time for uploaded files by using the `X-Expiration-Seconds` header. This allows the file to be downloaded multiple times until it expires, after which it will be automatically deleted.

Example:
```sh
# Set 1-hour expiration (file can be downloaded multiple times for 1 hour)
curl -H "X-Expiration-Seconds: 3600" bashupload.app -T file.txt

# Set 24-hour expiration
curl -H "X-Expiration-Seconds: 86400" bashupload.app -T file.txt

# Set 7-day expiration
curl -H "X-Expiration-Seconds: 604800" bashupload.app -T file.txt
```

**Important Notes:**
- Without expiration time, files can only be downloaded once (one-time download)
- With expiration time, files can be downloaded multiple times until expiration
- The maximum allowed expiration time is controlled by `MAX_AGE_FOR_MULTIDOWNLOAD` (default: 24 hours)
- Browser upload also supports setting expiration times through the UI

### Quick Text Sharing

You can quickly share long text snippets, code, logs, or any text content without creating a file first. Simply use `curl -d` to upload text directly, and it will be saved as a `.txt` file.

Example:
```sh
# Share a quick text snippet
curl bashupload.app -d "Here's the error message I'm getting..."

# Share code snippet
curl bashupload.app -d "$(cat script.sh)"

# Share command output
curl bashupload.app -d "$(ls -la)"

# Share with expiration time for multiple views
curl -H "X-Expiration-Seconds: 3600" bashupload.app -d "Meeting notes for today..."

# Combine with short URL for easier sharing
curl bashupload.app/short -d "Your text content here"
```

### Password Protection

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
