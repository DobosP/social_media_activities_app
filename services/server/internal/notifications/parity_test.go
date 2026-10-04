package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestNativeNotificationMuteChokepointAndProtectedRetention(t *testing.T) {
	if *domainDSN == "" {
		t.Skip("explicit fixture DSN required")
	}
	db := testdb.New(t, *domainDSN, nil)
	ctx := context.Background()
	a := testdb.Actor(t, db, "protected-notice-owner", "adult")
	if _, err := db.Exec(ctx, `INSERT INTO notifications_notificationpreference(user_id,muted_kinds) VALUES($1,ARRAY['system','moderation','announcement'])`, a.ID); err != nil {
		t.Fatal(err)
	}
	// Use the same platform chokepoint every native domain calls.
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	for _, kind := range []string{"system", "moderation", "announcement", "event_reminder"} {
		sent, err := platform.Notify(ctx, tx, a.ID, kind, "Fixture notice", "", "/notifications/")
		if err != nil || sent != (kind != "announcement") {
			t.Fatal("mutable/nonmutable delivery", kind, sent, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE notifications_notification SET created_at=now()-interval '40 days',read_at=now()-interval '2 days' WHERE recipient_id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO notifications_notification(recipient_id,kind,title,body,url,created_at,read_at) VALUES($1,'event_reminder','Unread old','','',now()-interval '40 days',NULL)`, a.ID); err != nil {
		t.Fatal(err)
	}
	s := New(db, platform.CursorCodec{Key: []byte(strings.Repeat("z", 40))})
	if n, err := s.PurgeRead(ctx, 30, 1); err != nil || n != 1 {
		t.Fatal("bounded retention", n, err)
	}
	if n, err := s.PurgeRead(ctx, 30, 100); err != nil || n != 0 {
		t.Fatal("protected notice purged", n, err)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	call := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, platform.WithActor(httptest.NewRequest("GET", path, nil), a))
		return w
	}
	got := call("/api/v1/notifications/?limit=1&unread=true")
	var page struct {
		Unread  int    `json:"unread_count"`
		Next    string `json:"next_cursor"`
		Results []map[string]any
	}
	if json.Unmarshal(got.Body.Bytes(), &page) != nil || page.Unread != 1 || len(page.Results) != 1 || page.Next != "" || page.Results[0]["title"] != "Unread old" {
		t.Fatal("unread filter", got.Body.String())
	}
	got = call("/api/v1/notifications/?limit=1")
	if json.Unmarshal(got.Body.Bytes(), &page) != nil || page.Next == "" {
		t.Fatal("cursor missing", got.Body.String())
	}
	got = call(fmt.Sprintf("/api/v1/notifications/?limit=1&cursor=%s", page.Next))
	if got.Code != 200 {
		t.Fatal("signed cursor rejected")
	}
	got = call("/api/v1/notifications/?limit=1&cursor=forged")
	if got.Code != 200 {
		t.Fatal("malformed cursor source fallback")
	}
}
