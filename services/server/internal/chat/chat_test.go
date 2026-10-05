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
func TestFrameLimiterDropsTypingAndMetersAbuse(t *testing.T) {
	clock := time.Unix(1_700_000_000, 0)
	now := func() time.Time { return clock }
	cfg := DefaultConfig()
	limiter := newFrameLimiter(cfg, now)
	if limiter.allow(true) != frameAccept {
		t.Fatal("first typing frame dropped")
	}
	for i := 0; i < 50; i++ {
		if limiter.allow(true) != frameDrop {
			t.Fatal("typing within the per-connection interval accepted", i)
		}
	}
	// Dropped typing spent no tokens: the rest of the burst is still available.
	for i := 1; i < cfg.FrameBurst; i++ {
		if limiter.allow(false) != frameAccept {
			t.Fatal("frame within the burst refused", i)
		}
	}
	if limiter.allow(false) != frameAbuse {
		t.Fatal("ordinary frame beyond the bucket was not abuse")
	}
	clock = clock.Add(cfg.FrameInterval)
	if limiter.allow(false) != frameAccept {
		t.Fatal("bucket did not refill with the injected clock")
	}
	if limiter.allow(false) != frameAbuse {
		t.Fatal("refill exceeded one token per interval")
	}
	clock = clock.Add(cfg.TypingInterval)
	if limiter.allow(true) != frameAccept {
		t.Fatal("typing after the interval dropped")
	}
	slow := newFrameLimiter(Config{FrameInterval: time.Minute, FrameBurst: 1, TypingInterval: time.Second}, now)
	if slow.allow(false) != frameAccept {
		t.Fatal("first frame refused")
	}
	clock = clock.Add(time.Second)
	if slow.allow(true) != frameDrop {
		t.Fatal("typing on an empty bucket escalated instead of dropping")
	}
	if !typingFrame([]byte(`{"type":"typing"}`)) || typingFrame([]byte(`{"body":"typing"}`)) || typingFrame([]byte(`{"type":"typing"} {}`)) || typingFrame([]byte(`{"type":7}`)) {
		t.Fatal("frame classification")
	}
}
func TestTransientEventsNeverEvictSubscribers(t *testing.T) {
	b := NewBroker(nil)
	b.ready.Store(true)
	b.queue = 4
	sub, e := b.subscribe("chat", 1)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 10; i++ {
		b.dispatch(Event{Kind: "chat", Event: "typing", RoomID: 1, ActorID: 9})
	}
	select {
	case <-sub.done:
		t.Fatal("typing overflow evicted the socket")
	default:
	}
	if _, ok := b.subscribers[sub]; !ok || len(sub.events) != 2 {
		t.Fatal("typing kept no headroom for durable events", ok, len(sub.events))
	}
	b.dispatch(Event{Kind: "chat", Event: "message", RoomID: 1, MessageID: 2})
	b.dispatch(Event{Kind: "chat", Event: "attachments", RoomID: 1, MessageID: 2})
	select {
	case <-sub.done:
		t.Fatal("durable events within the headroom evicted the socket")
	default:
	}
	b.dispatch(Event{Kind: "chat", Event: "message", RoomID: 1, MessageID: 3})
	select {
	case <-sub.done:
	default:
		t.Fatal("durable overflow retained the socket")
	}
	if _, ok := b.subscribers[sub]; ok {
		t.Fatal("evicted subscriber still registered")
	}
}
func TestTypingCoalescerWindowAndBound(t *testing.T) {
	clock := time.Unix(1_700_000_000, 0)
	c := newTypingCoalescer(typingWindow, 3)
	c.now = func() time.Time { return clock }
	if !c.allow(1, 7) || c.allow(1, 7) {
		t.Fatal("same room and actor not coalesced")
	}
	if !c.allow(1, 8) || !c.allow(2, 7) {
		t.Fatal("different actor or room suppressed")
	}
	clock = clock.Add(typingWindow)
	if !c.allow(1, 7) {
		t.Fatal("publish after the window suppressed")
	}
	for room := int64(10); room < 100; room++ {
		if !c.allow(room, 7) {
			t.Fatal("new actor refused because the coalescer was full", room)
		}
		if len(c.seen) > 3 {
			t.Fatal("coalescer grew past its bound", len(c.seen))
		}
	}
	if c.allow(99, 7) {
		t.Fatal("eviction dropped the newest entry")
	}
}
func TestTypingFanOutResolvesTyperOnce(t *testing.T) {
	b := NewBroker(nil)
	b.ready.Store(true)
	var lookups atomic.Int64
	adapter, e := PlainAdapter(b, PlainCallbacks{
		Authorize: func(context.Context, platform.Actor, int64) (bool, error) { return true, nil },
		Write:     func(context.Context, platform.Actor, int64, string, *int64) error { return nil },
		Typing: func(_ context.Context, a platform.Actor, _ int64) (map[string]any, error) {
			lookups.Add(1)
			return map[string]any{"author_id": a.ID, "author": "synthetic"}, nil
		},
		Post:        func(context.Context, platform.Actor, int64) (map[string]any, error) { return nil, nil },
		Attachments: func(context.Context, platform.Actor, int64) ([]any, error) { return []any{}, nil },
	})
	if e != nil {
		t.Fatal(e)
	}
	subs := []*subscription{}
	for i := 0; i < 3; i++ {
		sub, e := b.subscribe("chat", 1)
		if e != nil {
			t.Fatal(e)
		}
		subs = append(subs, sub)
	}
	viewer := platform.Actor{ID: 10, IsActive: true}
	for round := int64(1); round <= 2; round++ {
		b.dispatch(Event{Kind: "chat", Event: "typing", RoomID: 1, ActorID: 9})
		for _, sub := range subs {
			payload, e := adapter.Payload(context.Background(), viewer, <-sub.events)
			if e != nil || payload["author"] != "synthetic" || payload["author_id"] != int64(9) {
				t.Fatal("typing payload", payload, e)
			}
		}
		if lookups.Load() != round {
			t.Fatal("typer identity resolved per recipient instead of per notification", lookups.Load())
		}
	}
}
func TestInboundFramesMeteredBeforeAuthorization(t *testing.T) {
	b := NewBroker(nil)
	b.ready.Store(true)
	var authorizations atomic.Int64
	received := make(chan bool, 512)
	authority := func(context.Context, *http.Request) (platform.Actor, error) {
		authorizations.Add(1)
		return platform.Actor{ID: 7, IsActive: true}, nil
	}
	adapter := Adapter{Authorize: func(context.Context, platform.Actor, int64) (bool, error) { return true, nil }, Receive: func(_ context.Context, _ platform.Actor, _ int64, raw json.RawMessage, _ string) error {
		received <- typingFrame(raw)
		return nil
	}, Payload: func(context.Context, platform.Actor, Event) (map[string]any, error) { return nil, nil }}
	cfg := DefaultConfig()
	server, e := NewServer(b, authority, map[string]Adapter{"chat": adapter, "messaging": adapter}, cfg)
	if e != nil {
		t.Fatal(e)
	}
	// A frozen clock makes the bucket deterministic: no refill, no typing interval.
	frozen := time.Unix(1_700_000_000, 0)
	server.Now = func() time.Time { return frozen }
	mux := http.NewServeMux()
	server.Register(mux)
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws/chat/1/"
	conn, _, e := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {httpServer.URL}, "Authorization": {"Fixture"}}})
	if e != nil {
		t.Fatal(e)
	}
	defer conn.CloseNow()
	authorizations.Store(0)
	for i := 0; i < 200; i++ {
		if e = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"typing"}`)); e != nil {
			t.Fatal(e)
		}
	}
	if e = conn.Write(ctx, websocket.MessageText, []byte(`{"body":"synthetic"}`)); e != nil {
		t.Fatal(e)
	}
	typing := 0
	for message := false; !message; {
		select {
		case isTyping := <-received:
			if isTyping {
				typing++
			} else {
				message = true
			}
		case <-ctx.Done():
			t.Fatal("message after a typing flood was not processed")
		}
	}
	if typing != 1 || authorizations.Load() != 2 {
		t.Fatal("dropped typing frames reached authorization or the adapter", typing, authorizations.Load())
	}
	// Two tokens are spent; the remaining burst is accepted, then the socket closes.
	for i := 0; i < cfg.FrameBurst; i++ {
		if conn.Write(ctx, websocket.MessageText, []byte(`{"body":"synthetic"}`)) != nil {
			break
		}
	}
	_, _, e = conn.Read(ctx)
	if websocket.CloseStatus(e) != websocket.StatusPolicyViolation {
		t.Fatal("sustained frames beyond the bucket did not close with 1008", e)
	}
	if authorizations.Load() != int64(cfg.FrameBurst) {
		t.Fatal("abusive frame reached authorization", authorizations.Load())
	}
}
