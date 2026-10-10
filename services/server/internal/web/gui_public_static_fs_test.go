package web

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func guiPublicStaticSnapshots(t *testing.T) (string, fstest.MapFS, map[string][]byte) {
	t.Helper()
	root, err := filepath.Abs("../../../../static")
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{}
	originals := map[string][]byte{}
	modes := map[string]fs.FileMode{}
	for _, name := range []string{"css/base.css", "js/site.js", "js/hovercard.js"} {
		file := filepath.Join(root, filepath.FromSlash(name))
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(file)
		if err != nil || !info.Mode().IsRegular() || int64(len(raw)) != info.Size() {
			t.Fatal("actual public release asset is not regular and exact")
		}
		originals[name] = raw
		modes[name] = info.Mode()
		files[name] = &fstest.MapFile{Data: append([]byte(nil), raw...), Mode: info.Mode().Perm(), ModTime: info.ModTime()}
	}
	t.Cleanup(func() {
		for name, before := range originals {
			file := filepath.Join(root, filepath.FromSlash(name))
			after, err := os.ReadFile(file)
			info, statErr := os.Stat(file)
			if err != nil || statErr != nil || !bytes.Equal(before, after) || info.Mode() != modes[name] {
				t.Error("actual public source asset changed during filesystem tests")
			}
		}
	})
	return root, files, originals
}

func guiPublicStaticRequest(handler http.Handler, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "https://gui-fixture.invalid/", nil)
	request.URL.Path = path // Literal handler-path controls, including malformed inputs.
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func guiPublicStaticEqual(t *testing.T, disk, files http.Handler, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	want := guiPublicStaticRequest(disk, method, path, headers)
	got := guiPublicStaticRequest(files, method, path, headers)
	if want.Code != got.Code || !bytes.Equal(want.Body.Bytes(), got.Body.Bytes()) || !reflect.DeepEqual(want.Header(), got.Header()) {
		t.Fatalf("native OS/FS static HTTP behavior differs: method=%s path=%q status=%d/%d", method, path, want.Code, got.Code)
	}
	return got
}

func TestGUIPublicStaticFilesystemActualAssets(t *testing.T) {
	root, snapshot, originals := guiPublicStaticSnapshots(t)
	disk, err := StaticHandler(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := StaticHandlerFS(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"css/base.css", "js/site.js", "js/hovercard.js"} {
		t.Run(name, func(t *testing.T) {
			path := "/static/" + name
			raw := originals[name]
			base := guiPublicStaticEqual(t, disk, files, "GET", path, nil)
			etag := fmt.Sprintf("\"%x\"", sha256.Sum256(raw))
			modified := base.Header().Get("Last-Modified")
			if base.Code != 200 || !bytes.Equal(base.Body.Bytes(), raw) || base.Header().Get("ETag") != etag || modified == "" || base.Header().Get("Cache-Control") != "public, max-age=3600" || base.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("actual public asset body/header contract changed")
			}
			for _, check := range []struct {
				name, method string
				headers      map[string]string
				status       int
				body         []byte
			}{
				{"get", "GET", nil, 200, raw},
				{"head", "HEAD", nil, 200, nil},
				{"range", "GET", map[string]string{"Range": "bytes=1-7"}, 206, raw[1:8]},
				{"etag-not-modified", "GET", map[string]string{"If-None-Match": etag}, 304, nil},
				{"time-not-modified", "GET", map[string]string{"If-Modified-Since": modified}, 304, nil},
				{"if-range", "GET", map[string]string{"Range": "bytes=1-7", "If-Range": etag}, 206, raw[1:8]},
			} {
				t.Run(check.name, func(t *testing.T) {
					response := guiPublicStaticEqual(t, disk, files, check.method, path, check.headers)
					if response.Code != check.status || !bytes.Equal(response.Body.Bytes(), check.body) {
						t.Fatal("actual asset method/range/conditional outcome changed")
					}
				})
			}
		})
	}
}

func TestGUIPublicStaticFilesystemIsolationAndMethods(t *testing.T) {
	root, snapshot, originals := guiPublicStaticSnapshots(t)
	disk, err := StaticHandler(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("supplied-sentinel", func(t *testing.T) {
		// A fictional transport control at an existing public asset name.
		files := fstest.MapFS{"css/base.css": &fstest.MapFile{Data: []byte("FS_ONLY_SENTINEL"), Mode: 0o444}}
		handler, err := StaticHandlerFS(files)
		if err != nil {
			t.Fatal(err)
		}
		response := guiPublicStaticRequest(handler, "GET", "/static/css/base.css", nil)
		if response.Code != 200 || response.Body.String() != "FS_ONLY_SENTINEL" || bytes.Equal(response.Body.Bytes(), originals["css/base.css"]) {
			t.Fatal("supplied filesystem did not own asset bytes")
		}
	})
	t.Run("missing-never-falls-back", func(t *testing.T) {
		handler, err := StaticHandlerFS(fstest.MapFS{})
		if err != nil {
			t.Fatal(err)
		}
		control := guiPublicStaticRequest(disk, "GET", "/static/css/base.css", nil)
		response := guiPublicStaticRequest(handler, "GET", "/static/css/base.css", nil)
		if control.Code != 200 || response.Code != 404 || bytes.Equal(response.Body.Bytes(), originals["css/base.css"]) {
			t.Fatal("missing FS asset fell back to existing OS source")
		}
	})
	t.Run("nil-filesystem", func(t *testing.T) {
		if handler, err := StaticHandlerFS(nil); err == nil || handler != nil {
			t.Fatal("nil filesystem admitted")
		}
	})
	for _, method := range []string{"POST", "PUT", "OPTIONS"} {
		t.Run(method, func(t *testing.T) {
			handler, err := StaticHandlerFS(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			response := guiPublicStaticEqual(t, disk, handler, method, "/static/js/site.js", nil)
			if response.Code != 405 || response.Header().Get("Allow") != "GET, HEAD" {
				t.Fatal("existing static method refusal changed")
			}
		})
	}
	t.Run("existing-immutable-prefix-and-empty-file", func(t *testing.T) {
		// Synthetic release layout; it proves the existing prefix policy, not a build.
		directory := t.TempDir()
		name := "frontend/assets/site-fixture.js"
		if err := os.MkdirAll(filepath.Join(directory, "frontend/assets"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, filepath.FromSlash(name)), originals["js/site.js"], 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "empty.bin"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		files := fstest.MapFS{}
		for _, file := range []string{name, "empty.bin"} {
			info, err := os.Stat(filepath.Join(directory, filepath.FromSlash(file)))
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(file)))
			if err != nil {
				t.Fatal(err)
			}
			files[file] = &fstest.MapFile{Data: data, Mode: 0o600, ModTime: info.ModTime()}
		}
		osHandler, err := StaticHandler(directory)
		if err != nil {
			t.Fatal(err)
		}
		fsHandler, err := StaticHandlerFS(files)
		if err != nil {
			t.Fatal(err)
		}
		response := guiPublicStaticEqual(t, osHandler, fsHandler, "GET", "/static/"+name, nil)
		if response.Code != 200 || response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
			t.Fatal("existing immutable frontend asset policy changed")
		}
		empty := guiPublicStaticEqual(t, osHandler, fsHandler, "GET", "/static/empty.bin", nil)
		if empty.Code != 200 || empty.Body.Len() != 0 {
			t.Fatal("empty regular asset behavior changed")
		}
	})
}

type guiPublicStaticFSFunc func(string) (fs.File, error)

func (f guiPublicStaticFSFunc) Open(name string) (fs.File, error) { return f(name) }

func TestGUIPublicStaticFilesystemRequestRefusals(t *testing.T) {
	root, snapshot, _ := guiPublicStaticSnapshots(t)
	snapshot[".hidden"] = &fstest.MapFile{Data: []byte("HIDDEN_SENTINEL"), Mode: 0o444}
	snapshot["css/.private.css"] = &fstest.MapFile{Data: []byte("HIDDEN_SENTINEL"), Mode: 0o444}
	disk, err := StaticHandler(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		path  string
		opens int
	}{
		{"/other/css/base.css", 0},
		{"/static/", 0},
		{"/static//css/base.css", 0},
		{"/static/css//base.css", 0},
		{"/static/css/../js/site.js", 0},
		{"/static/../site.js", 0},
		{"/static/css\\base.css", 0},
		{"/static/css/base.css\x00", 0},
		{"/static/.hidden", 0},
		{"/static/css/.private.css", 0},
		{"/static/missing.css", 1},
		{"/static/css", 1},
	} {
		t.Run(fmt.Sprintf("%q", check.path), func(t *testing.T) {
			opens := 0
			handler, err := StaticHandlerFS(guiPublicStaticFSFunc(func(name string) (fs.File, error) {
				opens++
				return snapshot.Open(name)
			}))
			if err != nil {
				t.Fatal(err)
			}
			response := guiPublicStaticEqual(t, disk, handler, "GET", check.path, nil)
			if response.Code != 404 || opens != check.opens || strings.Contains(response.Body.String(), "HIDDEN_SENTINEL") {
				t.Fatal("path/regular-file refusal reached or exposed an asset")
			}
		})
	}
}

type guiPublicStaticInfo struct {
	size int64
	mode fs.FileMode
}

func (i guiPublicStaticInfo) Name() string       { return "asset.css" }
func (i guiPublicStaticInfo) Size() int64        { return i.size }
func (i guiPublicStaticInfo) Mode() fs.FileMode  { return i.mode }
func (i guiPublicStaticInfo) ModTime() time.Time { return time.Unix(1700000000, 0) }
func (i guiPublicStaticInfo) IsDir() bool        { return i.mode.IsDir() }
func (i guiPublicStaticInfo) Sys() any           { return nil }

type guiPublicStaticProbe struct {
	reader                      *bytes.Reader
	info                        fs.FileInfo
	statErr, readErr, closeErr  error
	seekErrors                  map[int]error
	seekPositions               map[int]int64
	stats, reads, bytes, closes int
	seeks                       int
}

func (f *guiPublicStaticProbe) Stat() (fs.FileInfo, error) {
	f.stats++
	return f.info, f.statErr
}
func (f *guiPublicStaticProbe) Read(p []byte) (int, error) {
	f.reads++
	n, err := f.reader.Read(p)
	f.bytes += n
	if f.readErr != nil {
		return n, f.readErr
	}
	return n, err
}
func (f *guiPublicStaticProbe) Seek(offset int64, whence int) (int64, error) {
	f.seeks++
	position, err := f.reader.Seek(offset, whence)
	if override, exists := f.seekPositions[whence]; exists {
		position = override
	}
	if forced := f.seekErrors[whence]; forced != nil {
		return position, forced // A correct position must not hide a real seek error.
	}
	return position, err
}
func (f *guiPublicStaticProbe) Close() error {
	f.closes++
	return f.closeErr
}

// Deliberately does not implement Seek; embedding the probe would mask this arm.
type guiPublicStaticNonSeeker struct{ probe *guiPublicStaticProbe }

func (f guiPublicStaticNonSeeker) Read(p []byte) (int, error) { return f.probe.Read(p) }
func (f guiPublicStaticNonSeeker) Stat() (fs.FileInfo, error) { return f.probe.Stat() }
func (f guiPublicStaticNonSeeker) Close() error             { return f.probe.Close() }

func TestGUIPublicStaticFilesystemIORefusalsAndOneClose(t *testing.T) {
	raw := []byte("SYNTHETIC_ASSET_BYTES_MUST_NOT_LEAK")
	problem := errors.New("synthetic filesystem failure")
	for _, check := range []struct {
		name                   string
		edit                   func(*guiPublicStaticProbe)
		openError, noFile      bool
		nonSeeker, pass, reads bool
		seeks                  bool
		closes                 int
	}{
		{name: "regular", pass: true, reads: true, seeks: true, closes: 1},
		{name: "nil-file", noFile: true},
		{name: "open-error", openError: true, noFile: true},
		{name: "open-error-with-file", openError: true, closes: 1},
		{name: "stat-error", edit: func(f *guiPublicStaticProbe) { f.statErr = problem }, closes: 1},
		{name: "nil-stat", edit: func(f *guiPublicStaticProbe) { f.info = nil }, closes: 1},
		{name: "directory", edit: func(f *guiPublicStaticProbe) { f.info = guiPublicStaticInfo{mode: fs.ModeDir} }, closes: 1},
		{name: "negative-size", edit: func(f *guiPublicStaticProbe) { f.info = guiPublicStaticInfo{size: -1} }, closes: 1},
		{name: "oversize", edit: func(f *guiPublicStaticProbe) { f.info = guiPublicStaticInfo{size: 64<<20 + 1} }, closes: 1},
		{name: "nonseekable", nonSeeker: true, closes: 1},
		{name: "seek-end-error", edit: func(f *guiPublicStaticProbe) { f.seekErrors[io.SeekEnd] = problem }, seeks: true, closes: 1},
		{name: "wrong-seek-end", edit: func(f *guiPublicStaticProbe) { f.seekPositions[io.SeekEnd] = 1 }, seeks: true, closes: 1},
		{name: "seek-start-error", edit: func(f *guiPublicStaticProbe) { f.seekErrors[io.SeekStart] = problem }, seeks: true, closes: 1},
		{name: "wrong-seek-start", edit: func(f *guiPublicStaticProbe) { f.seekPositions[io.SeekStart] = 1 }, seeks: true, closes: 1},
		{name: "partial-read-error", edit: func(f *guiPublicStaticProbe) { f.readErr = problem }, reads: true, seeks: true, closes: 1},
		{name: "short-stream", edit: func(f *guiPublicStaticProbe) {
			f.info = guiPublicStaticInfo{size: int64(len(raw) + 1)}
			f.seekPositions[io.SeekEnd] = int64(len(raw) + 1)
		}, reads: true, seeks: true, closes: 1},
		{name: "overrun-stream", edit: func(f *guiPublicStaticProbe) {
			f.info = guiPublicStaticInfo{size: int64(len(raw) - 1)}
			f.seekPositions[io.SeekEnd] = int64(len(raw) - 1)
		}, reads: true, seeks: true, closes: 1},
		{name: "close-error", edit: func(f *guiPublicStaticProbe) { f.closeErr = problem }, reads: true, seeks: true, closes: 1},
	} {
		t.Run(check.name, func(t *testing.T) {
			probe := &guiPublicStaticProbe{reader: bytes.NewReader(raw), info: guiPublicStaticInfo{size: int64(len(raw))}, seekErrors: map[int]error{}, seekPositions: map[int]int64{}}
			if check.edit != nil {
				check.edit(probe)
			}
			handler, err := StaticHandlerFS(guiPublicStaticFSFunc(func(string) (fs.File, error) {
				var file fs.File = probe
				if check.nonSeeker {
					file = guiPublicStaticNonSeeker{probe: probe}
				}
				if check.noFile {
					file = nil
				}
				if check.openError {
					return file, problem
				}
				return file, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			response := guiPublicStaticRequest(handler, "GET", "/static/asset.css", nil)
			if check.pass {
				if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), raw) {
					t.Fatal("valid regular FS asset refused or changed")
				}
			} else if response.Code != 404 || strings.Contains(response.Body.String(), string(raw)) || response.Header().Get("ETag") != "" {
				t.Fatal("unsupported FS input published asset bytes or metadata")
			}
			if probe.closes != check.closes || (probe.reads > 0) != check.reads || (probe.seeks > 0) != check.seeks {
				t.Fatal("underlying input close/read/seek ownership differs")
			}
			if check.name == "overrun-stream" && probe.bytes != len(raw) {
				t.Fatal("overflow control did not consume the extra byte")
			}
		})
	}
}
