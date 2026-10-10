package web

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/flosch/pongo2/v6"
)

const guiCommunityStyleTemplate = "apps/web/templates/web/communities.html"
const guiCommunityStylePreimageSHA256 = "ce0ea6ed2667f94c543afac220c1535b20ec209e940d20d09df6f181efc5f4f6"

var guiCommunityStyleDelta = [][2]string{
	{`<h3 style="margin:.2rem 0">`, `<h3 class="community-card-title">`},
	{`<p class="muted" style="margin:0">`, `<p class="muted u-m-0">`},
}

type guiCommunityStyleFixture struct {
	root              string
	current, original []byte
	base              *guiPublicFSSnapshot
	normalizer        *guiPublicNormalizer
}

func guiNewCommunityStyleFixture(t *testing.T) *guiCommunityStyleFixture {
	t.Helper()
	root, err := filepath.Abs("../../../../")
	if err != nil {
		t.Fatal(err)
	}
	read := func(name string) []byte {
		input, err := os.OpenRoot(root)
		if err != nil {
			t.Fatal(err)
		}
		file, err := input.Open(name)
		input.Close()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := readRendererFSFile(file, 512<<10)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	current := read(guiCommunityStyleTemplate)
	original := append([]byte(nil), current...)
	for _, delta := range guiCommunityStyleDelta {
		if bytes.Count(current, []byte(delta[1])) != 1 {
			t.Fatal("current public-card source does not have exactly the declared class delta")
		}
		original = bytes.Replace(original, []byte(delta[1]), []byte(delta[0]), 1)
	}
	// The separately declared public-footer delta is outside the old card cases.
	const currentFooter = "{% if page.has_other_pages %}\n  <p class=\"muted u-mt-lg\">"
	const previousFooter = "{% if page.has_other_pages %}\n  <p class=\"muted\" style=\"margin-top:1rem\">"
	if bytes.Count(original, []byte(currentFooter)) != 1 {
		t.Fatal("current source does not have exactly the declared public pagination delta")
	}
	original = bytes.Replace(original, []byte(currentFooter), []byte(previousFooter), 1)
	// This reconstructs the exact known pre-change source, not a new baseline.
	// Its hash pins every private-group/pagination/conditional/text byte too.
	if len(original) != 3349 || guiPublicHash(original) != guiCommunityStylePreimageSHA256 {
		t.Fatal("public-card inverse differs from the committed pre-change template")
	}
	css := read("static/css/base.css")
	rule := []byte(".card h3.community-card-title { margin: .2rem 0; }\n")
	if bytes.Count(css, rule) != 1 || !bytes.Contains(css, []byte(".u-m-0 { margin: 0; }")) ||
		bytes.Index(css, rule) <= bytes.Index(css, []byte(".card h3 { margin: 0 0 .25rem; }")) {
		t.Fatal("declared source CSS mapping/order differs")
	}
	// Source-specificity reasoning only: the typed two-class title selector
	// outranks .card > :first-child and .card h3. The paragraph is second/last;
	// .u-m-0 beats the ordinary p margin, and the last-child bottom remains zero.
	// This is not a browser/computed-style/CSSOM or whole-page CSP test.
	base, err := guiPublicFilesystemSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	normalizer, err := guiPublicNewNormalizer(root)
	if err != nil || normalizer == nil {
		t.Fatal("actual existing released normalizer/archive/original-corpus bindings are required")
	}
	checkpoint := read("docs/reviews/gui-public-original/checkpoint.json")
	if _, err := guiPublicReadReference(*guiPublicGoldenReference, checkpoint); err != nil {
		t.Fatal(err)
	}
	f := &guiCommunityStyleFixture{root: root, current: current, original: original, base: base, normalizer: normalizer}
	t.Cleanup(func() {
		if err := base.check(); err != nil {
			t.Error(err)
		}
		if !bytes.Equal(read(guiCommunityStyleTemplate), current) || !bytes.Equal(read("static/css/base.css"), css) {
			t.Error("actual template/CSS source changed during focused checks")
		}
		if err := normalizer.checkUnchanged(); err != nil {
			t.Error(err)
		}
		if _, err := guiPublicReadReference(*guiPublicGoldenReference, checkpoint); err != nil {
			t.Error(err)
		}
	})
	return f
}

func (f *guiCommunityStyleFixture) render(t *testing.T, filesystem, original bool, kind string) []byte {
	t.Helper()
	renderer := NewRenderer(f.root)
	if filesystem || original {
		files := fstest.MapFS{}
		for name, file := range f.base.files {
			files[name] = file
		}
		raw := f.current
		if original {
			raw = f.original
		}
		files[guiCommunityStyleTemplate] = &fstest.MapFile{Data: append([]byte(nil), raw...), Mode: 0o444}
		digest := guiPublicHash(raw)
		t.Cleanup(func() {
			if len(files) != 7 || files[guiCommunityStyleTemplate] == nil || files[guiCommunityStyleTemplate].Mode != 0o444 || guiPublicHash(files[guiCommunityStyleTemplate].Data) != digest {
				t.Error("caller-owned public-card source snapshot changed")
			}
			for name, file := range f.base.files {
				if files[name] != file {
					t.Error("original six-entry FS snapshot replaced")
				}
			}
		})
		var err error
		renderer, err = NewRendererFS(f.root, files)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Synthetic anonymous presentation only. No group records, creation grant,
	// pagination claim, registered route, DB/auth/cohort admission or child data.
	page := []any{}
	if kind != "empty" {
		page = append(page, map[string]any{"slug": "synthetic-public-community", "name": "Synthetic public community", "tier": kind,
			"category": map[string]any{"name": "Synthetic category"}, "area": map[string]any{"name": "Synthetic area"}})
	}
	pageWrapper, _ := socialPagination("", len(page), 30)
	pageWrapper["object_list"] = page
	groupsWrapper, _ := socialPagination("", 0, 30)
	request := httptest.NewRequest("GET", "https://gui-fixture.invalid/communities/", nil)
	request.Header.Set("Accept-Language", "en")
	response := httptest.NewRecorder()
	if err := renderer.Render(response, request, "web/communities.html", pongo2.Context{"page": pageWrapper, "groups_page": groupsWrapper, "can_create": false}); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || response.Header().Get("Content-Type") != "text/html; charset=utf-8" || response.Header().Get("Content-Security-Policy-Report-Only") == "" || response.Header().Get("Content-Security-Policy") != "" {
		t.Fatal("existing native response/report-only policy changed")
	}
	body := append([]byte(nil), response.Body.Bytes()...)
	if kind == "empty" {
		if !bytes.Contains(body, []byte("No communities yet")) || bytes.Contains(body, []byte("Synthetic public community")) {
			t.Fatal("actual public empty arm differs")
		}
	} else if bytes.Count(body, []byte(`<a href="/communities/synthetic-public-community/">Synthetic public community</a>`)) != 1 || bytes.Contains(body, []byte("No communities yet")) {
		t.Fatal("actual public populated arm differs")
	}
	if kind == "type" && !bytes.Contains(body, []byte("Synthetic category &middot; Synthetic area")) {
		t.Fatal("actual type-community label arm differs")
	}
	if kind == "category" && !bytes.Contains(body, []byte("All Synthetic category &middot; Synthetic area")) {
		t.Fatal("actual category-community label arm differs")
	}
	return body
}

func (f *guiCommunityStyleFixture) compare(t *testing.T, filesystem bool, kind string) ([]byte, []byte) {
	t.Helper()
	before := f.render(t, true, true, kind)
	current := f.render(t, filesystem, false, kind)
	expected := append([]byte(nil), before...)
	count := 0
	if kind != "empty" {
		count = 1
	}
	for _, delta := range guiCommunityStyleDelta {
		if bytes.Count(before, []byte(delta[0])) != count {
			t.Fatal("actual pre-change synthetic body does not have exactly the declared target attributes")
		}
		expected = bytes.Replace(expected, []byte(delta[0]), []byte(delta[1]), count)
	}
	normalize := func(raw []byte) []byte {
		canonical, hard, err := f.normalizer.normalize(raw)
		if err != nil || len(hard) != 0 {
			t.Fatal("actual released DOM normalizer refused the finite presentation comparison")
		}
		return canonical
	}
	originalDOM, expectedDOM, currentDOM := normalize(before), normalize(expected), normalize(current)
	if !bytes.Equal(expectedDOM, currentDOM) || (bytes.Equal(originalDOM, currentDOM) != (kind == "empty")) {
		t.Fatal("actual canonical DOM differs outside the explicit public-card class delta")
	}
	t.Logf("synthetic DOM kind=%s filesystem=%t delta_attributes=%d original_sha256=%s expected_sha256=%s current_sha256=%s", kind, filesystem, count*2, guiPublicHash(originalDOM), guiPublicHash(expectedDOM), guiPublicHash(currentDOM))
	return current, expectedDOM
}

func TestGUIPublicCommunityStyleDOM(t *testing.T) {
	f := guiNewCommunityStyleFixture(t)
	for _, filesystem := range []bool{false, true} {
		for _, kind := range []string{"empty", "type", "category"} {
			t.Run(fmt.Sprintf("filesystem-%t/%s", filesystem, kind), func(t *testing.T) {
				f.compare(t, filesystem, kind)
			})
		}
	}
}

func TestGUIPublicCommunityStyleRejectsDOMMutations(t *testing.T) {
	f := guiNewCommunityStyleFixture(t)
	body, expected := f.compare(t, false, "type")
	for _, mutation := range []struct {
		name string
		from string
		to   string
	}{
		{"missing-title-class", `<h3 class="community-card-title">`, `<h3>`},
		{"missing-paragraph-class", `<p class="muted u-m-0">`, `<p class="muted">`},
		{"restored-inline-style", `<h3 class="community-card-title">`, `<h3 class="community-card-title" style="margin:.2rem 0">`},
		{"changed-public-text", `Synthetic public community`, `Synthetic changed community`},
		{"changed-public-link", `/communities/synthetic-public-community/`, `/communities/synthetic-other/`},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			if bytes.Count(body, []byte(mutation.from)) != 1 {
				t.Fatal("actual mutation target is not unique")
			}
			changed := bytes.Replace(body, []byte(mutation.from), []byte(mutation.to), 1)
			canonical, hard, err := f.normalizer.normalize(changed)
			if err != nil || len(hard) != 0 || bytes.Equal(canonical, expected) {
				t.Fatal("actual safe DOM mutation was not independently distinguished")
			}
		})
	}
	t.Run("reversed-card-children", func(t *testing.T) {
		s := string(body)
		hStart := strings.Index(s, `<h3 class="community-card-title">`)
		pStart := strings.Index(s, `<p class="muted u-m-0">`)
		if hStart < 0 || pStart < 0 {
			t.Fatal("actual card children are unavailable for the mutation")
		}
		hClose := strings.Index(s[hStart:], "</h3>")
		pClose := strings.Index(s[pStart:], "</p>")
		if hClose < 0 || pClose < 0 {
			t.Fatal("actual card closing tags are unavailable for the mutation")
		}
		hEnd := hClose + hStart + len("</h3>")
		pEnd := pClose + pStart + len("</p>")
		if hStart < 0 || hEnd <= hStart || pStart < hEnd || pEnd <= pStart {
			t.Fatal("actual ordered card children are unavailable for the mutation")
		}
		changed := []byte(s[:hStart] + s[pStart:pEnd] + s[hEnd:pStart] + s[hStart:hEnd] + s[pEnd:])
		canonical, hard, err := f.normalizer.normalize(changed)
		if err != nil || len(hard) != 0 || bytes.Equal(canonical, expected) {
			t.Fatal("actual DOM child order mutation was not distinguished")
		}
	})
}
