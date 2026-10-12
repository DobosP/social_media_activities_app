package web

import (
	"net/http"
	"net/url"
	"strings"
)

// Navigation carries only supported filters. Feeds intentionally omit free-text
// and history, while view switches preserve every discovery filter.
func eventFilterValues(r *http.Request, feed bool) url.Values {
	values := url.Values{}
	for _, key := range []string{"city", "activity", "area", "q"} {
		if feed && key == "q" {
			continue
		}
		if value := strings.TrimSpace(r.URL.Query().Get(key)); value != "" {
			values.Set(key, value)
		}
	}
	return values
}

func eventFilterURL(path string, values url.Values) string {
	if query := values.Encode(); query != "" {
		return path + "?" + query
	}
	return path
}

func (s *Server) eventViewURL(r *http.Request, mode string, clear bool) string {
	values := eventFilterValues(r, false)
	if clear {
		values = url.Values{}
	}
	if s.Config.LocalPOC && (mode == "include_past" || mode == "past_only") {
		values.Set(mode, "1")
	}
	return eventFilterURL(routeURL("events_list"), values)
}
