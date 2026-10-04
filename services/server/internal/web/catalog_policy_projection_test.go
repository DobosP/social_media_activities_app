package web

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestCatalogPolicyPublicProjectionDefaultsAndOpenNow(t *testing.T) {
	base := context.Background()
	if publicPlaceFieldsSQL(base) != publicPlaceFields {
		t.Fatal("default projection changed")
	}
	p := catalog.DefaultPolicy()
	p.OpenNowReportDecay = 30 * 24 * time.Hour
	p.OpenNowReportThreshold = 2
	ctx := catalog.WithPolicy(base, p)
	if query := publicPlaceFieldsSQL(ctx); !strings.Contains(query, "interval '2592000 seconds'") || strings.Contains(query, "interval '14 days'") {
		t.Fatal("projection ignored configured decay", query)
	}
	s := &Server{Catalog: catalog.New(nil)}
	for _, tc := range []struct {
		ctx  context.Context
		want any
	}{{base, true}, {ctx, "unverified"}} {
		place := map[string]any{"id": 1, "name": "Synthetic venue", "_display_name": "Synthetic venue", "opening_hours_text": "24/7", "_reports": 2}
		if err := s.publicDecoratePlaces(tc.ctx, []map[string]any{place}); err != nil || place["open_now"] != tc.want {
			t.Fatal("web warning differs from catalog policy", place["open_now"], err)
		}
	}
}

func TestPostgresCatalogPolicyWebReportsFactsAndInterest(t *testing.T) {
	s, owner, place, typ := socialLegacyFixture(t)
	p := catalog.DefaultPolicy()
	p.OpenNowReportDecay, p.ClosureReportDecay = 30*24*time.Hour, 30*24*time.Hour
	p.OpenNowReportThreshold, p.ClosureReportThreshold, p.FactQuorum = 2, 1, 4
	s.Catalog.Policy = p
	ctx := catalog.WithPolicy(context.Background(), p)
	socialPolicy := social.DefaultPolicyConfig()
	socialPolicy.InterestThreshold = 4
	socialPolicy.ArrivalWindowBeforeHours, socialPolicy.ArrivalWindowAfterHours = 0, 0
	socialPolicy.UserGroupCohorts, socialPolicy.SupportCompanionCohorts = nil, nil
	if err := s.Social.ConfigurePolicy(socialPolicy); err != nil {
		t.Fatal(err)
	}
	s.Social.AllowUserGroups = true
	actors := []platform.Actor{owner, testdb.Actor(t, s.DB, "projection-voter-one", "adult"), testdb.Actor(t, s.DB, "projection-voter-two", "adult")}
	if _, err := s.DB.Exec(ctx, `UPDATE places_place SET opening_hours_raw='24/7',opening_hours='{"mo":[[0,1440]],"tu":[[0,1440]],"we":[[0,1440]],"th":[[0,1440]],"fr":[[0,1440]],"sa":[[0,1440]],"su":[[0,1440]]}' WHERE id=$1`, place); err != nil {
		t.Fatal(err)
	}
	for _, a := range actors {
		if err := s.Catalog.VoteFact(ctx, a, place, "drinking_water", true); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range actors[:2] {
		if _, err := s.DB.Exec(ctx, `INSERT INTO places_opennowreport(place_id,reporter_id,created_at) VALUES($1,$2,now()-interval '20 days')`, place, a.ID); err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest("GET", "/places/", nil).WithContext(ctx)
	places, _, err := s.publicPlaces(r, platform.Actor{}, "", "", 10)
	if err != nil || len(places) != 1 || places[0]["open_now"] != "unverified" {
		t.Fatal("public projection lost configured warning", places, err)
	}
	flags, err := s.socialVenueFlags(ctx, place)
	if err != nil || len(flags) != 1 || flags[0] != "hours_unverified" {
		t.Fatal("activity warning differs from catalog", flags, err)
	}
	facts, err := s.publicVenueFacts(r, owner, places[0], false)
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range facts {
		if fact["key"] == "drinking_water" && (fact["state"] != "unknown" || fact["required"] != 4) {
			t.Fatal("private carve-out bypassed fact quorum", fact)
		}
	}
	activity, err := s.Social.CreateActivity(ctx, owner, social.ActivityInput{Place: place, ActivityType: typ, Title: "Synthetic policy projection", StartsAt: time.Now().Add(30 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	console, err := s.Social.OrganizerConsole(ctx, owner)
	if err != nil || len(spaRows(console["activities"])) != 1 || !spaBool(spaRows(console["activities"])[0]["venue_flag"]) {
		t.Fatal("organizer warning differs from catalog", console, err)
	}
	r.SetPathValue("pk", spaText(activity))
	r = platform.WithActor(r, owner)
	data, err := s.socialActivityDetail(r, owner)
	if err != nil || data["arrival_window_open"] != false || data["can_create_group"] != false || data["can_set_support"] != false {
		t.Fatal("activity affordances ignored policy", data, err)
	}
	gauge, err := s.Social.ProposeGauge(ctx, owner, social.GaugeInput{Place: place, ActivityType: typ, CoarseWindow: "weekday_evening"})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range actors[1:] {
		if err := s.Social.MarkGauge(ctx, a, gauge, true); err != nil {
			t.Fatal(err)
		}
	}
	gauges, err := s.socialGaugeRows(r, owner, gauge)
	if err != nil || len(gauges) != 1 || spaBool(gauges[0]["ready"]) || spaInt(gauges[0]["remaining"]) != 1 {
		t.Fatal("web gauge differs from configured threshold", gauges, err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO places_placeclosurereport(place_id,reporter_id,created_at) VALUES($1,$2,now()-interval '20 days')`, place, owner.ID); err != nil {
		t.Fatal(err)
	}
	flags, err = s.socialVenueFlags(ctx, place)
	if err != nil || len(flags) != 1 || flags[0] != "closed" {
		t.Fatal("closure warning lost threshold/decay", flags, err)
	}
}
