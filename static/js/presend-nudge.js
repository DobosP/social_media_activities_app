// F33 — pre-send safety nudge (client half).
//
// A calm, dismissible "are you sure?" shown when an activity-thread post looks about to share
// a phone number, email, street address, or a plan to meet one-to-one off-platform. The point
// is to catch the highest-harm leak at AUTHORSHIP time — nothing leaves the device for a
// message the author abandons. It is advisory only: it NEVER blocks a post and NEVER reports.
//
// The regex ruleset is NOT defined here — it is emitted verbatim by the server (the single
// source of truth in apps/chat/presend.py) and compiled below, so the client and server can
// never drift. The confirm message arrives pre-translated in the same config blob.
//
// Ordering matters: this file is loaded non-deferred and listed BEFORE the inline live-send
// script in activity_detail.html, so this submit listener registers (and fires) FIRST. On
// "cancel" we stopImmediatePropagation() that later listener (the WebSocket send) and
// preventDefault() the native no-JS form POST — gating BOTH send paths. On "confirm" we do
// nothing and let the normal send/POST proceed.
(function () {
  "use strict";

  var cfgEl = document.getElementById("presend-nudge");
  var form = document.getElementById("compose");
  if (!cfgEl || !form) return; // fail-open: never get in the way of posting

  var cfg;
  try {
    cfg = JSON.parse(cfgEl.textContent);
  } catch (e) {
    return;
  }
  var message = cfg && cfg.message;
  var rules = (cfg && cfg.rules) || [];
  if (!message || !rules.length) return;

  // Compile the shared ruleset once. An unparseable rule is skipped (fail-open per rule), so a
  // bad pattern can never throw inside the composer. No "g" flag is used, so .test() is stateless.
  var compiled = [];
  for (var i = 0; i < rules.length; i++) {
    try {
      compiled.push(new RegExp(rules[i].pattern, rules[i].flags || ""));
    } catch (e) {
      /* skip an unparseable rule */
    }
  }
  if (!compiled.length) return;

  function looksRisky(text) {
    for (var j = 0; j < compiled.length; j++) {
      if (compiled[j].test(text)) return true;
    }
    return false;
  }

  form.addEventListener("submit", function (ev) {
    var ta = form.querySelector("textarea");
    var text = ta && ta.value ? ta.value : "";
    if (!text.trim()) return; // empty / attachment-only post: nothing to leak
    if (!looksRisky(text)) return; // clean message: never interrupt
    if (window.confirm(message)) return; // the author chose to send it anyway
    ev.preventDefault(); // stop the no-JS form POST fallback
    ev.stopImmediatePropagation(); // stop the WebSocket-send listener registered after this one
  });
})();
