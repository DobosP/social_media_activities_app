package web

import (
	"github.com/flosch/pongo2/v6"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseTemplatesCompile(t *testing.T) {
	root, err := filepath.Abs("../../../../")
	if err != nil {
		t.Fatal(err)
	}
	r := NewRenderer(root)
	err = filepath.WalkDir(filepath.Join(root, "apps/web/templates"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".html" {
			return nil
		}
		name, _ := filepath.Rel(filepath.Join(root, "apps/web/templates"), path)
		t.Run(name, func(t *testing.T) {
			if _, err := r.set.FromFile(name); err != nil {
				t.Fatal(err)
			}
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestNativeHTMLAndJSONIslandEscape(t *testing.T) {
	root, _ := filepath.Abs("../../../../")
	r := NewRenderer(root)
	request := httptest.NewRequest("GET", "https://example.org/privacy/", nil)
	response := httptest.NewRecorder()
	if err := r.Render(response, request, "web/privacy.html", pongo2.Context{"csrf": "dummy", "activity_svg": activitySVG}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(response.Body.String(), "<!DOCTYPE html>") || !strings.Contains(response.Body.String(), "/static/css/base.css") {
		t.Fatal("native shell missing")
	}
	island := jsonScript(map[string]any{"body": "</script><script>alert(1)</script>"}, "spa-bootstrap", "dummy").String()
	if strings.Count(island, "<script") != 1 || strings.Contains(island, "</script><script>") {
		t.Fatal("island breakout")
	}
	block := translateBlock(`<strong>%s</strong>`, "<img onerror=x>").String()
	if strings.Contains(block, "<img") {
		t.Fatal("block interpolation must escape")
	}
}
