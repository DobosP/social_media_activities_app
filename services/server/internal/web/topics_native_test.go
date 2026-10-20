package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

func topicsNativeFixture(t *testing.T) (*Renderer, *http.Request, pongo2.Context) {
	t.Helper()
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal("topics fixture root unavailable")
	}
	actor := platform.Actor{ID: 941, PublicID: "55555555-5555-4555-8555-555555555941", Username: "generated-topics", DisplayName: "Generated <name> & person", Cohort: "adult", AgeBand: "adult", IsActive: true}
	request := platform.WithActor(httptest.NewRequest(http.MethodGet, "https://topics.example/topics/", nil), actor)
	avatar := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><circle cx="5" cy="5" r="3"/></svg>`))
	// topicPage supplies socialNav plus category/selection maps. The user is
	// precisely Renderer defaults, without AccountView id/pk/is_active fields.
	data := pongo2.Context{
		"user":     map[string]any{"is_authenticated": true, "public_id": actor.PublicID, "username": actor.Username, "display_name": actor.DisplayName, "avatar_uri": avatar},
		"language": "en", "display_theme": "auto", "display_text": "normal", "display_motion": "auto", "site_name": "Generated topic site", "canonical_url": "https://topics.example/topics/", "csrf": "synthetic-topics-csrf",
		"request":    map[string]any{"path": "/topics/", "csp_nonce": "synthetic-topics-nonce"},
		"categories": []map[string]any{{"slug": "generated-first", "name": "Zed supplied first", "description": "Generated detail"}, {"slug": "generated-second", "name": "Alpha supplied second", "description": ""}},
		"chosen":     map[string]bool{}, "connections_enabled": false, "has_guardians": false, "unread_notifications": 2, "PRIVATE_extra": "PRIVATE-topics-unrendered",
	}
	return NewRenderer(root), request, data
}

func topicsNativeForm(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, `<form method="post" class="card topics-form">`)
	if start < 0 {
		t.Fatal("topics owning form unavailable")
	}
	end := strings.Index(body[start:], "</form>")
	if end < 0 {
		t.Fatal("topics owning form closing unavailable")
	}
	return body[start : start+end+len("</form>")]
}

func TestNativeTopicsOrderedChoicesMembershipAndEmptyState(t *testing.T) {
	for _, language := range []string{"en", "ro"} {
		for _, variant := range []string{"empty", "none", "selected", "false-valued-key", "unknown-selected-empty"} {
			r, request, data := topicsNativeFixture(t)
			data["language"] = language
			if variant == "empty" || variant == "unknown-selected-empty" {
				data["categories"] = []map[string]any{}
			}
			chosen := data["chosen"].(map[string]bool)
			if variant == "selected" || variant == "false-valued-key" {
				chosen["generated-first"] = variant == "selected"
			}
			if variant == "unknown-selected-empty" {
				chosen["not-a-visible-category"] = true
			}
			before, _ := json.Marshal(data)
			raw, err := r.renderTopics(request, data)
			if err != nil {
				t.Fatal("typed topics rendering unavailable", err)
			}
			after, _ := json.Marshal(data)
			if !bytes.Equal(before, after) {
				t.Fatal("topics projection mutated its producer")
			}
			body, form := string(raw), topicsNativeForm(t, string(raw))
			empty := variant == "empty" || variant == "unknown-selected-empty"
			if !strings.Contains(body, `<html lang="`+language+`"`) || strings.Contains(form, " action=") || strings.Count(form, `name="csrfmiddlewaretoken" value="synthetic-topics-csrf"`) != 1 || strings.Contains(form, `name="user_id"`) || strings.Contains(body, "PRIVATE-topics-unrendered") {
				t.Fatal("default-shell, implicit-action or form boundary differs")
			}
			if strings.Contains(form, "No topics are available yet.") != empty || strings.Count(form, `type="checkbox"`) != map[bool]int{false: 2, true: 0}[empty] || strings.Contains(form, `class="muted preference-feed-link"`) != (len(chosen) != 0) {
				t.Fatal("empty, selected or independent feed-link condition differs")
			}
			if !empty {
				if strings.Index(form, "Zed supplied first") > strings.Index(form, "Alpha supplied second") || strings.Count(form, "Generated detail") != 1 {
					t.Fatal("producer category order or optional description differs")
				}
				_, checked := chosen["generated-first"]
				first := `name="topics" value="generated-first"`
				if checked {
					first += " checked"
				}
				if strings.Count(form, first+">") != 1 || strings.Count(form, `name="topics" value="generated-second">`) != 1 {
					t.Fatal("checkbox key membership differs from source template")
				}
			}
			if !strings.Contains(body, "It never hides anything:") || strings.Contains(body, `aria-label="Account"`) || strings.Contains(body, "/wards/") || strings.Count(body, `class="pill">2</span>`) != 1 {
				t.Fatal("original explanatory copy or self-only shell changed")
			}
		}
	}
}

func TestNativeTopicsEscapingAndNavigationFlags(t *testing.T) {
	r, request, data := topicsNativeFixture(t)
	data["categories"] = []map[string]any{{"slug": `generated-" onclick="payload`, "name": `<script>topic</script>`, "description": `<img src=x onerror=payload>`}}
	data["connections_enabled"], data["has_guardians"] = true, true
	data["messages"] = []socialMessage{{"message": `<script>notice</script>`, "tags": `error" onclick="payload`}}
	raw, err := r.renderTopics(request, data)
	if err != nil {
		t.Fatal("escaped topics rendering unavailable")
	}
	body := string(raw)
	for _, wanted := range []string{"&lt;script&gt;topic&lt;/script&gt;", "&lt;img src=x onerror=payload&gt;", "&lt;script&gt;notice&lt;/script&gt;", `href="/connections/"`, `Generated &lt;name&gt; &amp; person`} {
		if !strings.Contains(body, wanted) {
			t.Fatal("source label, message or explicit navigation flag lost")
		}
	}
	for _, forbidden := range []string{"<script>topic", "<script>notice", "<img src=x", `" onclick="payload`, "/wards/", "/guardianship/", "PRIVATE-topics-unrendered"} {
		if strings.Contains(body, forbidden) {
			t.Fatal("active content or invented guardian authority rendered")
		}
	}
}

func TestNativeTopicsRejectsMalformedSnapshotWithoutAccountViewRequirements(t *testing.T) {
	for name, mutate := range map[string]func(pongo2.Context){
		"missing-categories":   func(d pongo2.Context) { delete(d, "categories") },
		"wrong-chosen-type":    func(d pongo2.Context) { d["chosen"] = map[string]any{"generated-first": true} },
		"nontext-label":        func(d pongo2.Context) { d["categories"].([]map[string]any)[0]["name"] = 1 },
		"foreign-shell":        func(d pongo2.Context) { d["user"].(map[string]any)["public_id"] = "foreign" },
		"anonymous-shell":      func(d pongo2.Context) { d["user"].(map[string]any)["is_authenticated"] = false },
		"unsafe-avatar":        func(d pongo2.Context) { d["user"].(map[string]any)["avatar_uri"] = "https://foreign.invalid/avatar" },
		"unsafe-canonical":     func(d pongo2.Context) { d["canonical_url"] = "javascript:payload" },
		"empty-private-csrf":   func(d pongo2.Context) { d["csrf"] = "" },
		"wrong-language":       func(d pongo2.Context) { d["language"] = "invalid" },
		"negative-unread":      func(d pongo2.Context) { d["unread_notifications"] = -1 },
		"foreign-request-path": func(d pongo2.Context) { d["request"].(map[string]any)["path"] = "/other/" },
	} {
		t.Run(name, func(t *testing.T) {
			r, request, data := topicsNativeFixture(t)
			mutate(data)
			if raw, err := r.renderTopics(request, data); err == nil || len(raw) != 0 {
				t.Fatal("invalid snapshot produced HTML")
			}
		})
	}
	r, request, data := topicsNativeFixture(t)
	if _, err := r.renderTopics(httptest.NewRequest(http.MethodGet, "https://topics.example/topics/", nil), data); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("anonymous direct render admitted")
	}
	actor, _ := platform.ActorFrom(request)
	actor.IsActive = false
	if _, err := r.renderTopics(platform.WithActor(request, actor), data); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("inactive direct render admitted")
	}
	// The valid default-shell fixture has none of AccountView's extra fields.
	if raw, err := r.renderTopics(request, data); err != nil || len(raw) == 0 {
		t.Fatal("valid current default shell was tightened")
	}
}

func TestNativeTopicsRendererPolicyAndConcurrentCachedTemplate(t *testing.T) {
	r, request, data := topicsNativeFixture(t)
	response := httptest.NewRecorder()
	if err := r.Render(response, request, "web/topic_preferences.html", data); err != nil {
		t.Fatal("ordinary topics dispatch failed")
	}
	body := response.Body.String()
	if !strings.Contains(body, `class="topics-native has-tabbar"`) || !strings.Contains(body, `/static/css/topics-native.css`) || strings.Contains(body, "spa-bootstrap") || response.Header().Get("Cache-Control") != "private, no-cache" || response.Header().Get("Vary") != "Cookie" || response.Header().Get("Content-Security-Policy-Report-Only") == "" || response.Header().Get("Set-Cookie") != "" {
		t.Fatal("native response, current assets or private policy differs")
	}
	if strings.Count(body, `src="/static/js/`) != 2 || !strings.Contains(body, `name="next" value="/topics/"`) || !strings.Contains(body, `action="/i18n/setlang/"`) {
		t.Fatal("existing language or script context differs")
	}
	var workers sync.WaitGroup
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		_, jobRequest, jobData := topicsNativeFixture(t)
		if i%2 != 0 {
			jobData["language"] = "ro"
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := r.renderTopics(jobRequest, jobData)
			failures <- err
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal("concurrent immutable template rendering failed")
		}
	}
}

func TestNativeTopicsTemplateLoadFailureIsReturned(t *testing.T) {
	r, request, data := topicsNativeFixture(t)
	before := loadTopicsTemplates
	defer func() { loadTopicsTemplates = before }()
	want := errors.New("synthetic topics template load failure")
	loadTopicsTemplates = func() (*template.Template, error) { return nil, want }
	if raw, err := r.renderTopics(request, data); !errors.Is(err, want) || len(raw) != 0 {
		t.Fatal("template loading failure was hidden")
	}
}
