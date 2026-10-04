package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestNativeBackupSignerScopesAndAuthenticatedEncryptionMetadata(t *testing.T) {
	signer, err := NewS3RequestSigner(S3Config{Endpoint: "https://eu-storage.fixture.test", Bucket: "fixture-private-bucket", Region: "eu-central-1", AccessKey: "synthetic-only", SecretKey: "synthetic-only", SessionToken: "synthetic-session", SSE: "AES256", AddressingStyle: "virtual", EUResidencyVerified: true, PrivateBucketVerified: true})
	if err != nil {
		t.Fatal(err)
	}
	data := "synthetic backup"
	sum := sha256.Sum256([]byte(data))
	digest := hex.EncodeToString(sum[:])
	r, err := signer.Request(context.Background(), http.MethodPut, "backups/db/socialapp-db-20261005T120000Z.sql.gz", digest, strings.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if r.URL.Host != "fixture-private-bucket.eu-storage.fixture.test" || !strings.HasPrefix(r.URL.Path, "/backups/db/") || r.URL.RawQuery != "" {
		t.Fatal("backup signer changed endpoint/addressing boundary")
	}
	for _, name := range []string{"x-amz-server-side-encryption", "x-amz-meta-sha256", "x-amz-acl", "x-amz-security-token", "if-none-match"} {
		if !strings.Contains(r.Header.Get("Authorization"), name) {
			t.Fatal("backup security header omitted from signature")
		}
	}
	if r.Header.Get("x-amz-acl") != "private" || r.Header.Get("If-None-Match") != "*" || r.Header.Get("x-amz-meta-sha256") != digest {
		t.Fatal("backup object security policy omitted")
	}
	for _, input := range []struct {
		method, key, digest string
		body                io.Reader
		size                int64
	}{
		{"DELETE", "backups/db/socialapp-db-20261005T120000Z.sql.gz", "UNSIGNED-PAYLOAD", nil, 0},
		{"DELETE", "photos/1", "UNSIGNED-PAYLOAD", nil, 0},
		{"DELETE", "backups/probe/../db", "UNSIGNED-PAYLOAD", nil, 0},
		{"POST", "backups/probe/fixture.probe.gz", "UNSIGNED-PAYLOAD", nil, 0},
		{"PUT", "backups/probe/fixture.probe.gz", "invalid", strings.NewReader(data), 1},
		{"PUT", "backups/probe/fixture.probe.gz", digest, strings.NewReader(data), 4<<30 + 1},
	} {
		if _, err := signer.Request(context.Background(), input.method, input.key, input.digest, input.body, input.size); err == nil {
			t.Fatal("unreviewed signer operation accepted")
		}
	}
}
