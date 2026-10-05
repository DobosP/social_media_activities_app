package ops

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

type dueRunIDKey struct{}

// WithDueRunID creates one opaque operator correlation ID in a child context.
// No incoming request, user, payload or error material contributes to it.
func WithDueRunID(ctx context.Context) context.Context {
	var fresh [16]byte
	_, _ = rand.Read(fresh[:])
	return context.WithValue(ctx, dueRunIDKey{}, "job:run_due_jobs:"+hex.EncodeToString(fresh[:]))
}

// DueRunID is available to every handler in the same due run. Outside that
// child context it returns the source-compatible uncorrelated marker.
func DueRunID(ctx context.Context) string {
	if id, ok := ctx.Value(dueRunIDKey{}).(string); ok {
		return id
	}
	return "-"
}
