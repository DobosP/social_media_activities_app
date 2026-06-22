/* Browse modes — progressive enhancement for /activities/.
 *
 * The server renders a bounded set of text-first activity cards inside [data-browse] and sets the
 * initial mode via data-view (list | cards) from the ?view= query param, so the page is fully
 * usable with NO JavaScript. This script makes it snappy:
 *   - a client-side List <-> Cards toggle (no reload, URL kept in sync);
 *   - "cards" is a phone-like CAROUSEL: a centred card with the neighbours peeking, moved through
 *     with the Prev/Next buttons, the Left/Right arrow keys, a touch swipe, OR a mouse drag (so it
 *     feels like a phone card-stack on a laptop too).
 *
 * Important: drag/swipe/keys/buttons only NAVIGATE between meetups (turn a page). Nothing is liked,
 * rejected, ranked, or recorded — no engagement scoring, no behavioural tracking. Cards are
 * text-only; the generated accent banner is procedural decoration, never a photo. Movement is plain
 * CSS transitions, so the site's reduced-motion preference disables it automatically.
 */
(function () {
  "use strict";

  var root = document.querySelector("[data-browse]");
  if (!root) return;

  var deck = root.querySelector("[data-browse-deck]");
  var track = root.querySelector(".browse-grid");
  var items = Array.prototype.slice.call(root.querySelectorAll(".browse-item"));
  var status = root.querySelector("[data-browse-status]");
  var toggleBtns = Array.prototype.slice.call(document.querySelectorAll("[data-view-btn]"));
  var prevBtn = root.querySelector("[data-deck-prev]");
  var nextBtn = root.querySelector("[data-deck-next]");
  var posEl = root.querySelector("[data-deck-pos]");
  var current = 0;
  var SUPPORTS_INERT = "inert" in HTMLElement.prototype;

  // Cards are programmatically focusable so deck navigation can move focus onto the active one.
  items.forEach(function (el) {
    el.setAttribute("tabindex", "-1");
  });

  function view() {
    return root.getAttribute("data-view") === "cards" ? "cards" : "list";
  }

  // translateX needed to centre the current card in the deck viewport.
  function centerOffset() {
    if (!items[current] || !deck) return 0;
    return deck.clientWidth / 2 - (items[current].offsetLeft + items[current].offsetWidth / 2);
  }

  function position(animate) {
    if (!track) return;
    if (view() !== "cards") {
      track.style.transform = "";
      return;
    }
    var x = centerOffset();
    if (animate) {
      track.style.transform = "translateX(" + x + "px)";
    } else {
      track.style.transition = "none";
      track.style.transform = "translateX(" + x + "px)";
      void track.offsetWidth; // force reflow so the NEXT change animates
      track.style.transition = "";
    }
  }

  function applyDeck() {
    var cards = view() === "cards";
    items.forEach(function (el, idx) {
      var off = cards && idx !== current;
      el.classList.toggle("is-current", cards && idx === current);
      el.toggleAttribute("inert", off);
      el.setAttribute("aria-hidden", off ? "true" : "false");
      // Fallback for browsers without `inert`: keep hidden cards out of the tab order too.
      if (!SUPPORTS_INERT) {
        el.querySelectorAll("a, button").forEach(function (f) {
          if (off) f.setAttribute("tabindex", "-1");
          else f.removeAttribute("tabindex");
        });
      }
    });
    if (prevBtn) prevBtn.disabled = !cards || current === 0;
    if (nextBtn) nextBtn.disabled = !cards || current === items.length - 1;
    if (posEl) posEl.textContent = current + 1 + " / " + items.length;
  }

  function go(i, announce) {
    current = Math.max(0, Math.min(i, items.length - 1));
    applyDeck();
    position(true);
    if (announce && items[current]) {
      // Move focus onto the now-active card (the only non-inert one) so a keyboard user lands on
      // the new content instead of being stranded on the card that just went inert.
      items[current].focus({ preventScroll: true });
      if (status) {
        var title = items[current].getAttribute("data-title") || "";
        status.textContent = title + " — " + (current + 1) + " of " + items.length;
      }
    }
  }

  function setView(next, remember) {
    var v = next === "cards" ? "cards" : "list";
    root.setAttribute("data-view", v);
    toggleBtns.forEach(function (b) {
      var on = b.getAttribute("data-view-btn") === v;
      b.classList.toggle("is-active", on);
      // Navigation links (role=link): the selected state is aria-current, not aria-pressed.
      if (on) b.setAttribute("aria-current", "page");
      else b.removeAttribute("aria-current");
    });
    applyDeck();
    position(false); // snap (no slide) when switching modes
    if (remember) {
      try {
        var u = new URL(window.location.href);
        u.searchParams.set("view", v);
        u.searchParams.delete("page");
        window.history.replaceState({}, "", u.toString());
      } catch (e) {
        /* history unavailable — in-page state still updated */
      }
    }
  }

  // --- toggle + buttons + keys ---
  toggleBtns.forEach(function (b) {
    b.addEventListener("click", function (e) {
      e.preventDefault();
      setView(b.getAttribute("data-view-btn"), true);
    });
  });
  if (prevBtn) prevBtn.addEventListener("click", function () { go(current - 1, true); });
  if (nextBtn) nextBtn.addEventListener("click", function () { go(current + 1, true); });

  document.addEventListener("keydown", function (e) {
    if (view() !== "cards") return;
    var t = e.target;
    if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" || t.isContentEditable)) return;
    if (e.key === "ArrowRight") { e.preventDefault(); go(current + 1, true); }
    else if (e.key === "ArrowLeft") { e.preventDefault(); go(current - 1, true); }
  });

  // --- drag (mouse + touch + pen) — a phone-like card drag, on a laptop too ---
  var dragging = false, moved = false, startX = 0, startY = 0, base = 0, pid = null;
  var suppressClick = false;

  if (deck) {
    deck.addEventListener("pointerdown", function (e) {
      if (view() !== "cards" || e.button === 1 || e.button === 2) return;
      if (dragging) return; // a drag already owns this gesture — ignore a 2nd (multi-touch) finger
      suppressClick = false; // fresh gesture: clear any stale latch (a touch swipe fires no click)
      dragging = true; moved = false; pid = e.pointerId;
      startX = e.clientX; startY = e.clientY; base = centerOffset();
    });
    deck.addEventListener("pointermove", function (e) {
      if (!dragging) return;
      var dx = e.clientX - startX, dy = e.clientY - startY;
      if (!moved) {
        if (Math.abs(dx) < 6) return; // a tiny move stays a click
        if (Math.abs(dy) > Math.abs(dx)) { // a vertical gesture — let the page scroll
          dragging = false;
          return;
        }
        moved = true;
        root.classList.add("is-dragging");
        try { deck.setPointerCapture(pid); } catch (err) { /* not capturable */ }
      }
      e.preventDefault();
      if (track) track.style.transform = "translateX(" + (base + dx) + "px)";
    });
    var endDrag = function (e) {
      if (!dragging) return;
      dragging = false;
      root.classList.remove("is-dragging");
      try { deck.releasePointerCapture(pid); } catch (err) { /* nothing to release */ }
      if (!moved) return; // it was a click — let the link work
      suppressClick = true; // a real drag must not also fire a link click
      var dx = e.clientX - startX;
      var threshold = Math.min(80, deck.clientWidth * 0.18);
      if (dx <= -threshold && current < items.length - 1) go(current + 1, true);
      else if (dx >= threshold && current > 0) go(current - 1, true);
      else position(true); // snap back
    };
    deck.addEventListener("pointerup", endDrag);
    deck.addEventListener("pointercancel", endDrag);
    // Swallow the click synthesised at the end of a drag so a swipe never opens a meetup.
    deck.addEventListener("click", function (e) {
      if (suppressClick) { e.preventDefault(); e.stopPropagation(); suppressClick = false; }
    }, true);
  }

  // Re-centre the active card if the viewport changes.
  var resizeTimer = null;
  window.addEventListener("resize", function () {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(function () { position(false); }, 120);
  });

  root.classList.add("is-enhanced");
  setView(view(), false);
})();
