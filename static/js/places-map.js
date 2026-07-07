(function () {
  "use strict";

  var STYLE_URL = "https://tiles.openfreemap.org/styles/liberty";
  var DEFAULT_CENTER = [23.591, 46.77];
  var DEFAULT_ZOOM = 12;

  var mapEl = document.getElementById("map");
  if (!mapEl || typeof maplibregl === "undefined") return;

  var placesUrl = mapEl.dataset.placesUrl || "/api/places/?page_size=500";
  var placePath = mapEl.dataset.placePath || "/places/";
  var workerUrl = mapEl.dataset.workerUrl || "/static/vendor/maplibre/maplibre-gl-csp-worker.js";
  var unnamed = mapEl.dataset.unnamed || "Unnamed place";
  var openNowLabel = mapEl.dataset.openNowLabel || "Open now";
  var linkLabel = mapEl.dataset.linkLabel || "View";
  var rootStyle = window.getComputedStyle(document.documentElement);
  var activeFilters = { category: "", hasUpcoming: false, openNow: false };
  var lastApiData = emptyFeatureCollection();
  var loaded = false;

  function cssVar(name, fallback) {
    return rootStyle.getPropertyValue(name).trim() || fallback;
  }

  function emptyFeatureCollection() {
    return { type: "FeatureCollection", features: [] };
  }

  function normalizeList(value) {
    if (Array.isArray(value)) return value;
    if (typeof value === "string" && value) return [value];
    return [];
  }

  function filteredData(data) {
    var features = (data && data.features ? data.features : []).filter(function (feature) {
      var props = feature.properties || {};
      return !activeFilters.openNow || props.open_now === true;
    });
    return { type: "FeatureCollection", features: features };
  }

  function placesRequestUrl() {
    var url = new URL(placesUrl, window.location.origin);
    if (activeFilters.category) {
      url.searchParams.set("category", activeFilters.category);
    } else {
      url.searchParams.delete("category");
    }
    if (activeFilters.hasUpcoming) {
      url.searchParams.set("has_upcoming", "true");
    } else {
      url.searchParams.delete("has_upcoming");
    }
    return url.toString();
  }

  function setSourceData(data, fit) {
    var source = map.getSource("places");
    if (!source) return;
    var visibleData = filteredData(data);
    source.setData(visibleData);
    if (fit) fitToFeatures(visibleData.features);
  }

  function loadPlaces(fit) {
    fetch(placesRequestUrl(), { headers: { Accept: "application/json" } })
      .then(function (resp) {
        if (!resp.ok) throw new Error("places fetch failed");
        return resp.json();
      })
      .then(function (data) {
        lastApiData = data && data.type === "FeatureCollection" ? data : emptyFeatureCollection();
        setSourceData(lastApiData, fit);
      })
      .catch(function () {
        lastApiData = emptyFeatureCollection();
        setSourceData(lastApiData, false);
      });
  }

  function fitToFeatures(features) {
    if (!features.length) return;
    if (features.length === 1) {
      var only = features[0].geometry && features[0].geometry.coordinates;
      if (only) map.easeTo({ center: only, zoom: Math.max(map.getZoom(), 14) });
      return;
    }
    var bounds = new maplibregl.LngLatBounds();
    features.forEach(function (feature) {
      var coords = feature.geometry && feature.geometry.coordinates;
      if (coords) bounds.extend(coords);
    });
    if (!bounds.isEmpty()) map.fitBounds(bounds, { padding: 42, maxZoom: 15 });
  }

  function buildPopup(feature) {
    var props = feature.properties || {};
    var placeId = feature.id || props.id;
    var wrapper = document.createElement("div");
    wrapper.className = "map-popup";

    var title = document.createElement(placeId ? "a" : "strong");
    title.className = "map-popup-title";
    title.textContent = props.name || unnamed;
    if (placeId) title.href = placePath + placeId + "/";
    wrapper.appendChild(title);

    var labels = normalizeList(props.category_labels).slice(0, 3);
    if (labels.length) {
      var chips = document.createElement("div");
      chips.className = "map-popup-chips";
      labels.forEach(function (label) {
        var chip = document.createElement("span");
        chip.className = "map-popup-chip";
        chip.textContent = label;
        chips.appendChild(chip);
      });
      wrapper.appendChild(chips);
    }

    if (props.open_now === true) {
      var open = document.createElement("div");
      open.className = "map-popup-open";
      var dot = document.createElement("span");
      dot.className = "map-popup-open-dot";
      dot.setAttribute("aria-hidden", "true");
      var text = document.createElement("span");
      text.textContent = openNowLabel;
      open.appendChild(dot);
      open.appendChild(text);
      wrapper.appendChild(open);
    }

    if (placeId) {
      var link = document.createElement("a");
      link.className = "map-popup-link";
      link.href = placePath + placeId + "/";
      link.textContent = linkLabel;
      wrapper.appendChild(link);
    }

    return wrapper;
  }

  function setChipState() {
    document.querySelectorAll("[data-map-filter]").forEach(function (button) {
      var kind = button.dataset.mapFilter;
      var pressed = false;
      if (kind === "category") pressed = activeFilters.category === button.dataset.filterValue;
      if (kind === "has-upcoming") pressed = activeFilters.hasUpcoming;
      if (kind === "open-now") pressed = activeFilters.openNow;
      button.classList.toggle("is-active", pressed);
      button.setAttribute("aria-pressed", pressed ? "true" : "false");
    });
  }

  function bindFilters() {
    document.querySelectorAll("[data-map-filter]").forEach(function (button) {
      button.addEventListener("click", function () {
        var kind = button.dataset.mapFilter;
        if (kind === "category") {
          var value = button.dataset.filterValue || "";
          activeFilters.category = activeFilters.category === value ? "" : value;
          setChipState();
          loadPlaces(true);
          return;
        }
        if (kind === "has-upcoming") {
          activeFilters.hasUpcoming = !activeFilters.hasUpcoming;
          setChipState();
          loadPlaces(true);
          return;
        }
        if (kind === "open-now") {
          activeFilters.openNow = !activeFilters.openNow;
          setChipState();
          setSourceData(lastApiData, true);
        }
      });
    });
  }

  if (typeof maplibregl.setWorkerUrl === "function") {
    maplibregl.setWorkerUrl(workerUrl);
  } else {
    maplibregl.workerUrl = workerUrl;
  }

  var map = new maplibregl.Map({
    container: mapEl,
    style: STYLE_URL,
    center: DEFAULT_CENTER,
    zoom: DEFAULT_ZOOM,
    attributionControl: {
      compact: true,
      customAttribution: "\u00a9 OpenStreetMap contributors"
    }
  });

  map.addControl(new maplibregl.NavigationControl({ showCompass: false }), "top-right");

  map.on("load", function () {
    loaded = true;
    map.addSource("places", {
      type: "geojson",
      data: emptyFeatureCollection(),
      cluster: true,
      clusterMaxZoom: 15,
      clusterRadius: 44
    });

    map.addLayer({
      id: "place-clusters",
      type: "circle",
      source: "places",
      filter: ["has", "point_count"],
      paint: {
        "circle-color": ["step", ["get", "point_count"], cssVar("--accent-2", "#0d9488"), 20, cssVar("--accent", "#4f46e5"), 60, cssVar("--accent-strong", "#4338ca")],
        "circle-radius": ["step", ["get", "point_count"], 18, 20, 24, 60, 31],
        "circle-stroke-width": 2,
        "circle-stroke-color": cssVar("--card", "#ffffff")
      }
    });

    map.addLayer({
      id: "place-cluster-count",
      type: "symbol",
      source: "places",
      filter: ["has", "point_count"],
      layout: {
        "text-field": ["get", "point_count_abbreviated"],
        "text-size": 12
      },
      paint: { "text-color": cssVar("--accent-fg", "#ffffff") }
    });

    map.addLayer({
      id: "place-points",
      type: "circle",
      source: "places",
      filter: ["!", ["has", "point_count"]],
      paint: {
        "circle-color": cssVar("--accent", "#4f46e5"),
        "circle-radius": 8,
        "circle-stroke-width": 2,
        "circle-stroke-color": cssVar("--card", "#ffffff")
      }
    });

    map.on("click", "place-clusters", function (event) {
      var features = map.queryRenderedFeatures(event.point, { layers: ["place-clusters"] });
      var clusterId = features[0] && features[0].properties.cluster_id;
      var source = map.getSource("places");
      if (clusterId === undefined || !source) return;
      var finish = function (err, zoom) {
        if (err) return;
        map.easeTo({ center: features[0].geometry.coordinates, zoom: zoom });
      };
      var result = source.getClusterExpansionZoom(clusterId, finish);
      if (result && typeof result.then === "function") result.then(function (zoom) { finish(null, zoom); });
    });

    map.on("click", "place-points", function (event) {
      var feature = event.features && event.features[0];
      if (!feature) return;
      new maplibregl.Popup({ closeButton: true, maxWidth: "280px" })
        .setLngLat(feature.geometry.coordinates)
        .setDOMContent(buildPopup(feature))
        .addTo(map);
    });

    ["place-clusters", "place-points"].forEach(function (layer) {
      map.on("mouseenter", layer, function () { map.getCanvas().classList.add("is-clickable"); });
      map.on("mouseleave", layer, function () { map.getCanvas().classList.remove("is-clickable"); });
    });

    bindFilters();
    setChipState();
    loadPlaces(true);
  });

  map.on("error", function () {
    if (!loaded) return;
    /* the text-list fallback link remains available */
  });
})();
