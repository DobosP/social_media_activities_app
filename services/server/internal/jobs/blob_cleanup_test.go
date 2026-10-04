package jobs

import (
	"context"
	"fmt"
	"github.com/DobosP/social_media_activities_app/services/server/internal/ops"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"testing"
)

func TestDeferredErasureNeverDropsKeysAfterChunk(t *testing.T) {
	r := jobFixture(t)
	r.Config.BlobBatch = 2
	deleted := map[string]int{}
	r.Config.DeleteBlob = func(_ context.Context, key string) error { deleted[key]++; return nil }
	keys := []string{}
	for i := 0; i < 7; i++ {
		keys = append(keys, fmt.Sprintf("synthetic/%d.avif", i))
	}
	ctx := context.Background()
	if err := platform.Transaction(ctx, r.DB, func(tx pgx.Tx) error {
		_, err := r.Queue.Enqueue(ctx, tx, "erasure.blob_cleanup", map[string]any{"blob_keys": keys}, ops.EnqueueOptions{})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	summary, err := r.Queue.RunPending(ctx, 10)
	if err != nil || summary.Failed != 0 || summary.Done != 4 {
		t.Fatal("chunk continuation lost", summary, err)
	}
	if len(deleted) != len(keys) {
		t.Fatal("physical erasure forgot keys", len(deleted))
	}
	for _, key := range keys {
		if deleted[key] != 1 {
			t.Fatal("wrong deletion count")
		}
	}
	var pending int
	if err := r.DB.QueryRow(ctx, `SELECT count(*) FROM ops_deferredtask WHERE status='PENDING'`).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("continuation stranded")
	}
}
