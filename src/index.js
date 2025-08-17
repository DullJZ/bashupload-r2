import mime from 'mime';

export default {
  async fetch(request, env, ctx) {
    const url = new URL(request.url);
    const pathname = url.pathname;

    // 处理 GET 请求
    if (request.method === 'GET') {
      // 根路径处理
      if (pathname === '/' || pathname === '') {
        // 检查 User-Agent 以确定是浏览器还是 curl
        const userAgent = request.headers.get('user-agent') || '';
        if (userAgent.toLowerCase().includes('curl')) {
        // 如果是 curl，返回简单的文本说明
        return new Response(`bashupload.app - 一次性文件分享服务\n\n使用方法 Usage:\n  curl bashupload.app -T file.txt\n\n特性 Features:\n  • 文件只能下载一次 / Files can only be downloaded once\n  • 下载后自动删除 / Auto-delete after download\n  • 保护隐私安全 / Privacy protection\n`, {
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
          const assetResponse = await env.ASSETS.fetch(`/${fileName}`, {});
          if (assetResponse.status === 200) {
            return assetResponse;
          }
        } catch (e) {
          console.error(`Error fetching asset ${fileName}:`, e);
        }
      }

      // 从 R2 获取文件
      if (fileName) {
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
          
          // 先获取文件内容
          const body = object.body;
          
          // 一次性下载：下载后立即删除文件
          // 使用 ctx.waitUntil 确保删除操作在响应发送后执行
          ctx.waitUntil(
            (async () => {
              try {
                // 小延迟，确保文件先被发送
                await new Promise(resolve => setTimeout(resolve, 100));
                await env.R2_BUCKET.delete(fileName);
                console.log(`已删除一次性文件: ${fileName}`);
              } catch (deleteError) {
                console.error(`删除文件 ${fileName} 失败:`, deleteError);
              }
            })()
          );
          
          // 添加响应头标识这是一次性下载
          headers.set('X-One-Time-Download', 'true');
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

    try {
      // 生成随机文件名
      const randomId = generateRandomId();
      const contentType = request.headers.get('content-type') || 'application/octet-stream';
      // 使用 mime.js 根据 Content-Type 获取扩展名
      const ext = mime.getExtension(contentType);
      const extension = ext ? `.${ext}` : '';
      const fileName = `${randomId}${extension}`;

      // 使用流式上传 - 直接传递 request.body 到 R2
      // 这样不会将整个文件加载到 Worker 内存中
      const uploadResult = await env.R2_BUCKET.put(fileName, request.body, {
        httpMetadata: {
          contentType: contentType,
        },
        // 添加自定义元数据，标记为一次性文件
        customMetadata: { 
          oneTime: 'true',
          uploadTime: new Date().toISOString()
        },
      });

      // 返回上传成功的 URL
      const url = new URL(request.url);
      const fileUrl = `${url.protocol}//${url.hostname}/${fileName}`;
      
      // 返回简单的文本响应，提醒用户这是一次性下载
      const responseText = `${fileUrl}\n\n⚠️  注意：此文件只能下载一次，下载后将自动删除！\n   Note: This file can only be downloaded once!\n`;
      
      return new Response(responseText, {
        status: 200,
        headers: {
          'Content-Type': 'text/plain; charset=utf-8',
          'X-One-Time-Upload': 'true',
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

