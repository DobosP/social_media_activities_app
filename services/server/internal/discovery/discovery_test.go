package discovery

import (
	"context"
	"flag"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/recommendations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

var discoveryDSN = flag.String("discovery-test-dsn", "", "Explicit disposable PostgreSQL fixture server")

func TestDeterministicDeckShuffle(t *testing.T) {
	// Fixed independent hashlib.sha256("default:1") reference vector.
	if ShuffleKey("default", 1) != "3b3500c0413132a48c429d426dd8d9bf13451ae689159c87cd5fc561b80c2883" {
		t.Fatal("Django shuffle digest changed")
	}
	if ShuffleKey("a", 1) == ShuffleKey("b", 1) {
		t.Fatal("seed ignored")
	}
}
func TestPostgresDiscoveryPrivacyCursorDeckAndFeed(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t, *discoveryDSN, func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	cat := catalog.New(db)
	soc := social.New(db, platform.RecordAudit)
	soc.ActivityVisuals = func(ctx context.Context, q platform.Querier, a platform.Actor, ids []int64) (map[int64]any, error) {
		out := map[int64]any{}
		for _, id := range ids {
			out[id] = map[string]any{"kind": "generated_accent"}
		}
		return out, nil
	}
	rec := recommendations.New(db, cat, soc)
	s := New(db, cat, soc, rec)
	s.Cursor = platform.CursorCodec{Key: []byte(strings.Repeat("x", 32))}
	viewer := testdb.Actor(t, db, "discover-viewer", "adult")
	owner := testdb.Actor(t, db, "discover-owner", "adult")
	minor := testdb.Actor(t, db, "discover-child", "child")
	place := testdb.Place(t, db, "Public venue", "osm")
	testdb.Place(t, db, "Unpublished venue", "user")
	var typ int64
	if err := db.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		id, err := soc.CreateActivity(ctx, owner, social.ActivityInput{Place: place, ActivityType: typ, Title: "Adult meetup", Description: "Real description", StartsAt: time.Now().Add(time.Duration(i+1) * time.Hour), BeginnersWelcome: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `UPDATE social_activity SET is_publicly_listed=true WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
	}
	child, err := soc.CreateActivity(ctx, minor, social.ActivityInput{Place: place, ActivityType: typ, Title: "Never public", StartsAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(ctx, `UPDATE social_activity SET is_publicly_listed=true WHERE id=$1`, child)
	cards, err := s.ActivityCards(ctx, platform.Actor{}, catalog.Near{}, "", true, false, nil, nil, 20)
	if err != nil || len(cards) != 3 {
		t.Fatalf("public cards %+v %v", cards, err)
	}
	near, err := s.NearMe(ctx, viewer, catalog.Near{}, "", false, false, false, false, 300)
	if err != nil || len(near) != 1 || near[0]["name"] != "Public venue" {
		t.Fatalf("public places %+v %v", near, err)
	}
	deck, err := s.Deck(ctx, viewer, "reproducible", "", 1, catalog.Near{}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	items := deck["items"].([]map[string]any)
	if len(items) != 1 || items[0]["description"] != "Real description" || items[0]["place_name"] != "Public venue" || items[0]["cohort"] != nil {
		t.Fatal(deck)
	}
	actions := items[0]["actions"].(map[string]string)
	if !strings.HasPrefix(actions["detail_url"], "/api/v1/social/activities/") {
		t.Fatal(actions)
	}
	second, err := s.Deck(ctx, viewer, "reproducible", deck["next_cursor"].(string), 1, catalog.Near{}, "", false)
	if err != nil || itemID(second["items"].([]map[string]any)[0]) == itemID(items[0]) {
		t.Fatal(second, err)
	}
	feed, err := s.HomeFeed(ctx, viewer, catalog.Near{})
	if err != nil || len(feed["recommended"].([]map[string]any)) != 3 {
		t.Fatal(feed, err)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	r := platform.WithActor(httptest.NewRequest("GET", "/api/v1/discovery/activities/?limit=1", nil), viewer)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "next_cursor") {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := db.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, owner.ID, viewer.ID); err != nil {
		t.Fatal(err)
	}
	deck, err = s.Deck(ctx, viewer, "reproducible", "", 24, catalog.Near{}, "", false)
	if err != nil || len(deck["items"].([]map[string]any)) != 0 {
		t.Fatal(deck, err)
	}
}
