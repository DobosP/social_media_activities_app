package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestCasePortVenueClaimFormOfficialBadgeAndCivicExclusion(t *testing.T) {
	s, owner, place, _, mux := webCasePortFixture(t)
	ctx := context.Background()
	path := fmt.Sprintf("/places/%d/", place)
	form := path + "claim/"
	body := webCasePortHTML(t, mux, owner, path)
	webCasePortContains(t, body, "Is this your venue? Claim it")
	webCasePortAbsent(t, body, "Official venue page")
	webCasePortHTML(t, mux, owner, form)
	w := webCasePort2Post(t, mux, owner, form, form, url.Values{"org_name": {"SC Sala SRL"}, "kind": {"business"}, "official_website": {"https://sala.example/"}})
	if w.Code != 302 || w.Header().Get("Location") != path {
		t.Fatal("claim redirect", w.Code, w.Header().Get("Location"))
	}
	var claim int64
	if err := s.DB.QueryRow(ctx, `SELECT id FROM places_placeclaim WHERE place_id=$1 AND claimant_id=$2 AND status='pending'`, place, owner.ID).Scan(&claim); err != nil {
		t.Fatal("claim form did not persist", err)
	}
	staff := testdb.Actor(t, s.DB, "venue-claim-case-staff", "adult")
	staff.IsStaff = true
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_staff=true WHERE id=$1`, staff.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Catalog.DecideClaim(ctx, staff, claim, true, ""); err != nil {
		t.Fatal(err)
	}
	body = webCasePortHTML(t, mux, owner, path)
	webCasePortContains(t, body, "Official venue page")
	webCasePortAbsent(t, body, "Is this your venue? Claim it")
	if _, err := s.DB.Exec(ctx, `UPDATE places_partner SET kind='library' WHERE place_id=$1`, place); err != nil {
		t.Fatal(err)
	}
	body = webCasePortHTML(t, mux, owner, path)
	webCasePortAbsent(t, body, "Official venue page")
	webCasePortContains(t, body, "Is this your venue? Claim it")
}

func TestCasePortAccessNeedsStableSoftSortAndHearingForm(t *testing.T) {
	s, actor, _, _, mux := webCasePortFixture(t)
	ctx := context.Background()
	testdb.Place(t, s.DB, "AAA Unknown hall", "osm")
	mismatch := testdb.Place(t, s.DB, "BBB Mismatch hall", "osm")
	match := testdb.Place(t, s.DB, "ZZZ Step-free hall", "osm")
	if _, err := s.DB.Exec(ctx, `UPDATE places_place SET raw_tags=CASE WHEN id=$1 THEN '{"wheelchair":"yes"}'::jsonb WHEN id=$2 THEN '{"wheelchair":"no"}'::jsonb ELSE '{}'::jsonb END`, match, mismatch); err != nil {
		t.Fatal(err)
	}
	for _, pref := range []catalog.AccessPreference{{}, {Quiet: true}, {StepFree: true}} {
		if err := s.Catalog.SetAccess(ctx, actor, pref); err != nil {
			t.Fatal(err)
		}
		body := webCasePortHTML(t, mux, actor, "/places/list/")
		webCasePortContains(t, body, "AAA Unknown hall", "BBB Mismatch hall", "ZZZ Step-free hall")
		if strings.Index(body, "AAA Unknown hall") > strings.Index(body, "BBB Mismatch hall") {
			t.Fatal("stable nonmatch order lost")
		}
		if pref.StepFree {
			webCasePortContains(t, body, "Matches your access needs")
			if strings.Index(body, "ZZZ Step-free hall") > strings.Index(body, "AAA Unknown hall") {
				t.Fatal("confirmed match not floated")
			}
		} else {
			webCasePortAbsent(t, body, "Matches your access needs")
			if strings.Index(body, "ZZZ Step-free hall") < strings.Index(body, "AAA Unknown hall") {
				t.Fatal("quiet/no stated needs reordered")
			}
		}
	}
	webCasePortAbsent(t, webCasePortHTML(t, mux, platform.Actor{}, "/places/list/"), "Matches your access needs")
	api := http.NewServeMux()
	s.Discovery.Register(api)
	r := webCasePortRead(api, actor, "/api/discovery/near-me/")
	var rows []map[string]any
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &rows) != nil {
		t.Fatal("near-me source route", r.Code)
	}
	positions := map[string]int{}
	for i, row := range rows {
		positions[spaText(row["name"])] = i
		if _, exists := row["raw_tags"]; exists {
			t.Fatal("near-me raw tags leaked")
		}
	}
	for _, name := range []string{"AAA Unknown hall", "BBB Mismatch hall", "ZZZ Step-free hall"} {
		if _, ok := positions[name]; !ok {
			t.Fatal("soft sort hid venue", name)
		}
	}
	if positions["ZZZ Step-free hall"] > positions["AAA Unknown hall"] || positions["AAA Unknown hall"] > positions["BBB Mismatch hall"] {
		t.Fatal("near-me stable soft sort", positions)
	}
	w := webCasePort2Post(t, mux, actor, "/access/", "/access/", url.Values{"needs_hearing_loop": {"on"}})
	if w.Code != 302 {
		t.Fatal("access form redirect", w.Code)
	}
	pref, err := s.Catalog.Access(ctx, actor)
	if err != nil || pref == nil || !pref.HearingLoop || pref.StepFree || pref.Toilet {
		t.Fatal("hearing loop save replaced full preference", pref, err)
	}
}

func TestCasePortPlaceDetailPlainBriefIsRenderedAndUsesLoadedFacts(t *testing.T) {
	s, actor, place, _, mux := webCasePortFixture(t)
	ctx := context.Background()
	if _, err := s.DB.Exec(ctx, `UPDATE places_place SET name='Read Aloud Hall',raw_tags='{"wheelchair":"yes","toilets:wheelchair":"no"}' WHERE id=$1`, place); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/places/%d/", place)
	body := webCasePortHTML(t, mux, platform.Actor{}, path)
	webCasePortContains(t, body, `aria-labelledby="place-brief-heading"`, "At a glance", "Step-free access", "Read Aloud Hall")
	r := socialLegacyRequest("GET", path, actor, place, nil)
	data, _, handled, err := s.PublicView(r, actor, "place_detail")
	if err != nil || !handled {
		t.Fatal("brief native context", err)
	}
	rows, ok := data["place_brief"].([][]any)
	if !ok {
		t.Fatal("brief missing ordered labels")
	}
	byLabel := map[string]any{}
	for _, row := range rows {
		byLabel[row[0].(string)] = row[1]
	}
	if byLabel["Place"] != "Read Aloud Hall" || byLabel["Step-free access"] != "yes" || byLabel["Accessible toilet"] != "no" || byLabel["Baby changing table"] != "not recorded" || byLabel["Drinking water"] != "not recorded" {
		t.Fatal("rendered detail not source brief", byLabel)
	}
}
