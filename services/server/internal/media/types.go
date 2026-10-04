// Package media processes quarantined uploads. A successful manifest is evidence
// of processing, never permission to publish: the domain must still authorize,
// enforce cohort/retention rules and atomically commit its audit and object rows.
package media

import (
	"context"
	"errors"
	"time"
)

var (
	ErrRejected   = errors.New("media rejected")
	ErrBlocked    = errors.New("media blocked by safety screening")
	ErrThrottled  = errors.New("media upload rate exceeded")
	ErrScanner    = errors.New("media safety scanner unavailable or invalid")
	ErrProcessing = errors.New("media processing unavailable or failed")
	ErrObject     = errors.New("invalid private media object")
)

const PolicyVersion = "social-media-go-v1"

type Config struct {
	ScratchDir      string
	ImageMaxBytes   int64
	ImageMaxPixels  int64
	ImageMaxSide    int
	ThumbnailSide   int
	ImageFormat     string // AVIF or WEBP; native encoders are required, no silent fallback.
	VideoMaxBytes   int64
	VideoMaxSeconds float64
	VideoSourceSide int
	VideoTargetSide int
	VideoEnabled    bool
	CommandTimeout  time.Duration
	ProbeTimeout    time.Duration
	MemoryBytes     uint64
	Threads         int
	ConcurrentJobs  int
	FFmpeg          string
	FFprobe         string
	Avifenc         string
	Prlimit         string
}

func DefaultConfig(scratch string) Config {
	return Config{ScratchDir: scratch, ImageMaxBytes: 5 << 20, ImageMaxPixels: 30_000_000,
		ImageMaxSide: 2048, ThumbnailSide: 800, ImageFormat: "AVIF", VideoMaxBytes: 80 << 20,
		VideoMaxSeconds: 90, VideoSourceSide: 3840, VideoTargetSide: 1280, VideoEnabled: true,
		CommandTimeout: 600 * time.Second, ProbeTimeout: 60 * time.Second, MemoryBytes: 2 << 30,
		Threads: 2, ConcurrentJobs: 1, FFmpeg: "ffmpeg", FFprobe: "ffprobe", Avifenc: "avifenc", Prlimit: "prlimit"}
}

type ScanInput struct {
	SHA256         string
	PerceptualHash string
}
type Verdict struct{ Clean bool }
type Scanner interface {
	Scan(context.Context, ScanInput) (Verdict, error)
}
type DocumentScanner interface {
	ScanDocument(context.Context, string, int64) (Verdict, error)
}
type BlockedError struct{ SourceSHA256 string }

func (e *BlockedError) Error() string { return ErrBlocked.Error() }
func (e *BlockedError) Unwrap() error { return ErrBlocked }

type Artifact struct {
	Path        string `json:"-"` // private worker scratch; never serialized into public responses.
	SHA256      string `json:"sha256"`
	ContentType string `json:"content_type"`
	ByteSize    int64  `json:"byte_size"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
}
type Manifest struct {
	PolicyVersion    string    `json:"policy_version"`
	SourceSHA256     string    `json:"source_sha256"`
	SourceByteSize   int64     `json:"source_byte_size"`
	Kind             string    `json:"kind"`
	Status           string    `json:"status"` // ready only after all required scans.
	ScannerClean     bool      `json:"scanner_clean"`
	MetadataStripped bool      `json:"metadata_stripped"`
	PerceptualHash   string    `json:"perceptual_hash,omitempty"`
	DurationSeconds  float64   `json:"duration_seconds,omitempty"`
	Main             Artifact  `json:"main"`
	Thumbnail        *Artifact `json:"thumbnail,omitempty"`
	Poster           *Artifact `json:"poster,omitempty"`
	scratch          string
}

// Cleanup must be called after durable storage or after an abandoned transaction.
func (m *Manifest) Cleanup() error { return cleanupDir(m.scratch) }

// Store controls bytes only. Authorization, scanner state and expiry belong to
// the relational domain; presigning must happen only AFTER those checks.
type Store interface {
	Put(context.Context, string, string, string) error // key, local path, content type
	OpenRange(context.Context, string, int64, int64) ([]byte, error)
	Size(context.Context, string) (int64, error)
	Delete(context.Context, string) error
	PresignGet(context.Context, string, time.Duration, string, string) (string, error)
}
