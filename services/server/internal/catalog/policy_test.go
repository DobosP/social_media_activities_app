package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

func TestPolicyDefaultsAndSafetyBounds(t *testing.T) {
	d := DefaultPolicy()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if (Policy{}).WithDefaults() != d || d.PlaceSQL() != PublicPlaceSQL || d.EventSQL() != publicEventSQL {
		t.Fatal("source defaults drifted")
	}
	for _, tc := range []struct {
		name string
		edit func(*Policy)
	}{
		{"closure threshold", func(p *Policy) { p.ClosureReportThreshold = 4 }},
		{"open threshold", func(p *Policy) { p.OpenNowReportThreshold = -1 }},
		{"event threshold", func(p *Policy) { p.EventReportThreshold = 4 }},
		{"fact quorum", func(p *Policy) { p.FactQuorum = 2 }},
		{"correction quorum", func(p *Policy) { p.CorrectionQuorum = 2 }},
		{"edge quorum", func(p *Policy) { p.EdgeQuorum = 101 }},
		{"closure decay floor", func(p *Policy) { p.ClosureReportDecay = 14*24*time.Hour - time.Second }},
		{"open decay precision", func(p *Policy) { p.OpenNowReportDecay += time.Nanosecond }},
		{"event decay ceiling", func(p *Policy) { p.EventReportDecay = 366 * 24 * time.Hour }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := d
			tc.edit(&p)
			if p.Validate() == nil {
				t.Fatal("unsafe policy accepted")
			}
			if p.PlaceSQL() != "false" || p.EventSQL() != "false" {
				t.Fatal("invalid policy bypassed public visibility")
			}
		})
	}
	if (Policy{}).Validate() == nil {
		t.Fatal("explicit zero settings accepted without normalization")
	}
}

func TestPolicyQueryAndOpenNowBehavior(t *testing.T) {
	p := DefaultPolicy()
	p.ClosureReportThreshold = 2
	p.ClosureReportDecay = 30 * 24 * time.Hour
	p.OpenNowReportDecay = 30 * 24 * time.Hour
	p.OpenNowReportThreshold = 2
	p.FactQuorum, p.CorrectionQuorum, p.EdgeQuorum = 4, 4, 4
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	placeQuery, args := p.placeWhere(PlaceQuery{}, platform.Actor{})
	eventQuery, _, _ := p.eventWhere(EventQuery{})
	for _, query := range []string{placeQuery, eventQuery} {
		if !strings.Contains(query, "interval '2592000 seconds'") || !strings.Contains(query, "count(*)>=2") || !strings.Contains(query, "pp.status='published'") {
			t.Fatal("query ignored policy or proposal gate", query)
		}
	}
	if len(args) != 9 || !strings.Contains(eventQuery, "NOT e.is_tombstone AND NOT e.is_import_held") || !strings.Contains(p.placeProjection(platform.Actor{}, "NULL"), "interval '2592000 seconds'") {
		t.Fatal("query contract drifted")
	}
	s := New(nil)
	s.Now = func() time.Time { return time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC) }
	raw := json.RawMessage(`{"id":1,"properties":{"opening_hours":{"su":[[0,1440]]}},"_corrected_hours":"","_reports":2}`)
	for _, tc := range []struct {
		policy Policy
		want   any
	}{{DefaultPolicy(), true}, {p, "unverified"}} {
		s.Policy = tc.policy
		out, err := s.finalizePlaces(context.Background(), []json.RawMessage{raw})
		if err != nil {
			t.Fatal(err)
		}
		obj := jsonObject(t, out[0])
		if obj["properties"].(map[string]any)["open_now"] != tc.want || obj["_reports"] != nil {
			t.Fatal("wrong open-now policy or leaked internal tally", obj)
		}
	}
}

func TestRuntimePolicyContextIsolation(t *testing.T) {
	base := context.Background()
	first := DefaultPolicy()
	first.ClosureReportThreshold = 1
	second := DefaultPolicy()
	second.ClosureReportDecay = 30 * 24 * time.Hour
	one, two := WithPolicy(base, first), WithPolicy(base, second)
	first.ClosureReportThreshold = -1
	if PolicyFromContext(base) != DefaultPolicy() || PolicyFromContext(one).ClosureReportThreshold != 1 || PolicyFromContext(two).ClosureReportDecay != 30*24*time.Hour {
		t.Fatal("context policy mutated or leaked across runtimes")
	}
	derived, cancel := context.WithCancel(one)
	cancel()
	if PolicyFromContext(context.WithoutCancel(derived)).ClosureReportThreshold != 1 {
		t.Fatal("derived lifetime lost catalog visibility")
	}
	if PolicyFromContext(WithPolicy(base, first)).PlaceSQL() != "false" {
		t.Fatal("invalid supplied policy silently reverted to defaults")
	}
}

func TestPostgresConfiguredPolicyReportsAndQuorums(t *testing.T) {
	s := fixture(t, true)
	ctx := context.Background()
	p := DefaultPolicy()
	p.ClosureReportThreshold, p.OpenNowReportThreshold, p.EventReportThreshold = 2, 2, 2
	p.ClosureReportDecay, p.OpenNowReportDecay, p.EventReportDecay = 30*24*time.Hour, 30*24*time.Hour, 30*24*time.Hour
	p.FactQuorum, p.CorrectionQuorum, p.EdgeQuorum = 4, 4, 4
	s.Policy = p
	actors := make([]platform.Actor, 5)
	for i, name := range []string{"policy-owner", "policy-one", "policy-two", "policy-three", "policy-four"} {
		actors[i] = user(t, s, name, "adult")
	}
	place := placeFixture(t, s, "Synthetic policy venue", "osm", 23.6, 46.77)
	for _, a := range actors[:3] {
		if err := s.VoteFact(ctx, a, place, "drinking_water", true); err != nil {
			t.Fatal(err)
		}
	}
	checkFact := func(want string) {
		t.Helper()
		facts, err := s.VenueFacts(ctx, actors[0], place, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, fact := range facts {
			if fact["key"] == "drinking_water" && (fact["state"] != want || fact["required"] != 4) {
				t.Fatal(fact)
			}
		}
	}
	checkFact("unknown")
	if err := s.VoteFact(ctx, actors[3], place, "drinking_water", true); err != nil {
		t.Fatal(err)
	}
	checkFact("true")
	correction, err := s.ProposeCorrection(ctx, actors[0], place, "name", "Synthetic corrected name")
	if err != nil {
		t.Fatal(err)
	}
	for i, a := range actors[1:] {
		if err := s.ConfirmCorrection(ctx, a, correction); err != nil {
			t.Fatal(err)
		}
		var state string
		var quorum int
		if err := s.DB.QueryRow(ctx, `SELECT status,required_confirmations FROM places_placecorrection WHERE id=$1`, correction).Scan(&state, &quorum); err != nil {
			t.Fatal(err)
		}
		if quorum != 4 || (i < 3 && state != "pending") || (i == 3 && state != "published") {
			t.Fatal(state, quorum)
		}
	}
	var edge int64
	if err := s.DB.QueryRow(ctx, `INSERT INTO places_placeactivity(place_id,activity_id,origin,confidence,source,mapping_rule,is_disputed,created_at,updated_at) SELECT $1,id,'inferred',0.9,'synthetic','',false,now(),now() FROM taxonomy_activitytype ORDER BY id LIMIT 1 RETURNING id`, place).Scan(&edge); err != nil {
		t.Fatal(err)
	}
	for i, a := range actors[:4] {
		if err := s.VoteEdge(ctx, a, edge, "confirm"); err != nil {
			t.Fatal(err)
		}
		var origin string
		if err := s.DB.QueryRow(ctx, `SELECT origin FROM places_placeactivity WHERE id=$1`, edge).Scan(&origin); err != nil {
			t.Fatal(err)
		}
		if (i < 3 && origin != "inferred") || (i == 3 && origin != "confirmed") {
			t.Fatal(origin)
		}
	}
	for _, a := range actors[:2] {
		if created, err := s.ReportVenue(ctx, a, place, false); err != nil || !created {
			t.Fatal(created, err)
		}
	}
	if _, err := s.DB.Exec(ctx, `UPDATE places_opennowreport SET created_at=now()-interval '20 days' WHERE place_id=$1`, place); err != nil {
		t.Fatal(err)
	}
	raw, err := s.Place(ctx, actors[0], place)
	if err != nil || jsonObject(t, raw)["properties"].(map[string]any)["open_now"] != "unverified" {
		t.Fatal("longer open-now report decay ignored", err)
	}
	for _, a := range actors[:2] {
		if created, err := s.ReportVenue(ctx, a, place, true); err != nil || !created {
			t.Fatal(created, err)
		}
	}
	if _, err := s.DB.Exec(ctx, `UPDATE places_placeclosurereport SET created_at=now()-interval '20 days' WHERE place_id=$1`, place); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Place(ctx, actors[0], place); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("closure override did not withhold venue", err)
	}
	if created, err := s.ReportVenue(ctx, actors[0], place, true); err != nil || created {
		t.Fatal("longer closure dedup ignored", created, err)
	}
	event := eventFixture(t, s, "Synthetic policy event", "scheduled", nil, time.Now().Add(time.Hour))
	for _, a := range actors[:2] {
		if created, err := s.ReportEvent(ctx, a, event, "cancelled"); err != nil || !created {
			t.Fatal(created, err)
		}
	}
	if _, err := s.DB.Exec(ctx, `UPDATE events_eventreport SET created_at=now()-interval '20 days' WHERE event_id=$1`, event); err != nil {
		t.Fatal(err)
	}
	if value, err := s.EventReliability(ctx, event); err != nil || value != "unverified" {
		t.Fatal("event threshold/decay ignored", value, err)
	}
	if created, err := s.ReportEvent(ctx, actors[0], event, "cancelled"); err != nil || created {
		t.Fatal("longer event dedup ignored", created, err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE events_eventreport SET created_at=now()-interval '31 days' WHERE event_id=$1`, event); err != nil {
		t.Fatal(err)
	}
	if value, err := s.EventReliability(ctx, event); err != nil || value != nil {
		t.Fatal("expired reports retained", value, err)
	}
}
