package media

import (
	"fmt"
	"time"
)

// PolicyConfig is set once before serving. Empty cohort maps disable their mode;
// adult is the immutable ceiling for file/video admission.
type PolicyConfig struct {
	AvatarUploadLimit, PerceptualProfileScanCap                        int
	AvatarUploadWindow                                                 time.Duration
	SignedURLTTL, PresignedTTL, EphemeralMinTTL, EphemeralMinTTLMinors time.Duration
	VideoMaxAttempts                                                   int
	VideoStaleProcessing                                               time.Duration
	AttachmentsEnabled                                                 bool
	FileCohorts, VideoCohorts                                          map[string]bool
}

func DefaultPolicyConfig() PolicyConfig {
	return PolicyConfig{AvatarUploadLimit: 20, AvatarUploadWindow: time.Hour, PerceptualProfileScanCap: 10000, SignedURLTTL: 300 * time.Second, PresignedTTL: 60 * time.Second,
		EphemeralMinTTL: time.Hour, EphemeralMinTTLMinors: 24 * time.Hour, VideoMaxAttempts: 3,
		VideoStaleProcessing: 1800 * time.Second, AttachmentsEnabled: true,
		FileCohorts: map[string]bool{"adult": true}, VideoCohorts: map[string]bool{"adult": true}}
}
func (c PolicyConfig) Validate() error {
	for _, v := range []struct {
		name        string
		n, min, max time.Duration
	}{
		{"MEDIA_SIGNED_URL_TTL", c.SignedURLTTL, time.Second, 300 * time.Second}, {"MEDIA_PRESIGNED_TTL", c.PresignedTTL, time.Second, 60 * time.Second},
		{"MEDIA_EPHEMERAL_MIN_TTL_SECONDS", c.EphemeralMinTTL, time.Hour, 24 * time.Hour}, {"MEDIA_EPHEMERAL_MIN_TTL_MINORS_SECONDS", c.EphemeralMinTTLMinors, 24 * time.Hour, 7 * 24 * time.Hour},
		{"MEDIA_VIDEO_STALE_PROCESSING_SECONDS", c.VideoStaleProcessing, time.Second, 1800 * time.Second}} {
		if v.n < v.min || v.n > v.max {
			return fmt.Errorf("%s is outside native bounds", v.name)
		}
	}
	if c.AvatarUploadLimit < 1 || c.AvatarUploadLimit > 10000 {
		return fmt.Errorf("AVATAR_UPLOAD_RATE_LIMIT is outside native bounds")
	}
	if c.AvatarUploadWindow < time.Second || c.AvatarUploadWindow > 7*24*time.Hour {
		return fmt.Errorf("AVATAR_UPLOAD_RATE_WINDOW_SECONDS is outside native bounds")
	}
	if c.PerceptualProfileScanCap < 10000 || c.PerceptualProfileScanCap > 100000 {
		return fmt.Errorf("MEDIA_PERCEPTUAL_PROFILE_SCAN_CAP cannot lower safety coverage")
	}
	if c.VideoMaxAttempts < 1 || c.VideoMaxAttempts > 10 {
		return fmt.Errorf("MEDIA_VIDEO_MAX_ATTEMPTS is outside native bounds")
	}
	for name, cohorts := range map[string]map[string]bool{"MEDIA_FILE_COHORTS": c.FileCohorts, "MEDIA_VIDEO_COHORTS": c.VideoCohorts} {
		for cohort, enabled := range cohorts {
			if enabled && cohort != "adult" {
				return fmt.Errorf("%s permits only adult or empty", name)
			}
		}
	}
	return nil
}
func (s *Service) ConfigurePolicy(c PolicyConfig) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if s.processor != nil && c.VideoStaleProcessing <= max(s.processor.cfg.CommandTimeout, s.processor.cfg.ProbeTimeout) {
		return fmt.Errorf("MEDIA_VIDEO_STALE_PROCESSING_SECONDS must exceed the longest bounded codec phase")
	}
	c.FileCohorts = map[string]bool{"adult": c.FileCohorts["adult"]}
	c.VideoCohorts = map[string]bool{"adult": c.VideoCohorts["adult"]}
	s.policy = c
	return nil
}
func (s *Service) attachmentModeAllowed(kind, cohort string) bool {
	if !s.policy.AttachmentsEnabled {
		return false
	}
	if kind == "image" {
		return true
	}
	if cohort != "adult" {
		return false
	}
	if kind == "file" {
		return s.policy.FileCohorts[cohort]
	}
	return kind == "video" && s.policy.VideoCohorts[cohort] && s.processor != nil && s.processor.cfg.VideoEnabled
}
func (s *Service) attachmentExpiry(cohort string, ttl *int64) *time.Time {
	return expiryWithPolicy(s.policy, cohort, ttl)
}
func (s *Service) AttachmentMaxBytes() int64 {
	if s == nil || s.processor == nil {
		return 0
	}
	return s.processor.cfg.AttachmentMaxBytes
}
