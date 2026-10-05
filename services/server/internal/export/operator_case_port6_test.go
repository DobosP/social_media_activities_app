package export

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOperatorCase6ExportAtomicExactCompactBytesAndFailedReplace(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "replace", true: "failed_replace"}[fail], func(t *testing.T) {
			dir := t.TempDir()
			final := filepath.Join(dir, "places.json")
			old := []byte("previous generation")
			if err := os.WriteFile(final, old, 0600); err != nil {
				t.Fatal(err)
			}
			payload := struct {
				Name  string `json:"name"`
				Line  string `json:"line"`
				Count int    `json:"count"`
			}{"Bibliotecă", "a\nb", 1}
			expected := []byte(`{"name":"Bibliotecă","line":"a\nb","count":1}`)
			replaces := 0
			digest, err := writeJSONWithReplace(dir, "places.json", payload, func(source, target string) error {
				replaces++
				prior, e := os.ReadFile(final)
				if e != nil || !bytes.Equal(prior, old) {
					t.Fatal("final generation changed before replacement")
				}
				staged, e := os.ReadFile(source)
				if e != nil || !bytes.Equal(staged, expected) || target != final {
					t.Fatal("actual temporary bytes/target differ from exact source contract")
				}
				if info, e := os.Stat(source); e != nil || info.Mode().Perm() != 0644 {
					t.Fatal("synced/closed public staging file has wrong mode")
				}
				if fail {
					return errors.New("synthetic replacement failure")
				}
				return os.Rename(source, target)
			})
			actual, e := os.ReadFile(final)
			if e != nil {
				t.Fatal(e)
			}
			if replaces != 1 {
				t.Fatal("publication replacement count changed")
			}
			if fail {
				if err == nil || digest != "" || !bytes.Equal(actual, old) {
					t.Fatal("failed replace reported hash/success or lost prior generation")
				}
			} else {
				sum := sha256.Sum256(expected)
				if err != nil || digest != hex.EncodeToString(sum[:]) || !bytes.Equal(actual, expected) {
					t.Fatal("digest did not bind exact atomically replaced compact UTF8 bytes", err)
				}
			}
			entries, e := os.ReadDir(dir)
			if e != nil || len(entries) != 1 || entries[0].Name() != "places.json" {
				t.Fatal("atomic publisher retained temporary artifacts")
			}
		})
	}
}
