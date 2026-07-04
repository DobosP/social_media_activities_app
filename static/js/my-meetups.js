(function () {
  "use strict";

  var note = document.getElementById("offline-note");
  if (!note) return;

  function reveal() {
    note.hidden = false;
  }

  function probe() {
    if (!navigator.onLine) {
      reveal();
      return;
    }
    fetch("/healthz", { cache: "no-store" })
      .then(function (resp) {
        note.hidden = !!(resp && resp.ok);
      })
      .catch(reveal);
  }

  probe();
  window.addEventListener("offline", reveal);
  window.addEventListener("online", probe);
})();
