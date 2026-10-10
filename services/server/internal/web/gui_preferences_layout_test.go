package web

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

const guiPreferenceCSS = `
/* Fixed preference-form layout; field/policy behavior is unchanged. */
.row.preference-choice { gap: .5rem; align-items: flex-start; margin: .4rem 0; }
.row.preference-choice--topic { margin: .35rem 0; }
.card > .preference-submit { margin-top: .5rem; }
fieldset.preference-topic-fields { border: 0; padding: 0; margin: 0; }
legend.preference-topic-title { margin-bottom: .4rem; }
.preference-feed-link { margin-left: .6rem; }
`

type guiPreferenceDelta struct {
	before string
	after  string
	count  int
}

var guiPreferenceDeltas = map[string][]guiPreferenceDelta{
	"access_preferences": {
		{`<label class="row" style="gap:.5rem;align-items:flex-start;margin:.4rem 0">`, `<label class="row preference-choice">`, 4},
		{`<button class="btn" type="submit" style="margin-top:.5rem">`, `<button class="btn preference-submit" type="submit">`, 1},
	},
	"notification_preferences": {
		{`<label class="row" style="gap:.5rem;align-items:flex-start;margin:.4rem 0">`, `<label class="row preference-choice">`, 1},
		{`<button class="btn" type="submit" style="margin-top:.5rem">`, `<button class="btn preference-submit" type="submit">`, 1},
	},
	"topic_preferences": {
		{`<fieldset style="border:0;padding:0;margin:0">`, `<fieldset class="preference-topic-fields">`, 1},
		{`<legend class="muted" style="margin-bottom:.4rem">`, `<legend class="muted preference-topic-title">`, 1},
		{`<label class="row" style="gap:.5rem;align-items:flex-start;margin:.35rem 0">`, `<label class="row preference-choice preference-choice--topic">`, 1},
		{`<button class="btn" type="submit" style="margin-top:.5rem">`, `<button class="btn preference-submit" type="submit">`, 1},
		{`<a class="muted" href="{% url 'home' %}" style="margin-left:.6rem">`, `<a class="muted preference-feed-link" href="{% url 'home' %}">`, 1},
	},
}

type guiPreferenceCase struct {
	name    string
	family  string
	variant string
	rows    int
}

var guiPreferenceCases = []guiPreferenceCase{
	{"access-none", "access_preferences", "none", 4},
	{"access-all", "access_preferences", "all", 4},
	{"access-mixed", "access_preferences", "mixed", 4},
	{"notifications-empty", "notification_preferences", "empty", 0},
	{"notifications-mixed", "notification_preferences", "mixed", 2},
	{"notifications-arrival", "notification_preferences", "arrival", 1},
	{"topics-empty", "topic_preferences", "empty", 0},
	{"topics-none", "topic_preferences", "none", 2},
	{"topics-selected", "topic_preferences", "selected", 2},
}

type guiPreferenceFixture struct {
	root         string
	originalRoot string
	current      map[string][]byte
	original     map[string][]byte
	base         *guiPublicFSSnapshot
	normalizer   *guiPublicNormalizer
}

func guiNewPreferenceFixture(t *testing.T) *guiPreferenceFixture {
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
	for family, expected := range map[string]struct {
		bytes  int
		digest string
	}{
		"access_preferences":       {1791, "0e13f8fcbf60a90c830ac185eb74cb95129c3a3238c1d814f745afabd1feb055"},
		"notification_preferences": {1329, "47a67e596420d28829ea44e68c3c61b3b163e95d5affd91302e7ee1e1e443529"},
		"topic_preferences":        {1551, "11447297015e28c38b94ff6662ca64d14b15e01feff91c5c9222cba873c2ef2c"},
	} {
		name := "apps/web/templates/web/" + family + ".html"
		current[name] = read(name)
		original[name] = append([]byte(nil), current[name]...)
		for _, delta := range guiPreferenceDeltas[family] {
			if bytes.Count(current[name], []byte(delta.after)) != delta.count {
				t.Fatal("exact declared preference source class targets required")
			}
			original[name] = bytes.ReplaceAll(original[name], []byte(delta.after), []byte(delta.before))
		}
		if bytes.Contains(current[name], []byte("style=")) || len(original[name]) != expected.bytes || guiPublicHash(original[name]) != expected.digest {
			t.Fatal("whole preference template inverse differs outside twelve attributes")
		}
	}
	css := read("static/css/base.css")
	if !bytes.HasSuffix(css, []byte(guiPreferenceCSS)) || bytes.Count(css, []byte(guiPreferenceCSS)) != 1 {
		t.Fatal("exact appended scoped preference CSS required")
	}
	oldCSS := css[:len(css)-len(guiPreferenceCSS)]
	if len(oldCSS) != 62453 || guiPublicHash(oldCSS) != "41f0f6a04b14da0874000ed7c9010f5dd75e2040a6f09082607ba12cf890fa00" {
		t.Fatal("CSS append inverse differs from the unchanged original stylesheet")
	}
	// Static cascade reasoning only: new row/submit020 selectors follow the
	// existing card:first-child020 rule, retaining .4/.35/.5rem even for an
	// empty notification list. Fieldset/legend rules do not alter font/padding
	// beyond the original inline properties. No computed-style claim is made.
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
	// Nine small source/catalog files only, never the original14 response corpus
	// or a workspace clone. The original OS renderer reads actual regular files
	// through its unchanged constructor/loader, with exact sealed preimage bytes.
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
	f := &guiPreferenceFixture{root, originalRoot, current, original, base, normalizer}
	t.Cleanup(func() {
		if err := base.check(); err != nil {
			t.Error(err)
		}
		for name, raw := range current {
			if !bytes.Equal(read(name), raw) {
				t.Error("actual preference source changed during presentation checks")
			}
		}
		for name, raw := range oldFiles {
			file := filepath.Join(originalRoot, name)
			info, err := os.Lstat(file)
			actual, readErr := os.ReadFile(file)
			if err != nil || readErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o444 || !bytes.Equal(actual, raw) {
				t.Error("private original OS source snapshot bytes/mode changed")
			}
		}
		if !bytes.Equal(read("static/css/base.css"), css) {
			t.Error("actual stylesheet changed during presentation checks")
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

func guiPreferenceForm(t *testing.T, body []byte) []byte {
	t.Helper()
	startTag := []byte(`<form method="post" class="card">`)
	if bytes.Count(body, startTag) != 1 {
		t.Fatal("exact original implicit-action POST form required")
	}
	start := bytes.Index(body, startTag)
	end := bytes.Index(body[start:], []byte("</form>"))
	if end < 0 {
		t.Fatal("actual preference form end missing")
	}
	return body[start : start+end+len("</form>")]
}

func (f *guiPreferenceFixture) render(t *testing.T, filesystem, original bool, fixture guiPreferenceCase) []byte {
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
				t.Error("preference FS source member set changed")
			}
			for name, raw := range selected {
				if files[name] == nil || files[name].Mode != 0o444 || !bytes.Equal(files[name].Data, raw) {
					t.Error("preference FS source bytes/mode changed")
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
	// Fictional authenticated adult PRESENTATION only. No handler, session,
	// accounts API, preference write, eligibility, PG, guardian or child data.
	actor := platform.Actor{ID: 9001, PublicID: "00000000-0000-4000-8000-000000009001", Username: "gui_fixture_adult", DisplayName: "GUI fixture", AgeBand: "adult", Cohort: "adult", IsActive: true}
	request := platform.WithActor(httptest.NewRequest("GET", "https://gui-fixture.invalid/synthetic-preferences/", nil), actor)
	request.Header.Set("Accept-Language", "en")
	data := pongo2.Context{"user": socialActor(actor), "csrf": "synthetic-preference-csrf"}
	switch fixture.family {
	case "access_preferences":
		data["pref"] = map[string]bool{"needs_step_free": fixture.variant != "none", "needs_accessible_toilet": fixture.variant == "all", "needs_hearing_loop": fixture.variant == "all", "prefers_quiet": fixture.variant != "none"}
	case "notification_preferences":
		rows := []map[string]any{}
		for _, kind := range map[string][]string{"empty": {}, "mixed": {"join_requested", "announcement"}, "arrival": {"arrival"}}[fixture.variant] {
			rows = append(rows, map[string]any{"value": kind, "label": accountNotificationLabels[kind], "reason": accountNotificationReasons[kind], "muted": kind != "join_requested"})
		}
		data["rows"] = rows
	case "topic_preferences":
		categories, chosen := []map[string]any{}, map[string]bool{}
		if fixture.variant != "empty" {
			categories = append(categories, map[string]any{"slug": "synthetic-a", "name": "Synthetic topic A", "description": "Synthetic topic detail."}, map[string]any{"slug": "synthetic-b", "name": "Synthetic topic B", "description": ""})
		}
		if fixture.variant == "selected" {
			chosen["synthetic-a"] = true
		}
		data["categories"], data["chosen"] = categories, chosen
	}
	response := httptest.NewRecorder()
	if err := renderer.Render(response, request, "web/"+fixture.family+".html", data); err != nil {
		t.Fatal(err)
	}
	body := append([]byte(nil), response.Body.Bytes()...)
	if response.Code != 200 || response.Header().Get("Content-Type") != "text/html; charset=utf-8" || response.Header().Get("Content-Security-Policy") != "" {
		t.Fatal("existing native response/report-only policy changed")
	}
	if _, err := guiPublicScriptNonceBinding(string(body), response.Header().Get("Content-Security-Policy-Report-Only"), &actor.PublicID); err != nil {
		t.Fatal(err)
	}
	form := guiPreferenceForm(t, body)
	if bytes.Count(form, []byte(`<input type="hidden" name="csrfmiddlewaretoken" value="synthetic-preference-csrf">`)) != 1 || bytes.Count(form, []byte(`type="checkbox"`)) != fixture.rows {
		t.Fatal("original CSRF/checkbox form structure differs")
	}
	checked := func(name, value string, want bool) {
		tag := `<input type="checkbox" name="` + name + `"`
		if value != "" {
			tag += ` value="` + value + `"`
		}
		if want {
			tag += " checked"
		}
		if bytes.Count(form, []byte(tag+">")) != 1 {
			t.Fatal("exact original checkbox identity/checked state differs")
		}
	}
	switch fixture.family {
	case "access_preferences":
		checked("needs_step_free", "", fixture.variant != "none")
		checked("needs_accessible_toilet", "", fixture.variant == "all")
		checked("needs_hearing_loop", "", fixture.variant == "all")
		checked("prefers_quiet", "", fixture.variant != "none")
		if !bytes.Contains(body, []byte("places with unknown accessibility are still shown to you.")) || !bytes.Contains(body, []byte("change what you see.")) {
			t.Fatal("existing access/unknown-data disclosure changed")
		}
	case "notification_preferences":
		if fixture.variant == "mixed" {
			checked("muted", "join_requested", false)
			checked("muted", "announcement", true)
		} else if fixture.variant == "arrival" {
			checked("muted", "arrival", true)
		}
		if !bytes.Contains(body, []byte("Important safety and moderation notices are always delivered")) || bytes.Count(body, []byte("alerts for a child you supervise.")) != map[bool]int{false: 0, true: 1}[fixture.variant == "arrival"] {
			t.Fatal("existing mandatory-safety/arrival disclosure changed")
		}
	case "topic_preferences":
		if fixture.rows != 0 {
			checked("topics", "synthetic-a", fixture.variant == "selected")
			checked("topics", "synthetic-b", false)
		}
		if bytes.Count(body, []byte("No topics are available yet.")) != map[bool]int{false: 0, true: 1}[fixture.rows == 0] || bytes.Count(form, []byte(">see your feed</a>")) != map[bool]int{false: 0, true: 1}[fixture.variant == "selected"] || !bytes.Contains(body, []byte("It never hides anything:")) {
			t.Fatal("existing empty/selected-topic/visibility copy changed")
		}
	}
	return body
}

func (f *guiPreferenceFixture) compare(t *testing.T, filesystem bool, fixture guiPreferenceCase) ([]byte, []byte) {
	t.Helper()
	before := f.render(t, filesystem, true, fixture)
	current := f.render(t, filesystem, false, fixture)
	expected := append([]byte(nil), before...)
	counts := []int{fixture.rows, 1}
	if fixture.family == "topic_preferences" {
		counts = []int{1, 1, fixture.rows, 1, map[bool]int{false: 0, true: 1}[fixture.variant == "selected"]}
	}
	for index, delta := range guiPreferenceDeltas[fixture.family] {
		old := []byte(strings.ReplaceAll(delta.before, "{% url 'home' %}", "/"))
		next := []byte(strings.ReplaceAll(delta.after, "{% url 'home' %}", "/"))
		if bytes.Count(before, old) != counts[index] || bytes.Count(current, next) != counts[index] {
			t.Fatal("actual declared style/class target count differs")
		}
		expected = bytes.Replace(expected, old, next, counts[index])
	}
	normalize := func(phase string, raw []byte) []byte {
		canonical, hard, err := f.normalizer.normalize(raw)
		if err != nil || len(hard) != 0 {
			wrapperCode := "none"
			if err != nil {
				// Compare in memory against source-bound wrapper constants only;
				// never emit the error message or any captured child stream.
				wrapperCode = "unknown-wrapper"
				switch message := err.Error(); {
				case message == "released normalizer returned no complete diagnostic frame":
					wrapperCode = "frame-missing"
				case message == "released normalizer diagnostic frame differs":
					wrapperCode = "frame-invalid"
				case strings.HasPrefix(message, "released normalizer refused; hard_findings="):
					wrapperCode = "child-refused"
				}
			}
			safePath := regexp.MustCompile(`^(/[a-z][a-z0-9-]*\[[0-9]+\])+(@[a-z][a-z0-9-]*)?$`)
			codes, paths := []string{}, []string{}
			for index, finding := range hard {
				if index == 8 {
					break // Full count remains visible; metadata is bounded.
				}
				code, path := "unknown-hard", "[withheld]"
				for _, kind := range []struct {
					prefix string
					code   string
					path   bool
				}{
					{"empty URL at ", "empty-url", true},
					{"failed URL sanitization at ", "failed-url-sanitization", true},
					{"template syntax leak at ", "template-syntax-leak", true},
					{"invalid JSON script at ", "invalid-json-script", true},
					{"HTML parse: ", "html-parse", false},
					{"oracle HTML parse: ", "oracle-html-parse", false},
				} {
					if strings.HasPrefix(finding, kind.prefix) {
						code = kind.code
						candidate := strings.TrimPrefix(finding, kind.prefix)
						if kind.path && len(candidate) <= 256 && safePath.MatchString(candidate) {
							path = candidate
						}
						break
					}
				}
				codes, paths = append(codes, code), append(paths, path)
			}
			t.Fatalf("actual released DOM normalizer refused preference presentation: phase=%s err_type=%T wrapper_code=%s hard_count=%d hard_shown=%d hard_codes=%q hard_paths=%q", phase, err, wrapperCode, len(hard), len(codes), codes, paths)
		}
		return canonical
	}
	oldDOM := normalize("original", before)
	expectedDOM := normalize("expected", expected)
	currentDOM := normalize("current", current)
	if !bytes.Equal(expectedDOM, currentDOM) {
		t.Fatal("canonical preference DOM differs outside the finite class delta")
	}
	t.Logf("synthetic preference DOM case=%s filesystem=%t original_sha256=%s expected_sha256=%s current_sha256=%s original_equal=%t", fixture.name, filesystem, guiPublicHash(oldDOM), guiPublicHash(expectedDOM), guiPublicHash(currentDOM), bytes.Equal(oldDOM, currentDOM))
	return current, expectedDOM
}

func TestGUIPreferencesLayoutDOM(t *testing.T) {
	f := guiNewPreferenceFixture(t)
	for _, filesystem := range []bool{false, true} {
		for _, fixture := range guiPreferenceCases {
			t.Run(fmt.Sprintf("filesystem-%t/%s", filesystem, fixture.name), func(t *testing.T) {
				f.compare(t, filesystem, fixture)
			})
		}
	}
}

func TestGUIPreferencesLayoutRejectsDOMMutations(t *testing.T) {
	f := guiNewPreferenceFixture(t)
	access, accessDOM := f.compare(t, false, guiPreferenceCases[2])
	notifications, notificationDOM := f.compare(t, false, guiPreferenceCases[5])
	topics, topicDOM := f.compare(t, false, guiPreferenceCases[8])
	for _, mutation := range []struct {
		name     string
		body     []byte
		expected []byte
		from     string
		to       string
	}{
		{"access-field-name", access, accessDOM, `name="needs_step_free"`, `name="synthetic-other"`},
		{"access-checked", access, accessDOM, `name="needs_step_free" checked`, `name="needs_step_free"`},
		{"access-disclosure", access, accessDOM, `places with unknown accessibility are still shown to you.`, `Synthetic changed disclosure.`},
		{"access-row-class", access, accessDOM, `<label class="row preference-choice">`, `<label class="row">`},
		{"access-inline-style", access, accessDOM, `<label class="row preference-choice">`, `<label class="row preference-choice" style="margin:0">`},
		{"notification-value", notifications, notificationDOM, `value="arrival"`, `value="synthetic-other"`},
		{"notification-muted", notifications, notificationDOM, `value="arrival" checked`, `value="arrival"`},
		{"notification-safety", notifications, notificationDOM, `Important safety and moderation notices are always delivered`, `Synthetic changed safety copy`},
		{"notification-arrival", notifications, notificationDOM, `alerts for a child you supervise.`, `Synthetic changed arrival copy.`},
		{"form-method", notifications, notificationDOM, `<form method="post" class="card">`, `<form method="get" class="card">`},
		{"form-action", notifications, notificationDOM, `<form method="post" class="card">`, `<form method="post" class="card" action="/synthetic-other/">`},
		{"csrf-field-name", notifications, notificationDOM, `<input type="hidden" name="csrfmiddlewaretoken" value="synthetic-preference-csrf">`, `<input type="hidden" name="synthetic-other" value="synthetic-preference-csrf">`},
		{"submit-class", notifications, notificationDOM, `<button class="btn preference-submit" type="submit">`, `<button class="btn" type="submit">`},
		{"topic-slug", topics, topicDOM, `value="synthetic-a"`, `value="synthetic-other"`},
		{"topic-chosen", topics, topicDOM, `value="synthetic-a" checked`, `value="synthetic-a"`},
		{"topic-feed-link", topics, topicDOM, `<a class="muted preference-feed-link" href="/">`, `<a class="muted preference-feed-link" href="/synthetic-other/">`},
		{"topic-fieldset-class", topics, topicDOM, `<fieldset class="preference-topic-fields">`, `<fieldset>`},
		{"topic-legend-class", topics, topicDOM, `<legend class="muted preference-topic-title">`, `<legend class="muted">`},
		{"topic-label-class", topics, topicDOM, `<label class="row preference-choice preference-choice--topic">`, `<label class="row preference-choice">`},
		{"topic-feed-class", topics, topicDOM, `<a class="muted preference-feed-link" href="/">`, `<a class="muted" href="/">`},
		{"topic-order", topics, topicDOM, "", ""},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			changed := append([]byte(nil), mutation.body...)
			if mutation.name == "topic-order" {
				tag := []byte(`<label class="row preference-choice preference-choice--topic">`)
				first := bytes.Index(changed, tag)
				if first < 0 {
					t.Fatal("ordered topic labels unavailable")
				}
				firstClose := bytes.Index(changed[first:], []byte("</label>"))
				if firstClose < 0 {
					t.Fatal("first topic label end unavailable")
				}
				firstEnd := firstClose + first + len("</label>")
				secondOffset := bytes.Index(changed[firstEnd:], tag)
				if firstEnd <= first || secondOffset < 0 {
					t.Fatal("ordered topic label boundary unavailable")
				}
				second := firstEnd + secondOffset
				secondClose := bytes.Index(changed[second:], []byte("</label>"))
				if secondClose < 0 {
					t.Fatal("second topic label boundary unavailable")
				}
				secondEnd := secondClose + second + len("</label>")
				changed = []byte(string(mutation.body[:first]) + string(mutation.body[second:secondEnd]) + string(mutation.body[firstEnd:second]) + string(mutation.body[first:firstEnd]) + string(mutation.body[secondEnd:]))
			} else if mutation.name == "csrf-field-name" {
				form := guiPreferenceForm(t, changed)
				if bytes.Count(form, []byte(mutation.from)) != 1 {
					t.Fatal("preference form CSRF mutation target not unique")
				}
				next := bytes.Replace(form, []byte(mutation.from), []byte(mutation.to), 1)
				changed = bytes.Replace(changed, form, next, 1)
			} else {
				// Some targets repeat (four access rows and two topic rows).
				// Mutate exactly one real target; distinct safe DOM must still fail.
				if bytes.Count(changed, []byte(mutation.from)) < 1 {
					t.Fatal("actual preference mutation target unavailable")
				}
				changed = bytes.Replace(changed, []byte(mutation.from), []byte(mutation.to), 1)
			}
			canonical, hard, err := f.normalizer.normalize(changed)
			if err != nil || len(hard) != 0 || bytes.Equal(canonical, mutation.expected) {
				t.Fatal("actual safe preference DOM mutation was not independently distinguished")
			}
		})
	}
}
