package web

import (
	"bytes"
	"fmt"
	"errors"
	"io"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/flosch/pongo2/v6"
)

const rendererFSTestPO = "msgid \"Activities\"\nmsgstr \"Activități\"\n"

type guiPublicFSSnapshot struct {
	files fstest.MapFS
	hashes map[string]string
}

func guiPublicFilesystemSnapshot(root string) (*guiPublicFSSnapshot, error) {
	input, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	snapshot := &guiPublicFSSnapshot{files: fstest.MapFS{}, hashes: map[string]string{}}
	for _, name := range []string{"templates/base.html", "apps/web/templates/web/privacy.html", "apps/web/templates/web/terms.html", "apps/web/templates/web/open_data.html", "apps/web/templates/web/landing.html", "locale/ro/LC_MESSAGES/django.po"} {
		file, err := input.Open(name)
		if err != nil {
			return nil, err
		}
		limit := int64(512 << 10)
		if strings.HasPrefix(name, "locale/") {
			limit = 2 << 20
		}
		raw, err := readRendererFSFile(file, limit)
		if err != nil {
			return nil, err
		}
		snapshot.files[name] = &fstest.MapFile{Data: raw, Mode: 0o444}
		snapshot.hashes[name] = guiPublicHash(raw)
	}
	return snapshot, nil
}

func (s *guiPublicFSSnapshot) check() error {
	if len(s.files) != len(s.hashes) || len(s.files) != 6 {
		return fmt.Errorf("public FS snapshot member set changed")
	}
	for name, digest := range s.hashes {
		file := s.files[name]
		if file == nil || file.Mode != 0o444 || guiPublicHash(file.Data) != digest {
			return fmt.Errorf("public FS snapshot bytes/mode changed")
		}
	}
	return nil
}

func TestGUIPublicFilesystemRootRelativeIncludes(t *testing.T) {
	files := fstest.MapFS{
		"locale/ro/LC_MESSAGES/django.po": {Data: []byte(rendererFSTestPO)},
		"templates/web/base.html": {Data: []byte(`<main>{% block content %}base{% endblock %}</main>`)},
		"templates/web/web/base.html": {Data: []byte(`<main>WRONG base-relative template</main>`)},
		"apps/web/templates/web/page.html": {Data: []byte(`{% extends "web/base.html" %}{% block content %}{% include "web/partial.html" %}{% endblock %}`)},
		"apps/web/templates/web/partial.html": {Data: []byte(`<p>{% trans "Activities" %}</p>`)},
		"apps/web/templates/web/web/partial.html": {Data: []byte(`<p>WRONG base-relative partial</p>`)},
	}
	renderer, err := NewRendererFS("unused-host-asset-root", files)
	if err != nil {
		t.Fatal(err)
	}
	if renderer.loader.Abs("web/page.html", "web/base.html") != "web/base.html" {
		t.Fatal("native root-relative extends semantics changed")
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/", nil)
	request.Header.Set("Accept-Language", "ro")
	if err := renderer.Render(response, request, "web/page.html", nil); err != nil || response.Body.String() != "<main><p>Activități</p></main>" {
		t.Fatalf("root-relative transform/include/catalogue behavior changed: %v", err)
	}
}

func TestGUIPublicFilesystemNeverFallsBackToHost(t *testing.T) {
	root := t.TempDir()
	for name, raw := range map[string]string{
		"locale/ro/LC_MESSAGES/django.po": "msgid \"Activities\"\nmsgstr \"HOST catalogue\"\n",
		"templates/web/page.html": "HOST template must never render",
	} {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := NewRendererFS(root, fstest.MapFS{}); err == nil {
		t.Fatal("missing FS catalogue fell back to the genuine host catalogue")
	}
	renderer, err := NewRendererFS(root, fstest.MapFS{"locale/ro/LC_MESSAGES/django.po": {Data: []byte(rendererFSTestPO)}})
	if err != nil || renderer.catalog.translate("ro", "Activities", 1) != "Activități" {
		t.Fatal("provided FS catalogue was not authoritative")
	}
	response := httptest.NewRecorder()
	if err := renderer.Render(response, httptest.NewRequest("GET", "/", nil), "web/page.html", nil); err == nil || response.Body.Len() != 0 || response.Header().Get("Content-Type") != "" {
		t.Fatal("missing FS template fell back to the genuine host template or wrote partial output")
	}
}

func TestGUIPublicFilesystemRefusesPathAliases(t *testing.T) {
	loader := &rendererFSLoader{delegate: pongo2.NewFSLoader(fstest.MapFS{
		"base.html": {Data: []byte("root fallback exists")},
		"templates/base.html": {Data: []byte("canonical template exists")},
	})}
	for _, name := range []string{"", ".", "/base.html", "../base.html", "./base.html", "web/../base.html", "web\\base.html", "base.html\x00"} {
		t.Run(strings.ReplaceAll(name, "\x00", "NUL"), func(t *testing.T) {
			if loader.Abs("web/page.html", name) != "" {
				t.Fatal("noncanonical FS name resolved")
			}
			if _, err := loader.Get(name); err == nil {
				t.Fatal("noncanonical FS name opened a real available template")
			}
		})
	}
}

type rendererFSInfo struct {
	size int64
	mode fs.FileMode
}

func (i rendererFSInfo) Name() string { return "fixture" }
func (i rendererFSInfo) Size() int64 { return i.size }
func (i rendererFSInfo) Mode() fs.FileMode { return i.mode }
func (i rendererFSInfo) ModTime() time.Time { return time.Time{} }
func (i rendererFSInfo) IsDir() bool { return i.mode.IsDir() }
func (i rendererFSInfo) Sys() any { return nil }

type rendererFSFile struct {
	reader io.Reader
	info fs.FileInfo
	statErr, closeErr error
	closed, readBytes int
}

func (f *rendererFSFile) Read(p []byte) (int, error) {
	n, err := f.reader.Read(p)
	f.readBytes += n
	return n, err
}
func (f *rendererFSFile) Stat() (fs.FileInfo, error) { return f.info, f.statErr }
func (f *rendererFSFile) Close() error { f.closed++; return f.closeErr }

type rendererErrorReader struct{}

func (rendererErrorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type rendererTemplateFS struct{ file fs.File }

func (f rendererTemplateFS) Open(name string) (fs.File, error) {
	if name != "templates/web/page.html" {
		return nil, fs.ErrNotExist
	}
	return f.file, nil
}

func TestGUIPublicFilesystemBoundedReadsRefuseRatherThanTruncate(t *testing.T) {
	limit := int64(512 << 10)
	for _, fixture := range []struct {
		name string
		file *rendererFSFile
	}{
		{"declared-overflow", &rendererFSFile{reader: strings.NewReader("x"), info: rendererFSInfo{limit + 1, 0o444}}},
		{"actual-overflow", &rendererFSFile{reader: bytes.NewReader(bytes.Repeat([]byte("x"), int(limit)+1)), info: rendererFSInfo{limit, 0o444}}},
		{"negative-size", &rendererFSFile{reader: strings.NewReader("x"), info: rendererFSInfo{-1, 0o444}}},
		{"short-read", &rendererFSFile{reader: strings.NewReader("x"), info: rendererFSInfo{2, 0o444}}},
		{"directory", &rendererFSFile{reader: strings.NewReader("x"), info: rendererFSInfo{1, fs.ModeDir}}},
		{"symlink-type", &rendererFSFile{reader: strings.NewReader("x"), info: rendererFSInfo{1, fs.ModeSymlink}}},
		{"stat-error", &rendererFSFile{reader: strings.NewReader("x"), statErr: errors.New("synthetic stat refusal")}},
		{"nil-stat", &rendererFSFile{reader: strings.NewReader("x")}},
		{"read-error", &rendererFSFile{reader: rendererErrorReader{}, info: rendererFSInfo{1, 0o444}}},
		{"close-error", &rendererFSFile{reader: strings.NewReader("x"), info: rendererFSInfo{1, 0o444}, closeErr: errors.New("synthetic close refusal")}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			loader := &rendererFSLoader{delegate: pongo2.NewFSLoader(rendererTemplateFS{fixture.file})}
			reader, err := loader.Get("web/page.html")
			if err == nil || reader != nil || fixture.file.closed != 1 {
				t.Fatal("bad FS input was accepted, truncated or not closed exactly once")
			}
			if fixture.name == "actual-overflow" && fixture.file.readBytes != int(limit)+1 {
				t.Fatal("overflow control did not inspect the extra byte beyond the exact declared limit")
			}
		})
	}
	if _, err := readRendererFSFile(nil, limit); err == nil {
		t.Fatal("nil FS file accepted")
	}
	file := &rendererFSFile{reader: strings.NewReader("exact"), info: rendererFSInfo{5, 0o444}}
	loader := &rendererFSLoader{delegate: pongo2.NewFSLoader(rendererTemplateFS{file})}
	reader, err := loader.Get("web/page.html")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(reader)
	if err != nil || string(raw) != "exact" || file.closed != 1 {
		t.Fatal("exact regular FS input changed")
	}
}

type rendererOpenBarrier struct {
	fallback fs.FS
	opened []string
}

func (f *rendererOpenBarrier) Open(name string) (fs.File, error) {
	f.opened = append(f.opened, name)
	if name == "templates/web/page.html" {
		return nil, fs.ErrPermission
	}
	return f.fallback.Open(name)
}

func TestGUIPublicFilesystemDoesNotBypassOpenRefusal(t *testing.T) {
	files := &rendererOpenBarrier{fallback: fstest.MapFS{"apps/web/templates/web/page.html": {Data: []byte("fallback must not bypass refusal")}}}
	loader := &rendererFSLoader{delegate: pongo2.NewFSLoader(files)}
	if _, err := loader.Get("web/page.html"); !errors.Is(err, fs.ErrPermission) || len(files.opened) != 1 {
		t.Fatal("FS permission refusal was bypassed through another prefix")
	}
}

func TestGUIPublicFilesystemCatalogueErrorKeepsDefaultDiskBehavior(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "locale/ro/LC_MESSAGES/django.po")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	raw := []byte(rendererFSTestPO + "\n" + strings.Repeat("x", (256<<10)+1))
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadCatalog(root).translate("ro", "Activities", 1); got != "Activități" {
		t.Fatal("legacy disk parser's existing partial-catalogue behavior changed")
	}
	if _, err := NewRendererFS(root, fstest.MapFS{"locale/ro/LC_MESSAGES/django.po": {Data: raw}}); err == nil {
		t.Fatal("new FS constructor accepted a scanner error or fell back to host")
	}
	if _, err := NewRendererFS(root, nil); err == nil {
		t.Fatal("nil FS accepted")
	}
}

type rendererCatalogueFS struct{ file fs.File }

func (f rendererCatalogueFS) Open(name string) (fs.File, error) {
	if name != "locale/ro/LC_MESSAGES/django.po" {
		return nil, fs.ErrNotExist
	}
	return f.file, nil
}

func TestGUIPublicFilesystemCatalogueUsesItsRealSizeBound(t *testing.T) {
	valid := []byte(rendererFSTestPO + "\n" + strings.Repeat("#\n", (600<<10)/2))
	renderer, err := NewRendererFS("unused-host-root", fstest.MapFS{"locale/ro/LC_MESSAGES/django.po": {Data: valid}})
	if err != nil || renderer.catalog.translate("ro", "Activities", 1) != "Activități" {
		t.Fatal("valid catalogue larger than the template bound was rejected or changed")
	}
	oversized := bytes.Repeat([]byte("x"), (2<<20)+1)
	if _, err := NewRendererFS("unused-host-root", fstest.MapFS{"locale/ro/LC_MESSAGES/django.po": {Data: oversized}}); err == nil {
		t.Fatal("oversized declared catalogue accepted")
	}
	file := &rendererFSFile{reader: bytes.NewReader(oversized), info: rendererFSInfo{2 << 20, 0o444}}
	if _, err := NewRendererFS("unused-host-root", rendererCatalogueFS{file}); err == nil || file.closed != 1 || file.readBytes != (2<<20)+1 {
		t.Fatal("actual catalogue overflow was truncated or its file was not closed")
	}
}
