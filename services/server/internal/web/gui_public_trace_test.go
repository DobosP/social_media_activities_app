package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/flosch/pongo2/v6"
)

// This observes the original loader, not template branches or a replacement
// renderer. The exact transformed stream is returned to Pongo without editing.
type guiPublicTemplateLoad struct {
	Name   string `json:"name"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type guiPublicTraceLoader struct {
	delegate pongo2.TemplateLoader
	loads    []guiPublicTemplateLoad
}

func (l *guiPublicTraceLoader) Abs(base, name string) string {
	return l.delegate.Abs(base, name)
}

func (l *guiPublicTraceLoader) Get(name string) (io.Reader, error) {
	reader, err := l.delegate.Get(name)
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	l.loads = append(l.loads, guiPublicTemplateLoad{name, len(raw), guiPublicHash(raw)})
	return bytes.NewReader(raw), nil
}

func guiPublicExpectedTemplate(fixture guiPublicCase) string {
	switch fixture.Path {
	case "/privacy/":
		return "web/privacy.html"
	case "/terms/":
		return "web/terms.html"
	case "/open-data/":
		return "web/open_data.html"
	case "/":
		return "web/landing.html"
	default:
		return "" // No inferred or unowned route joins the public fixture set.
	}
}

func guiPublicCheckLoads(fixture guiPublicCase, loads []guiPublicTemplateLoad) error {
	page := guiPublicExpectedTemplate(fixture)
	if page == "" || len(loads) != 2 || loads[0].Name != page || loads[1].Name != "base.html" {
		return fmt.Errorf("unexpected original template load closure for %s", fixture.ID)
	}
	for _, load := range loads {
		if load.Bytes == 0 || load.SHA256 == "" {
			return fmt.Errorf("empty original template load for %s", fixture.ID)
		}
	}
	return nil
}

type guiPublicReferenceRecord struct {
	Case     guiPublicCase `json:"case"`
	File     string        `json:"file"`
	Bytes    int           `json:"bytes"`
	SHA256   string        `json:"sha256"`
	FullMode uint32        `json:"full_mode"`
	raw      []byte
}

type guiPublicReferenceCheckpoint struct {
	ManifestSHA256 string                     `json:"capture_manifest_sha256"`
	Cases          []guiPublicReferenceRecord `json:"captured_cases"`
}

// This is custody of the already captured raw originals, not an HTML comparator.
// Nonce-bearing bodies are never normalized or promoted into a parity verdict.
func guiPublicReadReference(directory string, checkpoint []byte) (map[string]guiPublicReferenceRecord, error) {
	var binding guiPublicReferenceCheckpoint
	if err := json.Unmarshal(checkpoint, &binding); err != nil {
		return nil, err
	}
	cases := guiPublicCases()
	if len(binding.Cases) != len(cases) || binding.ManifestSHA256 == "" {
		return nil, fmt.Errorf("original checkpoint has an incomplete case matrix")
	}
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return nil, fmt.Errorf("original corpus path must be canonical and absolute")
	}
	for current := directory; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("original corpus ancestry must be regular directories")
		}
		if current == directory && info.Mode().Perm() != 0o700 {
			return nil, fmt.Errorf("original corpus must remain private mode0700")
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	read := func(name string) ([]byte, error) {
		info, err := root.Lstat(name)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() < 1 || info.Size() > 512<<10 {
			return nil, fmt.Errorf("original corpus member must be private, bounded and regular: %s", name)
		}
		file, err := root.Open(name)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) || opened.Mode() != info.Mode() || opened.Size() != info.Size() {
			return nil, fmt.Errorf("original corpus member changed while opening: %s", name)
		}
		raw, err := io.ReadAll(io.LimitReader(file, (512<<10)+1))
		if err != nil || int64(len(raw)) != info.Size() {
			return nil, fmt.Errorf("original corpus member could not be read exactly: %s", name)
		}
		return raw, nil
	}
	manifest, err := read("manifest.json")
	if err != nil {
		return nil, err
	}
	if guiPublicHash(manifest) != binding.ManifestSHA256 {
		return nil, fmt.Errorf("original capture manifest differs from the committed checkpoint")
	}
	wanted := map[string]bool{"manifest.json": true}
	result := make(map[string]guiPublicReferenceRecord, len(cases))
	for index, fixture := range cases {
		record := binding.Cases[index]
		name := fixture.ID + ".html"
		if record.Case != fixture || record.File != name || record.FullMode != 0o100600 || record.Bytes < 1 {
			return nil, fmt.Errorf("original checkpoint case identity differs: %s", fixture.ID)
		}
		raw, err := read(name)
		if err != nil {
			return nil, err
		}
		if len(raw) != record.Bytes || guiPublicHash(raw) != record.SHA256 {
			return nil, fmt.Errorf("original body differs from the committed checkpoint: %s", fixture.ID)
		}
		wanted[name] = true
		record.raw = raw
		result[fixture.ID] = record
	}
	listing, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer listing.Close()
	entries, err := listing.ReadDir(-1)
	if err != nil || len(entries) != len(wanted) {
		return nil, fmt.Errorf("original corpus member set differs")
	}
	for _, entry := range entries {
		if !wanted[entry.Name()] {
			return nil, fmt.Errorf("unexpected original corpus member: %s", entry.Name())
		}
	}
	return result, nil
}

func TestGUIPublicLoaderTracePreservesNativeStreams(t *testing.T) {
	root, err := filepath.Abs("../../../../")
	if err != nil {
		t.Fatal(err)
	}
	loader := &templateLoader{root: root}
	trace := &guiPublicTraceLoader{delegate: loader}
	for _, name := range []string{"web/privacy.html", "base.html"} {
		original, err := loader.Get(name)
		if err != nil {
			t.Fatal(err)
		}
		expected, err := io.ReadAll(original)
		if err != nil {
			t.Fatal(err)
		}
		observed, err := trace.Get(name)
		if err != nil {
			t.Fatal(err)
		}
		actual, err := io.ReadAll(observed)
		if err != nil || !bytes.Equal(actual, expected) {
			t.Fatal("trace changed the real transformed template stream")
		}
	}
	if err := guiPublicCheckLoads(guiPublicCases()[0], trace.loads); err != nil {
		t.Fatal(err)
	}
	if trace.Abs("web/privacy.html", "../../outside") != loader.Abs("web/privacy.html", "../../outside") {
		t.Fatal("trace changed native path refusal")
	}
	if _, err := trace.Get("web/no-such-public-template.html"); err == nil || len(trace.loads) != 2 {
		t.Fatal("trace hid a native loader error or invented a successful load")
	}
	for _, wrong := range [][]guiPublicTemplateLoad{nil, trace.loads[:1], {trace.loads[1], trace.loads[0]}, {trace.loads[0], trace.loads[1], trace.loads[1]}} {
		if err := guiPublicCheckLoads(guiPublicCases()[0], wrong); err == nil {
			t.Fatal("incomplete, reordered or extra template closure accepted")
		}
	}
}

func TestGUIPublicReferenceRefusesChangedCorpus(t *testing.T) {
	for _, mutation := range []string{"body", "missing", "extra", "alias", "case-identity"} {
		t.Run(mutation, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "original")
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			manifest := []byte("synthetic custody-test manifest, not a renderer capture\n")
			binding := guiPublicReferenceCheckpoint{ManifestSHA256: guiPublicHash(manifest)}
			if err := os.WriteFile(filepath.Join(directory, "manifest.json"), manifest, 0o600); err != nil {
				t.Fatal(err)
			}
			for _, fixture := range guiPublicCases() {
				raw := []byte("synthetic custody-test bytes: " + fixture.ID)
				if mutation == "alias" && len(binding.Cases) == 1 {
					raw = []byte("synthetic custody-test bytes: " + binding.Cases[0].Case.ID)
				}
				name := fixture.ID + ".html"
				if err := os.WriteFile(filepath.Join(directory, name), raw, 0o600); err != nil {
					t.Fatal(err)
				}
				binding.Cases = append(binding.Cases, guiPublicReferenceRecord{fixture, name, len(raw), guiPublicHash(raw), 0o100600, nil})
			}
			checkpoint, err := json.Marshal(binding)
			if err != nil {
				t.Fatal(err)
			}
			if records, err := guiPublicReadReference(directory, checkpoint); err != nil || len(records) != 14 {
				t.Fatalf("unchanged synthetic custody fixture refused: %v", err)
			}
			first := filepath.Join(directory, binding.Cases[0].File)
			switch mutation {
			case "body":
				err = os.WriteFile(first, []byte("changed"), 0o600)
			case "missing":
				err = os.Rename(first, filepath.Join(directory, "wrong.html"))
			case "extra":
				err = os.WriteFile(filepath.Join(directory, "extra.html"), []byte("extra"), 0o600)
			case "alias":
				if err = os.Remove(first); err == nil {
					err = os.Symlink(binding.Cases[1].File, first)
				}
			case "case-identity":
				binding.Cases[0].Case.Language = "ro"
				checkpoint, err = json.Marshal(binding)
			}
			if err != nil {
				t.Fatal(err)
			}
			if mutation == "alias" {
				entries, err := os.ReadDir(directory)
				if err != nil || len(entries) != 15 {
					t.Fatal("alias control changed the exact original member set")
				}
				raw, err := os.ReadFile(first)
				if err != nil || len(raw) != binding.Cases[0].Bytes || guiPublicHash(raw) != binding.Cases[0].SHA256 {
					t.Fatal("alias control changed the expected body bytes")
				}
			}
			if _, err := guiPublicReadReference(directory, checkpoint); err == nil {
				t.Fatal("changed synthetic corpus accepted")
			}
		})
	}
}
