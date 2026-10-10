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

const guiPlacesMapTemplate = "apps/web/templates/web/places.html"
const guiPlacesNearTemplate = "apps/web/templates/web/_near_me.html"

var guiPlacesNonceSourcePaths = []string{
	guiPlacesMapTemplate,
	guiPlacesNearTemplate,
	"apps/web/templates/web/places_list.html",
	"apps/web/templates/web/home.html",
	"apps/web/templates/web/activities.html",
	"apps/web/templates/web/_activity_card.html",
}

type guiPlacesNonceCase struct {
	name      string
	template  string
	uri       string
	populated bool
	near      bool
	query     string
}

var guiPlacesNonceCases = []guiPlacesNonceCase{
	{"map-empty", "web/places.html", "/places/", false, false, ""},
	{"map-categories", "web/places.html", "/places/", true, false, ""},
	{"list-empty", "web/places_list.html", "/places/list/", false, false, ""},
	{"list-populated", "web/places_list.html", "/places/list/", true, false, ""},
	{"list-near-active", "web/places_list.html", "/places/list/?city=Synthetic", true, true, ""},
	{"home-empty", "web/home.html", "/", false, false, ""},
	{"home-near-active", "web/home.html", "/", false, true, ""},
	{"activities-empty", "web/activities.html", "/activities/", false, false, ""},
	{"activities-near-active", "web/activities.html", "/activities/", false, true, ""},
	{"activities-search", "web/activities.html", "/activities/?q=synthetic", false, false, "synthetic"},
}

type guiPlacesNonceFixture struct {
	root       string
	current    map[string][]byte
	original   map[string][]byte
	base       *guiPublicFSSnapshot
	normalizer *guiPublicNormalizer
}

func guiNewPlacesNonceFixture(t *testing.T) *guiPlacesNonceFixture {
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
	for _, name := range guiPlacesNonceSourcePaths {
		current[name] = read(name)
		original[name] = append([]byte(nil), current[name]...)
	}
	for name, before := range map[string]struct {
		count  int
		bytes  int
		digest string
	}{
		guiPlacesMapTemplate:  {2, 2791, "7ccd6a47cfbc61a0a9eba1bb040ef25439cf663f6eed56add9eb5a303368388f"},
		guiPlacesNearTemplate: {1, 931, "33a062a0945e9c1c391606121de7575bcedb7f7267547f71451a69c0ebddc309"},
	} {
		const currentPrefix = `<script nonce="{{ request.csp_nonce }}" src=`
		if bytes.Count(current[name], []byte(currentPrefix)) != before.count {
			t.Fatal("exact declared external-script nonce additions required")
		}
		original[name] = bytes.ReplaceAll(current[name], []byte(currentPrefix), []byte(`<script src=`))
		if len(original[name]) != before.bytes || guiPublicHash(original[name]) != before.digest {
			t.Fatal("whole template inverse differs outside the three nonce attributes")
		}
	}
	base, err := guiPublicFilesystemSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	normalizer, err := guiPublicNewNormalizer(root)
	if err != nil || normalizer == nil {
		t.Fatal("existing actual released normalizer/archive/original bindings required")
	}
	checkpoint := read("docs/reviews/gui-public-original/checkpoint.json")
	if _, err := guiPublicReadReference(*guiPublicGoldenReference, checkpoint); err != nil {
		t.Fatal(err)
	}
	f := &guiPlacesNonceFixture{root, current, original, base, normalizer}
	t.Cleanup(func() {
		if err := base.check(); err != nil {
			t.Error(err)
		}
		for name, raw := range current {
			if !bytes.Equal(read(name), raw) {
				t.Error("actual places/shared-consumer source changed during presentation checks")
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

type guiPlacesNonceScript struct {
	path      string
	deferLoad bool
}

func guiPlacesNonceScripts(fixture guiPlacesNonceCase) []guiPlacesNonceScript {
	if fixture.template == "web/places.html" {
		return []guiPlacesNonceScript{{"/static/vendor/maplibre/maplibre-gl-csp.js", false}, {"/static/js/places-map.js", true}}
	}
	if fixture.template == "web/activities.html" && fixture.query != "" {
		return nil
	}
	return []guiPlacesNonceScript{{"/static/js/near-me.js", true}}
}

func guiPlacesNonceTag(script guiPlacesNonceScript, nonce string) string {
	tag := "<script"
	if nonce != "" {
		tag += ` nonce="` + nonce + `"`
	}
	tag += ` src="` + script.path + `"`
	if script.deferLoad {
		tag += " defer"
	}
	return tag + ">"
}

// This checker validates actual response binding/load attributes. Its mutation
// refusals are not golden.Normalize hard findings or browser CSP execution.
func guiPlacesNonceBinding(body, policy string, fixture guiPlacesNonceCase, current bool) (string, error) {
	base, err := guiPublicScriptNonceBinding(body, policy, nil)
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
		return "", fmt.Errorf("actual native base/header nonce differs")
	}
	selected := guiPlacesNonceScripts(fixture)
	previous := -1
	for _, script := range selected {
		value := ""
		if current {
			value = nonce
		}
		tag := guiPlacesNonceTag(script, value)
		position := strings.Index(body, tag)
		if strings.Count(body, tag) != 1 || strings.Count(body, `src="`+script.path+`"`) != 1 || position <= previous {
			return "", fmt.Errorf("places/shared script nonce/path/order/load attributes differ")
		}
		previous = position
	}
	for _, path := range []string{"/static/vendor/maplibre/maplibre-gl-csp.js", "/static/js/places-map.js", "/static/js/near-me.js"} {
		wanted := false
		for _, script := range selected {
			wanted = wanted || script.path == path
		}
		if !wanted && strings.Contains(body, `src="`+path+`"`) {
			return "", fmt.Errorf("script emitted outside the actual template branch")
		}
	}
	if fixture.template == "web/places.html" && strings.Count(body, `<script nonce="`+nonce+`" id="places-type-vocabulary" type="application/json">`) != 1 {
		return "", fmt.Errorf("existing native vocabulary JSON nonce changed")
	}
	return nonce, nil
}

func (f *guiPlacesNonceFixture) render(t *testing.T, filesystem, original bool, fixture guiPlacesNonceCase) ([]byte, string) {
	t.Helper()
	renderer := NewRenderer(f.root)
	if filesystem || original {
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
			if len(files) != 12 {
				t.Error("places caller-owned snapshot member set changed")
			}
			for name, raw := range selected {
				if files[name] == nil || files[name].Mode != 0o444 || !bytes.Equal(files[name].Data, raw) {
					t.Error("places caller-owned snapshot bytes/mode changed")
				}
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
	// Actual synthetic anonymous PRESENTATION only, including Home and activities
	// as shared-partial consumers. No route/actor admission, DB, real coordinates,
	// privacy/cohort decisions, map/API/geolocation or browser scripts execute.
	data := pongo2.Context{"near_active": fixture.near}
	switch fixture.template {
	case "web/places.html":
		categories, vocabulary := []any{}, []any{}
		if fixture.populated {
			categories = append(categories, map[string]any{"slug": "synthetic-category", "name": "Synthetic category"})
			vocabulary = append(vocabulary, map[string]any{"slug": "synthetic-type", "name": "Synthetic type", "aliases": []any{}, "category": "synthetic-category"})
		}
		data["categories"], data["type_vocabulary"] = categories, vocabulary
	case "web/places_list.html":
		places := []any{}
		if fixture.populated {
			places = append(places, map[string]any{"pk": 731, "label": "Synthetic public place", "address_city": "Synthetic city", "category_chips": []any{}})
		}
		data["places"] = places
		data["filters"] = map[string]any{"city": "", "activity": "", "source": ""}
		data["filtered"], data["truncated"] = fixture.near, false
	case "web/activities.html":
		page, _ := socialPagination("", 0, 24)
		data["page_obj"], data["activities"] = page, []any{}
		data["query"], data["view_mode"], data["base_qs"] = fixture.query, "list", ""
	case "web/home.html":
		for _, key := range []string{"guardian_invites", "group_updates", "starter_types", "recommended", "beginners", "mine", "upcoming", "events"} {
			data[key] = []any{}
		}
	}
	request := httptest.NewRequest("GET", "https://gui-fixture.invalid"+fixture.uri, nil)
	request.Header.Set("Accept-Language", "en")
	response := httptest.NewRecorder()
	if err := renderer.Render(response, request, fixture.template, data); err != nil {
		t.Fatal(err)
	}
	policy := response.Header().Get("Content-Security-Policy-Report-Only")
	if response.Code != 200 || response.Header().Get("Content-Type") != "text/html; charset=utf-8" || policy == "" || response.Header().Get("Content-Security-Policy") != "" {
		t.Fatal("existing native response/report-only CSP mode changed")
	}
	body := append([]byte(nil), response.Body.Bytes()...)
	if _, err := guiPlacesNonceBinding(string(body), policy, fixture, !original); err != nil {
		t.Fatal(err)
	}
	near := fixture.template != "web/places.html" && fixture.query == ""
	if (bytes.Count(body, []byte(`id="near-me-btn"`)) == 1) != near || bytes.Count(body, []byte("Sorted by distance from you.")) != map[bool]int{false: 0, true: 1}[near && fixture.near] {
		t.Fatal("actual opt-in/near-active presentation branch differs")
	}
	if near && !bytes.Contains(body, []byte("Your location is used only to sort this page")) {
		t.Fatal("unchanged opt-in location disclosure is absent")
	}
	switch fixture.template {
	case "web/places.html":
		if !bytes.Contains(body, []byte(`data-worker-url="/static/vendor/maplibre/maplibre-gl-csp-worker.js"`)) || !bytes.Contains(body, []byte(`<a href="/places/list/">View as a text list (no map needed) &rarr;</a>`)) || bytes.Count(body, []byte(`data-filter-value="synthetic-category"`)) != map[bool]int{false: 0, true: 1}[fixture.populated] {
			t.Fatal("existing map worker/text fallback/category arm differs")
		}
	case "web/places_list.html":
		if bytes.Count(body, []byte(`<a href="/places/731/">Synthetic public place</a>`)) != map[bool]int{false: 0, true: 1}[fixture.populated] || bytes.Count(body, []byte("No places match.")) != map[bool]int{false: 1, true: 0}[fixture.populated] {
			t.Fatal("actual empty/populated public text-list arm differs")
		}
	case "web/activities.html":
		if fixture.query != "" && (!bytes.Contains(body, []byte(`name="q" value="synthetic"`)) || !bytes.Contains(body, []byte("No upcoming activities match your search."))) {
			t.Fatal("actual search arm differs")
		}
	case "web/home.html":
		if !bytes.Contains(body, []byte("Upcoming for you")) || !bytes.Contains(body, []byte("Nothing upcoming yet.")) {
			t.Fatal("actual shared-partial Home presentation differs")
		}
	}
	return body, policy
}

func (f *guiPlacesNonceFixture) compare(t *testing.T, filesystem bool, fixture guiPlacesNonceCase) ([]byte, string, []byte) {
	t.Helper()
	before, _ := f.render(t, true, true, fixture)
	current, policy := f.render(t, filesystem, false, fixture)
	second, secondPolicy := f.render(t, filesystem, false, fixture)
	firstNonce, err := guiPlacesNonceBinding(string(current), policy, fixture, true)
	if err != nil {
		t.Fatal(err)
	}
	secondNonce, err := guiPlacesNonceBinding(string(second), secondPolicy, fixture, true)
	if err != nil || firstNonce == secondNonce {
		t.Fatal("actual native renderer reused or failed to bind a fresh response nonce")
	}
	expected := append([]byte(nil), before...)
	for _, script := range guiPlacesNonceScripts(fixture) {
		old := []byte(guiPlacesNonceTag(script, ""))
		if bytes.Count(before, old) != 1 {
			t.Fatal("declared pre-change external-script tag is not unique")
		}
		expected = bytes.Replace(expected, old, []byte(guiPlacesNonceTag(script, "synthetic-expected-places-nonce")), 1)
	}
	normalize := func(raw []byte) []byte {
		canonical, hard, err := f.normalizer.normalize(raw)
		if err != nil || len(hard) != 0 {
			t.Fatal("actual released DOM normalizer refused the presentation comparison")
		}
		return canonical
	}
	originalDOM, expectedDOM, currentDOM := normalize(before), normalize(expected), normalize(current)
	if !bytes.Equal(expectedDOM, currentDOM) {
		t.Fatal("canonical DOM differs outside the explicit nonce-attribute delta")
	}
	// The delivered API owns volatility. Canonical original/current equality is
	// allowed; the independent native binding checker above owns nonce refusals.
	t.Logf("synthetic places DOM case=%s filesystem=%t delta_attributes=%d original_equal=%t original_sha256=%s expected_sha256=%s current_sha256=%s", fixture.name, filesystem, len(guiPlacesNonceScripts(fixture)), bytes.Equal(originalDOM, currentDOM), guiPublicHash(originalDOM), guiPublicHash(expectedDOM), guiPublicHash(currentDOM))
	return current, policy, expectedDOM
}

func TestGUIPublicPlacesNonceDOM(t *testing.T) {
	f := guiNewPlacesNonceFixture(t)
	for _, filesystem := range []bool{false, true} {
		for _, fixture := range guiPlacesNonceCases {
			t.Run(fmt.Sprintf("filesystem-%t/%s", filesystem, fixture.name), func(t *testing.T) {
				f.compare(t, filesystem, fixture)
			})
		}
	}
}

func TestGUIPublicPlacesNonceRejectsBindingMutations(t *testing.T) {
	f := guiNewPlacesNonceFixture(t)
	mapCase, listCase := guiPlacesNonceCases[1], guiPlacesNonceCases[4]
	mapBody, mapPolicy := f.render(t, false, false, mapCase)
	listBody, listPolicy := f.render(t, false, false, listCase)
	for index, script := range append(guiPlacesNonceScripts(mapCase), guiPlacesNonceScripts(listCase)...) {
		body, policy, fixture := mapBody, mapPolicy, mapCase
		if index == 2 {
			body, policy, fixture = listBody, listPolicy, listCase
		}
		nonce, err := guiPlacesNonceBinding(string(body), policy, fixture, true)
		if err != nil {
			t.Fatal(err)
		}
		tag := guiPlacesNonceTag(script, nonce)
		for _, mutation := range []struct {
			name string
			tag  string
		}{
			{"missing-nonce", guiPlacesNonceTag(script, "")},
			{"wrong-nonce", guiPlacesNonceTag(script, "wrong-"+nonce)},
			{"duplicate", tag + "</script>" + tag},
			{"changed-path", strings.Replace(tag, script.path, "/static/js/synthetic-other.js", 1)},
			{"changed-defer", guiPlacesNonceTag(guiPlacesNonceScript{script.path, !script.deferLoad}, nonce)},
		} {
			t.Run(fmt.Sprintf("script-%d/%s", index, mutation.name), func(t *testing.T) {
				changed := strings.Replace(string(body), tag, mutation.tag, 1)
				if changed == string(body) {
					t.Fatal("native rendered binding mutation did not change its target")
				}
				if _, err := guiPlacesNonceBinding(changed, policy, fixture, true); err == nil {
					t.Fatal("rendered mutation passed the separate native binding checker")
				}
			})
		}
	}
	for _, name := range []string{"missing-header", "disagreeing-header", "map-script-order"} {
		t.Run(name, func(t *testing.T) {
			body, policy := string(mapBody), mapPolicy
			nonce, err := guiPlacesNonceBinding(body, policy, mapCase, true)
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "missing-header":
				policy = ""
			case "disagreeing-header":
				policy = strings.Replace(policy, "'nonce-"+nonce+"'", "'nonce-wrong-"+nonce+"'", 1)
			case "map-script-order":
				scripts := guiPlacesNonceScripts(mapCase)
				first, second := guiPlacesNonceTag(scripts[0], nonce), guiPlacesNonceTag(scripts[1], nonce)
				body = strings.Replace(body, first, "SOC7-ORDER-TEMP", 1)
				body = strings.Replace(body, second, first, 1)
				body = strings.Replace(body, "SOC7-ORDER-TEMP", second, 1)
			}
			if _, err := guiPlacesNonceBinding(body, policy, mapCase, true); err == nil {
				t.Fatal("native header/order mutation passed the binding checker")
			}
		})
	}
}

func TestGUIPublicPlacesNonceRejectsDOMMutations(t *testing.T) {
	f := guiNewPlacesNonceFixture(t)
	mapBody, _, mapExpected := f.compare(t, false, guiPlacesNonceCases[1])
	listBody, _, listExpected := f.compare(t, false, guiPlacesNonceCases[4])
	for _, mutation := range []struct {
		name     string
		body     []byte
		expected []byte
		from     string
		to       string
	}{
		{"map-worker-path", mapBody, mapExpected, `data-worker-url="/static/vendor/maplibre/maplibre-gl-csp-worker.js"`, `data-worker-url="/static/vendor/maplibre/synthetic-other.js"`},
		{"map-text-fallback-link", mapBody, mapExpected, `<a href="/places/list/">View as a text list (no map needed) &rarr;</a>`, `<a href="/synthetic-other/">View as a text list (no map needed) &rarr;</a>`},
		{"map-filter-value", mapBody, mapExpected, `data-filter-value="synthetic-category"`, `data-filter-value="synthetic-other"`},
		{"list-near-copy", listBody, listExpected, `Sorted by distance from you.`, `Synthetic changed disclosure.`},
		{"list-place-link", listBody, listExpected, `<a href="/places/731/">Synthetic public place</a>`, `<a href="/places/732/">Synthetic public place</a>`},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			if bytes.Count(mutation.body, []byte(mutation.from)) != 1 {
				t.Fatal("actual safe DOM mutation target is not unique")
			}
			changed := bytes.Replace(mutation.body, []byte(mutation.from), []byte(mutation.to), 1)
			canonical, hard, err := f.normalizer.normalize(changed)
			if err != nil || len(hard) != 0 || bytes.Equal(canonical, mutation.expected) {
				t.Fatal("actual safe DOM mutation was not independently distinguished")
			}
		})
	}
}
