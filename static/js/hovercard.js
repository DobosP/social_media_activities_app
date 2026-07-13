/* Person hover-overview cards (ADR-0028).
 *
 * Progressive enhancement over plain links: any element carrying
 * `data-hovercard-user="<public_id>"` gains a small overview popover on hover/focus,
 * fetched once from /people/<id>/card/ (the same tier-gated service as the profile page)
 * and cached per id for the page's lifetime. With JS off the link still navigates to the
 * person page — nothing depends on this file.
 *
 * Conventions: one pair of DOCUMENT-LEVEL delegated listeners (site.js precedent), no
 * external deps, same-origin fetch only, all styling via .hovercard classes in base.css
 * (style-src has no nonce — inline styles are never used). Keyboard accessible: focus
 * shows, blur/Escape hides; the card itself is hoverable so links inside stay reachable.
 */
(function () {
  "use strict";

  var SHOW_DELAY = 300; // hover intent — don't fetch on a drive-by mouse pass
  var HIDE_DELAY = 200; // grace period to move the pointer onto the card

  var cache = new Map(); // public_id -> html string | null (null = 404/veto: never re-fetch)
  var card = null; // the single popover element
  var anchor = null; // element the card is currently shown for
  var showTimer = 0;
  var hideTimer = 0;

  function ensureCard() {
    if (card) return card;
    card = document.createElement("div");
    card.className = "hovercard";
    card.setAttribute("role", "dialog");
    card.setAttribute("aria-label", "profile preview");
    card.hidden = true;
    card.addEventListener("mouseenter", function () {
      window.clearTimeout(hideTimer);
    });
    card.addEventListener("mouseleave", scheduleHide);
    document.body.appendChild(card);
    return card;
  }

  function hide() {
    window.clearTimeout(showTimer);
    window.clearTimeout(hideTimer);
    if (card) card.hidden = true;
    anchor = null;
  }

  function scheduleHide() {
    window.clearTimeout(hideTimer);
    hideTimer = window.setTimeout(hide, HIDE_DELAY);
  }

  function position(el) {
    var r = el.getBoundingClientRect();
    var c = ensureCard();
    c.hidden = false; // must be measurable
    var top = r.bottom + window.scrollY + 6;
    var left = r.left + window.scrollX;
    var w = c.offsetWidth || 280;
    var maxLeft = window.scrollX + document.documentElement.clientWidth - w - 8;
    if (left > maxLeft) left = Math.max(window.scrollX + 8, maxLeft);
    // Flip above the anchor when there is no room below.
    if (r.bottom + c.offsetHeight + 12 > document.documentElement.clientHeight) {
      top = r.top + window.scrollY - c.offsetHeight - 6;
      if (top < window.scrollY) top = r.bottom + window.scrollY + 6;
    }
    c.style.top = top + "px";
    c.style.left = left + "px";
  }

  function show(el, id) {
    var c = ensureCard();
    var html = cache.get(id);
    if (html === null) return; // veto/404 — stay quiet
    anchor = el;
    c.innerHTML = html;
    position(el);
  }

  function request(el) {
    var id = el.getAttribute("data-hovercard-user");
    if (!id) return;
    window.clearTimeout(hideTimer);
    window.clearTimeout(showTimer);
    showTimer = window.setTimeout(function () {
      if (cache.has(id)) {
        show(el, id);
        return;
      }
      fetch("/people/" + encodeURIComponent(id) + "/card/", {
        credentials: "same-origin",
        headers: { Accept: "text/html" },
      })
        .then(function (resp) {
          return resp.ok ? resp.text() : null;
        })
        .then(function (html) {
          cache.set(id, html);
          if (html !== null) show(el, id);
        })
        .catch(function () {
          /* transient network failure: allow a later retry */
        });
    }, SHOW_DELAY);
  }

  function closestTrigger(node) {
    return node && node.closest ? node.closest("[data-hovercard-user]") : null;
  }

  document.addEventListener("mouseover", function (e) {
    var el = closestTrigger(e.target);
    if (el) request(el);
    else if (anchor && !(card && card.contains(e.target))) scheduleHide();
  });

  document.addEventListener("focusin", function (e) {
    var el = closestTrigger(e.target);
    if (el) request(el);
  });

  document.addEventListener("focusout", function (e) {
    if (closestTrigger(e.target)) scheduleHide();
  });

  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape") hide();
  });
})();
