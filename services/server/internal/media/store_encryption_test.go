package media

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type storageRoundTrip func(*http.Request) (*http.Response, error)

func (f storageRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPrivateS3EncryptionIsSignedAndVirtualAddressingPreservesKeys(t *testing.T) {
	var calls int
	cfg := S3Config{Endpoint: "https://objects.eu.fixture.invalid", Bucket: "private-fixture", Region: "eu-test-1", AccessKey: "synthetic-access", SecretKey: "synthetic-secret", EUResidencyVerified: true, PrivateBucketVerified: true, SSE: "AES256", AddressingStyle: "virtual"}
	cfg.Client = &http.Client{Transport: storageRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "private-fixture.objects.eu.fixture.invalid" || r.URL.Path != "/attachments/fixture.png" || r.Header.Get("x-amz-server-side-encryption") != "AES256" || r.URL.Query().Get("X-Amz-SignedHeaders") != "host;x-amz-content-sha256;x-amz-server-side-encryption" {
			t.Fatal("private encryption/addressing contract lost")
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "synthetic-bytes" {
			t.Fatal("PUT payload changed")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	s, err := NewS3Store(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "fixture.png")
	if err = os.WriteFile(path, []byte("synthetic-bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.Put(context.Background(), "attachments/fixture.png", path, "image/png"); err != nil || calls != 1 {
		t.Fatal("signed private PUT failed", err)
	}
	s.now = func() time.Time { return time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC) }
	a, _ := s.signed("PUT", "attachments/fixture.png", time.Minute, nil, strings.Repeat("a", 64))
	s.cfg.SSE = "aws:kms"
	b, _ := s.signed("PUT", "attachments/fixture.png", time.Minute, nil, strings.Repeat("a", 64))
	au, _ := url.Parse(a)
	bu, _ := url.Parse(b)
	if au.Query().Get("X-Amz-Signature") == bu.Query().Get("X-Amz-Signature") {
		t.Fatal("encryption can be changed without invalidating signature")
	}
	get, _ := s.signed("GET", "attachments/fixture.png", time.Minute, nil, "UNSIGNED-PAYLOAD")
	gu, _ := url.Parse(get)
	if strings.Contains(gu.Query().Get("X-Amz-SignedHeaders"), "encryption") {
		t.Fatal("GET incorrectly requires an upload encryption header")
	}
	for _, invalid := range []string{"garbage", "AES256\r\nheader:value"} {
		cfg.SSE = invalid
		if _, err := NewS3Store(cfg); err == nil {
			t.Fatal("invalid encryption accepted")
		}
	}
}
