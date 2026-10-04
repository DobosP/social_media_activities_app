package media

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *Service) VideoEnabled() bool {
	return s.ComposerCapabilities("adult").Videos
}

// AttachmentCapabilities describes selectable media, never upload authority.
// Publication still requires the current thread, scan and cohort gates.
type AttachmentCapabilities struct {
	Images, Files, Videos bool
	Accept                string
}

func (s *Service) ComposerCapabilities(cohort string) AttachmentCapabilities {
	if s == nil || (cohort != "adult" && cohort != "teen" && cohort != "child") {
		return AttachmentCapabilities{}
	}
	c := AttachmentCapabilities{Images: s.attachmentModeAllowed("image", cohort), Files: s.attachmentModeAllowed("file", cohort), Videos: s.attachmentModeAllowed("video", cohort)}
	types := []string{}
	if c.Images {
		types = append(types, "image/png", "image/jpeg", "image/webp")
	}
	if c.Files {
		types = append(types, "application/pdf")
	}
	if c.Videos {
		types = append(types, "video/mp4", "video/quicktime", "video/webm")
	}
	c.Accept = strings.Join(types, ",")
	return c
}

// DisappearanceOptions applies the same configured floor as attachmentExpiry,
// deduplicating choices that collapse to that floor. A kept attachment (zero or
// absent TTL) is separate from these positive disappearance choices.
func (s *Service) DisappearanceOptions(cohort string) []int64 {
	if s == nil || s.policy.Validate() != nil {
		return nil
	}
	floor := int64(s.policy.EphemeralMinTTL / time.Second)
	candidates := []int64{3600, 86400, 604800}
	switch cohort {
	case "adult":
	case "teen", "child":
		floor = int64(s.policy.EphemeralMinTTLMinors / time.Second)
		candidates = candidates[1:]
	default:
		return nil
	}
	options := make([]int64, 0, len(candidates))
	for _, value := range candidates {
		value = max(value, floor)
		if len(options) == 0 || options[len(options)-1] != value {
			options = append(options, value)
		}
	}
	return options
}

// ValidDisappearanceOption accepts current effective choices and the source
// choices from an already-rendered form. Admission still clamps any old choice
// to the current floor, so a policy change never weakens retention.
func (s *Service) ValidDisappearanceOption(cohort string, seconds int64) bool {
	options := s.DisappearanceOptions(cohort)
	if len(options) == 0 {
		return false
	}
	if seconds == 0 || seconds == 3600 || seconds == 86400 || seconds == 604800 {
		return true
	}
	for _, option := range options {
		if option == seconds {
			return true
		}
	}
	return false
}

// ProfileHash fingerprints stored approved images through the same bounded
// sandbox decoder as uploads, including WebP and AVIF. It grants no scan verdict
// or publication permission and performs no network or provider calls.
func (s *Service) ProfileHash(ctx context.Context, data []byte) (string, error) {
	if s == nil || s.processor == nil || len(data) == 0 || int64(len(data)) > s.processor.cfg.ImageMaxBytes {
		return "", ErrRejected
	}
	p := s.processor
	h, err := parseImageHeader(data)
	if err != nil || h.width < 1 || h.height < 1 || int64(h.width) > p.cfg.ImageMaxPixels/int64(h.height) {
		return "", ErrRejected
	}
	if err = p.acquire(ctx); err != nil {
		return "", err
	}
	defer p.release()
	dir, err := os.MkdirTemp(p.cfg.ScratchDir, "profile-hash-")
	if err != nil {
		return "", ErrProcessing
	}
	defer cleanupDir(dir)
	source, decoded := filepath.Join(dir, "source"), filepath.Join(dir, "decoded.png")
	if err = os.WriteFile(source, data, 0600); err != nil {
		return "", ErrProcessing
	}
	args := p.ffmpegBase(source)
	args = append(args, "-c:v", "png", "-threads", "1", decoded)
	if _, err = p.command(ctx, p.cfg.ProbeTimeout, p.cfg.FFmpeg, args...); err != nil {
		return "", err
	}
	im, err := decodePNG(decoded, max(h.width, h.height))
	if err != nil {
		return "", err
	}
	if im.Bounds().Dx() != h.width || im.Bounds().Dy() != h.height {
		return "", ErrRejected
	}
	return DHash(im), nil
}
func (s *Service) VideoMaxSeconds() float64 {
	if s == nil || s.processor == nil {
		return 0
	}
	return s.processor.cfg.VideoMaxSeconds
}
