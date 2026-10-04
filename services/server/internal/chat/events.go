// Package chat owns bounded live transport over durable domain rows. PostgreSQL
// notifications carry identifiers only; payloads are resolved after each viewer
// passes fresh session/domain authorization. No content is logged or queued here.
package chat

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type Event struct {
	Kind      string `json:"kind"`
	Event     string `json:"event"`
	RoomID    int64  `json:"room_id"`
	MessageID int64  `json:"message_id,omitempty"`
	ActorID   int64  `json:"actor_id,omitempty"`
	Sender    string `json:"sender,omitempty"` // ephemeral connection nonce, never a session token.
}

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
