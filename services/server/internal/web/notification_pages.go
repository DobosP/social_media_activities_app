package web

import (
	"net/http"

	"github.com/flosch/pongo2/v6"
)

func (s *Server) notificationPage(r *http.Request) (pongo2.Context, error) {
	value, err := s.get(r, "/api/notifications/")
	if err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for _, record := range results(value) {
		row := spaMap(record)
		item := map[string]any{}
		for key, value := range row {
			item[key] = value
		}
		item["why"] = row["reason"]
		// The source template and SPA only consume read_at's presence. The API
		// exposes a boolean rather than a timestamp, so retain that distinction.
		item["read_at"] = nil
		if spaBool(row["is_read"]) {
			item["read_at"] = true
		}
		items = append(items, item)
	}
	return pongo2.Context{"items": items}, nil
}
