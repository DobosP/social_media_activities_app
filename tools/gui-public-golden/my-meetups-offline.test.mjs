import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import vm from 'node:vm';
import test, { after } from 'node:test';

// Exact unchanged script, synthetic node/navigator/window/fake fetch only.
// No browser, real request, service worker/cache, member data or privacy proof.
assert.equal(process.versions.node, '26.10.0');
const sourcePath = fileURLToPath(new URL('../../static/js/my-meetups.js', import.meta.url));
assert.equal(fs.realpathSync(sourcePath), sourcePath);
const sourceStat = fs.lstatSync(sourcePath);
assert.ok(sourceStat.isFile());
const source = fs.readFileSync(sourcePath);
const hash = bytes => crypto.createHash('sha256').update(bytes).digest('hex');
assert.equal(hash(source), 'a90afd9e8e320dbe3b3f348627f416bb0422940512145069607fb350e3e2924f');
after(() => {
  const current = fs.lstatSync(sourcePath);
  assert.ok(current.isFile());
  assert.equal(current.mode, sourceStat.mode);
  assert.equal(hash(fs.readFileSync(sourcePath)), hash(source));
});

function fixture({ mount = true, online = true, hidden = true } = {}) {
  const note = { hidden };
  const navigator = { onLine: online };
  const listeners = new Map();
  const requests = [];
  const context = {
    document: {
      getElementById(id) {
        assert.equal(id, 'offline-note');
        return mount ? note : null;
      },
    },
    navigator,
    window: {
      addEventListener(name, callback) {
        assert.ok(name === 'online' || name === 'offline');
        assert.equal(typeof callback, 'function');
        assert.equal(listeners.has(name), false);
        listeners.set(name, callback);
      },
    },
    fetch(url, options) {
      assert.equal(url, '/healthz');
      assert.deepEqual(Object.keys(options), ['cache']);
      assert.equal(options.cache, 'no-store');
      let resolve, reject;
      const promise = new Promise((accept, refuse) => { resolve = accept; reject = refuse; });
      requests.push({ url, cache: options.cache, resolve, reject });
      return promise;
    },
  };
  vm.runInNewContext(source.toString('utf8'), context, { filename: 'static/js/my-meetups.js', timeout: 1000 });
  if (mount) assert.deepEqual([...listeners.keys()], ['offline', 'online']);
  return { note, navigator, requests, listeners };
}

const settled = () => new Promise(resolve => setImmediate(resolve));

test('missing-note returns without requests or listeners', () => {
  const f = fixture({ mount: false });
  assert.equal(f.requests.length, 0);
  assert.equal(f.listeners.size, 0);
});

test('offline-initial reveals warning without a health request', () => {
  const f = fixture({ online: false });
  assert.equal(f.note.hidden, false);
  assert.equal(f.requests.length, 0);
});

test('online-success hides the warning after no-store health result', async () => {
  const f = fixture({ hidden: false });
  assert.equal(f.requests.length, 1);
  assert.equal(f.note.hidden, false);
  f.requests[0].resolve({ ok: true });
  await settled();
  assert.equal(f.note.hidden, true);
});

test('online-failure reveals the warning', async () => {
  const f = fixture();
  assert.equal(f.requests.length, 1);
  assert.equal(f.note.hidden, true);
  f.requests[0].resolve({ ok: false });
  await settled();
  assert.equal(f.note.hidden, false);
});

test('online-rejection reveals the warning', async () => {
  const f = fixture();
  assert.equal(f.requests.length, 1);
  f.requests[0].reject(new Error('synthetic health refusal'));
  await settled();
  assert.equal(f.note.hidden, false);
});

test('offline-event reveals warning without another request', async () => {
  const f = fixture();
  f.requests[0].resolve({ ok: true });
  await settled();
  assert.equal(f.note.hidden, true);
  f.navigator.onLine = false;
  f.listeners.get('offline')();
  assert.equal(f.note.hidden, false);
  assert.equal(f.requests.length, 1);
});

test('online-event re-probes with no-store before hiding warning', async () => {
  const f = fixture({ online: false });
  f.navigator.onLine = true;
  f.listeners.get('online')();
  assert.equal(f.requests.length, 1);
  assert.equal(f.note.hidden, false);
  f.requests[0].resolve({ ok: true });
  await settled();
  assert.equal(f.note.hidden, true);
  f.listeners.get('online')();
  assert.equal(f.requests.length, 2);
  assert.equal(f.requests[1].cache, 'no-store');
  f.requests[1].resolve({ ok: false });
  await settled();
  assert.equal(f.note.hidden, false);
});

test('pending-online result preserves the previous warning state', async () => {
  const f = fixture({ hidden: false });
  await settled();
  assert.equal(f.requests.length, 1);
  assert.equal(f.note.hidden, false);
  f.requests[0].resolve({ ok: true });
  await settled();
  assert.equal(f.note.hidden, true);
});
