(function () {
  "use strict";

  var mapEl = document.getElementById("map");
  if (!mapEl || typeof L === "undefined") return;

  var placesUrl = mapEl.dataset.placesUrl || "/api/places/?page_size=500";
  var placePath = mapEl.dataset.placePath || "/places/";
  var unnamed = mapEl.dataset.unnamed || "Unnamed place";
  var linkLabel = mapEl.dataset.linkLabel || "View";

  var map = L.map("map").setView([46.77, 23.591], 13);
  L.tileLayer("https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png", {
    maxZoom: 19,
    attribution: "&copy; OpenStreetMap contributors",
  }).addTo(map);

  fetch(placesUrl, { headers: { Accept: "application/json" } })
    .then(function (resp) {
      return resp.json();
    })
    .then(function (data) {
      var points = [];
      (data.features || []).forEach(function (feature) {
        var coords = feature.geometry && feature.geometry.coordinates;
        if (!coords) return;
        var props = feature.properties || {};
        var marker = L.marker([coords[1], coords[0]]).addTo(map);
        var link = document.createElement("a");
        link.href = placePath + feature.id + "/";
        link.textContent = linkLabel;
        var box = document.createElement("div");
        var strong = document.createElement("strong");
        strong.textContent = props.name || unnamed;
        box.appendChild(strong);
        box.appendChild(document.createElement("br"));
        box.appendChild(link);
        marker.bindPopup(box);
        points.push([coords[1], coords[0]]);
      });
      if (points.length) map.fitBounds(points, { padding: [30, 30] });
    })
    .catch(function () {
      /* the text-list fallback link remains available */
    });
})();
