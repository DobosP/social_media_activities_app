package platform

import (
	"context"
	"io"
)

type uploadLimitKey struct{}

// File bytes remain streamed under media's separate cap. This request-local
// budget caps aggregate non-file form data retained by multipart adapters.
func WithDataUploadLimit(ctx context.Context, limit int64) context.Context {
	return context.WithValue(ctx, uploadLimitKey{}, limit)
}

type UploadBudget struct{ remaining int64 }

func NewUploadBudget(ctx context.Context) *UploadBudget {
	limit, ok := ctx.Value(uploadLimitKey{}).(int64)
	if !ok {
		limit = 8 << 20
	}
	if limit < 1 || limit > 8<<20 {
		limit = 0
	}
	return &UploadBudget{remaining: limit}
}
func (b *UploadBudget) ReadField(reader io.Reader, fieldCap int64) ([]byte, error) {
	if b.remaining < 0 || fieldCap < 1 {
		return nil, ErrInvalid
	}
	limit := min(b.remaining, fieldCap)
	raw, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, ErrInvalid
	}
	b.remaining -= int64(len(raw))
	return raw, nil
}
