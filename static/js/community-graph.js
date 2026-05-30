// 3D community-graph navigator — progressive enhancement over the server-rendered list.
// Reads the cohort-walled /api/communities/communities/graph/ endpoint (a child only ever
// receives child nodes; no member counts). Capability-gated so cheap/low-power/reduced-motion
// devices fall back to the text list instead of loading WebGL.
(function () {
  var el = document.getElementById("mz-graph");
  if (!el) return;

  function fallback(msg) {
    el.innerHTML = '<p class="muted" style="padding:1rem">' + msg +
      ' <a href="/communities/">Browse the list</a>.</p>';
  }
  function hasWebGL() {
    try {
      var c = document.createElement("canvas");
      return !!(window.WebGLRenderingContext &&
        (c.getContext("webgl") || c.getContext("experimental-webgl")));
    } catch (e) { return false; }
  }
  var saveData = navigator.connection && navigator.connection.saveData;
  var reduce = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  if (saveData || reduce || !hasWebGL()) {
    fallback("The 3D view is off on this device.");
    return;
  }

  var COLORS = { category: "#37d27a", type: "#5aa7d6", community: "#e0b14a" };
  var showCounts = true;
  function nodeLabel(n) {
    if (n.kind === "community" && showCounts && typeof n.activity_count === "number") {
      return n.label + " (" + n.activity_count + ")";
    }
    return n.label;
  }

  fetch("/api/communities/communities/graph/", { headers: { Accept: "application/json" } })
    .then(function (r) { return r.ok ? r.json() : { nodes: [], links: [] }; })
    .then(function (data) {
      if (!data.nodes || !data.nodes.length) { fallback("No communities to graph yet."); return; }
      if (typeof ForceGraph3D === "undefined") { fallback("The 3D library didn't load."); return; }

      var Graph = ForceGraph3D()(el)
        .backgroundColor("#0b1411")
        .graphData(data)
        .nodeLabel(nodeLabel)
        .nodeColor(function (n) { return COLORS[n.kind] || "#999"; })
        .nodeVal(function (n) {
          if (n.kind === "category") return 8;
          if (n.kind === "type") return 4;
          // Community node size reflects RELEVANCE = its upcoming-activity count.
          return 1.5 + Math.min(7, n.activity_count || 0);
        })
        .linkColor(function () { return "rgba(150,170,160,0.35)"; })
        .linkWidth(0.6)
        .cooldownTicks(90) // settle then idle — no perpetual physics (battery on cheap phones)
        .onNodeClick(function (n) {
          if (n.drill) { window.location.href = n.drill; return; } // community -> its page
          Graph.cameraPosition({ x: n.x, y: n.y, z: (n.z || 0) + 140 }, n, 800);
        });
      Graph.width(el.clientWidth).height(el.clientHeight);
      window.addEventListener("resize", function () { Graph.width(el.clientWidth); });

      var search = document.getElementById("mz-graph-search");
      if (search) search.addEventListener("input", function () {
        var q = search.value.trim().toLowerCase();
        if (!q) return;
        var hit = data.nodes.find(function (n) { return n.label.toLowerCase().indexOf(q) >= 0; });
        if (hit && hit.x != null) {
          Graph.cameraPosition({ x: hit.x, y: hit.y, z: (hit.z || 0) + 140 }, hit, 800);
        }
      });
      var counts = document.getElementById("mz-graph-counts");
      if (counts) counts.addEventListener("change", function () {
        showCounts = counts.checked;
        Graph.nodeLabel(nodeLabel); // re-apply the hover-label accessor
      });
    })
    .catch(function () { fallback("Couldn't load the graph."); });
})();
