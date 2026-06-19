// Modern live client for the activity thread — progressive enhancement over the server-rendered
// stream. The page is FULLY functional with JS off (the compose form POSTs; reactions are form
// POSTs; edit/delete/search/older-messages are plain GET/POST). This script only makes a live
// session feel natural: first-class live posts (server-rendered body_html so mentions + markdown
// match exactly), near-bottom auto-scroll + a "new messages" pill, relative timestamps, author-run
// grouping, a transient typing indicator, reconnect-with-backoff, an auto-grow + Enter-to-send
// composer, live reactions, and a session-local unread divider (localStorage only — never sent to
// the server, so the no-per-user-read-tracking invariant holds).
//
// Safety contracts preserved from the prior inline client:
//  * Loaded AFTER presend-nudge.js, so the safety nudge's submit listener registers FIRST and can
//    gate both the WebSocket send and the no-JS POST.
//  * A file attachment forces a native POST (uploads never go over the socket).
//  * The aria-live region announces ONLY the viewer's own send + new announcements — never every
//    peer message; the typing hint is aria-hidden, so peer presence is screen-reader-silent.
//  * Everything from the socket except body_html (which the SERVER already escaped to safe HTML) is
//    treated as untrusted text and inserted via textContent / esc().
(function () {
  "use strict";

  var cfgEl = document.getElementById("thread-chat-config");
  var list = document.getElementById("thread-list");
  if (!cfgEl || !list) return;

  var CFG = JSON.parse(cfgEl.textContent);
  var I = CFG.i18n || {};
  var ME_ID = CFG.meId;
  var threadId = CFG.threadId;
  var status = document.getElementById("live-status");
  var pill = document.getElementById("thread-jump");
  var typingEl = document.getElementById("typing-indicator");
  var form = document.getElementById("compose");
  var reduceMotion =
    window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  var LS_KEY = "thread:" + threadId + ":lastSeen";
  var MON = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
  var SHARE_PATHS = { activity: "/activities/", place: "/places/", event: "/events/" };

  // ---------------------------------------------------------------- small utils
  function esc(t) {
    var d = document.createElement("div");
    d.textContent = t == null ? "" : String(t);
    return d.innerHTML;
  }
  function cookie(name) {
    var m = document.cookie.match(
      "(?:^|; )" + name.replace(/([.*+?^${}()|[\]\\])/g, "\\$1") + "=([^;]*)"
    );
    return m ? decodeURIComponent(m[1]) : "";
  }
  function lsGet(k) {
    try {
      return localStorage.getItem(k);
    } catch (e) {
      return null;
    }
  }
  function lsSet(k, v) {
    try {
      localStorage.setItem(k, v);
    } catch (e) {
      /* private mode / blocked storage — degrade silently */
    }
  }
  function pad(n) {
    return n < 10 ? "0" + n : "" + n;
  }
  function atBottom(px) {
    return list.scrollHeight - list.scrollTop - list.clientHeight < (px || 140);
  }
  function scrollToBottom(smooth) {
    list.scrollTo({ top: list.scrollHeight, behavior: smooth && !reduceMotion ? "smooth" : "auto" });
  }
  function reactUrl(postId) {
    return (CFG.reactUrlTemplate || "").replace("987654321", String(postId));
  }

  // ---------------------------------------------------------------- relative timestamps
  function rel(iso) {
    var t = Date.parse(iso);
    if (isNaN(t)) return null;
    var s = Math.round((Date.now() - t) / 1000);
    if (s < 0) s = 0;
    if (s < 45) return I.justNow || "just now";
    if (s < 3600) return Math.max(1, Math.round(s / 60)) + "m";
    if (s < 86400) return Math.round(s / 3600) + "h";
    if (s < 7 * 86400) return Math.round(s / 86400) + "d";
    return null; // older than a week: keep the absolute server text
  }
  function absShort(iso) {
    var dt = new Date(iso);
    if (isNaN(dt.getTime())) return "";
    return dt.getDate() + " " + MON[dt.getMonth()] + " " + pad(dt.getHours()) + ":" + pad(dt.getMinutes());
  }
  function stampTimes(root) {
    (root || list).querySelectorAll("time[datetime]").forEach(function (el) {
      var r = rel(el.getAttribute("datetime"));
      if (r) {
        if (!el.dataset.abs) el.dataset.abs = el.textContent; // keep absolute as the hover tooltip
        el.title = el.dataset.abs;
        el.textContent = r;
      }
    });
  }

  // ---------------------------------------------------------------- author-run grouping
  // Collapse the repeated name header on consecutive posts by the same author. A day separator or a
  // non-empty replies block breaks the run. Works over both server-rendered and live posts.
  function regroup() {
    var prev = null;
    Array.prototype.forEach.call(list.children, function (el) {
      if (el.tagName === "ARTICLE" && el.classList.contains("post")) {
        if (el.classList.contains("post-announcement")) {
          prev = null;
          return;
        }
        var same = prev && prev.dataset.author && prev.dataset.author === el.dataset.author;
        el.classList.toggle("post--grouped", !!same);
        prev = el;
      } else if (el.classList && el.classList.contains("day-sep")) {
        prev = null;
      } else if (el.classList && el.classList.contains("replies")) {
        if (el.children.length) prev = null; // real replies break the run; an empty block is transparent
      }
    });
  }

  // ---------------------------------------------------------------- unread divider (client-only)
  function postIds() {
    return Array.prototype.map
      .call(list.querySelectorAll('article.post[id^="post-"]'), function (el) {
        return parseInt(el.id.slice(5), 10);
      })
      .filter(function (n) {
        return !isNaN(n);
      });
  }
  function maxId() {
    var ids = postIds();
    return ids.length ? Math.max.apply(null, ids) : 0;
  }
  function markUnreadDivider() {
    var lastSeen = parseInt(lsGet(LS_KEY) || "0", 10);
    if (!lastSeen || isNaN(lastSeen)) return;
    var arts = list.querySelectorAll('article.post[id^="post-"]');
    for (var i = 0; i < arts.length; i++) {
      var id = parseInt(arts[i].id.slice(5), 10);
      if (id > lastSeen && i > 0) {
        // i > 0: only meaningful when there is history above the marker
        var div = document.createElement("div");
        div.className = "unread-sep";
        div.setAttribute("aria-hidden", "true");
        var span = document.createElement("span");
        span.textContent = I.newMessages || "New messages";
        div.appendChild(span);
        arts[i].parentNode.insertBefore(div, arts[i]);
        break;
      }
    }
  }
  function consumeSeen() {
    // Advance the marker once the reader has reached the bottom (they've seen everything). Stored
    // ONLY in this browser — never sent to the server.
    if (atBottom(60)) {
      var m = maxId();
      if (m) lsSet(LS_KEY, String(m));
    }
  }

  // ---------------------------------------------------------------- reactions (anonymous, countless)
  var mineByPost = {}; // postId(string) -> [emoji] the viewer reacted with (seeded from server DOM)
  list.querySelectorAll('article.post[id^="post-"]').forEach(function (art) {
    var id = art.id.slice(5);
    var mine = [];
    art.querySelectorAll(".rx .rx-chip.rx-mine").forEach(function (c) {
      mine.push(c.textContent.trim());
    });
    if (mine.length) mineByPost[id] = mine;
  });

  function renderChips(wrap, present, mine) {
    wrap.querySelectorAll(".rx-chip").forEach(function (c) {
      c.remove();
    });
    var pick = wrap.querySelector(".rx-pick");
    present.forEach(function (e) {
      var s = document.createElement("span");
      s.className = "rx-chip" + (mine.indexOf(e) >= 0 ? " rx-mine" : "");
      s.textContent = e;
      wrap.insertBefore(s, pick || null);
    });
  }
  function syncPicker(wrap, mine) {
    wrap.querySelectorAll(".rx-pick .rx-btn").forEach(function (b) {
      b.classList.toggle("rx-mine", mine.indexOf(b.textContent.trim()) >= 0);
    });
  }
  function reactionRow(postId, present, mine) {
    var wrap = document.createElement("div");
    wrap.className = "rx";
    var details = document.createElement("details");
    details.className = "rx-pick";
    var summary = document.createElement("summary");
    summary.className = "muted";
    summary.textContent = I.react || "react";
    details.appendChild(summary);
    (CFG.emojis || []).forEach(function (e) {
      var f = document.createElement("form");
      f.className = "inline rx-form";
      f.method = "post";
      f.action = reactUrl(postId);
      var tok = document.createElement("input");
      tok.type = "hidden";
      tok.name = "csrfmiddlewaretoken";
      tok.value = cookie("csrftoken");
      var em = document.createElement("input");
      em.type = "hidden";
      em.name = "emoji";
      em.value = e;
      var b = document.createElement("button");
      b.type = "submit";
      b.className = "rx-btn" + ((mine || []).indexOf(e) >= 0 ? " rx-mine" : "");
      b.textContent = e;
      f.appendChild(tok);
      f.appendChild(em);
      f.appendChild(b);
      details.appendChild(f);
    });
    wrap.appendChild(details);
    renderChips(wrap, present || [], mine || []);
    return wrap;
  }
  function applyReaction(d) {
    var art = document.getElementById("post-" + d.post_id);
    if (!art) return;
    var wrap = art.querySelector(".rx");
    if (!wrap) return;
    renderChips(wrap, d.present || [], mineByPost[d.post_id] || []);
  }
  // Intercept a reaction form submit -> fetch (no reload); other members update via the broadcast.
  list.addEventListener("submit", function (ev) {
    var f = ev.target;
    if (!f || f.tagName !== "FORM") return;
    var action = f.getAttribute("action") || "";
    if (!/\/react\/?($|\?)/.test(action)) return; // only reaction forms — never edit/delete
    ev.preventDefault();
    var emojiInput = f.querySelector('input[name="emoji"]');
    var emoji = emojiInput ? emojiInput.value : "";
    var art = f.closest("article.post");
    var postId = art ? art.id.slice(5) : null;
    fetch(f.action, {
      method: "POST",
      headers: { "X-Requested-With": "fetch", "X-CSRFToken": cookie("csrftoken") },
      body: new URLSearchParams({ emoji: emoji }),
      credentials: "same-origin",
    })
      .then(function (r) {
        return r.ok ? r.json() : null;
      })
      .then(function (j) {
        if (!j || !j.ok || !postId || !art) return;
        mineByPost[postId] = j.mine || [];
        var wrap = art.querySelector(".rx");
        if (wrap) {
          renderChips(wrap, j.present || [], j.mine || []);
          syncPicker(wrap, j.mine || []);
        }
      })
      .catch(function () {
        /* network hiccup — the no-JS POST path still works on the next reload */
      });
  });

  // ---------------------------------------------------------------- live post rendering
  var seen = new Set();
  list.querySelectorAll('[id^="post-"]').forEach(function (el) {
    seen.add(el.id.slice(5));
  });

  function render(d) {
    if (seen.has(String(d.id))) return; // dedupe by server pk (own send is echoed, never doubled)
    seen.add(String(d.id));
    var noneEl = document.getElementById("no-messages");
    if (noneEl) noneEl.remove();
    var stick = atBottom();

    var art = document.createElement("article");
    art.id = "post-" + d.id;
    art.dataset.author = d.author_id;
    art.className =
      "post" +
      (d.author_id === ME_ID ? " post--mine" : "") +
      (d.is_announcement ? " post-announcement" : "");

    if (d.reply_snippet) {
      var q = document.createElement("div");
      q.className = "reply-quote muted";
      var qa = document.createElement("a");
      qa.href = "#post-" + d.reply_snippet.pk;
      qa.textContent = (I.replyingTo || "Replying to") + " " + (d.reply_snippet.author || "");
      q.appendChild(qa);
      q.appendChild(document.createTextNode(": " + (d.reply_snippet.text || "")));
      art.appendChild(q);
    }

    var meta = document.createElement("div");
    meta.className = "meta";
    var an = document.createElement("span");
    an.className = "author";
    an.textContent = d.author;
    meta.appendChild(an);
    meta.appendChild(document.createTextNode(" · "));
    var link = document.createElement("a");
    link.href = "#post-" + d.id;
    link.className = "muted";
    var tm = document.createElement("time");
    if (d.created_at) {
      tm.setAttribute("datetime", d.created_at);
      tm.textContent = absShort(d.created_at);
    }
    link.appendChild(tm);
    meta.appendChild(link);
    if (d.edited) {
      meta.appendChild(document.createTextNode(" "));
      var em = document.createElement("span");
      em.className = "muted edited-mark";
      em.textContent = I.edited || "(edited)";
      meta.appendChild(em);
    }
    art.appendChild(meta);

    var body = document.createElement("div");
    // body_html is SERVER-rendered safe HTML (escaped first; mentions/markdown are our own tags),
    // so innerHTML is safe and makes a live post identical to the no-JS render.
    if (d.body_html != null) body.innerHTML = d.body_html;
    else body.textContent = d.body || "";
    art.appendChild(body);

    if (d.share && d.share.kind && d.share.kind !== "gone" && Number.isInteger(d.share.id) && SHARE_PATHS[d.share.kind]) {
      var sc = document.createElement("a");
      sc.className = "share-card";
      sc.href = SHARE_PATHS[d.share.kind] + d.share.id + "/"; // href from kind+int id only
      var k = document.createElement("span");
      k.className = "share-kind";
      k.textContent = d.share.kind;
      var st = document.createElement("strong");
      st.textContent = d.share.title || "";
      sc.appendChild(k);
      sc.appendChild(st);
      art.appendChild(sc);
    }

    if (!d.is_announcement) {
      art.appendChild(reactionRow(d.id, [], mineByPost[d.id] || []));
      var pa = document.createElement("div");
      pa.className = "post-actions";
      var rl = document.createElement("a");
      rl.href = "?reply_to=" + d.id + "#compose";
      rl.textContent = I.reply || "Reply";
      pa.appendChild(rl);
      art.appendChild(pa);
    }

    var target = null;
    if (d.reply_to) target = list.querySelector('.replies[data-parent="' + d.reply_to + '"]');
    (target || list).appendChild(art);
    stampTimes(art);
    regroup();
    if (stick) scrollToBottom(true);
    else showPill();
  }

  // ---------------------------------------------------------------- "new messages" pill
  function showPill() {
    if (pill) pill.hidden = false;
  }
  function hidePill() {
    if (pill) pill.hidden = true;
  }
  if (pill)
    pill.addEventListener("click", function () {
      scrollToBottom(true);
      hidePill();
    });
  list.addEventListener("scroll", function () {
    if (atBottom()) {
      hidePill();
      consumeSeen();
    }
  });

  // ---------------------------------------------------------------- typing indicator (transient)
  var typers = new Map(); // author_id -> { name, ts }
  function renderTyping() {
    var now = Date.now();
    typers.forEach(function (v, k) {
      if (now - v.ts > 5000) typers.delete(k);
    });
    if (!typingEl) return;
    var names = [];
    typers.forEach(function (v) {
      names.push(v.name);
    });
    var txt = "";
    if (names.length === 1) txt = (I.typingOne || "%(name)s is typing…").replace("%(name)s", names[0]);
    else if (names.length === 2)
      txt = (I.typingTwo || "%(a)s and %(b)s are typing…").replace("%(a)s", names[0]).replace("%(b)s", names[1]);
    else if (names.length > 2) txt = I.typingMany || "Several people are typing…";
    typingEl.textContent = txt;
    typingEl.classList.toggle("is-active", names.length > 0);
  }
  function onTyping(d) {
    if (d.author_id === ME_ID) return; // never show my own typing back to me
    typers.set(d.author_id, { name: d.author, ts: Date.now() });
    renderTyping();
  }
  setInterval(renderTyping, 2000); // expire stale typers

  var lastTypingSent = 0;
  function maybeSendTyping() {
    var now = Date.now();
    if (sock && sock.readyState === WebSocket.OPEN && now - lastTypingSent > 2500) {
      lastTypingSent = now;
      try {
        sock.send(JSON.stringify({ type: "typing" }));
      } catch (e) {
        /* best effort */
      }
    }
  }

  // ---------------------------------------------------------------- connection (reconnect/backoff)
  var sock = null;
  var backoff = 1000;
  var manualClose = false;
  var connNote = null;
  function setPaused(p) {
    if (p) {
      if (!connNote) {
        connNote = document.createElement("div");
        connNote.className = "thread-conn";
        connNote.setAttribute("role", "status");
        connNote.textContent = I.livePaused || "Live updates paused — reload to catch up.";
        if (typingEl && typingEl.parentNode) typingEl.parentNode.insertBefore(connNote, typingEl);
      }
      connNote.hidden = false;
    } else if (connNote) {
      connNote.hidden = true;
    }
  }
  function connect() {
    var proto = location.protocol === "https:" ? "wss" : "ws";
    sock = new WebSocket(proto + "://" + location.host + "/ws/chat/" + threadId + "/");
    sock.onopen = function () {
      backoff = 1000;
      setPaused(false);
    };
    sock.onmessage = function (e) {
      var d;
      try {
        d = JSON.parse(e.data);
      } catch (_) {
        return;
      }
      if (d.type === "message") {
        render(d);
        if (d.is_announcement && status) status.textContent = I.newAnnouncement || "";
      } else if (d.type === "reaction") {
        applyReaction(d);
      } else if (d.type === "typing") {
        onTyping(d);
      }
    };
    sock.onclose = function () {
      if (manualClose) return;
      setPaused(true);
      setTimeout(connect, backoff);
      backoff = Math.min(backoff * 2, 15000);
    };
    sock.onerror = function () {
      try {
        sock.close();
      } catch (e) {
        /* noop */
      }
    };
  }

  // ---------------------------------------------------------------- composer (send / grow / enter)
  if (form) {
    var ta = form.querySelector("textarea");
    function autoGrow() {
      if (!ta) return;
      ta.style.height = "auto";
      ta.style.height = Math.min(ta.scrollHeight, 200) + "px";
    }
    // Registered AFTER presend-nudge.js, so the safety nudge still gates first (it can
    // stopImmediatePropagation + preventDefault before this runs).
    form.addEventListener("submit", function (ev) {
      if (!sock || sock.readyState !== WebSocket.OPEN) return; // graceful fallback to a native POST
      var file = form.querySelector('input[name="attachment"]');
      if (file && file.files && file.files.length) return; // a file upload must POST, not WS
      if (!ta) return;
      var rt = form.querySelector('input[name="reply_to"]');
      var body = (ta.value || "").trim();
      if (!body) return;
      ev.preventDefault();
      try {
        sock.send(JSON.stringify({ body: body, reply_to: rt && rt.value ? Number(rt.value) : null }));
      } catch (e) {
        form.submit(); // socket died between the check and here -> native POST
        return;
      }
      ta.value = "";
      autoGrow();
      if (status) status.textContent = I.messageSent || "";
      scrollToBottom(true);
    });
    if (ta) {
      ta.addEventListener("input", function () {
        autoGrow();
        maybeSendTyping();
      });
      ta.addEventListener("keydown", function (ev) {
        // Enter sends; Shift+Enter is a newline. Routes through requestSubmit so the presend nudge
        // and the WS-vs-POST logic still run. (Skip while an IME composition is active.)
        if (
          ev.key === "Enter" &&
          !ev.shiftKey &&
          !ev.isComposing &&
          typeof form.requestSubmit === "function"
        ) {
          ev.preventDefault();
          form.requestSubmit();
        }
      });
      autoGrow();
    }
  }

  // ---------------------------------------------------------------- boot
  stampTimes();
  regroup();
  markUnreadDivider();
  setInterval(function () {
    stampTimes();
  }, 60000); // keep relative times fresh
  window.addEventListener("pagehide", consumeSeen);
  document.addEventListener("visibilitychange", function () {
    if (document.hidden) consumeSeen();
  });
  connect();
  scrollToBottom(false); // open at the newest message, like a chat
  consumeSeen();
})();
