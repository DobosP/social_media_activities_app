// Package contracts checks preservation evidence for the retired reference
// harness. It reads source as data and never executes a Python interpreter.
package contracts

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type LegacyCase struct {
	ID         string `json:"legacy_id"`
	File       string `json:"legacy_file"`
	Line       int    `json:"line"`
	SourceHash string `json:"source_sha256"`
}

type Inventory struct {
	Version    int          `json:"schema_version"`
	SourceHead string       `json:"source_head"`
	Cases      []LegacyCase `json:"cases"`
}

var testDeclaration = regexp.MustCompile(`^(?:async\s+)?def\s+(test_[A-Za-z0-9_]+)\s*\(`)
var classDeclaration = regexp.MustCompile(`^class\s+([A-Za-z_][A-Za-z0-9_]*)\b`)

type stringState struct {
	quote  byte
	triple bool
}

// advance treats comments/quoted source as data, including multiline docstrings
// containing example test declarations. No imports, decorators or fixtures run.
func (s *stringState) advance(line string) {
	for i := 0; i < len(line); i++ {
		c := line[i]
		if s.quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == s.quote && (!s.triple || i+2 < len(line) && line[i+1] == c && line[i+2] == c) {
				if s.triple {
					i += 2
				}
				s.quote, s.triple = 0, false
			}
			continue
		}
		if c == '#' {
			return
		}
		if c == '\'' || c == '"' {
			s.quote = c
			s.triple = i+2 < len(line) && line[i+1] == c && line[i+2] == c
			if s.triple {
				i += 2
			}
		}
	}
}

func SourceCases(path string, raw []byte) ([]LegacyCase, error) {
	digest := sha256.Sum256(raw)
	hash := hex.EncodeToString(digest[:])
	state := stringState{}
	type scope struct {
		name   string
		indent int
	}
	classes := []scope{}
	cases, seen := []LegacyCase{}, map[string]bool{}
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	scanner.Buffer(make([]byte, 4096), 2<<20)
	line := 0
	for scanner.Scan() {
		line++
		text := scanner.Text()
		trimmed := strings.TrimSpace(text)
		if state.quote == 0 && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			indent := len(text) - len(strings.TrimLeft(text, " \t"))
			for len(classes) > 0 && indent <= classes[len(classes)-1].indent {
				classes = classes[:len(classes)-1]
			}
			if match := classDeclaration.FindStringSubmatch(trimmed); match != nil {
				classes = append(classes, scope{match[1], indent})
			}
			if match := testDeclaration.FindStringSubmatch(trimmed); match != nil {
				parts := []string{filepath.ToSlash(path)}
				for _, c := range classes {
					parts = append(parts, c.name)
				}
				parts = append(parts, match[1])
				id := strings.Join(parts, "::")
				if seen[id] {
					return nil, fmt.Errorf("duplicate legacy case %s", id)
				}
				seen[id] = true
				cases = append(cases, LegacyCase{id, filepath.ToSlash(path), line, hash})
			}
		}
		state.advance(text)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return cases, nil
}

func testFile(name string) bool {
	return name == "tests.py" || strings.HasPrefix(name, "test_") && strings.HasSuffix(name, ".py") || strings.HasSuffix(name, "_tests.py")
}

// ScanInventory establishes a complete immutable case-ID baseline before any
// required reference code can be retired. Counts are source declarations, not a
// claim about pytest parameter expansion or native scenario equivalence.
func ScanInventory(root, head string) (Inventory, error) {
	out := Inventory{Version: 1, SourceHead: head, Cases: []LegacyCase{}}
	for _, top := range []string{"apps", "tests"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !testFile(entry.Name()) {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("legacy source cannot be a symlink: %s", path)
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			cases, err := SourceCases(rel, raw)
			if err != nil {
				return err
			}
			out.Cases = append(out.Cases, cases...)
			return nil
		})
		if err != nil {
			return Inventory{}, err
		}
	}
	sort.Slice(out.Cases, func(i, j int) bool { return out.Cases[i].ID < out.Cases[j].ID })
	return out, nil
}
