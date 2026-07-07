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
  var searchInput = document.getElementById("map-search");
  var searchListbox = document.getElementById("map-search-listbox");
  var searchClear = document.querySelector("[data-map-search-clear]");
  var activeFilters = { category: "", hasUpcoming: false, openNow: false, concept: null, query: "" };
  var lastApiData = emptyFeatureCollection();
  var loaded = false;
  var activePopup = null;
  var vocabulary = buildVocabulary();
  var vocabularyBySlug = vocabulary.bySlug;
  var suggestionItems = [];
  var activeSuggestion = -1;
  var searchTimer = null;

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

  function normalizeText(value) {
    return String(value || "")
      .normalize("NFD")
      .replace(/[\u0300-\u036f]/g, "")
      .toLowerCase();
  }

  function uniqueNormalized(values) {
    var seen = {};
    return values.filter(function (value) {
      var key = normalizeText(value);
      if (!key || seen[key]) return false;
      seen[key] = true;
      return true;
    });
  }

  function haystack(values) {
    return normalizeText(uniqueNormalized(values).join(" "));
  }

  function buildVocabulary() {
    var out = [];
    var categories = {};
    var bySlug = {};
    if (!searchInput) return { items: out, bySlug: bySlug };
    var islandId = searchInput.dataset.vocabIsland || "";
    var island = islandId ? document.getElementById(islandId) : null;
    var rows = [];
    if (island && island.textContent) {
      try {
        rows = JSON.parse(island.textContent);
      } catch (err) {
        rows = [];
      }
    }
    rows.forEach(function (row) {
      if (!row || !row.slug || !row.name) return;
      var aliases = normalizeList(row.aliases);
      var item = {
        kind: "concept",
        conceptKind: "type",
        slug: row.slug,
        name: row.name,
        aliases: aliases,
        category: row.category || "",
        categoryName: row.categoryName || "",
        label: row.name,
        meta: row.categoryName || "Activity",
        searchText: haystack([row.slug, row.name, row.category, row.categoryName].concat(aliases))
      };
      out.push(item);
      bySlug[normalizeText(row.slug)] = item;
      if (row.category && !categories[row.category]) {
        categories[row.category] = {
          kind: "concept",
          conceptKind: "category",
          slug: row.category,
          name: row.categoryName || row.category,
          aliases: [],
          category: row.category,
          categoryName: row.categoryName || row.category,
          label: row.categoryName || row.category,
          meta: "Category",
          searchText: haystack([row.category, row.categoryName])
        };
      }
    });
    Object.keys(categories).forEach(function (slug) {
      out.push(categories[slug]);
    });
    return { items: out, bySlug: bySlug };
  }

  function featureSearchText(feature) {
    var props = feature.properties || {};
    var values = [props.name].concat(normalizeList(props.categories), normalizeList(props.category_labels));
    normalizeList(props.activities).forEach(function (activity) {
      if (!activity) return;
      values.push(activity.slug, activity.name);
      var vocab = vocabularyBySlug[normalizeText(activity.slug)];
      if (vocab) {
        values.push(vocab.name, vocab.category, vocab.categoryName);
        values = values.concat(vocab.aliases || []);
      }
    });
    return haystack(values);
  }

  function valueMatchesAny(value, needles) {
    var normalized = normalizeText(value);
    return needles.some(function (needle) {
      return normalized === normalizeText(needle);
    });
  }

  function featureMatchesConcept(feature, concept) {
    if (!concept) return true;
    var props = feature.properties || {};
    var needles = uniqueNormalized([concept.slug, concept.name].concat(concept.aliases || []));
    if (normalizeList(props.categories).some(function (slug) { return valueMatchesAny(slug, needles); })) {
      return true;
    }
    if (
      concept.conceptKind === "category" &&
      normalizeList(props.category_labels).some(function (label) { return valueMatchesAny(label, needles); })
    ) {
      return true;
    }
    return normalizeList(props.activities).some(function (activity) {
      return activity && (valueMatchesAny(activity.slug, needles) || valueMatchesAny(activity.name, needles));
    });
  }

  function featureMatchesFreeQuery(feature) {
    var query = normalizeText(activeFilters.query);
    if (!query) return true;
    return featureSearchText(feature).indexOf(query) !== -1;
  }

  function filteredData(data) {
    var features = (data && data.features ? data.features : []).filter(function (feature) {
      var props = feature.properties || {};
      if (activeFilters.category && normalizeList(props.categories).indexOf(activeFilters.category) === -1) {
        return false;
      }
      if (activeFilters.hasUpcoming && props.has_upcoming !== true) return false;
      if (activeFilters.openNow && props.open_now !== true) return false;
      if (!featureMatchesConcept(feature, activeFilters.concept)) return false;
      if (!activeFilters.concept && !featureMatchesFreeQuery(feature)) return false;
      return true;
    });
    return { type: "FeatureCollection", features: features };
  }

  function placesRequestUrl() {
    return new URL(placesUrl, window.location.origin).toString();
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

    if (typeof props.image_thumb === "string" && props.image_thumb) {
      var thumb = document.createElement("img");
      thumb.className = "map-popup-thumb";
      thumb.src = props.image_thumb;
      thumb.alt = "";
      wrapper.appendChild(thumb);
    }

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

  function openFeaturePopup(feature, fly) {
    var coords = feature.geometry && feature.geometry.coordinates;
    if (!coords) return;
    if (activePopup) activePopup.remove();
    if (fly) map.easeTo({ center: coords, zoom: Math.max(map.getZoom(), 15) });
    activePopup = new maplibregl.Popup({ closeButton: true, maxWidth: "280px" })
      .setLngLat(coords)
      .setDOMContent(buildPopup(feature))
      .addTo(map);
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
          setSourceData(lastApiData, true);
          return;
        }
        if (kind === "has-upcoming") {
          activeFilters.hasUpcoming = !activeFilters.hasUpcoming;
          setChipState();
          setSourceData(lastApiData, true);
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

  function hideSuggestions() {
    if (!searchListbox || !searchInput) return;
    searchListbox.hidden = true;
    searchInput.setAttribute("aria-expanded", "false");
    searchInput.removeAttribute("aria-activedescendant");
    activeSuggestion = -1;
  }

  function setActiveSuggestion(index) {
    activeSuggestion = index;
    Array.prototype.forEach.call(searchListbox.children, function (row, rowIndex) {
      var active = rowIndex === activeSuggestion;
      row.classList.toggle("is-active", active);
      row.setAttribute("aria-selected", active ? "true" : "false");
      if (active && searchInput) searchInput.setAttribute("aria-activedescendant", row.id);
    });
  }

  function placeSuggestions(query) {
    var seen = {};
    return (lastApiData.features || []).reduce(function (items, feature) {
      var props = feature.properties || {};
      var name = props.name || unnamed;
      var key = String(feature.id || props.id || name);
      if (seen[key] || normalizeText(name).indexOf(query) === -1) return items;
      seen[key] = true;
      items.push({ kind: "place", label: name, meta: "Place", feature: feature });
      return items;
    }, []);
  }

  function matchingSuggestions() {
    var query = normalizeText(searchInput ? searchInput.value : "");
    if (!query) return [];
    var concepts = vocabulary.items.filter(function (item) {
      return item.searchText.indexOf(query) !== -1;
    });
    return concepts.slice(0, 6).concat(placeSuggestions(query).slice(0, 6)).slice(0, 8);
  }

  function renderSuggestions() {
    if (!searchInput || !searchListbox) return;
    suggestionItems = matchingSuggestions();
    searchListbox.textContent = "";
    if (!suggestionItems.length) {
      hideSuggestions();
      return;
    }
    suggestionItems.forEach(function (item, index) {
      var row = document.createElement("button");
      row.type = "button";
      row.id = "map-search-option-" + index;
      row.className = "map-search-option";
      row.setAttribute("role", "option");
      row.setAttribute("aria-selected", "false");
      row.addEventListener("click", function () { selectSuggestion(index); });
      var label = document.createElement("span");
      label.className = "map-search-option-label";
      label.textContent = item.label;
      var meta = document.createElement("span");
      meta.className = "map-search-option-meta";
      meta.textContent = item.meta;
      row.appendChild(label);
      row.appendChild(meta);
      searchListbox.appendChild(row);
    });
    searchListbox.hidden = false;
    searchInput.setAttribute("aria-expanded", "true");
    setActiveSuggestion(0);
  }

  function updateClearButton() {
    if (!searchClear) return;
    searchClear.hidden = !(activeFilters.query || activeFilters.concept);
  }

  function applySearch(fit) {
    if (!searchInput) return;
    activeFilters.query = searchInput.value;
    setSourceData(lastApiData, fit);
    updateClearButton();
  }

  function scheduleSearchUpdate() {
    window.clearTimeout(searchTimer);
    searchTimer = window.setTimeout(function () {
      applySearch(false);
      renderSuggestions();
    }, 150);
  }

  function selectSuggestion(index) {
    var item = suggestionItems[index];
    if (!item || !searchInput) return;
    if (item.kind === "concept") {
      activeFilters.concept = item;
      activeFilters.query = item.label;
      searchInput.value = item.label;
      setSourceData(lastApiData, true);
    } else if (item.kind === "place") {
      activeFilters.concept = null;
      activeFilters.query = item.label;
      searchInput.value = item.label;
      setSourceData(lastApiData, false);
      openFeaturePopup(item.feature, true);
    }
    updateClearButton();
    hideSuggestions();
  }

  function clearSearch() {
    if (!searchInput) return;
    searchInput.value = "";
    activeFilters.concept = null;
    activeFilters.query = "";
    hideSuggestions();
    updateClearButton();
    setSourceData(lastApiData, true);
    searchInput.focus();
  }

  function bindSearch() {
    if (!searchInput || !searchListbox) return;
    searchInput.addEventListener("input", function () {
      activeFilters.concept = null;
      scheduleSearchUpdate();
    });
    searchInput.addEventListener("focus", renderSuggestions);
    searchInput.addEventListener("blur", function () {
      window.setTimeout(hideSuggestions, 120);
    });
    searchInput.addEventListener("keydown", function (event) {
      if (event.key === "ArrowDown") {
        event.preventDefault();
        if (searchListbox.hidden) renderSuggestions();
        else setActiveSuggestion(Math.min(activeSuggestion + 1, suggestionItems.length - 1));
      } else if (event.key === "ArrowUp") {
        event.preventDefault();
        if (searchListbox.hidden) renderSuggestions();
        else setActiveSuggestion(Math.max(activeSuggestion - 1, 0));
      } else if (event.key === "Enter" && !searchListbox.hidden && activeSuggestion >= 0) {
        event.preventDefault();
        selectSuggestion(activeSuggestion);
      } else if (event.key === "Escape") {
        event.preventDefault();
        if (!searchListbox.hidden) hideSuggestions();
        else clearSearch();
      }
    });
    if (searchClear) searchClear.addEventListener("click", clearSearch);
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
      openFeaturePopup(feature, false);
    });

    ["place-clusters", "place-points"].forEach(function (layer) {
      map.on("mouseenter", layer, function () { map.getCanvas().classList.add("is-clickable"); });
      map.on("mouseleave", layer, function () { map.getCanvas().classList.remove("is-clickable"); });
    });

    bindFilters();
    bindSearch();
    setChipState();
    loadPlaces(true);
  });

  map.on("error", function () {
    if (!loaded) return;
    /* the text-list fallback link remains available */
  });
})();
