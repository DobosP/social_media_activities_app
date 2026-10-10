package web

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/flosch/pongo2/v6"
)

const guiPaginationBeforeSHA256 = "14a4cde5dd7c0951287eb5d5976258810f15448503d0d3d7efe6be9608cc4330"
const guiPaginationCurrentSource = "{% if page.has_other_pages %}\n  <p class=\"muted u-mt-lg\">"
const guiPaginationPreviousSource = "{% if page.has_other_pages %}\n  <p class=\"muted\" style=\"margin-top:1rem\">"
const guiPaginationCurrentTag = `<p class="muted u-mt-lg">`
const guiPaginationPreviousTag = `<p class="muted" style="margin-top:1rem">`

type guiCommunityPaginationCase struct {
	name, query          string
	count, number, pages int
	previous, next, rows int
}

var guiCommunityPaginationCases = []guiCommunityPaginationCase{
	{"empty", "", 0, 1, 1, 0, 0, 0},
	{"single", "", 1, 1, 1, 0, 0, 1},
	{"first", "page=1", 61, 1, 3, 0, 2, 30},
	{"middle", "page=2", 61, 2, 3, 1, 3, 30},
	{"last", "page=3", 61, 3, 3, 2, 0, 1},
	{"filtered-middle", "page=2&category=synthetic-category&area=synthetic-area&gpage=9", 61, 2, 3, 1, 3, 30},
}

func guiCommunityPaginationBefore(t *testing.T, f *guiCommunityStyleFixture) []byte {
	t.Helper()
	css, err := os.ReadFile(filepath.Join(f.root, "static/css/base.css"))
	if err != nil || bytes.Count(css, []byte(".u-mt-lg { margin-top: 1rem; }")) != 1 || !bytes.Contains(css, []byte("p { margin: .6rem 0; }")) || !bytes.Contains(css, []byte(".muted { color: var(--muted); }")) {
		t.Fatal("existing source utility/global paragraph mapping differs")
	}
	// The footer follows the closed public-card loop, outside .card. Its class
	// outranks ordinary p margin; .muted supplies color only. Source reasoning
	// and canonical DOM evidence are not computed-style/browser/CSP proof.
	if bytes.Count(f.current, []byte(guiPaginationCurrentSource)) != 1 {
		t.Fatal("actual public footer source delta is not unique")
	}
	previous := bytes.Replace(f.current, []byte(guiPaginationCurrentSource), []byte(guiPaginationPreviousSource), 1)
	if len(previous) != 3344 || guiPublicHash(previous) != guiPaginationBeforeSHA256 {
		t.Fatal("exact pre-pagination template differs; private groups/other markup must remain unchanged")
	}
	return previous
}

func guiCommunityPaginationRender(t *testing.T, f *guiCommunityStyleFixture, filesystem, previous bool, fixture guiCommunityPaginationCase) []byte {
	t.Helper()
	renderer := NewRenderer(f.root)
	if filesystem || previous {
		files := fstest.MapFS{}
		for name, file := range f.base.files {
			files[name] = file
		}
		raw := f.current
		if previous {
			raw = guiCommunityPaginationBefore(t, f)
		}
		files[guiCommunityStyleTemplate] = &fstest.MapFile{Data: append([]byte(nil), raw...), Mode: 0o444}
		digest := guiPublicHash(raw)
		t.Cleanup(func() {
			if len(files) != 7 || files[guiCommunityStyleTemplate] == nil || files[guiCommunityStyleTemplate].Mode != 0o444 || guiPublicHash(files[guiCommunityStyleTemplate].Data) != digest {
				t.Error("caller-owned pagination template snapshot changed")
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
	address := "https://gui-fixture.invalid/communities/"
	if fixture.query != "" {
		address += "?" + fixture.query
	}
	request := httptest.NewRequest("GET", address, nil)
	request.Header.Set("Accept-Language", "en")
	// Actual native helper/context shapes with fictional public rows only.
	// Query filters are presentation inputs; no SQL/filtering/admission is run.
	page, offset := socialPagination(request.URL.Query().Get("page"), fixture.count, 30)
	groups, _ := socialPagination(request.URL.Query().Get("gpage"), 0, 30)
	if page["number"] != fixture.number || page["paginator"].(map[string]any)["num_pages"] != fixture.pages || offset != (fixture.number-1)*30 {
		t.Fatal("actual paginator helper does not match the explicit synthetic state")
	}
	rows := []map[string]any{}
	for index := 0; index < fixture.rows; index++ {
		rows = append(rows, map[string]any{"slug": fmt.Sprintf("synthetic-public-community-%d", offset+index), "name": fmt.Sprintf("Synthetic public community %d", offset+index), "tier": "type",
			"category": map[string]any{"name": "Synthetic category"}, "area": map[string]any{"name": "Synthetic area"}})
	}
	page["object_list"] = rows
	response := httptest.NewRecorder()
	if err := renderer.Render(response, request, "web/communities.html", pongo2.Context{"page": page, "groups_page": groups, "can_create": false}); err != nil {
		t.Fatal(err)
	}
	if request.URL.RawQuery != fixture.query || response.Code != 200 || response.Header().Get("Content-Type") != "text/html; charset=utf-8" || response.Header().Get("Content-Security-Policy-Report-Only") == "" || response.Header().Get("Content-Security-Policy") != "" {
		t.Fatal("existing request/native response/report-only policy changed")
	}
	body := append([]byte(nil), response.Body.Bytes()...)
	if bytes.Count(body, []byte(`<h3 class="community-card-title">`)) != fixture.rows {
		t.Fatal("actual paginator object_list did not render the expected synthetic rows")
	}
	tag := guiPaginationCurrentTag
	if previous {
		tag = guiPaginationPreviousTag
	}
	visible := fixture.pages > 1
	if !visible {
		if bytes.Contains(body, []byte(tag)) || bytes.Contains(body, []byte(`href="?page=`)) {
			t.Fatal("single/empty pagination unexpectedly appeared")
		}
		return body
	}
	bar := guiCommunityPaginationBar(t, body, tag)
	if !bytes.Contains(bar, []byte(fmt.Sprintf("Page %d of %d", fixture.number, fixture.pages))) {
		t.Fatal("native page/total label differs")
	}
	links := 0
	for _, link := range []struct {
		label string
		page  int
	}{{"Previous", fixture.previous}, {"Next", fixture.next}} {
		if link.page == 0 {
			if bytes.Contains(bar, []byte(link.label)) {
				t.Fatal("native pagination displayed an unavailable direction")
			}
		} else {
			links++
			if bytes.Count(bar, []byte(fmt.Sprintf(`href="?page=%d"`, link.page))) != 1 || bytes.Count(bar, []byte(link.label)) != 1 {
				t.Fatal("native previous/next query link or wording differs")
			}
		}
	}
	if bytes.Count(bar, []byte(`href="?page=`)) != links || bytes.Contains(bar, []byte("?gpage=")) || bytes.Contains(bar, []byte("category=")) || bytes.Contains(bar, []byte("area=")) {
		t.Fatal("existing public page-only href behavior changed or crossed group pagination")
	}
	return body
}

func guiCommunityPaginationBar(t *testing.T, body []byte, tag string) []byte {
	t.Helper()
	if bytes.Count(body, []byte(tag)) != 1 {
		t.Fatal("actual visible public pagination paragraph is not unique")
	}
	start := bytes.Index(body, []byte(tag))
	end := bytes.Index(body[start:], []byte("</p>"))
	if end < 0 {
		t.Fatal("actual public pagination paragraph is not closed")
	}
	return append([]byte(nil), body[start:start+end+len("</p>")]...)
}

func guiCommunityPaginationCompare(t *testing.T, f *guiCommunityStyleFixture, filesystem bool, fixture guiCommunityPaginationCase) ([]byte, []byte) {
	t.Helper()
	before := guiCommunityPaginationRender(t, f, true, true, fixture)
	current := guiCommunityPaginationRender(t, f, filesystem, false, fixture)
	count := 0
	if fixture.pages > 1 {
		count = 1
	}
	if bytes.Count(before, []byte(guiPaginationPreviousTag)) != count {
		t.Fatal("pre-change synthetic body lacks exactly the declared conditional footer attribute")
	}
	expected := bytes.Replace(before, []byte(guiPaginationPreviousTag), []byte(guiPaginationCurrentTag), count)
	normalize := func(raw []byte) []byte {
		canonical, hard, err := f.normalizer.normalize(raw)
		if err != nil || len(hard) != 0 {
			t.Fatal("actual released DOM normalizer refused pagination comparison")
		}
		return canonical
	}
	oldDOM, expectedDOM, currentDOM := normalize(before), normalize(expected), normalize(current)
	if !bytes.Equal(expectedDOM, currentDOM) || (bytes.Equal(oldDOM, currentDOM) != (count == 0)) {
		t.Fatal("canonical DOM differs outside the explicit public pagination class delta")
	}
	t.Logf("synthetic pagination DOM case=%s filesystem=%t delta_attributes=%d original_sha256=%s expected_sha256=%s current_sha256=%s", fixture.name, filesystem, count, guiPublicHash(oldDOM), guiPublicHash(expectedDOM), guiPublicHash(currentDOM))
	return current, expectedDOM
}

func TestGUIPublicCommunityPaginationDOM(t *testing.T) {
	f := guiNewCommunityStyleFixture(t)
	for _, filesystem := range []bool{false, true} {
		for _, fixture := range guiCommunityPaginationCases {
			t.Run(fmt.Sprintf("filesystem-%t/%s", filesystem, fixture.name), func(t *testing.T) {
				guiCommunityPaginationCompare(t, f, filesystem, fixture)
			})
		}
	}
}

func TestGUIPublicCommunityPaginationRejectsDOMMutations(t *testing.T) {
	f := guiNewCommunityStyleFixture(t)
	body, expected := guiCommunityPaginationCompare(t, f, false, guiCommunityPaginationCases[5])
	bar := guiCommunityPaginationBar(t, body, guiPaginationCurrentTag)
	for _, mutation := range []struct {
		name string
		from string
		to   string
	}{
		{"missing-class", guiPaginationCurrentTag, `<p class="muted">`},
		{"restored-inline-style", guiPaginationCurrentTag, `<p class="muted u-mt-lg" style="margin-top:1rem">`},
		{"changed-previous-link", `href="?page=1"`, `href="?page=99"`},
		{"changed-next-link", `href="?page=3"`, `href="?page=99"`},
		{"changed-page-text", `Page 2 of 3`, `Page 9 of 3`},
		{"crossed-group-query", `href="?page=3"`, `href="?gpage=3"`},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			if bytes.Count(bar, []byte(mutation.from)) != 1 {
				t.Fatal("actual pagination mutation target is not unique")
			}
			changedBar := bytes.Replace(bar, []byte(mutation.from), []byte(mutation.to), 1)
			changed := bytes.Replace(body, bar, changedBar, 1)
			canonical, hard, err := f.normalizer.normalize(changed)
			if err != nil || len(hard) != 0 || bytes.Equal(canonical, expected) {
				t.Fatal("actual safe pagination DOM mutation was not distinguished")
			}
		})
	}
}
