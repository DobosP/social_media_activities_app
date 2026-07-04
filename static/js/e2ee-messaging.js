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
  const REACTIONS = cfg.reaction_emojis || []; // fixed emoji ack set
  const msgRows = {}; // server msg id -> rendered row element (so reactions find their target)
  const appliedReactions = {}; // de-dupe: "targetId:senderId:emoji" -> true
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

  // members: [{public_id, public_jwk}] — the conversation's active members (incl. any
  // guardian observer), fetched from the membership-scoped keys endpoint so a
  // cross-cohort guardian's key is available to encrypt to.
  async function encryptForMembers(members, plaintext) {
    const cek = await subtle.generateKey({ name: "AES-GCM", length: 256 }, true, [
      "encrypt",
      "decrypt",
    ]);
    const iv = randomBytes(12);
    const ciphertext = await subtle.encrypt({ name: "AES-GCM", iv }, cek, enc.encode(plaintext));
    const rawCek = await subtle.exportKey("raw", cek);

    const recipient_keys = [];
    for (const m of members) {
      const recipientPub = await importPublicJwk(m.public_jwk);
      const ephemeral = await subtle.generateKey(ECDH, true, ["deriveKey", "deriveBits"]);
      const wrapKey = await deriveAesKey(ephemeral.privateKey, recipientPub, ["encrypt"]);
      const wrapIv = randomBytes(12);
      const wrapped = await subtle.encrypt({ name: "AES-GCM", iv: wrapIv }, wrapKey, rawCek);
      recipient_keys.push({
        recipient_public_id: m.public_id,
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
    toolbar: document.getElementById("mz-toolbar"),
    timer: document.getElementById("mz-timer"),
    guardian: document.getElementById("mz-guardian"),
    guardianSection: document.getElementById("mz-guardian-section"),
    guardianList: document.getElementById("mz-guardian-list"),
    app: document.getElementById("mz-app"),
    back: document.getElementById("mz-back"),
    titleAvatar: document.getElementById("mz-title-avatar"),
    connections: document.getElementById("mz-connections"),
    tabs: Array.prototype.slice.call(document.querySelectorAll(".mz-tab")),
    convSearch: document.getElementById("mz-conv-search"),
    msgSearch: document.getElementById("mz-msg-search"),
  };
  let current = null; // current conversation object
  let socket = null;
  let currentFilter = "all"; // conversation-list filter (all/direct/group/invited)
  let convQuery = ""; // W6: chat-list search (metadata only — names/titles, never bodies)

  // An avatar circle: a person's generated identicon when we have one, else the first initial.
  // Groups keep the initial/colour (a group has no single identity).
  function avatarEl(label, isGroup, avatarUri) {
    const a = document.createElement("span");
    a.className = "mz-avatar" + (isGroup ? " group" : "");
    if (avatarUri && !isGroup) {
      const img = document.createElement("img");
      img.src = avatarUri;
      img.alt = "";
      img.className = "mz-avatar-img";
      a.appendChild(img);
    } else {
      a.textContent = (label || "?").trim().charAt(0).toUpperCase() || "?";
    }
    return a;
  }

  // The generated avatar for a 1:1 conversation = the OTHER participant's identicon (server-issued
  // on the participant user-ref). A group has none.
  function convAvatar(conv) {
    if (!conv || conv.kind === "group") return null;
    const other = (conv.participants || []).find(
      (p) => p.user.public_id !== ME.public_id
    );
    return other && other.user.avatar ? other.user.avatar : null;
  }

  // Which list filter a conversation matches.
  function convCategory(conv) {
    if (conv.my_state === "invited") return "invited";
    return conv.kind === "group" ? "group" : "direct";
  }

  function setStatus(text, kind) {
    els.status.textContent = text;
    els.status.className = "muted" + (kind ? " " + kind : "");
  }

  function convLabel(conv) {
    if (conv.title) return conv.title;
    const others = (conv.participants || [])
      .filter((p) => p.user.public_id !== ME.public_id)
      .map((p) => p.user.display_name || p.user.username);
    return others.join(", ") || "Conversation";
  }

  let lastConvs = [];

  async function loadConversations() {
    lastConvs = await api("GET", "/api/messaging/conversations/");
    renderConversations();
  }

  function renderConversations() {
    els.list.innerHTML = "";
    const convs = lastConvs.filter(
      (c) =>
        (currentFilter === "all" || convCategory(c) === currentFilter) &&
        (!convQuery || convLabel(c).toLowerCase().indexOf(convQuery) !== -1)
    );
    convs.forEach((conv) => {
      const isGroup = conv.kind === "group";
      const label = convLabel(conv);
      const row = document.createElement("button");
      row.type = "button";
      row.className = "mz-conv" + (current && current.id === conv.id ? " is-active" : "");
      row.dataset.convId = conv.id;
      row.appendChild(avatarEl(isGroup ? conv.title || "#" : label, isGroup, convAvatar(conv)));

      const meta = document.createElement("div");
      meta.className = "mz-meta";
      const name = document.createElement("div");
      name.className = "mz-name";
      name.textContent = label;
      const sub = document.createElement("div");
      sub.className = "mz-sub";
      sub.textContent =
        conv.my_state === "invited"
          ? "wants to chat with you"
          : isGroup
            ? (conv.participants || []).length + " people"
            : "direct message";
      meta.appendChild(name);
      meta.appendChild(sub);
      row.appendChild(meta);
      row.addEventListener("click", () => openConversation(conv.id));

      if (conv.my_state === "invited") {
        const actions = document.createElement("span");
        const accept = document.createElement("button");
        accept.className = "btn btn-sm";
        accept.textContent = "Accept";
        accept.addEventListener("click", async (e) => {
          e.stopPropagation();
          await api("POST", "/api/messaging/conversations/" + conv.id + "/accept/");
          await loadConversations();
          openConversation(conv.id);
        });
        const decline = document.createElement("button");
        decline.className = "linkbtn mz-decline";
        decline.textContent = "Decline";
        decline.addEventListener("click", async (e) => {
          e.stopPropagation();
          await api("POST", "/api/messaging/conversations/" + conv.id + "/decline/");
          loadConversations();
        });
        actions.appendChild(accept);
        actions.appendChild(decline);
        row.appendChild(actions);
      }
      els.list.appendChild(row);
    });
    if (!convs.length) {
      els.list.innerHTML =
        '<p class="muted">' +
        (currentFilter === "all" ? "No conversations yet." : "Nothing in this filter.") +
        "</p>";
    }
  }

  // Quick-start chips for your connections (people you've met). Clicking opens/creates a 1:1.
  function renderConnections() {
    const conns = cfg.connections || [];
    els.connections.innerHTML = "";
    conns.slice(0, 24).forEach((c) => {
      const chip = document.createElement("button");
      chip.type = "button";
      chip.className = "mz-connchip";
      chip.title = "Chat with " + c.display_name;
      chip.appendChild(avatarEl(c.display_name, false, c.avatar));
      const lbl = document.createElement("small");
      lbl.textContent = c.display_name;
      chip.appendChild(lbl);
      chip.addEventListener("click", () => startDirect(c.username));
      els.connections.appendChild(chip);
    });
  }

  // Open an existing 1:1 with `username`, or create one (they must accept the first message).
  async function startDirect(username) {
    const existing = lastConvs.find(
      (c) =>
        c.kind === "direct" &&
        (c.participants || []).some((p) => p.user.username === username)
    );
    if (existing) {
      openConversation(existing.id);
      return;
    }
    try {
      const conv = await api("POST", "/api/messaging/conversations/", {
        kind: "direct",
        usernames: [username],
        title: "",
      });
      await loadConversations();
      openConversation(conv.id);
      setStatus("Conversation started — they must accept before they can read it.", "ok");
    } catch (e) {
      setStatus("Could not start conversation: " + e.message, "error");
    }
  }

  // For guardians: list ward conversations not yet observed, each with an Observe button.
  async function loadGuardianConversations() {
    let convs = [];
    try {
      convs = await api("GET", "/api/messaging/guardian/conversations/");
    } catch (e) {
      return; // not a guardian / nothing to show
    }
    const pending = convs.filter((c) => c.my_state !== "active");
    if (!pending.length) {
      els.guardianSection.hidden = true;
      return;
    }
    els.guardianSection.hidden = false;
    els.guardianList.innerHTML = "";
    pending.forEach((conv) => {
      const item = document.createElement("div");
      item.className = "mz-conv";
      const label = document.createElement("span");
      label.textContent = convLabel(conv);
      const observe = document.createElement("button");
      observe.className = "btn btn-sm";
      observe.textContent = "Observe";
      observe.addEventListener("click", async () => {
        try {
          await api("POST", "/api/messaging/conversations/" + conv.id + "/guardian/");
          setStatus("You are now observing this conversation (read-only).", "ok");
          await loadConversations();
          await loadGuardianConversations();
          openConversation(conv.id);
        } catch (e) {
          setStatus("Could not observe: " + e.message, "error");
        }
      });
      item.appendChild(label);
      item.appendChild(observe);
      els.guardianList.appendChild(item);
    });
  }

  function appendMessage(msg, plaintext) {
    const empty = els.log.querySelector(".mz-empty");
    if (empty) empty.remove();
    const own = msg.sender && msg.sender.public_id === ME.public_id;
    const row = document.createElement("div");
    row.className = "mz-msg " + (own ? "mz-msg--own" : "mz-msg--them");
    if (msg.id != null) {
      row.dataset.msgId = msg.id;
      msgRows[msg.id] = row;
    }

    // Sender name above the bubble in GROUP chats (WhatsApp-style); not for your own / 1:1.
    if (!own && current && current.kind === "group") {
      const who = document.createElement("div");
      who.className = "mz-msg-who";
      who.textContent = msg.sender ? msg.sender.display_name || msg.sender.username : "unknown";
      row.appendChild(who);
    }
    const body = document.createElement("div");
    body.className = "mz-msg-body";
    body.textContent = plaintext === null ? "⚠ [no key for you]" : plaintext;
    row.appendChild(body);
    // W6 share-by-link: an in-app activity/place/event link pasted into a private chat
    // gets a tappable card UNDER the text. Client-side only (the server never sees the
    // plaintext); the href is rebuilt from the matched digits-only path, never raw text.
    if (plaintext) {
      const seenPaths = {};
      const linkRe = /\/(activities|places|events)\/(\d+)\//g;
      let m;
      while ((m = linkRe.exec(plaintext)) !== null) {
        const path = "/" + m[1] + "/" + m[2] + "/";
        if (seenPaths[path]) continue;
        seenPaths[path] = true;
        const card = document.createElement("a");
        card.className = "mz-share-card";
        card.href = path;
        card.textContent =
          (m[1] === "activities" ? "Open activity" : m[1] === "places" ? "Open place" : "Open event") +
          " →";
        row.appendChild(card);
      }
    }

    const time = document.createElement("div");
    time.className = "mz-msg-time";
    time.textContent = new Date(msg.created_at).toLocaleTimeString([], {
      hour: "2-digit",
      minute: "2-digit",
    });
    if (!own && msg.sender) {
      const report = document.createElement("button");
      report.className = "linkbtn mz-report";
      report.textContent = "report";
      report.addEventListener("click", () => reportMessage(msg, plaintext));
      time.appendChild(report);
    }
    // React affordance — you can react to any message (including your own). E2EE: the emoji is
    // sent as an encrypted message the server can't read; recipients render it as who+what.
    if (msg.id != null && REACTIONS.length) {
      const react = document.createElement("span");
      react.className = "mz-react";
      const pick = document.createElement("button");
      pick.className = "linkbtn mz-react-toggle";
      pick.textContent = "+react";
      pick.title = "Add a reaction";
      const tray = document.createElement("span");
      tray.className = "mz-react-tray";
      tray.hidden = true;
      REACTIONS.forEach((e) => {
        const b = document.createElement("button");
        b.className = "mz-react-emoji";
        b.textContent = e;
        b.addEventListener("click", () => {
          tray.hidden = true;
          sendReaction(msg.id, e);
        });
        tray.appendChild(b);
      });
      pick.addEventListener("click", () => {
        tray.hidden = !tray.hidden;
      });
      react.appendChild(pick);
      react.appendChild(tray);
      time.appendChild(react);
    }
    row.appendChild(time);
    // Container for rendered reactions (who reacted with what) under the bubble.
    const rx = document.createElement("div");
    rx.className = "mz-msg-rx";
    row.appendChild(rx);
    els.log.appendChild(row);
    els.log.scrollTop = els.log.scrollHeight;
  }

  // A reaction travels as an ENCRYPTED message whose plaintext is a sentinel JSON
  // {"__r":1,"m":<targetMsgId>,"e":"👍"} — the server only relays ciphertext, so the emoji is
  // private; WHO reacted is the (visible) message-sender metadata, WHAT is the encrypted emoji.
  // `e` is constrained to the fixed REACTIONS set: the server is ciphertext-blind and CANNOT
  // police the payload, so this receiving-side allowlist is the only gate. Without it a hostile
  // co-member could glue arbitrary free text (an off-platform lure, an impersonation string, a
  // spam wall — each distinct string defeating the dedupe) onto a victim's message bubble. A
  // sentinel that fails the allowlist returns null → routeMessage renders it as an ordinary
  // (scannable, reportable) message, the correct fail-safe.
  function asReaction(plaintext) {
    if (!plaintext || plaintext.charAt(0) !== "{") return null;
    try {
      const o = JSON.parse(plaintext);
      if (o && o.__r === 1 && typeof o.m === "number" && REACTIONS.indexOf(o.e) !== -1) {
        return o;
      }
      return null;
    } catch (e) {
      return null;
    }
  }

  // Route a decrypted message: a reaction attaches to its target; anything else is a normal msg.
  function routeMessage(msg, plaintext) {
    const r = asReaction(plaintext);
    if (r) {
      const who = msg.sender ? msg.sender.display_name || msg.sender.username : "someone";
      const sid = msg.sender ? msg.sender.public_id : "?";
      applyReaction(r.m, sid, who, r.e);
    } else {
      appendMessage(msg, plaintext);
    }
  }

  // Render "who reacted with what" under the target message (deduped against the live echo).
  function applyReaction(targetId, senderId, senderName, emoji) {
    const key = targetId + ":" + senderId + ":" + emoji;
    if (appliedReactions[key]) return;
    appliedReactions[key] = true;
    const targetRow = msgRows[targetId];
    if (!targetRow) return; // target outside the loaded window — skip
    const box = targetRow.querySelector(".mz-msg-rx");
    if (!box) return;
    const chip = document.createElement("span");
    chip.className = "mz-rx-chip";
    chip.textContent = emoji + " " + senderName;
    box.appendChild(chip);
  }

  async function sendReaction(targetId, emoji) {
    if (!current) return;
    const payload = { __r: 1, m: targetId, e: emoji };
    try {
      // Optimistic local render (the server echo is deduped by the key above).
      applyReaction(targetId, ME.public_id, ME.display_name, emoji);
      await sendCurrent(JSON.stringify(payload));
    } catch (e) {
      setStatus("Could not send reaction: " + e.message, "error");
    }
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
    // F43: open the panel for a zero-click safety-number comparison ONLY while a peer is still
    // unverified; once every peer is verified it stays collapsed, so it never becomes an
    // always-on nag (no dark pattern). The key-change warning above lives inside, so an
    // unverified-after-rotation peer surfaces its fingerprint immediately.
    panel.open = unverified > 0;
    els.verify.appendChild(panel);
  }

  // Transparency: show a banner whenever a guardian is observing, and switch the
  // viewer to read-only if they are themselves the guardian observer.
  function renderGuardianNotice(conv) {
    els.guardian.innerHTML = "";
    const guardians = (conv.participants || []).filter(
      (p) => p.state === "active" && p.role === "guardian"
    );
    if (conv.my_role === "guardian") {
      els.composer.hidden = true;
      els.toolbar.hidden = true;
    }
    if (!guardians.length) return;
    const names = guardians.map((p) => p.user.display_name || p.user.username).join(", ");
    const banner = document.createElement("div");
    banner.className = "banner mz-guardian-banner";
    banner.textContent =
      conv.my_role === "guardian"
        ? "You are observing this conversation as a guardian (read-only)."
        : "👁 A guardian (" + names + ") is observing this conversation.";
    els.guardian.appendChild(banner);
  }

  async function openConversation(id) {
    const convs = await api("GET", "/api/messaging/conversations/");
    current = convs.find((c) => c.id === id);
    if (!current) return;
    if (els.msgSearch) els.msgSearch.value = ""; // W6: a fresh chat starts unfiltered
    els.title.textContent = convLabel(current);
    // Header avatar, active-row highlight, and (on mobile) switch to the message pane.
    // avatarEl returns a span whose single child is EITHER the <img> (1:1 identicon) OR a text
    // node (the group/no-avatar initial); replaceChildren moves that child in, covering both.
    const isGroup = current.kind === "group";
    const headAvatar = avatarEl(convLabel(current), isGroup, convAvatar(current));
    els.titleAvatar.className = headAvatar.className;
    els.titleAvatar.replaceChildren(...headAvatar.childNodes);
    if (els.app) els.app.classList.add("show-main");
    renderConversations();
    els.log.innerHTML = "";
    // Reset per-conversation reaction state (rows + de-dupe) so nothing carries across chats.
    Object.keys(msgRows).forEach((k) => delete msgRows[k]);
    Object.keys(appliedReactions).forEach((k) => delete appliedReactions[k]);

    if (current.my_state !== "active") {
      els.composer.hidden = true;
      els.toolbar.hidden = true;
      els.verify.innerHTML = "";
      els.log.innerHTML = '<p class="muted">Accept the invitation to read and reply.</p>';
      return;
    }
    els.composer.hidden = false;
    els.toolbar.hidden = false;
    els.timer.value = String(current.disappearing_seconds || 0);

    renderGuardianNotice(current);
    await renderVerification(current);

    const history = await api(
      "GET",
      "/api/messaging/conversations/" + id + "/messages/"
    );
    for (const m of history) routeMessage(m, await decryptMessage(m));

    // Live socket.
    if (socket) socket.close();
    const proto = location.protocol === "https:" ? "wss" : "ws";
    socket = new WebSocket(proto + "://" + location.host + "/ws/messaging/" + id + "/");
    socket.onmessage = async (e) => {
      const d = JSON.parse(e.data);
      if (d.type === "message") routeMessage(d, await decryptMessage(d));
      else if (d.type === "error") setStatus(d.detail, "error");
    };
  }

  async function sendCurrent(text) {
    // Fetch the live member key set (includes any guardian observer) at send time.
    const members = await api("GET", "/api/messaging/conversations/" + current.id + "/keys/");
    const payload = await encryptForMembers(members, text);
    if (socket && socket.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify(payload));
    } else {
      const msg = await api(
        "POST",
        "/api/messaging/conversations/" + current.id + "/messages/",
        payload
      );
      routeMessage(msg, text);
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

  els.timer.addEventListener("change", async () => {
    if (!current) return;
    try {
      const conv = await api(
        "POST",
        "/api/messaging/conversations/" + current.id + "/disappearing/",
        { seconds: parseInt(els.timer.value, 10) }
      );
      current.disappearing_seconds = conv.disappearing_seconds;
      setStatus(
        conv.disappearing_seconds
          ? "Messages now disappear after the selected time."
          : "Disappearing messages turned off.",
        "ok"
      );
    } catch (e) {
      setStatus("Could not update timer: " + e.message, "error");
      els.timer.value = String(current.disappearing_seconds || 0);
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

  // Filter tabs (All / Direct / Groups / Requests) — re-render the already-loaded list.
  els.tabs.forEach((tab) => {
    tab.addEventListener("click", () => {
      els.tabs.forEach((t) => t.classList.toggle("is-active", t === tab));
      currentFilter = tab.dataset.filter;
      renderConversations();
    });
  });

  // W6: chat-list search — filters the loaded list by name/title locally (metadata only).
  if (els.convSearch) {
    els.convSearch.addEventListener("input", () => {
      convQuery = els.convSearch.value.trim().toLowerCase();
      renderConversations();
    });
  }

  // W6: search INSIDE the open chat — a pure on-device filter over the decrypted bubbles
  // already on screen. Nothing is sent anywhere (the server never has plaintext to search).
  function applyMsgSearch() {
    const q = els.msgSearch ? els.msgSearch.value.trim().toLowerCase() : "";
    els.log.querySelectorAll(".mz-msg").forEach((rowEl) => {
      const bodyEl = rowEl.querySelector(".mz-msg-body");
      const text = bodyEl ? bodyEl.textContent.toLowerCase() : "";
      rowEl.classList.toggle("mz-hidden", Boolean(q) && text.indexOf(q) === -1);
    });
  }
  if (els.msgSearch) {
    els.msgSearch.addEventListener("input", applyMsgSearch);
  }

  // Mobile: back arrow returns from the message pane to the conversation list.
  if (els.back) {
    els.back.addEventListener("click", () => {
      if (els.app) els.app.classList.remove("show-main");
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
      renderConnections();
      if (await ensureIdentity()) {
        await loadConversations();
        await loadGuardianConversations();
      }
    } catch (e) {
      setStatus("Messaging unavailable: " + e.message, "error");
    }
  })();
})();
