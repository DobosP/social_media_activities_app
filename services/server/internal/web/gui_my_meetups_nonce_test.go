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
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/avatars"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

type guiMyMeetupsCase struct {
	name    string
	rows    int
	ends    bool
	meeting bool
}

var guiMyMeetupsCases = []guiMyMeetupsCase{
	{"empty", 0, false, false},
	{"populated", 2, true, true},
	{"optional-absent", 1, false, false},
}

var guiMyMeetupsScript = guiPlacesNonceScript{"/static/js/my-meetups.js", true}

type guiMyMeetupsFixture struct {
	root         string
	originalRoot string
	current      map[string][]byte
	original     map[string][]byte
	base         *guiPublicFSSnapshot
	normalizer   *guiPublicNormalizer
}

func guiNewMyMeetupsFixture(t *testing.T) *guiMyMeetupsFixture {
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
	const target = "apps/web/templates/web/my_meetups.html"
	current[target] = read(target)
	added := []byte(`<script nonce="{{ request.csp_nonce }}" src="{% static 'js/my-meetups.js' %}" defer>`)
	if bytes.Count(current[target], added) != 1 {
		t.Fatal("exact one existing my-meetups script nonce attribute required")
	}
	original[target] = bytes.Replace(current[target], added, []byte(`<script src="{% static 'js/my-meetups.js' %}" defer>`), 1)
	if len(original[target]) != 2377 || guiPublicHash(original[target]) != "ab9ba410a854d39157c77b5d1bf81fd617076c1a0dca5099602fad289b05376c" {
		t.Fatal("whole my-meetups inverse differs beyond its one nonce attribute")
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
	// Seven source/catalog files, not a response baseline or workspace clone.
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
	f := &guiMyMeetupsFixture{root, originalRoot, current, original, base, normalizer}
	t.Cleanup(func() {
		if err := base.check(); err != nil {
			t.Error(err)
		}
		for name, raw := range current {
			if !bytes.Equal(read(name), raw) {
				t.Error("actual my-meetups source changed during presentation checks")
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

// Native header/tag binding owns nonce/load/order refusals independently of
// Normalize hard findings. No browser, worker/cache or private-route gate runs.
func guiMyMeetupsNonceBinding(body, policy string, current bool) (string, error) {
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
		return "", fmt.Errorf("actual my-meetups/base nonce differs")
	}
	value := ""
	if current {
		value = nonce
	}
	tag := guiPlacesNonceTag(guiMyMeetupsScript, value)
	if strings.Count(body, tag) != 1 || strings.Count(body, `src="/static/js/my-meetups.js"`) != 1 || strings.Index(body, tag) <= strings.Index(body, `id="site-js"`) {
		return "", fmt.Errorf("my-meetups nonce/path/defer/base order differs")
	}
	return nonce, nil
}

func (f *guiMyMeetupsFixture) render(t *testing.T, filesystem, original bool, fixture guiMyMeetupsCase) ([]byte, string) {
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
			if len(files) != 7 {
				t.Error("my-meetups FS source member set changed")
			}
			for name, raw := range selected {
				if files[name] == nil || files[name].Mode != 0o444 || !bytes.Equal(files[name].Data, raw) {
					t.Error("my-meetups FS source bytes/mode changed")
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
	// Explicit synthetic authenticated ADULT/MEMBER-CONTENT PRESENTATION only.
	// No socialUpcoming/socialGuardians/DB/API/auth/ownership/guardian/privacy/
	// admission/real member or child data, service worker/cache or browser runs.
	actor := platform.Actor{ID: 9001, PublicID: "00000000-0000-4000-8000-000000009001", Username: "gui_fixture_adult", DisplayName: "GUI fixture", AgeBand: "adult", Cohort: "adult", IsActive: true}
	request := platform.WithActor(httptest.NewRequest("GET", "https://gui-fixture.invalid/my-meetups/", nil), actor)
	request.Header.Set("Accept-Language", "en")
	user := socialActor(actor)
	user["avatar_uri"] = avatars.DataURI(avatars.RenderGeneration(avatars.DefaultGeneration, avatars.SignatureSeed(actor.Username, avatars.DefaultGeneration, 0), nil, nil, avatars.Options{PX: 80}))
	rows := []map[string]any{}
	for index := 0; index < fixture.rows; index++ {
		start := time.Date(2030, 1, 2, 12+index, 0, 0, 0, time.UTC)
		row := map[string]any{"pk": 1800 + index, "title": fmt.Sprintf("Synthetic member meetup %d", index), "activity_type": map[string]any{"name": "Synthetic type"}, "starts_at": start, "place": map[string]any{"display_name": "Fictional venue", "name": "Fictional venue"}}
		if fixture.ends {
			row["ends_at"] = start.Add(time.Hour)
		}
		if fixture.meeting {
			row["meeting_point"] = fmt.Sprintf("Fictional member meeting point %d.", index)
		}
		rows = append(rows, row)
	}
	data := pongo2.Context{"user": user, "csrf": "synthetic-my-meetups-csrf", "meetups": rows, "generated_at": time.Date(2029, 12, 31, 10, 0, 0, 0, time.UTC), "my_guardians": []any{}}
	response := httptest.NewRecorder()
	if err := renderer.Render(response, request, "web/my_meetups.html", data); err != nil {
		t.Fatal(err)
	}
	body := append([]byte(nil), response.Body.Bytes()...)
	policy := response.Header().Get("Content-Security-Policy-Report-Only")
	if response.Code != 200 || response.Header().Get("Content-Type") != "text/html; charset=utf-8" || policy == "" || response.Header().Get("Content-Security-Policy") != "" {
		t.Fatal("existing my-meetups response/report-only mode differs")
	}
	if _, err := guiMyMeetupsNonceBinding(string(body), policy, !original); err != nil {
		t.Fatal(err)
	}
	if bytes.Count(body, []byte(`<p id="offline-note" class="card card-accent" hidden role="status">`)) != 1 || !bytes.Contains(body, []byte("You're offline &mdash; this is a saved copy.")) || !bytes.Contains(body, []byte("may not show here.")) || !bytes.Contains(body, []byte("Saved ")) {
		t.Fatal("original hidden offline-honesty status/copy differs")
	}
	count := map[bool]int{false: 0, true: 1}[fixture.rows > 0]
	if bytes.Count(body, []byte(`href="/account/calendar.ics" download`)) != count || bytes.Count(body, []byte("No upcoming meetups yet.")) != 1-count {
		t.Fatal("original empty/calendar branch differs")
	}
	for index := 0; index < fixture.rows; index++ {
		link := fmt.Sprintf(`href="/activities/%d/">Synthetic member meetup %d</a>`, 1800+index, index)
		if bytes.Count(body, []byte(link)) != 1 || !bytes.Contains(body, []byte("Fictional venue")) {
			t.Fatal("synthetic native member-card identity/place differs")
		}
	}
	if bytes.Count(body, []byte("Where to meet:")) != map[bool]int{false: 0, true: fixture.rows}[fixture.meeting] || bytes.Count(body, []byte("&ndash;")) != map[bool]int{false: 0, true: fixture.rows}[fixture.ends] || bytes.Contains(body, []byte("Grown-ups you can turn to")) {
		t.Fatal("adult optional-time/meeting-point presentation differs; guardian coverage is excluded")
	}
	return body, policy
}

func (f *guiMyMeetupsFixture) compare(t *testing.T, filesystem bool, fixture guiMyMeetupsCase) ([]byte, string, []byte) {
	t.Helper()
	before, _ := f.render(t, filesystem, true, fixture)
	current, policy := f.render(t, filesystem, false, fixture)
	second, secondPolicy := f.render(t, filesystem, false, fixture)
	first, err := guiMyMeetupsNonceBinding(string(current), policy, true)
	if err != nil {
		t.Fatal(err)
	}
	next, err := guiMyMeetupsNonceBinding(string(second), secondPolicy, true)
	if err != nil || first == next {
		t.Fatal("actual my-meetups response nonce not fresh/bound")
	}
	old := []byte(guiPlacesNonceTag(guiMyMeetupsScript, ""))
	if bytes.Count(before, old) != 1 {
		t.Fatal("exact original my-meetups script required")
	}
	expected := bytes.Replace(before, old, []byte(guiPlacesNonceTag(guiMyMeetupsScript, "synthetic-expected-my-meetups-nonce")), 1)
	normalize := func(phase string, raw []byte) []byte {
		canonical, hard, err := f.normalizer.normalize(raw)
		if err != nil || len(hard) != 0 {
			t.Fatalf("released DOM normalizer refused member presentation phase=%s error_type=%T hard_count=%d", phase, err, len(hard))
		}
		return canonical
	}
	originalDOM, expectedDOM, currentDOM := normalize("original", before), normalize("expected", expected), normalize("current", current)
	if !bytes.Equal(expectedDOM, currentDOM) {
		t.Fatal("member canonical DOM differs outside one nonce attribute")
	}
	// Delivered nonce volatility permits equality; no added masks/forced delta.
	t.Logf("synthetic my-meetups DOM case=%s filesystem=%t original_equal=%t original_sha256=%s expected_sha256=%s current_sha256=%s", fixture.name, filesystem, bytes.Equal(originalDOM, currentDOM), guiPublicHash(originalDOM), guiPublicHash(expectedDOM), guiPublicHash(currentDOM))
	return current, policy, expectedDOM
}

func TestGUIMyMeetupsNonceDOM(t *testing.T) {
	f := guiNewMyMeetupsFixture(t)
	for _, filesystem := range []bool{false, true} {
		for _, fixture := range guiMyMeetupsCases {
			t.Run(fmt.Sprintf("filesystem-%t/%s", filesystem, fixture.name), func(t *testing.T) {
				f.compare(t, filesystem, fixture)
			})
		}
	}
}

func TestGUIMyMeetupsNonceRejectsBindingMutations(t *testing.T) {
	f := guiNewMyMeetupsFixture(t)
	body, policy := f.render(t, false, false, guiMyMeetupsCases[1])
	nonce, err := guiMyMeetupsNonceBinding(string(body), policy, true)
	if err != nil {
		t.Fatal(err)
	}
	tag := guiPlacesNonceTag(guiMyMeetupsScript, nonce)
	for _, mutation := range []struct{ name, tag string }{
		{"missing-nonce", guiPlacesNonceTag(guiMyMeetupsScript, "")},
		{"wrong-nonce", guiPlacesNonceTag(guiMyMeetupsScript, "wrong-"+nonce)},
		{"duplicate", tag + "</script>" + tag},
		{"changed-path", strings.Replace(tag, "/static/js/my-meetups.js", "/static/js/synthetic-other.js", 1)},
		{"changed-defer", strings.Replace(tag, " defer>", " async>", 1)},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			changed := strings.Replace(string(body), tag, mutation.tag, 1)
			if changed == string(body) {
				t.Fatal("binding mutation target unchanged")
			}
			if _, err := guiMyMeetupsNonceBinding(changed, policy, true); err == nil {
				t.Fatal("native my-meetups binding mutation accepted")
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
				full := tag + "</script>"
				if strings.Count(changed, full) != 1 || strings.Count(changed, "</head>") != 1 {
					t.Fatal("exact script/head order mutation targets required")
				}
				changed = strings.Replace(changed, full, "", 1)
				changed = strings.Replace(changed, "</head>", full+"</head>", 1)
			}
			if changed == string(body) && changedPolicy == policy {
				t.Fatal("header/order control unchanged")
			}
			if _, err := guiMyMeetupsNonceBinding(changed, changedPolicy, true); err == nil {
				t.Fatal("native header/order control accepted")
			}
		})
	}
}

func TestGUIMyMeetupsNonceRejectsDOMMutations(t *testing.T) {
	f := guiNewMyMeetupsFixture(t)
	empty, _, emptyDOM := f.compare(t, false, guiMyMeetupsCases[0])
	filled, _, filledDOM := f.compare(t, false, guiMyMeetupsCases[1])
	for _, mutation := range []struct {
		name     string
		empty    bool
		from, to string
	}{
		{"hidden-status", false, `<p id="offline-note" class="card card-accent" hidden role="status">`, `<p id="offline-note" class="card card-accent" role="status">`},
		{"status-role", false, `hidden role="status"`, `hidden role="note"`},
		{"offline-copy", false, `You're offline &mdash; this is a saved copy.`, `Synthetic changed offline warning.`},
		{"calendar-download", false, `href="/account/calendar.ics" download`, `href="/account/calendar.ics"`},
		{"meetup-link", false, `href="/activities/1800/"`, `href="/activities/1801/"`},
		{"meeting-point", false, `Fictional member meeting point 0.`, `Synthetic changed meeting point.`},
		{"saved-copy", false, `Saved `, `Snapshot `},
		{"empty-copy", true, `No upcoming meetups yet.`, `Synthetic changed empty copy.`},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			body, expected := filled, filledDOM
			if mutation.empty {
				body, expected = empty, emptyDOM
			}
			if bytes.Count(body, []byte(mutation.from)) != 1 {
				t.Fatal("exact unique safe member DOM mutation target required")
			}
			changed := bytes.Replace(body, []byte(mutation.from), []byte(mutation.to), 1)
			canonical, hard, err := f.normalizer.normalize(changed)
			if err != nil || len(hard) != 0 {
				t.Fatalf("safe member mutation refused before equality error_type=%T hard_count=%d", err, len(hard))
			}
			if bytes.Equal(canonical, expected) {
				t.Fatal("meaningful member mutation accepted as nonce-only expected DOM")
			}
		})
	}
}
