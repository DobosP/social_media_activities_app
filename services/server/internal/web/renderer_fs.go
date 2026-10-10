package web

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"

	"github.com/flosch/pongo2/v6"
)

// NewRendererFS is an additive original-Pongo template/catalog delivery boundary.
// files uses repository-style paths (templates/, apps/web/templates/, locale/).
// Callers own its immutability and lifetime; an arbitrary fs.FS is not sealed by
// this constructor. assetRoot remains only for the existing assets method,
// which is outside this partial boundary. Templates/catalog never use that root.
// The default NewRenderer disk path and production assembly are unchanged.
func NewRendererFS(assetRoot string, files fs.FS) (*Renderer, error) {
	if files == nil {
		return nil, fmt.Errorf("renderer filesystem required")
	}
	file, err := files.Open("locale/ro/LC_MESSAGES/django.po")
	if err != nil {
		return nil, fmt.Errorf("renderer filesystem catalogue: %w", err)
	}
	raw, err := readRendererFSFile(file, 2<<20)
	if err != nil {
		return nil, fmt.Errorf("renderer filesystem catalogue: %w", err)
	}
	catalog, err := parseCatalog(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("renderer filesystem catalogue: %w", err)
	}
	registerFilters()
	loader := &rendererFSLoader{delegate: pongo2.NewFSLoader(files)}
	return &Renderer{Root: assetRoot, loader: loader, set: pongo2.NewSet("native-social", loader), catalog: catalog}, nil
}

type rendererFSLoader struct{ delegate *pongo2.FSLoader }

func rendererFSPath(name string) bool {
	return name != "." && fs.ValidPath(name) && !strings.ContainsAny(name, "\\\x00")
}

// Native templates resolve names from the root, including extends/includes.
// The public Pongo FSLoader's base-relative Abs would change that contract.
func (l *rendererFSLoader) Abs(_ string, name string) string {
	if !rendererFSPath(name) {
		return ""
	}
	return name
}

func (l *rendererFSLoader) Get(name string) (io.Reader, error) {
	if !rendererFSPath(name) {
		return nil, fmt.Errorf("renderer filesystem template path refused")
	}
	for _, prefix := range []string{"templates", "apps/web/templates", "."} {
		reader, err := l.delegate.Get(path.Join(prefix, name))
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return nil, err
			}
			continue
		}
		file, ok := reader.(fs.File)
		if !ok {
			if closer, ok := reader.(io.Closer); ok {
				_ = closer.Close()
			}
			return nil, fmt.Errorf("renderer filesystem template is not a file")
		}
		raw, err := readRendererFSFile(file, 512<<10)
		if err != nil {
			return nil, err
		}
		return strings.NewReader(transformTemplate(string(raw))), nil
	}
	return nil, fmt.Errorf("renderer filesystem template unavailable")
}

func readRendererFSFile(file fs.File, limit int64) ([]byte, error) {
	if file == nil {
		return nil, fmt.Errorf("renderer filesystem file required")
	}
	info, err := file.Stat()
	if err != nil || info == nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > limit {
		_ = file.Close()
		return nil, fmt.Errorf("renderer filesystem input must be bounded and regular")
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if int64(len(raw)) > limit || int64(len(raw)) != info.Size() {
		return nil, fmt.Errorf("renderer filesystem input length differs or exceeds limit")
	}
	return raw, nil
}
