package catalog_test

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

func venueAPIStore(t *testing.T) *catalog.Service {
	t.Helper()
	dsn := flag.Lookup("catalog-test-dsn")
	if dsn == nil {
		t.Fatal("catalog fixture flag missing")
	}
	db := testdb.New(t, dsn.Value.String(), func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	return catalog.New(db)
}

func venueAPIRead(t *testing.T, s *catalog.Service, path string) map[string]any {
	t.Helper()
	mux := http.NewServeMux()
	s.Register(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "https://fixture.local"+path, nil))
	var out map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatal("public place API", path, w.Code)
	}
	return out
}

func venueAPIProperties(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	var out []map[string]any
	if body["type"] != "FeatureCollection" {
		t.Fatal("collection type", body["type"])
	}
	for _, f := range body["features"].([]any) {
		feature := f.(map[string]any)
		geometry := feature["geometry"].(map[string]any)
		if feature["type"] != "Feature" || geometry["type"] != "Point" || len(geometry["coordinates"].([]any)) != 2 {
			t.Fatal("GeoJSON feature shape")
		}
		out = append(out, feature["properties"].(map[string]any))
	}
	return out
}

func TestCasePortPlacePaginationActualAnonymousCapAndInertLimit(t *testing.T) {
	for _, c := range []struct {
		name, path string
		n, want    int
		next       bool
	}{
		{"cap", "/api/places/?page_size=100000", 505, 500, true},
		{"inert_limit", "/api/places/?limit=100000", 120, 50, true},
		{"anonymous", "/api/places/", 1, 1, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := venueAPIStore(t)
			for i := 0; i < c.n; i++ {
				testdb.Place(t, s.DB, fmt.Sprintf("Pagination %d", i), "osm")
			}
			body := venueAPIRead(t, s, c.path)
			features := venueAPIProperties(t, body)
			if len(features) != c.want || body["count"] != float64(c.n) || (body["next"] != nil) != c.next {
				t.Fatal("source page cap/count/continuation", len(features), body["count"], body["next"])
			}
		})
	}
}

func TestCasePortPlaceGeoJSONTaxonomyFiltersAndSourceCredit(t *testing.T) {
	s := venueAPIStore(t)
	ctx := context.Background()
	court := testdb.Place(t, s.DB, "Court", "osm")
	library := testdb.Place(t, s.DB, "Library", "osm")
	var basketball, reading int64
	if err := s.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&basketball); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='reading'`).Scan(&reading); err != nil {
		t.Fatal(err)
	}
	for _, edge := range []struct {
		place, typ int64
		disputed   bool
	}{{court, basketball, false}, {court, reading, true}, {library, reading, false}} {
		if _, err := s.DB.Exec(ctx, `INSERT INTO places_placeactivity(place_id,activity_id,origin,confidence,source,mapping_rule,is_disputed,created_at,updated_at) VALUES($1,$2,'manual',0.95,'osm','', $3,now(),now())`, edge.place, edge.typ, edge.disputed); err != nil {
			t.Fatal(err)
		}
	}
	props := venueAPIProperties(t, venueAPIRead(t, s, "/api/places/?activity=basketball"))
	if len(props) != 1 || props[0]["name"] != "Court" || props[0]["image_thumb"] != nil || props[0]["activities"].([]any)[0].(map[string]any)["slug"] != "basketball" {
		t.Fatal("activity/empty-cover filter", props)
	}
	props = venueAPIProperties(t, venueAPIRead(t, s, "/api/places/?category=sport"))
	if len(props) != 1 || props[0]["name"] != "Court" || !reflect.DeepEqual(props[0]["categories"], []any{"sport"}) || !reflect.DeepEqual(props[0]["category_labels"], []any{"Sport"}) {
		t.Fatal("top category/disputed edge", props)
	}
	props = venueAPIProperties(t, venueAPIRead(t, s, "/api/places/?category=reading"))
	if len(props) != 1 || props[0]["name"] != "Library" {
		t.Fatal("reading category filter", props)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE places_place SET source='roedu',attribution='RO-EDU',license_name='CC BY 4.0',provenance_url='https://data.example/venues/theatre-1',external_id='theatre-1' WHERE id=$1`, library); err != nil {
		t.Fatal(err)
	}
	props = venueAPIProperties(t, venueAPIRead(t, s, "/api/places/?activity=reading"))
	want := map[string]any{"attribution": "RO-EDU", "license_name": "CC BY 4.0", "provenance_url": "https://data.example/venues/theatre-1"}
	if len(props) != 1 || !reflect.DeepEqual(props[0]["attribution_credit"], want) {
		t.Fatal("attribution credit", props)
	}
}

func TestCasePortPlaceNearestFirstHasDistances(t *testing.T) {
	s := venueAPIStore(t)
	ctx := context.Background()
	near := testdb.Place(t, s.DB, "Near", "osm")
	far := testdb.Place(t, s.DB, "Far", "osm")
	if _, err := s.DB.Exec(ctx, `UPDATE places_place SET location=ST_SetSRID(ST_MakePoint(CASE WHEN id=$1 THEN 23.59 ELSE 23.70 END,CASE WHEN id=$1 THEN 46.77 ELSE 46.85 END),4326) WHERE id IN($1,$2)`, near, far); err != nil {
		t.Fatal(err)
	}
	props := venueAPIProperties(t, venueAPIRead(t, s, "/api/places/?near_lon=23.5899&near_lat=46.7712"))
	if len(props) != 2 || props[0]["name"] != "Near" || props[1]["name"] != "Far" || props[0]["distance_m"] == nil || props[0]["distance_m"].(float64) >= props[1]["distance_m"].(float64) {
		t.Fatal("nearest-first distance", props)
	}
}

func TestCasePortPlaceUpcomingIncludesPublicMeetupAndLiveEvent(t *testing.T) {
	s := venueAPIStore(t)
	ctx := context.Background()
	court := testdb.Place(t, s.DB, "Court", "osm")
	hall := testdb.Place(t, s.DB, "Hall", "osm")
	quiet := testdb.Place(t, s.DB, "Quiet", "osm")
	owner := testdb.Actor(t, s.DB, "map-source-owner", "adult")
	var typ int64
	if err := s.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	soc := social.New(s.DB, platform.RecordAudit)
	activity, err := soc.CreateActivity(ctx, owner, social.ActivityInput{Place: court, ActivityType: typ, Title: "Public pickup", StartsAt: time.Now().Add(24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE social_activity SET is_publicly_listed=true WHERE id=$1`, activity); err != nil {
		t.Fatal(err)
	}
	for _, e := range []struct {
		place         int64
		status, title string
	}{{hall, "scheduled", "Venue calendar"}, {quiet, "cancelled", "Cancelled venue calendar"}} {
		if _, err := s.DB.Exec(ctx, `INSERT INTO events_event(title,description,starts_at,url,source,external_id,attribution,license_name,provenance_url,created_at,updated_at,place_id,source_category,is_import_held,lifecycle_status,is_tombstone,source_venue_id,source_city,source_pack_id,source_snapshot_id,source_release_id,source_recurrence,source_timezone,source_currency,source_availability) VALUES($1,'',now()+interval '2 days','','manual','','','','',now(),now(),$2,'',false,$3,false,'','','','','','','','','')`, e.title, e.place, e.status); err != nil {
			t.Fatal(err)
		}
	}
	props := venueAPIProperties(t, venueAPIRead(t, s, "/api/places/"))
	byName := map[string]bool{}
	for _, p := range props {
		byName[p["name"].(string)] = p["has_upcoming"].(bool)
	}
	if !reflect.DeepEqual(byName, map[string]bool{"Court": true, "Hall": true, "Quiet": false}) {
		t.Fatal("upcoming source visibility", byName)
	}
	props = venueAPIProperties(t, venueAPIRead(t, s, "/api/places/?has_upcoming=true"))
	names := map[string]bool{}
	for _, p := range props {
		names[p["name"].(string)] = true
	}
	if !reflect.DeepEqual(names, map[string]bool{"Court": true, "Hall": true}) {
		t.Fatal("upcoming filter", names)
	}
}

func TestCasePortPlaceCoverThumbAndConstantMetadataQueryBudget(t *testing.T) {
	s := venueAPIStore(t)
	ctx := context.Background()
	ms := media.NewService(s.DB, nil, nil, media.TokenCodec{Key: []byte("synthetic-native-place-cover-key32")}, social.New(s.DB, platform.RecordAudit))
	s.PlaceVisuals = ms.PlaceVisuals
	place := testdb.Place(t, s.DB, "Covered Library", "osm")
	if _, err := s.DB.Exec(ctx, `INSERT INTO places_placecover(place_id,source,storage_key,content_type,byte_size,width,height,attribution,license_name,source_page_url,alt_text,created_at,updated_at,exif_stripped,sha256) VALUES($1,'wikimedia','place-covers/covered.jpg','image/jpeg',1,1,1,'Credit','CC-BY','https://commons.wikimedia.org/wiki/File:Covered.jpg','Library',now(),now(),true,'')`, place); err != nil {
		t.Fatal(err)
	}
	pool, trace := testdb.TracedPool(t, s.DB)
	s.DB = pool
	current := 1
	for _, n := range []int{4, 28} {
		for i := current; i < n; i++ {
			testdb.Place(t, s.DB, fmt.Sprintf("Query venue %d %d", n, i), "osm")
		}
		current = n
		trace.Reset()
		props := venueAPIProperties(t, venueAPIRead(t, s, "/api/places/?page_size=500"))
		queries := trace.Count()
		if len(props) != n {
			t.Fatal("query budget did not exercise populated fixture", n, len(props))
		}
		if queries > 4 || queries < 2 {
			t.Fatal("place query budget", n, queries)
		}
		found := false
		for _, p := range props {
			if p["name"] == "Covered Library" {
				found = true
				url, ok := p["image_thumb"].(string)
				if !ok || !strings.HasPrefix(url, "/api/media/place-cover-file/") {
					t.Fatal("actual stored cover thumb", p["image_thumb"])
				}
			} else if p["image_thumb"] != nil {
				t.Fatal("generated/no-cover visual became photo")
			}
		}
		if !found {
			t.Fatal("covered place missing")
		}
	}
}
