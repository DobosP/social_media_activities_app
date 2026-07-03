/* Browse modes - progressive enhancement for /activities/.
 *
 * The server renders a bounded set of activity cards inside [data-browse] and sets the initial mode
 * via data-view (list | cards), so the page is fully usable with no JavaScript. This script makes
 * it snappy: a client-side List/Cards toggle, local deck shuffle, Prev/Next, keys, touch swipe, and
 * mouse/pointer drag.
 *
 * Important: drag/swipe/keys/buttons only navigate between meetups. Nothing is liked, rejected,
 * ranked, stored, or sent to the server. Motion is plain CSS transitions, so the site's reduced
 * motion preference disables it automatically.
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
  var shuffleBtn = root.querySelector("[data-deck-shuffle]");
  var posEl = root.querySelector("[data-deck-pos]");
  var current = 0;
  var SUPPORTS_INERT = "inert" in HTMLElement.prototype;

  items.forEach(function (el) {
    el.setAttribute("tabindex", "-1");
  });

  function view() {
    return root.getAttribute("data-view") === "cards" ? "cards" : "list";
  }

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
      void track.offsetWidth;
      track.style.transition = "";
    }
  }

  function clearDragTilt() {
    items.forEach(function (el) {
      el.style.removeProperty("--drag-rot");
    });
  }

  function applyDeck() {
    var cards = view() === "cards";
    items.forEach(function (el, idx) {
      var off = cards && idx !== current;
      el.classList.toggle("is-current", cards && idx === current);
      el.toggleAttribute("inert", off);
      el.setAttribute("aria-hidden", off ? "true" : "false");
      if (!SUPPORTS_INERT) {
        el.querySelectorAll("a, button").forEach(function (f) {
          if (off) f.setAttribute("tabindex", "-1");
          else f.removeAttribute("tabindex");
        });
      }
    });
    if (prevBtn) prevBtn.disabled = !cards || current === 0;
    if (nextBtn) nextBtn.disabled = !cards || current === items.length - 1;
    if (shuffleBtn) shuffleBtn.disabled = !cards || items.length < 2;
    if (posEl) posEl.textContent = current + 1 + " / " + items.length;
  }

  function announceActive() {
    if (!status || !items[current]) return;
    var title = items[current].getAttribute("data-title") || "";
    status.textContent = title + " - " + (current + 1) + " of " + items.length;
  }

  function go(i, announce) {
    current = Math.max(0, Math.min(i, items.length - 1));
    clearDragTilt();
    applyDeck();
    position(true);
    if (announce && items[current]) {
      items[current].focus({ preventScroll: true });
      announceActive();
    }
  }

  function setView(next, remember) {
    var v = next === "cards" ? "cards" : "list";
    root.setAttribute("data-view", v);
    toggleBtns.forEach(function (b) {
      var on = b.getAttribute("data-view-btn") === v;
      b.classList.toggle("is-active", on);
      if (on) b.setAttribute("aria-current", "page");
      else b.removeAttribute("aria-current");
    });
    applyDeck();
    position(false);
    if (remember) {
      try {
        var u = new URL(window.location.href);
        u.searchParams.set("view", v);
        u.searchParams.delete("page");
        window.history.replaceState({}, "", u.toString());
      } catch (e) {
        /* history unavailable - in-page state still updated */
      }
    }
  }

  function shuffleDeck() {
    if (!track || items.length < 2) return;
    for (var i = items.length - 1; i > 0; i -= 1) {
      var j = Math.floor(Math.random() * (i + 1));
      var tmp = items[i];
      items[i] = items[j];
      items[j] = tmp;
    }
    items.forEach(function (item) { track.appendChild(item); });
    current = 0;
    clearDragTilt();
    applyDeck();
    position(false);
    if (items[current]) items[current].focus({ preventScroll: true });
    announceActive();
  }

  toggleBtns.forEach(function (b) {
    b.addEventListener("click", function (e) {
      e.preventDefault();
      setView(b.getAttribute("data-view-btn"), true);
    });
  });
  if (prevBtn) prevBtn.addEventListener("click", function () { go(current - 1, true); });
  if (nextBtn) nextBtn.addEventListener("click", function () { go(current + 1, true); });
  if (shuffleBtn) shuffleBtn.addEventListener("click", shuffleDeck);

  document.addEventListener("keydown", function (e) {
    if (view() !== "cards") return;
    var t = e.target;
    if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" || t.isContentEditable)) return;
    if (e.key === "ArrowRight") { e.preventDefault(); go(current + 1, true); }
    else if (e.key === "ArrowLeft") { e.preventDefault(); go(current - 1, true); }
  });

  var dragging = false, moved = false, startX = 0, startY = 0, base = 0, pid = null;
  var lastX = 0, lastT = 0, velocity = 0, suppressClick = false;

  if (deck) {
    deck.addEventListener("pointerdown", function (e) {
      if (view() !== "cards" || e.button === 1 || e.button === 2) return;
      if (dragging) return;
      suppressClick = false;
      dragging = true; moved = false; pid = e.pointerId; velocity = 0;
      startX = e.clientX; startY = e.clientY; base = centerOffset();
      lastX = startX; lastT = performance.now();
    });

    deck.addEventListener("pointermove", function (e) {
      if (!dragging) return;
      var dx = e.clientX - startX, dy = e.clientY - startY;
      if (!moved) {
        if (Math.abs(dx) < 5) return;
        if (Math.abs(dy) > Math.abs(dx)) {
          dragging = false;
          return;
        }
        moved = true;
        root.classList.add("is-dragging");
        try { deck.setPointerCapture(pid); } catch (err) { /* not capturable */ }
      }
      e.preventDefault();
      var now = performance.now();
      var dt = Math.max(1, now - lastT);
      velocity = (e.clientX - lastX) / dt;
      lastX = e.clientX; lastT = now;
      if (track) track.style.transform = "translateX(" + (base + dx) + "px)";
      if (items[current] && deck.clientWidth) {
        var rot = Math.max(-6, Math.min(6, (dx / deck.clientWidth) * 10));
        items[current].style.setProperty("--drag-rot", rot + "deg");
      }
    });

    var endDrag = function (e) {
      if (!dragging) return;
      dragging = false;
      root.classList.remove("is-dragging");
      try { deck.releasePointerCapture(pid); } catch (err) { /* nothing to release */ }
      if (!moved) return;
      suppressClick = true;
      var dx = e.clientX - startX;
      var threshold = Math.min(72, deck.clientWidth * 0.16);
      var fling = Math.abs(velocity) > 0.55 && Math.abs(dx) > 18;
      if ((dx <= -threshold || (fling && velocity < 0)) && current < items.length - 1) go(current + 1, true);
      else if ((dx >= threshold || (fling && velocity > 0)) && current > 0) go(current - 1, true);
      else { clearDragTilt(); position(true); }
    };
    deck.addEventListener("pointerup", endDrag);
    deck.addEventListener("pointercancel", endDrag);
    deck.addEventListener("click", function (e) {
      if (suppressClick) { e.preventDefault(); e.stopPropagation(); suppressClick = false; }
    }, true);
  }

  var resizeTimer = null;
  window.addEventListener("resize", function () {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(function () { position(false); }, 120);
  });

  root.classList.add("is-enhanced");
  setView(view(), false);
})();
