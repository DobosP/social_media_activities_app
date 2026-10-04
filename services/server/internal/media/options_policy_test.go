package media

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestComposerCapabilitiesFollowCohortPolicyAndCodecSwitch(t *testing.T) {
	for _, tc := range []struct {
		name, cohort                               string
		attachments, files, videos, codec          bool
		imagesAllowed, filesAllowed, videosAllowed bool
	}{
		{"adult_defaults", "adult", true, true, true, true, true, true, true},
		{"child_images", "child", true, true, true, true, true, false, false},
		{"teen_images", "teen", true, true, true, true, true, false, false},
		{"all_disabled", "adult", false, true, true, true, false, false, false},
		{"file_disabled", "adult", true, false, true, true, true, false, true},
		{"video_disabled", "adult", true, true, false, true, true, true, false},
		{"codec_disabled", "adult", true, true, true, false, true, true, false},
		{"unknown_cohort", "unassigned", true, true, true, true, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig(t.TempDir())
			cfg.VideoEnabled = tc.codec
			processor, err := NewProcessor(cfg, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			s := NewService(nil, processor, nil, TokenCodec{}, nil)
			policy := DefaultPolicyConfig()
			policy.AttachmentsEnabled = tc.attachments
			policy.FileCohorts = map[string]bool{"adult": tc.files}
			policy.VideoCohorts = map[string]bool{"adult": tc.videos}
			if err := s.ConfigurePolicy(policy); err != nil {
				t.Fatal(err)
			}
			c := s.ComposerCapabilities(tc.cohort)
			if c.Images != tc.imagesAllowed || c.Files != tc.filesAllowed || c.Videos != tc.videosAllowed {
				t.Fatal("misleading composer capabilities", c)
			}
			if strings.Contains(c.Accept, "image/") != tc.imagesAllowed || strings.Contains(c.Accept, "application/pdf") != tc.filesAllowed || strings.Contains(c.Accept, "video/") != tc.videosAllowed {
				t.Fatal("MIME choices disagree with capabilities", c)
			}
			if tc.cohort == "adult" && s.VideoEnabled() != tc.videosAllowed {
				t.Fatal("VideoEnabled ignored policy")
			}
		})
	}
	var missing *Service
	if c := missing.ComposerCapabilities("adult"); c.Images || c.Files || c.Videos || c.Accept != "" || missing.VideoEnabled() {
		t.Fatal("missing service advertised uploads", c)
	}
}

func TestDisappearanceChoicesUseEffectiveFloorAndPreserveKeep(t *testing.T) {
	for _, tc := range []struct {
		name, cohort string
		adult, minor time.Duration
		want         []int64
	}{
		{"adult_defaults", "adult", time.Hour, 24 * time.Hour, []int64{3600, 86400, 604800}},
		{"child_defaults", "child", time.Hour, 24 * time.Hour, []int64{86400, 604800}},
		{"teen_defaults", "teen", time.Hour, 24 * time.Hour, []int64{86400, 604800}},
		{"adult_two_hours", "adult", 2 * time.Hour, 24 * time.Hour, []int64{7200, 86400, 604800}},
		{"adult_one_day_dedup", "adult", 24 * time.Hour, 24 * time.Hour, []int64{86400, 604800}},
		{"minor_two_days", "child", time.Hour, 48 * time.Hour, []int64{172800, 604800}},
		{"minor_one_week_dedup", "teen", time.Hour, 7 * 24 * time.Hour, []int64{604800}},
		{"exact_seconds", "adult", 5401 * time.Second, 24 * time.Hour, []int64{5401, 86400, 604800}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewService(nil, nil, nil, TokenCodec{}, nil)
			policy := DefaultPolicyConfig()
			policy.EphemeralMinTTL = tc.adult
			policy.EphemeralMinTTLMinors = tc.minor
			if err := s.ConfigurePolicy(policy); err != nil {
				t.Fatal(err)
			}
			options := s.DisappearanceOptions(tc.cohort)
			if !reflect.DeepEqual(options, tc.want) {
				t.Fatal("choices below floor or duplicated", options, tc.want)
			}
			for _, seconds := range options {
				now := time.Now()
				expiry := s.attachmentExpiry(tc.cohort, &seconds)
				if expiry == nil || expiry.Before(now.Add(time.Duration(seconds)*time.Second)) || expiry.After(time.Now().Add(time.Duration(seconds)*time.Second)) {
					t.Fatal("offered choice changed at admission", seconds, expiry)
				}
				if !s.ValidDisappearanceOption(tc.cohort, seconds) {
					t.Fatal("offered choice cannot roundtrip", seconds)
				}
			}
			zero := int64(0)
			if s.attachmentExpiry(tc.cohort, nil) != nil || s.attachmentExpiry(tc.cohort, &zero) != nil || !s.ValidDisappearanceOption(tc.cohort, zero) {
				t.Fatal("keep gained an expiry")
			}
			for _, old := range []int64{3600, 86400, 604800} {
				if !s.ValidDisappearanceOption(tc.cohort, old) {
					t.Fatal("already rendered source choice was refused")
				}
			}
			if s.ValidDisappearanceOption(tc.cohort, -1) || s.ValidDisappearanceOption(tc.cohort, 17) {
				t.Fatal("non-choice admitted")
			}
		})
	}
	var missing *Service
	if len(missing.DisappearanceOptions("adult")) != 0 || missing.ValidDisappearanceOption("adult", 3600) {
		t.Fatal("missing policy supplied choices")
	}
	s := NewService(nil, nil, nil, TokenCodec{}, nil)
	if len(s.DisappearanceOptions("unassigned")) != 0 || s.ValidDisappearanceOption("unassigned", 3600) {
		t.Fatal("unknown cohort supplied choices")
	}
}
