package web

import (
	_ "embed"
	"net/http"
)

// The original apps/web/views.py::_SERVICE_WORKER_JS is
// preserved byte-for-byte at source head ce3d0e3ee9f180ce2e95140300b12582db9895d6.
// Original script SHA-256: a6d20d592b64b4dcdc3816a1bbc5e21c727bfb665900bf3f5aca30669e80bbd9. Serving has no Python dependency.
//
//go:embed assets/meetups-worker.js
var nativeMeetupsWorker []byte

func serveMeetupsWorker(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Service-Worker-Allowed", "/")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(nativeMeetupsWorker)
}
