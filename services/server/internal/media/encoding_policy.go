package media

import (
	"fmt"
	"math"
	"time"
)

func (c Config) ValidateEncodingPolicy() error {
	if c.ImageQuality < 0 || c.ImageQuality > 100 {
		return fmt.Errorf("MEDIA_IMAGE_QUALITY is outside native bounds")
	}
	if c.AttachmentMaxBytes < 1 || c.AttachmentMaxBytes > 7<<20 {
		return fmt.Errorf("MEDIA_ATTACHMENT_MAX_BYTES is outside native bounds")
	}
	if c.VideoCRF < 18 || c.VideoCRF > 40 {
		return fmt.Errorf("MEDIA_VIDEO_CRF is outside native bounds")
	}
	if !map[string]bool{"ultrafast": true, "superfast": true, "veryfast": true, "faster": true, "fast": true, "medium": true}[c.VideoPreset] {
		return fmt.Errorf("MEDIA_VIDEO_PRESET is unsupported")
	}
	if !map[string]bool{"32k": true, "48k": true, "64k": true, "96k": true, "128k": true}[c.VideoAudioBitrate] {
		return fmt.Errorf("MEDIA_VIDEO_AUDIO_BITRATE is unsupported")
	}
	if c.VideoFrameScanInterval < time.Second || c.VideoFrameScanInterval > 5*time.Second {
		return fmt.Errorf("MEDIA_VIDEO_FRAME_SCAN_INTERVAL_SECONDS cannot weaken scan coverage")
	}
	if c.VideoFrameScanMaxFrames < 25 || c.VideoFrameScanMaxFrames > 100 || float64(c.VideoFrameScanMaxFrames) < math.Ceil(c.VideoMaxSeconds/c.VideoFrameScanInterval.Seconds()) {
		return fmt.Errorf("MEDIA_VIDEO_FRAME_SCAN_MAX_FRAMES must cover full bounded clip")
	}
	return nil
}
func (p *Processor) avifQuantizer() int {
	q := p.cfg.ImageQuality
	if q == 0 {
		q = 64
	}
	return ((100-q)*63 + 50) / 100
}
func (p *Processor) webpQuality() int {
	q := p.cfg.ImageQuality
	if q == 0 {
		q = 80
	}
	return q
}
