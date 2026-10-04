package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/coder/websocket"
)

func TestEventContentsAndSchemaIsolation(t *testing.T) {
	raw := `{"kind":"messaging","event":"message","room_id":1,"message_id":2}`
	if _, e := decodeEvent(raw); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{`{"kind":"messaging","event":"message","room_id":1,"message_id":2,"ciphertext":"not allowed"}`, `{"kind":"chat","event":"typing","room_id":0,"actor_id":2}`, strings.Repeat("x", 1025)} {
		if _, e := decodeEvent(bad); e == nil {
			t.Fatal("unsafe event accepted")
		}
	}
	if len(Channel("public")) > 63 || Channel("public") == Channel("private-test") {
		t.Fatal("schema channel collision")
	}
}
func TestBoundedSubscriberQueues(t *testing.T) {
	b := NewBroker(nil)
	b.ready.Store(true)
	b.maxTotal = 2
	b.maxRoom = 1
	b.queue = 1
	first, e := b.subscribe("chat", 1)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = b.subscribe("chat", 1); e == nil {
		t.Fatal("room bound ignored")
	}
	second, e := b.subscribe("chat", 2)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = b.subscribe("chat", 3); e == nil {
		t.Fatal("global bound ignored")
	}
	b.dispatch(Event{Kind: "chat", Event: "message", RoomID: 1, MessageID: 2})
	b.dispatch(Event{Kind: "chat", Event: "message", RoomID: 1, MessageID: 3})
	select {
	case <-first.done:
	default:
		t.Fatal("slow consumer retained")
	}
	select {
	case <-second.done:
		t.Fatal("independent room closed")
	default:
	}
	b.reset()
	select {
	case <-second.done:
	default:
		t.Fatal("listener disconnect did not invalidate sockets")
	}
}
func TestWebsocketDeliveryRechecksSessionAndOrigin(t *testing.T) {
	b := NewBroker(nil)
	b.ready.Store(true)
	var allowed atomic.Bool
	allowed.Store(true)
	var payloads atomic.Int64
	authority := func(context.Context, *http.Request) (platform.Actor, error) {
		if !allowed.Load() {
			return platform.Actor{}, platform.ErrForbidden
		}
		return platform.Actor{ID: 7, IsActive: true}, nil
	}
	adapter := Adapter{Authorize: func(context.Context, platform.Actor, int64) (bool, error) { return true, nil }, Receive: func(context.Context, platform.Actor, int64, json.RawMessage, string) error { return nil }, Payload: func(context.Context, platform.Actor, Event) (map[string]any, error) {
		payloads.Add(1)
		return map[string]any{"type": "message", "id": 2}, nil
	}}
	server, e := NewServer(b, authority, map[string]Adapter{"chat": adapter, "messaging": adapter}, DefaultConfig())
	if e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	server.Register(mux)
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws/chat/1/"
	if conn, response, e := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"https://other.invalid"}, "Authorization": {"Fixture"}}}); e == nil {
		conn.CloseNow()
		t.Fatal("cross-site cookie socket allowed")
	} else if response == nil || response.StatusCode != 403 {
		t.Fatal("origin status", e)
	}
	conn, _, e := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {httpServer.URL}, "Authorization": {"Fixture"}}})
	if e != nil {
		t.Fatal(e)
	}
	defer conn.CloseNow()
	allowed.Store(false)
	b.dispatch(Event{Kind: "chat", Event: "message", RoomID: 1, MessageID: 2})
	_, _, e = conn.Read(ctx)
	if websocket.CloseStatus(e) != websocket.StatusCode(4403) || payloads.Load() != 0 {
		t.Fatal("revoked delivery was resolved", e, payloads.Load())
	}
	cfg := DefaultConfig()
	cfg.OriginPatterns = []string{"*"}
	if _, e = NewServer(b, authority, map[string]Adapter{"chat": adapter, "messaging": adapter}, cfg); e == nil {
		t.Fatal("wildcard origins accepted")
	}
}
