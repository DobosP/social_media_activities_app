package main

import (
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/jobs"
	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
)

func TestTranscodeJobTimeoutScalesWithItsLimit(t *testing.T) {
	config := jobs.DefaultConfig()
	config.Media = media.NewService(nil, nil, nil, media.TokenCodec{}, nil)
	runner := jobs.New(nil, config)
	lease := media.DefaultPolicyConfig().VideoStaleProcessing
	five := 5
	for _, tc := range []struct {
		call invocation
		want time.Duration
	}{
		{invocation{Name: "transcode_videos"}, jobs.DueVideoBatch * lease},
		{invocation{Name: "transcode_videos", Limit: &five}, 5 * lease},
		{invocation{Name: "purge_expired_attachments", Limit: &five}, config.JobTimeout},
		{invocation{Name: "lift_suspensions"}, config.JobTimeout},
	} {
		if got := tc.call.timeout(runner); got != tc.want {
			t.Fatal("job deadline", tc.call.Name, got, tc.want)
		}
	}
}
