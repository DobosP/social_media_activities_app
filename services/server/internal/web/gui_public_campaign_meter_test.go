package web

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/flosch/pongo2/v6"
)

const guiCampaignMeterTemplate = "apps/web/templates/web/campaigns.html"
const guiCampaignMeterFixture = "services/server/internal/web/testdata/gui-public-campaign-meter/campaigns-pre-externalization-7930.html"
const guiCampaignMeterBeforeSHA = "40bcb0b71f3e95c56fe238e57244f3f36c937283e1f6961bfa654a390feb3b06"
const guiCampaignMeterBeforeCSSSHA = "db20897d8d822eba19c0f6124a678fb0642e69e0775fcc746913c837ac5e211a"

const guiCampaignMeterBeforeTag = `<div style="width:{{ c.percent }}%"></div>`
const guiCampaignMeterAfterTag = `<svg class="campaign-meter-fill" aria-hidden="true" focusable="false" viewBox="0 0 100 1" preserveAspectRatio="none"><rect width="{{ c.percent }}%" height="1"></rect></svg>`

const guiCampaignMeterBeforeComment = `/* Donation-campaign progress meter (campaigns.html). The bar width stays a
   per-row inline style (dynamic percent) — everything static lives here. */
`
const guiCampaignMeterAfterComment = `/* Donation-campaign progress meter (campaigns.html). The fractional fill uses an
   SVG width attribute; static geometry and colors remain in this stylesheet. */
`
const guiCampaignMeterAnchor = ".meter > div { background: var(--accent); height: 100%; }\n"
const guiCampaignMeterRules = `.meter > svg.campaign-meter-fill { display: block; width: 100%; height: 100%; }
.meter > svg.campaign-meter-fill > rect { fill: var(--accent); }
`

// Only the explicitly approved two-rule/comment delta is inverted, on a copy.
// Actual renderers/static handlers and every source check keep the full new CSS.
// The existing preference suffix and its prior62453B/hash assertion are unchanged.
func guiCampaignMeterCSSBefore(current []byte) ([]byte, error) {
	comment := []byte(guiCampaignMeterAfterComment)
	rules := []byte(guiCampaignMeterRules)
	anchor := []byte(guiCampaignMeterAnchor)
	if bytes.Count(current, comment) != 1 || bytes.Count(current, []byte(guiCampaignMeterBeforeComment)) != 0 ||
		bytes.Count(current, rules) != 1 || bytes.Count(current, anchor) != 1 ||
		bytes.Index(current, rules) != bytes.Index(current, anchor)+len(anchor) {
		return nil, fmt.Errorf("campaign-CSS-delta")
	}
	previous := append([]byte(nil), current...)
	previous = bytes.Replace(previous, comment, []byte(guiCampaignMeterBeforeComment), 1)
	previous = bytes.Replace(previous, rules, nil, 1)
	if len(previous) != 62882 || guiPublicHash(previous) != guiCampaignMeterBeforeCSSSHA {
		return nil, fmt.Errorf("campaign-CSS-whole-inverse")
	}
	return previous, nil
}

var guiCampaignMeterKinds = []string{"empty", "zero", "half", "fractional", "full100", "completed-only"}

var guiCampaignMeterSourcePaths = []string{
	"templates/base.html",
	guiCampaignMeterTemplate,
	guiCampaignMeterFixture,
	"static/css/base.css",
	"apps/web/templates/web/privacy.html",
	"apps/web/templates/web/terms.html",
	"apps/web/templates/web/open_data.html",
	"apps/web/templates/web/landing.html",
	"locale/ro/LC_MESSAGES/django.po",
	"services/server/go.mod",
	"services/server/go.sum",
	"services/server/internal/web/gui_public_campaign_meter_test.go",
	"services/server/internal/web/gui_preferences_layout_test.go",
	"services/server/internal/web/gui_public_golden_test.go",
	"services/server/internal/web/gui_public_normalize_test.go",
	"services/server/internal/web/gui_public_nonce_test.go",
	"services/server/internal/web/gui_public_trace_test.go",
	"services/server/internal/web/renderer_fs_test.go",
	"services/server/internal/web/renderer.go",
	"services/server/internal/web/renderer_fs.go",
	"services/server/internal/web/templates.go",
	"services/server/internal/web/static.go",
	"services/server/internal/web/payments.go",
	"services/server/internal/donations/ledger.go",
	"tools/gui-public-golden/normalize.go",
	"tools/gui-public-golden/release-bindings.json",
	"docs/reviews/gui-public-original/checkpoint.json",
}

func guiCampaignMeterRead(root, name string) ([]byte, string, error) {
	input, err := os.OpenRoot(root)
	if err != nil {
		return nil, "", err
	}
	defer input.Close()
	file, err := input.Open(name)
	if err != nil {
		return nil, "", err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, "", err
	}
	limit := int64(512 << 10)
	if strings.HasPrefix(name, "locale/") {
		limit = 2 << 20
	}
	raw, err := readRendererFSFile(file, limit)
	return raw, info.Mode().String(), err
}

type guiCampaignMeterFixtureData struct {
	root       string
	original   string
	before     []byte
	current    []byte
	css        []byte
	base       *guiPublicFSSnapshot
	files      fstest.MapFS
	normalizer *guiPublicNormalizer
	static     http.Handler
}

func guiNewCampaignMeterFixture(t *testing.T) *guiCampaignMeterFixtureData {
	t.Helper()
	root, err := filepath.Abs("../../../../")
	if err != nil {
		t.Fatal(err)
	}
	read := func(name string) []byte {
		raw, _, err := guiCampaignMeterRead(root, name)
		if err != nil {
			t.Fatal("bounded campaign source read failed")
		}
		return raw
	}
	before := read(guiCampaignMeterFixture)
	current := read(guiCampaignMeterTemplate)
	if len(before) != 3116 || guiPublicHash(before) != guiCampaignMeterBeforeSHA ||
		bytes.Count(before, []byte(guiCampaignMeterBeforeTag)) != 1 || bytes.Count(current, []byte(guiCampaignMeterAfterTag)) != 1 ||
		!bytes.Equal(bytes.Replace(current, []byte(guiCampaignMeterAfterTag), []byte(guiCampaignMeterBeforeTag), 1), before) {
		t.Fatal("whole7930 campaign template inverse required")
	}
	css := read("static/css/base.css")
	previousCSS, err := guiCampaignMeterCSSBefore(css)
	if err != nil || len(previousCSS) != 62882 {
		t.Fatal("exact current campaign CSS inverse required")
	}
	// Inline inverse refusals keep the positive full stylesheet proof above;
	// they do not add named t.Run identities or browser claims.
	for name, changed := range map[string][]byte{
		"missing":   bytes.Replace(css, []byte(guiCampaignMeterRules), nil, 1),
		"duplicate": append(append([]byte(nil), css...), []byte(guiCampaignMeterRules)...),
		"misplaced": append(bytes.Replace(css, []byte(guiCampaignMeterRules), nil, 1), []byte(guiCampaignMeterRules)...),
		"changed":   bytes.Replace(css, []byte(guiCampaignMeterRules), []byte(strings.Replace(guiCampaignMeterRules, "fill: var(--accent);", "fill: transparent;", 1)), 1),
	} {
		if _, err := guiCampaignMeterCSSBefore(changed); err == nil {
			t.Fatalf("exact CSS inverse admitted %s delta", name)
		}
	}
	base, err := guiPublicFilesystemSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if guiSourceOnlyDefault(t, root, "campaign meter") {
		return nil
	}
	normalizer, err := guiPublicNewNormalizer(root)
	if err != nil || normalizer == nil {
		t.Fatal("actual unchanged764 normalizer/archive/original15 required")
	}
	checkpoint := read("docs/reviews/gui-public-original/checkpoint.json")
	if _, err := guiPublicReadReference(*guiPublicGoldenReference, checkpoint); err != nil {
		t.Fatal("original15 custody refused")
	}
	files := fstest.MapFS{}
	for name, file := range base.files {
		files[name] = &fstest.MapFile{Data: append([]byte(nil), file.Data...), Mode: 0o444}
	}
	files[guiCampaignMeterTemplate] = &fstest.MapFile{Data: append([]byte(nil), current...), Mode: 0o444}
	// Seven small literal template/catalog files in fresh owned storage, not a
	// workspace or original-body copy. No source symlink/write-through is used.
	original := t.TempDir()
	oldFiles := map[string][]byte{}
	for name, file := range base.files {
		oldFiles[name] = file.Data
	}
	oldFiles[guiCampaignMeterTemplate] = before
	for name, raw := range oldFiles {
		path := filepath.Join(original, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o444)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := file.Write(raw)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil || os.Chmod(path, 0o444) != nil {
			t.Fatal("owned source fixture write failed")
		}
	}
	static, err := StaticHandler(filepath.Join(root, "static"))
	if err != nil {
		t.Fatal(err)
	}
	source := map[string]string{}
	for _, name := range guiCampaignMeterSourcePaths {
		raw, mode, err := guiCampaignMeterRead(root, name)
		if err != nil || source[name] != "" {
			t.Fatal("closed distinct campaign source readset required")
		}
		source[name] = guiPublicHash(raw) + ":" + mode
	}
	f := &guiCampaignMeterFixtureData{root, original, before, current, css, base, files, normalizer, static}
	t.Cleanup(func() {
		if err := base.check(); err != nil {
			t.Error(err)
		}
		if len(files) != 7 {
			t.Error("current seven-file snapshot changed")
		}
		for name, file := range files {
			want := current
			if name != guiCampaignMeterTemplate {
				original := base.files[name]
				if original == nil {
					t.Error("unexpected current snapshot source")
					continue
				}
				want = original.Data
			}
			if file == nil || file.Mode != 0o444 || !bytes.Equal(file.Data, want) {
				t.Error("current caller-owned snapshot bytes/mode changed")
			}
		}
		for name, want := range oldFiles {
			path := filepath.Join(original, name)
			info, err := os.Lstat(path)
			raw, readErr := os.ReadFile(path)
			if err != nil || readErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o444 || !bytes.Equal(raw, want) {
				t.Error("literal7930 owned source fixture changed")
			}
		}
		for name, want := range source {
			raw, mode, err := guiCampaignMeterRead(root, name)
			if err != nil || guiPublicHash(raw)+":"+mode != want {
				t.Error("campaign source readset bytes/mode changed")
			}
		}
		if err := normalizer.checkUnchanged(); err != nil {
			t.Error("actual normalizer/archive binding changed")
		}
		if _, err := guiPublicReadReference(*guiPublicGoldenReference, checkpoint); err != nil {
			t.Error("original15 custody changed")
		}
	})
	return f
}

func guiCampaignMeterContext(kind string) (pongo2.Context, string, string) {
	campaigns := []map[string]any{}
	completed := []map[string]any{}
	percent, raised, goal, money := any(int64(0)), int64(0), int64(100), "Raised 0.00 of 1.00 EUR goal."
	token := "0"
	switch kind {
	case "empty":
		token = ""
	case "zero":
	case "half":
		percent, raised, token, money = int64(50), 50, "50", "Raised 0.50 of 1.00 EUR goal."
	case "fractional":
		// Ledger.readObject keeps noninteger JSON numbers as exact strings.
		percent, raised, goal, token, money = "33.3333333333333333", 100, 300, "33.3333333333333333", "Raised 1.00 of 3.00 EUR goal."
	case "full100":
		percent, raised, token, money = int64(100), 150, "100", "Raised 1.50 of 1.00 EUR goal."
	case "completed-only":
		token = ""
		completed = append(completed, map[string]any{"title": "Synthetic completed campaign", "outcome": "Synthetic recorded outcome.", "raised_cents": int64(50), "closed_at": "2026-10-04T09:00:00Z", "spend_entries": []map[string]any{{"category": "Synthetic EU hosting", "amount_cents": int64(50), "currency": "EUR", "period": "Synthetic period"}}})
	default:
		return nil, "", ""
	}
	if token != "" {
		row := map[string]any{"title": "Synthetic campaign", "slug": "synthetic-meter", "description": "Synthetic campaign description.", "raised_cents": raised, "goal_cents": goal, "currency": "EUR", "percent": percent}
		if kind != "zero" {
			row["partner_name"], row["partner_website"], row["partner_blurb"] = "Synthetic civic partner", "https://partner.example.invalid/", "Synthetic public credit."
		}
		campaigns = append(campaigns, row)
	}
	return pongo2.Context{"campaigns": campaigns, "completed_campaigns": completed}, token, money
}

// The bounded actual owning progressbar fragment is well-formed XML. This
// parses its HTML/SVG element/attribute tree only; it is not a browser DOM,
// computed-style, geometry, payment or enforced-CSP qualification.
type guiCampaignMeterNode struct {
	name     string
	attrs    map[string]string
	children []*guiCampaignMeterNode
}

func guiCampaignMeterTree(raw []byte) (*guiCampaignMeterNode, int, error) {
	if len(raw) > 8192 {
		raw = raw[:8192]
	}
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	var root *guiCampaignMeterNode
	stack := []*guiCampaignMeterNode{}
	for count := 0; count < 32; count++ {
		token, err := decoder.Token()
		if err != nil {
			return nil, 0, fmt.Errorf("meter-fragment")
		}
		switch token := token.(type) {
		case xml.StartElement:
			if token.Name.Space != "" {
				return nil, 0, fmt.Errorf("meter-namespace")
			}
			node := &guiCampaignMeterNode{name: token.Name.Local, attrs: map[string]string{}}
			for _, attr := range token.Attr {
				if attr.Name.Space != "" {
					return nil, 0, fmt.Errorf("meter-attribute")
				}
				if _, duplicate := node.attrs[attr.Name.Local]; duplicate {
					return nil, 0, fmt.Errorf("meter-attribute")
				}
				node.attrs[attr.Name.Local] = attr.Value
			}
			if len(stack) == 0 {
				root = node
			} else {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, node)
			}
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1].name != token.Name.Local {
				return nil, 0, fmt.Errorf("meter-fragment")
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return root, int(decoder.InputOffset()), nil
			}
		case xml.CharData:
			if strings.TrimSpace(string(token)) != "" {
				return nil, 0, fmt.Errorf("meter-text")
			}
		default:
			return nil, 0, fmt.Errorf("meter-fragment")
		}
	}
	return nil, 0, fmt.Errorf("meter-bound")
}

func guiCampaignMeterAttrs(actual, expected map[string]string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for name, value := range expected {
		if actual[name] != value {
			return false
		}
	}
	return true
}

func guiCampaignMeterDOM(body []byte, kind string, previous bool) (int, int, error) {
	_, percent, money := guiCampaignMeterContext(kind)
	startTag := []byte(`<div role="progressbar"`)
	if percent == "" {
		if bytes.Count(body, startTag) != 0 || bytes.Contains(body, []byte("Synthetic campaign</h2>")) || !bytes.Contains(body, []byte("No active campaigns right now.")) {
			return 0, 0, fmt.Errorf("empty-campaign")
		}
		if bytes.Contains(body, []byte("Synthetic completed campaign")) != (kind == "completed-only") ||
			(kind == "completed-only" && (!bytes.Contains(body, []byte("Synthetic recorded outcome.")) || !bytes.Contains(body, []byte("Synthetic EU hosting")))) {
			return 0, 0, fmt.Errorf("completed-campaign")
		}
		return 0, 0, nil
	}
	if bytes.Count(body, startTag) != 1 {
		return 0, 0, fmt.Errorf("meter-count")
	}
	start := bytes.Index(body, startTag)
	node, length, err := guiCampaignMeterTree(body[start:])
	if err != nil {
		return 0, 0, err
	}
	label := strings.TrimSuffix(strings.Replace(money, "Raised ", "raised ", 1), ".")
	if node.name != "div" || !guiCampaignMeterAttrs(node.attrs, map[string]string{"role": "progressbar", "aria-valuenow": percent, "aria-valuemin": "0", "aria-valuemax": "100", "aria-label": label, "class": "meter"}) {
		return 0, 0, fmt.Errorf("outer-aria")
	}
	if len(node.children) != 1 {
		return 0, 0, fmt.Errorf("fill-count")
	}
	fill := node.children[0]
	if previous {
		if fill.name != "div" || len(fill.children) != 0 || !guiCampaignMeterAttrs(fill.attrs, map[string]string{"style": "width:" + percent + "%"}) {
			return 0, 0, fmt.Errorf("old-fill")
		}
	} else {
		if fill.name != "svg" || !guiCampaignMeterAttrs(fill.attrs, map[string]string{"class": "campaign-meter-fill", "aria-hidden": "true", "focusable": "false", "viewBox": "0 0 100 1", "preserveAspectRatio": "none"}) {
			return 0, 0, fmt.Errorf("decorative-svg")
		}
		if len(fill.children) != 1 || fill.children[0].name != "rect" || len(fill.children[0].children) != 0 {
			return 0, 0, fmt.Errorf("fill-count")
		}
		if !guiCampaignMeterAttrs(fill.children[0].attrs, map[string]string{"width": percent + "%", "height": "1"}) {
			return 0, 0, fmt.Errorf("fill-width")
		}
	}
	if !bytes.Contains(body, []byte(money)) || bytes.Count(body, []byte(`href="/donate/?campaign=synthetic-meter"`)) != 1 || !bytes.Contains(body, []byte("Synthetic campaign description.")) {
		return 0, 0, fmt.Errorf("financial-copy")
	}
	if bytes.Contains(body, []byte("Synthetic civic partner")) != (kind != "zero") ||
		(kind != "zero" && !bytes.Contains(body, []byte(`href="https://partner.example.invalid/" rel="noopener noreferrer" target="_blank"`))) {
		return 0, 0, fmt.Errorf("partner-copy")
	}
	return start, start + length, nil
}

func (f *guiCampaignMeterFixtureData) render(t *testing.T, filesystem, previous bool, kind string) []byte {
	t.Helper()
	renderer := NewRenderer(f.root)
	raw := f.current
	if previous {
		renderer, raw = NewRenderer(f.original), f.before
	} else if filesystem {
		var err error
		renderer, err = NewRendererFS(f.root, f.files)
		if err != nil {
			t.Fatal(err)
		}
	}
	trace := &guiPublicTraceLoader{delegate: renderer.loader}
	renderer.set = pongo2.NewSet("native-social", trace)
	data, _, _ := guiCampaignMeterContext(kind)
	if data == nil {
		t.Fatal("closed campaign model required")
	}
	request := httptest.NewRequest(http.MethodGet, "https://gui-fixture.invalid/campaigns/", nil)
	request.Header.Set("Accept-Language", "en")
	response := httptest.NewRecorder()
	if err := renderer.Render(response, request, "web/campaigns.html", data); err != nil {
		t.Fatal("native campaign render failed")
	}
	if response.Code != 200 || response.Header().Get("Content-Type") != "text/html; charset=utf-8" || response.Header().Get("Content-Security-Policy") != "" {
		t.Fatal("existing native campaign response changed")
	}
	body := append([]byte(nil), response.Body.Bytes()...)
	if _, err := guiPublicScriptNonceBinding(string(body), response.Header().Get("Content-Security-Policy-Report-Only"), nil); err != nil {
		t.Fatal("actual unchanged base-script nonce/header binding refused")
	}
	want := map[string]string{"web/campaigns.html": guiPublicHash([]byte(transformTemplate(string(raw)))), "base.html": guiPublicHash([]byte(transformTemplate(string(f.base.files["templates/base.html"].Data))))}
	if len(trace.loads) != 2 {
		t.Fatal("exact page/base native loader observations required")
	}
	for _, load := range trace.loads {
		if want[load.Name] == "" || want[load.Name] != load.SHA256 {
			t.Fatal("actual transformed campaign/base source stream differs")
		}
		delete(want, load.Name)
	}
	if len(want) != 0 {
		t.Fatal("native source stream missing")
	}
	if _, _, err := guiCampaignMeterDOM(body, kind, previous); err != nil {
		t.Fatal("actual owning campaign tree refused: " + err.Error())
	}
	return body
}

func (f *guiCampaignMeterFixtureData) compare(t *testing.T, filesystem bool, kind string) ([]byte, []byte) {
	t.Helper()
	before := f.render(t, false, true, kind)
	current := f.render(t, filesystem, false, kind)
	start, end, err := guiCampaignMeterDOM(before, kind, true)
	if err != nil {
		t.Fatal("pre-externalization owning tree required")
	}
	expected := append([]byte(nil), before...)
	_, percent, _ := guiCampaignMeterContext(kind)
	if percent != "" {
		oldFill := []byte(strings.Replace(guiCampaignMeterBeforeTag, "{{ c.percent }}", percent, 1))
		newFill := []byte(strings.Replace(guiCampaignMeterAfterTag, "{{ c.percent }}", percent, 1))
		segment := before[start:end]
		if bytes.Count(segment, oldFill) != 1 {
			t.Fatal("one exact owning pre-externalization fill required")
		}
		changed := bytes.Replace(append([]byte(nil), segment...), oldFill, newFill, 1)
		expected = append(append(append([]byte(nil), before[:start]...), changed...), before[end:]...)
	}
	normalize := func(raw []byte) []byte {
		canonical, hard, err := f.normalizer.normalize(raw)
		if err != nil || len(hard) != 0 {
			t.Fatal("strict released public Normalize refused campaign presentation")
		}
		return canonical
	}
	originalDOM, expectedDOM, currentDOM := normalize(before), normalize(expected), normalize(current)
	if !bytes.Equal(expectedDOM, currentDOM) {
		t.Fatal("current canonical DOM differs outside exact owned fill delta")
	}
	t.Logf("synthetic campaign kind=%s filesystem=%t current_source_inputs=%d original_sha256=%s expected_sha256=%s current_sha256=%s original_current_equal=%t; no baseline or browser claim", kind, filesystem, len(guiCampaignMeterSourcePaths), guiPublicHash(originalDOM), guiPublicHash(expectedDOM), guiPublicHash(currentDOM), bytes.Equal(originalDOM, currentDOM))
	return current, currentDOM
}

func TestGUIPublicCampaignMeterDelivery(t *testing.T) {
	f := guiNewCampaignMeterFixture(t)
	if f == nil {
		return // Committed source-only default path (ADR-0056 Q6).
	}
	disk := map[string][]byte{}
	for _, kind := range guiCampaignMeterKinds {
		for _, filesystem := range []bool{false, true} {
			mode := "os"
			if filesystem {
				mode = "fs"
			}
			t.Run(mode+"-"+kind, func(t *testing.T) {
				_, canonical := f.compare(t, filesystem, kind)
				if filesystem {
					if !bytes.Equal(disk[kind], canonical) {
						t.Fatal("current actual OS/FS canonical DOM differs")
					}
				} else {
					disk[kind] = append([]byte(nil), canonical...)
				}
			})
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run("css-"+method, func(t *testing.T) {
			response := httptest.NewRecorder()
			f.static.ServeHTTP(response, httptest.NewRequest(method, "https://gui-fixture.invalid/static/css/base.css", nil))
			if response.Code != 200 || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/css") || response.Header().Get("Content-Length") != strconv.Itoa(len(f.css)) ||
				response.Header().Get("ETag") != `"`+guiPublicHash(f.css)+`"` || response.Header().Get("Cache-Control") != "public, max-age=3600" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("actual full current CSS static response differs")
			}
			if (method == http.MethodGet && !bytes.Equal(response.Body.Bytes(), f.css)) || (method == http.MethodHead && response.Body.Len() != 0) {
				t.Fatal("actual full current CSS GET/HEAD bytes differ")
			}
		})
	}
}

func TestGUIPublicCampaignMeterRefusals(t *testing.T) {
	f := guiNewCampaignMeterFixture(t)
	if f == nil {
		return // Committed source-only default path (ADR-0056 Q6).
	}
	body, _ := f.compare(t, false, "fractional")
	_, percent, money := guiCampaignMeterContext("fractional")
	fill := strings.Replace(guiCampaignMeterAfterTag, "{{ c.percent }}", percent, 1)
	for _, mutation := range []struct {
		name string
		from string
		to   string
		code string
	}{
		{"missing-fill", fill, "", "fill-count"},
		{"duplicate-fill", fill, fill + fill, "fill-count"},
		{"wrong-width", `width="` + percent + `%"`, `width="50%"`, "fill-width"},
		{"wrong-aria-now", `aria-valuenow="` + percent + `"`, `aria-valuenow="50"`, "outer-aria"},
		{"wrong-aria-max", `aria-valuemax="100"`, `aria-valuemax="200"`, "outer-aria"},
		{"focusable-decoration", `focusable="false"`, `focusable="true"`, "decorative-svg"},
		{"unexpected-inline-style", `class="campaign-meter-fill"`, `class="campaign-meter-fill" style="width:50%"`, "decorative-svg"},
		{"changed-financial-copy", money, "Raised 2.00 of 3.00 EUR goal.", "financial-copy"},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			start, end := 0, len(body)
			if mutation.name != "changed-financial-copy" {
				var err error
				start, end, err = guiCampaignMeterDOM(body, "fractional", false)
				if err != nil {
					t.Fatal("exact positive owning meter required")
				}
			}
			segment := body[start:end]
			if bytes.Count(segment, []byte(mutation.from)) != 1 {
				t.Fatal("exact unique positive mutation target required")
			}
			changedSegment := bytes.Replace(append([]byte(nil), segment...), []byte(mutation.from), []byte(mutation.to), 1)
			changed := append(append(append([]byte(nil), body[:start]...), changedSegment...), body[end:]...)
			if _, _, err := guiCampaignMeterDOM(changed, "fractional", false); err == nil || err.Error() != mutation.code {
				t.Fatal("mutation did not reach its exact owning refusal")
			}
			if _, _, err := guiCampaignMeterDOM(body, "fractional", false); err != nil {
				t.Fatal("positive source frame was aliased or changed")
			}
		})
	}
}
