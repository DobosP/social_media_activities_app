package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

const guiPublicExpectedNonceMarker = "synthetic-expected-two-script-delta"

var guiPublicOriginalScriptTags = []string{
	`<script src="/static/js/hovercard.js" defer>`,
	`<script id="site-js" src="/static/js/site.js" defer>`,
}

// This is the declared two-attribute M1 delta, not a normalizer or a new
// baseline. Only the existing hash-bound anonymous originals reach this path.
// Their bytes remain untouched; the expected copy is never persisted as goldens.
func guiPublicExpectedNonceReference(original []byte) ([]byte, map[string]any, error) {
	expected := append([]byte(nil), original...)
	for _, tag := range guiPublicOriginalScriptTags {
		if bytes.Count(original, []byte(tag)) != 1 {
			return nil, nil, fmt.Errorf("original reference does not have exactly the known unnonced external script shape")
		}
		replacement := `<script nonce="` + guiPublicExpectedNonceMarker + `"` + strings.TrimPrefix(tag, "<script")
		expected = bytes.Replace(expected, []byte(tag), []byte(replacement), 1)
	}
	return expected, map[string]any{
		"kind":             "only-two-existing-external-script-nonce-attributes",
		"paths":            []string{"/static/js/hovercard.js", "/static/js/site.js"},
		"attributes_added": 2, "original_raw_sha256": guiPublicHash(original), "expected_copy_sha256": guiPublicHash(expected),
		"scope": "explicit in-memory expected delta; no original or normalized baseline replaced; released SDK masks unchanged",
	}, nil
}

func guiPublicScriptNonceBinding(body, policy string, owner *string) (map[string]any, error) {
	var nonce string
	directives, nonces := 0, 0
	for _, directive := range strings.Split(policy, ";") {
		fields := strings.Fields(directive)
		if len(fields) == 0 || fields[0] != "script-src" {
			continue
		}
		directives++
		for _, token := range fields[1:] {
			if strings.HasPrefix(token, "'nonce-") && strings.HasSuffix(token, "'") {
				nonces++
				nonce = strings.TrimSuffix(strings.TrimPrefix(token, "'nonce-"), "'")
			}
		}
	}
	if directives != 1 || nonces != 1 || nonce == "" || strings.ContainsAny(nonce, "\"'<> \t\r\n") {
		return nil, fmt.Errorf("one nonempty actual script-src nonce required")
	}
	hovercard := `<script nonce="` + nonce + `" src="/static/js/hovercard.js" defer>`
	site := `<script nonce="` + nonce + `" id="site-js" src="/static/js/site.js" defer`
	if owner != nil {
		site += ` data-meetups-owner="` + html.EscapeString(*owner) + `"`
	}
	site += ">"
	if strings.Count(body, hovercard) != 1 || strings.Count(body, site) != 1 || strings.Count(body, `src="/static/js/hovercard.js"`) != 1 || strings.Count(body, `src="/static/js/site.js"`) != 1 {
		return nil, fmt.Errorf("external script path/load attributes or actual header/tag nonce binding differs")
	}
	if strings.Count(body, "data-meetups-owner=") != map[bool]int{false: 0, true: 1}[owner != nil] {
		return nil, fmt.Errorf("authenticated-only external script attribute differs")
	}
	return map[string]any{"header_nonce_sha256": guiPublicHash([]byte(nonce)), "bound_external_scripts": 2, "owner_attribute_present": owner != nil, "paths": []string{"/static/js/hovercard.js", "/static/js/site.js"}}, nil
}

func guiPublicNonceResponse(t *testing.T, filesystem, authenticated bool) (string, string) {
	t.Helper()
	root, err := filepath.Abs("../../../../")
	if err != nil {
		t.Fatal(err)
	}
	renderer := NewRenderer(root)
	if filesystem {
		snapshot, err := guiPublicFilesystemSnapshot(root)
		if err != nil {
			t.Fatal(err)
		}
		renderer, err = NewRendererFS(root, snapshot.files)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest("GET", "https://gui-fixture.invalid/", nil)
	data := pongo2.Context{}
	var owner *string
	if authenticated {
		// Fictional adult presentation context only; no account/session, eligibility
		// or privacy admission is established by this database-free renderer test.
		actor := platform.Actor{ID: 9001, PublicID: "00000000-0000-4000-8000-000000009001", Username: "gui_fixture_adult", DisplayName: "GUI fixture", AgeBand: "adult", Cohort: "adult", IsActive: true}
		request = platform.WithActor(request, actor)
		data["user"] = socialActor(actor) // Existing native view context, unchanged.
		owner = &actor.PublicID
	}
	seen := map[string]bool{}
	var firstBody, firstPolicy string
	for iteration := 0; iteration < 2; iteration++ {
		response := httptest.NewRecorder()
		if err := renderer.Render(response, request, "web/landing.html", data); err != nil {
			t.Fatal(err)
		}
		body, policy := response.Body.String(), response.Header().Get("Content-Security-Policy-Report-Only")
		if response.Code != 200 || response.Header().Get("Content-Type") != "text/html; charset=utf-8" || policy == "" || response.Header().Get("Content-Security-Policy") != "" {
			t.Fatal("existing native response or report-only mode changed")
		}
		binding, err := guiPublicScriptNonceBinding(body, policy, owner)
		if err != nil {
			t.Fatal(err)
		}
		digest := binding["header_nonce_sha256"].(string)
		if seen[digest] {
			t.Fatal("actual renderer reused a nonce across responses")
		}
		seen[digest] = true
		if iteration == 0 {
			firstBody, firstPolicy = body, policy
		}
	}
	return firstBody, firstPolicy
}

func TestGUIPublicExternalScriptsBindFreshNativeNonce(t *testing.T) {
	for _, filesystem := range []bool{false, true} {
		for _, authenticated := range []bool{false, true} {
			t.Run(fmt.Sprintf("filesystem-%t/authenticated-%t", filesystem, authenticated), func(t *testing.T) {
				guiPublicNonceResponse(t, filesystem, authenticated)
			})
		}
	}
}

func TestGUIPublicNonceBindingRejectsRealRenderedMutations(t *testing.T) {
	body, policy := guiPublicNonceResponse(t, false, false)
	path := strings.Index(body, `src="/static/js/hovercard.js"`)
	if path < 0 {
		t.Fatal("actual nonce control missing")
	}
	start := strings.LastIndex(body[:path], "<script")
	end := strings.Index(body[path:], ">") + path
	if start < 0 || end < path {
		t.Fatal("actual script start tag missing")
	}
	hovercard := body[start : end+1]
	for name, mutate := range map[string]func(string, string) (string, string){
		"missing-nonce": func(b, p string) (string, string) {
			return strings.Replace(b, hovercard, guiPublicOriginalScriptTags[0], 1), p
		},
		"mismatched-nonce": func(b, p string) (string, string) {
			return strings.Replace(b, hovercard, strings.Replace(hovercard, `nonce="`, `nonce="wrong-`, 1), 1), p
		},
		"changed-defer": func(b, p string) (string, string) {
			return strings.Replace(b, hovercard, strings.Replace(hovercard, " defer>", " async>", 1), 1), p
		},
		"changed-path": func(b, p string) (string, string) {
			return strings.Replace(b, "/static/js/site.js", "/static/js/different.js", 1), p
		},
		"unexpected-owner": func(b, p string) (string, string) {
			return strings.Replace(b, `id="site-js"`, `id="site-js" data-meetups-owner="unexpected"`, 1), p
		},
		"duplicate-script": func(b, p string) (string, string) {
			return strings.Replace(b, "</body>", hovercard+"</script></body>", 1), p
		},
		"missing-header": func(b, p string) (string, string) { return b, "" },
	} {
		t.Run(name, func(t *testing.T) {
			changed, changedPolicy := mutate(body, policy)
			if changed == body && changedPolicy == policy {
				t.Fatal("mutation did not touch the real response")
			}
			if _, err := guiPublicScriptNonceBinding(changed, changedPolicy, nil); err == nil {
				t.Fatal("actual nonce/attribute mutation accepted")
			}
		})
	}
}

func TestGUIPublicExpectedNonceDeltaIsExactlyTwoAttributes(t *testing.T) {
	original := []byte(`<main>literal nonce text</main><script src="/static/js/hovercard.js" defer></script><script id="site-js" src="/static/js/site.js" defer></script>`)
	before := append([]byte(nil), original...)
	expected, delta, err := guiPublicExpectedNonceReference(original)
	want := []byte(`<main>literal nonce text</main><script nonce="synthetic-expected-two-script-delta" src="/static/js/hovercard.js" defer></script><script nonce="synthetic-expected-two-script-delta" id="site-js" src="/static/js/site.js" defer></script>`)
	if err != nil || !bytes.Equal(expected, want) || !bytes.Equal(original, before) || delta["attributes_added"] != 2 || delta["original_raw_sha256"] != guiPublicHash(before) || delta["expected_copy_sha256"] != guiPublicHash(want) {
		t.Fatal("declared expected delta changed anything beyond the two attributes")
	}
	for name, changed := range map[string][]byte{
		"missing":                bytes.Replace(original, []byte(guiPublicOriginalScriptTags[0]), nil, 1),
		"duplicate":              append(append([]byte(nil), original...), []byte(guiPublicOriginalScriptTags[1])...),
		"already-nonced":         want,
		"changed-load-attribute": bytes.Replace(original, []byte(" defer>"), []byte(" async>"), 1),
		"changed-path":           bytes.Replace(original, []byte("/static/js/site.js"), []byte("/static/js/different.js"), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := guiPublicExpectedNonceReference(changed); err == nil {
				t.Fatal("unsupported baseline shape accepted as the declared delta")
			}
		})
	}
}

func TestGUIPublicBaseTemplateOnlyAddsTheTwoNonceAttributes(t *testing.T) {
	root, err := filepath.Abs("../../../../")
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(filepath.Join(root, "templates/base.html"))
	if err != nil {
		t.Fatal(err)
	}
	before := append([]byte(nil), current...)
	for _, original := range []string{`<script src="{% static 'js/hovercard.js' %}" defer>`, `<script id="site-js" src="{% static 'js/site.js' %}" defer{% if user.is_authenticated %} data-meetups-owner="{{ user.public_id }}"{% endif %}>`} {
		changed := `<script nonce="{{ request.csp_nonce }}"` + strings.TrimPrefix(original, "<script")
		if bytes.Count(before, []byte(changed)) != 1 {
			t.Fatal("the exact two template nonce additions differ")
		}
		before = bytes.Replace(before, []byte(changed), []byte(original), 1)
	}
	var checkpoint struct {
		Sources map[string]string `json:"renderer_sources"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "docs/reviews/gui-public-original/checkpoint.json"))
	if err != nil || json.Unmarshal(raw, &checkpoint) != nil || len(before) != 13594 || guiPublicHash(before) != "ef2f179f6cbbfa8acc38272aa5a6c1d6acbb36c3c6e9290102d98a23b4b02e3b" || checkpoint.Sources["templates/base.html"] != guiPublicHash(before) {
		t.Fatal("base template changed beyond the declared nonce attributes or original source binding drifted")
	}
}
