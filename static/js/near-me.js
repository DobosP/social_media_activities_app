(function () {
  "use strict";

  document.querySelectorAll("[data-near-me]").forEach(function (btn) {
    if (!navigator.geolocation) {
      btn.hidden = true;
      return;
    }
    btn.addEventListener("click", function () {
      btn.disabled = true;
      btn.textContent = btn.dataset.locating || "Locating...";
      navigator.geolocation.getCurrentPosition(
        function (pos) {
          var url = new URL(window.location.href);
          url.searchParams.set("near_lon", pos.coords.longitude.toFixed(6));
          url.searchParams.set("near_lat", pos.coords.latitude.toFixed(6));
          window.location.href = url.toString();
        },
        function () {
          btn.disabled = false;
          btn.textContent = btn.dataset.unavailable || "Location unavailable";
        }
      );
    });
  });
})();
