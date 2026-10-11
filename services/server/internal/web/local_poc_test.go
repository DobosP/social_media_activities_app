package web

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

const localPOCMarkerHTML = "LOCAL POC — not cleared for publication"

func localPOCRender(t *testing.T, s *Server, name string, pk int64, path string) (map[string]any, string) {
	t.Helper()
	r := socialLegacyRequest("GET", path, platform.Actor{}, pk, nil)
	data, template, handled, err := s.PublicView(r, platform.Actor{}, name)
	if err != nil || !handled {
		t.Fatal(name, handled, err)
	}
	data["csrf"], data["local_poc_mode"] = "fixture", s.Config.LocalPOC
	w := httptest.NewRecorder()
	if err = s.Renderer.Render(w, r, template, data); err != nil {
		t.Fatal(name, err)
	}
	return data, w.Body.String()
}

// ADR-0057: rows a POC sync flagged carry the visible marker on event and
// place pages and lists in every mode; past POC events list only with the POC
// gate on and include_past=1; a database no POC sync touched renders unchanged.
func TestLocalPOCMarkerAndPastEventsOnPublicPages(t *testing.T) {
	s, _, place, typ := socialLegacyFixture(t)
	ctx := context.Background()
	upcoming := publicEventFixture(t, s, "POC upcoming concert", place, typ)
	_ = publicEventFixture(t, s, "Production upcoming concert", place, typ)
	pastPOC := publicEventFixture(t, s, "POC archived concert", place, typ)
	pastProduction := publicEventFixture(t, s, "Production archived concert", place, typ)
	if _, err := s.DB.Exec(ctx, `UPDATE events_event SET starts_at=now()-interval '400 days',lifecycle_status='expired' WHERE id IN($1,$2)`, pastPOC, pastProduction); err != nil {
		t.Fatal(err)
	}

	// No POC sync ever ran: no column, no marker, no banner, no past rows.
	_, html := localPOCRender(t, s, "events_list", 0, "/events/?include_past=1")
	if strings.Contains(html, localPOCMarkerHTML) || strings.Contains(html, "archived") || !strings.Contains(html, "POC upcoming concert") {
		t.Fatal("production page changed", html)
	}

	// What a POC sync writes (jobs.markLocalPOCEvent / jobs.LocalPOCTags).
	if _, err := s.DB.Exec(ctx, `ALTER TABLE events_event ADD COLUMN IF NOT EXISTS source_local_poc boolean NOT NULL DEFAULT false`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE events_event SET source_local_poc=true WHERE id IN($1,$2)`, upcoming, pastPOC); err != nil {
		t.Fatal(err)
	}
	data, html := localPOCRender(t, s, "events_list", 0, "/events/")
	if strings.Count(html, localPOCMarkerHTML) != 2 || data["local_poc_content"] != true || strings.Contains(html, "archived") {
		t.Fatal("POC row unmarked or past row listed by default", strings.Count(html, localPOCMarkerHTML), html)
	}
	// Gate off: include_past is not a POC switch.
	if _, html = localPOCRender(t, s, "events_list", 0, "/events/?include_past=1"); strings.Contains(html, "archived") || strings.Contains(html, "include_past=1\">") {
		t.Fatal("past events listed with the POC gate off", html)
	}
	s.Config.LocalPOC = true
	if _, html = localPOCRender(t, s, "events_list", 0, "/events/"); strings.Contains(html, "archived") || !strings.Contains(html, "?include_past=1\">Include past events") || !strings.Contains(html, "local-poc-banner") {
		t.Fatal("POC mode default listing", html)
	}
	_, html = localPOCRender(t, s, "events_list", 0, "/events/?include_past=1")
	if !strings.Contains(html, "POC archived concert") || strings.Contains(html, "Production archived concert") || !strings.Contains(html, "Past event") || strings.Index(html, "POC upcoming concert") > strings.Index(html, "POC archived concert") {
		t.Fatal("POC past listing", html)
	}

	_, html = localPOCRender(t, s, "event_detail", pastPOC, fmt.Sprintf("/events/%d/", pastPOC))
	if !strings.Contains(html, localPOCMarkerHTML) {
		t.Fatal("POC event detail unmarked")
	}
	s.Config.LocalPOC = false
	// A POC venue tag marks the place everywhere, in any mode.
	if _, html = localPOCRender(t, s, "places_list", 0, "/places/list/"); strings.Contains(html, localPOCMarkerHTML) {
		t.Fatal("production place marked")
	}
	if _, err := s.DB.Exec(ctx, `UPDATE places_place SET raw_tags=raw_tags||'{"roedu_local_poc":true}'::jsonb WHERE id=$1`, place); err != nil {
		t.Fatal(err)
	}
	if _, html = localPOCRender(t, s, "places_list", 0, "/places/list/"); !strings.Contains(html, localPOCMarkerHTML) || !strings.Contains(html, "local-poc-banner") {
		t.Fatal("POC place unmarked in list", html)
	}
	data, html = localPOCRender(t, s, "place_detail", place, fmt.Sprintf("/places/%d/", place))
	if !strings.Contains(html, localPOCMarkerHTML) || data["local_poc_content"] != true {
		t.Fatal("POC place detail unmarked")
	}
	// An event at a POC venue shows the venue's POC content, so it is marked too.
	data, _ = localPOCRender(t, s, "events_list", 0, "/events/")
	for _, e := range spaRows(data["events"]) {
		if e["local_poc"] != true {
			t.Fatal("event at a POC venue unmarked", e["title"])
		}
	}
}
