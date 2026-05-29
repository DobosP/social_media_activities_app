/* End-to-end-encrypted messaging client.
 *
 * All cryptography runs here, in the browser. The server is a zero-knowledge relay:
 * it only ever sees ciphertext and per-recipient wrapped keys. Scheme:
 *
 *   identity key   : ECDH P-256 keypair, generated locally, private key kept in
 *                    IndexedDB (optionally backed up to the server, wrapped under a
 *                    passphrase the server never sees).
 *   per message    : a random AES-256-GCM content key (CEK) encrypts the body once;
 *                    for each recipient the CEK is wrapped with a key derived via
 *                    ephemeral ECDH against that recipient's public key (ECIES-style).
 *
 * Honest limits (see docs/MESSAGING.md): this is hybrid public-key encryption, not
 * Signal/MLS — there is no double-ratchet forward secrecy or post-compromise
 * security, and trust is server-asserted (no out-of-band key verification yet).
 */
(function () {
  "use strict";

  const cfg = JSON.parse(document.getElementById("mz-config").textContent);
  const ME = cfg.me; // {public_id, username, display_name}
  const subtle = window.crypto && window.crypto.subtle;

  // ---- small helpers -------------------------------------------------------
  const enc = new TextEncoder();
  const dec = new TextDecoder();

  function b64encode(buf) {
    const bytes = new Uint8Array(buf);
    let s = "";
    for (let i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
    return btoa(s);
  }
  function b64decode(str) {
    const bin = atob(str);
    const bytes = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
    return bytes;
  }
  function randomBytes(n) {
    return window.crypto.getRandomValues(new Uint8Array(n));
  }
  function getCookie(name) {
    const m = document.cookie.match("(^|;)\\s*" + name + "\\s*=\\s*([^;]+)");
    return m ? m.pop() : "";
  }
  function publicJwkOnly(jwk) {
    return { kty: jwk.kty, crv: jwk.crv, x: jwk.x, y: jwk.y };
  }

  async function api(method, url, body) {
    const opts = {
      method,
      headers: { Accept: "application/json" },
      credentials: "same-origin",
    };
    if (body !== undefined) {
      opts.headers["Content-Type"] = "application/json";
      opts.headers["X-CSRFToken"] = getCookie("csrftoken");
      opts.body = JSON.stringify(body);
    } else if (method !== "GET") {
      opts.headers["X-CSRFToken"] = getCookie("csrftoken");
    }
    const resp = await fetch(url, opts);
    let data = null;
    try {
      data = await resp.json();
    } catch (e) {
      /* empty body (e.g. 204) */
    }
    if (!resp.ok) {
      const detail = (data && data.detail) || resp.statusText;
      throw new Error(detail);
    }
    return data;
  }

  // ---- IndexedDB key storage ----------------------------------------------
  const DB_NAME = "e2ee-messaging";
  const STORE = "keys";
  const KEY_ID = "identity:" + ME.public_id; // namespaced per account

  function idb() {
    return new Promise((resolve, reject) => {
      const req = indexedDB.open(DB_NAME, 1);
      req.onupgradeneeded = () => req.result.createObjectStore(STORE);
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => reject(req.error);
    });
  }
  async function idbGet(key) {
    const db = await idb();
    return new Promise((resolve, reject) => {
      const tx = db.transaction(STORE, "readonly").objectStore(STORE).get(key);
      tx.onsuccess = () => resolve(tx.result);
      tx.onerror = () => reject(tx.error);
    });
  }
  async function idbPut(key, value) {
    const db = await idb();
    return new Promise((resolve, reject) => {
      const tx = db.transaction(STORE, "readwrite").objectStore(STORE).put(value, key);
      tx.onsuccess = () => resolve();
      tx.onerror = () => reject(tx.error);
    });
  }

  // ---- ECDH identity key ---------------------------------------------------
  const ECDH = { name: "ECDH", namedCurve: "P-256" };
  let identity = null; // {privateKey: CryptoKey, publicJwk}
  const keyCache = {}; // username -> public JWK

  async function importPublicJwk(jwk) {
    return subtle.importKey("jwk", jwk, ECDH, false, []);
  }

  async function deriveAesKey(privateKey, publicKey, usages) {
    return subtle.deriveKey(
      { name: "ECDH", public: publicKey },
      privateKey,
      { name: "AES-GCM", length: 256 },
      false,
      usages
    );
  }

  // ---- key fingerprints & safety numbers (out-of-band verification) --------
  // canonicalJwk MUST match the server's json.dumps(sort_keys, no spaces) so the
  // fingerprint we submit equals services.key_fingerprint() (see docs/MESSAGING.md).
  function canonicalJwk(jwk) {
    return JSON.stringify({ crv: jwk.crv, kty: jwk.kty, x: jwk.x, y: jwk.y });
  }
  function toHex(buf) {
    return [...new Uint8Array(buf)].map((b) => b.toString(16).padStart(2, "0")).join("");
  }
  async function sha256hex(text) {
    return toHex(await subtle.digest("SHA-256", enc.encode(text)));
  }
  async function keyFingerprint(jwk) {
    return (await sha256hex(canonicalJwk(jwk))).slice(0, 32);
  }
  // A 60-digit safety number, identical for both peers when their keys match (it is
  // order-independent and binds each party's identity to their key). If a server
  // MITMs by serving a different key to one side, the two numbers differ.
  async function safetyNumber(meId, meJwk, themId, themJwk) {
    const a = meId + "|" + (await sha256hex(canonicalJwk(meJwk)));
    const b = themId + "|" + (await sha256hex(canonicalJwk(themJwk)));
    const combined = [a, b].sort().join("");
    const digest = new Uint8Array(await subtle.digest("SHA-512", enc.encode(combined)));
    const groups = [];
    for (let i = 0; i < 12; i++) {
      let n = 0;
      for (let j = 0; j < 5; j++) n = n * 256 + digest[i * 5 + j];
      groups.push(String(n % 100000).padStart(5, "0"));
    }
    return groups.join(" ");
  }

  // ---- passphrase backup (optional, enables history on a new device) -------
  async function kekFromPassphrase(passphrase, salt) {
    const base = await subtle.importKey("raw", enc.encode(passphrase), "PBKDF2", false, [
      "deriveKey",
    ]);
    return subtle.deriveKey(
      { name: "PBKDF2", salt, iterations: 250000, hash: "SHA-256" },
      base,
      { name: "AES-GCM", length: 256 },
      false,
      ["encrypt", "decrypt"]
    );
  }

  async function makeBackup(passphrase) {
    // The private key must be extractable to back it up; we re-generate as
    // extractable only when the user opts into backup.
    const privJwk = await idbGet(KEY_ID).then((r) => r && r.privateJwk);
    if (!privJwk) throw new Error("This device cannot export its key for backup.");
    const salt = randomBytes(16);
    const iv = randomBytes(12);
    const kek = await kekFromPassphrase(passphrase, salt);
    const ct = await subtle.encrypt({ name: "AES-GCM", iv }, kek, enc.encode(JSON.stringify(privJwk)));
    return {
      ct: b64encode(ct),
      iv: b64encode(iv),
      salt: b64encode(salt),
      v: 1,
    };
  }

  async function restoreFromBackup(blob, passphrase) {
    const kek = await kekFromPassphrase(passphrase, b64decode(blob.salt));
    const raw = await subtle.decrypt(
      { name: "AES-GCM", iv: b64decode(blob.iv) },
      kek,
      b64decode(blob.ct)
    );
    const privJwk = JSON.parse(dec.decode(raw));
    const privateKey = await subtle.importKey("jwk", privJwk, ECDH, true, [
      "deriveKey",
      "deriveBits",
    ]);
    const publicJwk = publicJwkOnly(privJwk);
    await idbPut(KEY_ID, { privateKey, publicJwk, privateJwk: privJwk });
    return { privateKey, publicJwk };
  }

  // ---- message encryption / decryption ------------------------------------
  // Fetch a contact's full key record ({public_jwk, fingerprint, verified, ...}).
  async function getContactKey(username) {
    if (keyCache[username]) return keyCache[username];
    const data = await api("GET", "/api/messaging/keys/" + encodeURIComponent(username) + "/");
    keyCache[username] = data;
    return data;
  }

  async function getPublicJwk(username) {
    if (username === ME.username) return identity.publicJwk;
    return (await getContactKey(username)).public_jwk;
  }

  // recipients: [{username, public_id}]
  async function encryptFor(recipients, plaintext) {
    const cek = await subtle.generateKey({ name: "AES-GCM", length: 256 }, true, [
      "encrypt",
      "decrypt",
    ]);
    const iv = randomBytes(12);
    const ciphertext = await subtle.encrypt({ name: "AES-GCM", iv }, cek, enc.encode(plaintext));
    const rawCek = await subtle.exportKey("raw", cek);

    const recipient_keys = [];
    for (const r of recipients) {
      const pubJwk = await getPublicJwk(r.username);
      const recipientPub = await importPublicJwk(pubJwk);
      const ephemeral = await subtle.generateKey(ECDH, true, ["deriveKey", "deriveBits"]);
      const wrapKey = await deriveAesKey(ephemeral.privateKey, recipientPub, ["encrypt"]);
      const wrapIv = randomBytes(12);
      const wrapped = await subtle.encrypt({ name: "AES-GCM", iv: wrapIv }, wrapKey, rawCek);
      recipient_keys.push({
        recipient_public_id: r.public_id,
        ephemeral_public_jwk: publicJwkOnly(await subtle.exportKey("jwk", ephemeral.publicKey)),
        wrapped_key: b64encode(wrapped),
        wrap_iv: b64encode(wrapIv),
      });
    }
    return { ciphertext: b64encode(ciphertext), iv: b64encode(iv), recipient_keys };
  }

  async function decryptMessage(msg) {
    const key = msg.key || (msg.keys || []).find((k) => k.recipient_public_id === ME.public_id);
    if (!key) return null; // not addressed to us (e.g. sent before we joined)
    try {
      const ephemeralPub = await importPublicJwk(key.ephemeral_public_jwk);
      const wrapKey = await deriveAesKey(identity.privateKey, ephemeralPub, ["decrypt"]);
      const rawCek = await subtle.decrypt(
        { name: "AES-GCM", iv: b64decode(key.wrap_iv) },
        wrapKey,
        b64decode(key.wrapped_key)
      );
      const cek = await subtle.importKey("raw", rawCek, { name: "AES-GCM" }, false, ["decrypt"]);
      const plain = await subtle.decrypt(
        { name: "AES-GCM", iv: b64decode(msg.iv) },
        cek,
        b64decode(msg.ciphertext)
      );
      return dec.decode(plain);
    } catch (e) {
      return "⚠ [unable to decrypt]";
    }
  }

  // ---- UI ------------------------------------------------------------------
  const els = {
    status: document.getElementById("mz-status"),
    list: document.getElementById("mz-conversations"),
    title: document.getElementById("mz-title"),
    log: document.getElementById("mz-log"),
    composer: document.getElementById("mz-composer"),
    input: document.getElementById("mz-input"),
    newForm: document.getElementById("mz-new-form"),
    backupBtn: document.getElementById("mz-backup"),
    verify: document.getElementById("mz-verify"),
  };
  let current = null; // current conversation object
  let socket = null;
  const activeRecipients = []; // [{username, public_id}] for the current conversation

  function setStatus(text, kind) {
    els.status.textContent = text;
    els.status.className = "muted" + (kind ? " " + kind : "");
  }

  function activeMembers(conv) {
    return (conv.participants || [])
      .filter((p) => p.state === "active")
      .map((p) => ({ username: p.user.username, public_id: p.user.public_id }));
  }

  function convLabel(conv) {
    if (conv.title) return conv.title;
    const others = (conv.participants || [])
      .filter((p) => p.user.public_id !== ME.public_id)
      .map((p) => p.user.display_name || p.user.username);
    return others.join(", ") || "Conversation";
  }

  async function loadConversations() {
    const convs = await api("GET", "/api/messaging/conversations/");
    els.list.innerHTML = "";
    convs.forEach((conv) => {
      const item = document.createElement("div");
      item.className = "mz-conv";
      const label = document.createElement("button");
      label.className = "linkbtn";
      label.textContent = convLabel(conv) + (conv.kind === "group" ? "  (group)" : "");
      label.addEventListener("click", () => openConversation(conv.id));
      item.appendChild(label);
      if (conv.my_state === "invited") {
        const badge = document.createElement("span");
        badge.className = "pill";
        badge.textContent = "invite";
        item.appendChild(badge);
        const accept = document.createElement("button");
        accept.className = "btn btn-sm";
        accept.textContent = "Accept";
        accept.addEventListener("click", async () => {
          await api("POST", "/api/messaging/conversations/" + conv.id + "/accept/");
          await loadConversations();
          openConversation(conv.id);
        });
        const decline = document.createElement("button");
        decline.className = "btn btn-sm btn-secondary";
        decline.textContent = "Decline";
        decline.addEventListener("click", async () => {
          await api("POST", "/api/messaging/conversations/" + conv.id + "/decline/");
          loadConversations();
        });
        item.appendChild(accept);
        item.appendChild(decline);
      }
      els.list.appendChild(item);
    });
    if (!convs.length) els.list.innerHTML = '<p class="muted">No conversations yet.</p>';
  }

  function appendMessage(msg, plaintext) {
    const row = document.createElement("div");
    row.className = "post";
    const meta = document.createElement("div");
    meta.className = "meta";
    const who = msg.sender ? msg.sender.display_name || msg.sender.username : "unknown";
    meta.textContent = who + " · " + new Date(msg.created_at).toLocaleString();
    if (msg.sender && msg.sender.public_id !== ME.public_id) {
      const report = document.createElement("button");
      report.className = "linkbtn";
      report.style.marginLeft = ".5rem";
      report.style.fontSize = ".75rem";
      report.textContent = "report";
      report.addEventListener("click", () => reportMessage(msg, plaintext));
      meta.appendChild(report);
    }
    const body = document.createElement("div");
    body.textContent = plaintext === null ? "⚠ [no key for you]" : plaintext;
    row.appendChild(meta);
    row.appendChild(body);
    els.log.appendChild(row);
    els.log.scrollTop = els.log.scrollHeight;
  }

  async function reportMessage(msg, plaintext) {
    const reason = window.prompt(
      "Reason (grooming, harassment, csam, spam, off_platform, other):",
      "harassment"
    );
    if (!reason) return;
    await api(
      "POST",
      "/api/messaging/conversations/" + current.id + "/messages/" + msg.id + "/report/",
      { reason: reason, decrypted_excerpt: plaintext || "" }
    );
    setStatus("Report submitted to moderators.", "ok");
  }

  // Render the key-verification panel: a safety number per other member, a verified
  // badge, a verify button, and a warning if a member's key changed since last seen.
  async function renderVerification(conv) {
    els.verify.innerHTML = "";
    const others = (conv.participants || [])
      .filter((p) => p.state === "active" && p.user.public_id !== ME.public_id)
      .map((p) => ({
        username: p.user.username,
        public_id: p.user.public_id,
        display: p.user.display_name || p.user.username,
      }));
    if (!others.length) return;

    const panel = document.createElement("details");
    panel.className = "mz-verify-panel card";
    const summary = document.createElement("summary");
    panel.appendChild(summary);
    let unverified = 0;

    for (const o of others) {
      let ck;
      try {
        ck = await getContactKey(o.username);
      } catch (e) {
        continue;
      }
      const number = await safetyNumber(ME.public_id, identity.publicJwk, o.public_id, ck.public_jwk);

      // Key-change detection (stored locally — the server isn't trusted for this).
      const seenKey = "seenfp:" + o.public_id;
      const prev = await idbGet(seenKey);
      const changed = prev && prev !== ck.fingerprint;
      await idbPut(seenKey, ck.fingerprint);

      const row = document.createElement("div");
      row.className = "mz-verify-row";
      const head = document.createElement("div");
      const name = document.createElement("strong");
      name.textContent = o.display;
      head.appendChild(name);
      head.appendChild(document.createTextNode(" "));
      const badge = document.createElement("span");
      if (ck.verified) {
        badge.className = "pill ok";
        badge.textContent = "✓ verified";
      } else {
        unverified++;
        badge.className = "pill";
        badge.textContent = "unverified";
      }
      head.appendChild(badge);
      row.appendChild(head);

      if (changed) {
        const warn = document.createElement("div");
        warn.className = "mz-warn";
        warn.textContent =
          "⚠ " + o.display + "'s key changed since you last saw it — re-verify before trusting it.";
        row.appendChild(warn);
      }

      const num = document.createElement("code");
      num.className = "mz-safety";
      num.textContent = number;
      row.appendChild(num);

      const btn = document.createElement("button");
      btn.className = "btn btn-sm";
      btn.textContent = ck.verified ? "Re-verify" : "Mark verified";
      btn.addEventListener("click", async () => {
        try {
          await api("POST", "/api/messaging/verify/", {
            username: o.username,
            fingerprint: ck.fingerprint,
          });
          delete keyCache[o.username];
          setStatus("Marked " + o.display + " as verified.", "ok");
          await renderVerification(current);
        } catch (e) {
          setStatus("Verify failed: " + e.message, "error");
        }
      });
      row.appendChild(btn);
      panel.appendChild(row);
    }

    summary.textContent =
      "🔒 Verify encryption keys" + (unverified ? " (" + unverified + " unverified)" : " (all verified)");
    els.verify.appendChild(panel);
  }

  async function openConversation(id) {
    const convs = await api("GET", "/api/messaging/conversations/");
    current = convs.find((c) => c.id === id);
    if (!current) return;
    els.title.textContent = convLabel(current);
    els.log.innerHTML = "";
    activeRecipients.length = 0;
    activeMembers(current).forEach((m) => activeRecipients.push(m));

    if (current.my_state !== "active") {
      els.composer.style.display = "none";
      els.verify.innerHTML = "";
      els.log.innerHTML = '<p class="muted">Accept the invitation to read and reply.</p>';
      return;
    }
    els.composer.style.display = "";

    await renderVerification(current);

    const history = await api(
      "GET",
      "/api/messaging/conversations/" + id + "/messages/"
    );
    for (const m of history) appendMessage(m, await decryptMessage(m));

    // Live socket.
    if (socket) socket.close();
    const proto = location.protocol === "https:" ? "wss" : "ws";
    socket = new WebSocket(proto + "://" + location.host + "/ws/messaging/" + id + "/");
    socket.onmessage = async (e) => {
      const d = JSON.parse(e.data);
      if (d.type === "message") appendMessage(d, await decryptMessage(d));
      else if (d.type === "error") setStatus(d.detail, "error");
    };
  }

  async function sendCurrent(text) {
    const payload = await encryptFor(activeRecipients, text);
    if (socket && socket.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify(payload));
    } else {
      const msg = await api(
        "POST",
        "/api/messaging/conversations/" + current.id + "/messages/",
        payload
      );
      appendMessage(msg, text);
    }
  }

  // ---- wiring --------------------------------------------------------------
  els.composer.addEventListener("submit", async (ev) => {
    ev.preventDefault();
    const text = els.input.value.trim();
    if (!text || !current) return;
    els.input.value = "";
    try {
      await sendCurrent(text);
    } catch (e) {
      setStatus("Send failed: " + e.message, "error");
    }
  });

  els.newForm.addEventListener("submit", async (ev) => {
    ev.preventDefault();
    const data = new FormData(els.newForm);
    const usernames = (data.get("usernames") || "")
      .split(/[\s,]+/)
      .map((s) => s.trim())
      .filter(Boolean);
    if (!usernames.length) return;
    const kind = usernames.length > 1 ? "group" : data.get("kind") || "direct";
    try {
      const conv = await api("POST", "/api/messaging/conversations/", {
        kind,
        usernames,
        title: data.get("title") || "",
      });
      els.newForm.reset();
      await loadConversations();
      openConversation(conv.id);
      setStatus("Conversation started — they must accept before they can read it.", "ok");
    } catch (e) {
      setStatus("Could not start conversation: " + e.message, "error");
    }
  });

  if (els.backupBtn) {
    els.backupBtn.addEventListener("click", async () => {
      const stored = await idbGet(KEY_ID);
      if (!stored || !stored.privateJwk) {
        setStatus(
          "This key was created without backup support. Reset the key to enable backup.",
          "error"
        );
        return;
      }
      const passphrase = window.prompt("Choose a backup passphrase (you'll need it on other devices):");
      if (!passphrase) return;
      try {
        const blob = await makeBackup(passphrase);
        await api("POST", "/api/messaging/keys/", {
          public_jwk: identity.publicJwk,
          wrapped_private_jwk: blob,
        });
        setStatus("Encrypted key backup saved. Keep the passphrase safe — we can't recover it.", "ok");
      } catch (e) {
        setStatus("Backup failed: " + e.message, "error");
      }
    });
  }

  // ---- bootstrap -----------------------------------------------------------
  async function ensureIdentity() {
    if (!subtle) {
      setStatus("This browser lacks the Web Crypto API needed for secure messaging.", "error");
      return false;
    }
    const stored = await idbGet(KEY_ID);
    if (stored && stored.privateKey) {
      identity = { privateKey: stored.privateKey, publicJwk: stored.publicJwk };
      // Keep the server's public key in sync (no-op if unchanged).
      await api("POST", "/api/messaging/keys/", { public_jwk: identity.publicJwk });
      setStatus("Secure messaging ready. Messages are end-to-end encrypted.", "ok");
      return true;
    }
    // No local key. Offer to restore from a server backup, else generate one.
    let serverKey = null;
    try {
      serverKey = await api("GET", "/api/messaging/keys/");
    } catch (e) {
      /* no key on the server yet */
    }
    if (serverKey && serverKey.wrapped_private_jwk) {
      const passphrase = window.prompt(
        "Enter your backup passphrase to restore secure messaging on this device:"
      );
      if (passphrase) {
        try {
          identity = await restoreFromBackup(serverKey.wrapped_private_jwk, passphrase);
          setStatus("Key restored from backup. Secure messaging ready.", "ok");
          return true;
        } catch (e) {
          setStatus("Could not restore key (wrong passphrase?). Generating a new one.", "error");
        }
      }
    }
    // Generate a fresh extractable key (so backup is possible) and register it.
    const pair = await subtle.generateKey(ECDH, true, ["deriveKey", "deriveBits"]);
    const privateJwk = await subtle.exportKey("jwk", pair.privateKey);
    const publicJwk = publicJwkOnly(await subtle.exportKey("jwk", pair.publicKey));
    await idbPut(KEY_ID, { privateKey: pair.privateKey, publicJwk, privateJwk });
    identity = { privateKey: pair.privateKey, publicJwk };
    await api("POST", "/api/messaging/keys/", { public_jwk: publicJwk });
    setStatus("Generated your encryption key on this device. Back it up to use other devices.", "ok");
    return true;
  }

  (async function init() {
    try {
      if (await ensureIdentity()) await loadConversations();
    } catch (e) {
      setStatus("Messaging unavailable: " + e.message, "error");
    }
  })();
})();
