package jobs

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
)

// The common five-minute duty ceiling is shorter than one transcode's command
// budget. A drain cut short there spends an attempt of a valid upload.
func TestVideoDrainDeadlineCoversItsLeaseBudget(t *testing.T) {
	config := DefaultConfig()
	config.Media = media.NewService(nil, nil, nil, media.TokenCodec{}, nil)
	r := New(nil, config)
	lease := media.DefaultPolicyConfig().VideoStaleProcessing
	if got := r.JobTimeout("transcode_videos", DueVideoBatch); got != DueVideoBatch*lease {
		t.Fatal("video drain timeout does not cover its claims", got)
	}
	if got := r.JobTimeout("transcode_videos", 0); got != lease {
		t.Fatal("video drain timeout lost its single-claim floor", got)
	}
	if got := r.JobTimeout("sync_event_feeds", DueVideoBatch); got != config.JobTimeout {
		t.Fatal("ordinary duty lost the common ceiling", got)
	}
	if got := New(nil, DefaultConfig()).JobTimeout("transcode_videos", DueVideoBatch); got != config.JobTimeout {
		t.Fatal("runner without media changed the common ceiling", got)
	}
	remaining := map[string]time.Duration{}
	for _, name := range DueNames {
		current := name
		r.handlers[name] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
			if deadline, ok := ctx.Deadline(); ok {
				remaining[current] = time.Until(deadline)
			}
			return 1, nil
		}
	}
	if _, err := r.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	command := media.DefaultConfig(t.TempDir()).CommandTimeout
	if remaining["transcode_videos"] <= command || remaining["purge_messaging"] <= 0 || remaining["purge_messaging"] > config.JobTimeout {
		t.Fatal("scheduled deadlines", remaining["transcode_videos"], remaining["purge_messaging"])
	}
}
