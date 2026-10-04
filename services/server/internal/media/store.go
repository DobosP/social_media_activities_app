package media

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var keyRE = regexp.MustCompile(`^[a-zA-Z0-9_-]+(/[a-zA-Z0-9_.-]+)*$`)

func validKey(key string) bool {
	if len(key) < 1 || len(key) > 160 || !keyRE.MatchString(key) {
		return false
	}
	for _, part := range strings.Split(key, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}
func validRange(start, end int64) bool { return start >= 0 && end >= start && end-start < 8<<20 }

// LocalStore is development-only; Go's rooted filesystem confines all operations
// even when an intermediate symlink attempts to leave the private storage root.
type LocalStore struct{ root *os.Root }

func NewLocalStore(dir string) (*LocalStore, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, ErrObject
	}
	r, e := os.OpenRoot(dir)
	if e != nil {
		return nil, ErrObject
	}
	return &LocalStore{root: r}, nil
}
func (s *LocalStore) Close() error { return s.root.Close() }
func (s *LocalStore) Size(ctx context.Context, key string) (int64, error) {
	if !validKey(key) {
		return 0, ErrObject
	}
	if e := ctx.Err(); e != nil {
		return 0, e
	}
	f, e := s.root.Open(key)
	if e != nil {
		return 0, ErrObject
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() < 0 || st.Size() > 80<<20 {
		return 0, ErrObject
	}
	return st.Size(), nil
}
func (s *LocalStore) Put(ctx context.Context, key, path, mime string) error {
	if !validKey(key) || !validMIME(mime) {
		return ErrObject
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	source, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return ErrObject
	}
	defer source.Close()
	st, e := source.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > 80<<20 {
		return ErrObject
	}
	if idx := strings.LastIndex(key, "/"); idx > 0 {
		if e = s.root.MkdirAll(key[:idx], 0700); e != nil {
			return ErrObject
		}
	}
	var raw [16]byte
	if _, e = rand.Read(raw[:]); e != nil {
		return ErrObject
	}
	tmp := hex.EncodeToString(raw[:]) + ".pending"
	f, e := s.root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return ErrObject
	}
	defer s.root.Remove(tmp)
	reader := &contextReader{ctx: ctx, r: source}
	n, e := io.Copy(f, io.LimitReader(reader, st.Size()+1))
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil || ce != nil || n != st.Size() {
		return ErrObject
	}
	if e = s.root.Rename(tmp, key); e != nil {
		return ErrObject
	}
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *contextReader) Read(b []byte) (int, error) {
	if e := c.ctx.Err(); e != nil {
		return 0, e
	}
	return c.r.Read(b)
}
func (s *LocalStore) OpenRange(ctx context.Context, key string, start, end int64) ([]byte, error) {
	if !validKey(key) || !validRange(start, end) {
		return nil, ErrObject
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	f, e := s.root.Open(key)
	if e != nil {
		return nil, ErrObject
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || end >= st.Size() {
		return nil, ErrObject
	}
	b := make([]byte, end-start+1)
	if _, e = f.ReadAt(b, start); e != nil {
		return nil, ErrObject
	}
	return b, nil
}
func (s *LocalStore) Delete(ctx context.Context, key string) error {
	if !validKey(key) {
		return ErrObject
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	e := s.root.Remove(key)
	if os.IsNotExist(e) {
		return nil
	}
	return e
}
func (s *LocalStore) PresignGet(ctx context.Context, key string, ttl time.Duration, mime, download string) (string, error) {
	if e := ctx.Err(); e != nil {
		return "", e
	}
	if !validKey(key) || ttl < time.Second || ttl > time.Minute || !validMIME(mime) {
		return "", ErrObject
	}
	return "", nil
}

type S3Config struct {
	Endpoint, Bucket, Region, AccessKey, SecretKey, SessionToken string
	SSE, AddressingStyle                                         string
	EUResidencyVerified                                          bool
	PrivateBucketVerified                                        bool
	Client                                                       *http.Client
}
type S3Store struct {
	cfg      S3Config
	endpoint *url.URL
	client   *http.Client
	now      func() time.Time
}

func NewS3Store(c S3Config) (*S3Store, error) {
	if c.SSE != "" && c.SSE != "AES256" && c.SSE != "aws:kms" && c.SSE != "aws:kms:dsse" {
		return nil, ErrObject
	}
	if c.AddressingStyle != "" && c.AddressingStyle != "auto" && c.AddressingStyle != "path" && c.AddressingStyle != "virtual" {
		return nil, ErrObject
	}
	u, e := url.Parse(c.Endpoint)
	host := ""
	if u != nil {
		host = strings.ToLower(u.Hostname())
	}
	if e != nil || u.Scheme != "https" || host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || !c.EUResidencyVerified || !c.PrivateBucketVerified || strings.Contains(host, "cloudflarestorage") || strings.Contains(host, "minio") || !regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`).MatchString(c.Bucket) || c.Region == "" || strings.ContainsAny(c.Region, "/\r\n") || c.AccessKey == "" || c.SecretKey == "" {
		return nil, ErrObject
	}
	client := &http.Client{Timeout: 30 * time.Second}
	if c.Client != nil {
		*client = *c.Client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if client.Timeout <= 0 || client.Timeout > 30*time.Second {
		client.Timeout = 30 * time.Second
	}
	return &S3Store{cfg: c, endpoint: u, client: client, now: time.Now}, nil
}
func validMIME(mime string) bool {
	return contains([]string{"image/avif", "image/webp", "image/png", "image/jpeg", "image/jpg", "application/pdf", "video/mp4", "application/octet-stream"}, mime)
}
func awsEscape(s string) string {
	const digits = "0123456789ABCDEF"
	var b strings.Builder
	for _, x := range []byte(s) {
		if x >= 'a' && x <= 'z' || x >= 'A' && x <= 'Z' || x >= '0' && x <= '9' || strings.ContainsRune("-_.~", rune(x)) {
			b.WriteByte(x)
		} else {
			b.WriteByte('%')
			b.WriteByte(digits[x>>4])
			b.WriteByte(digits[x&15])
		}
	}
	return b.String()
}
func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sortStrings(keys)
	var parts []string
	for _, k := range keys {
		v := append([]string(nil), q[k]...)
		sortStrings(v)
		for _, x := range v {
			parts = append(parts, awsEscape(k)+"="+awsEscape(x))
		}
	}
	return strings.Join(parts, "&")
}
func sortStrings(v []string) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}
func hmacSHA(key []byte, s string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(s))
	return h.Sum(nil)
}
func signingKey(secret, date, region string) []byte {
	k := hmacSHA([]byte("AWS4"+secret), date)
	k = hmacSHA(k, region)
	k = hmacSHA(k, "s3")
	return hmacSHA(k, "aws4_request")
}
func (s *S3Store) signed(method, key string, ttl time.Duration, extra url.Values, payloadDigest string) (string, error) {
	if !validKey(key) || ttl < time.Second || ttl > 60*time.Second {
		return "", ErrObject
	}
	now := s.now().UTC()
	date := now.Format("20060102")
	scope := date + "/" + s.cfg.Region + "/s3/aws4_request"
	u := *s.endpoint
	u.Path = "/" + s.cfg.Bucket + "/" + key
	if s.cfg.AddressingStyle == "virtual" {
		u.Host = s.cfg.Bucket + "." + u.Host
		u.Path = "/" + key
	}
	u.RawPath = ""
	q := url.Values{}
	for k, v := range extra {
		q[k] = append([]string(nil), v...)
	}
	headers := "host:" + u.Host + "\n"
	signedHeaders := "host"
	if payloadDigest != "UNSIGNED-PAYLOAD" {
		if !digestRE.MatchString(payloadDigest) {
			return "", ErrObject
		}
		headers += "x-amz-content-sha256:" + payloadDigest + "\n"
		signedHeaders += ";x-amz-content-sha256"
	}
	if method == http.MethodPut && s.cfg.SSE != "" {
		headers += "x-amz-server-side-encryption:" + s.cfg.SSE + "\n"
		signedHeaders += ";x-amz-server-side-encryption"
	}
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", s.cfg.AccessKey+"/"+scope)
	q.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	q.Set("X-Amz-Expires", strconv.Itoa(int(ttl/time.Second)))
	q.Set("X-Amz-SignedHeaders", signedHeaders)
	if s.cfg.SessionToken != "" {
		q.Set("X-Amz-Security-Token", s.cfg.SessionToken)
	}
	canonical := method + "\n" + u.EscapedPath() + "\n" + canonicalQuery(q) + "\n" + headers + "\n" + signedHeaders + "\n" + payloadDigest
	sum := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + now.Format("20060102T150405Z") + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
	q.Set("X-Amz-Signature", hex.EncodeToString(hmacSHA(signingKey(s.cfg.SecretKey, date, s.cfg.Region), toSign)))
	u.RawQuery = canonicalQuery(q)
	return u.String(), nil
}
func (s *S3Store) Put(ctx context.Context, key, path, mime string) error {
	if !validMIME(mime) {
		return ErrObject
	}
	a, e := artifact(path, mime, 0, 0, 80<<20)
	if e != nil {
		return ErrObject
	}
	signed, e := s.signed("PUT", key, 60*time.Second, nil, a.SHA256)
	if e != nil {
		return e
	}
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return ErrObject
	}
	defer f.Close()
	req, e := http.NewRequestWithContext(ctx, "PUT", signed, &contextReader{ctx: ctx, r: f})
	if e != nil {
		return ErrObject
	}
	req.ContentLength = a.ByteSize
	req.Header.Set("Content-Type", mime)
	req.Header.Set("x-amz-content-sha256", a.SHA256)
	if s.cfg.SSE != "" {
		req.Header.Set("x-amz-server-side-encryption", s.cfg.SSE)
	}
	resp, e := s.client.Do(req)
	if e != nil {
		return ErrObject
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != 200 {
		return ErrObject
	}
	return nil
}
func (s *S3Store) OpenRange(ctx context.Context, key string, start, end int64) ([]byte, error) {
	if !validRange(start, end) {
		return nil, ErrObject
	}
	signed, e := s.signed("GET", key, 60*time.Second, nil, "UNSIGNED-PAYLOAD")
	if e != nil {
		return nil, e
	}
	req, e := http.NewRequestWithContext(ctx, "GET", signed, nil)
	if e != nil {
		return nil, ErrObject
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	resp, e := s.client.Do(req)
	if e != nil {
		return nil, ErrObject
	}
	defer resp.Body.Close()
	if resp.StatusCode != 206 {
		return nil, ErrObject
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, end-start+2))
	if e != nil || int64(len(b)) != end-start+1 {
		return nil, ErrObject
	}
	return b, nil
}
func (s *S3Store) Size(ctx context.Context, key string) (int64, error) {
	signed, e := s.signed("HEAD", key, 60*time.Second, nil, "UNSIGNED-PAYLOAD")
	if e != nil {
		return 0, e
	}
	r, e := http.NewRequestWithContext(ctx, "HEAD", signed, nil)
	if e != nil {
		return 0, ErrObject
	}
	resp, e := s.client.Do(r)
	if e != nil {
		return 0, ErrObject
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.ContentLength < 0 || resp.ContentLength > 80<<20 {
		return 0, ErrObject
	}
	return resp.ContentLength, nil
}
func (s *S3Store) Delete(ctx context.Context, key string) error {
	signed, e := s.signed("DELETE", key, 60*time.Second, nil, "UNSIGNED-PAYLOAD")
	if e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, "DELETE", signed, nil)
	if e != nil {
		return ErrObject
	}
	resp, e := s.client.Do(req)
	if e != nil {
		return ErrObject
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != 204 && resp.StatusCode != 404 {
		return ErrObject
	}
	return nil
}
func (s *S3Store) PresignGet(ctx context.Context, key string, ttl time.Duration, mime, download string) (string, error) {
	if e := ctx.Err(); e != nil {
		return "", e
	}
	if !validMIME(mime) {
		return "", ErrObject
	}
	q := url.Values{"response-content-type": {mime}, "response-cache-control": {"private, no-store"}}
	if mime == "application/pdf" {
		if download == "" || len(download) > 120 || strings.ContainsAny(download, "\r\n\"\\/") {
			return "", ErrObject
		}
		q.Set("response-content-disposition", `attachment; filename="`+download+`"`)
	}
	return s.signed("GET", key, ttl, q, "UNSIGNED-PAYLOAD")
}

// Reference binds a media URL to its current viewer, variant and expiry. The
// caller MUST re-check DB permissions when verifying it; this grants no cohort.
type Reference struct {
	Key      string `json:"key"`
	ViewerID int64  `json:"viewer_id"`
	Variant  string `json:"variant"`
	Expires  int64  `json:"expires"`
	Public   bool   `json:"public,omitempty"`
}
type TokenCodec struct{ Key []byte }

func (c TokenCodec) Sign(r Reference, now time.Time) (string, error) {
	if len(c.Key) < 32 || !validKey(r.Key) || r.ViewerID < 0 || (r.ViewerID == 0 && !r.Public) || (r.ViewerID > 0 && r.Public) || !contains([]string{"main", "thumb", "poster"}, r.Variant) || r.Expires <= now.Unix() || r.Expires > now.Add(10*time.Minute).Unix() {
		return "", ErrObject
	}
	b, e := json.Marshal(r)
	if e != nil {
		return "", ErrObject
	}
	sum := hmacSHA(c.Key, "social-media-reference-v1\n"+string(b))
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(sum), nil
}
func (c TokenCodec) Verify(token string, viewerID int64, now time.Time) (Reference, error) {
	var r Reference
	if len(c.Key) < 32 || len(token) > 1024 || viewerID < 0 {
		return r, ErrObject
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return r, ErrObject
	}
	b, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return r, ErrObject
	}
	mac, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil || !hmac.Equal(mac, hmacSHA(c.Key, "social-media-reference-v1\n"+string(b))) {
		return r, ErrObject
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil || d.Decode(new(any)) != io.EOF || r.ViewerID != viewerID || (viewerID == 0 && !r.Public) || (viewerID > 0 && r.Public) || !validKey(r.Key) || !contains([]string{"main", "thumb", "poster"}, r.Variant) || r.Expires <= now.Unix() || r.Expires > now.Add(10*time.Minute).Unix() {
		return Reference{}, ErrObject
	}
	return r, nil
}
