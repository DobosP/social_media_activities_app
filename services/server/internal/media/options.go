package media

import (
	"context"
	"os"
	"path/filepath"
)

func (s *Service) VideoEnabled() bool {
	return s != nil && s.processor != nil && s.processor.cfg.VideoEnabled
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
