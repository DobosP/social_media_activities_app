package commands

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

func operatorCase4Place(t *testing.T, db *pgxpool.Pool, name, source string, lon, lat float64, tags map[string]any) int64 {
	t.Helper()
	id := testdb.Place(t, db, name, source)
	raw, err := json.Marshal(tags)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(context.Background(), `UPDATE places_place SET location=ST_SetSRID(ST_MakePoint($2,$3),4326),raw_tags=$4 WHERE id=$1`, id, lon, lat, raw); err != nil {
		t.Fatal(err)
	}
	return id
}

func operatorCase4Edge(t *testing.T, db *pgxpool.Pool, place int64, slug string, confidence float64, origin, rule string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(context.Background(), `INSERT INTO places_placeactivity(place_id,activity_id,origin,confidence,source,mapping_rule,is_disputed,created_at,updated_at) SELECT $1,id,$3,$4,'osm',$5,false,now(),now() FROM taxonomy_activitytype WHERE slug=$2 RETURNING id`, place, slug, origin, confidence, rule).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPostgresOperatorCase4DemoUsersParticipationStaffAndExactReplay(t *testing.T) {
	_, r := seedFixture(t)
	ctx := context.Background()
	r.Config.Now = time.Now
	place := testdb.Place(t, r.DB, "Sala Demo", "osm")
	var slug string
	if err := r.DB.QueryRow(ctx, `SELECT slug FROM taxonomy_activitytype WHERE is_active ORDER BY slug LIMIT 1`).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	operatorCase4Edge(t, r.DB, place, slug, .9, "inferred", "")
	for n := 0; n < 2; n++ {
		if _, err := r.Run(ctx, "seed_demo_users", nil); err != nil {
			t.Fatal("actual registered seed invocation", err)
		}
	}
	var ana int64
	if err := r.DB.QueryRow(ctx, `SELECT id FROM accounts_user WHERE username='ana.demo'`).Scan(&ana); err != nil {
		t.Fatal(err)
	}
	actor, err := accounts.NewStore(r.DB).Actor(ctx, strconv.FormatInt(ana, 10))
	if err != nil || platform.Participate(ctx, r.DB, actor) != nil {
		t.Fatal("seeded Ana cannot participate through native current-authority gate", err)
	}
	var staff, super bool
	if err := r.DB.QueryRow(ctx, `SELECT is_staff,is_superuser FROM accounts_user WHERE username='staff.demo'`).Scan(&staff, &super); err != nil || !staff || !super {
		t.Fatal("staff demo account lacks both staff/superuser flags", err)
	}
	var users, owned int
	if err := r.DB.QueryRow(ctx, `SELECT count(*) FROM accounts_user WHERE username LIKE '%.demo'`).Scan(&users); err != nil || users != 3 {
		t.Fatal("seed replay duplicated demo users", users, err)
	}
	if err := r.DB.QueryRow(ctx, `SELECT count(*) FROM social_activity WHERE owner_id=$1 AND title LIKE '[DEMO]%'`, ana).Scan(&owned); err != nil || owned != 1 {
		t.Fatal("seed replay did not retain exactly one Ana-owned demo activity", owned, err)
	}
}

func TestOperatorCase4SeedsRefuseProduction(t *testing.T) {
	s := &Service{}
	if _, err := s.seedDemoUsers(context.Background(), nil); err == nil {
		t.Fatal("demo users seed accepted outside development mode")
	}
	if _, err := s.generateDemoEvents(context.Background(), nil); err == nil {
		t.Fatal("demo event generation accepted outside development mode")
	}
}

func TestPostgresOperatorCase4DemoEventsRescheduleAndSynthesizeExactValues(t *testing.T) {
	s, r := seedFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second).Add(123456 * time.Microsecond)
	r.Config.Now = func() time.Time { return now }
	place := testdb.Place(t, r.DB, "Casa de Cultură", "osm")
	old := now.AddDate(0, 0, -17)
	var event int64
	if err := r.DB.QueryRow(ctx, `INSERT INTO events_event(title,description,starts_at,ends_at,url,source,external_id,attribution,license_name,provenance_url,created_at,updated_at,place_id,is_import_held,lifecycle_status,is_tombstone,source_venue_id,source_city,source_pack_id,source_snapshot_id,source_release_id,source_recurrence,source_timezone,source_currency,source_availability,source_category) VALUES('Concert vechi','',$1,$1::timestamptz+interval '2 hours','','roedu','roedu:Concert vechi','','','',now(),now(),$2,false,'scheduled',false,'','','','','','','','','','') RETURNING id`, old, place).Scan(&event); err != nil {
		t.Fatal(err)
	}
	if _, err := s.generateDemoEvents(ctx, nil); err != nil {
		t.Fatal(err)
	}
	var start, end time.Time
	if err := r.DB.QueryRow(ctx, `SELECT starts_at,ends_at FROM events_event WHERE id=$1`, event).Scan(&start, &end); err != nil {
		t.Fatal(err)
	}
	start, end = start.UTC(), end.UTC()
	if start.Before(now) || start.After(now.AddDate(0, 0, 29)) || start.Weekday() != old.Weekday() || start.Hour() != old.Hour() || start.Minute() != old.Minute() || start.Second() != old.Second() || start.Nanosecond() != old.Nanosecond() || !end.After(start) {
		t.Fatal("reschedule lost source weekday/time/end or bounded future window")
	}
	for n := 0; n < 2; n++ {
		if _, err := s.generateDemoEvents(ctx, args(map[string]any{"synthesize": 6})); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := r.DB.Query(ctx, `SELECT external_id,title,starts_at FROM events_event WHERE source='demo' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var external, title string
		var starts time.Time
		if err := rows.Scan(&external, &title, &starts); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(external, "demo:") || !strings.HasPrefix(title, "[DEMO]") || starts.Before(now) {
			t.Fatal("synthesized source identity/title/future marker changed")
		}
		count++
	}
	if rows.Err() != nil || count != 6 {
		t.Fatal("demo event replay did not retain exact six synthetic events", count, rows.Err())
	}
}

func TestPostgresOperatorCase4DemoEventDryRunCountsWithoutWrites(t *testing.T) {
	s, r := seedFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second).Add(123456 * time.Microsecond)
	r.Config.Now = func() time.Time { return now }
	place := testdb.Place(t, r.DB, "Casa de Cultură", "osm")
	old := now.AddDate(0, 0, -10)
	var event int64
	if err := r.DB.QueryRow(ctx, `INSERT INTO events_event(title,description,starts_at,ends_at,url,source,external_id,attribution,license_name,provenance_url,created_at,updated_at,place_id,is_import_held,lifecycle_status,is_tombstone,source_venue_id,source_city,source_pack_id,source_snapshot_id,source_release_id,source_recurrence,source_timezone,source_currency,source_availability,source_category) VALUES('Concert vechi','',$1,$1::timestamptz+interval '2 hours','','roedu','roedu:dry','','','',now(),now(),$2,false,'scheduled',false,'','','','','','','','','','') RETURNING id`, old, place).Scan(&event); err != nil {
		t.Fatal(err)
	}
	result, err := s.generateDemoEvents(ctx, args(map[string]any{"dry_run": true, "synthesize": 3}))
	if err != nil || result.(map[string]int)["rescheduled"] != 1 || result.(map[string]int)["synthesized"] != 3 {
		t.Fatal("dry-run meaningful count values missing", result, err)
	}
	var retained time.Time
	var demo int
	if err := r.DB.QueryRow(ctx, `SELECT starts_at FROM events_event WHERE id=$1`, event).Scan(&retained); err != nil || !retained.Equal(old) {
		t.Fatal("dry run changed original event start", err)
	}
	if err := r.DB.QueryRow(ctx, `SELECT count(*) FROM events_event WHERE source='demo'`).Scan(&demo); err != nil || demo != 0 {
		t.Fatal("dry run wrote synthetic events", demo, err)
	}
}

func TestPostgresOperatorCase4DedupCommandDryRunAndOSMIdentity(t *testing.T) {
	r, s := commandFixture(t)
	ctx := context.Background()
	osm := operatorCase4Place(t, r.DB, "City Sports Hall", "osm", 23.59, 46.77, map[string]any{})
	duplicate := operatorCase4Place(t, r.DB, "City Sports Hall", "overture", 23.59001, 46.77001, map[string]any{})
	if _, err := r.DB.Exec(ctx, `UPDATE places_place SET external_id='ov-1' WHERE id=$1`, duplicate); err != nil {
		t.Fatal(err)
	}
	if _, err := s.dedupPlaces(ctx, nil); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := r.DB.QueryRow(ctx, `SELECT count(*) FROM places_place`).Scan(&count); err != nil || count != 2 {
		t.Fatal("default dedup dry run mutated source rows", err)
	}
	if _, err := s.dedupPlaces(ctx, args(map[string]any{"apply": true})); err != nil {
		t.Fatal(err)
	}
	var survivor int64
	var external string
	if err := r.DB.QueryRow(ctx, `SELECT id,raw_tags->'merged_sources'->0->>'external_id' FROM places_place`).Scan(&survivor, &external); err != nil || survivor != osm || external != "ov-1" {
		t.Fatal("apply did not retain exact OSM canonical PK and source external identity", err)
	}
	if err := r.DB.QueryRow(ctx, `SELECT count(*) FROM places_place`).Scan(&count); err != nil || count != 1 {
		t.Fatal("apply retained duplicate venue", err)
	}
}

func TestPostgresOperatorCase4MergeProtectsConfirmedEdge(t *testing.T) {
	r, s := commandFixture(t)
	ctx := context.Background()
	osm := operatorCase4Place(t, r.DB, "Court", "osm", 23.59, 46.77, map[string]any{})
	duplicate := operatorCase4Place(t, r.DB, "Court", "overture", 23.59, 46.77, map[string]any{})
	confirmed := operatorCase4Edge(t, r.DB, osm, "basketball", 1, "confirmed", "")
	operatorCase4Edge(t, r.DB, duplicate, "basketball", .5, "inferred", "")
	if applied, err := s.mergePlaces(ctx, osm, duplicate, false); err != nil || !applied {
		t.Fatal("dependency-free merge failed", err)
	}
	var origin string
	var confidence float64
	if err := r.DB.QueryRow(ctx, `SELECT origin,confidence FROM places_placeactivity WHERE id=$1`, confirmed).Scan(&origin, &confidence); err != nil || origin != "confirmed" || confidence != 1 {
		t.Fatal("merge changed protected edge's identity/origin/confidence", err)
	}
}

func TestPostgresOperatorCase4AggregateHigherConfidenceAndConservativeExceptions(t *testing.T) {
	for _, name := range []string{"merge", "named_far_dependent", "dry_run"} {
		t.Run(name, func(t *testing.T) {
			r, s := commandFixture(t)
			ctx := context.Background()
			parent := operatorCase4Place(t, r.DB, "Baza Sportiva", "osm", 23.6, 46.77, map[string]any{"leisure": "sports_centre", "name": "Baza Sportiva"})
			child := operatorCase4Place(t, r.DB, "", "osm", 23.6004, 46.7701, map[string]any{"leisure": "pitch", "sport": "soccer"})
			operatorCase4Edge(t, r.DB, child, "football", .9, "inferred", "football_pitch")
			if name == "merge" {
				operatorCase4Edge(t, r.DB, parent, "football", .4, "inferred", "")
			}
			if name == "named_far_dependent" {
				if _, err := r.DB.Exec(ctx, `DELETE FROM places_placeactivity WHERE place_id=$1`, child); err != nil {
					t.Fatal(err)
				}
				if _, err := r.DB.Exec(ctx, `DELETE FROM places_place WHERE id=$1`, child); err != nil {
					t.Fatal(err)
				}
				ids := []int64{operatorCase4Place(t, r.DB, "Teren 1", "osm", 23.6004, 46.7701, map[string]any{"leisure": "pitch", "sport": "soccer"}), operatorCase4Place(t, r.DB, "", "osm", 23.63, 46.79, map[string]any{"leisure": "pitch", "sport": "soccer"}), operatorCase4Place(t, r.DB, "", "osm", 23.6002, 46.7702, map[string]any{"leisure": "pitch", "sport": "soccer"})}
				for _, id := range ids {
					operatorCase4Edge(t, r.DB, id, "football", .9, "inferred", "")
				}
				actor := testdb.Actor(t, r.DB, "operator-case4-dependent-owner", "adult")
				var typ int64
				if err := r.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='football'`).Scan(&typ); err != nil {
					t.Fatal(err)
				}
				if _, err := social.New(r.DB, platform.RecordAudit).CreateActivity(ctx, actor, social.ActivityInput{Place: ids[2], ActivityType: typ, Title: "Scheduled match", StartsAt: time.Now().Add(24 * time.Hour)}); err != nil {
					t.Fatal(err)
				}
				if _, err := s.aggregate(ctx, nil); err != nil {
					t.Fatal(err)
				}
				var retained, edges int
				if err := r.DB.QueryRow(ctx, `SELECT count(*) FROM places_place WHERE id=ANY($1)`, ids).Scan(&retained); err != nil || retained != 3 {
					t.Fatal("aggregation removed named/far/dependent source fixture", err)
				}
				if err := r.DB.QueryRow(ctx, `SELECT count(*) FROM places_placeactivity WHERE place_id=$1`, parent).Scan(&edges); err != nil || edges != 0 {
					t.Fatal("exception cases changed parent edge set", err)
				}
				return
			}
			input := map[string]json.RawMessage(nil)
			if name == "dry_run" {
				input = args(map[string]any{"dry_run": true})
			}
			result, err := s.aggregate(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			var children, parentEdges int
			if err := r.DB.QueryRow(ctx, `SELECT count(*) FROM places_place WHERE id=$1`, child).Scan(&children); err != nil {
				t.Fatal(err)
			}
			if err := r.DB.QueryRow(ctx, `SELECT count(*) FROM places_placeactivity WHERE place_id=$1`, parent).Scan(&parentEdges); err != nil {
				t.Fatal(err)
			}
			if name == "dry_run" {
				if result.(map[string]int)["would_merge"] != 1 || result.(map[string]int)["merged"] != 0 || children != 1 || parentEdges != 0 {
					t.Fatal("dry-run meaningful counts or unchanged state differ", result)
				}
				return
			}
			var confidence float64
			var origin, rule string
			if children != 0 || parentEdges != 1 {
				t.Fatal("aggregate did not remove exact child and retain one folded edge")
			}
			if err := r.DB.QueryRow(ctx, `SELECT confidence,origin,mapping_rule FROM places_placeactivity WHERE place_id=$1`, parent).Scan(&confidence, &origin, &rule); err != nil || confidence != .9 || origin != "inferred" || rule != "football_pitch" {
				t.Fatal("aggregate lost source higher confidence/origin/mapping rule", err)
			}
		})
	}
}
