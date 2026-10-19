package web

import (
	"bytes"
	"html"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

func notificationInboxFixture(mode string) (*http.Request, pongo2.Context) {
	actor := platform.Actor{ID: 37, PublicID: "33333333-3333-4333-8333-333333333333", Username: "generated-inbox-owner", DisplayName: "Generated account", Cohort: "unassigned", AgeBand: "unknown", IsActive: true}
	request := platform.WithActor(httptest.NewRequest("GET", "https://fixture.local/notifications/", nil), actor)
	language, theme, size, motion := "en", "light", "normal", "auto"
	if strings.HasSuffix(mode, "-ro") {
		language, theme, size, motion = "ro", "dark", "large", "reduce"
	}
	if mode == "mixed-en" {
		theme, size, motion = "contrast", "larger", "full"
	}
	stamp := time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC)
	row := func(title, body, why, href string, read bool) map[string]any {
		var readAt any
		if read {
			readAt = true
		}
		return map[string]any{"title": title, "body": body, "why": why, "url": href, "created_at": stamp.Format(time.RFC3339), "read_at": readAt, "recipient_id": int64(999), "PRIVATE-unused": "PRIVATE-unused-inbox-field"}
	}
	items := []map[string]any{}
	switch mode {
	case "empty-en":
	case "one-unread-ro":
		items = append(items, row("Generated unread notice", "Generated body", "An important account or safety notice (you cannot turn these off).", "/notifications/", false))
	case "mixed-en", "all-read-ro":
		items = append(items, row("Generated safety notice", "Generated first body", "An important account or safety notice (you cannot turn these off).", "/profile/", mode == "all-read-ro"))
		items = append(items, row("Generated arrival notice", "Generated second body", "Someone arrived at or is on their way to a meetup you're part of, or that someone you look after joined.", "https://example.test/generated", true))
		items = append(items, row("Generated unknown notice", "", "", "", mode == "all-read-ro"))
	case "escaped-ro":
		items = append(items, row(`<script>alert("title")</script>`, `<img src=x onerror="body">`, `<svg onload="reason">`, "/notices/?x=1&y=2", false))
	case "unsafe-links-en":
		for _, href := range []string{"/notices/", "https://example.test/notice", "http://example.test/notice", "javascript:alert(1)", "//outside.invalid/notice", "https://u:p@example.test/notice", "/\\outside.invalid", "/notice\r\nLocation: https://outside.invalid", "/bad%zz", "mailto:a@example.test", "data:text/html,bad"} {
			items = append(items, row("Generated link notice", "Content remains visible", "", href, true))
		}
	default:
		panic("unknown pure inbox fixture")
	}
	// Deliberately match Renderer defaults, not AccountView's richer map.
	data := pongo2.Context{"language": language, "display_theme": theme, "display_text": size, "display_motion": motion, "site_name": "Synthetic Activities", "csrf": "synthetic-inbox-csrf", "request": map[string]any{"csp_nonce": "synthetic-inbox-nonce"}, "user": map[string]any{"is_authenticated": true, "username": actor.Username, "display_name": actor.DisplayName, "public_id": actor.PublicID}, "items": items, "PRIVATE-extra": "PRIVATE-extra-inbox-payload"}
	return request, data
}

func TestNativeNotificationInboxProjectionAndOrdinaryControls(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	r := NewRenderer(root)
	for _, mode := range []string{"empty-en", "one-unread-ro", "mixed-en", "all-read-ro", "escaped-ro", "unsafe-links-en"} {
		t.Run(mode, func(t *testing.T) {
			request, data := notificationInboxFixture(mode)
			page, err := r.notificationInboxPage(request, data)
			if err != nil {
				t.Fatal("current producer/default shape refused", err)
			}
			rows := data["items"].([]map[string]any)
			if len(page.Rows) != len(rows) || page.PublicID != "33333333-3333-4333-8333-333333333333" {
				t.Fatal("source row order/scope projection changed")
			}
			for i, item := range page.Rows {
				if item.Title != rows[i]["title"] || item.Body != rows[i]["body"] || item.Unread != (rows[i]["read_at"] == nil) || item.When != "15 Jan 14:00" || item.ISOTime != "2026-01-15T14:00:00+02:00" || item.URL != notificationInboxSafeHref(rows[i]["url"].(string)) {
					t.Fatal("source display alias/date/spelling changed", i)
				}
				why := rows[i]["why"].(string)
				wanted := ""
				if why != "" {
					wanted = strings.ReplaceAll(html.UnescapeString(r.catalog.translate(page.Shell.Language, "Why you got this: %(why)s", 1)), "%(why)s", why)
				}
				if item.Why != wanted {
					t.Fatal("source reason or escaped catalogue substitution changed")
				}
			}
			raw, err := r.renderNotificationInbox(request, data)
			if err != nil {
				t.Fatal(err)
			}
			body := string(raw)
			for _, wanted := range []string{`body class="notification-inbox-native"`, `id="main"`, `href="/notifications/preferences/"`, `name="csrfmiddlewaretoken" value="synthetic-inbox-csrf"`, `data-meetups-owner="33333333-3333-4333-8333-333333333333"`, `href="/static/css/base.css"`, `href="/static/css/notification-inbox-native.css"`} {
				if !strings.Contains(body, wanted) {
					t.Fatal("native shell/control missing", wanted)
				}
			}
			if strings.Count(body, `data-notification-row=""`) != len(rows) || strings.Count(body, "<h1>") != 1 || strings.Count(body, `nonce="synthetic-inbox-nonce"`) != 2 {
				t.Fatal("native finite rows/heading/nonces changed")
			}
			if strings.Contains(body, `action="/notifications/read-all/"`) != (len(rows) > 0) || strings.Contains(body, `id="notification-inbox-empty"`) != (len(rows) == 0) {
				t.Fatal("all/empty ordinary action state changed")
			}
			for _, forbidden := range []string{"PRIVATE-extra-inbox-payload", "PRIVATE-unused-inbox-field", `name="recipient_id"`, `name="notification_id"`, `data-notification-id=`, `<script>alert(`, `<img src=x`, `<svg onload=`, "ZgotmplZ"} {
				if strings.Contains(body, forbidden) {
					t.Fatal("unadmitted/private/raw HTML value rendered", forbidden)
				}
			}
			if mode == "escaped-ro" && (!strings.Contains(body, "&lt;script&gt;") || !strings.Contains(body, "&lt;img") || !strings.Contains(body, "&lt;svg")) {
				t.Fatal("admitted hostile text was not escaped")
			}
			if mode == "unsafe-links-en" && strings.Count(body, `data-notification-link=""`) != 3 {
				t.Fatal("strict links suppressed content or admitted unsafe URL")
			}
			page.Copy["Notifications"] = "PRIVATE-mutated-copy"
			if len(page.Rows) > 0 {
				page.Rows[0].Title = "PRIVATE-mutated-row"
			}
			again, err := r.renderNotificationInbox(request, data)
			if err != nil || !bytes.Equal(raw, again) {
				t.Fatal("per-request copy/rows aliased renderer cache", err)
			}
		})
	}
}

func TestNativeNotificationInboxExactStrictHrefAndDates(t *testing.T) {
	r := NewRenderer(t.TempDir())
	// Test-only filter reference checks precisely the protected Pongo predicate;
	// it is not a reference rendering of the rebuilt native page.
	reference, err := r.set.FromString(`{{ value|safe_href }}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "/notice/", "/notice/?a=1&b=2", "http://example.test/x", "https://example.test/x", "http:relative", "https:///notice", "//example.test/x", "javascript:alert(1)", "data:text/html,x", "mailto:a@example.test", "#main", "https://u:p@example.test/x", "/\\example.test/x", "/x\r\nLocation: x", "/x%zz", "https://example.test/\\x"} {
		got, err := reference.Execute(pongo2.Context{"value": value})
		if err != nil || notificationInboxSafeHref(value) != html.UnescapeString(got) {
			t.Fatal("strict original href policy changed", value, err)
		}
	}
	request, data := notificationInboxFixture("one-unread-ro")
	data["items"].([]map[string]any)[0]["created_at"] = time.Date(2026, time.July, 15, 10, 0, 0, 0, time.UTC)
	page, err := r.notificationInboxPage(request, data)
	if err != nil || page.Rows[0].When != "15 Jul 13:00" || page.Rows[0].ISOTime != "2026-07-15T13:00:00+03:00" {
		t.Fatal("source Bucharest DST/time.Time display changed", err)
	}
	for _, cohort := range []string{"unassigned", "child", "teen", "adult"} {
		actor := platform.Actor{ID: 37, Username: "generated-unverified", Cohort: cohort, AgeBand: "unknown", IsActive: true}
		if _, err := r.notificationInboxPage(platform.WithActor(request, actor), data); err != nil {
			t.Fatal("renderer added age/cohort/verification authority", cohort, err)
		}
	}
}

func TestNativeNotificationInboxMalformedSnapshotsRefuseBeforeOutput(t *testing.T) {
	mutations := []struct {
		name  string
		apply func(pongo2.Context)
	}{
		{"missing-items", func(d pongo2.Context) { delete(d, "items") }},
		{"wrong-rows", func(d pongo2.Context) { d["items"] = []string{"not-a-row"} }},
		{"malformed-row", func(d pongo2.Context) { d["items"] = []any{37} }},
		{"wrong-title", func(d pongo2.Context) { d["items"].([]map[string]any)[0]["title"] = 37 }},
		{"wrong-body", func(d pongo2.Context) { d["items"].([]map[string]any)[0]["body"] = nil }},
		{"wrong-reason", func(d pongo2.Context) { d["items"].([]map[string]any)[0]["why"] = false }},
		{"wrong-link", func(d pongo2.Context) { d["items"].([]map[string]any)[0]["url"] = 37 }},
		{"invented-read-timestamp", func(d pongo2.Context) { d["items"].([]map[string]any)[0]["read_at"] = "yesterday" }},
		{"false-read-alias", func(d pongo2.Context) { d["items"].([]map[string]any)[0]["read_at"] = false }},
		{"bad-date", func(d pongo2.Context) { d["items"].([]map[string]any)[0]["created_at"] = "2026-01-15" }},
		{"zero-date", func(d pongo2.Context) { d["items"].([]map[string]any)[0]["created_at"] = time.Time{} }},
		{"more-than-source-bound", func(d pongo2.Context) {
			row := d["items"].([]map[string]any)[0]
			rows := []map[string]any{}
			for i := 0; i < 101; i++ {
				rows = append(rows, row)
			}
			d["items"] = rows
		}},
		{"bad-language", func(d pongo2.Context) { d["language"] = "xx" }},
		{"bad-theme", func(d pongo2.Context) { d["display_theme"] = "invented" }},
		{"bad-size", func(d pongo2.Context) { d["display_text"] = "invented" }},
		{"bad-motion", func(d pongo2.Context) { d["display_motion"] = "invented" }},
		{"empty-csrf", func(d pongo2.Context) { d["csrf"] = "" }},
		{"bad-request", func(d pongo2.Context) { d["request"] = "not-a-map" }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			request, data := notificationInboxFixture("one-unread-ro")
			mutation.apply(data)
			if raw, err := NewRenderer(t.TempDir()).renderNotificationInbox(request, data); err == nil || len(raw) != 0 {
				t.Fatal("malformed display snapshot escaped", mutation.name)
			}
		})
	}
	request, data := notificationInboxFixture("empty-en")
	for _, actor := range []platform.Actor{{}, {ID: 37, IsActive: false}} {
		if raw, err := NewRenderer(t.TempDir()).renderNotificationInbox(platform.WithActor(request, actor), data); err == nil || len(raw) != 0 {
			t.Fatal("missing/inactive current actor rendered")
		}
	}
}

func TestNativeNotificationInboxRendererHeadersAndStrictDispatch(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	r := NewRenderer(root)
	request, data := notificationInboxFixture("one-unread-ro")
	delete(data, "language")
	delete(data, "display_theme")
	delete(data, "display_text")
	delete(data, "display_motion")
	delete(data, "request")
	delete(data, "PRIVATE-extra")
	request.AddCookie(&http.Cookie{Name: "django_language", Value: "ro"})
	for _, enforce := range []bool{false, true} {
		r.CSPEnforce = enforce
		response := httptest.NewRecorder()
		if err := r.Render(response, request, "web/notifications.html", data); err != nil {
			t.Fatal("actual default-shell dispatch refused", err)
		}
		body := response.Body.String()
		header := "Content-Security-Policy-Report-Only"
		if enforce {
			header = "Content-Security-Policy"
		}
		if response.Code != 200 || response.Header().Get("Cache-Control") != "private, no-cache" || !strings.Contains(response.Header().Get("Vary"), "Cookie") || response.Header().Get(header) == "" || response.Header().Get("Reporting-Endpoints") == "" || !strings.Contains(body, `body class="notification-inbox-native"`) {
			t.Fatal("common headers/nonce/native dispatch changed")
		}
		if strings.Contains(response.Header().Get(header), "unsafe-inline") || strings.Count(body, `nonce="`) != 2 {
			t.Fatal("CSP bypass or invented fixed nonce")
		}
	}
	bad := httptest.NewRecorder()
	data["items"] = []any{37}
	if err := r.Render(bad, request, "web/notifications.html", data); err == nil || bad.Body.Len() != 0 {
		t.Fatal("production acquired a Pongo/malformed fallback")
	}
}
