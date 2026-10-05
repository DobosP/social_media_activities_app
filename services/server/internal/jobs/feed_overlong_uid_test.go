package jobs

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFeedExternalIDFitsColumnAndStaysStable(t *testing.T) {
	if short := feedExternalID(7, "bare-id"); short != "feed7:bare-id" {
		t.Fatal("fitting UID namespace changed", short)
	}
	// "feed7:" is six characters, so a 194-character UID is the last that fits.
	boundary := strings.Repeat("x", 194)
	if got := feedExternalID(7, boundary); got != "feed7:"+boundary || utf8.RuneCountInString(got) != 200 {
		t.Fatal("boundary UID was not preserved")
	}
	long, other := strings.Repeat("x", 195), strings.Repeat("x", 194)+"y"
	first, second := feedExternalID(7, long), feedExternalID(7, other)
	if utf8.RuneCountInString(first) > 200 || first != feedExternalID(7, long) || first == second || first == feedExternalID(8, long) || !strings.HasPrefix(first, "feed7:sha256:") {
		t.Fatal("overlong UID identity is not bounded, stable and distinct", first, second)
	}
	if got := feedExternalID(7, strings.Repeat("é", 195)); utf8.RuneCountInString(got) > 200 {
		t.Fatal("multibyte overlong UID exceeded the column")
	}
}

// A namespaced UID longer than events_event.external_id used to be looked up
// in full but stored truncated, so every later sync hit the unique index and
// failed the whole feed.
func TestPostgresFeedOverlongUIDReplaysWithoutFailingFeed(t *testing.T) {
	r := jobFixture(t)
	ctx := context.Background()
	var feed int64
	if err := r.DB.QueryRow(ctx, `INSERT INTO events_eventfeed(name,url,is_active,last_status,created_at,place_id,activity_type_id) VALUES('overlong','https://example.org/overlong.ics',true,'',now(),NULL,NULL) RETURNING id`).Scan(&feed); err != nil {
		t.Fatal(err)
	}
	future := r.Config.Now().AddDate(0, 0, 30).UTC().Format("20060102T150405Z")
	title := "Chess night"
	uids := []string{strings.Repeat("a", 195), strings.Repeat("b", 250), "short-uid"}
	r.Config.FetchFeed = func(context.Context, string) ([]byte, error) {
		var body strings.Builder
		for _, uid := range uids {
			body.WriteString("BEGIN:VEVENT\nUID:" + uid + "\nSUMMARY:" + title + "\nDTSTART:" + future + "\nEND:VEVENT\n")
		}
		return []byte(body.String()), nil
	}
	for n := 0; n < 3; n++ {
		if n == 2 {
			title = "Chess night moved"
		}
		summary, err := r.SyncFeeds(ctx)
		if err != nil || summary["events"] != len(uids) || summary["failed_feeds"] != 0 {
			t.Fatal("overlong UID failed its feed on sync", n, summary, err)
		}
	}
	var rows, overlong, renamed int
	if err := r.DB.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE char_length(external_id)>200),count(*) FILTER(WHERE title='Chess night moved') FROM events_event WHERE source='ical'`).Scan(&rows, &overlong, &renamed); err != nil || rows != len(uids) || overlong != 0 || renamed != len(uids) {
		t.Fatal("overlong UID replay duplicated, truncated or missed its update", rows, overlong, renamed, err)
	}
	var status string
	if err := r.DB.QueryRow(ctx, `SELECT last_status FROM events_eventfeed WHERE id=$1`, feed).Scan(&status); err != nil || !strings.HasPrefix(status, "ok:") {
		t.Fatal("feed status after overlong UID replay", status, err)
	}
}
