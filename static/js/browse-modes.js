/* Browse modes — progressive enhancement for /activities/.
 *
 * The server renders a bounded set of text-first activity cards inside [data-browse] and sets the
 * initial mode via data-view (list | cards) from the ?view= query param, so the page is fully
 * usable with NO JavaScript. This script makes the experience snappy:
 *   - a client-side List <-> Cards toggle (no page reload, URL kept in sync);
 *   - "cards" mode shows ONE activity at a time as a focused deck you can move through with the
 *     Prev/Next buttons, the Left/Right arrow keys, or a touch swipe.
 *
 * Important: swipe/keys/buttons only NAVIGATE between meetups (like turning a page). Nothing is
 * "liked", rejected, ranked, or recorded — there is no engagement scoring and no behavioural
 * tracking. Cards are text-only (no images). Animations are plain CSS transitions, so the site's
 * reduced-motion preference (data-motion / prefers-reduced-motion) disables them automatically.
 */
(function () {
  "use strict";

  var root = document.querySelector("[data-browse]");
  if (!root) return;

  var items = Array.prototype.slice.call(root.querySelectorAll(".browse-item"));
  var status = root.querySelector("[data-browse-status]");
  var toggleBtns = Array.prototype.slice.call(document.querySelectorAll("[data-view-btn]"));
  var prevBtn = root.querySelector("[data-deck-prev]");
  var nextBtn = root.querySelector("[data-deck-next]");
  var posEl = root.querySelector("[data-deck-pos]");
  var current = 0;
  var SUPPORTS_INERT = "inert" in HTMLElement.prototype;
  // Make each card programmatically focusable so deck navigation can move focus onto it.
  items.forEach(function (el) {
    el.setAttribute("tabindex", "-1");
  });

  function view() {
    return root.getAttribute("data-view") === "cards" ? "cards" : "list";
  }

  // Move keyboard focus + screen-reader visibility off the hidden cards in deck mode.
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
    if (announce && items[current]) {
      // Move focus onto the now-active card (the only non-inert one), so a keyboard user lands on
      // the new content instead of being stranded on the card that just went inert.
      items[current].focus({ preventScroll: true });
      if (status) {
        var title = items[current].getAttribute("data-title") || "";
        status.textContent = title + " — " + (current + 1) + " of " + items.length;
      }
    }
  }

  function setView(next, remember) {
    root.setAttribute("data-view", next === "cards" ? "cards" : "list");
    toggleBtns.forEach(function (b) {
      var on = b.getAttribute("data-view-btn") === next;
      b.classList.toggle("is-active", on);
      // These are navigation links (role=link): the selected state is aria-current, not
      // aria-pressed (valid only on a toggle button).
      if (on) b.setAttribute("aria-current", "page");
      else b.removeAttribute("aria-current");
    });
    if (next === "cards") go(current, false);
    else applyDeck();
    if (remember) {
      try {
        var u = new URL(window.location.href);
        u.searchParams.set("view", next);
        u.searchParams.delete("page");
        window.history.replaceState({}, "", u.toString());
      } catch (e) {
        /* history not available — the in-page state still updated */
      }
    }
  }

  // --- wire the toggle ---
  toggleBtns.forEach(function (b) {
    b.addEventListener("click", function (e) {
      e.preventDefault();
      setView(b.getAttribute("data-view-btn"), true);
    });
  });

  // --- deck navigation: buttons ---
  if (prevBtn) prevBtn.addEventListener("click", function () { go(current - 1, true); });
  if (nextBtn) nextBtn.addEventListener("click", function () { go(current + 1, true); });

  // --- deck navigation: arrow keys (only in cards mode, and not while typing) ---
  document.addEventListener("keydown", function (e) {
    if (view() !== "cards") return;
    var t = e.target;
    if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.isContentEditable)) return;
    if (e.key === "ArrowRight") { e.preventDefault(); go(current + 1, true); }
    else if (e.key === "ArrowLeft") { e.preventDefault(); go(current - 1, true); }
  });

  // --- deck navigation: touch swipe (horizontal only; vertical scroll untouched) ---
  var startX = null, startY = null, swiping = false;
  var deck = root.querySelector("[data-browse-deck]") || root;
  deck.addEventListener("touchstart", function (e) {
    if (view() !== "cards" || e.touches.length !== 1) return;
    startX = e.touches[0].clientX;
    startY = e.touches[0].clientY;
    swiping = true;
  }, { passive: true });
  deck.addEventListener("touchend", function (e) {
    if (!swiping || startX === null) return;
    swiping = false;
    var touch = e.changedTouches[0];
    var dx = touch.clientX - startX;
    var dy = touch.clientY - startY;
    startX = startY = null;
    if (Math.abs(dx) < 45 || Math.abs(dx) < Math.abs(dy)) return; // not a clear horizontal swipe
    go(dx < 0 ? current + 1 : current - 1, true);
  }, { passive: true });

  root.classList.add("is-enhanced");
  setView(view(), false);
})();
