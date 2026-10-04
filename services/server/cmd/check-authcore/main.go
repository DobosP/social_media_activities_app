// check-authcore verifies the reviewed, portable authentication snapshot.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func verify(snapshot, canonical string) error {
	var manifest struct {
		Module string            `json:"canonical_module"`
		Path   string            `json:"canonical_path"`
		Files  map[string]string `json:"files_sha256"`
	}
	raw, err := os.ReadFile(filepath.Join(snapshot, "SOURCE.json"))
	if err != nil {
		return errors.New("snapshot manifest unavailable")
	}
	if json.Unmarshal(raw, &manifest) != nil || manifest.Module != "github.com/DobosP/cat_de_roman_esti/shared-go/authcore" || manifest.Path != "shared-go/authcore" || len(manifest.Files) == 0 {
		return errors.New("snapshot manifest invalid")
	}
	for name, expected := range manifest.Files {
		if !fs.ValidPath(name) || name == "SOURCE.json" || len(expected) != 64 {
			return errors.New("snapshot file identity invalid")
		}
		for _, dir := range []string{snapshot, canonical} {
			if dir == "" {
				continue
			}
			info, err := os.Lstat(filepath.Join(dir, name))
			if err != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("snapshot file missing: %s", name)
			}
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				return fmt.Errorf("snapshot file unreadable: %s", name)
			}
			sum := sha256.Sum256(data)
			if hex.EncodeToString(sum[:]) != expected {
				return fmt.Errorf("snapshot hash mismatch: %s", name)
			}
		}
	}
	return filepath.WalkDir(snapshot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return errors.New("snapshot directory unavailable")
		}
		if entry.IsDir() {
			return nil
		}
		name, err := filepath.Rel(snapshot, path)
		if err != nil {
			return err
		}
		name = filepath.ToSlash(name)
		if name != "SOURCE.json" && manifest.Files[name] == "" {
			return fmt.Errorf("unreviewed snapshot file: %s", name)
		}
		return nil
	})
}

func main() {
	snapshot := flag.String("snapshot", "../authcore", "portable snapshot directory")
	canonical := flag.String("canonical", "", "optional canonical module directory for a cross-repo comparison")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected argument")
		os.Exit(1)
	}
	if err := verify(*snapshot, *canonical); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("authentication snapshot hashes verified")
}
