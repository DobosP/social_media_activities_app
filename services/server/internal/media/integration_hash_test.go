package media

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeProfileHashPNGWebPAVIFAndBounds(t *testing.T) {
	for _, bin := range []string{"ffmpeg", "prlimit", "avifenc"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip("native codec runtime not installed")
		}
	}
	p, err := NewProcessor(DefaultConfig(t.TempDir()), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(nil, p, nil, TokenCodec{}, nil)
	ctx := context.Background()
	im := image.NewNRGBA(image.Rect(0, 0, 100, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 100; x++ {
			im.SetNRGBA(x, y, color.NRGBA{uint8((x*17 + y*3 + 93) % 256), uint8((x*5 + y*19 + 186) % 256), uint8((x*y + 93*23) % 256), uint8((x*7 + y*11 + 93) % 256)})
		}
	}
	var encoded bytes.Buffer
	if err = png.Encode(&encoded, im); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ProfileHash(ctx, encoded.Bytes()); err != nil || got != "98e6b05445614d33" {
		t.Fatal("Pillow PNG fingerprint golden", got, err)
	}
	// An opaque image avoids encoder-specific invisible RGB normalization.
	for y := 0; y < 80; y++ {
		for x := 0; x < 100; x++ {
			c := im.NRGBAAt(x, y)
			c.A = 255
			im.SetNRGBA(x, y, c)
		}
	}
	encoded.Reset()
	if err = png.Encode(&encoded, im); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "source.png")
	if err = os.WriteFile(source, encoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"webp", "avif"} {
		path := filepath.Join(t.TempDir(), "profile."+format)
		if format == "webp" {
			_, err = p.command(ctx, 30*time.Second, p.cfg.FFmpeg, "-v", "error", "-y", "-i", source, "-c:v", "libwebp", "-lossless", "1", "-threads", "1", path)
		} else {
			_, err = p.command(ctx, 30*time.Second, p.cfg.Avifenc, "--lossless", "--jobs", "1", source, path)
		}
		if err != nil {
			t.Fatal("synthetic codec fixture", format, err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := s.ProfileHash(ctx, raw); err != nil || got != "98e6b05445614d33" {
			t.Fatal("lossless codec fingerprint golden", format, got, err)
		}
	}
	for _, raw := range [][]byte{nil, []byte("%PDF-invalid"), bytes.Repeat([]byte{1}, int(p.cfg.ImageMaxBytes)+1)} {
		if _, err = s.ProfileHash(ctx, raw); err == nil {
			t.Fatal("nonimage/unbounded fingerprint accepted")
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.ProfileHash(cancelled, encoded.Bytes()); err == nil {
		t.Fatal("cancelled fingerprint continued")
	}
	entries, err := os.ReadDir(p.cfg.ScratchDir)
	if err != nil || len(entries) != 0 {
		t.Fatal("fingerprint scratch leaked", len(entries), err)
	}
}
