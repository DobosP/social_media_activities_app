/* Native workflow presentation only; retain Leaflet's labelled zoom actions. */
(function () {
  "use strict";
  if (!window.L || !L.Control || !L.Control.Zoom) return;
  var icon = function (vertical) {
    return '<svg viewBox="0 0 20 20" width="20" height="20" aria-hidden="true" focusable="false">' +
      '<path d="M4 10h12' + (vertical ? 'M10 4v12' : '') +
      '" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>';
  };
  L.Control.Zoom.mergeOptions({ zoomInText: icon(true), zoomOutText: icon(false) });
})();
