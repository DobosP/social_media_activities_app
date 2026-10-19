package app

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

func notificationInboxAppHTML(t *testing.T, w *httptest.ResponseRecorder, count int) string {
	t.Helper()
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") ||
		w.Header().Get("Cache-Control") != "private, no-cache" || !strings.Contains(w.Header().Get("Vary"), "Cookie") ||
		w.Header().Get("X-Social-Runtime") != "go" || !strings.Contains(w.Header().Get("Content-Security-Policy-Report-Only"), "frame-ancestors 'none'") || w.Header().Get("Reporting-Endpoints") == "" {
		t.Fatal("registered inbox lost current native/private transport", w.Code)
	}
	body := w.Body.String()
	for _, part := range []string{`body class="notification-inbox-native"`, `id="main"`, `href="/notifications/preferences/"`, `href="/static/css/notification-inbox-native.css"`} {
		if !strings.Contains(body, part) {
			t.Fatal("registered inbox did not use complete native view", part)
		}
	}
	if strings.Count(body, `data-notification-row=""`) != count || strings.Count(body, `<h1`) != 1 {
		t.Fatal("registered inbox row bound or heading count changed")
	}
	if count > 0 && (!strings.Contains(body, `action="/notifications/read-all/"`) || !strings.Contains(body, `name="csrfmiddlewaretoken"`)) {
		t.Fatal("ordinary all-notices form lost existing action or CSRF")
	}
	if strings.Contains(body, `data-notification-id=`) || strings.Contains(body, `name="recipient_id"`) || strings.Contains(body, `name="notification_id"`) {
		t.Fatal("native inbox exposed a raw-ID action or recipient control")
	}
	return body
}

// Match the protected original safe_href predicate, not the weaker SPA helper.
// The captured URL is only the admitted spelling; a refused URL becomes empty.
func notificationInboxAppSafeHref(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || strings.ContainsAny(raw, "\r\n\\") ||
		!((strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//")) || parsed.Scheme == "http" || parsed.Scheme == "https") {
		return ""
	}
	return raw
}

// The ordinary TestNative... declaration registers this test in the complete
// App package. All protected HTTP requests use the assembled App and rotated
// cookies from its real password login; no WithActor or synthetic session is
// used to admit a registered HTTP request.
func TestNativeNotificationInboxRegisteredHTTPAndCurrentAuthority(t *testing.T) {
	f := newGroupNativeAppFixture(t)
	ctx := context.Background()
	// Share only the fixture's password hash; the peer logs in independently.
	if _, err := f.app.DB.Exec(ctx, `UPDATE accounts_user SET password=(SELECT password FROM accounts_user WHERE id=$1) WHERE id=$2`, f.owner.ID, f.peer.ID); err != nil {
		t.Fatal("synthetic peer password fixture unavailable")
	}
	peer := familyAppLogin(t, f.app, f.peer.Username, "synthetic-group-http-password-only")
	base := time.Date(2026, time.January, 15, 10, 0, 0, 0, time.UTC)
	title := func(i int) string {
		if i == 0 {
			return `Generated own <notice> & "quoted" 000`
		}
		return fmt.Sprintf("Generated own notice %03d", i)
	}
	urls := map[int]string{
		0: "/notifications/?from=generated&row=0", 103: "/notifications/", 102: "https://example.test/generated", 101: "http://example.test/generated",
		100: "javascript:alert(1)", 99: "//outside.invalid/notice", 98: "https://user:password@outside.invalid/notice", 97: "/\\outside.invalid/notice",
		96: "/notice\r\nLocation: https://outside.invalid", 95: "/notice%zz", 94: "mailto:outside@example.test", 93: "data:text/html,unsafe", 92: "https://outside.invalid/\\notice",
	}
	// The first-created row deliberately has the newest timestamp. Every later
	// timestamp is shared by a pair, making id DESC observable independently.
	for i := 0; i < 104; i++ {
		created := base.Add(time.Duration(i/2) * time.Second)
		if i == 0 {
			created = base.Add(2 * time.Hour)
		}
		var readAt any
		if i%3 != 0 {
			readAt = base.Add(-time.Hour)
		}
		kind := "system"
		if i == 102 {
			kind = "arrival"
		} else if i == 101 {
			kind = "moderation"
		} else if i == 100 {
			kind = "unknown_fixture"
		}
		body := fmt.Sprintf("Generated plain notice %03d", i)
		if i == 0 {
			body = `Generated <script>alert("plain text")</script> & body`
		}
		if _, err := f.app.DB.Exec(ctx, `INSERT INTO notifications_notification(recipient_id,kind,title,body,url,read_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, f.owner.ID, kind, title(i), body, urls[i], readAt, created); err != nil {
			t.Fatal("synthetic ordered own notice unavailable", i)
		}
	}
	var foreignID int64
	for i := 0; i < 3; i++ {
		if err := f.app.DB.QueryRow(ctx, `INSERT INTO notifications_notification(recipient_id,kind,title,body,url,read_at,created_at) VALUES($1,'system',$2,'PRIVATE-foreign-inbox-body','/notifications/',NULL,$3) RETURNING id`, f.peer.ID, fmt.Sprintf("PRIVATE-foreign-inbox-%d", i), base.Add(24*time.Hour)).Scan(&foreignID); err != nil {
			t.Fatal("synthetic foreign notice unavailable")
		}
	}
	if _, err := f.app.DB.Exec(ctx, `INSERT INTO notifications_notificationpreference(user_id,muted_kinds) VALUES($1,ARRAY['arrival']::text[]),($2,ARRAY['mention']::text[])`, f.owner.ID, f.peer.ID); err != nil {
		t.Fatal("independent delivery preference fixture unavailable")
	}
	state := func(t *testing.T, foreignOnly bool) string {
		t.Helper()
		var value string
		if err := f.app.DB.QueryRow(ctx, `SELECT jsonb_build_object(
			'notices',(SELECT jsonb_agg(to_jsonb(n) ORDER BY id) FROM notifications_notification n WHERE recipient_id=ANY($1::bigint[]) AND (NOT $2 OR recipient_id=$3)),
			'preferences',(SELECT jsonb_agg(to_jsonb(p) ORDER BY user_id) FROM notifications_notificationpreference p WHERE user_id=ANY($1::bigint[])),
			'audit',(SELECT count(*) FROM safety_auditlog))::text`, []int64{f.owner.ID, f.peer.ID}, foreignOnly, f.peer.ID).Scan(&value); err != nil {
			t.Fatal("bounded inbox state snapshot unavailable")
		}
		return value
	}
	unread := func(t *testing.T, actor int64) int {
		t.Helper()
		var count int
		if err := f.app.DB.QueryRow(ctx, `SELECT count(*) FROM notifications_notification WHERE recipient_id=$1 AND read_at IS NULL`, actor).Scan(&count); err != nil {
			t.Fatal("current unread count unavailable")
		}
		return count
	}
	request := func(credentials familyAppCredential, method, path, origin string, values url.Values, proof, fetch bool, language string) *httptest.ResponseRecorder {
		copied := url.Values{}
		for key, values := range values {
			copied[key] = append([]string(nil), values...)
		}
		if proof && credentials.CSRF != nil {
			copied.Set("csrfmiddlewaretoken", credentials.CSRF.Value)
		}
		r := httptest.NewRequest(method, "https://app.example"+path, strings.NewReader(copied.Encode()))
		if method != http.MethodGet && method != http.MethodHead {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if fetch {
			r.Header.Set("X-Requested-With", "fetch")
		}
		if language != "" {
			r.Header.Set("Accept-Language", language)
		}
		if credentials.Session != nil {
			r.AddCookie(credentials.Session)
		}
		if credentials.CSRF != nil {
			r.AddCookie(credentials.CSRF)
		}
		w := httptest.NewRecorder()
		f.app.ServeHTTP(w, r)
		return w
	}

	t.Run("registered-self-newest-first-100-and-read-only-get", func(t *testing.T) {
		before := state(t, false)
		path := "/notifications/?user_id=" + strconv.FormatInt(f.peer.ID, 10) + "&unread=1&limit=1&cursor=untrusted"
		body := notificationInboxAppHTML(t, request(f.credentials, http.MethodGet, path, "", nil, false, false, "en"), 100)
		titles := regexp.MustCompile(`<h2\b[^>]*\bdata-notification-title=""[^>]*>([^<]*)</h2>`).FindAllStringSubmatch(body, -1)
		if len(titles) != 100 || html.UnescapeString(titles[0][1]) != title(0) {
			t.Fatal("registered inbox did not keep source timestamp order and bound")
		}
		for i := 1; i < 100; i++ {
			if html.UnescapeString(titles[i][1]) != title(104-i) {
				t.Fatal("inbox changed equal-timestamp id order", i)
			}
		}
		for _, absent := range []string{title(1), title(2), title(3), title(4), "PRIVATE-foreign-inbox", `<script>alert("plain text")</script>`} {
			if strings.Contains(body, absent) {
				t.Fatal("query forwarded, foreign/old row leaked, or plain text became markup")
			}
		}
		cards := regexp.MustCompile(`(?s)<article\b[^>]*\bdata-notification-row=""[^>]*>.*?</article>`).FindAllString(body, -1)
		if len(cards) != 100 || !strings.Contains(cards[0], `data-notification-unread="true"`) ||
			!strings.Contains(cards[0], `datetime="2026-01-15T14:00:00+02:00"`) || !strings.Contains(cards[0], "15 Jan 14:00") ||
			!strings.Contains(html.UnescapeString(cards[0]), `Generated <script>alert("plain text")</script> & body`) {
			t.Fatal("source read-presence/date/plain-body aliases changed")
		}
		if !strings.Contains(cards[1], `data-notification-unread="false"`) ||
			!strings.Contains(cards[2], "Someone arrived at or is on their way to a meetup") || !strings.Contains(cards[3], "A moderation decision affected your content or account") ||
			strings.Contains(cards[4], `data-notification-why=""`) {
			t.Fatal("source read/reason/unknown-kind presentation changed")
		}
		for i, card := range cards {
			index := 0
			if i > 0 {
				index = 104 - i
			}
			matches := regexp.MustCompile(`<a\b[^>]*\bdata-notification-link=""[^>]*>`).FindAllString(card, -1)
			admitted := notificationInboxAppSafeHref(urls[index])
			if admitted == "" {
				if len(matches) != 0 {
					t.Fatal("strict unsafe URL acquired a view link", index)
				}
			} else if len(matches) != 1 || !strings.Contains(matches[0], `href="`+html.EscapeString(admitted)+`"`) {
				t.Fatal("admitted local/http/https spelling changed", index)
			}
		}
		notificationInboxAppHTML(t, request(peer, http.MethodGet, "/notifications/?recipient_id="+strconv.FormatInt(f.owner.ID, 10), "", nil, false, false, "en"), 3)
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			asset := request(familyAppCredential{}, method, "/static/css/notification-inbox-native.css", "", nil, false, false, "")
			if asset.Code != http.StatusOK || !strings.HasPrefix(asset.Header().Get("Content-Type"), "text/css") || asset.Header().Get("Cache-Control") != "public, max-age=3600" ||
				asset.Header().Get("X-Content-Type-Options") != "nosniff" || asset.Header().Get("X-Social-Runtime") != "go" || asset.Header().Get("ETag") == "" || len(asset.Result().Cookies()) != 0 ||
				method == http.MethodGet && asset.Body.Len() == 0 || method == http.MethodHead && asset.Body.Len() != 0 {
				t.Fatal("new scoped CSS lost real public App GET/HEAD delivery", method, asset.Code)
			}
		}
		if state(t, false) != before || unread(t, f.owner.ID) != 35 || unread(t, f.peer.ID) != 3 {
			t.Fatal("registered GET marked, muted, deleted or changed a notification")
		}
	})
	t.Run("csrf-origin-head-and-existing-method-admission", func(t *testing.T) {
		before := state(t, false)
		path := "/notifications/?unread=1&next=%2Fgenerated%2F"
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			w := request(familyAppCredential{}, method, path, "", nil, false, false, "")
			if w.Code != http.StatusFound || w.Header().Get("Location") != "/login/?next="+url.QueryEscape(path) || strings.Contains(w.Body.String(), "data-notification-row") {
				t.Fatal("anonymous inbox admission/next changed", method, w.Code)
			}
		}
		guest := request(familyAppCredential{}, http.MethodPost, "/notifications/read-all/", "https://app.example", nil, false, false, "")
		if guest.Code != http.StatusFound || guest.Header().Get("Location") != "/login/" {
			t.Fatal("anonymous mark-all redirect changed", guest.Code)
		}
		for _, probe := range []struct {
			proof         bool
			origin, token string
		}{
			{false, "https://app.example", ""}, {false, "https://app.example", "incorrect"}, {true, "https://outside.invalid", ""}, {true, "", ""},
		} {
			w := request(f.credentials, http.MethodPost, "/notifications/read-all/", probe.origin, url.Values{"csrfmiddlewaretoken": {probe.token}}, probe.proof, false, "")
			if w.Code != http.StatusForbidden || state(t, false) != before {
				t.Fatal("missing/bad CSRF or foreign Origin changed notices", w.Code)
			}
		}
		// Direct-handler HEAD follows GET and writes into httptest. This does not
		// claim a network HEAD body; net/http owns wire-level body suppression.
		notificationInboxAppHTML(t, request(f.credentials, http.MethodHead, "/notifications/", "", nil, false, false, "en"), 100)
		for _, probe := range []struct{ method, path, allow string }{
			{http.MethodPost, "/notifications/", "GET"}, {http.MethodGet, "/notifications/read-all/", "POST"}, {http.MethodHead, "/notifications/read-all/", "POST"},
		} {
			w := request(f.credentials, probe.method, probe.path, "https://app.example", nil, true, false, "")
			if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != probe.allow || state(t, false) != before {
				t.Fatal("existing typed-router method/Allow contract changed", probe.method, probe.path, w.Code)
			}
		}
	})
	t.Run("mark-all-beyond-100-self-only-idempotent-and-fetch", func(t *testing.T) {
		beforeForeign := state(t, true)
		values := url.Values{"recipient_id": {strconv.FormatInt(f.peer.ID, 10)}, "user_id": {strconv.FormatInt(f.peer.ID, 10)}, "id": {strconv.FormatInt(foreignID, 10)}}
		path := "/notifications/read-all/?recipient_id=" + strconv.FormatInt(f.peer.ID, 10)
		w := request(f.credentials, http.MethodPost, path, "https://app.example", values, true, true, "")
		var result map[string]any
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || json.Unmarshal(w.Body.Bytes(), &result) != nil ||
			len(result) != 2 || result["ok"] != true || result["marked_read"] != float64(35) || unread(t, f.owner.ID) != 0 || unread(t, f.peer.ID) != 3 || state(t, true) != beforeForeign {
			t.Fatal("actual all-notices mutation lost self scope/count/fetch response", w.Code)
		}
		var omittedRead bool
		if err := f.app.DB.QueryRow(ctx, `SELECT read_at IS NOT NULL FROM notifications_notification WHERE recipient_id=$1 AND title=$2`, f.owner.ID, title(3)).Scan(&omittedRead); err != nil || !omittedRead {
			t.Fatal("mark all stopped at the displayed hundred rows")
		}
		after := state(t, false)
		w = request(f.credentials, http.MethodPost, "/notifications/read-all/", "https://app.example", nil, true, false, "")
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/notifications/" || state(t, false) != after {
			t.Fatal("ordinary idempotent mark-all changed redirect or stored timestamps", w.Code)
		}
		w = request(f.credentials, http.MethodPost, "/notifications/read-all/", "https://app.example", nil, true, true, "")
		result = nil
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil || result["marked_read"] != float64(0) || result["ok"] != true || len(result) != 2 || state(t, false) != after {
			t.Fatal("second mark-all was not idempotent")
		}
		body := notificationInboxAppHTML(t, request(f.credentials, http.MethodGet, "/notifications/", "", nil, false, false, "ro"), 100)
		if strings.Contains(body, `data-notification-unread="true"`) || !strings.Contains(body, `action="/notifications/read-all/"`) {
			t.Fatal("all-read inbox lost the ordinary mark-all form or read state")
		}
	})
	t.Run("current-active-account-and-existing-cohort-admission", func(t *testing.T) {
		before := state(t, false)
		defer func() {
			if _, err := f.app.DB.Exec(ctx, `UPDATE accounts_user SET is_active=true,is_identity_verified=true,cohort='adult',age_band='adult',role='user',is_staff=false,is_superuser=false WHERE id=$1`, f.owner.ID); err != nil {
				t.Error("restore synthetic account admission fields")
			}
		}()
		for _, probe := range []struct {
			cohort, age, role string
			verified, staff   bool
		}{
			{"unassigned", "adult", "user", false, false}, {"teen", "16_17", "user", false, false}, {"child", "under_16", "user", false, false}, {"adult", "adult", "moderator", true, true},
		} {
			if _, err := f.app.DB.Exec(ctx, `UPDATE accounts_user SET cohort=$2,age_band=$3,role=$4,is_identity_verified=$5,is_staff=$6 WHERE id=$1`, f.owner.ID, probe.cohort, probe.age, probe.role, probe.verified, probe.staff); err != nil {
				t.Fatal("synthetic current-account role fixture unavailable")
			}
			notificationInboxAppHTML(t, request(f.credentials, http.MethodGet, "/notifications/", "", nil, false, false, "en"), 100)
			w := request(f.credentials, http.MethodPost, "/notifications/read-all/", "https://app.example", nil, true, false, "")
			if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/notifications/" || state(t, false) != before {
				t.Fatal("inbox added a role/assurance/participation prerequisite", probe.cohort, w.Code)
			}
		}
		if _, err := f.app.DB.Exec(ctx, `UPDATE accounts_user SET is_active=false WHERE id=$1`, f.owner.ID); err != nil {
			t.Fatal("synthetic inactive fixture unavailable")
		}
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			path := "/notifications/"
			if method == http.MethodPost {
				path = "/notifications/read-all/"
			}
			w := request(f.credentials, method, path, "https://app.example", nil, true, false, "")
			if w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), "/login/") || strings.Contains(w.Body.String(), "data-notification-row") || state(t, false) != before {
				t.Fatal("inactive account kept current inbox read/write authority", method, w.Code)
			}
		}
		var expires time.Time
		if err := f.app.DB.QueryRow(ctx, `SELECT expires_at FROM accounts_go_session WHERE user_id=$1`, f.peer.ID).Scan(&expires); err != nil {
			t.Fatal("current synthetic peer session unavailable")
		}
		if _, err := f.app.DB.Exec(ctx, `UPDATE accounts_go_session SET expires_at=now()-interval '1 hour' WHERE user_id=$1`, f.peer.ID); err != nil {
			t.Fatal("expire synthetic peer session")
		}
		defer func() {
			if _, err := f.app.DB.Exec(ctx, `UPDATE accounts_go_session SET expires_at=$2 WHERE user_id=$1`, f.peer.ID, expires); err != nil {
				t.Error("restore synthetic peer expiry")
			}
		}()
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			path := "/notifications/"
			if method == http.MethodPost {
				path = "/notifications/read-all/"
			}
			w := request(peer, method, path, "https://app.example", nil, true, false, "")
			if w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), "/login/") || strings.Contains(w.Body.String(), "PRIVATE-foreign-inbox") || state(t, false) != before {
				t.Fatal("expired real cookie kept inbox authority", method, w.Code)
			}
		}
	})
	t.Run("upstream-failure-preserves-state-and-response", func(t *testing.T) {
		if _, err := f.app.DB.Exec(ctx, `INSERT INTO notifications_notification(recipient_id,kind,title,body,url,read_at,created_at) VALUES($1,'system','Generated failure-control notice','Generated plain failure control','/notifications/',NULL,$2)`, f.owner.ID, base.Add(3*time.Hour)); err != nil {
			t.Fatal("synthetic unread failure fixture unavailable")
		}
		var schema string
		if err := f.app.DB.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
			t.Fatal("fixture schema unavailable")
		}
		function := pgx.Identifier{schema, "native_inbox_update_refusal"}.Sanitize()
		table := pgx.Identifier{schema, "notifications_notification"}.Sanitize()
		if _, err := f.app.DB.Exec(ctx, `CREATE FUNCTION `+function+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'PRIVATE-synthetic-inbox-update-refusal'; END; $$; CREATE TRIGGER native_inbox_update_refusal BEFORE UPDATE ON `+table+` FOR EACH ROW EXECUTE FUNCTION `+function+`()`); err != nil {
			t.Fatal("synthetic update refusal unavailable")
		}
		before := state(t, false)
		w := request(f.credentials, http.MethodPost, "/notifications/read-all/", "https://app.example", nil, true, false, "")
		if _, err := f.app.DB.Exec(ctx, `DROP TRIGGER native_inbox_update_refusal ON `+table+`; DROP FUNCTION `+function+`()`); err != nil {
			t.Fatal("remove own update refusal")
		}
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"detail":"Service unavailable."`) || strings.Contains(w.Body.String(), "PRIVATE-synthetic") || strings.Contains(w.Body.String(), `"ok":true`) || w.Header().Get("Location") != "" || state(t, false) != before {
			t.Fatal("actual SQL failure changed notices or emitted false success/private detail", w.Code)
		}
		original := f.app.Web.API
		defer func() { f.app.Web.API = original }()
		forwarded := 0
		f.app.Web.API = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == "/api/notifications/read-all/" {
				actor, ok := platform.ActorFrom(r)
				if !ok || actor.ID != f.owner.ID || !actor.IsActive {
					t.Fatal("upstream refusal lost current admitted actor")
				}
				forwarded++
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "17")
				w.Header().Set("X-Synthetic-Inbox-Refusal", "retained")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"detail":"Generated upstream refusal."}`))
				return
			}
			original.ServeHTTP(w, r)
		})
		for _, fetch := range []bool{false, true} {
			w := request(f.credentials, http.MethodPost, "/notifications/read-all/", "https://app.example", nil, true, fetch, "")
			if w.Code != http.StatusServiceUnavailable || w.Body.String() != `{"detail":"Generated upstream refusal."}` ||
				w.Header().Get("Retry-After") != "17" || w.Header().Get("X-Synthetic-Inbox-Refusal") != "retained" || w.Header().Get("Location") != "" || state(t, false) != before {
				t.Fatal("existing action did not propagate source failure body/headers without writes", fetch, w.Code)
			}
		}
		if forwarded != 2 {
			t.Fatal("source action failure was not forwarded exactly once per request")
		}
	})
	var variants []map[string]any
	t.Run("registered-display-whitelist-and-existing-spa-source", func(t *testing.T) {
		before := state(t, false)
		originalAPI, originalSPA := f.app.Web.API, f.app.Web.Config.SPA
		defer func() { f.app.Web.API = originalAPI; f.app.Web.Config.SPA = originalSPA }()
		for _, language := range []string{"en", "ro"} {
			var projected []map[string]any
			forwarded := 0
			// Intercept only the existing API callback inside THIS admitted GET.
			// Forward once, retain only its finite display whitelist and write the
			// original response unchanged. No second private fetch is performed.
			f.app.Web.API = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/notifications/" {
					originalAPI.ServeHTTP(w, r)
					return
				}
				actor, ok := platform.ActorFrom(r)
				if !ok || actor.ID != f.owner.ID || !actor.IsActive || r.URL.RawQuery != "" {
					t.Fatal("capture lost admitted self/unversioned exact GET")
				}
				forwarded++
				actual := httptest.NewRecorder()
				originalAPI.ServeHTTP(actual, r)
				var value struct {
					Results []struct {
						Title   string `json:"title"`
						Body    string `json:"body"`
						Reason  string `json:"reason"`
						URL     string `json:"url"`
						Read    bool   `json:"is_read"`
						Created string `json:"created_at"`
					} `json:"results"`
				}
				if actual.Code != http.StatusOK || json.Unmarshal(actual.Body.Bytes(), &value) != nil || len(value.Results) != 100 {
					t.Fatal("same-call authorized API display source unavailable")
				}
				for _, row := range value.Results {
					if _, err := time.Parse(time.RFC3339Nano, row.Created); err != nil || strings.Contains(row.Title, "PRIVATE-foreign") {
						t.Fatal("capture contains malformed timestamp or another recipient")
					}
					var readAt any
					if row.Read {
						readAt = true
					}
					projected = append(projected, map[string]any{"title": row.Title, "body": row.Body, "why": row.Reason, "url": notificationInboxAppSafeHref(row.URL), "created_at": row.Created, "read_at": readAt})
				}
				for key, values := range actual.Header() {
					w.Header()[key] = append([]string(nil), values...)
				}
				w.WriteHeader(actual.Code)
				_, _ = w.Write(actual.Body.Bytes())
			})
			body := notificationInboxAppHTML(t, request(f.credentials, http.MethodGet, "/notifications/", "", nil, false, false, language), 100)
			if forwarded != 1 || len(projected) != 100 || !strings.Contains(body, "Generated failure-control notice") || strings.Contains(body, "PRIVATE-foreign-inbox") || state(t, false) != before {
				t.Fatal("successful registered capture was not one read-only admitted source GET")
			}
			variants = append(variants, map[string]any{"language": language, "projection": map[string]any{"items": projected}})
		}
		f.app.Web.API = originalAPI
		f.app.Web.Config.SPA = true
		w := request(f.credentials, http.MethodGet, "/notifications/?_data=1&unread=1&limit=1", "", nil, false, false, "en")
		var payload struct {
			Route string `json:"route"`
			Data  struct {
				Items []map[string]any `json:"items"`
			} `json:"data"`
		}
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || json.Unmarshal(w.Body.Bytes(), &payload) != nil || payload.Route != "notifications" || len(payload.Data.Items) != 100 {
			t.Fatal("existing SPA data branch/HTML query non-forwarding changed", w.Code)
		}
		first := payload.Data.Items[0]
		if len(first) != 6 || first["title"] != "Generated failure-control notice" || first["body"] != "Generated plain failure control" || first["unread"] != true || first["url"] != "/notifications/" || first["when"] == nil || first["why"] == nil {
			t.Fatal("existing SPA source notification fields changed")
		}
		// _data does not convert ordinary form POST to JSON; only the existing
		// explicit fetch header requests that mutation response.
		w = request(f.credentials, http.MethodPost, "/notifications/read-all/?_data=1", "https://app.example", nil, true, false, "")
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/notifications/" || unread(t, f.owner.ID) != 0 || unread(t, f.peer.ID) != 3 {
			t.Fatal("SPA query changed ordinary mark-all action semantics", w.Code)
		}
		if !reflect.DeepEqual(variants[0]["projection"], variants[1]["projection"]) {
			t.Fatal("dynamic notice source was silently translated between capture languages")
		}
	})
	if t.Failed() {
		return
	}
	payload, err := json.Marshal(map[string]any{"schema": 1, "variants": variants, "capture": "same registered-cookie GET callback; display whitelist only; no actor/IDs/credentials/render environment"})
	if err != nil || len(variants) != 2 || len(payload) > 256<<10 {
		t.Fatal("bounded successful inbox projection marker unavailable")
	}
	t.Log("NATIVE_NOTIFICATION_INBOX_V1 " + string(payload))
}
