// Package backup is an explicit native operator boundary for private EU
// PostgreSQL dump objects. It has no scheduler and never changes the database.
package backup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
)

var ErrConfiguration = errors.New("private EU backup configuration invalid")
var ErrFile = errors.New("backup file must be a private regular gzip dump within the size limit")
var ErrStorage = errors.New("backup storage operation failed")

const DefaultMaxBytes int64 = 1 << 30
const AbsoluteMaxBytes int64 = 4 << 30
const DefaultTimeout = 15 * time.Minute

type Config struct {
	Storage  media.S3Config
	MaxBytes int64
	Timeout  time.Duration
	Client   *http.Client
}
type Service struct {
	signer   *media.S3RequestSigner
	client   *http.Client
	maxBytes int64
	timeout  time.Duration
	sse      string
}
type Receipt struct {
	Key      string `json:"key"`
	Bytes    int64  `json:"bytes"`
	SHA256   string `json:"sha256"`
	Verified bool   `json:"verified"`
}

func New(config Config) (*Service, error) {
	if config.MaxBytes == 0 {
		config.MaxBytes = DefaultMaxBytes
	}
	if config.Timeout == 0 {
		config.Timeout = DefaultTimeout
	}
	// Encryption is mandatory for dumps even when media's chosen provider relies
	// on bucket encryption. Operators must name their reviewed supported SSE.
	if config.Storage.SSE == "" || config.MaxBytes < 1 || config.MaxBytes > AbsoluteMaxBytes || config.Timeout < time.Second || config.Timeout > time.Hour {
		return nil, ErrConfiguration
	}
	signer, err := media.NewS3RequestSigner(config.Storage)
	if err != nil {
		return nil, ErrConfiguration
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = 30 * time.Second
	transport.MaxResponseHeaderBytes = 16 << 10
	client := &http.Client{Transport: transport, Timeout: config.Timeout}
	if config.Client != nil {
		*client = *config.Client
		client.Timeout = config.Timeout
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Service{signer, client, config.MaxBytes, config.Timeout, config.Storage.SSE}, nil
}

var dumpKey = regexp.MustCompile(`^backups/db/socialapp-db-[0-9]{8}T[0-9]{6}Z\.sql\.gz$`)

func ValidKey(key string) bool {
	if !dumpKey.MatchString(key) {
		return false
	}
	stamp := strings.TrimSuffix(strings.TrimPrefix(key, "backups/db/socialapp-db-"), ".sql.gz")
	_, err := time.Parse("20060102T150405Z", stamp)
	return err == nil
}
func Key(at time.Time) string {
	return "backups/db/socialapp-db-" + at.UTC().Format("20060102T150405Z") + ".sql.gz"
}

func privateFile(path string) (*os.File, error) {
	if !filepath.IsAbs(path) {
		return nil, ErrFile
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrFile
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() < 1 {
		f.Close()
		return nil, ErrFile
	}
	return f, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func (s *Service) Upload(ctx context.Context, path, key string) (Receipt, error) {
	if !ValidKey(key) {
		return Receipt{}, ErrFile
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	f, err := privateFile(path)
	if err != nil {
		return Receipt{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() > s.maxBytes {
		return Receipt{}, ErrFile
	}
	var header [2]byte
	if _, err = io.ReadFull(f, header[:]); err != nil || header != [2]byte{0x1f, 0x8b} {
		return Receipt{}, ErrFile
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return Receipt{}, ErrFile
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(contextReader{ctx, f}, s.maxBytes+1))
	if err != nil || n != info.Size() || n > s.maxBytes {
		return Receipt{}, ErrFile
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return Receipt{}, ErrFile
	}
	receipt := Receipt{Key: key, Bytes: n, SHA256: hex.EncodeToString(hash.Sum(nil))}
	if err = s.put(ctx, receipt, contextReader{ctx, io.LimitReader(f, n+1)}); err != nil {
		return Receipt{}, err
	}
	// HEAD confirms persisted bytes and server-side encryption; an explicit
	// download/probe additionally verifies streamed content against its digest.
	if err = s.head(ctx, receipt); err != nil {
		return Receipt{}, err
	}
	receipt.Verified = true
	return receipt, nil
}

func (s *Service) put(ctx context.Context, receipt Receipt, body io.Reader) error {
	r, err := s.signer.Request(ctx, http.MethodPut, receipt.Key, receipt.SHA256, body, receipt.Bytes)
	if err != nil {
		return ErrStorage
	}
	r.Header.Set("x-amz-meta-sha256", receipt.SHA256)
	r.Header.Set("x-amz-acl", "private")
	response, err := s.client.Do(r)
	if err != nil {
		return ErrStorage
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != 200 && response.StatusCode != 201 {
		return ErrStorage
	}
	return nil
}
func (s *Service) head(ctx context.Context, receipt Receipt) error {
	r, err := s.signer.Request(ctx, http.MethodHead, receipt.Key, "UNSIGNED-PAYLOAD", nil, 0)
	if err != nil {
		return ErrStorage
	}
	response, err := s.client.Do(r)
	if err != nil {
		return ErrStorage
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.ContentLength != receipt.Bytes || response.Header.Get("x-amz-meta-sha256") != receipt.SHA256 || response.Header.Get("x-amz-server-side-encryption") != s.sse {
		return ErrStorage
	}
	return nil
}

func (s *Service) Download(ctx context.Context, key, path string) (Receipt, error) {
	if !ValidKey(key) || !filepath.IsAbs(path) {
		return Receipt{}, ErrFile
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !parent.IsDir() || parent.Mode().Perm()&0077 != 0 {
		return Receipt{}, ErrFile
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return Receipt{}, ErrFile
	}
	complete := false
	defer func() {
		f.Close()
		if !complete {
			_ = os.Remove(path)
		}
	}()
	receipt, err := s.download(ctx, key, f)
	if err != nil {
		return Receipt{}, err
	}
	if err = f.Sync(); err != nil {
		return Receipt{}, ErrFile
	}
	if err = f.Close(); err != nil {
		return Receipt{}, ErrFile
	}
	complete = true
	return receipt, nil
}
func (s *Service) download(ctx context.Context, key string, destination io.Writer) (Receipt, error) {
	r, err := s.signer.Request(ctx, http.MethodGet, key, "UNSIGNED-PAYLOAD", nil, 0)
	if err != nil {
		return Receipt{}, ErrStorage
	}
	response, err := s.client.Do(r)
	if err != nil {
		return Receipt{}, ErrStorage
	}
	defer response.Body.Close()
	want := response.Header.Get("x-amz-meta-sha256")
	if response.StatusCode != 200 || response.ContentLength < 1 || response.ContentLength > s.maxBytes || len(want) != 64 || response.Header.Get("x-amz-server-side-encryption") != s.sse {
		return Receipt{}, ErrStorage
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(destination, hash), io.LimitReader(contextReader{ctx, response.Body}, s.maxBytes+1))
	if err != nil || n != response.ContentLength || n > s.maxBytes || hex.EncodeToString(hash.Sum(nil)) != want {
		return Receipt{}, ErrStorage
	}
	return Receipt{key, n, want, true}, nil
}

// Probe touches only a cryptographically random probe key, roundtrips synthetic
// bytes, and deletes only that exact key. It cannot delete dump objects.
func (s *Service) Probe(ctx context.Context) (Receipt, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	key := "backups/probe/" + rand.Text() + ".probe.gz"
	data := []byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xff, 0x01, 0x00, 0x00, 0xff, 0xff, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	hash := sha256.Sum256(data)
	receipt := Receipt{key, int64(len(data)), hex.EncodeToString(hash[:]), false}
	if err := s.put(ctx, receipt, strings.NewReader(string(data))); err != nil {
		return Receipt{}, err
	}
	readback, err := s.download(ctx, key, io.Discard)
	cleanup, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancelCleanup()
	r, signErr := s.signer.Request(cleanup, http.MethodDelete, key, "UNSIGNED-PAYLOAD", nil, 0)
	if signErr != nil {
		return Receipt{}, ErrStorage
	}
	response, deleteErr := s.client.Do(r)
	if deleteErr != nil {
		return Receipt{}, ErrStorage
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != 204 && response.StatusCode != 404 || err != nil || readback.Bytes != receipt.Bytes || readback.SHA256 != receipt.SHA256 {
		return Receipt{}, ErrStorage
	}
	receipt.Verified = true
	return receipt, nil
}

func (s *Service) Close() error { s.client.CloseIdleConnections(); return nil }
func (r Receipt) String() string {
	return fmt.Sprintf("backup bytes=%d verified=%t", r.Bytes, r.Verified)
}
