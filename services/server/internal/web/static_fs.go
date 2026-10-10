package web

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"net/http"
)

// StaticHandlerFS adds an opt-in release-asset transport rooted at files.
// Names are relative to the static directory (css/base.css, not static/css/base.css).
// The caller owns confinement, stability and lifetime of an arbitrary fs.FS;
// this constructor does not seal it or activate it in the application's assembly.
// Inputs must be regular, bounded and seekable. They are validated and closed
// before the common handler receives a request-local memory snapshot, so read,
// seek, size or close errors cannot publish partial asset bytes. This buffers
// at most the existing64MiB asset limit per request, with one overflow byte.
// The default OS constructor, headers/cache policy and ServeContent path remain.
func StaticHandlerFS(files fs.FS) (http.Handler, error) {
	if files == nil {
		return nil, fmt.Errorf("static filesystem required")
	}
	return newStaticHandler(func(name string) (staticAssetFile, error) {
		if !fs.ValidPath(name) || name == "." {
			return nil, fs.ErrInvalid
		}
		file, err := files.Open(name)
		if err != nil {
			if file != nil {
				_ = file.Close()
			}
			return nil, err
		}
		return readStaticFSAsset(file)
	}), nil
}

func readStaticFSAsset(file fs.File) (asset staticAssetFile, err error) {
	if file == nil {
		return nil, fmt.Errorf("static filesystem returned no file")
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			asset = nil
			if err == nil {
				err = closeErr
			}
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info == nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > 64<<20 {
		return nil, fmt.Errorf("static filesystem input must be bounded and regular")
	}
	seeker, ok := file.(io.Seeker)
	if !ok {
		return nil, fmt.Errorf("static filesystem input must support seeking")
	}
	end, err := seeker.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	if end != info.Size() {
		return nil, fmt.Errorf("static filesystem seek length differs")
	}
	start, err := seeker.Seek(0, io.SeekStart)
	if err != nil {
		return nil, err
	}
	if start != 0 {
		return nil, fmt.Errorf("static filesystem seek origin differs")
	}
	raw, err := io.ReadAll(io.LimitReader(file, info.Size()+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) != info.Size() {
		return nil, fmt.Errorf("static filesystem input length differs or exceeds limit")
	}
	return &staticFSMemoryFile{Reader: bytes.NewReader(raw), info: info}, nil
}

// The source file has already been closed. Only this validated request-local
// memory snapshot reaches the unchanged shared ServeContent implementation.
type staticFSMemoryFile struct {
	*bytes.Reader
	info fs.FileInfo
}

func (f *staticFSMemoryFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *staticFSMemoryFile) Close() error               { return nil }
