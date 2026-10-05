package jobs

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5"
)

func TestOperatorCase5ICSExactFieldsPastRetentionAndUnfolding(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	future, past := now.AddDate(0, 0, 3).Format("20060102T150405Z"), now.AddDate(0, 0, -3).Format("20060102T150405Z")
	text := "BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:evt-1@venue\nSUMMARY:Chess club night\nDESCRIPTION:Weekly casual chess\nDTSTART:" + future + "\nDTEND:" + future + "\nURL:https://venue.example.ro/chess\nEND:VEVENT\nBEGIN:VEVENT\nUID:evt-old@venue\nSUMMARY:Past event\nDTSTART:" + past + "\nEND:VEVENT\nEND:VCALENDAR\n"
	rows, err := ParseICS(text, now)
	if err != nil || len(rows) != 2 {
		t.Fatal("parser must retain both source future and past events", err)
	}
	first := rows[0]
	if first.ExternalID != "evt-1@venue" || first.Title != "Chess club night" || first.URL != "https://venue.example.ro/chess" || first.Starts.Location() != time.UTC {
		t.Fatal("exact source ICS fields/timezone changed")
	}
	rows, err = ParseICS("BEGIN:VEVENT\nUID:u1\nSUMMARY:Long title that is\n  folded across lines\nDTSTART:"+future+"\nEND:VEVENT", now)
	if err != nil || len(rows) != 1 || rows[0].Title != "Long title that is folded across lines" {
		t.Fatal("source RFC unfolding spacing changed", err)
	}
}

func TestOperatorCase5ICSExplicitHorizonReanchorsSixMonthsAndRetainsCaps(t *testing.T) {
	start, now := time.Date(2026, 1, 31, 18, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows, err := ExpandRuleWithHorizon(start, "FREQ=MONTHLY;COUNT=6", now, 400)
	days := []string{}
	for _, d := range rows {
		days = append(days, d.Format("20060102"))
	}
	if err != nil || !reflect.DeepEqual(days, []string{"20260131", "20260228", "20260331", "20260430", "20260531", "20260630"}) {
		t.Fatal("immutable day-of-month reanchor failed explicit source400day horizon", days, err)
	}
	if defaults := ExpandRule(start, "FREQ=MONTHLY;COUNT=6", now); len(defaults) != 3 {
		t.Fatal("default90day horizon changed")
	}
	for _, invalid := range []int{-1, 0, 4001, 1 << 30} {
		rows, err := ExpandRuleWithHorizon(start, "FREQ=DAILY", now, invalid)
		if !errors.Is(err, platform.ErrInvalid) || rows != nil {
			t.Fatal("invalid explicit horizon accepted")
		}
	}
	bounded, err := ExpandRuleWithHorizon(now.AddDate(0, 0, -5000), "FREQ=DAILY", now, 4000)
	if err != nil || len(bounded) == 0 || len(bounded) > 120 {
		t.Fatal("explicit large horizon removed occurrence/fast-forward bounds", len(bounded), err)
	}
	for _, at := range bounded {
		if at.Before(now.Add(-24*time.Hour)) || at.After(now.AddDate(0, 0, 4000)) {
			t.Fatal("explicit horizon escaped finite time window")
		}
	}
}

func TestPostgresOperatorCase5ClassifierExactSeededAliasesAndNoMatch(t *testing.T) {
	r := jobFixture(t)
	ctx := context.Background()
	for text, slug := range map[string]string{"Maratonul Internațional Cluj": "marathon", "Zilele Clujului – city day": "city_day", "Concert live în parc": "concert", "Atelier de pictura": "workshop"} {
		id, err := ClassifyActivity(ctx, r.DB, text)
		if err != nil || id == nil {
			t.Fatal("source alias not classified", text, err)
		}
		var got string
		if err := r.DB.QueryRow(ctx, `SELECT slug FROM taxonomy_activitytype WHERE id=$1`, *id).Scan(&got); err != nil || got != slug {
			t.Fatal("source alias resolved to wrong exact taxonomy slug", text, got, slug, err)
		}
	}
	for _, text := range []string{"Random unrelated text xyz", ""} {
		if id, err := ClassifyActivity(ctx, r.DB, text); err != nil || id != nil {
			t.Fatal("unmatched source text classified", id, err)
		}
	}
}

func TestPostgresOperatorCase5FeedNamespacingFailureStatusAndReplay(t *testing.T) {
	r := jobFixture(t)
	ctx := context.Background()
	ids := map[string]int64{}
	for _, name := range []string{"a", "broken", "b"} {
		var id int64
		if err := r.DB.QueryRow(ctx, `INSERT INTO events_eventfeed(name,url,is_active,last_status,created_at,place_id,activity_type_id) VALUES($1,$2,true,'',now(),NULL,NULL) RETURNING id`, name, "https://example.org/"+name+".ics").Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[name] = id
	}
	future := r.Config.Now().AddDate(0, 0, 30).UTC().Format("20060102T150405Z")
	r.Config.FetchFeed = func(_ context.Context, url string) ([]byte, error) {
		if strings.Contains(url, "broken") {
			return nil, errors.New("synthetic private failure")
		}
		return []byte("BEGIN:VEVENT\nUID:shared-uid-1\nSUMMARY:Chess night\nDTSTART:" + future + "\nEND:VEVENT"), nil
	}
	for n := 0; n < 2; n++ {
		summary, err := r.SyncFeeds(ctx)
		if err != nil || summary["events"] != 2 || summary["failed_feeds"] != 1 {
			t.Fatal("feed failure isolation/counts changed", summary, err)
		}
	}
	rows, err := r.DB.Query(ctx, `SELECT external_id FROM events_event ORDER BY external_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		got[id] = true
	}
	if rows.Err() != nil || len(got) != 2 || !got[fmt.Sprintf("feed%d:shared-uid-1", ids["a"])] || !got[fmt.Sprintf("feed%d:shared-uid-1", ids["b"])] {
		t.Fatal("source per-feed identity/replay changed", got)
	}
	for name, id := range ids {
		var status string
		var synced bool
		if err := r.DB.QueryRow(ctx, `SELECT last_status,last_synced_at IS NOT NULL FROM events_eventfeed WHERE id=$1`, id).Scan(&status, &synced); err != nil {
			t.Fatal(err)
		}
		prefix := "ok:"
		if name == "broken" {
			prefix = "error:"
		}
		if !strings.HasPrefix(status, prefix) || name != "broken" && !synced || strings.Contains(status, "synthetic private failure") {
			t.Fatal("source feed operational status/time missing or raw error leaked", status)
		}
	}
}

func TestPostgresOperatorCase5UIDlessAndOptionalCreditUpsert(t *testing.T) {
	r := jobFixture(t)
	ctx := context.Background()
	place := testdb.Place(t, r.DB, "Park", "osm")
	when := r.Config.Now().AddDate(0, 0, 1)
	for n := 0; n < 2; n++ {
		if err := platform.Transaction(ctx, r.DB, func(tx pgx.Tx) error { return upsertICS(ctx, tx, RawEvent{Title: "Picnic", Starts: when}, &place, nil) }); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := r.DB.QueryRow(ctx, `SELECT count(*) FROM events_event WHERE title='Picnic' AND place_id=$1 AND starts_at=$2`, place, when).Scan(&count); err != nil || count != 1 {
		t.Fatal("source UIDless place/title/start key duplicated", count, err)
	}
	raw := RawEvent{Title: "Concert", Starts: when, URL: "https://events.example/concert", ExternalID: "roedu:concert-1", Attribution: "RO-EDU", License: "CC BY 4.0", Provenance: "https://data.example/events/concert-1"}
	if err := platform.Transaction(ctx, r.DB, func(tx pgx.Tx) error { return upsertICS(ctx, tx, raw, &place, nil) }); err != nil {
		t.Fatal(err)
	}
	var credit, license, page string
	if err := r.DB.QueryRow(ctx, `SELECT attribution,license_name,provenance_url FROM events_event WHERE external_id=$1`, raw.ExternalID).Scan(&credit, &license, &page); err != nil || credit != raw.Attribution || license != raw.License || page != raw.Provenance {
		t.Fatal("exact source optional credit did not survive upsert", err)
	}
}
