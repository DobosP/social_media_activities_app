package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCSPDigestCLIFileStdinAndGoldenFormatsWithoutDatabase(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		for _, file := range []bool{false, true} {
			t.Run(format+map[bool]string{false: "/stdin", true: "/file"}[file], func(t *testing.T) {
				input, err := os.ReadFile("testdata/csp_digest.jsonl")
				if err != nil {
					t.Fatal(err)
				}
				expected, err := os.ReadFile("testdata/csp_digest.expected." + format)
				if err != nil {
					t.Fatal(err)
				}
				args := []string{"--csp-digest", "--format", format}
				stdin := io.Reader(bytes.NewReader(input))
				if file {
					args = append(args, "--input", "testdata/csp_digest.jsonl")
					stdin = strings.NewReader("not used")
				}
				var output bytes.Buffer
				get := func(name string) string { t.Fatalf("DB-free CSP digest consulted environment %s", name); return "" }
				if err = run(context.Background(), args, get, stdin, &output, io.Discard); err != nil {
					t.Fatal(err)
				}
				if format == "json" {
					var got, want any
					if json.Unmarshal(output.Bytes(), &got) != nil || json.Unmarshal(expected, &want) != nil {
						t.Fatal("invalid golden JSON")
					}
					if !bytes.Equal(marshalFixture(got), marshalFixture(want)) {
						t.Fatal("JSON digest differs from golden")
					}
				} else if !bytes.Equal(output.Bytes(), expected) {
					t.Fatalf("text digest differs: %q", output.String())
				}
				if strings.Contains(output.String(), "discard") || strings.Contains(output.String(), "#fragment") || strings.Contains(output.String(), "userinfo") {
					t.Fatal("CSP sanitizer leaked raw fields")
				}
			})
		}
	}
}
func marshalFixture(value any) []byte { raw, _ := json.Marshal(value); return raw }

func TestCSPDigestCLIEmptyMalformedInvalidAndBoundedInputs(t *testing.T) {
	get := func(name string) string { t.Fatalf("operator validation consulted environment %s", name); return "" }
	for _, args := range [][]string{{"--csp-digest", "--format", "invalid"}, {"--format", "json"}, {"--csp-digest", "--due"}, {"--csp-digest", "--backup-probe"}, {"--backup-download", "backups/db/x.sql.gz"}, {"--csp-digest", "--input", "absent-fixture-file"}} {
		if err := run(context.Background(), args, get, strings.NewReader("{}"), io.Discard, io.Discard); err == nil {
			t.Fatal("invalid standalone flags accepted")
		}
	}
	for _, input := range []string{"", "not json\n1\n", strings.Repeat(" ", 16<<20+1)} {
		var output bytes.Buffer
		err := run(context.Background(), []string{"--csp-digest", "--format", "json"}, get, strings.NewReader(input), &output, io.Discard)
		if len(input) > 16<<20 {
			if err == nil {
				t.Fatal("oversized digest accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		var result struct{ Total, Malformed int }
		if json.Unmarshal(output.Bytes(), &result) != nil {
			t.Fatal("invalid digest output")
		}
		if input != "" && result.Malformed != 2 || result.Total != 0 {
			t.Fatal("malformed/empty digest count")
		}
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "report.json")
	if err := os.WriteFile(target, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"--csp-digest", "--input", link}, get, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("symlink digest input accepted")
	}
	if err := run(context.Background(), []string{"--csp-digest", "--input", dir}, get, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("directory digest input accepted")
	}
}

func TestNativeBackupCLIConfigurationFailsClosedBeforeDatabase(t *testing.T) {
	for _, args := range [][]string{{"--backup-probe"}, {"--backup-upload", "/fixture/absent.sql.gz"}, {"--backup-download", "backups/db/socialapp-db-20261005T120000Z.sql.gz", "--backup-file", "/fixture/absent.sql.gz"}} {
		get := func(name string) string {
			if name == "DATABASE_URL" || name == "DJANGO_SECRET_KEY" {
				t.Fatalf("backup command booted application %s", name)
			}
			return ""
		}
		if err := run(context.Background(), args, get, nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "MEDIA_EU_RESIDENCY_VERIFIED") {
			t.Fatal("backup configuration did not fail closed")
		}
	}
	for _, args := range [][]string{{"--backup-probe", "--due"}, {"--backup-probe", "--backup-max-bytes", "4294967297"}, {"--backup-probe", "--backup-timeout", "2h"}, {"--backup-key", "backups/db/invalid"}} {
		if _, err := parseCLI(args, io.Discard); err == nil {
			t.Fatal("invalid backup flags accepted")
		}
	}
	o, err := parseCLI([]string{"--csp-digest"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err = runCSPDigest(context.Background(), o, strings.NewReader("{}"), failingOperatorWriter{}); err == nil {
		t.Fatal("output failure ignored")
	}
}

type failingOperatorWriter struct{}

func (failingOperatorWriter) Write([]byte) (int, error) {
	return 0, errors.New("synthetic output unavailable")
}
