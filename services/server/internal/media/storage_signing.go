package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"sort"
	"strings"
)

// S3RequestSigner reuses the media store's reviewed endpoint, residency,
// bucket, SigV4 and SSE policy for explicit operator storage adapters. It sends
// no traffic and does not expose a public URL or weaken media upload limits.
type S3RequestSigner struct{ store *S3Store }

func NewS3RequestSigner(config S3Config) (*S3RequestSigner, error) {
	store, err := NewS3Store(config)
	if err != nil {
		return nil, err
	}
	return &S3RequestSigner{store}, nil
}

func (s *S3RequestSigner) Request(ctx context.Context, method, key, digest string, body io.Reader, size int64) (*http.Request, error) {
	if s == nil || s.store == nil || ctx.Err() != nil || !strings.HasPrefix(key, "backups/") || size < 0 || size > 4<<30 {
		return nil, ErrObject
	}
	switch method {
	case http.MethodPut:
		if body == nil || size == 0 || !digestRE.MatchString(digest) {
			return nil, ErrObject
		}
	case http.MethodGet, http.MethodHead, http.MethodDelete:
		if body != nil || size != 0 || digest != "UNSIGNED-PAYLOAD" {
			return nil, ErrObject
		}
		if method == http.MethodDelete && (!strings.HasPrefix(key, "backups/probe/") || !strings.HasSuffix(key, ".probe.gz") || strings.Contains(strings.TrimPrefix(key, "backups/probe/"), "/")) {
			return nil, ErrObject
		}
	default:
		return nil, ErrObject
	}
	if !validKey(key) {
		return nil, ErrObject
	}
	u := *s.store.endpoint
	u.Path = "/" + s.store.cfg.Bucket + "/" + key
	if s.store.cfg.AddressingStyle == "virtual" {
		u.Host = s.store.cfg.Bucket + "." + u.Host
		u.Path = "/" + key
	}
	u.RawPath = ""
	r, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, ErrObject
	}
	if method == http.MethodPut {
		r.ContentLength = size
		r.Header.Set("Content-Type", "application/gzip")
		r.Header.Set("x-amz-content-sha256", digest)
		if s.store.cfg.SSE != "" {
			r.Header.Set("x-amz-server-side-encryption", s.store.cfg.SSE)
		}
		r.Header.Set("x-amz-meta-sha256", digest)
		r.Header.Set("x-amz-acl", "private")
		r.Header.Set("If-None-Match", "*")
	}
	// The operator uses header SigV4 so metadata, private ACL, encryption and
	// conditional creation all enter the authenticated canonical request. The
	// endpoint policy and crypto/escaping primitives are shared with media.
	now := s.store.now().UTC()
	date := now.Format("20060102")
	stamp := now.Format("20060102T150405Z")
	r.Header.Set("x-amz-date", stamp)
	r.Header.Set("x-amz-content-sha256", digest)
	if s.store.cfg.SessionToken != "" {
		r.Header.Set("x-amz-security-token", s.store.cfg.SessionToken)
	}
	headers := map[string]string{"host": r.URL.Host}
	for key, values := range r.Header {
		name := strings.ToLower(key)
		if strings.HasPrefix(name, "x-amz-") || name == "if-none-match" {
			headers[name] = strings.Join(values, ",")
		}
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	canonicalHeaders := ""
	for _, name := range names {
		canonicalHeaders += name + ":" + strings.Join(strings.Fields(headers[name]), " ") + "\n"
	}
	signedHeaders := strings.Join(names, ";")
	canonical := method + "\n" + r.URL.EscapedPath() + "\n\n" + canonicalHeaders + "\n" + signedHeaders + "\n" + digest
	sum := sha256.Sum256([]byte(canonical))
	scope := date + "/" + s.store.cfg.Region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
	r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+s.store.cfg.AccessKey+"/"+scope+", SignedHeaders="+signedHeaders+", Signature="+hex.EncodeToString(hmacSHA(signingKey(s.store.cfg.SecretKey, date, s.store.cfg.Region), toSign)))
	return r, nil
}
