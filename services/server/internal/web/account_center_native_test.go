package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

func accountCenterFixture(t *testing.T, settings, actionError bool) (*Renderer, *http.Request, string, pongo2.Context) {
	t.Helper()
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal("fixture root unavailable")
	}
	r := NewRenderer(root)
	path, name, method := "/you/", "web/you.html", http.MethodGet
	if settings {
		path, name = "/settings/", "web/settings.html"
	}
	if actionError {
		path, method = "/settings/api-token/revoke/", http.MethodPost
	}
	a := platform.Actor{ID: 731, PublicID: "33333333-3333-4333-8333-333333333333", Username: "generated-account", DisplayName: "Generated <name> & person", Cohort: "unassigned", AgeBand: "unknown", IsActive: true}
	request := platform.WithActor(httptest.NewRequest(method, "https://account.example"+path, nil), a)
	user := accountUserMap(a)
	user["is_guardian"] = false
	data := pongo2.Context{"user": user, "is_guardian": false, "has_guardians": false, "connections_enabled": false,
		"language": "en", "display_theme": "light", "display_text": "normal", "display_motion": "reduce",
		"site_name": "Generated nondefault account site", "canonical_url": "https://account.example" + path, "csrf": "synthetic-account-center-csrf",
		"request": map[string]any{"path": path, "csp_nonce": "synthetic-account-center-nonce"}, "PRIVATE-extra": "PRIVATE-unrendered-value"}
	if settings {
		data["languages"], data["current_language"] = []any{[]string{"en", "English"}, []string{"ro", "Română"}}, "en"
	}
	if actionError {
		data["site_name"], data["canonical_url"] = "Social Activities", path
		data["messages"] = []socialMessage{{"message": "This action is temporarily unavailable. Please try again.", "tags": "error"}}
	} else {
		avatar := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="26" height="26"><circle cx="13" cy="13" r="10"/></svg>`))
		user["avatar_uri"], data["avatar_uri"] = avatar, avatar
	}
	return r, request, name, data
}

func TestNativeAccountCenterFiniteNavigationAndRecordStates(t *testing.T) {
	for _, settings := range []bool{false, true} {
		for _, guardian := range []bool{false, true} {
			for _, hasGuardians := range []bool{false, true} {
				r, request, name, data := accountCenterFixture(t, settings, false)
				data["is_guardian"], data["has_guardians"], data["connections_enabled"] = guardian, hasGuardians, true
				data["user"].(map[string]any)["is_guardian"] = guardian
				page, err := r.accountCenterPage(request, name, data)
				if err != nil {
					t.Fatal("finite account projection refused")
				}
				if page.Settings != settings || len(page.Groups) != 3 || page.HasToken || page.LanguageNext != request.URL.Path {
					t.Fatal("finite page shape differs")
				}
				if (len(page.Shell.AccountTabs) == 8) != hasGuardians {
					t.Fatal("guardian tab source flag differs")
				}
				privacy := page.Groups[1].Links
				wards, guardians := 0, 0
				for _, link := range privacy {
					if link.URL == "/wards/" {
						wards++
					}
					if link.URL == "/guardianship/" {
						guardians++
					}
				}
				if (wards == 1) != guardian || (guardians == 1) != hasGuardians {
					t.Fatal("source display flag became authority or changed navigation")
				}
				if settings {
					want := []string{"/display/", "/notifications/preferences/", "/access/", "/interests/", "/topics/", "/saved-searches/"}
					actual := []string{}
					for _, link := range page.Groups[0].Links {
						actual = append(actual, link.URL)
					}
					if !reflect.DeepEqual(actual, want) {
						t.Fatal("settings finite order differs")
					}
				}
			}
		}
	}
}

func TestNativeAccountCenterNormalAndRevokeErrorContextsStayDistinct(t *testing.T) {
	for _, actionError := range []bool{false, true} {
		r, request, name, data := accountCenterFixture(t, true, actionError)
		data["api_token_created"] = time.Date(2020, time.January, 15, 10, 0, 0, 0, time.UTC) // Deliberately old stored metadata, not validity.
		page, err := r.accountCenterPage(request, name, data)
		if err != nil {
			t.Fatal("source context unavailable")
		}
		if !page.HasToken || !strings.Contains(page.TokenText, "15 Jan 2020") {
			t.Fatal("stored metadata incorrectly treated as current token validity")
		}
		if actionError {
			if page.LanguageNext != "/settings/api-token/revoke/" || page.Shell.Canonical != page.LanguageNext || page.Shell.SiteName != "Social Activities" || page.Avatar != "" || len(page.Messages) != 1 {
				t.Fatal("POST error normalized to GET context")
			}
		} else if page.Shell.Canonical != "https://account.example/settings/" || page.Shell.SiteName != "Generated nondefault account site" || page.Avatar == "" || len(page.Messages) != 0 {
			t.Fatal("GET enrichment lost")
		}
		raw, err := r.renderAccountCenter(request, name, data)
		if err != nil {
			t.Fatal("source context did not render")
		}
		body := html.UnescapeString(string(raw))
		if !strings.Contains(body, `name="next" value="`+request.URL.Path+`"`) || !strings.Contains(body, `action="/settings/api-token/revoke/"`) || strings.Contains(body, "PRIVATE-unrendered") {
			t.Fatal("source form/context or private field changed")
		}
	}
}

func TestNativeAccountCenterEscapesAndPreservesReadOnlyActions(t *testing.T) {
	r, request, name, data := accountCenterFixture(t, true, true)
	data["messages"] = []socialMessage{{"message": `Generated <script>alert("text")</script>`, "tags": "error"}}
	before, _ := json.Marshal(data)
	raw, err := r.renderAccountCenter(request, name, data)
	if err != nil {
		t.Fatal("escaped account view unavailable")
	}
	after, _ := json.Marshal(data)
	if !bytes.Equal(before, after) {
		t.Fatal("projection mutated source snapshot")
	}
	body := string(raw)
	for _, part := range []string{`class="account-center-native has-tabbar"`, `&lt;script&gt;`, `href="/account/export/"`, `href="/account/delete/"`, `action="/logout/"`, `action="/i18n/setlang/"`, `name="csrfmiddlewaretoken"`} {
		if !strings.Contains(body, part) {
			t.Fatal("required finite native contract absent")
		}
	}
	for _, private := range []string{`<script>alert`, "PRIVATE-unrendered", `name="user_id"`, `name="token"`, `name="token_key"`, `class="pill"`, `action="/account/delete/"`} {
		if strings.Contains(body, private) {
			t.Fatal("private value, invented unread count or destructive form leaked")
		}
	}
	if strings.Count(body, `id="account-center-language"`) != 1 || strings.Count(body, `id="account-center-footer-language"`) != 1 {
		t.Fatal("language forms lost independent labels")
	}
}

func TestNativeAccountCenterMalformedAuthorityAndSnapshotRefuse(t *testing.T) {
	for _, mutate := range []func(pongo2.Context){
		func(d pongo2.Context) { d["user"].(map[string]any)["id"] = int64(999) },
		func(d pongo2.Context) { d["user"].(map[string]any)["is_active"] = false },
		func(d pongo2.Context) { d["user"].(map[string]any)["is_guardian"] = true },
		func(d pongo2.Context) { d["connections_enabled"] = "true" },
		func(d pongo2.Context) { d["api_token_created"] = "private-token-value" },
		func(d pongo2.Context) { d["canonical_url"] = "javascript:alert(1)" },
		func(d pongo2.Context) { d["request"].(map[string]any)["path"] = "/settings/normalized-error/" },
		func(d pongo2.Context) { d["languages"] = []any{[]string{"en", "English"}} },
		func(d pongo2.Context) { d["display_theme"] = "invented" },
		func(d pongo2.Context) { d["user"].(map[string]any)["avatar_uri"] = "https://private.invalid/avatar" },
	} {
		r, request, name, data := accountCenterFixture(t, true, true)
		mutate(data)
		if raw, err := r.renderAccountCenter(request, name, data); err == nil || len(raw) != 0 {
			t.Fatal("malformed source or foreign authority emitted HTML")
		}
	}
	r, request, name, data := accountCenterFixture(t, true, false)
	anonymous := httptest.NewRequest(http.MethodGet, request.URL.String(), nil)
	if raw, err := r.renderAccountCenter(anonymous, name, data); err == nil || len(raw) != 0 {
		t.Fatal("anonymous native rendering admitted")
	}
	if raw, err := r.renderAccountCenter(request, "web/profile.html", data); err == nil || len(raw) != 0 {
		t.Fatal("unscoped page intercepted")
	}
}

func TestNativeAccountCenterConcurrentViewsOwnTheirState(t *testing.T) {
	r, request, name, data := accountCenterFixture(t, false, false)
	want, err := r.renderAccountCenter(request, name, data)
	if err != nil {
		t.Fatal("reference account render unavailable")
	}
	var wait sync.WaitGroup
	failures := make(chan bool, 16)
	for i := 0; i < 16; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			raw, err := r.renderAccountCenter(request, name, data)
			failures <- err != nil || !bytes.Equal(raw, want)
		}()
	}
	wait.Wait()
	close(failures)
	for failed := range failures {
		if failed {
			t.Fatal("independent render mutated shared source/copy state")
		}
	}
}
