package admin

import (
	"context"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestCasePortPartnerSourceTextOnlyCreditCapAndVisibility(t *testing.T) {
	s, a := fixture(t)
	ctx := context.Background()
	place := testdb.Place(t, s.DB, "Partner source venue", "osm")
	makePartner := func(name, website string, verified, active bool) int64 {
		t.Helper()
		id, err := s.Save(ctx, a, "places.partner", 0, rawFields(map[string]any{"name": name, "kind": "library", "place_id": place, "website": website, "is_verified": verified, "is_active": active, "blurb": strings.Repeat("ș", 280)}))
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	good := makePartner("Good", "https://example.org", true, true)
	makePartner("Unverified", "", false, true)
	makePartner("Inactive", "", true, false)
	rows, err := catalog.New(s.DB).Partners(ctx, &place, false)
	if err != nil || len(rows) != 1 || rows[0]["id"] != good || rows[0]["name"] != "Good" || rows[0]["website"] != "https://example.org" {
		t.Fatal("public partner gate", rows, err)
	}
	if official, err := catalog.New(s.DB).Partners(ctx, &place, true); err != nil || len(official) != 0 {
		t.Fatal("civic partner became official business", official, err)
	}
	other := testdb.Place(t, s.DB, "Unverified steward venue", "osm")
	if _, err := s.Save(ctx, a, "places.partner", 0, rawFields(map[string]any{"name": "Hidden steward", "kind": "library", "place_id": other, "is_verified": false, "is_active": true})); err != nil {
		t.Fatal(err)
	}
	if rows, err := catalog.New(s.DB).Partners(ctx, &other, false); err != nil || len(rows) != 0 {
		t.Fatal("unverified steward surfaced for its place", rows, err)
	}
	if _, err := s.Save(ctx, a, "places.partner", good, rawFields(map[string]any{"blurb": strings.Repeat("ș", 281)})); err == nil {
		t.Fatal("281-rune promotional credit admitted")
	}
	var blurb string
	if err := s.DB.QueryRow(ctx, `SELECT blurb FROM places_partner WHERE id=$1`, good).Scan(&blurb); err != nil || blurb != strings.Repeat("ș", 280) {
		t.Fatal("failed credit update changed row", err)
	}
	for _, field := range []string{"image", "file", "logo", "banner", "featured", "boost"} {
		if _, err := s.Save(ctx, a, "places.partner", good, rawFields(map[string]any{field: "private-file"})); err == nil {
			t.Fatal("nontext/ad surface accepted", field)
		}
	}
	if _, err := s.Save(ctx, a, "places.partner", good, rawFields(map[string]any{"website": "javascript:alert(1)"})); err == nil {
		t.Fatal("unsafe partner URL admitted")
	}
	if err := s.DB.QueryRow(ctx, `SELECT website FROM places_partner WHERE id=$1`, good).Scan(&blurb); err != nil || blurb != "https://example.org" {
		t.Fatal("unsafe URL refusal changed prior safe row", err)
	}
}
