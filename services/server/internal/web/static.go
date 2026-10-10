package web

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
)

// StaticHandler serves only regular release assets. It never lists directories
// or resolves outside the release root, and it bypasses account/database work.
func StaticHandler(directory string) (http.Handler, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	return newStaticHandler(func(name string) (staticAssetFile, error) {
		return root.Open(name)
	}), nil
}

type staticAssetFile interface {
	fs.File
	io.Seeker
}

func newStaticHandler(open func(string) (staticAssetFile, error)) http.Handler {
	var hashes sync.Map
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", 405)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/static/")
		if name == r.URL.Path || name == "" || path.Clean(name) != name || strings.ContainsAny(name, "\\\x00") {
			http.NotFound(w, r)
			return
		}
		for _, part := range strings.Split(name, "/") {
			if strings.HasPrefix(part, ".") {
				http.NotFound(w, r)
				return
			}
		}
		file, err := open(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<20 {
			http.NotFound(w, r)
			return
		}
		contentType := mime.TypeByExtension(path.Ext(name))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		if strings.HasPrefix(name, "frontend/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		cacheKey := fmt.Sprintf("%s:%d:%d", name, info.Size(), info.ModTime().UnixNano())
		tag, found := hashes.Load(cacheKey)
		if !found {
			hash := sha256.New()
			if _, err = io.Copy(hash, file); err != nil {
				http.Error(w, "Asset unavailable", 500)
				return
			}
			tag = `"` + hex.EncodeToString(hash.Sum(nil)) + `"`
			hashes.Store(cacheKey, tag)
		}
		w.Header().Set("ETag", tag.(string))
		_, _ = file.Seek(0, io.SeekStart)
		http.ServeContent(w, r, path.Base(name), info.ModTime(), file)
	})
}
