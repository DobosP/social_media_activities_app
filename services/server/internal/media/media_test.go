package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func syntheticPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			im.SetRGBA(x, y, color.RGBA{uint8(x % 255), uint8(y % 255), uint8((x + y) % 255), 255})
		}
	}
	var b bytes.Buffer
	if e := png.Encode(&b, im); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func writeSource(t *testing.T, b []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "synthetic-source")
	if e := os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	return path
}

type cleanScanner struct{}

func (cleanScanner) Scan(context.Context, ScanInput) (Verdict, error) {
	return Verdict{Clean: true}, nil
}

func TestHeaderDimensionsAndEXIF(t *testing.T) {
	b := syntheticPNG(t, 12, 8)
	h, e := parseImageHeader(b)
	if e != nil || h.width != 12 || h.height != 8 || h.format != "PNG" {
		t.Fatalf("PNG %+v %v", h, e)
	}
	var jpg bytes.Buffer
	if e = jpeg.Encode(&jpg, image.NewRGBA(image.Rect(0, 0, 15, 10)), nil); e != nil {
		t.Fatal(e)
	}
	h, e = parseImageHeader(jpg.Bytes())
	if e != nil || h.width != 15 || h.height != 10 {
		t.Fatalf("JPEG %+v %v", h, e)
	}
	for _, little := range []bool{false, true} {
		var order binary.ByteOrder = binary.BigEndian
		prefix := "MM"
		if little {
			order = binary.LittleEndian
			prefix = "II"
		}
		exif := make([]byte, 26)
		copy(exif, prefix)
		order.PutUint16(exif[2:], 42)
		order.PutUint32(exif[4:], 8)
		order.PutUint16(exif[8:], 1)
		order.PutUint16(exif[10:], 0x112)
		order.PutUint16(exif[12:], 3)
		order.PutUint32(exif[14:], 1)
		order.PutUint16(exif[18:], 6)
		if exifOrientation(exif) != 6 {
			t.Fatal("orientation not parsed")
		}
	}
	for _, bad := range [][]byte{[]byte("not-image"), b[:20], {0xff, 0xd8, 0xff, 0xe1, 0xff, 0xff}} {
		if _, e = parseImageHeader(bad); e == nil {
			t.Fatal("bad header accepted")
		}
	}
}
func TestHeaderWebPAndAVIF(t *testing.T) {
	b := make([]byte, 30)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], 22)
	copy(b[8:], "WEBPVP8X")
	binary.LittleEndian.PutUint32(b[16:], 10)
	b[24] = 8
	b[27] = 7
	h, e := parseImageHeader(b)
	if e != nil || h.width != 9 || h.height != 8 {
		t.Fatalf("WebP %+v %v", h, e)
	}
	box := func(name string, body []byte) []byte {
		x := make([]byte, len(body)+8)
		binary.BigEndian.PutUint32(x, uint32(len(x)))
		copy(x[4:], name)
		copy(x[8:], body)
		return x
	}
	ispe := make([]byte, 12)
	binary.BigEndian.PutUint32(ispe[4:], 500)
	binary.BigEndian.PutUint32(ispe[8:], 200)
	avif := append(box("ftyp", []byte("avif\x00\x00\x00\x00avif")), box("meta", append([]byte{0, 0, 0, 0}, box("iprp", box("ipco", box("ispe", ispe)))...))...)
	h, e = parseImageHeader(avif)
	if e != nil || h.width != 500 || h.height != 200 {
		t.Fatalf("AVIF %+v %v", h, e)
	}
}
func TestDHashAndHamming(t *testing.T) {
	im := image.NewGray(image.Rect(0, 0, 9, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 9; x++ {
			v := uint8(x * 20)
			if y%2 == 0 {
				v = 255 - v
			}
			im.SetGray(x, y, color.Gray{Y: v})
		}
	}
	if DHash(im) != "ff00ff00ff00ff00" {
		t.Fatal(DHash(im))
	}
	if n, e := Hamming("ff00ff00ff00ff00", "ff00ff00ff00ff01"); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	if _, e := Hamming("x", "x"); e == nil {
		t.Fatal("invalid hashes accepted")
	}
	if DHash(image.NewGray(image.Rect(0, 0, 9, 8))) != "" {
		t.Fatal("flat image identity")
	}
}
func TestDHashPillowGoldenFixtures(t *testing.T) {
	b, e := os.ReadFile("testdata/phash_reference.json")
	if e != nil {
		t.Fatal(e)
	}
	var cases []struct {
		Width, Height, Seed int
		Hash                string
	}
	if e = json.Unmarshal(b, &cases); e != nil {
		t.Fatal(e)
	}
	for _, c := range cases {
		im := image.NewNRGBA(image.Rect(0, 0, c.Width, c.Height))
		for y := 0; y < c.Height; y++ {
			for x := 0; x < c.Width; x++ {
				im.SetNRGBA(x, y, color.NRGBA{uint8((x*17 + y*3 + c.Seed) % 256), uint8((x*5 + y*19 + c.Seed*2) % 256), uint8((x*y + c.Seed*23) % 256), uint8((x*7 + y*11 + c.Seed) % 256)})
			}
		}
		if actual := DHash(im); actual != c.Hash {
			t.Fatalf("%dx%d seed%d: expected%s actual%s", c.Width, c.Height, c.Seed, c.Hash, actual)
		}
	}
}
func TestScannersFailClosed(t *testing.T) {
	for _, b := range []string{`{}`, `null`, `{"match":null}`, `{"match":0}`, `{"match":"false"}`, `{"match":false,"match":true}`, `{"flagged":false} {}`, `{"match":false,"unreviewed":true}`} {
		if _, e := parseManagedVerdict([]byte(b)); !errors.Is(e, ErrScanner) {
			t.Fatalf("accepted %s", b)
		}
	}
	for _, b := range []string{`{"match":false}`, `{"flagged":false}`, `{"match":false,"flagged":false}`} {
		v, e := parseManagedVerdict([]byte(b))
		if e != nil || !v.Clean {
			t.Fatalf("rejected explicit clean %s", b)
		}
	}
	v, e := parseManagedVerdict([]byte(`{"match":false,"flagged":true}`))
	if e != nil || v.Clean {
		t.Fatal("flagged accepted")
	}
	if _, e := NewBlocklist(nil, nil, 8); e == nil {
		t.Fatal("empty blocklist effective")
	}
	digest := strings.Repeat("a", 64)
	list, e := NewBlocklist([]string{digest}, nil, 8)
	if e != nil {
		t.Fatal(e)
	}
	v, e = list.Scan(context.Background(), ScanInput{SHA256: digest})
	if e != nil || v.Clean {
		t.Fatal("blocked digest accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = list.Scan(ctx, ScanInput{SHA256: digest}); e == nil {
		t.Fatal("cancelled scan accepted")
	}
}
func TestManagedScannerNetworkAndRedirect(t *testing.T) {
	var redirect atomic.Bool
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if redirect.Load() {
			http.Redirect(w, r, "https://other.invalid/scan", 302)
			return
		}
		if r.Method != "POST" {
			t.Error(r.Method)
		}
		io.WriteString(w, `{"match":false}`)
	}))
	defer s.Close()
	scanner := ManagedScanner{Endpoint: s.URL, Client: s.Client()}
	v, e := scanner.Scan(context.Background(), ScanInput{SHA256: strings.Repeat("b", 64)})
	if e != nil || !v.Clean {
		t.Fatal(v, e)
	}
	redirect.Store(true)
	if _, e = scanner.Scan(context.Background(), ScanInput{SHA256: strings.Repeat("b", 64)}); e == nil {
		t.Fatal("redirect accepted")
	}
}
func TestClamdProtocol(t *testing.T) {
	for _, reply := range []string{"stream: OK\x00", "stream: test FOUND\x00", "malformed OK\x00"} {
		t.Run(strings.TrimSpace(reply), func(t *testing.T) {
			listener, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			defer listener.Close()
			body := []byte("%PDF-1.7\nsynthetic")
			path := writeSource(t, body)
			done := make(chan error, 1)
			go func() {
				conn, e := listener.Accept()
				if e != nil {
					done <- e
					return
				}
				defer conn.Close()
				magic := make([]byte, 10)
				if _, e = io.ReadFull(conn, magic); e != nil || string(magic) != "zINSTREAM\x00" {
					done <- fmt.Errorf("bad protocol")
					return
				}
				var received bytes.Buffer
				for {
					var head [4]byte
					if _, e = io.ReadFull(conn, head[:]); e != nil {
						done <- e
						return
					}
					n := binary.BigEndian.Uint32(head[:])
					if n == 0 {
						break
					}
					if n > 65536 {
						done <- fmt.Errorf("bad chunk")
						return
					}
					if _, e = io.CopyN(&received, conn, int64(n)); e != nil {
						done <- e
						return
					}
				}
				if !bytes.Equal(received.Bytes(), body) {
					done <- fmt.Errorf("wrong streamed bytes")
					return
				}
				_, e = io.WriteString(conn, reply)
				done <- e
			}()
			v, e := (Clamd{Address: listener.Addr().String()}).ScanDocument(context.Background(), path, int64(len(body)))
			if e != nil {
				t.Fatal(e)
			}
			if v.Clean != (reply == "stream: OK\x00") {
				t.Fatal("bad verdict", v)
			}
			if e = <-done; e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestLocalStorageTraversalRangesAndTokens(t *testing.T) {
	store, e := NewLocalStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	source := writeSource(t, []byte("synthetic media"))
	if e = store.Put(context.Background(), "photo/a.webp", source, "image/webp"); e != nil {
		t.Fatal(e)
	}
	b, e := store.OpenRange(context.Background(), "photo/a.webp", 0, 8)
	if e != nil || string(b) != "synthetic" {
		t.Fatal(string(b), e)
	}
	for _, key := range []string{"../escape", "/absolute", "a/../../x", "a//x", "x?token=y"} {
		if e = store.Put(context.Background(), key, source, "image/webp"); e == nil {
			t.Fatal("unsafe key", key)
		}
	}
	if _, e = store.OpenRange(context.Background(), "photo/a.webp", 0, 8<<20); e == nil {
		t.Fatal("unbounded range")
	}
	outside := t.TempDir()
	if e = store.root.Symlink(outside, "escape"); e != nil {
		t.Fatal(e)
	}
	if e = store.Put(context.Background(), "escape/x.webp", source, "image/webp"); e == nil {
		t.Fatal("symlink escape")
	}
	now := time.Unix(1700000000, 0)
	codec := TokenCodec{Key: bytes.Repeat([]byte{7}, 32)}
	reference := Reference{Key: "photo/a.webp", ViewerID: 3, Variant: "thumb", Expires: now.Add(time.Minute).Unix()}
	token, e := codec.Sign(reference, now)
	if e != nil {
		t.Fatal(e)
	}
	if r, e := codec.Verify(token, 3, now); e != nil || r != reference {
		t.Fatal(r, e)
	}
	for _, test := range []struct {
		token  string
		viewer int64
		at     time.Time
	}{{token, 4, now}, {token, 3, now.Add(time.Minute)}, {token + "x", 3, now}} {
		if _, e = codec.Verify(test.token, test.viewer, test.at); e == nil {
			t.Fatal("invalid reference accepted")
		}
	}
}
func TestPresigningBoundsAndCanonicalAWSVector(t *testing.T) {
	s, e := NewS3Store(S3Config{Endpoint: "https://objects.eu.example.invalid", Bucket: "private-test", Region: "eu-test-1", AccessKey: "test-access", SecretKey: "test-secret", EUResidencyVerified: true, PrivateBucketVerified: true})
	if e != nil {
		t.Fatal(e)
	}
	s.now = func() time.Time { return time.Unix(1700000000, 0) }
	signed, e := s.PresignGet(context.Background(), "pdf/a.pdf", time.Minute, "application/pdf", "document.pdf")
	if e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(signed)
	if e != nil || u.Query().Get("X-Amz-Expires") != "60" || u.Query().Get("response-content-disposition") != `attachment; filename="document.pdf"` || u.Query().Get("response-cache-control") != "private, no-store" {
		t.Fatal("presign contract")
	}
	if _, e = s.PresignGet(context.Background(), "pdf/a.pdf", 61*time.Second, "application/pdf", "document.pdf"); e == nil {
		t.Fatal("long private presign")
	}
	if _, e = NewS3Store(S3Config{Endpoint: "https://objects.eu.example.invalid", Bucket: "private-test", Region: "eu-test-1", AccessKey: "test", SecretKey: "test"}); e == nil {
		t.Fatal("unverified residence")
	}
	// AWS's published canonical-request vector exercises escaping, sorting,
	// blank lines, scope and hash independently of our own request construction.
	q := url.Values{"X-Amz-Algorithm": {"AWS4-HMAC-SHA256"}, "X-Amz-Credential": {"AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request"}, "X-Amz-Date": {"20130524T000000Z"}, "X-Amz-Expires": {"86400"}, "X-Amz-SignedHeaders": {"host"}}
	canonical := "GET\n/test.txt\n" + canonicalQuery(q) + "\nhost:examplebucket.s3.amazonaws.com\n\nhost\nUNSIGNED-PAYLOAD"
	sum := sha256.Sum256([]byte(canonical))
	if hex.EncodeToString(sum[:]) != "3bfa292879f6447bbcda7001decf97f4a54dc650c8942174ae0a9121cf58ad04" {
		t.Fatal("AWS vector")
	}
	if hex.EncodeToString(hmacSHA([]byte("Jefe"), "what do ya want for nothing?")) != "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843" {
		t.Fatal("RFC4231 HMAC vector")
	}
}
func TestImageBombAndMissingScannerRejectedBeforeCodec(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	config.ImageFormat = "WEBP"
	p, e := NewProcessor(config, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	b := syntheticPNG(t, 12, 8)
	binary.BigEndian.PutUint32(b[16:20], 100000)
	binary.BigEndian.PutUint32(b[20:24], 100000)
	if _, e = p.ProcessImage(context.Background(), writeSource(t, b)); !errors.Is(e, ErrRejected) {
		t.Fatal(e)
	}
	binary.BigEndian.PutUint32(b[16:20], ^uint32(0))
	binary.BigEndian.PutUint32(b[20:24], ^uint32(0))
	if _, e = p.ProcessImage(context.Background(), writeSource(t, b)); !errors.Is(e, ErrRejected) {
		t.Fatal("overflow pixel bomb", e)
	}
	if _, e = p.ProcessImage(context.Background(), writeSource(t, syntheticPNG(t, 12, 8))); !errors.Is(e, ErrScanner) {
		t.Fatal(e)
	}
	source := writeSource(t, syntheticPNG(t, 12, 8))
	link := filepath.Join(t.TempDir(), "link")
	if e = os.Symlink(source, link); e != nil {
		t.Fatal(e)
	}
	if _, e = p.ProcessImage(context.Background(), link); !errors.Is(e, ErrRejected) {
		t.Fatal("symlink admitted", e)
	}
}
func TestVideoProbeCaps(t *testing.T) {
	document := map[string]any{"format": map[string]any{"format_name": "mov,mp4,m4a,3gp,3g2,mj2", "duration": "1.5"}, "streams": []any{map[string]any{"codec_type": "video", "codec_name": "h264", "pix_fmt": "yuv420p", "width": 640, "height": 480}}}
	marshal := func() []byte { b, _ := json.Marshal(document); return b }
	w, h, d, e := validateProbe(marshal(), 3840, 90)
	if e != nil || w != 640 || h != 480 || d != 1.5 {
		t.Fatal(w, h, d, e)
	}
	for _, duration := range []string{"NaN", "Inf", "91", "0", "-1"} {
		document["format"].(map[string]any)["duration"] = duration
		if _, _, _, e = validateProbe(marshal(), 3840, 90); e == nil {
			t.Fatal("duration accepted", duration)
		}
	}
}
func TestRangesMultipartAndPrivateJSON(t *testing.T) {
	for _, c := range []struct {
		raw        string
		start, end int64
	}{{"bytes=0-4", 0, 4}, {"bytes=7-", 7, 9}, {"bytes=-3", 7, 9}, {"bytes=3-999", 3, 9}} {
		start, end, e := singleRange(c.raw, 10)
		if e != nil || start != c.start || end != c.end {
			t.Fatal(c, e)
		}
	}
	for _, raw := range []string{"bytes=10-", "bytes=0--1", "bytes=-0", "bytes=8-3", "bytes=9999999999999999999999-", "items=0-4"} {
		if _, _, e := singleRange(raw, 10); e == nil {
			t.Fatal("invalid range", raw)
		}
	}
	m := Manifest{Main: Artifact{Path: "/private/worker/source"}, scratch: "/private/worker"}
	b, e := json.Marshal(m)
	if e != nil || bytes.Contains(b, []byte("/private")) {
		t.Fatal("private paths exposed")
	}
	now := time.Now()
	ttl := int64(1)
	if e := expiry("child", &ttl); e == nil || e.Sub(now) < 24*time.Hour-time.Second {
		t.Fatal("child expiry floor")
	}
	if e := expiry("adult", &ttl); e == nil || e.Sub(now) < time.Hour-time.Second {
		t.Fatal("adult expiry floor")
	}
}

// This executes real native codecs when the runtime tools are installed. The
// image and video are generated here; no personal uploads or network needed.
func TestNativeCodecs(t *testing.T) {
	for _, bin := range []string{"ffmpeg", "ffprobe", "prlimit"} {
		if _, e := exec.LookPath(bin); e != nil {
			t.Skip("native codec runtime not installed")
		}
	}
	ctx := context.Background()
	config := DefaultConfig(t.TempDir())
	config.ImageFormat = "WEBP"
	p, e := NewProcessor(config, cleanScanner{}, nil)
	if e != nil {
		t.Fatal(e)
	}
	imageManifest, e := p.ProcessImage(ctx, writeSource(t, syntheticPNG(t, 1000, 600)))
	if e != nil {
		t.Fatal("image", e)
	}
	defer imageManifest.Cleanup()
	if imageManifest.Main.ContentType != "image/webp" || imageManifest.Main.Width != 1000 || imageManifest.Thumbnail == nil || imageManifest.Thumbnail.Width != 800 || !imageManifest.ScannerClean || !imageManifest.MetadataStripped {
		t.Fatalf("bad image manifest %+v", imageManifest)
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "test.mp4")
	if _, e = p.command(ctx, 30*time.Second, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "color=c=red:s=64x48:r=10", "-t", "1", "-c:v", "libx264", "-threads", "1", source); e != nil {
		t.Fatal("generate video", e)
	}
	videoManifest, e := p.ProcessVideo(ctx, source)
	if e != nil {
		t.Fatal("video", e)
	}
	defer videoManifest.Cleanup()
	if videoManifest.Main.ContentType != "video/mp4" || videoManifest.Poster == nil || videoManifest.Status != "ready" || videoManifest.DurationSeconds <= 0 {
		t.Fatalf("bad video manifest %+v", videoManifest)
	}
}
func TestNativeAVIFAndOrientation(t *testing.T) {
	for _, bin := range []string{"ffmpeg", "ffprobe", "prlimit", "avifenc"} {
		if _, e := exec.LookPath(bin); e != nil {
			t.Skip("AVIF native codec runtime not installed")
		}
	}
	p, e := NewProcessor(DefaultConfig(t.TempDir()), cleanScanner{}, nil)
	if e != nil {
		t.Fatal(e)
	}
	m, e := p.ProcessImage(context.Background(), writeSource(t, syntheticPNG(t, 1200, 900)))
	if e != nil {
		t.Fatal("AVIF", e)
	}
	defer m.Cleanup()
	if m.Main.ContentType != "image/avif" || m.Thumbnail == nil || m.Thumbnail.Width != 800 || m.Thumbnail.Height != 600 {
		t.Fatalf("AVIF manifest %+v", m)
	}
	var jpg bytes.Buffer
	if e = jpeg.Encode(&jpg, image.NewRGBA(image.Rect(0, 0, 15, 10)), nil); e != nil {
		t.Fatal(e)
	}
	exif := make([]byte, 26)
	copy(exif, "II")
	binary.LittleEndian.PutUint16(exif[2:], 42)
	binary.LittleEndian.PutUint32(exif[4:], 8)
	binary.LittleEndian.PutUint16(exif[8:], 1)
	binary.LittleEndian.PutUint16(exif[10:], 0x112)
	binary.LittleEndian.PutUint16(exif[12:], 3)
	binary.LittleEndian.PutUint32(exif[14:], 1)
	binary.LittleEndian.PutUint16(exif[18:], 6)
	segment := append([]byte("Exif\x00\x00"), exif...)
	source := []byte{0xff, 0xd8, 0xff, 0xe1, 0, byte(len(segment) + 2)}
	source = append(source, segment...)
	source = append(source, jpg.Bytes()[2:]...)
	rotated, e := p.ProcessImage(context.Background(), writeSource(t, source))
	if e != nil {
		t.Fatal("orientation", e)
	}
	defer rotated.Cleanup()
	if rotated.Main.Width != 10 || rotated.Main.Height != 15 {
		t.Fatal("orientation not baked", rotated.Main)
	}
	encoded, e := os.ReadFile(rotated.Main.Path)
	if e != nil || bytes.Contains(encoded, []byte("Exif")) {
		t.Fatal("source EXIF survived")
	}
}
