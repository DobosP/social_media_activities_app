package media

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestMediaPolicyFloorsModesExpiryAndTokenTTL(t *testing.T) {
	s := NewService(nil, nil, nil, TokenCodec{Key: []byte(strings.Repeat("s", 32))}, nil)
	c := DefaultPolicyConfig()
	c.SignedURLTTL = 10 * time.Second
	c.EphemeralMinTTL = 2 * time.Hour
	c.VideoCohorts = map[string]bool{}
	if err := s.ConfigurePolicy(c); err != nil {
		t.Fatal(err)
	}
	if !s.attachmentModeAllowed("file", "adult") || s.attachmentModeAllowed("file", "teen") || s.attachmentModeAllowed("video", "adult") {
		t.Fatal("cohort/mode policy")
	}
	ttl := int64(1)
	expires := s.attachmentExpiry("adult", &ttl)
	if expires == nil || time.Until(*expires) < 2*time.Hour-time.Second {
		t.Fatal("expiry floor ignored")
	}
	u, err := s.url("photo", 1, platform.Actor{ID: 1}, "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSuffix(strings.TrimPrefix(u, "/api/media/file/"), "/")
	if _, err = s.tokens.Verify(token, 1, time.Now().Add(11*time.Second)); err == nil {
		t.Fatal("configured signed TTL ignored")
	}
	c.AttachmentsEnabled = false
	if err = s.ConfigurePolicy(c); err != nil || s.attachmentModeAllowed("image", "adult") {
		t.Fatal("attachment kill switch", err)
	}
	for _, mutate := range []func(*PolicyConfig){func(c *PolicyConfig) { c.EphemeralMinTTLMinors = time.Hour }, func(c *PolicyConfig) { c.SignedURLTTL = 301 * time.Second }, func(c *PolicyConfig) { c.FileCohorts = map[string]bool{"child": true} }, func(c *PolicyConfig) { c.PerceptualProfileScanCap = 9999 }} {
		bad := DefaultPolicyConfig()
		mutate(&bad)
		if s.ConfigurePolicy(bad) == nil {
			t.Fatal("safety/privacy floor accepted")
		}
	}
}
func TestMediaEncodingPolicyUsesQualityAndRejectsPartialScanCoverage(t *testing.T) {
	c := DefaultConfig(t.TempDir())
	p, err := NewProcessor(c, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.avifQuantizer() != 23 || p.webpQuality() != 80 {
		t.Fatal("source defaults changed")
	}
	c.ImageQuality = 90
	c.VideoFrameScanInterval = time.Second
	c.VideoFrameScanMaxFrames = 90
	p, err = NewProcessor(c, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.avifQuantizer() != 6 || p.webpQuality() != 90 {
		t.Fatal("quality override ignored")
	}
	c.VideoFrameScanMaxFrames = 25
	if _, err = NewProcessor(c, nil, nil); err == nil {
		t.Fatal("partial video scan admitted")
	}
	c = DefaultConfig(t.TempDir())
	c.VideoFrameScanInterval = 6 * time.Second
	if _, err = NewProcessor(c, nil, nil); err == nil {
		t.Fatal("scan baseline weakened")
	}
	c = DefaultConfig(t.TempDir())
	c.VideoPreset = "veryslow"
	if _, err = NewProcessor(c, nil, nil); err == nil {
		t.Fatal("unbounded encoder preset")
	}
}
func TestNativeCodecNondefaultImageQualityAndAttachmentCap(t *testing.T) {
	for _, bin := range []string{"ffmpeg", "ffprobe", "prlimit"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip("native codec runtime absent")
		}
	}
	c := DefaultConfig(t.TempDir())
	c.ImageQuality = 90
	c.ImageFormat = "WEBP"
	c.AttachmentMaxBytes = 8
	p, err := NewProcessor(c, cleanScanner{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := p.ProcessImage(context.Background(), writeSource(t, syntheticPNG(t, 40, 30)))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Cleanup()
	if m.Main.Width != 40 || m.Main.Height != 30 || !m.ScannerClean {
		t.Fatal("configured codec lost contract")
	}
	if _, err = p.ProcessPDF(context.Background(), writeSource(t, []byte("%PDF-1.7 synthetic oversized"))); err != ErrRejected {
		t.Fatal("PDF byte cap ignored", err)
	}
}

func TestNativeCodecNondefaultVideoEncodingAndDenserScan(t *testing.T) {
	for _, bin := range []string{"ffmpeg", "ffprobe", "prlimit", "avifenc"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip("native codec runtime absent")
		}
	}
	c := DefaultConfig(t.TempDir())
	c.ImageQuality = 90
	c.VideoCRF = 30
	c.VideoPreset = "fast"
	c.VideoAudioBitrate = "64k"
	c.VideoFrameScanInterval = time.Second
	c.VideoFrameScanMaxFrames = 90
	p, err := NewProcessor(c, cleanScanner{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.ScratchDir, "nondefault.mp4")
	if _, err = p.command(context.Background(), 30*time.Second, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "color=c=blue:s=64x48:r=10", "-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=44100", "-t", "2.1", "-c:v", "libx264", "-c:a", "aac", "-threads", "1", path); err != nil {
		t.Fatal(err)
	}
	m, err := p.ProcessVideo(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Cleanup()
	if m.Status != "ready" || !m.ScannerClean || !m.MetadataStripped || m.Poster == nil || m.Poster.ContentType != "image/avif" {
		t.Fatal("configured video contract", m)
	}
	frames, err := filepath.Glob(filepath.Join(m.scratch, "scan_*.png"))
	if err != nil || len(frames) < 3 {
		t.Fatal("denser scan interval ignored", len(frames), err)
	}
}
