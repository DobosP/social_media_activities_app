package web

import (
	"net/http"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
)

// Local-POC presentation (ADR-0057). Rows a development-only RO-EDU local POC
// sync wrote carry `local_poc`; templates label each with the visible marker,
// and base.html shows the banner whenever the POC gate is on or the page shows
// a POC row. Production rows never carry the flag, so production pages render
// exactly as before.

// publicLocalPOCPastSQL lists upcoming events as publicUpcomingSQL does, plus
// PAST POC rows (scheduled/rescheduled/sold_out/expired). Only reachable with
// the POC gate on and an explicit include_past=1 (localPOCPast).
const publicLocalPOCPastSQL = `NOT e.is_tombstone AND NOT e.is_import_held AND ((e.starts_at>=now() AND e.lifecycle_status IN('scheduled','rescheduled','sold_out')) OR (` + catalog.EventLocalPOCSQL + ` AND e.starts_at<now() AND e.lifecycle_status IN('scheduled','rescheduled','sold_out','expired')))`

const publicLocalPOCPastOnlySQL = `NOT e.is_tombstone AND NOT e.is_import_held AND ` + catalog.EventLocalPOCSQL + ` AND e.starts_at<now() AND e.lifecycle_status IN('scheduled','rescheduled','sold_out','expired')`

// Compatibility mode keeps upcoming first; past_only avoids those rows taking
// the whole bounded list when a city has more than 100 upcoming events.
const publicLocalPOCPastOrder = `(e.starts_at<now()),CASE WHEN e.starts_at>=now() THEN e.starts_at END,e.starts_at DESC,e.id`

func (s *Server) localPOCPast(r *http.Request) bool {
	return s.Config.LocalPOC && r.URL.Query().Get("include_past") == "1"
}

func (s *Server) localPOCPastOnly(r *http.Request) bool {
	return s.Config.LocalPOC && r.URL.Query().Get("past_only") == "1"
}

func markLocalPOCEvent(e map[string]any) {
	e["local_poc"] = spaBool(e["local_poc"]) || spaBool(spaMap(e["place"])["local_poc"])
	if starts, ok := spaDateValue(e["starts_at"]); ok && spaBool(e["local_poc"]) && starts.Before(time.Now()) {
		e["local_poc_past"] = true
	}
}

func anyLocalPOC(rows []map[string]any) bool {
	for _, row := range rows {
		if spaBool(row["local_poc"]) {
			return true
		}
	}
	return false
}
