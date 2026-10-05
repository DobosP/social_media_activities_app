// Package chat owns bounded live transport over durable domain rows. PostgreSQL
// notifications carry identifiers only; payloads are resolved after each viewer
// passes fresh session/domain authorization. No content is logged or queued here.
package chat

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
)

type Event struct {
	Kind      string `json:"kind"`
	Event     string `json:"event"`
	RoomID    int64  `json:"room_id"`
	MessageID int64  `json:"message_id,omitempty"`
	ActorID   int64  `json:"actor_id,omitempty"`
	Sender    string `json:"sender,omitempty"` // ephemeral connection nonce, never a session token.

	// shared is process-local and never marshaled: the local fan-out of one
	// typing notification shares a single resolution of the typer's identity.
	shared *typingResolution
}

// typingResolution runs the typer's current identity check once per local
// dispatch instead of once per recipient. The result depends only on the typer
// and room; every recipient's own read authorization still runs per delivery.
type typingResolution struct {
	once sync.Once
	info map[string]any
	err  error
}

func (e Event) typingIdentity(resolve func() (map[string]any, error)) (map[string]any, error) {
	if e.shared == nil {
		return resolve()
	}
	e.shared.once.Do(func() { e.shared.info, e.shared.err = resolve() })
	return e.shared.info, e.shared.err
}

// transient reports a best-effort typing signal: dropping one never requires a
// durable-history reload, unlike message and attachment events.
func transient(e Event) bool { return e.Kind == "chat" && e.Event == "typing" }

func ValidEvent(e Event) bool {
	return (e.Kind == "chat" || e.Kind == "messaging") && e.RoomID > 0 && (e.Event == "message" && e.MessageID > 0 || e.Kind == "chat" && e.Event == "attachments" && e.MessageID > 0 || e.Kind == "chat" && e.Event == "typing" && e.ActorID > 0 && len(e.Sender) <= 64)
}
func Channel(schema string) string {
	return fmt.Sprintf("social_live_%x", sha256.Sum256([]byte(schema)))[:28]
}
func Publish(ctx context.Context, tx pgx.Tx, event Event) error {
	if !ValidEvent(event) {
		return fmt.Errorf("invalid live event")
	}
	var schema string
	if e := tx.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); e != nil {
		return e
	}
	b, e := json.Marshal(event)
	if e != nil || len(b) > 1024 {
		return fmt.Errorf("invalid live event")
	}
	_, e = tx.Exec(ctx, `SELECT pg_notify($1,$2)`, Channel(schema), string(b))
	return e
}
