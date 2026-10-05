package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Processor struct {
	cfg       Config
	scanner   Scanner
	documents DocumentScanner
	jobs      chan struct{} // image/PDF/profile-hash slots, bounded wait.
	videoJobs chan struct{} // minutes-long transcodes never hold a request-path slot.
}

func NewProcessor(c Config, scanner Scanner, documents DocumentScanner) (*Processor, error) {
	if c.ScratchDir == "" || c.ImageMaxBytes < 1 || c.ImageMaxBytes > 5<<20 || c.ImageMaxPixels < 1 || c.ImageMaxPixels > 30_000_000 || c.ImageMaxSide < 1 || c.ImageMaxSide > 2048 || c.ThumbnailSide < 1 || c.ThumbnailSide > 800 || c.VideoMaxBytes < 1 || c.VideoMaxBytes > 80<<20 || c.VideoMaxSeconds <= 0 || c.VideoMaxSeconds > 90 || c.VideoSourceSide < 1 || c.VideoSourceSide > 3840 || c.VideoTargetSide < 2 || c.VideoTargetSide > 1280 || c.Threads < 1 || c.Threads > 2 || c.ConcurrentJobs < 1 || c.ConcurrentJobs > 4 || c.ConcurrentVideoJobs < 1 || c.ConcurrentVideoJobs > 4 || c.ImageQueueWait <= 0 || c.ImageQueueWait > 60*time.Second || c.MemoryBytes < 64<<20 || c.MemoryBytes > 2<<30 || c.CommandTimeout <= 0 || c.CommandTimeout > 600*time.Second || c.ProbeTimeout <= 0 || c.ProbeTimeout > 60*time.Second {
		return nil, ErrRejected
	}
	if err := c.ValidateEncodingPolicy(); err != nil {
		return nil, err
	}
	if c.ImageFormat != "AVIF" && c.ImageFormat != "WEBP" {
		return nil, ErrRejected
	}
	if err := os.MkdirAll(c.ScratchDir, 0700); err != nil {
		return nil, ErrProcessing
	}
	return &Processor{cfg: c, scanner: scanner, documents: documents, jobs: make(chan struct{}, c.ConcurrentJobs), videoJobs: make(chan struct{}, c.ConcurrentVideoJobs)}, nil
}

// CheckRuntime belongs to application startup when uploads/video are enabled.
func (p *Processor) CheckRuntime() error {
	tools := []string{p.cfg.FFmpeg, p.cfg.Prlimit}
	if p.cfg.VideoEnabled {
		tools = append(tools, p.cfg.FFprobe)
	}
	if p.cfg.ImageFormat == "AVIF" {
		tools = append(tools, p.cfg.Avifenc)
	}
	for _, bin := range tools {
		if _, e := exec.LookPath(bin); e != nil {
			return ErrProcessing
		}
	}
	return nil
}
func cleanupDir(dir string) error {
	if dir == "" {
		return nil
	}
	return os.RemoveAll(dir)
}

// acquire admits request-path image/PDF work. A full queue is a retryable
// ErrBusy after ImageQueueWait, never an unbounded wait holding an upload.
func (p *Processor) acquire(ctx context.Context) error {
	wait := time.NewTimer(p.cfg.ImageQueueWait)
	defer wait.Stop()
	select {
	case p.jobs <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-wait.C:
		return ErrBusy
	}
}
func (p *Processor) release() { <-p.jobs }

// acquireVideo is off-request: it waits on its own slots for the caller's context.
func (p *Processor) acquireVideo(ctx context.Context) error {
	select {
	case p.videoJobs <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (p *Processor) releaseVideo() { <-p.videoJobs }

func (p *Processor) scannerEffective() bool {
	if p == nil || p.scanner == nil {
		return false
	}
	if scanner, ok := p.scanner.(interface{ Effective() bool }); ok {
		return scanner.Effective()
	}
	return true
}

func (p *Processor) stage(path string, max int64) (dir, source, digest string, size int64, err error) {
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return "", "", "", 0, ErrRejected
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > max {
		return "", "", "", 0, ErrRejected
	}
	dir, e = os.MkdirTemp(p.cfg.ScratchDir, "media-")
	if e != nil {
		return "", "", "", 0, ErrProcessing
	}
	source = filepath.Join(dir, "source")
	dst, e := os.OpenFile(source, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		cleanupDir(dir)
		return "", "", "", 0, ErrProcessing
	}
	h := sha256.New()
	size, e = io.Copy(io.MultiWriter(dst, h), io.LimitReader(f, max+1))
	ce := dst.Close()
	if e != nil || ce != nil || size != st.Size() || size > max {
		cleanupDir(dir)
		return "", "", "", 0, ErrRejected
	}
	return dir, source, hex.EncodeToString(h.Sum(nil)), size, nil
}

type limitedBuffer struct {
	b   bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(v []byte) (int, error) {
	if len(v) > b.max-b.b.Len() {
		return 0, ErrProcessing
	}
	return b.b.Write(v)
}

// Commands are fixed argv with no shell. prlimit applies BEFORE exec; a new
// process group is killed as a unit at timeout/cancellation. The serving image
// must additionally drop caps, disable networking for codec workers and use cgroups.
func (p *Processor) command(ctx context.Context, timeout time.Duration, bin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cpu := int(math.Ceil(timeout.Seconds()))
	argv := []string{fmt.Sprintf("--as=%d", p.cfg.MemoryBytes), fmt.Sprintf("--cpu=%d", cpu), "--", bin}
	argv = append(argv, args...)
	cmd := exec.CommandContext(ctx, p.cfg.Prlimit, argv...)
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C", "OMP_NUM_THREADS=2", "OPENBLAS_NUM_THREADS=1"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = time.Second
	out := &limitedBuffer{max: 1 << 20}
	stderr := &limitedBuffer{max: 16 << 10}
	cmd.Stdout = out
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return nil, ErrProcessing
	}
	return out.b.Bytes(), nil
}
func (p *Processor) scan(ctx context.Context, digest, phash string) error {
	if p.scanner == nil {
		return ErrScanner
	}
	v, err := p.scanner.Scan(ctx, ScanInput{SHA256: digest, PerceptualHash: phash})
	if err != nil {
		return ErrScanner
	}
	if !v.Clean {
		return &BlockedError{SourceSHA256: digest}
	}
	return nil
}
func (p *Processor) ffmpegBase(source string) []string {
	return []string{"-v", "error", "-y", "-nostdin", "-max_alloc", "134217728", "-protocol_whitelist", "file", "-codec_whitelist", "png,mjpeg,webp,av1,libdav1d,libaom-av1", "-threads", strconv.Itoa(p.cfg.Threads), "-filter_threads", "1", "-noautorotate", "-max_pixels", strconv.FormatInt(p.cfg.ImageMaxPixels, 10), "-i", source, "-map", "0:v:0", "-frames:v", "1", "-map_metadata", "-1", "-map_chapters", "-1"}
}
func rotationFilter(o int) string {
	switch o {
	case 2:
		return "hflip,"
	case 3:
		return "hflip,vflip,"
	case 4:
		return "vflip,"
	case 5:
		return "transpose=1,hflip,"
	case 6:
		return "transpose=1,"
	case 7:
		return "transpose=2,hflip,"
	case 8:
		return "transpose=2,"
	}
	return ""
}

func (p *Processor) ProcessImage(ctx context.Context, path string) (m Manifest, err error) {
	if err = p.acquire(ctx); err != nil {
		return m, err
	}
	defer p.release()
	dir, source, digest, size, err := p.stage(path, p.cfg.ImageMaxBytes)
	if err != nil {
		return m, err
	}
	defer func() {
		if err != nil {
			cleanupDir(dir)
		}
	}()
	b, err := os.ReadFile(source)
	if err != nil {
		return m, ErrProcessing
	}
	h, err := parseImageHeader(b)
	if err != nil || h.width <= 0 || h.height <= 0 || int64(h.width) > p.cfg.ImageMaxPixels/int64(h.height) {
		return m, ErrRejected
	}
	// Screen ORIGINAL bytes before any derivative is admitted. A later perceptual
	// scan screens canonical pixels; failures leave no durable approved object.
	if err = p.scan(ctx, digest, ""); err != nil {
		return m, err
	}
	m, err = p.imageArtifacts(ctx, dir, source, h.orientation, true)
	if err != nil {
		return m, err
	}
	if err = p.scan(ctx, digest, m.PerceptualHash); err != nil {
		return m, err
	}
	m.SourceSHA256 = digest
	m.SourceByteSize = size
	m.Kind = "image"
	m.Status = "ready"
	m.ScannerClean = true
	m.MetadataStripped = true
	m.PolicyVersion = PolicyVersion
	m.scratch = dir
	return m, nil
}

func (p *Processor) imageArtifacts(ctx context.Context, dir, source string, orientation int, thumbnail bool) (m Manifest, err error) {
	canonical := filepath.Join(dir, "canonical.png")
	filter := rotationFilter(orientation) + fmt.Sprintf("scale=min(%d\\,iw):min(%d\\,ih):force_original_aspect_ratio=decrease", p.cfg.ImageMaxSide, p.cfg.ImageMaxSide)
	args := p.ffmpegBase(source)
	// JPEG/PNG/WebP EXIF transforms are parsed before decoding and applied once.
	// With no explicit EXIF transform, retain FFmpeg's native display-matrix
	// handling (including AVIF irot/imir); canonical derivatives contain neither.
	if orientation == 1 {
		for i, x := range args {
			if x == "-noautorotate" {
				args = append(args[:i], args[i+1:]...)
				break
			}
		}
	}
	args = append(args, "-vf", filter, "-c:v", "png", "-threads", "1", canonical)
	if _, err = p.command(ctx, p.cfg.ProbeTimeout, p.cfg.FFmpeg, args...); err != nil {
		return m, err
	}
	// Reconstruct from decoded pixels to discard even ancillary PNG/ICC/text data.
	im, e := decodePNG(canonical, p.cfg.ImageMaxSide)
	if e != nil {
		return m, ErrProcessing
	}
	f, e := os.OpenFile(canonical, os.O_TRUNC|os.O_WRONLY, 0600)
	if e != nil {
		return m, ErrProcessing
	}
	e = png.Encode(f, im)
	ce := f.Close()
	if e != nil || ce != nil {
		return m, ErrProcessing
	}
	m.Main, err = p.encode(ctx, dir, canonical, "main", im.Bounds().Dx(), im.Bounds().Dy())
	if err != nil {
		return m, err
	}
	// The reference fingerprints and thumbnail source are the stored full
	// derivative, so re-decode it before either operation; lossy encoders may
	// move a low-contrast edge by a few levels.
	args = p.ffmpegBase(m.Main.Path)
	args = append(args, "-c:v", "png", "-threads", "1", canonical)
	if _, err = p.command(ctx, p.cfg.ProbeTimeout, p.cfg.FFmpeg, args...); err != nil {
		return m, err
	}
	im, e = decodePNG(canonical, p.cfg.ImageMaxSide)
	if e != nil {
		return m, ErrProcessing
	}
	if thumbnail && max(im.Bounds().Dx(), im.Bounds().Dy()) > p.cfg.ThumbnailSide {
		thumb := filepath.Join(dir, "thumb.png")
		args = p.ffmpegBase(canonical)
		args = append(args, "-vf", fmt.Sprintf("scale=min(%d\\,iw):min(%d\\,ih):force_original_aspect_ratio=decrease", p.cfg.ThumbnailSide, p.cfg.ThumbnailSide), "-c:v", "png", "-threads", "1", thumb)
		if _, err = p.command(ctx, p.cfg.ProbeTimeout, p.cfg.FFmpeg, args...); err != nil {
			return m, err
		}
		b, e := os.ReadFile(thumb)
		if e != nil {
			return m, ErrProcessing
		}
		h, e := parseImageHeader(b)
		if e != nil {
			return m, ErrProcessing
		}
		a, e := p.encode(ctx, dir, thumb, "thumbnail", h.width, h.height)
		if e != nil {
			return m, e
		}
		m.Thumbnail = &a
	}
	m.PerceptualHash = DHash(im)
	return m, nil
}
func decodePNG(path string, maxSide int) (image.Image, error) {
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return nil, ErrProcessing
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() < 24 || st.Size() > 32<<20 {
		return nil, ErrProcessing
	}
	head := make([]byte, 24)
	if _, e = io.ReadFull(f, head); e != nil {
		return nil, ErrProcessing
	}
	h, e := parseImageHeader(head)
	if e != nil || h.width > maxSide || h.height > maxSide {
		return nil, ErrProcessing
	}
	if _, e = f.Seek(0, 0); e != nil {
		return nil, ErrProcessing
	}
	im, e := png.Decode(io.LimitReader(f, 32<<20))
	if e != nil {
		return nil, ErrProcessing
	}
	return im, nil
}
func (p *Processor) encode(ctx context.Context, dir, source, name string, w, h int) (Artifact, error) {
	ext, mime := "webp", "image/webp"
	if p.cfg.ImageFormat == "AVIF" {
		ext, mime = "avif", "image/avif"
	}
	path := filepath.Join(dir, name+"."+ext)
	if ext == "avif" {
		// libaom's documented quality64 maps to ((100-64)*63+50)/100=23.
		// Quantizer flags support both libavif0.11 and current1.x; alpha is lossless.
		if _, err := p.command(ctx, p.cfg.ProbeTimeout, p.cfg.Avifenc, "--codec", "aom", "--yuv", "420", "--min", strconv.Itoa(p.avifQuantizer()), "--max", strconv.Itoa(p.avifQuantizer()), "--minalpha", "0", "--maxalpha", "0", "--speed", "6", "--jobs", strconv.Itoa(p.cfg.Threads), "--ignore-exif", "--ignore-xmp", source, path); err != nil {
			return Artifact{}, err
		}
	} else {
		args := p.ffmpegBase(source)
		args = append(args, "-c:v", "libwebp", "-quality", strconv.Itoa(p.webpQuality()), "-compression_level", "6", "-threads", strconv.Itoa(p.cfg.Threads), path)
		if _, err := p.command(ctx, p.cfg.ProbeTimeout, p.cfg.FFmpeg, args...); err != nil {
			return Artifact{}, err
		}
	}
	return artifact(path, mime, w, h, p.cfg.ImageMaxBytes)
}
func artifact(path, mime string, w, h int, maxBytes int64) (Artifact, error) {
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return Artifact{}, ErrProcessing
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > maxBytes {
		return Artifact{}, ErrProcessing
	}
	sum := sha256.New()
	n, e := io.Copy(sum, io.LimitReader(f, maxBytes+1))
	if e != nil || n != st.Size() {
		return Artifact{}, ErrProcessing
	}
	return Artifact{Path: path, SHA256: hex.EncodeToString(sum.Sum(nil)), ContentType: mime, ByteSize: n, Width: w, Height: h}, nil
}

type probe struct {
	Format struct {
		Name     string `json:"format_name"`
		Duration string `json:"duration"`
	} `json:"format"`
	Streams []struct {
		Type        string `json:"codec_type"`
		Codec       string `json:"codec_name"`
		Pixel       string `json:"pix_fmt"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
		Duration    string `json:"duration"`
		Disposition struct {
			Attached int `json:"attached_pic"`
		} `json:"disposition"`
	} `json:"streams"`
}

func (p *Processor) probe(ctx context.Context, path string, maxSide int, maxDuration float64) (w, h int, duration float64, err error) {
	b, err := p.command(ctx, p.cfg.ProbeTimeout, p.cfg.FFprobe, "-v", "error", "-protocol_whitelist", "file", "-format_whitelist", "mov,matroska,webm", "-codec_whitelist", "h264,hevc,vp8,vp9,av1,libdav1d,mpeg4,aac,mp3,mp3float,opus,libopus,vorbis,libvorbis,mjpeg", "-max_pixels", "14745600", "-analyzeduration", "10M", "-probesize", "25M", "-print_format", "json", "-show_format", "-show_streams", path)
	if err != nil {
		return 0, 0, 0, err
	}
	return validateProbe(b, maxSide, maxDuration)
}
func validateProbe(b []byte, maxSide int, maxDuration float64) (w, h int, duration float64, err error) {
	var q probe
	if json.Unmarshal(b, &q) != nil {
		return 0, 0, 0, ErrRejected
	}
	if q.Format.Name != "mov,mp4,m4a,3gp,3g2,mj2" && q.Format.Name != "matroska,webm" {
		return 0, 0, 0, ErrRejected
	}
	videos, audios := 0, 0
	raw := q.Format.Duration
	for _, s := range q.Streams {
		switch s.Type {
		case "video":
			if s.Disposition.Attached != 0 {
				continue
			}
			videos++
			if !contains([]string{"h264", "hevc", "vp8", "vp9", "av1", "mpeg4"}, s.Codec) || !hasPrefix(s.Pixel, []string{"yuv", "yuvj", "nv12", "nv21", "gray"}) {
				return 0, 0, 0, ErrRejected
			}
			w, h = s.Width, s.Height
			if raw == "" {
				raw = s.Duration
			}
		case "audio":
			audios++
			if !contains([]string{"aac", "mp3", "opus", "vorbis"}, s.Codec) {
				return 0, 0, 0, ErrRejected
			}
		default:
			return 0, 0, 0, ErrRejected
		}
	}
	duration, e := strconv.ParseFloat(raw, 64)
	if e != nil || math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 || duration > maxDuration || videos != 1 || audios > 1 || w <= 0 || h <= 0 || w > maxSide || h > maxSide {
		return 0, 0, 0, ErrRejected
	}
	return w, h, duration, nil
}
func contains(v []string, s string) bool {
	for _, x := range v {
		if x == s {
			return true
		}
	}
	return false
}
func hasPrefix(s string, v []string) bool {
	for _, x := range v {
		if strings.HasPrefix(s, x) {
			return true
		}
	}
	return false
}

// ProcessVideo is the off-request worker operation. Admission and READY updates
// must be separate short domain transactions; a pending source stays withheld.
func (p *Processor) ProcessVideo(ctx context.Context, path string) (m Manifest, err error) {
	if !p.cfg.VideoEnabled {
		return m, ErrRejected
	}
	if err = p.acquireVideo(ctx); err != nil {
		return m, err
	}
	defer p.releaseVideo()
	dir, source, digest, size, err := p.stage(path, p.cfg.VideoMaxBytes)
	if err != nil {
		return m, err
	}
	defer func() {
		if err != nil {
			cleanupDir(dir)
		}
	}()
	if err = p.scan(ctx, digest, ""); err != nil {
		return m, err
	}
	_, _, duration, err := p.probe(ctx, source, p.cfg.VideoSourceSide, p.cfg.VideoMaxSeconds)
	if err != nil {
		return m, err
	}
	output := filepath.Join(dir, "main.mp4")
	scale := fmt.Sprintf("scale=min(%d\\,iw):min(%d\\,ih):force_original_aspect_ratio=decrease:force_divisible_by=2,format=yuv420p", p.cfg.VideoTargetSide, p.cfg.VideoTargetSide)
	args := []string{"-v", "error", "-y", "-nostdin", "-max_alloc", "134217728", "-protocol_whitelist", "file", "-format_whitelist", "mov,matroska,webm", "-codec_whitelist", "h264,hevc,vp8,vp9,av1,libdav1d,mpeg4,aac,mp3,mp3float,opus,libopus,vorbis,libvorbis,mjpeg", "-max_pixels", "14745600", "-threads", "2", "-filter_threads", "1", "-i", source, "-map", "0:V:0", "-map", "0:a:0?", "-map_metadata", "-1", "-map_chapters", "-1", "-vf", scale, "-c:v", "libx264", "-profile:v", "high", "-level", "4.1", "-preset", p.cfg.VideoPreset, "-crf", strconv.Itoa(p.cfg.VideoCRF), "-c:a", "aac", "-b:a", p.cfg.VideoAudioBitrate, "-ac", "2", "-metadata:s:v:0", "rotate=0", "-t", strconv.FormatFloat(p.cfg.VideoMaxSeconds, 'f', -1, 64), "-threads", strconv.Itoa(p.cfg.Threads), "-movflags", "+faststart", "-f", "mp4", output}
	if _, err = p.command(ctx, p.cfg.CommandTimeout, p.cfg.FFmpeg, args...); err != nil {
		return m, err
	}
	w, h, outDuration, err := p.probe(ctx, output, p.cfg.VideoTargetSide, p.cfg.VideoMaxSeconds+5)
	if err != nil {
		return m, err
	}
	// Frame zero is unconditional, so even a sub-interval clip gets screened.
	pattern := filepath.Join(dir, "scan_%04d.png")
	args = []string{"-v", "error", "-y", "-nostdin", "-protocol_whitelist", "file", "-threads", "1", "-filter_threads", "1", "-i", output, "-vf", "select=isnan(prev_selected_t)+gte(t-prev_selected_t\\," + strconv.FormatFloat(p.cfg.VideoFrameScanInterval.Seconds(), 'f', -1, 64) + ")", "-fps_mode", "vfr", "-frames:v", strconv.Itoa(p.cfg.VideoFrameScanMaxFrames), "-c:v", "png", "-threads", "1", pattern}
	if _, err = p.command(ctx, p.cfg.CommandTimeout, p.cfg.FFmpeg, args...); err != nil {
		return m, err
	}
	frames, err := filepath.Glob(filepath.Join(dir, "scan_*.png"))
	if err != nil || len(frames) == 0 || len(frames) > p.cfg.VideoFrameScanMaxFrames {
		return m, ErrProcessing
	}
	for _, frame := range frames {
		a, e := artifact(frame, "image/png", w, h, 32<<20)
		if e != nil {
			return m, e
		}
		im, e := decodePNG(frame, p.cfg.VideoTargetSide)
		if e != nil {
			return m, ErrProcessing
		}
		if e = p.scan(ctx, a.SHA256, DHash(im)); e != nil {
			return m, e
		}
	}
	posterSource := filepath.Join(dir, "poster-source.png")
	seek := "0"
	if duration >= 2 {
		seek = "1"
	}
	args = []string{"-v", "error", "-y", "-nostdin", "-protocol_whitelist", "file", "-ss", seek, "-i", output, "-frames:v", "1", "-c:v", "png", "-threads", "1", posterSource}
	if _, err = p.command(ctx, p.cfg.ProbeTimeout, p.cfg.FFmpeg, args...); err != nil {
		return m, err
	}
	poster, err := p.imageArtifacts(ctx, dir, posterSource, 1, false)
	if err != nil {
		return m, err
	}
	if err = p.scan(ctx, poster.Main.SHA256, poster.PerceptualHash); err != nil {
		return m, err
	}
	poster.Main.Path = filepath.Join(dir, "poster."+strings.ToLower(p.cfg.ImageFormat))
	if err = os.Rename(filepath.Join(dir, "main."+strings.ToLower(p.cfg.ImageFormat)), poster.Main.Path); err != nil {
		return m, ErrProcessing
	}
	m.Main, err = artifact(output, "video/mp4", w, h, p.cfg.VideoMaxBytes)
	if err != nil {
		return m, err
	}
	m.Poster = &poster.Main
	m.SourceSHA256 = digest
	m.SourceByteSize = size
	m.Kind = "video"
	m.Status = "ready"
	m.ScannerClean = true
	m.MetadataStripped = true
	m.PolicyVersion = PolicyVersion
	m.DurationSeconds = outDuration
	m.scratch = dir
	return m, nil
}

func (p *Processor) ProcessPDF(ctx context.Context, path string) (m Manifest, err error) {
	if err = p.acquire(ctx); err != nil {
		return m, err
	}
	defer p.release()
	dir, source, digest, size, err := p.stage(path, p.cfg.AttachmentMaxBytes)
	if err != nil {
		return m, err
	}
	defer func() {
		if err != nil {
			cleanupDir(dir)
		}
	}()
	f, e := os.Open(source)
	if e != nil {
		return m, ErrRejected
	}
	var magic [5]byte
	_, e = io.ReadFull(f, magic[:])
	f.Close()
	if e != nil || string(magic[:]) != "%PDF-" {
		return m, ErrRejected
	}
	if err = p.scan(ctx, digest, ""); err != nil {
		return m, err
	}
	if p.documents == nil {
		return m, ErrScanner
	}
	v, e := p.documents.ScanDocument(ctx, source, size)
	if e != nil {
		return m, ErrScanner
	}
	if !v.Clean {
		return m, &BlockedError{SourceSHA256: digest}
	}
	m.Main, err = artifact(source, "application/pdf", 0, 0, p.cfg.AttachmentMaxBytes)
	if err != nil {
		return m, err
	}
	m.SourceSHA256 = digest
	m.SourceByteSize = size
	m.Kind = "file"
	m.Status = "ready"
	m.ScannerClean = true
	m.PolicyVersion = PolicyVersion
	m.scratch = dir
	return m, nil
}
