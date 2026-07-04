(function () {
  "use strict";

  var mapEl = document.getElementById("map");
  var select = document.getElementById("id_place");
  if (!mapEl || !select || typeof L === "undefined") return;

  var section = document.getElementById("place-picker");
  if (section) section.hidden = false;

  var statusEl = document.getElementById("place-picker-status");
  var chooseLabel = mapEl.dataset.chooseLabel || "Choose this place";
  var selectedPrefix = mapEl.dataset.selectedPrefix || "Selected place:";
  var unnamed = mapEl.dataset.unnamed || "Unnamed place";
  var placesUrl = mapEl.dataset.placesUrl || "/api/places/";

  var map = L.map("map").setView([46.77, 23.591], 13);
  L.tileLayer("https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png", {
    maxZoom: 19,
    attribution: "&copy; OpenStreetMap contributors",
  }).addTo(map);
  var layer = L.layerGroup().addTo(map);

  function optionFor(id) {
    return select.querySelector('option[value="' + id + '"]');
  }

  function selectPlace(id, name) {
    if (!optionFor(id)) return;
    select.value = String(id);
    select.dispatchEvent(new Event("change", { bubbles: true }));
    if (statusEl) statusEl.textContent = selectedPrefix + " " + name;
  }

  function render(features, recenter) {
    layer.clearLayers();
    var points = [];
    features.forEach(function (feature) {
      var coords = feature.geometry && feature.geometry.coordinates;
      if (!coords || !optionFor(feature.id)) return;
      var name = (feature.properties && feature.properties.name) || unnamed;
      var marker = L.marker([coords[1], coords[0]]).addTo(layer);
      var box = document.createElement("div");
      var strong = document.createElement("strong");
      var btn = document.createElement("button");
      strong.textContent = name;
      btn.type = "button";
      btn.className = "btn btn-sm";
      btn.textContent = chooseLabel;
      btn.addEventListener("click", function () {
        selectPlace(feature.id, name);
        map.closePopup();
      });
      box.appendChild(strong);
      box.appendChild(document.createElement("br"));
      box.appendChild(btn);
      marker.bindPopup(box);
      points.push([coords[1], coords[0]]);
    });
    if (recenter) map.setView(recenter, 14);
    else if (points.length) map.fitBounds(points, { padding: [30, 30] });
  }

  function load(query, recenter) {
    fetch(placesUrl + query, { headers: { Accept: "application/json" } })
      .then(function (resp) {
        return resp.json();
      })
      .then(function (data) {
        render(data.features || [], recenter);
      })
      .catch(function () {
        /* leave the dropdown as the fallback */
      });
  }

  load("?page_size=500");

  var btn = document.getElementById("place-near-me");
  if (btn && navigator.geolocation) {
    btn.addEventListener("click", function () {
      var original = btn.textContent;
      btn.disabled = true;
      btn.textContent = btn.dataset.locating;
      navigator.geolocation.getCurrentPosition(
        function (pos) {
          var lon = pos.coords.longitude.toFixed(6);
          var lat = pos.coords.latitude.toFixed(6);
          load("?near_lon=" + lon + "&near_lat=" + lat + "&radius_m=8000&page_size=200", [
            lat,
            lon,
          ]);
          btn.disabled = false;
          btn.textContent = original;
        },
        function () {
          btn.disabled = false;
          btn.textContent = btn.dataset.unavailable;
        }
      );
    });
  } else if (btn) {
    btn.hidden = true;
  }

  select.addEventListener("change", function () {
    if (!statusEl) return;
    var opt = optionFor(select.value);
    statusEl.textContent = opt ? selectedPrefix + " " + opt.textContent : "";
  });
})();
