import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';

const script = fs.readFileSync(new URL('./meetups-worker.js', import.meta.url), 'utf8');

function fixture({ offline = false } = {}) {
  const handlers = new Map();
  const calls = { fetch: [], matches: [], puts: [], deletes: [] };
  const live = { ok: true, body: 'live synthetic own meetup', clone() { return this; } };
  const cached = { ok: true, body: 'cached synthetic own meetup' };
  const cache = {
    addAll: async () => {},
    put: async (key, response) => calls.puts.push([key, response]),
  };
  const context = {
    URL,
    Promise,
    self: {
      location: { origin: 'https://fixture.invalid' },
      addEventListener: (name, callback) => handlers.set(name, callback),
      skipWaiting() {},
      clients: { claim: async () => {} },
    },
    caches: {
      open: async () => cache,
      keys: async () => ['mz-meetups-v1', 'mz-meetups-old', 'unrelated-cache'],
      match: async (key) => { calls.matches.push(key); return cached; },
      delete: async (key) => { calls.deletes.push(key); return true; },
    },
    fetch: async (request) => {
      calls.fetch.push(request.url);
      if (offline) throw new Error('synthetic network unavailable');
      return live;
    },
  };
  vm.runInNewContext(script, context, { filename: 'native-meetups-worker.js' });
  return { handlers, calls, live, cached };
}

function dispatchFetch(f, path, { method = 'GET', origin = 'https://fixture.invalid' } = {}) {
  const event = {
    request: { method, url: origin + path },
    respondWith(promise) { this.response = promise; },
  };
  f.handlers.get('fetch')(event);
  return event;
}

test('private auth, API, media and other HTML routes are never intercepted or cached', () => {
  const f = fixture();
  for (const path of ['/login/', '/logout/', '/register/', '/account/export/', '/account/restricted/', '/profile/', '/activities/1/', '/api/accounts/me/', '/api/messaging/conversations/1/messages/', '/media/photo/1/', '/static/js/site.js']) {
    assert.equal(dispatchFetch(f, path).response, undefined, path);
  }
  assert.equal(dispatchFetch(f, '/my-meetups/', { method: 'POST' }).response, undefined);
  assert.equal(dispatchFetch(f, '/my-meetups/', { origin: 'https://other.invalid' }).response, undefined);
  assert.deepEqual(f.calls, { fetch: [], matches: [], puts: [], deletes: [] });
});

test('own meetups are network-first, with only PAGE fallback when offline', async () => {
  const online = fixture();
  const response = await dispatchFetch(online, '/my-meetups/').response;
  await Promise.resolve();
  assert.equal(response, online.live);
  assert.deepEqual(online.calls.fetch, ['https://fixture.invalid/my-meetups/']);
  assert.deepEqual(online.calls.matches, []);
  assert.equal(online.calls.puts.length, 1);
  assert.equal(online.calls.puts[0][0], '/my-meetups/');
  assert.equal(online.calls.puts[0][1], online.live);

  const offline = fixture({ offline: true });
  assert.equal(await dispatchFetch(offline, '/my-meetups/').response, offline.cached);
  assert.deepEqual(offline.calls.matches, ['/my-meetups/']);
  assert.deepEqual(offline.calls.puts, []);
});

test('purge deletes the exact meetups cache; unrelated messages leave it intact', async () => {
  const f = fixture();
  const unrelated = { data: { type: 'unrelated' }, waitUntil() { assert.fail('unexpected wait'); } };
  f.handlers.get('message')(unrelated);
  assert.deepEqual(f.calls.deletes, []);
  const purge = { data: { type: 'purge' }, waitUntil(promise) { this.completion = promise; } };
  f.handlers.get('message')(purge);
  await purge.completion;
  assert.deepEqual(f.calls.deletes, ['mz-meetups-v1']);
});

test('only the exact stylesheet uses stale-while-revalidate and old meetups caches are retired', async () => {
  const f = fixture();
  const response = await dispatchFetch(f, '/static/css/base.css').response;
  await Promise.resolve();
  assert.equal(response, f.cached);
  assert.deepEqual(f.calls.fetch, ['https://fixture.invalid/static/css/base.css']);
  assert.equal(f.calls.puts.length, 1);
  assert.equal(f.calls.puts[0][0].url, 'https://fixture.invalid/static/css/base.css');
  const activation = { waitUntil(promise) { this.completion = promise; } };
  f.handlers.get('activate')(activation);
  await activation.completion;
  assert.deepEqual(f.calls.deletes, ['mz-meetups-old']);
});
