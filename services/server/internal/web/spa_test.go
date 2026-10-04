package web

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

// The independent fixtures execute the actual source Django builders in the
// offline reference image; no Python runtime is needed for this Go test.
func TestDjangoTwentySPAContractGoldens(t *testing.T) {
	raw, err := os.ReadFile("testdata/django_spa_empty.json")
	if err != nil {
		t.Fatal(err)
	}
	var goldens map[string]map[string]any
	if err := json.Unmarshal(raw, &goldens); err != nil {
		t.Fatal(err)
	}
	if len(goldens) != 20 {
		t.Fatal("source screen inventory changed")
	}
	s := &Server{Renderer: NewRenderer("../../../..")}
	actor := platform.Actor{ID: 1, DisplayName: "Fixture", Username: "fixture", AgeBand: "adult", Cohort: "adult", IdentityVerified: true, IsActive: true}
	r := httptest.NewRequest("GET", "/fixture/", nil)
	page := map[string]any{"number": 1, "paginator": map[string]any{"count": 0, "num_pages": 1}, "object_list": []any{}}
	for name, expected := range goldens {
		t.Run(name, func(t *testing.T) {
			legacy := pongo2.Context{"csrf_token": "synthetic-csrf", "structured_data": "", "breadcrumb_data": "", "area": map[string]any{"name": "Cluj-Napoca", "slug": "cluj-napoca"}, "activity_type": map[string]any{"name": "Basketball", "slug": "basketball"}, "community": map[string]any{"name": "Cluj Basketball", "area": map[string]any{"name": "Cluj-Napoca"}, "activity_type": map[string]any{"name": "Basketball"}}, "can_participate": true, "languages": [][]string{{"en", "English"}, {"ro", "Română"}}, "current_language": "en", "page_obj": page, "conn_page": page, "page": page, "groups_page": page}
			if name == "events" {
				legacy["area"] = ""
			}
			if name == "home" || name == "organize" {
				legacy["activities"] = []any{}
			}
			payload, title, public, seo, err := s.BuildSPA(context.Background(), r, actor, expected["route"].(string), legacy)
			if err != nil {
				t.Fatal(err)
			}
			delete(seo, "snapshot_template")
			wantSEO := spaMap(expected["seo"])
			for _, key := range []string{"structured_data", "breadcrumb_data"} {
				if wantSEO[key] == nil {
					delete(seo, key)
				}
			}
			got := map[string]any{"route": payload["route"], "title": title, "data": payload["data"], "public": public, "seo": seo}
			gotRaw, _ := json.Marshal(got)
			var normalized map[string]any
			_ = json.Unmarshal(gotRaw, &normalized)
			if !reflect.DeepEqual(normalized, expected) {
				t.Fatalf("Django mismatch\ngot: %s\nwant: %s", gotRaw, mustJSON(expected))
			}
			if public && payload["csrf"] != "" {
				t.Fatal("public payload exposed token")
			}
		})
	}
}
func mustJSON(value any) string { raw, _ := json.Marshal(value); return string(raw) }
func TestSPAPrivateRequiresCSRFAndSafeURL(t *testing.T) {
	s := &Server{Renderer: NewRenderer("../../../..")}
	r := httptest.NewRequest("GET", "/you/", nil)
	if _, _, _, _, err := s.BuildSPA(context.Background(), r, platform.Actor{}, "you", pongo2.Context{}); err == nil {
		t.Fatal("private payload without CSRF")
	}
	for _, unsafe := range []string{"javascript:alert(1)", "data:text/html,x", "//evil.invalid"} {
		if spaSafeHref(unsafe) != "" {
			t.Fatal("unsafe notification URL")
		}
	}
}

func TestDjangoPopulatedSPAActivityAndEventCards(t *testing.T) {
	s := &Server{Renderer: NewRenderer("../../../..")}
	r := httptest.NewRequest("GET", "/fixture/", nil)
	words := []string{}
	for i := 0; i < 26; i++ {
		words = append(words, "word"+fmt.Sprint(i))
	}
	place := map[string]any{"pk": 41, "name": "Raw park", "display_name": "Corrected park", "address_city": "Cluj-Napoca"}
	typ := map[string]any{"name": "Basketball", "slug": "basketball"}
	model := map[string]any{"pk": 7, "title": "Friendly basketball", "description": strings.Join(words, " "), "activity_type": typ, "place": place, "starts_at": "2026-10-04T14:30:00Z", "status": "open", "cost_band": "free", "difficulty": "easy", "beginners_welcome": true, "secondary_types": []map[string]any{{"name": "Walking"}}, "visual": map[string]any{"kind": "generated_accent"}}
	got, err := s.spaCardsFromModels(r, []map[string]any{model})
	if err != nil {
		t.Fatal(err)
	}
	got[0]["visual"].(map[string]any)["svg"] = "ACCENT_ORACLE"
	event := map[string]any{"pk": 5, "title": "Event Romanian șță", "description": "Two ordinary words", "activity_type": typ, "place": place, "starts_at": "2026-10-04T14:30:00Z"}
	events, err := s.spaEventRows(context.Background(), r, []map[string]any{event})
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	if err := json.Unmarshal([]byte(`{"activity":{"pk":7,"url":"/activities/7/","title":"Friendly basketball","visual":{"kind":"accent","svg":"ACCENT_ORACLE"},"tags":["Basketball","Walking","Free","Easy","beginners welcome"],"meta":"Sun 4 Oct, 14:30 · Corrected park, Cluj-Napoca","description":"word0 word1 word2 word3 word4 word5 word6 word7 word8 word9 word10 word11 word12 word13 word14 word15 word16 word17 word18 word19 word20 word21 …","score":null},"event":{"pk":5,"url":"/events/5/event-romanian-sta/","title":"Event Romanian șță","type":"Basketball","when":"Sun 4 Oct, 14:30","place":{"name":"Raw park","url":"/places/41/corrected-park/"},"description":"Two ordinary words"}}`), &want); err != nil {
		t.Fatal(err)
	}
	var normalized map[string]any
	_ = json.Unmarshal([]byte(mustJSON(map[string]any{"activity": got[0], "event": events[0]})), &normalized)
	if !reflect.DeepEqual(normalized, want) {
		t.Fatalf("Populated Django source mismatch\ngot=%s\nwant=%s", mustJSON(normalized), mustJSON(want))
	}
}

func TestPostgresSPAHydrationNeverWidensReadGate(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t, *webDomainDSN, func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	viewer := testdb.Actor(t, db, "spa-viewer", "adult")
	owner := testdb.Actor(t, db, "spa-owner", "adult")
	minor := testdb.Actor(t, db, "spa-child", "child")
	place := testdb.Place(t, db, "SPA public place", "osm")
	var typ int64
	if err := db.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	soc := social.New(db, platform.RecordAudit)
	soc.ActivityVisuals = func(ctx context.Context, q platform.Querier, a platform.Actor, ids []int64) (map[int64]any, error) {
		out := map[int64]any{}
		for _, id := range ids {
			out[id] = map[string]any{"kind": "generated_accent"}
		}
		return out, nil
	}
	ids := []int64{}
	for _, a := range []platform.Actor{owner, minor} {
		id, err := soc.CreateActivity(ctx, a, social.ActivityInput{Place: place, ActivityType: typ, Title: "Native SPA", StartsAt: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	s := &Server{DB: db, Social: soc, Renderer: NewRenderer("../../../..")}
	models, err := s.HydrateActivities(ctx, viewer, []map[string]any{{"id": ids[0]}, {"id": ids[1]}})
	if err != nil || len(models) != 1 {
		t.Fatal("SPA widened cohort", models, err)
	}
	if spaMap(models[0]["activity_type_obj"])["name"] != "Basketball" || spaMap(models[0]["place_obj"])["display_name"] != "SPA public place" {
		t.Fatal("model adapter lost source display fields", models)
	}
	if _, err := db.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, owner.ID, viewer.ID); err != nil {
		t.Fatal(err)
	}
	models, err = s.HydrateActivities(ctx, viewer, []map[string]any{{"id": ids[0]}})
	if err != nil || len(models) != 0 {
		t.Fatal("SPA widened block gate", models, err)
	}
}
