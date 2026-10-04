package notifications

import (
	"context"
	"flag"
	"fmt"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var domainDSN = flag.String("domain-test-dsn", "", "explicit disposable native domain database")

func TestNativeNotificationOwnershipAndCursor(t *testing.T) {
	if *domainDSN == "" {
		t.Skip("explicit fixture DSN required")
	}
	db := testdb.New(t, *domainDSN, nil)
	a := testdb.Actor(t, db, "notice-owner", "adult")
	b := testdb.Actor(t, db, "notice-other", "adult")
	var id int64
	if err := db.QueryRow(context.Background(), `INSERT INTO notifications_notification(recipient_id,kind,title,body,url,created_at,read_at) VALUES($1,'system','Synthetic notice','','/profile/',now(),NULL) RETURNING id`, a.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	s := New(db, platform.CursorCodec{Key: []byte(strings.Repeat("x", 40))})
	mux := http.NewServeMux()
	s.Register(mux)
	call := func(actor platform.Actor, method, path string) *httptest.ResponseRecorder {
		r := platform.WithActor(httptest.NewRequest(method, path, nil), actor)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	response := call(a, "GET", "/api/v1/notifications/?limit=1")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"unread_count":1`) || !strings.Contains(response.Body.String(), `"next_cursor":""`) {
		t.Fatal("last cursor or unread", response.Code, response.Body.String())
	}
	path := fmt.Sprintf("/api/notifications/%d/read/", id)
	if call(b, "POST", path).Code != 404 {
		t.Fatal("foreign notification read")
	}
	if call(a, "POST", path).Code != 200 {
		t.Fatal("own notification read")
	}
	response = call(a, "POST", "/api/notifications/read-all/")
	if !strings.Contains(response.Body.String(), `"marked_read":0`) {
		t.Fatal("read idempotence")
	}
}
