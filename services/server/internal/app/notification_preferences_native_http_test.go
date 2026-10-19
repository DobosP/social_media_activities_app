package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/DobosP/social_media_activities_app/services/server/internal/web"
	"github.com/jackc/pgx/v5"
)

// This is the existing account-page source order, not a new delivery policy.
var notificationNativeHTTPOrder = []string{
	"join_requested", "join_approved", "event_reminder", "activity_cancelled",
	"activity_updated", "announcement", "group_announcement", "meetup_confirmed",
	"arrival", "connection_request", "connection_accepted", "mention",
	"organizer_role", "activity_match", "gauge_match", "group_question",
	"interest_converted", "rsvp_nudge", "organizer_prep", "supervisor_needed",
	"formative_note", "mod_alert",
}

// Public static transport remains independently checkable without a database
// or another password/whole-App fixture.
func TestNativeNotificationPreferencesStaticTransportWithoutDatabase(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := web.StaticHandler(filepath.Join(root, "static"))
	if err != nil {
		t.Fatal(err)
	}
	a := &App{Config: Config{AllowedHosts: []string{"social.fixture.test"}}, Static: handler}
	const expected = ".notification-preferences-page .btn { transition: transform .04s ease; }\n:root .notification-preferences-page .site-header { background: var(--card); }\n"
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest(method, "https://social.fixture.test/static/css/notification-preferences-native.css", nil))
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/css") || w.Header().Get("Cache-Control") != "public, max-age=3600" ||
			w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("X-Social-Runtime") != "go" || w.Header().Get("ETag") == "" || len(w.Result().Cookies()) != 0 {
			t.Fatal("scoped notification CSS lost actual public App transport", method, w.Code)
		}
		if method == http.MethodGet && w.Body.String() != expected || method == http.MethodHead && w.Body.Len() != 0 {
			t.Fatal("notification CSS GET/HEAD body differs", method)
		}
	}
}

func notificationNativeHTTPHTML(t *testing.T, response *httptest.ResponseRecorder, selected []string) string {
	t.Helper()
	if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/html") ||
		response.Header().Get("Cache-Control") != "private, no-cache" ||
		!strings.Contains(response.Header().Get("Vary"), "Cookie") || response.Header().Get("X-Social-Runtime") != "go" ||
		!strings.Contains(response.Header().Get("Content-Security-Policy-Report-Only"), "frame-ancestors 'none'") {
		t.Fatal("registered notification preferences lost native/private response", response.Code)
	}
	body := response.Body.String()
	for _, part := range []string{`notification-preferences-page`, `id="main"`,
		`action="/notifications/preferences/"`, `notification-preferences-form`,
		`name="csrfmiddlewaretoken"`, `id="notification-arrival-warning"`} {
		if !strings.Contains(body, part) {
			t.Fatal("native notification form lost its finite contract", part)
		}
	}
	inputs := regexp.MustCompile(`<input\b[^>]*\bid="notification-kind-([a-z_]+)"[^>]*>`).FindAllStringSubmatch(body, -1)
	if len(inputs) != len(notificationNativeHTTPOrder) {
		t.Fatal("optional notification row count differs", len(inputs))
	}
	muted := map[string]bool{}
	for _, value := range selected {
		muted[value] = true
	}
	for i, input := range inputs {
		if input[1] != notificationNativeHTTPOrder[i] || !strings.Contains(input[0], `type="checkbox"`) ||
			!strings.Contains(input[0], `name="muted"`) || !strings.Contains(input[0], `value="`+input[1]+`"`) ||
			regexp.MustCompile(`\schecked(?:\s|=|>)`).MatchString(input[0]) != muted[input[1]] {
			t.Fatal("ordinary checked-means-mute row/order changed", input[1])
		}
	}
	for _, bad := range []string{`name="muted[]"`, `id="notification-kind-system"`, `id="notification-kind-moderation"`} {
		if strings.Contains(body, bad) {
			t.Fatal("mandatory kind or bracketed form field entered optional controls")
		}
	}
	return body
}

func TestNativeNotificationPreferencesRegisteredHTTPAndDelivery(t *testing.T) {
	f := newGroupNativeAppFixture(t)
	ctx := context.Background()
	// Reuse the fixture's one password hash. The peer still obtains its own real
	// login/rotated cookies; no synthetic session or WithActor authenticates HTTP.
	if _, err := f.app.DB.Exec(ctx, `UPDATE accounts_user SET password=(SELECT password FROM accounts_user WHERE id=$1) WHERE id=$2`, f.owner.ID, f.peer.ID); err != nil {
		t.Fatal("share synthetic fixture password hash")
	}
	peer := familyAppLogin(t, f.app, f.peer.Username, "synthetic-group-http-password-only")
	ward := testdb.Actor(t, f.app.DB, "generated-native-notification-ward", "child")
	if _, err := f.app.DB.Exec(ctx, `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,relationship,status,consent_id,created_at,updated_at) VALUES($1,$2,'parent','active',NULL,now(),now())`, f.owner.ID, ward.ID); err != nil {
		t.Fatal("synthetic existing guardian relationship unavailable")
	}
	if result, err := f.app.DB.Exec(ctx, `UPDATE accounts_parentalconsent SET guardian_identifier=$2 WHERE minor_id=$1`, ward.ID, f.owner.PublicID); err != nil || result.RowsAffected() != 1 {
		t.Fatal("synthetic existing parental-consent fixture unavailable")
	}
	if _, err := f.app.DB.Exec(ctx, `INSERT INTO notifications_notificationpreference(user_id,muted_kinds) VALUES($1,ARRAY['arrival']::text[]),($2,ARRAY['event_reminder']::text[]),($3,ARRAY['mention']::text[])`, f.owner.ID, f.peer.ID, ward.ID); err != nil {
		t.Fatal("initial own/other/ward preferences unavailable")
	}
	if _, err := f.app.DB.Exec(ctx, `INSERT INTO places_accesspreference(user_id,needs_step_free,needs_accessible_toilet,needs_hearing_loop,prefers_quiet,created_at,updated_at) VALUES($1,true,false,false,false,now(),now()),($2,false,false,true,false,now(),now()),($3,false,true,false,true,now(),now())`, f.owner.ID, f.peer.ID, ward.ID); err != nil {
		t.Fatal("independent access preferences unavailable")
	}
	ids := []int64{f.owner.ID, f.peer.ID, ward.ID}
	snapshot := func(t *testing.T, foreignOnly bool) string {
		t.Helper()
		var value string
		err := f.app.DB.QueryRow(ctx, `SELECT jsonb_build_object(
			'preferences',(SELECT jsonb_agg(to_jsonb(p) ORDER BY user_id) FROM notifications_notificationpreference p WHERE user_id=ANY($1::bigint[]) AND (NOT $2 OR user_id<>$3)),
			'access',(SELECT jsonb_agg(to_jsonb(p) ORDER BY user_id) FROM places_accesspreference p WHERE user_id=ANY($1::bigint[])),
			'relationships',(SELECT jsonb_agg(to_jsonb(g) ORDER BY id) FROM accounts_guardianrelationship g WHERE guardian_id=$3 OR ward_id=$4),
			'consents',(SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM accounts_parentalconsent c WHERE minor_id=$4),
			'audit',(CASE WHEN $2 THEN NULL ELSE (SELECT count(*) FROM safety_auditlog WHERE event='notification.preferences_updated') END))::text`, ids, foreignOnly, f.owner.ID, ward.ID).Scan(&value)
		if err != nil {
			t.Fatal("notification scope snapshot unavailable")
		}
		return value
	}
	kinds := func(t *testing.T, user int64) []string {
		t.Helper()
		var result []string
		if err := f.app.DB.QueryRow(ctx, `SELECT muted_kinds FROM notifications_notificationpreference WHERE user_id=$1`, user).Scan(&result); err != nil {
			t.Fatal("stored notification preferences unavailable")
		}
		return result
	}
	audits := func(t *testing.T, user int64) int {
		t.Helper()
		var count int
		if err := f.app.DB.QueryRow(ctx, `SELECT count(*) FROM safety_auditlog WHERE event='notification.preferences_updated' AND actor_id=$1`, user).Scan(&count); err != nil {
			t.Fatal("settings audit count unavailable")
		}
		return count
	}
	post := func(t *testing.T, credentials familyAppCredential, path string, values url.Values) *httptest.ResponseRecorder {
		t.Helper()
		response := familyAppRequest(t, f.app, credentials, http.MethodPost, path, "https://app.example", values, true)
		if response.Code != http.StatusFound || response.Header().Get("Location") != "/notifications/preferences/" {
			t.Fatal("self settings save lost existing redirect", response.Code)
		}
		return response
	}
	t.Run("registered-get-is-self-scoped-and-side-effect-free", func(t *testing.T) {
		before := snapshot(t, false)
		body := notificationNativeHTTPHTML(t, familyAppRequest(t, f.app, f.credentials, http.MethodGet, "/notifications/preferences/?user_id="+strconv.FormatInt(f.peer.ID, 10), "", nil, false), []string{"arrival"})
		if !strings.Contains(strings.Join(strings.Fields(body), " "), "Note: muting this also turns off arrival alerts for a child you supervise.") {
			t.Fatal("current parent arrival-warning copy disappeared")
		}
		notificationNativeHTTPHTML(t, familyAppRequest(t, f.app, peer, http.MethodGet, "/notifications/preferences/?user_id="+strconv.FormatInt(f.owner.ID, 10), "", nil, false), []string{"event_reminder"})
		guest := familyAppRequest(t, f.app, familyAppCredential{}, http.MethodGet, "/notifications/preferences/", "", nil, false)
		if guest.Code != http.StatusFound || !strings.HasPrefix(guest.Header().Get("Location"), "/login/?next=") || strings.Contains(guest.Body.String(), "notification-kind-arrival") || snapshot(t, false) != before {
			t.Fatal("anonymous/query scope or read-only preferences boundary changed")
		}
		wantCSS := ".notification-preferences-page .btn { transition: transform .04s ease; }\n:root .notification-preferences-page .site-header { background: var(--card); }\n"
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			style := familyAppRequest(t, f.app, familyAppCredential{}, method, "/static/css/notification-preferences-native.css", "", nil, false)
			if style.Code != http.StatusOK || !strings.HasPrefix(style.Header().Get("Content-Type"), "text/css") || style.Header().Get("Cache-Control") != "public, max-age=3600" ||
				style.Header().Get("X-Content-Type-Options") != "nosniff" || style.Header().Get("X-Social-Runtime") != "go" ||
				method == http.MethodGet && style.Body.String() != wantCSS || method == http.MethodHead && style.Body.Len() != 0 || !strings.Contains(body, `href="/static/css/notification-preferences-native.css"`) {
				t.Fatal("native notification stylesheet actual public GET/HEAD contract changed", method, style.Code)
			}
		}
	})
	t.Run("csrf-origin-and-current-unverified-account-policy", func(t *testing.T) {
		before := snapshot(t, false)
		for _, control := range []struct {
			proof  bool
			origin string
		}{{false, "https://app.example"}, {true, "https://outside.invalid"}} {
			w := familyAppRequest(t, f.app, f.credentials, http.MethodPost, "/notifications/preferences/", control.origin, url.Values{"muted": {"mention"}}, control.proof)
			if w.Code != http.StatusForbidden || snapshot(t, false) != before {
				t.Fatal("CSRF/Origin refusal changed persisted preferences/access/guardian/audit state")
			}
		}
		if _, err := f.app.DB.Exec(ctx, `UPDATE accounts_user SET is_identity_verified=false,cohort='unassigned' WHERE id=$1`, f.owner.ID); err != nil {
			t.Fatal("current pending-account fixture unavailable")
		}
		t.Cleanup(func() {
			if _, err := f.app.DB.Exec(ctx, `UPDATE accounts_user SET is_identity_verified=true,cohort='adult' WHERE id=$1`, f.owner.ID); err != nil {
				t.Error("restore synthetic account projection")
			}
		})
		notificationNativeHTTPHTML(t, familyAppRequest(t, f.app, f.credentials, http.MethodGet, "/notifications/preferences/", "", nil, false), []string{"arrival"})
		// Current Settings admission does not require participation/verification.
		// Prove the native view did not add that gate to a valid existing save.
		post(t, f.credentials, "/notifications/preferences/", url.Values{"muted": {"arrival"}})
		if !reflect.DeepEqual(kinds(t, f.owner.ID), []string{"arrival"}) {
			t.Fatal("existing pending-account settings admission changed")
		}
	})
	t.Run("repeated-form-filter-sort-and-own-vs-other-boundary", func(t *testing.T) {
		beforeForeign, beforeAudit := snapshot(t, true), audits(t, f.owner.ID)
		post(t, f.credentials, "/notifications/preferences/?user_id="+strconv.FormatInt(f.peer.ID, 10), url.Values{
			"muted":   {"mention", "arrival", "mention", "system", "moderation", "UNKNOWN"},
			"user_id": {strconv.FormatInt(f.peer.ID, 10)}, "needs_step_free": {"false"},
		})
		if !reflect.DeepEqual(kinds(t, f.owner.ID), []string{"arrival", "mention"}) || snapshot(t, true) != beforeForeign || audits(t, f.owner.ID) != beforeAudit+1 {
			t.Fatal("existing filtering/sorted dedup/self scope/access/guardian/audit changed")
		}
		var target, data string
		if err := f.app.DB.QueryRow(ctx, `SELECT target_ref,(data->'muted_kinds')::text FROM safety_auditlog WHERE event='notification.preferences_updated' AND actor_id=$1 ORDER BY id DESC LIMIT 1`, f.owner.ID).Scan(&target, &data); err != nil {
			t.Fatal("committed preference audit missing")
		}
		var recorded []string
		if json.Unmarshal([]byte(data), &recorded) != nil || !reflect.DeepEqual(recorded, []string{"arrival", "mention"}) || target != "accounts.user:"+strconv.FormatInt(f.owner.ID, 10) {
			t.Fatal("settings audit target/payload did not match actual filtered save")
		}
		ownerBefore := kinds(t, f.owner.ID)
		post(t, peer, "/notifications/preferences/?user_id="+strconv.FormatInt(f.owner.ID, 10), url.Values{"muted": {"group_announcement"}, "user_id": {strconv.FormatInt(f.owner.ID, 10)}})
		if !reflect.DeepEqual(kinds(t, f.peer.ID), []string{"group_announcement"}) || !reflect.DeepEqual(kinds(t, f.owner.ID), ownerBefore) || !reflect.DeepEqual(kinds(t, ward.ID), []string{"mention"}) {
			t.Fatal("a peer save escaped its current-account scope")
		}
	})
	t.Run("mandatory-audit-refusal-rolls-back-and-renders-current-checks", func(t *testing.T) {
		var schema string
		if err := f.app.DB.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
			t.Fatal("fixture schema unavailable")
		}
		function := pgx.Identifier{schema, "native_notification_audit_refusal"}.Sanitize()
		table := pgx.Identifier{schema, "safety_auditlog"}.Sanitize()
		if _, err := f.app.DB.Exec(ctx, `CREATE FUNCTION `+function+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event='notification.preferences_updated' THEN RAISE EXCEPTION 'synthetic notification audit refused'; END IF; RETURN NEW; END; $$; CREATE TRIGGER native_notification_audit_refusal BEFORE INSERT ON `+table+` FOR EACH ROW EXECUTE FUNCTION `+function+`()`); err != nil {
			t.Fatal("synthetic mandatory-audit refusal unavailable")
		}
		t.Cleanup(func() {
			if _, err := f.app.DB.Exec(ctx, `DROP TRIGGER native_notification_audit_refusal ON `+table+`; DROP FUNCTION `+function+`()`); err != nil {
				t.Error("remove own audit-refusal fixture")
			}
		})
		before := snapshot(t, false)
		w := familyAppRequest(t, f.app, f.credentials, http.MethodPost, "/notifications/preferences/", "https://app.example", url.Values{"muted": {"event_reminder"}}, true)
		body := notificationNativeHTTPHTML(t, w, []string{"arrival", "mention"})
		if !strings.Contains(body, `role="alert"`) || !strings.Contains(body, "This action is temporarily unavailable. Please try again.") ||
			strings.Contains(body, "synthetic notification audit refused") || snapshot(t, false) != before {
			t.Fatal("failed mandatory audit changed settings/audit or exposed a false selection/private error")
		}
	})
	t.Run("actual-delivery-mandatory-bypass-and-absent-selection-clears", func(t *testing.T) {
		// A valid stored array may contain old/imported mandatory entries. This
		// causal fixture detects removal of Notify's mandatory-kind exception.
		if _, err := f.app.DB.Exec(ctx, `UPDATE notifications_notificationpreference SET muted_kinds=ARRAY['arrival','mention','system','moderation']::text[] WHERE user_id=$1`, f.owner.ID); err != nil {
			t.Fatal("legacy mute-array fixture unavailable")
		}
		inserted := map[string]bool{}
		if err := platform.Transaction(ctx, f.app.DB, func(tx pgx.Tx) error {
			for _, kind := range []string{"arrival", "moderation", "system"} {
				ok, err := platform.Notify(ctx, tx, f.owner.ID, kind, "Generated notification delivery", "Generated plain notice", "/notifications/")
				if err != nil {
					return err
				}
				inserted[kind] = ok
			}
			return nil
		}); err != nil {
			t.Fatal("actual notification transaction failed")
		}
		if inserted["arrival"] || !inserted["moderation"] || !inserted["system"] {
			t.Fatal("optional mute or mandatory notification delivery changed")
		}
		var delivered []string
		rows, err := f.app.DB.Query(ctx, `SELECT kind FROM notifications_notification WHERE recipient_id=$1 AND title='Generated notification delivery' ORDER BY kind`, f.owner.ID)
		if err != nil {
			t.Fatal("delivered notices unavailable")
		}
		for rows.Next() {
			var kind string
			if err := rows.Scan(&kind); err != nil {
				rows.Close()
				t.Fatal("notice kind unavailable")
			}
			delivered = append(delivered, kind)
		}
		err = rows.Err()
		rows.Close()
		if err != nil || !reflect.DeepEqual(delivered, []string{"moderation", "system"}) {
			t.Fatal("actual mandatory notices missing or muted optional notice inserted")
		}
		beforeForeign, beforeAudit := snapshot(t, true), audits(t, f.owner.ID)
		post(t, f.credentials, "/notifications/preferences/", url.Values{})
		if len(kinds(t, f.owner.ID)) != 0 || snapshot(t, true) != beforeForeign || audits(t, f.owner.ID) != beforeAudit+1 {
			t.Fatal("absent repeated field did not clear only own optional mutes with audit")
		}
		notificationNativeHTTPHTML(t, familyAppRequest(t, f.app, f.credentials, http.MethodGet, "/notifications/preferences/", "", nil, false), nil)
	})
	t.Run("existing-spa-data-branch-and-mutation-return-path", func(t *testing.T) {
		original := f.app.Web.Config.SPA
		f.app.Web.Config.SPA = true
		t.Cleanup(func() { f.app.Web.Config.SPA = original })
		w := familyAppRequest(t, f.app, f.credentials, http.MethodGet, "/notifications/preferences/?_data=1", "", nil, false)
		var data struct {
			Route string `json:"route"`
			Data  struct {
				Rows []struct {
					Value string `json:"value"`
					Muted bool   `json:"muted"`
				} `json:"rows"`
			} `json:"data"`
		}
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || json.Unmarshal(w.Body.Bytes(), &data) != nil || data.Route != "notification-preferences" || len(data.Data.Rows) != 22 {
			t.Fatal("current SPA data dispatcher contract changed", w.Code)
		}
		for i, row := range data.Data.Rows {
			if row.Value != notificationNativeHTTPOrder[i] || row.Muted {
				t.Fatal("SPA source order/cleared choices changed")
			}
		}
		post(t, f.credentials, "/notifications/preferences/?_data=1", url.Values{"muted": {"arrival"}})
		if !reflect.DeepEqual(kinds(t, f.owner.ID), []string{"arrival"}) {
			t.Fatal("SPA query changed ordinary POST semantics")
		}
	})
	var variants []map[string]any
	t.Run("registered-display-whitelist-and-current-inactive-account", func(t *testing.T) {
		for _, language := range []string{"en", "ro"} {
			request := httptest.NewRequest(http.MethodGet, "https://app.example/notifications/preferences/", nil)
			request.Header.Set("Accept-Language", language)
			request.AddCookie(f.credentials.Session)
			request.AddCookie(f.credentials.CSRF)
			response := httptest.NewRecorder()
			f.app.ServeHTTP(response, request)
			notificationNativeHTTPHTML(t, response, []string{"arrival"})
			current := httptest.NewRequest(http.MethodGet, "https://app.example/notifications/preferences/", nil)
			current.Header.Set("Accept-Language", language)
			current.AddCookie(f.credentials.Session)
			current.AddCookie(f.credentials.CSRF)
			actual, err := f.app.authority(ctx, current)
			if err != nil || actual.ID != f.owner.ID || !actual.IsActive {
				t.Fatal("registered capture lost current real-cookie authority")
			}
			current = platform.WithActor(current, actual)
			data, name, handled, err := f.app.Web.AccountView(current, actual, "notification_preferences")
			if err != nil || !handled || name != "web/notification_preferences.html" {
				t.Fatal("actual account projection unavailable")
			}
			rows, ok := data["rows"].([]map[string]any)
			if !ok || len(rows) != 22 {
				t.Fatal("actual ordered preference rows unavailable")
			}
			projected := make([]map[string]any, 0, 22)
			for i, row := range rows {
				value, vok := row["value"].(string)
				label, lok := row["label"].(string)
				reason, rok := row["reason"].(string)
				muted, mok := row["muted"].(bool)
				if !vok || !lok || !rok || !mok || value != notificationNativeHTTPOrder[i] || muted != (value == "arrival") {
					t.Fatal("actual captured display shape/order differs")
				}
				projected = append(projected, map[string]any{"value": value, "label": label, "reason": reason, "muted": muted})
			}
			variants = append(variants, map[string]any{"language": language, "projection": map[string]any{"rows": projected}})
		}
		before := snapshot(t, false)
		if _, err := f.app.DB.Exec(ctx, `UPDATE accounts_user SET is_active=false WHERE id=$1`, f.owner.ID); err != nil {
			t.Fatal("inactive current-account fixture unavailable")
		}
		t.Cleanup(func() {
			if _, err := f.app.DB.Exec(ctx, `UPDATE accounts_user SET is_active=true WHERE id=$1`, f.owner.ID); err != nil {
				t.Error("restore inactive synthetic account")
			}
		})
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			w := familyAppRequest(t, f.app, f.credentials, method, "/notifications/preferences/", "https://app.example", url.Values{"muted": {"mention"}}, true)
			if w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), "/login/") || strings.Contains(w.Body.String(), "notification-kind-arrival") || snapshot(t, false) != before {
				t.Fatal("inactive current account retained settings read/write authority", method, w.Code)
			}
		}
	})
	if t.Failed() {
		return
	}
	payload, err := json.Marshal(map[string]any{"schema": 1, "variants": variants})
	if err != nil || len(payload) > 64<<10 || len(variants) != 2 {
		t.Fatal("bounded successful notification projection marker unavailable")
	}
	t.Log("NATIVE_NOTIFICATION_PREFERENCES_V1 " + string(payload))
}
