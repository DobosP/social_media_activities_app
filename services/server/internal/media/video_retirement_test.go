package media

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// This matrix ports independent malformed ffprobe documents from the legacy
// video-pipeline regressions. It never invokes a codec or a live scanner.
func TestRetirementVideoProbeMatrix(t *testing.T) {
	valid := func() map[string]any {
		return map[string]any{"format": map[string]any{"format_name": "mov,mp4,m4a,3gp,3g2,mj2", "duration": "10.5"}, "streams": []any{
			map[string]any{"codec_type": "video", "codec_name": "h264", "pix_fmt": "yuv420p", "width": 640, "height": 480},
			map[string]any{"codec_type": "audio", "codec_name": "aac"},
		}}
	}
	video := func(q map[string]any) map[string]any { return q["streams"].([]any)[0].(map[string]any) }
	audio := func(q map[string]any) map[string]any { return q["streams"].([]any)[1].(map[string]any) }
	for _, scenario := range []struct {
		name     string
		change   func(map[string]any)
		allow    bool
		duration float64
	}{
		{"ok_case_returns_correct_fields", func(map[string]any) {}, true, 10.5},
		{"disallowed_container_rejected", func(q map[string]any) { q["format"].(map[string]any)["format_name"] = "avi" }, false, 0},
		{"two_video_streams_rejected", func(q map[string]any) { q["streams"] = append(q["streams"].([]any), video(q)) }, false, 0},
		{"attached_pic_cover_is_ignored", func(q map[string]any) {
			q["streams"] = append(q["streams"].([]any), map[string]any{"codec_type": "video", "codec_name": "mjpeg", "pix_fmt": "yuvj420p", "width": 200, "height": 200, "duration": "0", "disposition": map[string]any{"attached_pic": 1}})
		}, true, 10.5},
		{"two_audio_streams_rejected", func(q map[string]any) { q["streams"] = append(q["streams"].([]any), audio(q)) }, false, 0},
		{"disallowed_video_codec_rejected", func(q map[string]any) { video(q)["codec_name"] = "prores" }, false, 0},
		{"disallowed_audio_codec_rejected", func(q map[string]any) { audio(q)["codec_name"] = "flac" }, false, 0},
		{"disallowed_pix_fmt_rejected", func(q map[string]any) { video(q)["pix_fmt"] = "rgb48le" }, false, 0},
		{"width_over_max_side_rejected", func(q map[string]any) { video(q)["width"] = 2000 }, false, 0},
		{"height_over_max_side_rejected", func(q map[string]any) { video(q)["height"] = 2000 }, false, 0},
		{"zero_duration_rejected", func(q map[string]any) { q["format"].(map[string]any)["duration"] = "0"; video(q)["duration"] = "0" }, false, 0},
		{"missing_duration_key_rejected", func(q map[string]any) { delete(q["format"].(map[string]any), "duration") }, false, 0},
		{"duration_falls_back_to_stream_duration", func(q map[string]any) {
			delete(q["format"].(map[string]any), "duration")
			video(q)["duration"] = "7.25"
		}, true, 7.25},
		{"duration_over_max_rejected", func(q map[string]any) { q["format"].(map[string]any)["duration"] = "200" }, false, 0},
		{"subtitle_track_rejected", func(q map[string]any) {
			q["streams"] = append(q["streams"].([]any), map[string]any{"codec_type": "subtitle"})
		}, false, 0},
		{"absent_video_track_rejected", func(q map[string]any) { q["streams"] = []any{audio(q)} }, false, 0},
		{"zero_width_rejected", func(q map[string]any) { video(q)["width"] = 0 }, false, 0},
		{"negative_height_rejected", func(q map[string]any) { video(q)["height"] = -1 }, false, 0},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			document := valid()
			scenario.change(document)
			raw, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			w, h, duration, err := validateProbe(raw, 1280, 90)
			if scenario.allow {
				if err != nil || w != 640 || h != 480 || duration != scenario.duration {
					t.Fatalf("accepted probe fields = %d/%d/%g, error=%v", w, h, duration, err)
				}
			} else {
				if !errors.Is(err, ErrRejected) || w != 0 || h != 0 || duration != 0 {
					t.Fatal("malformed probe retained partial fields or was admitted")
				}
				if len(err.Error()) >= 200 || strings.Contains(err.Error(), "Traceback") || strings.Contains(err.Error(), "/private/") {
					t.Fatal("probe rejection exposed internal diagnostics")
				}
			}
		})
	}
}

func TestRetirementEphemeralPermanentAndFloorCases(t *testing.T) {
	for _, ttl := range []*int64{nil, retirementTTL(0), retirementTTL(-1)} {
		for _, cohort := range []string{"adult", "teen", "child"} {
			if expiry(cohort, ttl) != nil {
				t.Fatal("unset/nonpositive TTL became immediate expiry")
			}
		}
	}
	for _, scenario := range []struct {
		cohort     string
		ttl, floor int64
	}{{"adult", 7200, 7200}, {"child", 3600, 86400}, {"teen", 3600, 86400}} {
		now := time.Now()
		expires := expiry(scenario.cohort, &scenario.ttl)
		if expires == nil || expires.Before(now.Add(time.Duration(scenario.floor)*time.Second)) || expires.After(now.Add(time.Duration(scenario.floor)*time.Second+time.Second)) {
			t.Fatal("expiry did not honor requested adult TTL or minor floor")
		}
	}
}

func retirementTTL(value int64) *int64 { return &value }
