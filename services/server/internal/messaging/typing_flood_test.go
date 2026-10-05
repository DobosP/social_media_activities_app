package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/chat"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5"
)

// One member flooding typing frames must neither force a peer to reload
// durable history (1012) nor turn each frame into a PostgreSQL notification.
func TestPostgresPlainThreadTypingFloodStaysBounded(t *testing.T) {
	s := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := fixtureUser(t, s, "flood-owner", "adult")
	b := fixtureUser(t, s, "flood-member", "adult")
	domain := social.New(s.DB, platform.RecordAudit)
	domain.BodyMarkup = func(text string, _ map[string]bool, _ bool) string { return html.EscapeString(text) }
	var place, activityType int64
	if e := s.DB.QueryRow(ctx, `INSERT INTO places_place(name,source,osm_type,osm_id,external_id,location,raw_tags,address_street,address_housenumber,address_city,address_postcode,address_country,opening_hours_raw,opening_hours,phone,website,first_seen_at,last_seen_at,attribution,license_name,provenance_url) VALUES('Synthetic flood hall','osm','',NULL,'',ST_SetSRID(ST_MakePoint(23.6,46.77),4326),'{}','','','Cluj-Napoca','','RO','','{}','','',now(),now(),'','','') RETURNING id`).Scan(&place); e != nil {
		t.Fatal(e)
	}
	if e := s.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&activityType); e != nil {
		t.Fatal(e)
	}
	activity, e := domain.CreateActivity(ctx, a, social.ActivityInput{Place: place, ActivityType: activityType, Title: "Synthetic flood activity", StartsAt: time.Now().Add(time.Hour)})
	if e != nil {
		t.Fatal(e)
	}
	membership, e := domain.Join(ctx, b, activity)
	if e != nil {
		t.Fatal(e)
	}
	if e = domain.Vote(ctx, a, membership, true, false); e != nil {
		t.Fatal(e)
	}
	var thread int64
	if e = s.DB.QueryRow(ctx, `SELECT id FROM social_thread WHERE activity_id=$1`, activity).Scan(&thread); e != nil {
		t.Fatal(e)
	}
	// A dedicated listener outside the bounded pool counts every notification
	// the flood actually publishes on this schema's channel.
	var schema string
	if e = s.DB.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); e != nil {
		t.Fatal(e)
	}
	counter, e := pgx.ConnectConfig(ctx, s.DB.Config().ConnConfig)
	if e != nil {
		t.Fatal(e)
	}
	defer counter.Close(context.Background())
	if _, e = counter.Exec(ctx, `LISTEN `+pgx.Identifier{chat.Channel(schema)}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	broker := chat.NewBroker(s.DB)
	go broker.Run(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for !broker.Ready() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !broker.Ready() {
		t.Fatal("listener not ready")
	}
	plain, e := chat.PlainAdapter(broker, chat.PlainCallbacks{Authorize: func(ctx context.Context, a platform.Actor, id int64) (bool, error) {
		return domain.CanReadThread(ctx, s.DB, a, id)
	}, Write: func(ctx context.Context, a platform.Actor, id int64, body string, reply *int64) error {
		kind, owner, e := domain.ThreadOwner(ctx, a, id)
		if e != nil {
			return e
		}
		_, e = domain.WritePost(ctx, a, kind, owner, social.PostInput{Body: body, ReplyTo: reply}, false)
		return e
	}, Typing: domain.TypingIdentity, Post: domain.LivePost, Attachments: func(context.Context, platform.Actor, int64) ([]any, error) { return []any{}, nil }})
	if e != nil {
		t.Fatal(e)
	}
	authority := func(ctx context.Context, r *http.Request) (platform.Actor, error) {
		if r.Header.Get("Authorization") == "Fixture a" {
			return actor(ctx, s.DB, a.ID)
		}
		return actor(ctx, s.DB, b.ID)
	}
	live, e := chat.NewServer(broker, authority, map[string]chat.Adapter{"chat": plain, "messaging": s.LiveAdapter()}, chat.DefaultConfig())
	if e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	live.Register(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	dial := func(name string) *websocket.Conn {
		t.Helper()
		url := "ws" + strings.TrimPrefix(server.URL, "http") + fmt.Sprintf("/ws/chat/%d/", thread)
		conn, _, e := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Fixture " + name}, "Origin": {server.URL}}})
		if e != nil {
			t.Fatal(e)
		}
		return conn
	}
	ca, cb := dial("a"), dial("b")
	defer ca.CloseNow()
	defer cb.CloseNow()
	started := time.Now()
	for i := 0; i < 200; i++ {
		if e = ca.Write(ctx, websocket.MessageText, []byte(`{"type":"typing"}`)); e != nil {
			t.Fatal("typing flood closed the sender", e)
		}
	}
	// The sender's read loop is sequential, so this durable message is handled
	// after every flood frame and its notification follows all typing ones.
	if e = ca.Write(ctx, websocket.MessageText, []byte(`{"body":"after the flood"}`)); e != nil {
		t.Fatal(e)
	}
	readCtx, done := context.WithTimeout(ctx, 10*time.Second)
	defer done()
	for delivered := false; !delivered; {
		_, raw, e := cb.Read(readCtx)
		if e != nil {
			t.Fatal("peer socket closed during a typing flood", websocket.CloseStatus(e), e)
		}
		var payload map[string]any
		if e = json.Unmarshal(raw, &payload); e != nil {
			t.Fatal(e)
		}
		if payload["type"] == "message" {
			if payload["body_html"] != "after the flood" {
				t.Fatal("durable message after the flood", payload["type"])
			}
			delivered = true
		} else if payload["type"] != "typing" {
			t.Fatal("unexpected live payload", payload["type"])
		}
	}
	elapsed := time.Since(started)
	publishes := 0
	for {
		notification, e := counter.WaitForNotification(readCtx)
		if e != nil {
			t.Fatal("durable notification missing", e)
		}
		if strings.Contains(notification.Payload, `"event":"message"`) {
			break
		}
		if strings.Contains(notification.Payload, `"event":"typing"`) {
			publishes++
		}
	}
	// The per-connection typing interval and the (room, actor) coalescing window
	// both allow one publish per window, so the flood yields at most one plus one
	// per elapsed window. Before the limits every frame published. The literal
	// matches chat.DefaultConfig().TypingInterval and keeps this test compiling
	// against the unthrottled package for fail-before evidence.
	window := 2 * time.Second
	bound := 1 + int(elapsed/window)
	if publishes < 1 || publishes > bound {
		t.Fatal("typing publishes not bounded by the per-connection and coalescing limits", publishes, bound)
	}
	cancel()
}
