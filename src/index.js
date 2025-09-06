import mime from 'mime';

export default {
  // 处理定时任务
  async scheduled(event, env, ctx) {
    // 获取 MAX_AGE 配置（秒），默认 3600 秒（1小时）
    const maxAge = parseInt(env.MAX_AGE || '3600', 10);
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

          // 创建异步删除任务
          const deleteTask = (async () => {
            try {
              // 获取文件的元数据
              const fileInfo = await env.R2_BUCKET.head(object.key);

              if (fileInfo) {
                // 检查文件是否有自定义的过期时间
                const expirationTime = fileInfo.customMetadata?.expirationTime;
                if (expirationTime) {
                  const now = new Date().getTime();
                  const expireAt = new Date(expirationTime).getTime();
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
          maxAge: parseInt(env.MAX_AGE || '3600', 10)
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
  curl bashupload.app -T file.txt                    # 返回普通链接 / Normal URL
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
          const object = await env.R2_BUCKET.get(fileName);
          if (!object) {
            return new Response('File not found\n', { status: 404 });
          }

          const headers = new Headers();
          object.writeHttpMetadata(headers);
          headers.set('etag', object.httpEtag);

          // 使用 mime.js 根据文件名获取 Content-Type
          const contentType = mime.getType(fileName) || 'application/octet-stream';
          headers.set('Content-Type', contentType);

          // 检查文件元数据，确定是否是有效期模式
          const fileInfo = await env.R2_BUCKET.head(fileName);
          const isOneTime = !fileInfo?.customMetadata?.oneTime || fileInfo.customMetadata.oneTime === 'true';
          const expirationTime = fileInfo?.customMetadata?.expirationTime;

          // 如果有过期时间，检查是否已经过期
          if (expirationTime) {
            const now = new Date().getTime();
            const expireAt = new Date(expirationTime).getTime();
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

    // 处理 PUT 请求（curl -T 使用 PUT）
    if (request.method !== 'PUT') {
      return new Response('Method Not Allowed\n', { status: 405 });
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

    try {
      // 检查是否是 /short 路径，如果是则强制使用短链接
      const forceShortUrl = pathname === '/short' || pathname.startsWith('/short/');
      // 获取最大上传大小（字节），默认 5GB
      const maxUploadSize = parseInt(env.MAX_UPLOAD_SIZE || '5368709120', 10);
      // 检查 Content-Length
      const contentLengthHeader = request.headers.get('content-length');
      if (contentLengthHeader) {
        const contentLength = parseInt(contentLengthHeader, 10);
        if (!isNaN(contentLength) && contentLength > maxUploadSize) {
          return new Response(`Upload failed: file too large. Max size is ${formatBytes(maxUploadSize)}.\n`, {
            status: 413,
            headers: { 'Content-Type': 'text/plain; charset=utf-8' },
          });
        }
      }

      // 获取有效期参数（秒）
      const expirationSeconds = request.headers.get('X-Expiration-Seconds');
      const hasExpiration = expirationSeconds && !isNaN(parseInt(expirationSeconds, 10)) && parseInt(expirationSeconds, 10) > 0;
      const expirationTime = hasExpiration ? parseInt(expirationSeconds, 10) : null;

      // 生成随机文件名
      const randomId = generateRandomId();
      const contentType = request.headers.get('content-type') || 'application/octet-stream';
      // 使用 mime.js 根据 Content-Type 获取扩展名
      const ext = mime.getExtension(contentType);
      const extension = ext ? `.${ext}` : '';
      const fileName = `${randomId}${extension}`;

      // 使用流式上传 - 直接传递 request.body 到 R2
      // 这样不会将整个文件加载到 Worker 内存中
      const customMetadata = {
        oneTime: hasExpiration ? 'false' : 'true',
        uploadTime: new Date().toISOString()
      };

      // 如果有有效期，添加到元数据中
      if (hasExpiration) {
        customMetadata.expirationTime = new Date(Date.now() + expirationTime * 1000).toISOString();
        customMetadata.expirationSeconds = expirationTime.toString();
      }

      const uploadResult = await env.R2_BUCKET.put(fileName, request.body, {
        httpMetadata: {
          contentType: contentType,
        },
        customMetadata: customMetadata,
      });

      // 返回上传成功的 URL
      const url = new URL(request.url);
      let fileUrl = `${url.protocol}//${url.hostname}/${fileName}`;

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
              console.log(`Generated short URL: ${fileUrl} for original: ${url.protocol}//${url.hostname}/${fileName}`);
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
      let responseText;
      if (hasExpiration) {
        const expirationHours = Math.floor(expirationTime / 3600);
        const expirationMinutes = Math.floor((expirationTime % 3600) / 60);
        const expirationString = expirationHours > 0 
          ? `${expirationHours}小时${expirationMinutes > 0 ? expirationMinutes + '分钟' : ''}`
          : `${expirationMinutes}分钟`;
        responseText = `\n\n${fileUrl}\n\n🕐 注意：此文件将在 ${expirationString} 后过期，期间可以多次下载。\n   Note: This file will expire after ${expirationString} and can be downloaded multiple times.\n`;
      } else {
        responseText = `\n\n${fileUrl}\n\n⚠️  注意：此文件只能下载一次，下载后将自动删除！\n   Note: This file can only be downloaded once!\n`;
      }

      return new Response(responseText, {
        status: 200,
        headers: {
          'Content-Type': 'text/plain; charset=utf-8',
          'X-One-Time-Upload': hasExpiration ? 'false' : 'true',
        },
      });
    } catch (e) {
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

// 生成随机 ID
function generateRandomId() {
  const chars = 'abcdefghijklmnopqrstuvwxyz0123456789';
  let result = '';
  for (let i = 0; i < 6; i++) {
    result += chars.charAt(Math.floor(Math.random() * chars.length));
  }
  return result;
}

// 格式化字节数为可读字符串
function formatBytes(bytes) {
  if (bytes === 0) return '0B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + sizes[i];
}

