package web

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"strings"
	"sync"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

// Renderer uses a native Go template engine; existing HTML layouts remain data
// and never execute Python. Runtime templates are immutable release inputs.
type Renderer struct {
	Root       string
	CSPEnforce bool
	loader     *templateLoader
	set        *pongo2.TemplateSet
	once       sync.Once
	catalog    translationCatalog
}
type templateLoader struct{ root string }

func (l *templateLoader) Abs(base, name string) string {
	if strings.HasPrefix(name, "/") {
		return ""
	}
	clean := filepath.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return ""
	}
	return clean
}

var loadTag = regexp.MustCompile(`\{%\s*load\s+[^%]+%\}`)
var transTag = regexp.MustCompile(`\{%\s*(?:trans|translate)\s+((?:"[^"]*"|'[^']*'))\s*%\}`)
var langTag = regexp.MustCompile(`\{%\s*get_current_language\s+as\s+(\w+)\s*%\}`)
var staticTag = regexp.MustCompile(`\{%\s*static\s+((?:"[^"]*"|'[^']*'))\s*%\}`)
var urlTag = regexp.MustCompile(`\{%\s*url\s+((?:"[^"]*"|'[^']*'|[^\s%]+))\s*([^%]*)%\}`)

func (l *templateLoader) Get(name string) (io.Reader, error) {
	var file *os.File
	var err error
	for _, directory := range []string{filepath.Join(l.root, "templates"), filepath.Join(l.root, "apps/web/templates"), l.root} {
		root, openErr := os.OpenRoot(directory)
		if openErr != nil {
			continue
		}
		file, err = root.Open(name)
		root.Close()
		if err == nil {
			break
		}
	}
	if file == nil {
		return nil, errors.New("template unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 512<<10 {
		return nil, errors.New("template unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 512<<10))
	if err != nil {
		return nil, err
	}
	s := transformTemplate(string(raw))
	return strings.NewReader(s), nil
}
func NewRenderer(root string) *Renderer {
	registerFilters()
	loader := &templateLoader{root}
	return &Renderer{Root: root, loader: loader, set: pongo2.NewSet("native-social", loader), catalog: loadCatalog(root)}
}
func (r *Renderer) Render(w http.ResponseWriter, request *http.Request, name string, data pongo2.Context) error {
	nonce := rand.Text()
	lang := language(request)
	actor, authenticated := platform.ActorFrom(request)
	defaults := pongo2.Context{"language": lang, "LANGUAGE_CODE": lang, "display_theme": display(request, "display_theme", "auto", "auto", "light", "dark", "contrast"), "display_text": display(request, "display_text", "normal", "normal", "large", "larger"), "display_motion": display(request, "display_motion", "auto", "auto", "reduce", "full"), "site_name": "Social Activities", "canonical_url": request.URL.Path, "og_locale": "en_GB", "request": map[string]any{"csp_nonce": nonce, "path": request.URL.Path, "GET": request.URL.Query()}, "user": map[string]any{"is_authenticated": authenticated, "is_moderator": actor.Moderator(), "is_admin": actor.Admin(), "username": actor.Username, "display_name": actor.DisplayName, "avatar_uri": data["avatar_uri"]}, "activity_svg": activitySVG, "route": routeURL, "translate": func(message string) string { return r.catalog.translate(lang, message, 1) }, "translate_block": func(encoded string, count any, values ...any) *pongo2.Value {
		return translatedBlock(r.catalog, lang, encoded, spaInt(count), values...)
	}, "json_script": func(value any, id string) *pongo2.Value { return jsonScript(value, id, nonce) }, "spa_assets": func(entry string) *pongo2.Value { return r.assets(entry, nonce) }, "available_langs": []any{[]string{"en", "English"}, []string{"ro", "Română"}}, "static": func(s string) string { return "/static/" + strings.TrimPrefix(s, "/") }}
	for k, v := range data {
		defaults[k] = v
	}
	template, err := r.set.FromCache(name)
	if err != nil {
		return err
	}
	var output bytes.Buffer
	if err = template.ExecuteWriter(defaults, &output); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-cache")
	if !authenticated && data["cache_public"] == true {
		w.Header().Set("Cache-Control", "private, max-age=3600")
	}
	w.Header().Add("Vary", "Cookie")
	cspHeader := "Content-Security-Policy-Report-Only"
	if r.CSPEnforce {
		cspHeader = "Content-Security-Policy"
	}
	w.Header().Set(cspHeader, "default-src 'self'; base-uri 'self'; frame-ancestors 'none'; object-src 'none'; script-src 'self' 'nonce-"+nonce+"'; style-src 'self'; img-src 'self' data: https://*.tile.openstreetmap.org; connect-src 'self' ws: wss: https://tiles.openfreemap.org; font-src 'self'; worker-src 'self'; form-action 'self'; report-uri /api/v1/ops/csp-report/; report-to csp")
	w.Header().Set("Reporting-Endpoints", `csp="/api/v1/ops/csp-report/"`)
	_, err = w.Write(output.Bytes())
	return err
}
func (r *Renderer) Bootstrap(w http.ResponseWriter, request *http.Request, route, title, csrf string, data any) error {
	payload := map[string]any{"route": route, "title": title, "csrf": csrf, "data": data}
	if request.URL.Query().Get("_data") == "1" {
		platform.JSON(w, 200, payload)
		return nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return r.Render(w, request, "web/spa.html", pongo2.Context{"spa_route": route, "spa_title": title, "spa_bootstrap": json.RawMessage(raw)})
}
