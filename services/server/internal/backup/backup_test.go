package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
)

const fixtureAccess = "synthetic-backup-access"
const fixtureSecret = "synthetic-backup-secret"

type objectFixture struct {
	data        []byte
	digest, sse string
}
type backupFixture struct {
	mu                       sync.Mutex
	objects                  map[string]objectFixture
	requests                 []string
	corrupt, wrongEncryption bool
	status                   int
	redirect                 string
}

func newBackupFixture(t *testing.T) (*Service, *backupFixture) {
	t.Helper()
	fixture := &backupFixture{objects: map[string]objectFixture{}}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		fixture.requests = append(fixture.requests, r.Method)
		if !fixtureSigned(r) {
			w.WriteHeader(403)
			return
		}
		if fixture.redirect != "" {
			w.Header().Set("Location", fixture.redirect)
			w.WriteHeader(307)
			return
		}
		if fixture.status != 0 {
			w.WriteHeader(fixture.status)
			return
		}
		key := strings.TrimPrefix(r.URL.Path, "/fixture-private-bucket/")
		switch r.Method {
		case "PUT":
			if _, exists := fixture.objects[key]; exists && r.Header.Get("If-None-Match") == "*" {
				w.WriteHeader(412)
				return
			}
			data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				w.WriteHeader(400)
				return
			}
			sum := sha256.Sum256(data)
			if hex.EncodeToString(sum[:]) != r.Header.Get("x-amz-content-sha256") || r.Header.Get("x-amz-acl") != "private" || r.Header.Get("x-amz-server-side-encryption") != "AES256" {
				w.WriteHeader(400)
				return
			}
			fixture.objects[key] = objectFixture{data, r.Header.Get("x-amz-meta-sha256"), r.Header.Get("x-amz-server-side-encryption")}
			w.WriteHeader(200)
		case "GET", "HEAD":
			object, ok := fixture.objects[key]
			if !ok {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("Content-Length", decimalFixture(len(object.data)))
			w.Header().Set("x-amz-meta-sha256", object.digest)
			sse := object.sse
			if fixture.wrongEncryption {
				sse = ""
			}
			w.Header().Set("x-amz-server-side-encryption", sse)
			if r.Method == "GET" {
				data := append([]byte(nil), object.data...)
				if fixture.corrupt {
					data[len(data)-1] ^= 1
				}
				_, _ = w.Write(data)
			}
		case "DELETE":
			if !strings.HasPrefix(key, "backups/probe/") {
				w.WriteHeader(403)
				return
			}
			delete(fixture.objects, key)
			w.WriteHeader(204)
		default:
			w.WriteHeader(405)
		}
	}))
	t.Cleanup(server.Close)
	service, err := New(Config{Storage: media.S3Config{Endpoint: server.URL, Bucket: "fixture-private-bucket", Region: "eu-central-1", AccessKey: fixtureAccess, SecretKey: fixtureSecret, SSE: "AES256", EUResidencyVerified: true, PrivateBucketVerified: true}, Client: server.Client(), Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	return service, fixture
}
func decimalFixture(value int) string {
	if value == 0 {
		return "0"
	}
	out := ""
	for value > 0 {
		out = string(byte('0'+value%10)) + out
		value /= 10
	}
	return out
}

// The synthetic service independently checks the AWS header canonical request,
// including every x-amz header. Merely adding unsigned encryption/private ACL or
// metadata after signing makes the actual HTTP roundtrip fail.
func fixtureSigned(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	parts := strings.Split(strings.TrimPrefix(auth, "AWS4-HMAC-SHA256 "), ", ")
	if len(parts) != 3 || !strings.HasPrefix(parts[0], "Credential="+fixtureAccess+"/") || !strings.HasPrefix(parts[1], "SignedHeaders=") || !strings.HasPrefix(parts[2], "Signature=") {
		return false
	}
	scope := strings.TrimPrefix(parts[0], "Credential="+fixtureAccess+"/")
	fields := strings.Split(scope, "/")
	if len(fields) != 4 || fields[1] != "eu-central-1" || fields[2] != "s3" || fields[3] != "aws4_request" {
		return false
	}
	names := strings.Split(strings.TrimPrefix(parts[1], "SignedHeaders="), ";")
	if !sort.StringsAreSorted(names) {
		return false
	}
	canonicalHeaders := ""
	seen := map[string]bool{}
	for _, name := range names {
		value := r.Header.Get(name)
		if name == "host" {
			value = r.Host
		}
		canonicalHeaders += name + ":" + strings.Join(strings.Fields(value), " ") + "\n"
		seen[name] = true
	}
	for name := range r.Header {
		name = strings.ToLower(name)
		if strings.HasPrefix(name, "x-amz-") && !seen[name] {
			return false
		}
	}
	if r.Method == "PUT" && (!seen["x-amz-acl"] || !seen["x-amz-server-side-encryption"] || !seen["x-amz-meta-sha256"] || !seen["if-none-match"]) {
		return false
	}
	canonical := r.Method + "\n" + r.URL.EscapedPath() + "\n" + r.URL.RawQuery + "\n" + canonicalHeaders + "\n" + strings.Join(names, ";") + "\n" + r.Header.Get("x-amz-content-sha256")
	sum := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + r.Header.Get("x-amz-date") + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
	key := fixtureHMAC([]byte("AWS4"+fixtureSecret), fields[0])
	key = fixtureHMAC(key, fields[1])
	key = fixtureHMAC(key, "s3")
	key = fixtureHMAC(key, "aws4_request")
	return hmac.Equal([]byte(hex.EncodeToString(fixtureHMAC(key, toSign))), []byte(strings.TrimPrefix(parts[2], "Signature=")))
}
func fixtureHMAC(key []byte, value string) []byte {
	hash := hmac.New(sha256.New, key)
	_, _ = hash.Write([]byte(value))
	return hash.Sum(nil)
}
func gzipDump(t *testing.T) string {
	t.Helper()
	var data bytes.Buffer
	writer := gzip.NewWriter(&data)
	_, _ = writer.Write([]byte("-- synthetic PostgreSQL dump\nSELECT 1;\n"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "fixture.sql.gz")
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func privateRecoveryDirectory(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestNativeBackupSignedUploadDownloadAndScopedProbe(t *testing.T) {
	service, fixture := newBackupFixture(t)
	path := gzipDump(t)
	key := Key(time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC))
	receipt, err := service.Upload(context.Background(), path, key)
	if err != nil || !receipt.Verified || receipt.Bytes == 0 {
		t.Fatal("signed upload/HEAD verification failed", err)
	}
	destination := filepath.Join(privateRecoveryDirectory(t), "restored.sql.gz")
	restored, err := service.Download(context.Background(), key, destination)
	if err != nil || restored != receipt {
		t.Fatal("backup download/hash contract failed", err)
	}
	before, _ := os.ReadFile(path)
	after, _ := os.ReadFile(destination)
	if !bytes.Equal(before, after) {
		t.Fatal("backup recovery changed dump bytes")
	}
	info, _ := os.Stat(destination)
	if info.Mode().Perm() != 0600 {
		t.Fatal("backup recovery file not private")
	}
	probe, err := service.Probe(context.Background())
	if err != nil || !probe.Verified || !strings.HasPrefix(probe.Key, "backups/probe/") {
		t.Fatal("scoped probe failed", err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.objects) != 1 || fixture.objects[key].digest != receipt.SHA256 {
		t.Fatal("probe removed dump or left probe object")
	}
	if len(fixture.requests) != 6 {
		t.Fatal("unexpected network request count")
	}
}
func TestNativeBackupNeverOverwritesExistingDumpOrDownload(t *testing.T) {
	service, _ := newBackupFixture(t)
	path := gzipDump(t)
	key := Key(time.Now())
	if _, err := service.Upload(context.Background(), path, key); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Upload(context.Background(), path, key); err == nil {
		t.Fatal("conditional backup upload overwrote existing object")
	}
	if _, err := service.Download(context.Background(), key, path); err == nil {
		t.Fatal("download overwrote existing local file")
	}
}
func TestNativeBackupRejectsFilesKeysAndConfigurationWithoutIO(t *testing.T) {
	service, fixture := newBackupFixture(t)
	path := gzipDump(t)
	key := Key(time.Now())
	link := filepath.Join(t.TempDir(), "symlink.sql.gz")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{link, filepath.Dir(path), "relative.sql.gz"} {
		if _, err := service.Upload(context.Background(), input, key); err == nil {
			t.Fatal("unsafe source accepted")
		}
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Upload(context.Background(), path, key); err == nil {
		t.Fatal("public-readable dump accepted")
	}
	_ = os.Chmod(path, 0600)
	for _, key := range []string{"photos/1", "backups/db/../../private", "backups/db/socialapp-db-20260231T120000Z.sql.gz", "backups/db/socialapp-db-20261005T120000Z.sql.gz?public=true"} {
		if _, err := service.Upload(context.Background(), path, key); err == nil {
			t.Fatal("unreviewed backup key accepted")
		}
	}
	service.maxBytes = 1
	if _, err := service.Upload(context.Background(), path, key); err == nil {
		t.Fatal("oversize backup accepted")
	}
	if len(fixture.requests) != 0 {
		t.Fatal("invalid inputs performed I/O")
	}
	base := media.S3Config{Endpoint: "https://eu-storage.fixture.test", Bucket: "private-backups", Region: "eu-central-1", AccessKey: fixtureAccess, SecretKey: fixtureSecret, SSE: "AES256", EUResidencyVerified: true, PrivateBucketVerified: true}
	for _, change := range []func(*media.S3Config){func(c *media.S3Config) { c.Endpoint = "http://eu-storage.fixture.test" }, func(c *media.S3Config) { c.Endpoint = "https://eu-storage.fixture.test/path" }, func(c *media.S3Config) { c.EUResidencyVerified = false }, func(c *media.S3Config) { c.PrivateBucketVerified = false }, func(c *media.S3Config) { c.SSE = "" }, func(c *media.S3Config) { c.SSE = "unsupported" }, func(c *media.S3Config) { c.Endpoint = "https://bucket.cloudflarestorage.com" }} {
		config := base
		change(&config)
		if _, err := New(Config{Storage: config}); err == nil {
			t.Fatal("unsafe backup configuration accepted")
		}
	}
}
func TestNativeBackupCorruptionAndEncryptionFailureRemovePartialRecovery(t *testing.T) {
	service, fixture := newBackupFixture(t)
	path := gzipDump(t)
	key := Key(time.Now())
	if _, err := service.Upload(context.Background(), path, key); err != nil {
		t.Fatal(err)
	}
	for _, encryption := range []bool{false, true} {
		fixture.mu.Lock()
		fixture.corrupt = !encryption
		fixture.wrongEncryption = encryption
		fixture.mu.Unlock()
		destination := filepath.Join(privateRecoveryDirectory(t), "partial.sql.gz")
		if _, err := service.Download(context.Background(), key, destination); err == nil {
			t.Fatal("corrupt/unencrypted recovery accepted")
		}
		if _, err := os.Stat(destination); !os.IsNotExist(err) {
			t.Fatal("partial recovery retained")
		}
	}
}
func TestNativeBackupTimeoutCancellationRedirectAndNoRetries(t *testing.T) {
	service, fixture := newBackupFixture(t)
	path := gzipDump(t)
	key := Key(time.Now())
	fixture.status = 503
	if _, err := service.Upload(context.Background(), path, key); err == nil || strings.Contains(err.Error(), fixtureSecret) {
		t.Fatal("storage failure/diagnostic contract")
	}
	if len(fixture.requests) != 1 {
		t.Fatal("backup upload retried ambiguous request")
	}
	fixture.status = 0
	fixture.redirect = "https://outside.invalid/private"
	if _, err := service.Upload(context.Background(), path, key); err == nil {
		t.Fatal("redirect followed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Upload(ctx, path, key); err == nil {
		t.Fatal("canceled upload continued")
	}
	if len(fixture.requests) != 2 {
		t.Fatal("canceled request or redirect escaped explicit boundary")
	}
}
