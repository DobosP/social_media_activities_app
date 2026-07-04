(function () {
  "use strict";

  var script = document.getElementById("site-js");

  document.addEventListener("change", function (ev) {
    var el = ev.target;
    if (!el || !el.matches("[data-auto-submit]")) return;
    if (el.form) el.form.submit();
  });

  document.addEventListener("submit", function (ev) {
    var form = ev.target;
    if (!form || !form.dataset || !form.dataset.confirm) return;
    if (!window.confirm(form.dataset.confirm)) ev.preventDefault();
  });

  if (!script || !script.dataset.meetupsOwner || !("serviceWorker" in navigator)) return;

  var owner = script.dataset.meetupsOwner;
  function purge() {
    try {
      if (window.caches) caches.delete("mz-meetups-v1");
      if (navigator.serviceWorker.controller) {
        navigator.serviceWorker.controller.postMessage({ type: "purge" });
      }
    } catch (e) {
      /* storage/SW may be unavailable; the server-rendered page still works */
    }
  }

  try {
    if (window.localStorage && localStorage.getItem("mz-meetups-owner") !== owner) {
      purge();
      localStorage.setItem("mz-meetups-owner", owner);
    }
  } catch (e) {
    /* blocked storage: skip ownership marker, keep the live page */
  }

  navigator.serviceWorker.register("/sw.js", { scope: "/" }).catch(function () {});
  document.querySelectorAll('form[action$="/logout/"]').forEach(function (form) {
    form.addEventListener("submit", function () {
      purge();
      try {
        if (window.localStorage) localStorage.removeItem("mz-meetups-owner");
      } catch (e) {
        /* blocked storage */
      }
    });
  });
})();
