package web

import (
	"bytes"
	"fmt"
	"html"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/avatars"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

type guiActivityBrowseCase struct {
	name      string
	view      string
	query     string
	page      string
	count     int
	beginners bool
	suggest   bool
}

var guiActivityBrowseCases = []guiActivityBrowseCase{
	{"empty-list", "list", "", "1", 0, false, false},
	{"empty-search-cards", "cards", "synthetic", "1", 0, false, true},
	{"filled-list", "list", "", "1", 2, false, false},
	{"filled-cards-beginners", "cards", "", "1", 2, true, false},
	{"cards-first", "cards", "synthetic", "1", 49, false, false},
	{"cards-middle", "cards", "synthetic", "2", 49, true, false},
	{"cards-last", "cards", "", "3", 49, false, false},
}

var guiActivityBrowseScript = guiPlacesNonceScript{"/static/js/browse-modes.js", true}

type guiActivityBrowseFixture struct {
	root         string
	originalRoot string
	current      map[string][]byte
	original     map[string][]byte
	base         *guiPublicFSSnapshot
	normalizer   *guiPublicNormalizer
}

func guiNewActivityBrowseFixture(t *testing.T) *guiActivityBrowseFixture {
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
	current, original := map[string][]byte{}, map[string][]byte{}
	const target = "apps/web/templates/web/activities.html"
	current[target] = read(target)
	added := []byte(`<script nonce="{{ request.csp_nonce }}" src="{% static 'js/browse-modes.js' %}" defer>`)
	if bytes.Count(current[target], added) != 1 {
		t.Fatal("exact one existing browse script nonce attribute required")
	}
	original[target] = bytes.Replace(current[target], added, []byte(`<script src="{% static 'js/browse-modes.js' %}" defer>`), 1)
	if len(original[target]) != 5197 || guiPublicHash(original[target]) != "efb88d6e2731dce2433e0836b277e7748683f467eb19e052bc3ed56014403cd3" {
		t.Fatal("whole activity browse inverse differs beyond its one nonce attribute")
	}
	for _, name := range []string{"apps/web/templates/web/_activity_card.html", "apps/web/templates/web/_near_me.html"} {
		current[name] = read(name)
		original[name] = append([]byte(nil), current[name]...)
	}
	base, err := guiPublicFilesystemSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	normalizer, err := guiPublicNewNormalizer(root)
	if err != nil || normalizer == nil {
		t.Fatal("existing actual764 normalizer/released archive bindings required")
	}
	checkpoint := read("docs/reviews/gui-public-original/checkpoint.json")
	if _, err := guiPublicReadReference(*guiPublicGoldenReference, checkpoint); err != nil {
		t.Fatal(err)
	}
	// Nine source/catalog files, not a response baseline or workspace clone.
	// Both original OS and FS paths use the exact inverse template bytes.
	originalRoot := t.TempDir()
	oldFiles := map[string][]byte{}
	for name, file := range base.files {
		oldFiles[name] = file.Data
	}
	for name, raw := range original {
		oldFiles[name] = raw
	}
	for name, raw := range oldFiles {
		file := filepath.Join(originalRoot, name)
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, raw, 0o444); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(file, 0o444); err != nil {
			t.Fatal(err)
		}
	}
	f := &guiActivityBrowseFixture{root, originalRoot, current, original, base, normalizer}
	t.Cleanup(func() {
		if err := base.check(); err != nil {
			t.Error(err)
		}
		for name, raw := range current {
			if !bytes.Equal(read(name), raw) {
				t.Error("actual activity browse source changed during presentation checks")
			}
		}
		for name, raw := range oldFiles {
			file := filepath.Join(originalRoot, name)
			info, err := os.Lstat(file)
			actual, readErr := os.ReadFile(file)
			if err != nil || readErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o444 || !bytes.Equal(actual, raw) {
				t.Error("private original activity OS source bytes/mode changed")
			}
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

// Native nonce/path/load/order checks are separate from released DOM hard
// findings. No browser JS, geolocation or API filtering executes in this test.
func guiActivityBrowseNonceBinding(body, policy string, current, near bool) (string, error) {
	publicID := "00000000-0000-4000-8000-000000009001"
	base, err := guiPublicScriptNonceBinding(body, policy, &publicID)
	if err != nil {
		return "", err
	}
	var nonce string
	for _, directive := range strings.Split(policy, ";") {
		fields := strings.Fields(directive)
		if len(fields) > 0 && fields[0] == "script-src" {
			for _, token := range fields[1:] {
				if strings.HasPrefix(token, "'nonce-") && strings.HasSuffix(token, "'") {
					nonce = strings.TrimSuffix(strings.TrimPrefix(token, "'nonce-"), "'")
				}
			}
		}
	}
	if nonce == "" || guiPublicHash([]byte(nonce)) != base["header_nonce_sha256"] {
		return "", fmt.Errorf("actual browse/base header nonce differs")
	}
	value := ""
	if current {
		value = nonce
	}
	tag := guiPlacesNonceTag(guiActivityBrowseScript, value)
	position := strings.Index(body, tag)
	if strings.Count(body, tag) != 1 || strings.Count(body, `src="/static/js/browse-modes.js"`) != 1 || position <= strings.Index(body, `id="site-js"`) {
		return "", fmt.Errorf("browse nonce/path/defer/base order differs")
	}
	nearTag := guiPlacesNonceTag(guiPlacesNonceScript{"/static/js/near-me.js", true}, nonce)
	if near {
		if strings.Count(body, nearTag) != 1 || strings.Index(body, nearTag) >= position {
			return "", fmt.Errorf("existing near script nonce/relative order differs")
		}
	} else if strings.Contains(body, `src="/static/js/near-me.js"`) {
		return "", fmt.Errorf("near script emitted outside query-empty branch")
	}
	return nonce, nil
}

func guiActivityBrowseBaseQS(fixture guiActivityBrowseCase) string {
	params := url.Values{}
	if fixture.query != "" {
		params.Set("q", fixture.query)
	}
	if fixture.beginners {
		params.Set("beginners", "true")
	}
	return params.Encode()
}

func guiActivityBrowsePageHref(fixture guiActivityBrowseCase, page int) string {
	base := html.EscapeString(guiActivityBrowseBaseQS(fixture))
	if base != "" {
		base += "&amp;"
	}
	return "?" + base + "view=" + fixture.view + "&amp;page=" + fmt.Sprint(page)
}

func (f *guiActivityBrowseFixture) render(t *testing.T, filesystem, original bool, fixture guiActivityBrowseCase) ([]byte, string) {
	t.Helper()
	renderer := NewRenderer(f.root)
	if original && !filesystem {
		renderer = NewRenderer(f.originalRoot)
	}
	if filesystem {
		files := fstest.MapFS{}
		for name, file := range f.base.files {
			files[name] = file
		}
		selected := f.current
		if original {
			selected = f.original
		}
		for name, raw := range selected {
			files[name] = &fstest.MapFile{Data: append([]byte(nil), raw...), Mode: 0o444}
		}
		t.Cleanup(func() {
			if len(files) != 9 {
				t.Error("browse FS source member set changed")
			}
			for name, raw := range selected {
				if files[name] == nil || files[name].Mode != 0o444 || !bytes.Equal(files[name].Data, raw) {
					t.Error("browse FS source bytes/mode changed")
				}
			}
			for name, file := range f.base.files {
				if files[name] != file {
					t.Error("original six-entry FS source replaced")
				}
			}
		})
		var err error
		renderer, err = NewRendererFS(f.root, files)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Fictional authenticated adult PRESENTATION only, not real cohort-visible
	// rows or actor/ownership/privacy/admission/DB/API/filter/geo qualification.
	actor := platform.Actor{ID: 9001, PublicID: "00000000-0000-4000-8000-000000009001", Username: "gui_fixture_adult", DisplayName: "GUI fixture", AgeBand: "adult", Cohort: "adult", IsActive: true}
	request := platform.WithActor(httptest.NewRequest("GET", "https://gui-fixture.invalid/activities/", nil), actor)
	request.Header.Set("Accept-Language", "en")
	user := socialActor(actor)
	user["avatar_uri"] = avatars.DataURI(avatars.RenderGeneration(avatars.DefaultGeneration, avatars.SignatureSeed(actor.Username, avatars.DefaultGeneration, 0), nil, nil, avatars.Options{PX: 80}))
	page, offset := socialPagination(fixture.page, fixture.count, 24)
	rows := []map[string]any{}
	for index := offset; index < min(offset+24, fixture.count); index++ {
		rows = append(rows, map[string]any{"pk": 1000 + index, "title": fmt.Sprintf("Synthetic activity %d", index), "activity_type": map[string]any{"slug": "synthetic-type", "name": "Synthetic type"}, "secondary_types": map[string]any{"all": []any{}}, "starts_at": time.Date(2030, 1, 2, 12, 0, 0, 0, time.UTC), "place": map[string]any{"display_name": "Synthetic public place", "address_city": "Synthetic city"}, "description": "Synthetic activity description.", "cost_band": "unspecified", "difficulty": "unspecified", "status": "open", "beginners_welcome": fixture.beginners, "guardian_accompanied": false})
	}
	page["object_list"] = rows
	data := pongo2.Context{"user": user, "csrf": "synthetic-browse-csrf", "page_obj": page, "activities": rows, "query": fixture.query, "view_mode": fixture.view, "base_qs": guiActivityBrowseBaseQS(fixture), "beginners_only": fixture.beginners, "near_active": false}
	if fixture.suggest {
		data["did_you_mean"], data["did_you_mean_q"] = "alternative", "q=alternative"
	}
	response := httptest.NewRecorder()
	if err := renderer.Render(response, request, "web/activities.html", data); err != nil {
		t.Fatal(err)
	}
	body := append([]byte(nil), response.Body.Bytes()...)
	policy := response.Header().Get("Content-Security-Policy-Report-Only")
	if response.Code != 200 || response.Header().Get("Content-Type") != "text/html; charset=utf-8" || policy == "" || response.Header().Get("Content-Security-Policy") != "" {
		t.Fatal("existing browse response/report-only mode differs")
	}
	if _, err := guiActivityBrowseNonceBinding(string(body), policy, !original, fixture.query == ""); err != nil {
		t.Fatal(err)
	}
	if bytes.Count(body, []byte(`<form method="get" action="/activities/" class="search-bar" role="search">`)) != 1 || !bytes.Contains(body, []byte(`name="q" value="`+fixture.query+`"`)) || bytes.Count(body, []byte(`name="beginners" value="true"`)) != map[bool]int{false: 0, true: 1}[fixture.beginners] || bytes.Count(body, []byte(`name="view" value="cards"`)) != map[bool]int{false: 0, true: 1}[fixture.view == "cards"] {
		t.Fatal("existing GET search/hidden filter inputs differ")
	}
	if bytes.Count(body, []byte(`class="browse-item`)) != len(rows) || bytes.Count(body, []byte(`data-deck-prev`)) != map[bool]int{false: 0, true: 1}[len(rows) > 0] || !bytes.Contains(body, []byte(`data-browse data-view="`+fixture.view+`"`)) || !bytes.Contains(body, []byte("Shown to people your age only.")) {
		t.Fatal("actual browse mode/card count/disclosure differs")
	}
	active := `data-view-btn="` + fixture.view + `"`
	position := bytes.Index(body, []byte(active))
	if position < 0 {
		t.Fatal("actual selected browse link missing")
	}
	end := bytes.Index(body[position:], []byte(">"))
	if end < 0 || !bytes.Contains(body[position:position+end], []byte(`aria-current="page"`)) {
		t.Fatal("actual selected browse link state differs")
	}
	if len(rows) == 0 {
		copy := "Nothing upcoming yet. Be the first to organise something!"
		if fixture.query != "" {
			copy = "No upcoming activities match your search."
		}
		if !bytes.Contains(body, []byte(copy)) {
			t.Fatal("actual query/empty browse copy differs")
		}
	} else if !bytes.Contains(body, []byte(fmt.Sprintf(`href="/activities/%d/">Synthetic activity %d</a>`, 1000+offset, offset))) || !bytes.Contains(body, []byte("Synthetic public place")) {
		t.Fatal("actual populated activity link/title/place differs")
	}
	if bytes.Count(body, []byte(`<a href="?q=alternative">alternative</a>`)) != map[bool]int{false: 0, true: 1}[fixture.suggest] || bytes.Count(body, []byte(`id="near-me-btn"`)) != map[bool]int{false: 0, true: 1}[fixture.query == ""] {
		t.Fatal("actual suggestion/query-empty near control differs")
	}
	previous, next := page["has_previous"].(bool), page["has_next"].(bool)
	for _, link := range []struct {
		present bool
		number  int
		text    string
	}{{previous, page["previous_page_number"].(int), "← Newer"}, {next, page["next_page_number"].(int), "More →"}} {
		target := `href="` + guiActivityBrowsePageHref(fixture, link.number) + `">` + link.text + `</a>`
		if bytes.Count(body, []byte(target)) != map[bool]int{false: 0, true: 1}[link.present] {
			t.Fatal("actual pagination filter/view link or branch differs")
		}
	}
	if bytes.Count(body, []byte(`aria-label="Pages"`)) != map[bool]int{false: 0, true: 1}[page["has_other_pages"].(bool)] {
		t.Fatal("actual single/multi-page navigation branch differs")
	}
	return body, policy
}

func (f *guiActivityBrowseFixture) compare(t *testing.T, filesystem bool, fixture guiActivityBrowseCase) ([]byte, string, []byte) {
	t.Helper()
	before, _ := f.render(t, filesystem, true, fixture)
	current, policy := f.render(t, filesystem, false, fixture)
	second, secondPolicy := f.render(t, filesystem, false, fixture)
	first, err := guiActivityBrowseNonceBinding(string(current), policy, true, fixture.query == "")
	if err != nil {
		t.Fatal(err)
	}
	next, err := guiActivityBrowseNonceBinding(string(second), secondPolicy, true, fixture.query == "")
	if err != nil || first == next {
		t.Fatal("actual browse response nonce not fresh/bound")
	}
	old := []byte(guiPlacesNonceTag(guiActivityBrowseScript, ""))
	if bytes.Count(before, old) != 1 {
		t.Fatal("exact original browse script required")
	}
	expected := bytes.Replace(before, old, []byte(guiPlacesNonceTag(guiActivityBrowseScript, "synthetic-expected-browse-nonce")), 1)
	normalize := func(phase string, raw []byte) []byte {
		canonical, hard, err := f.normalizer.normalize(raw)
		if err != nil || len(hard) != 0 {
			t.Fatalf("released DOM normalizer refused browse presentation phase=%s error_type=%T hard_count=%d", phase, err, len(hard))
		}
		return canonical
	}
	originalDOM, expectedDOM, currentDOM := normalize("original", before), normalize("expected", expected), normalize("current", current)
	if !bytes.Equal(expectedDOM, currentDOM) {
		t.Fatal("browse canonical DOM differs outside one nonce attribute")
	}
	// Delivered nonce volatility owns equality; no extra masks/forced inequality.
	t.Logf("synthetic browse DOM case=%s filesystem=%t original_equal=%t original_sha256=%s expected_sha256=%s current_sha256=%s", fixture.name, filesystem, bytes.Equal(originalDOM, currentDOM), guiPublicHash(originalDOM), guiPublicHash(expectedDOM), guiPublicHash(currentDOM))
	return current, policy, expectedDOM
}

func TestGUIActivityBrowseNonceDOM(t *testing.T) {
	f := guiNewActivityBrowseFixture(t)
	for _, filesystem := range []bool{false, true} {
		for _, fixture := range guiActivityBrowseCases {
			t.Run(fmt.Sprintf("filesystem-%t/%s", filesystem, fixture.name), func(t *testing.T) {
				f.compare(t, filesystem, fixture)
			})
		}
	}
}

func TestGUIActivityBrowseNonceRejectsBindingMutations(t *testing.T) {
	f := guiNewActivityBrowseFixture(t)
	body, policy := f.render(t, false, false, guiActivityBrowseCases[2])
	nonce, err := guiActivityBrowseNonceBinding(string(body), policy, true, true)
	if err != nil {
		t.Fatal(err)
	}
	tag := guiPlacesNonceTag(guiActivityBrowseScript, nonce)
	for _, mutation := range []struct{ name, tag string }{
		{"missing-nonce", guiPlacesNonceTag(guiActivityBrowseScript, "")},
		{"wrong-nonce", guiPlacesNonceTag(guiActivityBrowseScript, "wrong-"+nonce)},
		{"duplicate", tag + "</script>" + tag},
		{"changed-path", strings.Replace(tag, "/static/js/browse-modes.js", "/static/js/synthetic-other.js", 1)},
		{"changed-defer", strings.Replace(tag, " defer>", " async>", 1)},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			changed := strings.Replace(string(body), tag, mutation.tag, 1)
			if changed == string(body) {
				t.Fatal("binding mutation target unchanged")
			}
			if _, err := guiActivityBrowseNonceBinding(changed, policy, true, true); err == nil {
				t.Fatal("native browse binding mutation accepted")
			}
		})
	}
	for _, name := range []string{"missing-header", "disagreeing-header", "script-order"} {
		t.Run(name, func(t *testing.T) {
			changed, changedPolicy := string(body), policy
			switch name {
			case "missing-header":
				changedPolicy = ""
			case "disagreeing-header":
				changedPolicy = strings.Replace(policy, "'nonce-"+nonce+"'", "'nonce-wrong-"+nonce+"'", 1)
			case "script-order":
				near := guiPlacesNonceTag(guiPlacesNonceScript{"/static/js/near-me.js", true}, nonce)
				changed = strings.Replace(changed, near, "BROWSE_ORDER_SENTINEL", 1)
				changed = strings.Replace(changed, tag, near, 1)
				changed = strings.Replace(changed, "BROWSE_ORDER_SENTINEL", tag, 1)
			}
			if changed == string(body) && changedPolicy == policy {
				t.Fatal("header/order control unchanged")
			}
			if _, err := guiActivityBrowseNonceBinding(changed, changedPolicy, true, true); err == nil {
				t.Fatal("native header/order control accepted")
			}
		})
	}
}

func TestGUIActivityBrowseNonceRejectsDOMMutations(t *testing.T) {
	f := guiNewActivityBrowseFixture(t)
	bodies, expected := map[int][]byte{}, map[int][]byte{}
	for _, index := range []int{1, 2, 4, 5} {
		bodies[index], _, expected[index] = f.compare(t, false, guiActivityBrowseCases[index])
	}
	for _, mutation := range []struct {
		name     string
		fixture  int
		from, to string
	}{
		{"GET-method", 2, `<form method="get" action="/activities/"`, `<form method="post" action="/activities/"`},
		{"query-value", 1, `name="q" value="synthetic"`, `name="q" value="other"`},
		{"beginners-hidden", 5, `name="beginners" value="true"`, `name="beginners" value="false"`},
		{"cards-hidden", 5, `name="view" value="cards"`, `name="view" value="list"`},
		{"selected-view", 5, `data-browse data-view="cards"`, `data-browse data-view="list"`},
		{"card-link", 2, `href="/activities/1000/">Synthetic activity 0</a>`, `href="/activities/1001/">Synthetic activity 0</a>`},
		{"card-title", 2, `>Synthetic activity 0</a>`, `>Synthetic changed title</a>`},
		{"previous-link", 5, `href="` + guiActivityBrowsePageHref(guiActivityBrowseCases[5], 1) + `">← Newer</a>`, `href="` + guiActivityBrowsePageHref(guiActivityBrowseCases[5], 3) + `">← Newer</a>`},
		{"next-link", 4, `href="` + guiActivityBrowsePageHref(guiActivityBrowseCases[4], 2) + `">More →</a>`, `href="` + guiActivityBrowsePageHref(guiActivityBrowseCases[4], 3) + `">More →</a>`},
		{"page-copy", 5, `Page 2 of 3`, `Page 2 of 4`},
		{"suggestion-link", 1, `<a href="?q=alternative">alternative</a>`, `<a href="?q=changed">alternative</a>`},
		{"empty-copy", 1, `No upcoming activities match your search.`, `Synthetic changed empty copy.`},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			body := bodies[mutation.fixture]
			if bytes.Count(body, []byte(mutation.from)) != 1 {
				t.Fatal("exact unique safe browse DOM mutation target required")
			}
			changed := bytes.Replace(body, []byte(mutation.from), []byte(mutation.to), 1)
			canonical, hard, err := f.normalizer.normalize(changed)
			if err != nil || len(hard) != 0 {
				t.Fatalf("safe browse mutation refused before equality error_type=%T hard_count=%d", err, len(hard))
			}
			if bytes.Equal(canonical, expected[mutation.fixture]) {
				t.Fatal("meaningful browse mutation accepted as nonce-only expected DOM")
			}
		})
	}
}
