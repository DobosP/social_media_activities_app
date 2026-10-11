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

	"github.com/DobosP/social_media_activities_app/services/server/internal/avatars"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

var guiActivityFormScripts = []guiPlacesNonceScript{
	{"/static/vendor/leaflet/leaflet.js", false},
	{"/static/js/place-picker.js", true},
	{"/static/js/concept-combobox.js", true},
	{"/static/js/form-wizard.js", true},
}

type guiActivityFormCase struct {
	name    string
	edit    bool
	invalid bool
}

var guiActivityFormCases = []guiActivityFormCase{
	{"create-filled", false, false},
	{"create-field-error", false, true},
	{"edit-filled", true, false},
	{"edit-field-error", true, true},
}

type guiActivityFormFixture struct {
	root         string
	originalRoot string
	current      map[string][]byte
	original     map[string][]byte
	base         *guiPublicFSSnapshot
	normalizer   *guiPublicNormalizer
}

func guiNewActivityFormFixture(t *testing.T) *guiActivityFormFixture {
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
	for name, expected := range map[string]struct {
		bytes  int
		digest string
	}{
		"activity_form": {2214, "d003a6b0f710ebef674389fe0dbbeecf9ad0bf891d48166e25bbdd588dd6133e"},
		"activity_edit": {2096, "67badb4b71e28c1bed74ca09ca9817e4fa0133bfbf3c5f0b3b67867bbf4d24ec"},
	} {
		path := "apps/web/templates/web/" + name + ".html"
		current[path] = read(path)
		added := []byte(`<script nonce="{{ request.csp_nonce }}" src=`)
		if bytes.Count(current[path], added) != 4 {
			t.Fatal("exact four nonce attributes per activity template required")
		}
		original[path] = bytes.ReplaceAll(current[path], added, []byte("<script src="))
		if len(original[path]) != expected.bytes || guiPublicHash(original[path]) != expected.digest {
			t.Fatal("whole activity template inverse differs beyond eight nonce attributes")
		}
	}
	const wizard = "apps/web/templates/web/_activity_form_wizard.html"
	current[wizard] = read(wizard)
	original[wizard] = append([]byte(nil), current[wizard]...)
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
	f := &guiActivityFormFixture{root, originalRoot, current, original, base, normalizer}
	t.Cleanup(func() {
		if err := base.check(); err != nil {
			t.Error(err)
		}
		for name, raw := range current {
			if !bytes.Equal(read(name), raw) {
				t.Error("actual activity source changed during presentation checks")
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

// Separate native binding checks own nonce/path/order/load refusals. These are
// not golden.Normalize hard findings or browser CSP/JavaScript execution.
func guiActivityFormNonceBinding(body, policy string, current bool) (string, error) {
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
		return "", fmt.Errorf("actual activity/base header nonce differs")
	}
	previous := -1
	for _, script := range guiActivityFormScripts {
		value := ""
		if current {
			value = nonce
		}
		tag := guiPlacesNonceTag(script, value)
		position := strings.Index(body, tag)
		if strings.Count(body, tag) != 1 || strings.Count(body, `src="`+script.path+`"`) != 1 || position <= previous {
			return "", fmt.Errorf("activity script nonce/path/order/blocking/defer binding differs")
		}
		previous = position
	}
	if strings.Count(body, `<script nonce="`+nonce+`" id="activity-type-vocabulary" type="application/json">`) != 1 {
		return "", fmt.Errorf("existing native activity vocabulary nonce differs")
	}
	return nonce, nil
}

func (f *guiActivityFormFixture) render(t *testing.T, filesystem, original bool, fixture guiActivityFormCase) ([]byte, string) {
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
				t.Error("activity FS source member set changed")
			}
			for name, raw := range selected {
				if files[name] == nil || files[name].Mode != 0o444 || !bytes.Equal(files[name].Data, raw) {
					t.Error("activity FS source bytes/mode changed")
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
	// Explicit fictional authenticated adult PRESENTATION. No socialFormPage,
	// DB, POST, participation/ownership/organizer/admission decision, real user,
	// geolocation, map/API or browser JavaScript runs. These maps match the
	// existing four-row form.steps and bound widget contract, not a backend proof.
	actor := platform.Actor{ID: 9001, PublicID: "00000000-0000-4000-8000-000000009001", Username: "gui_fixture_adult", DisplayName: "GUI fixture", AgeBand: "adult", Cohort: "adult", IsActive: true}
	uri, family := "/activities/new/", "activity_form"
	if fixture.edit {
		uri, family = "/activities/731/edit/", "activity_edit"
	}
	request := platform.WithActor(httptest.NewRequest("GET", "https://gui-fixture.invalid"+uri, nil), actor)
	request.Header.Set("Accept-Language", "en")
	user := socialActor(actor)
	user["avatar_uri"] = avatars.DataURI(avatars.RenderGeneration(avatars.DefaultGeneration, avatars.SignatureSeed(actor.Username, avatars.DefaultGeneration, 0), nil, nil, avatars.Options{PX: 80}))
	form := map[string]any{}
	field := func(name, label, widget, help string) map[string]any {
		value := map[string]any{"name": name, "label_tag": pongo2.AsSafeValue(`<label for="id_` + name + `">` + label + `</label>`), "widget": pongo2.AsSafeValue(widget), "errors": "", "help_text": help}
		form[name] = value
		return value
	}
	title := field("title", "Title", `<input type="text" id="id_title" name="title" value="Synthetic activity">`, "")
	if fixture.invalid {
		title["errors"] = "Synthetic title validation notice."
	}
	typeField := field("activity_type", "Activity type", `<select id="id_activity_type" name="activity_type"><option value="21" selected>Synthetic type</option></select>`, "")
	description := field("description", "Description", `<textarea id="id_description" name="description">Synthetic description.</textarea>`, "")
	place := field("place", "Place", `<select id="id_place" name="place"><option value="31" selected>Synthetic public place</option></select>`, "Synthetic place help.")
	starts := field("starts_at", "Starts at", `<input type="datetime-local" id="id_starts_at" name="starts_at" value="2030-01-02T12:00">`, "")
	capacity := field("capacity", "Capacity", `<input type="number" id="id_capacity" name="capacity" value="8">`, "Blank = unlimited.")
	firstTime := field("first_time_note", "First time note", `<textarea id="id_first_time_note" name="first_time_note">Synthetic welcome.</textarea>`, "")
	beginners := field("beginners_welcome", "Beginners welcome", `<input type="checkbox" id="id_beginners_welcome" name="beginners_welcome" value="on" checked>`, "Synthetic first-timer help.")
	form["steps"] = []any{
		[]any{"what", "Ce faceți", []any{typeField, title, description}},
		[]any{"where", "Unde", []any{place}},
		[]any{"when", "Când și cât", []any{starts, capacity}},
		[]any{"details", "Detalii", []any{firstTime, beginners}},
	}
	// Reuse only this existing pure bound-widget repair (GET, no DB) so the
	// edit primary type has the same disabled/selected presentation as source.
	socialBoundFormRepair(request, form, map[string]any{"activity_type": "21"}, fixture.edit)
	data := pongo2.Context{"user": user, "csrf": "synthetic-activity-csrf", "form": form, "type_vocabulary": []any{map[string]any{"id": 21, "slug": "synthetic-type", "name": "Synthetic type", "category": "Synthetic category", "aliases": []any{}}}}
	if fixture.edit {
		data["activity"] = map[string]any{"pk": 731, "title": "Synthetic activity", "activity_type": map[string]any{"name": "Synthetic type"}}
	}
	response := httptest.NewRecorder()
	if err := renderer.Render(response, request, "web/"+family+".html", data); err != nil {
		t.Fatal(err)
	}
	body := append([]byte(nil), response.Body.Bytes()...)
	policy := response.Header().Get("Content-Security-Policy-Report-Only")
	if response.Code != 200 || response.Header().Get("Content-Type") != "text/html; charset=utf-8" || policy == "" || response.Header().Get("Content-Security-Policy") != "" {
		t.Fatal("existing native response/report-only policy changed")
	}
	if _, err := guiActivityFormNonceBinding(string(body), policy, !original); err != nil {
		t.Fatal(err)
	}
	startTag := []byte(`<form method="post" class="stack activity-wizard" data-wizard>`)
	if bytes.Count(body, startTag) != 1 {
		t.Fatal("original implicit-action activity POST form required")
	}
	start := bytes.Index(body, startTag)
	end := bytes.Index(body[start:], []byte("</form>"))
	if end < 0 {
		t.Fatal("actual activity form end missing")
	}
	content := body[start : start+end+len("</form>")]
	if bytes.Count(content, []byte(`name="csrfmiddlewaretoken" value="synthetic-activity-csrf"`)) != 1 || bytes.Count(content, []byte(`class="form-section wizard-panel"`)) != 4 || bytes.Count(content, []byte(`class="wizard-step"`)) != 4 {
		t.Fatal("existing CSRF/four-step wizard structure differs")
	}
	previous := -1
	for _, key := range []string{"what", "where", "when", "details"} {
		position := bytes.Index(content, []byte(`data-step-key="`+key+`"`))
		if position <= previous {
			t.Fatal("original four-row step order differs")
		}
		previous = position
	}
	for _, name := range []string{"activity_type", "title", "description", "place", "starts_at", "capacity", "first_time_note", "beginners_welcome"} {
		if bytes.Count(content, []byte(`name="`+name+`"`)) != 1 {
			t.Fatal("synthetic native bound-field shape differs")
		}
	}
	if bytes.Count(content, []byte("Synthetic title validation notice.")) != map[bool]int{false: 0, true: 1}[fixture.invalid] || !bytes.Contains(content, []byte("Synthetic place help.")) || !bytes.Contains(body, []byte(`data-places-url="/api/places/"`)) || !bytes.Contains(body, []byte("Your location is used only to sort the map &mdash; never stored.")) {
		t.Fatal("existing field-error/help/map-disclosure presentation differs")
	}
	if fixture.edit {
		if !bytes.Contains(content, []byte(`<select disabled id="id_activity_type"`)) || !bytes.Contains(content, []byte(`href="/activities/731/">Cancel</a>`)) || !bytes.Contains(content, []byte(`>Save changes</button>`)) || bytes.Contains(content, []byte("?return=organize")) || !bytes.Contains(body, []byte("Changing the venue or start time re-notifies everyone who joined.")) {
			t.Fatal("existing edit fixed-type/cancel/submit/disclosure arm differs")
		}
	} else if bytes.Contains(content, []byte("<select disabled")) || !bytes.Contains(content, []byte(`href="/places/propose/?return=organize"`)) || !bytes.Contains(content, []byte(`>Create activity</button>`)) || !bytes.Contains(body, []byte("It will be visible to people your age only.")) || !bytes.Contains(body, []byte(`href="/activities/series/new/"`)) {
		t.Fatal("existing create progressive-enhancement/series/disclosure arm differs")
	}
	return body, policy
}

func (f *guiActivityFormFixture) compare(t *testing.T, filesystem bool, fixture guiActivityFormCase) ([]byte, string, []byte) {
	t.Helper()
	before, _ := f.render(t, filesystem, true, fixture)
	current, policy := f.render(t, filesystem, false, fixture)
	second, secondPolicy := f.render(t, filesystem, false, fixture)
	firstNonce, err := guiActivityFormNonceBinding(string(current), policy, true)
	if err != nil {
		t.Fatal(err)
	}
	secondNonce, err := guiActivityFormNonceBinding(string(second), secondPolicy, true)
	if err != nil || firstNonce == secondNonce {
		t.Fatal("actual native renderer reused or failed to bind a fresh nonce")
	}
	expected := append([]byte(nil), before...)
	for _, script := range guiActivityFormScripts {
		old := []byte(guiPlacesNonceTag(script, ""))
		if bytes.Count(before, old) != 1 {
			t.Fatal("declared original external-script tag must be unique")
		}
		expected = bytes.Replace(expected, old, []byte(guiPlacesNonceTag(script, "synthetic-expected-activity-nonce")), 1)
	}
	normalize := func(phase string, raw []byte) []byte {
		canonical, hard, err := f.normalizer.normalize(raw)
		if err != nil || len(hard) != 0 {
			// No raw error/HTML/finding values: the phase and bounded count alone
			// are presentation diagnostics, never acceptance or a policy bypass.
			t.Fatalf("released DOM normalizer refused activity presentation phase=%s error_type=%T hard_count=%d", phase, err, len(hard))
		}
		return canonical
	}
	originalDOM := normalize("original", before)
	expectedDOM := normalize("expected", expected)
	currentDOM := normalize("current", current)
	if !bytes.Equal(expectedDOM, currentDOM) {
		t.Fatal("canonical DOM differs outside the explicit four-attribute nonce delta")
	}
	// The delivered API owns volatility: nonce attributes may normalize away.
	// Native header/tag checks independently own nonce binding and freshness.
	t.Logf("synthetic activity DOM case=%s filesystem=%t delta_attributes=4 original_equal=%t original_sha256=%s expected_sha256=%s current_sha256=%s", fixture.name, filesystem, bytes.Equal(originalDOM, currentDOM), guiPublicHash(originalDOM), guiPublicHash(expectedDOM), guiPublicHash(currentDOM))
	return current, policy, expectedDOM
}

func TestGUIActivityFormNonceDOM(t *testing.T) {
	f := guiNewActivityFormFixture(t)
	for _, filesystem := range []bool{false, true} {
		for _, fixture := range guiActivityFormCases {
			t.Run(fmt.Sprintf("filesystem-%t/%s", filesystem, fixture.name), func(t *testing.T) {
				f.compare(t, filesystem, fixture)
			})
		}
	}
}

func TestGUIActivityFormNonceRejectsBindingMutations(t *testing.T) {
	f := guiNewActivityFormFixture(t)
	body, policy := f.render(t, false, false, guiActivityFormCases[0])
	nonce, err := guiActivityFormNonceBinding(string(body), policy, true)
	if err != nil {
		t.Fatal(err)
	}
	for index, script := range guiActivityFormScripts {
		tag := guiPlacesNonceTag(script, nonce)
		for _, mutation := range []struct {
			name string
			tag  string
		}{
			{"missing-nonce", guiPlacesNonceTag(script, "")},
			{"wrong-nonce", guiPlacesNonceTag(script, "wrong-"+nonce)},
			{"duplicate", tag + "</script>" + tag},
			{"changed-path", strings.Replace(tag, script.path, "/static/js/synthetic-other.js", 1)},
			{"changed-load", guiPlacesNonceTag(guiPlacesNonceScript{script.path, !script.deferLoad}, nonce)},
		} {
			t.Run(fmt.Sprintf("script-%d/%s", index, mutation.name), func(t *testing.T) {
				changed := strings.Replace(string(body), tag, mutation.tag, 1)
				if changed == string(body) {
					t.Fatal("binding mutation did not change its actual target")
				}
				if _, err := guiActivityFormNonceBinding(changed, policy, true); err == nil {
					t.Fatal("rendered mutation passed separate native nonce/path/load checks")
				}
			})
		}
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
				first, last := guiPlacesNonceTag(guiActivityFormScripts[0], nonce), guiPlacesNonceTag(guiActivityFormScripts[3], nonce)
				changed = strings.Replace(changed, first, "ACTIVITY_ORDER_SENTINEL", 1)
				changed = strings.Replace(changed, last, first, 1)
				changed = strings.Replace(changed, "ACTIVITY_ORDER_SENTINEL", last, 1)
			}
			if changed == string(body) && changedPolicy == policy {
				t.Fatal("binding negative control did not change its actual target")
			}
			if _, err := guiActivityFormNonceBinding(changed, changedPolicy, true); err == nil {
				t.Fatal("header/order negative control passed native binding checks")
			}
		})
	}
}

func TestGUIActivityFormNonceRejectsDOMMutations(t *testing.T) {
	f := guiNewActivityFormFixture(t)
	create, _, createDOM := f.compare(t, false, guiActivityFormCases[0])
	edit, _, editDOM := f.compare(t, false, guiActivityFormCases[3])
	for _, mutation := range []struct {
		name string
		edit bool
		from string
		to   string
	}{
		{"form-method", false, `<form method="post" class="stack activity-wizard"`, `<form method="get" class="stack activity-wizard"`},
		{"csrf-field-name", false, `name="csrfmiddlewaretoken" value="synthetic-activity-csrf"`, `name="synthetic-csrf-other" value="synthetic-activity-csrf"`},
		{"title-field", false, `name="title" value="Synthetic activity"`, `name="synthetic_title" value="Synthetic activity"`},
		{"selected-type", false, `<option value="21" selected>Synthetic type</option>`, `<option value="22" selected>Synthetic type</option>`},
		{"create-submit", false, `>Create activity</button>`, `>Synthetic other action</button>`},
		{"create-propose-link", false, `href="/places/propose/?return=organize"`, `href="/places/"`},
		{"edit-cancel-link", true, `href="/activities/731/">Cancel</a>`, `href="/activities/732/">Cancel</a>`},
		{"edit-fixed-type", true, `<select disabled id="id_activity_type"`, `<select id="id_activity_type"`},
		{"step-key", false, `data-step-key="where"`, `data-step-key="synthetic-other"`},
		{"error-copy", true, `Synthetic title validation notice.`, `Synthetic changed validation notice.`},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			body, expectedDOM := create, createDOM
			if mutation.edit {
				body, expectedDOM = edit, editDOM
			}
			count := bytes.Count(body, []byte(mutation.from))
			// The CSRF token exists in both content and the unchanged base logout
			// form. Select the content form so the mutation targets this family.
			if mutation.name == "csrf-field-name" {
				start := bytes.Index(body, []byte(`<form method="post" class="stack activity-wizard"`))
				if start < 0 || bytes.Count(body[start:], []byte(mutation.from)) != 1 {
					t.Fatal("exact activity CSRF mutation target required")
				}
				body = append(append([]byte(nil), body[:start]...), bytes.Replace(body[start:], []byte(mutation.from), []byte(mutation.to), 1)...)
			} else {
				if count != 1 {
					t.Fatal("exact unique activity DOM mutation target required")
				}
				body = bytes.Replace(body, []byte(mutation.from), []byte(mutation.to), 1)
			}
			canonical, hard, err := f.normalizer.normalize(body)
			if err != nil || len(hard) != 0 {
				t.Fatalf("safe DOM mutation refused before equality comparison error_type=%T hard_count=%d", err, len(hard))
			}
			if bytes.Equal(canonical, expectedDOM) {
				t.Fatal("meaningful form mutation accepted as the expected nonce-only DOM")
			}
		})
	}
}
