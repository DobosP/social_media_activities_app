package notifications

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestRetirementPostgresNotificationInboxQueryGrowth(t *testing.T) {
	db := testdb.New(t, *domainDSN, nil)
	pool, trace := testdb.TracedPool(t, db)
	owner := testdb.Actor(t, pool, "growth-inbox-owner", "adult")
	other := testdb.Actor(t, pool, "growth-inbox-other", "adult")
	s := New(pool, platform.CursorCodec{Key: []byte(strings.Repeat("q", 40))})
	mux := http.NewServeMux()
	s.Register(mux)
	seed := func(begin, end int) {
		if _, err := pool.Exec(context.Background(), `INSERT INTO notifications_notification(recipient_id,kind,title,body,url,created_at,read_at) SELECT $1,'announcement','Synthetic inbox row','Synthetic body','/profile/',now()+i*interval '1 millisecond',NULL FROM generate_series($2::int,$3::int) i`, owner.ID, begin, end-1); err != nil {
			t.Fatal(err)
		}
	}
	read := func(count int) int64 {
		trace.Reset()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, platform.WithActor(httptest.NewRequest("GET", "/api/v1/notifications/?limit=100", nil), owner))
		if w.Code != 200 {
			t.Fatal("notification inbox unavailable", w.Code)
		}
		queries := trace.Count()
		var body struct {
			Unread  int              `json:"unread_count"`
			Results []map[string]any `json:"results"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Unread != count || len(body.Results) != count {
			t.Fatal("notification inbox did not contain the growing fixture", err)
		}
		return queries
	}
	seed(0, 4)
	small := read(4)
	seed(4, 28)
	large := read(28)
	if small != 2 || large != small {
		t.Fatalf("inbox introduced per-notice queries: %d -> %d", small, large)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, platform.WithActor(httptest.NewRequest("GET", "/api/v1/notifications/", nil), other))
	if w.Code != 200 || strings.Contains(w.Body.String(), "Synthetic inbox row") {
		t.Fatal("growing inbox leaked another subject's notices")
	}
}
