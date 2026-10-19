package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

func notificationPreferencesFixture(mode string) (*http.Request, pongo2.Context) {
	actor := platform.Actor{ID: 17, PublicID: "11111111-1111-4111-8111-111111111111", Username: "generated-notification-owner", DisplayName: "Generated account", Cohort: "adult", AgeBand: "adult", IsActive: true, IdentityVerified: true}
	if mode == "unassigned-en" {
		actor.Cohort = "unassigned"
		actor.AgeBand = "unknown"
		actor.IdentityVerified = false
	}
	request := platform.WithActor(httptest.NewRequest("GET", "https://fixture.local/notifications/preferences/", nil), actor)
	language, theme, size, motion := "en", "light", "normal", "auto"
	if strings.HasSuffix(mode, "-ro") {
		language = "ro"
		theme = "dark"
		size = "large"
		motion = "reduce"
	}
	if mode == "unassigned-en" {
		theme, size, motion = "contrast", "larger", "full"
	}
	rows := []map[string]any{}
	for i, kind := range accountNotificationOrder {
		muted := mode == "all-ro" || mode == "some-ro" && i%3 == 0
		rows = append(rows, map[string]any{"value": kind, "label": accountNotificationLabels[kind], "reason": accountNotificationReasons[kind], "muted": muted})
	}
	data := pongo2.Context{"language": language, "display_theme": theme, "display_text": size, "display_motion": motion, "site_name": "Synthetic Activities", "csrf": "synthetic-notification-proof", "request": map[string]any{"csp_nonce": "synthetic-notification-nonce"}, "user": accountUserMap(actor), "connections_enabled": true, "rows": rows, "PRIVATE-foreign-preferences": "PRIVATE-not-rendered"}
	if mode == "errors-en" {
		data["messages"] = []socialMessage{{"message": "This action is temporarily unavailable. Please try again.", "tags": "error"}}
		rows[8]["muted"] = true
	}
	if mode == "escaped-ro" {
		rows[0]["label"] = `<script>alert("label")</script>`
		rows[0]["reason"] = `<img src=x onerror="private">`
		data["messages"] = []socialMessage{{"message": "PRIVATE-RAW-SQL-ERROR", "tags": "<script>"}}
	}
	return request, data
}

func TestNativeNotificationPreferencesOrderMutesAndMandatoryWarning(t *testing.T) {
	for _, mode := range []string{"none-en", "some-ro", "all-ro", "errors-en", "escaped-ro", "unassigned-en"} {
		t.Run(mode, func(t *testing.T) {
			request, data := notificationPreferencesFixture(mode)
			r := NewRenderer(t.TempDir())
			page, err := r.notificationPreferencesPage(request, data)
			if err != nil || len(page.Rows) != 22 {
				t.Fatal("complete optional preference projection required", err)
			}
			for i, row := range page.Rows {
				if row.Kind != accountNotificationOrder[i] || row.Muted != data["rows"].([]map[string]any)[i]["muted"] || row.Arrival != (row.Kind == "arrival") {
					t.Fatal("optional order/checked meaning/warning changed", i)
				}
			}
			raw, err := r.renderNotificationPreferences(request, data)
			if err != nil {
				t.Fatal(err)
			}
			body := string(raw)
			for _, wanted := range []string{`body class="notification-preferences-page"`, `action="/notifications/preferences/"`, `class="card notification-preferences-form"`, `name="csrfmiddlewaretoken" value="synthetic-notification-proof"`, `id="notification-arrival-warning"`, `notification-kind-arrival-reason notification-arrival-warning`, `href="/notifications/"`, `href="/profile/"`, `href="/you/"`, `data-auto-submit`, `data-meetups-owner="11111111-1111-4111-8111-111111111111"`} {
				if !strings.Contains(body, wanted) {
					t.Fatal("native preference contract missing", wanted)
				}
			}
			if strings.Count(body, `type="checkbox" id="notification-kind-`) != 22 || strings.Count(body, `name="muted"`) != 22 || strings.Count(body, `id="notification-arrival-warning"`) != 1 {
				t.Fatal("exact ordinary repeated checkbox contract changed")
			}
			for _, forbidden := range []string{`name="muted[]"`, `value="system"`, `value="moderation"`, `name="user_id"`, `name="ward_id"`, "PRIVATE-not-rendered", "PRIVATE-RAW-SQL-ERROR", "<script>alert(", "<img src=x"} {
				if strings.Contains(body, forbidden) {
					t.Fatal("unadmitted value/field/private data rendered", forbidden)
				}
			}
			if mode == "all-ro" && strings.Count(body, " checked>") != 22 {
				t.Fatal("all optional mutes lost their checked state")
			}
			if mode == "none-en" && strings.Count(body, " checked>") != 0 {
				t.Fatal("no optional mutes invented defaults")
			}
			if mode == "escaped-ro" && (!strings.Contains(body, "&lt;script&gt;") || !strings.Contains(body, "&lt;img")) {
				t.Fatal("ordinary supplied display strings were not escaped")
			}
			if mode == "errors-en" && (!strings.Contains(body, `role="alert"`) || !strings.Contains(body, `value="arrival" aria-describedby="notification-kind-arrival-reason notification-arrival-warning" checked>`)) {
				t.Fatal("save failure lost stored selection or accessible public message")
			}
			if strings.Index(body, `src="/static/js/hovercard.js"`) > strings.Index(body, `src="/static/js/site.js"`) || strings.Count(body, `nonce="synthetic-notification-nonce"`) != 2 {
				t.Fatal("existing shared script order/nonce ownership changed")
			}
			page.Rows[0].Muted = !page.Rows[0].Muted
			page.Copy["Notification settings"] = "PRIVATE-other-request"
			again, err := r.notificationPreferencesPage(request, data)
			if err != nil || again.Rows[0].Muted != data["rows"].([]map[string]any)[0]["muted"] || again.Copy["Notification settings"] == "PRIVATE-other-request" {
				t.Fatal("mutable/private state leaked across renders", err)
			}
		})
	}
}

func TestNativeNotificationPreferencesCurrentCatalogueAndRoleIndependentOptions(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	r := NewRenderer(root)
	request, data := notificationPreferencesFixture("some-ro")
	page, err := r.notificationPreferencesPage(request, data)
	if err != nil || page.Copy["Notification settings"] != "Setări notificări" || page.Copy["Save settings"] != "Salvează setările" || !strings.Contains(page.Intro, "Notificările importante de siguranță") || !strings.Contains(page.ArrivalWarning, "pentru un copil") || !strings.HasPrefix(page.Rows[0].Label, "Dezactivează:") {
		t.Fatal("existing Romanian catalogue contract lost", err)
	}
	for _, cohort := range []string{"unassigned", "child", "teen", "adult"} {
		actor, _ := platform.ActorFrom(request)
		actor.Cohort = cohort
		actor.AgeBand = "unknown"
		actor.IdentityVerified = false
		current := platform.WithActor(request, actor)
		data["user"] = accountUserMap(actor)
		page, err := r.notificationPreferencesPage(current, data)
		if err != nil || len(page.Rows) != 22 || page.Rows[21].Kind != "mod_alert" {
			t.Fatal("template invented a cohort/verification/moderator prerequisite", cohort, err)
		}
	}
}

func TestNativeNotificationPreferencesMalformedSnapshotRefusesBeforeOutput(t *testing.T) {
	probes := []struct {
		name   string
		change func(pongo2.Context)
	}{
		{"missing-rows", func(d pongo2.Context) { delete(d, "rows") }},
		{"nil-rows", func(d pongo2.Context) { d["rows"] = []map[string]any(nil) }},
		{"short-rows", func(d pongo2.Context) { d["rows"] = d["rows"].([]map[string]any)[:21] }},
		{"extra-row", func(d pongo2.Context) {
			rows := d["rows"].([]map[string]any)
			d["rows"] = append(rows, map[string]any{"value": "system", "label": "System", "reason": "Mandatory", "muted": true})
		}},
		{"duplicate-kind", func(d pongo2.Context) { d["rows"].([]map[string]any)[1]["value"] = "join_requested" }},
		{"missing-bool", func(d pongo2.Context) { delete(d["rows"].([]map[string]any)[0], "muted") }},
		{"string-bool", func(d pongo2.Context) { d["rows"].([]map[string]any)[0]["muted"] = "true" }},
		{"empty-label", func(d pongo2.Context) { d["rows"].([]map[string]any)[0]["label"] = "" }},
		{"invalid-UTF8", func(d pongo2.Context) { d["rows"].([]map[string]any)[0]["reason"] = string([]byte{0xff}) }},
		{"wrong-self", func(d pongo2.Context) { d["user"].(map[string]any)["id"] = 999999 }},
		{"invalid-display", func(d pongo2.Context) { d["display_theme"] = "untrusted" }},
		{"invalid-message-shape", func(d pongo2.Context) { d["messages"] = "PRIVATE-ERROR" }},
	}
	for _, probe := range probes {
		t.Run(probe.name, func(t *testing.T) {
			request, data := notificationPreferencesFixture("none-en")
			probe.change(data)
			w := httptest.NewRecorder()
			err := NewRenderer(t.TempDir()).Render(w, request, "web/notification_preferences.html", data)
			if err == nil || w.Body.Len() != 0 || len(w.Header()) != 0 {
				t.Fatal("malformed native snapshot wrote partial body/headers", err, w.Body.Len())
			}
		})
	}
	request, data := notificationPreferencesFixture("none-en")
	actor, _ := platform.ActorFrom(request)
	actor.IsActive = false
	if _, err := NewRenderer(t.TempDir()).renderNotificationPreferences(platform.WithActor(request, actor), data); err == nil {
		t.Fatal("inactive actor rendered account preferences")
	}
	if _, err := NewRenderer(t.TempDir()).renderNotificationPreferences(httptest.NewRequest("GET", "/notifications/preferences/", nil), data); err == nil {
		t.Fatal("anonymous account preferences rendered")
	}
}

func TestNativeNotificationPreferencesRendererKeepsHeadersNonceAndCurrentAssets(t *testing.T) {
	request, data := notificationPreferencesFixture("none-en")
	delete(data, "request")
	w := httptest.NewRecorder()
	r := NewRenderer(t.TempDir())
	r.CSPEnforce = true
	if err := r.Render(w, request, "web/notification_preferences.html", data); err != nil {
		t.Fatal(err)
	}
	policy := w.Header().Get("Content-Security-Policy")
	nonce := regexp.MustCompile(`'nonce-([^']+)'`).FindStringSubmatch(policy)
	if w.Header().Get("Content-Type") != "text/html; charset=utf-8" || w.Header().Get("Cache-Control") != "private, no-cache" || w.Header().Get("Vary") != "Cookie" || w.Header().Get("Reporting-Endpoints") == "" || len(nonce) != 2 || nonce[1] == "" {
		t.Fatal("existing private native transport changed")
	}
	if bytes.Count(w.Body.Bytes(), []byte(`nonce="`+nonce[1]+`"`)) != 2 || bytes.Contains(w.Body.Bytes(), []byte("synthetic-notification-nonce")) {
		t.Fatal("actual Renderer nonce does not bind both unchanged shared scripts")
	}
	for _, path := range []string{"bricolage-grotesque-latin-ext-wght-normal.woff2", "bricolage-grotesque-latin-wght-normal.woff2", "/static/css/base.css", "/static/css/notification-preferences-native.css", "/static/js/hovercard.js", "/static/js/site.js"} {
		if !strings.Contains(w.Body.String(), path) {
			t.Fatal("current asset missing", path)
		}
	}
}
