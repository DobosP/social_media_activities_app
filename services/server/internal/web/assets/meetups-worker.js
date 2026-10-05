'use strict';
const CACHE = 'mz-meetups-v1';
const PAGE = '/my-meetups/';
const ASSETS = ['/static/css/base.css'];

self.addEventListener('install', (e) => {
  self.skipWaiting();
  e.waitUntil(caches.open(CACHE).then((c) => c.addAll(ASSETS).catch(() => {})));
});

self.addEventListener('activate', (e) => {
  e.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(
        keys.filter((k) => k.startsWith('mz-meetups') && k !== CACHE).map((k) => caches.delete(k))
      )
    ).then(() => self.clients.claim())
  );
});

// The page postMessages this on logout / user-switch — drop the on-device copy immediately.
self.addEventListener('message', (e) => {
  if (e.data && e.data.type === 'purge') {
    e.waitUntil(caches.delete(CACHE));
  }
});

self.addEventListener('fetch', (e) => {
  const req = e.request;
  if (req.method !== 'GET') return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;
  const isPage = url.pathname === PAGE;
  const isAsset = ASSETS.indexOf(url.pathname) !== -1;
  if (!isPage && !isAsset) return; // manage ONLY the meetups page + its stylesheet

  if (isPage) {
    // NETWORK-FIRST: always prefer the live page (so a cancellation shows); cache the fresh copy;
    // only fall back to the cached copy when the network is unavailable.
    e.respondWith(
      fetch(req)
        .then((resp) => {
          if (resp && resp.ok) {
            const copy = resp.clone();
            caches.open(CACHE).then((c) => c.put(PAGE, copy));
          }
          return resp;
        })
        .catch(() => caches.match(PAGE))
    );
    return;
  }
  // The stylesheet: serve the cached copy at once (fast, keeps the offline page styled) but
  // refresh it in the background when online, so a deployed CSS change reaches the user on their
  // next online load (stale-while-revalidate) rather than being pinned until CACHE is bumped.
  e.respondWith(
    caches.match(req).then((hit) => {
      const live = fetch(req)
        .then((resp) => {
          if (resp && resp.ok) {
            const copy = resp.clone();
            caches.open(CACHE).then((c) => c.put(req, copy));
          }
          return resp;
        })
        .catch(() => hit);
      return hit || live;
    })
  );
});
