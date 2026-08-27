import assert from 'node:assert/strict';
import test from 'node:test';

import worker from '../src/index.js';

class MemoryR2Bucket {
  constructor() {
    this.objects = new Map();
    this.headKeys = [];
  }

  async put(key, body, options = {}) {
    const data = new Uint8Array(await new Response(body ?? '').arrayBuffer());
    const object = {
      data,
      uploaded: new Date(),
      httpMetadata: options.httpMetadata || {},
      customMetadata: options.customMetadata || {},
      httpEtag: `"${key}-${data.byteLength}"`,
    };
    this.objects.set(key, object);
    return this.describe(key, object);
  }

  async get(key) {
    const object = this.objects.get(key);
    if (!object) return null;
    return {
      ...this.describe(key, object),
      body: new Blob([object.data]).stream(),
      text: async () => new TextDecoder().decode(object.data),
      writeHttpMetadata: headers => {
        if (object.httpMetadata.contentType) {
          headers.set('Content-Type', object.httpMetadata.contentType);
        }
      },
    };
  }

  async head(key) {
    this.headKeys.push(key);
    const object = this.objects.get(key);
    return object ? this.describe(key, object) : null;
  }

  async list() {
    return {
      objects: [...this.objects].map(([key, object]) => this.describe(key, object)),
      truncated: false,
    };
  }

  async delete(key) {
    this.objects.delete(key);
  }

  describe(key, object) {
    return {
      key,
      size: object.data.byteLength,
      uploaded: object.uploaded,
      httpEtag: object.httpEtag,
      httpMetadata: object.httpMetadata,
      customMetadata: object.customMetadata,
    };
  }

  keys(prefix) {
    return [...this.objects.keys()].filter(key => key.startsWith(prefix));
  }

  expireAlias(key) {
    const object = this.objects.get(key);
    const record = JSON.parse(new TextDecoder().decode(object.data));
    record.expiresAt = new Date(Date.now() - 1000).toISOString();
    object.data = new TextEncoder().encode(JSON.stringify(record));
    object.customMetadata.expirationTime = record.expiresAt;
  }
}

function makeEnv(bucket, overrides = {}) {
  return {
    R2_BUCKET: bucket,
    ENABLE_DEDUP: 'true',
    DEDUP_SECRET: 'test-secret-with-at-least-32-bytes',
    MAX_UPLOAD_SIZE: '1048576',
    MAX_AGE_FOR_MULTIDOWNLOAD: '86400',
    ...overrides,
  };
}

async function upload(env, contentType, extraHeaders = {}) {
  return worker.fetch(new Request('https://example.test/', {
    method: 'PUT',
    headers: {
      'Content-Type': contentType,
      'X-Expiration-Seconds': '3600',
      ...extraHeaders,
    },
    body: 'identical bytes',
  }), env, { waitUntil() {} });
}

function aliasKeyFrom(responseText) {
  const match = responseText.match(/https?:\/\/[^\s]+\/(a\/[^\s]+)/);
  assert.ok(match, `missing alias URL in response: ${responseText}`);
  return match[1];
}

test('identical content shares one blob while aliases keep independent MIME and expiry', async () => {
  const bucket = new MemoryR2Bucket();
  const env = makeEnv(bucket);

  const first = await upload(env, 'text/plain');
  const second = await upload(env, 'text/html');
  assert.equal(first.status, 200);
  assert.equal(second.status, 200);

  const firstAlias = aliasKeyFrom(await first.text());
  const secondAlias = aliasKeyFrom(await second.text());
  assert.notEqual(firstAlias, secondAlias);
  assert.equal(bucket.keys('a/').length, 2);
  assert.equal(bucket.keys('b/').length, 1);
  assert.equal(bucket.keys('t/').length, 0);

  const firstDownload = await worker.fetch(
    new Request(`https://example.test/${firstAlias}`), env, { waitUntil() {} },
  );
  const secondDownload = await worker.fetch(
    new Request(`https://example.test/${secondAlias}`), env, { waitUntil() {} },
  );
  assert.equal(firstDownload.headers.get('Content-Type'), 'text/plain');
  assert.equal(secondDownload.headers.get('Content-Type'), 'text/html');
  assert.equal(await firstDownload.text(), 'identical bytes');

  bucket.expireAlias(firstAlias);
  const expired = await worker.fetch(
    new Request(`https://example.test/${firstAlias}`), env, { waitUntil() {} },
  );
  const stillLive = await worker.fetch(
    new Request(`https://example.test/${secondAlias}`), env, { waitUntil() {} },
  );
  assert.equal(expired.status, 404);
  assert.equal(stillLive.status, 200);
  assert.equal(bucket.keys('a/').length, 1);
  assert.equal(bucket.keys('b/').length, 1);
});

test('internal blob URLs are not downloadable', async () => {
  const bucket = new MemoryR2Bucket();
  const env = makeEnv(bucket);
  const response = await upload(env, 'application/octet-stream');
  assert.equal(response.status, 200);
  const blobKey = bucket.keys('b/')[0];

  const download = await worker.fetch(
    new Request(`https://example.test/${blobKey}`), env, { waitUntil() {} },
  );
  assert.equal(download.status, 404);
});

test('content hash validation works even when deduplication is unavailable', async () => {
  const bucket = new MemoryR2Bucket();
  const env = makeEnv(bucket, { DEDUP_SECRET: '' });

  const invalid = await upload(env, 'text/plain', { 'X-Content-SHA256': 'not-a-hash' });
  assert.equal(invalid.status, 400);

  const mismatch = await upload(env, 'text/plain', { 'X-Content-SHA256': '0'.repeat(64) });
  assert.equal(mismatch.status, 409);
  assert.equal(bucket.objects.size, 0);
});

test('config reports the effective deduplication state', async () => {
  const bucket = new MemoryR2Bucket();
  const enabled = await worker.fetch(
    new Request('https://example.test/api/config'), makeEnv(bucket), { waitUntil() {} },
  );
  const disabled = await worker.fetch(
    new Request('https://example.test/api/config'), makeEnv(bucket, { DEDUP_SECRET: '' }), { waitUntil() {} },
  );
  assert.equal((await enabled.json()).enableDedup, true);
  assert.equal((await disabled.json()).enableDedup, false);
});

test('oversized hashing uploads return 413 without an unhandled rejection', async () => {
  const bucket = new MemoryR2Bucket();
  const env = makeEnv(bucket, { MAX_UPLOAD_SIZE: '3' });
  const rejections = [];
  const onRejection = reason => rejections.push(reason);
  process.on('unhandledRejection', onRejection);
  try {
    const response = await upload(env, 'text/plain');
    assert.equal(response.status, 413);
    await new Promise(resolve => setImmediate(resolve));
    assert.deepEqual(rejections, []);
  } finally {
    process.off('unhandledRejection', onRejection);
  }
});

test('alias URLs retain non-standard ports', async () => {
  const bucket = new MemoryR2Bucket();
  const env = makeEnv(bucket);
  const response = await worker.fetch(new Request('http://localhost:8787/', {
    method: 'PUT',
    headers: {
      'Content-Type': 'text/plain',
      'X-Expiration-Seconds': '3600',
    },
    body: 'identical bytes',
  }), env, { waitUntil() {} });
  assert.match(await response.text(), /http:\/\/localhost:8787\/a\//);
});

test('scheduled cleanup skips blob HEAD requests', async () => {
  const bucket = new MemoryR2Bucket();
  await bucket.put(`b/${'a'.repeat(64)}`, 'blob');
  await bucket.put('legacy-object', 'legacy', {
    customMetadata: { uploadTime: new Date().toISOString() },
  });

  await worker.scheduled({}, makeEnv(bucket), {});
  assert.equal(bucket.headKeys.includes(`b/${'a'.repeat(64)}`), false);
  assert.equal(bucket.headKeys.includes('legacy-object'), true);
});
