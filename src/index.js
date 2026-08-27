import mime from 'mime';
import { hmac } from '@noble/hashes/hmac.js';
import { sha256 } from '@noble/hashes/sha2.js';
import { bytesToHex } from '@noble/hashes/utils.js';

export default {
  // 处理定时任务
  async scheduled(event, env, ctx) {
    if (isCleanupDisabled(env?.NO_CLEANUP)) {
      console.log('[Scheduled Task] Skipped cleanup because NO_CLEANUP is enabled');
      return;
    }

    // 获取 MAX_AGE 配置（秒），默认 3600 秒（1小时）
    const maxAge = parseInt(env.MAX_AGE || '3600', 10);
    const maxAgeForMultiDownload = parseInt(env.MAX_AGE_FOR_MULTIDOWNLOAD || '86400', 10);
    const allowLifetimeOverMaxAge = env.ALLOW_LIFETIME_OVER_MAX_AGE === 'true';
    const now = Date.now();

    console.log(`[Scheduled Task] Start cleaning expired files, MAX_AGE: ${maxAge}s`);

    try {
      let deletedCount = 0;
      let checkedCount = 0;
      let cursor = undefined;

      // 分页处理文件列表，避免一次性加载过多文件
      do {
        // 每次最多处理 1000 个文件
        const listed = await env.R2_BUCKET.list({
          limit: 1000,
          cursor: cursor,
        });

        // 并行处理文件检查和删除，提高效率
        const deletePromises = [];

        for (const object of listed.objects) {
          checkedCount++;

          // Content-addressed blobs are shared by aliases. Reference-aware GC is
          // not implemented yet, so avoid both age deletion and unnecessary HEADs.
          if (object.key.startsWith('b/')) {
            continue;
          }

          // 创建异步删除任务
          const deleteTask = (async () => {
            try {
              // 获取文件的元数据
              const fileInfo = await env.R2_BUCKET.head(object.key);

              if (fileInfo) {
                if (object.key.startsWith('t/')) {
                  const uploadTime = fileInfo.customMetadata?.uploadTime
                    ? new Date(fileInfo.customMetadata.uploadTime).getTime()
                    : fileInfo.uploaded.getTime();
                  const age = now - uploadTime;

                  if (age > 60 * 60 * 1000) {
                    await env.R2_BUCKET.delete(object.key);
                    console.log(`[Scheduled Task] Deleted stale temp object: ${object.key}`);
                    return true;
                  }

                  return false;
                }

                // 检查文件是否有自定义的过期时间
                const expirationTime = fileInfo.customMetadata?.expirationTime;
                if (expirationTime) {
                  const now = new Date().getTime();
                  let expireAt = new Date(expirationTime).getTime();
                  // 兜底：即使元数据里的过期时间超长（如限制调整前上传的文件），
                  // 也按上传时间 + MAX_AGE_FOR_MULTIDOWNLOAD 强制封顶
                  if (!allowLifetimeOverMaxAge) {
                    const uploadTime = fileInfo.customMetadata?.uploadTime
                      ? new Date(fileInfo.customMetadata.uploadTime).getTime()
                      : fileInfo.uploaded.getTime();
                    expireAt = Math.min(expireAt, uploadTime + maxAgeForMultiDownload * 1000);
                  }
                  if (now > expireAt) {
                    await env.R2_BUCKET.delete(object.key);
                    console.log(`[Scheduled Task] Deleted expired file: ${object.key}, expiration: ${expirationTime}`);
                    return true;
                  }
                  // 文件未过期，跳过后续的 MAX_AGE 检查
                  return false;
                }

                // 获取文件上传时间
                // 优先使用自定义元数据中的 uploadTime，如果没有则使用 uploaded 时间
                const uploadTime = fileInfo.customMetadata?.uploadTime
                  ? new Date(fileInfo.customMetadata.uploadTime).getTime()
                  : fileInfo.uploaded.getTime();

                // 计算文件年龄（毫秒）
                const age = now - uploadTime;
                const ageInSeconds = Math.floor(age / 1000);

                // 如果文件年龄超过 MAX_AGE，删除文件
                if (ageInSeconds > maxAge) {
                  await env.R2_BUCKET.delete(object.key);
                  console.log(`[Scheduled Task] Deleted expired file: ${object.key}, age: ${ageInSeconds}s`);
                  return true; // 返回 true 表示删除了文件
                }
              }
            } catch (error) {
              console.error(`[Scheduled Task] Error processing file ${object.key}:`, error);
            }
            return false;
          })();

          deletePromises.push(deleteTask);
        }

        // 等待所有删除任务完成
        const results = await Promise.all(deletePromises);
        deletedCount += results.filter(deleted => deleted).length;

        // 更新游标以获取下一页
        cursor = listed.truncated ? listed.cursor : undefined;

      } while (cursor); // 如果还有更多文件，继续处理

      console.log(`[Scheduled Task] Cleanup complete: checked ${checkedCount} files, deleted ${deletedCount} expired files`);
    } catch (error) {
      console.error('[Scheduled Task] Error during cleanup:', error);
    }
  },

  async fetch(request, env, ctx) {
    const url = new URL(request.url);
    const pathname = url.pathname;

    // 处理 GET 请求
    if (request.method === 'GET') {
      // 获取服务端配置信息的API端点
      if (pathname === '/api/config') {
        const config = {
          maxAgeForMultiDownload: parseInt(env.MAX_AGE_FOR_MULTIDOWNLOAD || '86400', 10),
          maxUploadSize: parseInt(env.MAX_UPLOAD_SIZE || '5368709120', 10),
          maxAge: parseInt(env.MAX_AGE || '3600', 10),
          needPassword: Boolean(env.PASSWORD),
          enableDedup: env.ENABLE_DEDUP !== 'false' && Boolean(env.DEDUP_SECRET)
        };
        
        return new Response(JSON.stringify(config), {
          status: 200,
          headers: {
            'Content-Type': 'application/json',
            'Access-Control-Allow-Origin': '*',
            'Access-Control-Allow-Methods': 'GET',
            'Access-Control-Allow-Headers': 'Content-Type'
          }
        });
      }

      // 根路径处理
      if (pathname === '/' || pathname === '') {
        // 检查 User-Agent 以确定是浏览器还是 curl
        const userAgent = request.headers.get('user-agent') || '';
        if (userAgent.toLowerCase().includes('curl')) {
          // 如果是 curl，返回简单的文本说明
          return new Response(`bashupload.app - 一次性文件分享服务 | One-time File Sharing Service

使用方法 Usage:
  curl bashupload.app -T file.txt                    # 上传文件 / Upload file
  curl bashupload.app -d "text content"              # 上传文本 / Upload text (saved as .txt)
  curl bashupload.app/short -T file.txt              # 返回短链接 / Short URL
  curl -H "X-Expiration-Seconds: 3600" bashupload.app -T file.txt   # 设置有效期 / Set expiration time

特性 Features:
  • 文件只能下载一次 / Files can only be downloaded once (默认 default)
  • 可以设置有效期 / Can set expiration time for multiple downloads
  • 下载后自动删除 / Auto-delete after download or expiration
  • 保护隐私安全 / Privacy protection

有效期示例 Expiration Examples:
  • 3600 秒 (1小时) / 3600s (1 hour)
  • 7200 秒 (2小时) / 7200s (2 hours)
  • 86400 秒 (24小时) / 86400s (24 hours)
`, {
            status: 200,
            headers: { 'Content-Type': 'text/plain; charset=utf-8' },
          });
        }
        // 如果是浏览器，重定向到 index.html
        return Response.redirect(url.origin + '/index.html', 302);
      }

      // 处理静态资源路径映射
      let fileName = pathname.substring(1); // 移除开头的斜杠

      if (fileName === 'index.html' || fileName === 'style.css' || fileName === 'upload.js') {
        try {
          const assetResponse = await env.ASSETS.fetch(`https://assets.local/${fileName}`);
          if (assetResponse.status === 200) {
            return assetResponse;
          }
        } catch (e) {
          console.error(`Error fetching asset ${fileName}:`, e);
        }
      }

      // 从 R2 获取文件
      if (fileName) {
        // 检查密码保护
        if (env.PASSWORD) {
          const authHeader = request.headers.get('Authorization');
          let providedPassword = '';
          
          // 处理Basic认证格式 (Basic base64encode(username:password))
          if (authHeader && authHeader.startsWith('Basic ')) {
            try {
              const base64Credentials = authHeader.split(' ')[1];
              const credentials = atob(base64Credentials);
              const [username, password] = credentials.split(':');
              // 用户名可以为空，我们只关心密码
              providedPassword = password || '';
            } catch (e) {
              console.error('Error parsing Basic auth:', e);
            }
          } else {
            // 处理直接密码格式
            providedPassword = authHeader || '';
          }
          
          if (providedPassword !== env.PASSWORD) {
            return new Response('Unauthorized\n', { 
              status: 401,
              headers: { 'WWW-Authenticate': 'Basic realm="Password Required"' }
            });
          }
        }
        
        try {
          if (fileName.startsWith('a/')) {
            return await handleAliasDownload(fileName, env);
          }

          // Blob and temporary object names are internal implementation details.
          if (fileName.startsWith('b/') || fileName.startsWith('t/')) {
            return new Response('File not found\n', { status: 404 });
          }

          const object = await env.R2_BUCKET.get(fileName);
          if (!object) {
            return new Response('File not found\n', { status: 404 });
          }

          const headers = new Headers();
          object.writeHttpMetadata(headers);
          headers.set('etag', object.httpEtag);

          // 优先使用 R2 中保存的 Content-Type，再根据文件名猜测
          const contentType = headers.get('Content-Type') || mime.getType(fileName) || 'application/octet-stream';
          headers.set('Content-Type', contentType);

          // 检查文件元数据，确定是否是有效期模式
          const fileInfo = await env.R2_BUCKET.head(fileName);
          const isOneTime = !fileInfo?.customMetadata?.oneTime || fileInfo.customMetadata.oneTime === 'true';
          const expirationTime = fileInfo?.customMetadata?.expirationTime;

          // 如果有过期时间，检查是否已经过期
          if (expirationTime) {
            const now = new Date().getTime();
            let expireAt = new Date(expirationTime).getTime();
            // 兜底：按上传时间 + MAX_AGE_FOR_MULTIDOWNLOAD 强制封顶
            if (env.ALLOW_LIFETIME_OVER_MAX_AGE !== 'true') {
              const maxMulti = parseInt(env.MAX_AGE_FOR_MULTIDOWNLOAD || '86400', 10);
              const uploadTime = fileInfo.customMetadata?.uploadTime
                ? new Date(fileInfo.customMetadata.uploadTime).getTime()
                : fileInfo.uploaded.getTime();
              expireAt = Math.min(expireAt, uploadTime + maxMulti * 1000);
            }
            if (now > expireAt) {
              // 文件已过期，删除并返回404
              await env.R2_BUCKET.delete(fileName);
              console.log(`[Expired Download] Deleted expired file: ${fileName}`);
              return new Response('File not found (expired)\n', { status: 404 });
            }
          }

          // 先获取文件内容
          const body = object.body;

          // 只有在一次性下载模式下才删除文件
          if (isOneTime) {
            // 一次性下载：下载后立即删除文件
            // 使用 ctx.waitUntil 确保删除操作在响应发送后执行
            ctx.waitUntil(
              (async () => {
                try {
                  // 小延迟，确保文件先被发送
                  await new Promise(resolve => setTimeout(resolve, 100));
                  await env.R2_BUCKET.delete(fileName);
                  console.log(`[One-Time Download] Deleted file: ${fileName}`);
                } catch (deleteError) {
                  console.error(`[One-Time Download] Failed to delete file ${fileName}:`, deleteError);
                }
              })()
            );

            // 添加响应头标识这是一次性下载
            headers.set('X-One-Time-Download', 'true');
          } else {
            // 有效期模式
            headers.set('X-Expiration-Download', 'true');
            if (expirationTime) {
              headers.set('X-Expiration-Time', expirationTime);
            }
          }

          headers.set('Cache-Control', 'no-cache, no-store, must-revalidate');
          headers.set('Pragma', 'no-cache');
          headers.set('Expires', '0');

          return new Response(body, { headers });
        } catch (e) {
          return new Response(`Error: ${e.message}\n`, { status: 500 });
        }
      }
    }

    // 处理 PUT 和 POST 请求（curl -T 使用 PUT，curl -d 使用 POST）
    if (request.method !== 'PUT' && request.method !== 'POST') {
      return new Response('Method Not Allowed\n', { status: 405 });
    }

    // 上传限流：单 IP 固定频率（未配置 ratelimit 绑定时自动跳过）
    if (env.UPLOAD_RATE_LIMITER) {
      const clientIP = getClientIP(request);
      try {
        const { success } = await env.UPLOAD_RATE_LIMITER.limit({ key: clientIP });
        if (!success) {
          console.log(`[Rate Limit] Blocked upload from ip=${clientIP}`);
          return new Response('Upload rate limit exceeded, please retry after 60 seconds.\n上传过于频繁，请在 60 秒后重试。\n', {
            status: 429,
            headers: {
              'Content-Type': 'text/plain; charset=utf-8',
              'Retry-After': '60',
            },
          });
        }
      } catch (e) {
        // 限流服务异常时放行（fail-open），避免限流故障导致上传不可用
        console.error('Rate limiter error:', e);
      }
    }

    // 检查密码保护
    if (env.PASSWORD) {
      const authHeader = request.headers.get('Authorization');
      let providedPassword = '';
      
      // 处理Basic认证格式 (Basic base64encode(username:password))
      if (authHeader && authHeader.startsWith('Basic ')) {
        try {
          const base64Credentials = authHeader.split(' ')[1];
          const credentials = atob(base64Credentials);
          const [username, password] = credentials.split(':');
          // 用户名可以为空，我们只关心密码
          providedPassword = password || '';
        } catch (e) {
          console.error('Error parsing Basic auth:', e);
        }
      } else {
        // 处理直接密码格式
        providedPassword = authHeader || '';
      }
      
      if (providedPassword !== env.PASSWORD) {
        return new Response('Unauthorized\n', { 
          status: 401
        });
      }
    }

    const maxUploadSize = parseInt(env.MAX_UPLOAD_SIZE || '5368709120', 10);

    try {
      // 检查是否是 /short 路径，如果是则强制使用短链接
      const forceShortUrl = pathname === '/short' || pathname.startsWith('/short/');
      // 检查 Content-Length
      const contentLengthHeader = request.headers.get('content-length');
      let parsedContentLength = null;
      if (contentLengthHeader) {
        const contentLength = parseInt(contentLengthHeader, 10);
        if (!isNaN(contentLength) && contentLength > maxUploadSize) {
          return new Response(`Upload failed: file too large. Max size is ${formatBytes(maxUploadSize)}.\n`, {
            status: 413,
            headers: { 'Content-Type': 'text/plain; charset=utf-8' },
          });
        }
        if (!isNaN(contentLength) && contentLength >= 0) {
          parsedContentLength = contentLength;
        }
      }

      // 获取有效期参数（秒）
      const expirationSeconds = request.headers.get('X-Expiration-Seconds');
      const hasExpiration = expirationSeconds && !isNaN(parseInt(expirationSeconds, 10)) && parseInt(expirationSeconds, 10) > 0;
      let expirationTime = hasExpiration ? parseInt(expirationSeconds, 10) : null;

      // 服务端强制限制有效期上限，防止绕过前端设置超长有效期
      const maxAgeForMultiDownload = parseInt(env.MAX_AGE_FOR_MULTIDOWNLOAD || '86400', 10);
      const allowLifetimeOverMaxAge = env.ALLOW_LIFETIME_OVER_MAX_AGE === 'true';
      let expirationLimited = false;
      if (hasExpiration && !allowLifetimeOverMaxAge && expirationTime > maxAgeForMultiDownload) {
        expirationTime = maxAgeForMultiDownload;
        expirationLimited = true;
      }
      const isOneTime = !hasExpiration;

      const enableDedup = env.ENABLE_DEDUP !== 'false';
      const dedupSecret = typeof env.DEDUP_SECRET === 'string' ? env.DEDUP_SECRET : '';
      const canDedup = enableDedup && dedupSecret.length > 0 && hasExpiration;
      const declaredHashHeader = (request.headers.get('X-Content-SHA256') || '').trim();
      const declaredHash = declaredHashHeader.toLowerCase();

      if (declaredHashHeader && !isValidSha256Hex(declaredHashHeader)) {
        return new Response('Upload failed: invalid X-Content-SHA256 header.\n', {
          status: 400,
          headers: { 'Content-Type': 'text/plain; charset=utf-8' },
        });
      }

      if (enableDedup && hasExpiration && !dedupSecret) {
        console.warn('[Dedup] DEDUP_SECRET is missing; using random-key upload for this request');
      }

      let contentType = request.headers.get('content-type') || 'application/octet-stream';
      let extension = '';

      // 如果是 POST 请求（curl -d），强制使用 .txt 扩展名和 text/plain content-type
      if (request.method === 'POST') {
        contentType = 'text/plain; charset=utf-8';
        extension = '.txt';
      } else {
        // PUT 请求：使用 mime.js 根据 Content-Type 获取扩展名
        const ext = mime.getExtension(contentType);
        extension = ext ? `.${ext}` : '';
      }

      const customMetadata = {
        oneTime: isOneTime ? 'true' : 'false',
        uploadTime: new Date().toISOString()
      };

      // 如果有有效期，添加到元数据中
      if (hasExpiration) {
        customMetadata.expirationTime = new Date(Date.now() + expirationTime * 1000).toISOString();
        customMetadata.expirationSeconds = expirationTime.toString();
      }

      if (canDedup) {
        const clientIP = getClientIP(request);
        const tempKey = getTempKey();
        const { stream: limitedBody, getBytesRead } = createSizeLimitedStream(request.body, maxUploadSize);
        const { stream: hashingBody, digest } = createHashingStream(limitedBody);
        digest.catch(() => {});
        let tempResult;
        try {
          tempResult = await env.R2_BUCKET.put(tempKey, hashingBody, {
            httpMetadata: { contentType },
            customMetadata: { uploadTime: new Date().toISOString() },
          });
          const actualHash = await digest;
          if (declaredHash && actualHash !== declaredHash) {
            await env.R2_BUCKET.delete(tempKey);
            return hashMismatchResponse();
          }

          const blobKey = getDedupKey(actualHash, dedupSecret);
          const existingInfo = await env.R2_BUCKET.head(blobKey);
          const dedupHit = Boolean(existingInfo);

          if (!existingInfo) {
            const tempObject = await env.R2_BUCKET.get(tempKey);
            if (!tempObject) throw new Error('Temporary upload object not found');
            await env.R2_BUCKET.put(blobKey, tempObject.body, {
              httpMetadata: { contentType: 'application/octet-stream' },
              customMetadata: {
                uploadTime: new Date().toISOString(),
                contentSha256: actualHash,
              },
            });
          }
          await env.R2_BUCKET.delete(tempKey);

          const uploadedSize = getUploadedSize(tempResult, null, parsedContentLength, getBytesRead());
          const aliasKey = getAliasKey();
          const aliasRecord = {
            version: 1,
            blobKey,
            createdAt: customMetadata.uploadTime,
            expiresAt: customMetadata.expirationTime,
            contentType,
            size: uploadedSize,
          };
          await env.R2_BUCKET.put(aliasKey, JSON.stringify(aliasRecord), {
            httpMetadata: { contentType: 'application/json; charset=utf-8' },
            customMetadata: {
              uploadTime: aliasRecord.createdAt,
              expirationTime: aliasRecord.expiresAt,
              blobKey,
            },
          });

          console.log(`[Dedup ${dedupHit ? 'Hit' : 'Upload'}] blob=${blobKey} alias=${aliasKey} size=${formatBytes(uploadedSize)} ip=${clientIP} oneTime=false`);
          const responseText = await buildUploadResponseText(request, env, aliasKey, forceShortUrl, true, expirationTime, expirationLimited);
          const headers = {
            'Content-Type': 'text/plain; charset=utf-8',
            'X-One-Time-Upload': 'false',
            'X-Dedup-Hit': dedupHit ? 'true' : 'false',
          };
          return new Response(responseText, { status: 200, headers });
        } catch (error) {
          await env.R2_BUCKET.delete(tempKey).catch(() => {});
          throw error;
        }
      }

      // 生成随机文件名。一次性上传永远不去重。
      const randomId = generateRandomId();
      const fileName = `${randomId}${extension}`;
      const { stream: limitedBody, getBytesRead } = createSizeLimitedStream(request.body, maxUploadSize);
      let uploadBody = limitedBody;
      let declaredHashDigest = null;
      if (declaredHash) {
        const hashing = createHashingStream(limitedBody);
        uploadBody = hashing.stream;
        declaredHashDigest = hashing.digest;
        declaredHashDigest.catch(() => {});
      }

      const uploadResult = await env.R2_BUCKET.put(fileName, uploadBody, {
        httpMetadata: {
          contentType: contentType,
        },
        customMetadata: customMetadata,
      });
      if (declaredHashDigest && await declaredHashDigest !== declaredHash) {
        await env.R2_BUCKET.delete(fileName);
        return hashMismatchResponse();
      }

      const uploadedSize =
        uploadResult && typeof uploadResult.size === 'number'
          ? uploadResult.size
          : typeof parsedContentLength === 'number'
            ? parsedContentLength
            : getBytesRead();
      const sizeLabel =
        typeof uploadedSize === 'number' ? formatBytes(uploadedSize) : 'unknown';
      const clientIP = getClientIP(request);

      console.log(`[Upload] key=${fileName} size=${sizeLabel} ip=${clientIP} oneTime=${isOneTime}`);

      const responseText = await buildUploadResponseText(request, env, fileName, forceShortUrl, hasExpiration, expirationTime, expirationLimited);

      return new Response(responseText, {
        status: 200,
        headers: {
          'Content-Type': 'text/plain; charset=utf-8',
          'X-One-Time-Upload': isOneTime ? 'true' : 'false',
        },
      });
    } catch (e) {
      if (isUploadTooLargeError(e)) {
        return new Response(`Upload failed: file too large. Max size is ${formatBytes(maxUploadSize)}.\n`, {
          status: 413,
          headers: {
            'Content-Type': 'text/plain; charset=utf-8',
          },
        });
      }

      console.error('Upload error:', e);
      return new Response(`Upload failed: ${e.message}\n`, {
        status: 500,
        headers: {
          'Content-Type': 'text/plain',
        },
      });
    }
  },
};

function isCleanupDisabled(value) {
  if (value === undefined || value === null) {
    return false;
  }

  if (typeof value === 'string') {
    const normalized = value.toLowerCase().trim();
    return normalized === '1' || normalized === 'true';
  }

  if (typeof value === 'number') {
    return value === 1;
  }

  return value === true;
}

// 生成随机 ID
function generateRandomId() {
  const chars = 'abcdefghijklmnopqrstuvwxyz0123456789';
  let result = '';
  for (let i = 0; i < 6; i++) {
    result += chars.charAt(Math.floor(Math.random() * chars.length));
  }
  return result;
}

// 获取客户端 IP（CF-Connecting-IP 由 Cloudflare 边缘设置，不可伪造）
function getClientIP(request) {
  const xForwardedFor = request.headers.get('X-Forwarded-For');
  const forwardedIP = xForwardedFor ? xForwardedFor.split(',')[0].trim() : '';
  return (
    request.headers.get('CF-Connecting-IP') ||
    request.headers.get('X-Real-IP') ||
    forwardedIP ||
    'unknown'
  );
}

function isValidSha256Hex(value) {
  return typeof value === 'string' && /^[a-f0-9]{64}$/i.test(value);
}

function getDedupKey(hash, secret) {
  if (!secret) {
    throw new Error('DEDUP_SECRET is required for deduplication');
  }
  const normalizedHash = hash.toLowerCase();
  const encoder = new TextEncoder();
  return `b/${bytesToHex(hmac(sha256, encoder.encode(secret), encoder.encode(normalizedHash)))}`;
}

function getTempKey() {
  return `t/${crypto.randomUUID()}`;
}

function getAliasKey() {
  return `a/${crypto.randomUUID()}`;
}

async function handleAliasDownload(aliasKey, env) {
  const aliasObject = await env.R2_BUCKET.get(aliasKey);
  if (!aliasObject) {
    return new Response('File not found\n', { status: 404 });
  }

  let alias;
  try {
    alias = JSON.parse(await aliasObject.text());
  } catch (error) {
    console.error(`[Alias Download] Invalid alias JSON: ${aliasKey}`, error);
    return new Response('File not found\n', { status: 404 });
  }

  if (!isValidAliasRecord(alias)) {
    console.error(`[Alias Download] Invalid alias record: ${aliasKey}`);
    return new Response('File not found\n', { status: 404 });
  }

  const expiresAt = new Date(alias.expiresAt).getTime();
  if (Date.now() > expiresAt) {
    await env.R2_BUCKET.delete(aliasKey);
    console.log(`[Alias Download] Deleted expired alias: ${aliasKey}`);
    return new Response('File not found (expired)\n', { status: 404 });
  }

  const blob = await env.R2_BUCKET.get(alias.blobKey);
  if (!blob) {
    console.error(`[Alias Download] Missing blob ${alias.blobKey} for alias ${aliasKey}`);
    return new Response('File not found\n', { status: 404 });
  }

  const headers = new Headers({
    'Content-Type': alias.contentType,
    'Cache-Control': 'no-cache, no-store, must-revalidate',
    'Pragma': 'no-cache',
    'Expires': '0',
    'X-Content-Type-Options': 'nosniff',
    'X-Expiration-Download': 'true',
    'X-Expiration-Time': alias.expiresAt,
  });
  if (blob.httpEtag) {
    headers.set('etag', blob.httpEtag);
  }
  if (Number.isSafeInteger(alias.size) && alias.size >= 0) {
    headers.set('Content-Length', String(alias.size));
  }

  return new Response(blob.body, { headers });
}

function isValidAliasRecord(alias) {
  if (!alias || alias.version !== 1) {
    return false;
  }

  if (typeof alias.blobKey !== 'string' || !/^b\/[a-f0-9]{64}$/.test(alias.blobKey)) {
    return false;
  }

  if (typeof alias.expiresAt !== 'string' || !Number.isFinite(new Date(alias.expiresAt).getTime())) {
    return false;
  }

  return typeof alias.contentType === 'string' && alias.contentType.length > 0;
}

function makeFileUrl(request, key) {
  const url = new URL(request.url);
  return `${url.protocol}//${url.host}/${key}`;
}

async function buildUploadResponseText(request, env, fileName, forceShortUrl, hasExpiration, expirationTime, expirationLimited) {
  let fileUrl = makeFileUrl(request, fileName);

  // 如果使用 /short 路径，尝试生成短链接
  if (forceShortUrl) {
    try {
      // 将长链接转换为 base64
      const base64Url = btoa(fileUrl);

      // 调用短链接 API
      const shortUrlResponse = await fetch(env.SHORT_URL_SERVICE || 'https://suosuo.de/short', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/x-www-form-urlencoded',
        },
        body: `longUrl=${encodeURIComponent(base64Url)}`,
      });

      if (shortUrlResponse.ok) {
        const shortUrlData = await shortUrlResponse.json();
        if (shortUrlData.Code === 1 && shortUrlData.ShortUrl) {
          fileUrl = shortUrlData.ShortUrl;
          console.log(`Generated short URL: ${fileUrl} for original: ${makeFileUrl(request, fileName)}`);
        } else if (forceShortUrl) {
          console.warn(`Short URL API returned unexpected response: ${JSON.stringify(shortUrlData)}`);
        }
      }
    } catch (error) {
      console.error('Failed to generate short URL:', error);
      // 如果是 /short 路径但短链接生成失败，提示用户
      if (forceShortUrl) {
        console.warn('Short URL was requested via /short but generation failed, falling back to original URL');
      }
      // 继续使用原始链接
    }
  }

  // 根据是否有有效期返回不同的文本提示
  if (hasExpiration) {
    const expirationHours = Math.floor(expirationTime / 3600);
    const expirationMinutes = Math.floor((expirationTime % 3600) / 60);
    const expirationString = expirationHours > 0
      ? `${expirationHours}小时${expirationMinutes > 0 ? expirationMinutes + '分钟' : ''}`
      : `${expirationMinutes}分钟`;
    let responseText = `\n\n${fileUrl}\n\n🕐 注意：此文件将在 ${expirationString} 后过期，期间可以多次下载。\n   Note: This file will expire after ${expirationString} and can be downloaded multiple times.\n`;
    if (expirationLimited) {
      responseText += `⚠️  请求的有效期超过服务器上限，已调整为 ${expirationString}。\n   Requested expiration exceeded the server limit and was reduced to ${expirationString}.\n`;
    }
    return responseText;
  }

  return `\n\n${fileUrl}\n\n⚠️  注意：此文件只能下载一次，下载后将自动删除！\n   Note: This file can only be downloaded once!\n`;
}

function getUploadedSize(uploadResult, fallbackSize, parsedContentLength, bytesRead) {
  if (uploadResult && typeof uploadResult.size === 'number') {
    return uploadResult.size;
  }
  if (typeof fallbackSize === 'number') {
    return fallbackSize;
  }
  if (typeof parsedContentLength === 'number') {
    return parsedContentLength;
  }
  return bytesRead;
}

function hashMismatchResponse() {
  return new Response('Upload failed: SHA-256 mismatch.\n', {
    status: 409,
    headers: {
      'Content-Type': 'text/plain; charset=utf-8',
    },
  });
}

// 格式化字节数为可读字符串
function formatBytes(bytes) {
  if (bytes === 0) return '0B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + sizes[i];
}

class UploadTooLargeError extends Error {
  constructor(maxBytes) {
    super(`Upload failed: file too large. Max size is ${formatBytes(maxBytes)}.`);
    this.name = 'UploadTooLargeError';
    this.maxBytes = maxBytes;
  }
}

function isUploadTooLargeError(error) {
  if (!error) {
    return false;
  }

  if (error instanceof UploadTooLargeError || error.name === 'UploadTooLargeError') {
    return true;
  }

  return isUploadTooLargeError(error.cause);
}

function createSizeLimitedStream(stream, maxBytes) {
  if (!stream) {
    return {
      stream,
      getBytesRead: () => 0,
    };
  }

  let bytesRead = 0;
  let streamClosed = false;
  const reader = stream.getReader();

  const limitedStream = new ReadableStream({
    async pull(controller) {
      const { done, value } = await reader.read();

      if (done) {
        streamClosed = true;
        controller.close();
        return;
      }

      const chunkSize = value?.byteLength ?? value?.length ?? 0;
      bytesRead += chunkSize;

      if (bytesRead > maxBytes) {
        const error = new UploadTooLargeError(maxBytes);
        streamClosed = true;

        try {
          await reader.cancel(error);
        } catch (cancelError) {
          console.warn('Failed to cancel oversized upload stream:', cancelError);
        }

        controller.error(error);
        return;
      }

      controller.enqueue(value);
    },
    async cancel(reason) {
      if (streamClosed) {
        return;
      }

      streamClosed = true;
      await reader.cancel(reason);
    },
  });

  return {
    stream: limitedStream,
    getBytesRead: () => bytesRead,
  };
}

function createHashingStream(stream) {
  const hasher = sha256.create();
  let resolveDigest;
  let rejectDigest;
  const digest = new Promise((resolve, reject) => {
    resolveDigest = resolve;
    rejectDigest = reject;
  });

  if (!stream) {
    resolveDigest(bytesToHex(hasher.digest()));
    return { stream, digest };
  }

  const reader = stream.getReader();
  let streamClosed = false;

  const hashingStream = new ReadableStream({
    async pull(controller) {
      try {
        const { done, value } = await reader.read();

        if (done) {
          streamClosed = true;
          resolveDigest(bytesToHex(hasher.digest()));
          controller.close();
          return;
        }

        hasher.update(value);
        controller.enqueue(value);
      } catch (error) {
        streamClosed = true;
        rejectDigest(error);
        controller.error(error);
      }
    },
    async cancel(reason) {
      if (!streamClosed) {
        streamClosed = true;
        await reader.cancel(reason);
      }
      rejectDigest(reason instanceof Error ? reason : new Error(String(reason || 'Hashing stream cancelled')));
    },
  });

  return {
    stream: hashingStream,
    digest,
  };
}
