package web

import "net/http"

// mediaBusyMessage answers a full image/PDF codec queue on a classic form.
const mediaBusyMessage = "The server is busy processing media. Please try again in a few seconds."

// busyPage lets a form re-render its own view as a retryable 503. Fetch clients
// get platform.Fail's JSON instead. An explicit status (redirect, failure) wins.
func busyPage(w http.ResponseWriter) http.ResponseWriter {
	w.Header().Set("Retry-After", "5")
	return &statusPage{ResponseWriter: w, status: http.StatusServiceUnavailable}
}

type statusPage struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusPage) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *statusPage) WriteHeader(status int) {
	if !w.wrote {
		w.wrote = true
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *statusPage) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(w.status)
	}
	return w.ResponseWriter.Write(b)
}
