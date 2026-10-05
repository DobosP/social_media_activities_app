package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestCasePortAccessibilityHonestStatesAndSoftNeeds(t *testing.T) {
	facts := AccessibilityFacts(map[string]any{"wheelchair": "yes", "toilets:wheelchair": "no", "changing_table": "yes", "hearing_loop": "yes", "automatic_door": "no"})
	if !reflect.DeepEqual(facts, map[string]string{"step_free": "true", "accessible_toilet": "false", "changing_table": "true", "tactile_paving": "unknown", "hearing_loop": "true", "automatic_door": "false"}) {
		t.Fatal("OSM accessibility projection")
	}
	for _, cell := range []struct {
		tags       map[string]any
		key, state string
	}{
		{map[string]any{"wheelchair": "limited"}, "step_free", "limited"},
		{map[string]any{"wheelchair": "designated"}, "step_free", "unknown"},
		{map[string]any{"automatic_door": "button"}, "automatic_door", "unknown"},
		{map[string]any{"automatic_door": true}, "automatic_door", "unknown"},
	} {
		if AccessibilityFacts(cell.tags)[cell.key] != cell.state {
			t.Fatal("descriptive/nonbinary fact became truth", cell.key)
		}
	}
	for _, tags := range []map[string]any{{}, {"google": map[string]any{"place_id": "synthetic"}}} {
		for _, state := range AccessibilityFacts(tags) {
			if state != "unknown" {
				t.Fatal("enrichment-only data claimed accessibility")
			}
		}
	}
	for _, key := range []string{"step_free", "accessible_toilet", "hearing_loop"} {
		pref := AccessPreference{StepFree: key == "step_free", Toilet: key == "accessible_toilet", HearingLoop: key == "hearing_loop"}
		for state, want := range map[string]string{"true": "match", "false": "mismatch", "unknown": "unknown", "limited": "unknown"} {
			if MatchesAccess(map[string]string{key: state}, &pref) != want {
				t.Fatal("soft classifier", key, state)
			}
		}
	}
	if MatchesAccess(facts, nil) != "unknown" || MatchesAccess(facts, &AccessPreference{Quiet: true}) != "unknown" || MatchesAccess(facts, &AccessPreference{}) != "unknown" {
		t.Fatal("unstated/quiet preference fabricated a match")
	}
}

func TestCasePortPostgresAccessPreferenceReplacesOneOwnedRow(t *testing.T) {
	s := fixture(t, true)
	ctx := context.Background()
	a := user(t, s, "case-access-owner", "adult")
	if pref, err := s.Access(ctx, a); err != nil || pref != nil {
		t.Fatal("unset preference must be absent", err)
	}
	first := AccessPreference{StepFree: true, Quiet: true, HearingLoop: true}
	if err := s.SetAccess(ctx, a, first); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Access(ctx, a); err != nil || got == nil || *got != first {
		t.Fatal("preference fields lost", err)
	}
	if err := s.SetAccess(ctx, a, AccessPreference{}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Access(ctx, a); err != nil || got == nil || *got != (AccessPreference{}) {
		t.Fatal("replacement did not clear old needs", err)
	}
	var count int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM places_accesspreference WHERE user_id=$1`, a.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("preference duplicated owned row", count, err)
	}
}

func TestCasePortOSMVenueFactLiteralSourceMatrix(t *testing.T) {
	for _, cell := range []struct {
		tags      map[string]any
		key, want string
	}{
		{map[string]any{"drinking_water": "yes"}, "drinking_water", "true"},
		{map[string]any{"toilets": "no"}, "toilets", "false"},
		{map[string]any{"lit": "yes"}, "lit_at_night", "true"},
		{map[string]any{"leisure": "playground"}, "playground", "true"},
		{map[string]any{"barrier": "fence"}, "fenced", "true"},
		{map[string]any{"natural": "tree"}, "shade", "true"},
		{map[string]any{}, "playground", "unknown"},
		{map[string]any{"building": "yes"}, "indoor_shelter", "unknown"},
		{map[string]any{"bicycle_parking": "yes"}, "bike_parking", "true"},
		{map[string]any{"parking": "yes"}, "car_parking", "true"},
		{map[string]any{"parking": "no"}, "car_parking", "false"},
		{map[string]any{"bicycle_parking": "stands"}, "bike_parking", "unknown"},
		{map[string]any{"amenity": "parking"}, "car_parking", "unknown"},
		{map[string]any{}, "bike_parking", "unknown"},
		{map[string]any{"public_transport": "platform"}, "bus_tram_nearby", "unknown"},
	} {
		if osmFact(cell.tags, cell.key) != cell.want {
			t.Fatal("OSM literal source contract", cell.key, cell.want)
		}
	}
}

func TestCasePortPostgresCrowdVenueFactsQuorumPrivacyAndGates(t *testing.T) {
	s := fixture(t, true)
	ctx := context.Background()
	place := placeFixture(t, s, "Crowd facts", "osm", 23.6, 46.77)
	if _, err := s.DB.Exec(ctx, `UPDATE places_place SET raw_tags='{}' WHERE id=$1`, place); err != nil {
		t.Fatal(err)
	}
	voters := []platform.Actor{}
	for i := 0; i < 6; i++ {
		voters = append(voters, user(t, s, fmt.Sprintf("case-fact-voter%d", i), "adult"))
	}
	fact := func(key string, detail bool, actor platform.Actor) map[string]any {
		t.Helper()
		rows, err := s.VenueFacts(ctx, actor, place, detail)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 10 {
			t.Fatal("venue facts lost exact key set")
		}
		keys := map[string]bool{}
		for _, row := range rows {
			keys[row["key"].(string)] = true
			if !detail && len(row) != 3 {
				t.Fatal("list fact exposed counts or identities")
			}
		}
		for _, expected := range []string{"drinking_water", "toilets", "lit_at_night", "indoor_shelter", "fenced", "shade", "playground", "bike_parking", "car_parking", "bus_tram_nearby"} {
			if !keys[expected] {
				t.Fatal("missing source fact", expected)
			}
		}
		for _, row := range rows {
			if row["key"] == key {
				return row
			}
		}
		t.Fatal("missing requested fact")
		return nil
	}
	for i := 0; i < 2; i++ {
		if err := s.VoteFact(ctx, voters[i], place, "indoor_shelter", true); err != nil {
			t.Fatal(err)
		}
	}
	if fact("indoor_shelter", false, voters[0])["state"] != "unknown" {
		t.Fatal("subquorum fact became truth")
	}
	if err := s.VoteFact(ctx, voters[2], place, "indoor_shelter", true); err != nil {
		t.Fatal(err)
	}
	if fact("indoor_shelter", false, voters[0])["state"] != "true" {
		t.Fatal("yes quorum missing")
	}
	for i := 3; i < 6; i++ {
		if err := s.VoteFact(ctx, voters[i], place, "indoor_shelter", false); err != nil {
			t.Fatal(err)
		}
	}
	if fact("indoor_shelter", false, voters[0])["state"] != "unknown" {
		t.Fatal("tied quorum did not remain unknown")
	}
	for i := 0; i < 3; i++ {
		if err := s.VoteFact(ctx, voters[i], place, "toilets", false); err != nil {
			t.Fatal(err)
		}
	}
	if fact("toilets", false, voters[0])["state"] != "false" {
		t.Fatal("no quorum missing")
	}
	var untouched []byte
	if err := s.DB.QueryRow(ctx, `SELECT raw_tags FROM places_place WHERE id=$1`, place).Scan(&untouched); err != nil || string(untouched) != "{}" {
		t.Fatal("crowd votes rewrote empty canonical rawtags", err)
	}
	for i := 0; i < 3; i++ {
		if err := s.VoteFact(ctx, voters[i], place, "bus_tram_nearby", true); err != nil {
			t.Fatal(err)
		}
	}
	if row := fact("bus_tram_nearby", true, voters[0]); row["state"] != "true" || row["osm_sourced"] != false {
		t.Fatal("transit crowd-only source")
	}
	for i := 0; i < 3; i++ {
		if err := s.VoteFact(ctx, voters[i], place, "drinking_water", true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB.Exec(ctx, `UPDATE places_place SET raw_tags='{"drinking_water":"no","bicycle_parking":"yes"}' WHERE id=$1`, place); err != nil {
		t.Fatal(err)
	}
	if row := fact("drinking_water", true, voters[0]); row["state"] != "false" || row["osm_sourced"] != true {
		t.Fatal("crowd overrode OSM truth")
	}
	if fact("bike_parking", true, voters[0])["osm_sourced"] != true {
		t.Fatal("parking source provenance lost")
	}
	if err := s.VoteFact(ctx, voters[0], place, "shade", true); err != nil {
		t.Fatal(err)
	}
	if err := s.VoteFact(ctx, voters[0], place, "shade", true); err != nil {
		t.Fatal(err)
	}
	if err := s.VoteFact(ctx, voters[0], place, "shade", false); err != nil {
		t.Fatal(err)
	}
	if err := s.VoteFact(ctx, voters[1], place, "shade", true); err != nil {
		t.Fatal(err)
	}
	row := fact("shade", true, voters[0])
	mine, ok := row["my_vote"].(*bool)
	if row["yes"] != 1 || row["no"] != 1 || !ok || mine == nil || *mine {
		t.Fatal("summary lost own changed vote or unique counts")
	}
	positive := fact("shade", true, voters[1])
	positiveVote, ok := positive["my_vote"].(*bool)
	if positive["yes"] != 1 || positive["no"] != 1 || !ok || positiveVote == nil || !*positiveVote {
		t.Fatal("source positive own-vote summary missing")
	}
	for _, key := range []string{"voters", "users", "proposer", "user_id", "username", "display_name"} {
		if _, exists := row[key]; exists {
			t.Fatal("fact summary exposed identity", key)
		}
	}
	var count int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM places_placefactvote WHERE place_id=$1 AND user_id=$2 AND fact_key='shade'`, place, voters[0].ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("mind-change duplicated vote", count, err)
	}
	var raw []byte
	if err := s.DB.QueryRow(ctx, `SELECT raw_tags FROM places_place WHERE id=$1`, place).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var tags map[string]string
	if json.Unmarshal(raw, &tags) != nil || len(tags) != 2 || tags["drinking_water"] != "no" || tags["bicycle_parking"] != "yes" {
		t.Fatal("votes wrote back to producer raw tags")
	}
	if _, err := s.DB.Exec(ctx, `UPDATE places_place SET raw_tags='{"toilets":"yes","bicycle_parking":"yes"}' WHERE id=$1`, place); err != nil {
		t.Fatal(err)
	}
	if got := fact("toilets", false, platform.Actor{}); got["state"] != "true" {
		t.Fatal("OSM toilets state missing from full source fact list")
	}
	if got := fact("toilets", true, platform.Actor{}); got["osm_sourced"] != true {
		t.Fatal("source detail toilet provenance missing")
	}
	if got := fact("indoor_shelter", true, platform.Actor{}); got["osm_sourced"] != false {
		t.Fatal("crowd shelter falsely OSM sourced")
	}
	if err := s.VoteFact(ctx, voters[0], place, "not_a_real_fact", true); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("unknown fact accepted", err)
	}
	private := placeFixture(t, s, "Private proposal", "user", 23.6, 46.77)
	if err := s.VoteFact(ctx, voters[0], private, "toilets", true); err == nil {
		t.Fatal("nonpublic vote accepted")
	}
	unverified := user(t, s, "case-fact-unverified", "adult")
	unverified.IdentityVerified = false
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_identity_verified=false WHERE id=$1`, unverified.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.VoteFact(ctx, unverified, place, "toilets", true); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("unverified vote accepted", err)
	}
	s.RatePolicies = map[string]budgets.Policy{"place_fact_vote": {Limit: 1, Window: time.Hour}}
	rateActor := user(t, s, "case-fact-rate", "adult")
	if err := s.VoteFact(ctx, rateActor, place, "toilets", true); err != nil {
		t.Fatal(err)
	}
	if err := s.VoteFact(ctx, rateActor, place, "shade", true); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("fact kinds bypassed shared admission", err)
	}
}
