package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestCasePortPostgresClosureQuorumDedupPublicFiltering(t *testing.T) {
	s := fixture(t, true)
	ctx := context.Background()
	place := placeFixture(t, s, "Closure venue", "osm", 23.6, 46.77)
	owner := user(t, s, "case-closure-owner", "adult")
	peer := user(t, s, "case-closure-peer", "adult")
	last := user(t, s, "case-closure-last", "adult")
	unverified := user(t, s, "case-closure-unverified", "adult")
	unverified.IdentityVerified = false
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_identity_verified=false WHERE id=$1`, unverified.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReportVenue(ctx, unverified, place, true); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("unverified closure reporting", err)
	}
	if created, err := s.ReportVenue(ctx, owner, place, true); err != nil || !created {
		t.Fatal("initial closure report", err)
	}
	if created, err := s.ReportVenue(ctx, owner, place, true); err != nil || created {
		t.Fatal("duplicate closure report changed state", err)
	}
	var reports int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM places_placeclosurereport WHERE place_id=$1 AND reporter_id=$2`, place, owner.ID).Scan(&reports); err != nil || reports != 1 {
		t.Fatal("closure report not unique per window", reports, err)
	}
	if _, err := s.ReportVenue(ctx, peer, place, true); err != nil {
		t.Fatal(err)
	}
	if out := request(t, s, fmt.Sprintf("/api/places/%d/", place), nil); out.Code != 200 {
		t.Fatal("subquorum closure hid venue", out.Code)
	}
	props := casePortPlaceProperties(t, s, place)
	for key := range props {
		if stringsContainClosure(key) {
			t.Fatal("public properties exposed closure/report field", key)
		}
	}
	if _, err := s.ReportVenue(ctx, last, place, true); err != nil {
		t.Fatal(err)
	}
	if out := request(t, s, fmt.Sprintf("/api/places/%d/", place), nil); out.Code != 404 {
		t.Fatal("quorum closure remained public", out.Code)
	}
	if out := request(t, s, "/api/places/", nil); out.Code != 200 || len(jsonObject(t, out.Body.Bytes())["features"].([]any)) != 0 {
		t.Fatal("closed place retained in public discovery")
	}
	var name string
	if err := s.DB.QueryRow(ctx, `SELECT name FROM places_place WHERE id=$1`, place).Scan(&name); err != nil || name != "Closure venue" {
		t.Fatal("closure mutated canonical row", err)
	}
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM places_placeclosurereport WHERE place_id=$1`, place).Scan(&reports); err != nil || reports != 3 {
		t.Fatal("closure overlay report rows missing", err)
	}
	staff := user(t, s, "case-closure-staff", "adult")
	staff.IsStaff = true
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_staff=true WHERE id=$1`, staff.ID); err != nil {
		t.Fatal(err)
	}
	if count, err := s.ClearVenueReports(ctx, staff, place, true); err != nil || count != 3 {
		t.Fatal("staff reset count", count, err)
	}
	if out := request(t, s, fmt.Sprintf("/api/places/%d/", place), nil); out.Code != 200 {
		t.Fatal("staff reset did not restore public visibility", out.Code)
	}
	if _, err := s.ClearVenueReports(ctx, owner, place, true); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("nonstaff closure reset accepted", err)
	}
	var audits int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_auditlog WHERE event='place.closure_reports_cleared' AND actor_id=$1`, staff.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("staff reset audit missing", audits, err)
	}
	second := placeFixture(t, s, "Threshold two venue", "osm", 23.6, 46.77)
	s.Policy.ClosureReportThreshold = 2
	if out := request(t, s, fmt.Sprintf("/api/places/%d/", second), nil); out.Code != 200 {
		t.Fatal("initial source threshold2 venue not public")
	}
	for _, reporter := range []platform.Actor{owner, peer} {
		if _, err := s.ReportVenue(ctx, reporter, second, true); err != nil {
			t.Fatal(err)
		}
	}
	if out := request(t, s, fmt.Sprintf("/api/places/%d/", second), nil); out.Code != 404 {
		t.Fatal("source threshold2 venue remains public")
	}
	if count, err := s.ClearVenueReports(ctx, staff, second, true); err != nil || count != 2 {
		t.Fatal("source staff clear exact2 rows", count, err)
	}
	if out := request(t, s, fmt.Sprintf("/api/places/%d/", second), nil); out.Code != 200 {
		t.Fatal("source threshold2 reset didn't self-heal")
	}
}

func stringsContainClosure(key string) bool {
	// Literal wire fields are independently checked by semantic spelling.
	for _, part := range []string{"closure", "closed", "reporter"} {
		if strings.Contains(key, part) {
			return true
		}
	}
	return false
}

func TestCasePortPostgresClosureCrossVenueBudgetAndConfiguredDecay(t *testing.T) {
	s := fixture(t, true)
	ctx := context.Background()
	first := placeFixture(t, s, "Closure first", "osm", 23.6, 46.77)
	second := placeFixture(t, s, "Closure second", "osm", 23.6, 46.77)
	a := user(t, s, "case-closure-rate", "adult")
	s.RatePolicies = map[string]budgets.Policy{"place_closure_report": {Limit: 1, Window: time.Hour}}
	if created, err := s.ReportVenue(ctx, a, first, true); err != nil || !created {
		t.Fatal(err)
	}
	if created, err := s.ReportVenue(ctx, a, second, true); err != nil || created {
		t.Fatal("crossvenue report escaped actor budget", err)
	}
	s.Policy.ClosureReportThreshold = 1
	if out := request(t, s, fmt.Sprintf("/api/places/%d/", first), nil); out.Code != 404 {
		t.Fatal("configured threshold did not hide")
	}
	// Approved native report floors remain14days. Preserve read-time decay by
	// aging beyond that actual policy, never bypassing validation to emulate an
	// unsafe legacy one-hour environment override.
	if _, err := s.DB.Exec(ctx, `UPDATE places_placeclosurereport SET created_at=now()-interval '15 days' WHERE place_id=$1`, first); err != nil {
		t.Fatal(err)
	}
	if out := request(t, s, fmt.Sprintf("/api/places/%d/", first), nil); out.Code != 200 {
		t.Fatal("decayed closure did not self-heal")
	}
	var count int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM places_placeclosurereport WHERE place_id=$1`, first).Scan(&count); err != nil || count != 1 {
		t.Fatal("decay deleted historical evidence", err)
	}
}
