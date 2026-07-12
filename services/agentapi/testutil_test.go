package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
)

// copyTestdata copies every file from services/agentapi/testdata into a
// fresh temp directory and returns that directory's path. Tests mutate the
// copy freely (e.g. for reload/corruption scenarios) without touching the
// checked-in fixtures.
func copyTestdata(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		copyFile(t, filepath.Join("testdata", e.Name()), filepath.Join(dst, e.Name()))
	}
	return dst
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatalf("open %s: %v", src, err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		t.Fatalf("create %s: %v", dst, err)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		t.Fatalf("copy %s -> %s: %v", src, dst, err)
	}
}

func testLogger() *log.Logger {
	return log.New(io.Discard, "", 0)
}

// newLoadedLoader copies testdata into a fresh temp dir, loads it, and
// returns the loader plus the directory (so tests can further mutate it for
// reload scenarios).
func newLoadedLoader(t *testing.T) (*Loader, string) {
	t.Helper()
	dir := copyTestdata(t)
	loader := NewLoader(dir, testLogger())
	if _, err := loader.CheckReload(); err != nil {
		t.Fatalf("initial load failed: %v", err)
	}
	if loader.Current() == nil {
		t.Fatal("expected a snapshot to be loaded")
	}
	return loader, dir
}

func testConfig() Config {
	return Config{
		Addr:           ":0",
		SnapshotDir:    "",
		ReloadInterval: 30,
		RatePerMin:     300,
		RateBurst:      60,
		TrustProxy:     false,
		MaxLimit:       200,
	}
}
