package web

import (
	"encoding/base64"
	"encoding/json"
	"html"
	"html/template"
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

func trustFixture(t *testing.T, name string) (*Renderer, *http.Request, pongo2.Context) {
	t.Helper()
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	path := "/generated-trust/"
	a := platform.Actor{ID: 731, PublicID: "33333333-3333-4333-8333-333333333333", Username: "generated-self", DisplayName: "Generated self", Cohort: "unassigned", AgeBand: "unknown", IsActive: true}
	request := platform.WithActor(httptest.NewRequest(http.MethodGet, "https://trust.example"+path, nil), a)
	avatar := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="26" height="26"><circle cx="13" cy="13" r="10"/></svg>`))
	data := pongo2.Context{"user": accountUserMap(a), "has_guardians": false, "connections_enabled": false,
		"language": "en", "display_theme": "light", "display_text": "normal", "display_motion": "reduce", "site_name": "Generated trust site", "canonical_url": "https://trust.example" + path, "csrf": "synthetic-trust-csrf", "avatar_uri": avatar,
		"request": map[string]any{"path": path, "csp_nonce": "synthetic-trust-nonce"}, "PRIVATE-extra": "PRIVATE-should-never-render"}
	stamp := time.Date(2020, time.January, 15, 10, 0, 0, 0, time.UTC)
	switch name {
	case "web/person.html", "web/_person_card.html":
		data["card"] = map[string]any{"public_id": "44444444-4444-4444-8444-444444444444", "display": "Generated <target> & person", "avatar": avatar, "tier": "connected", "username": "generated-peer", "verified": true, "connected": true, "can_connect": false, "request_pending": false, "can_message": true, "show_photo": true, "interests": []string{"Quiet <walk>"}, "shared": map[string]any{"activities": []string{"Shared <activity>"}, "groups": []string{"Shared group"}, "activity_overflow": 2, "group_overflow": 1, "join_request": true}, "PRIVATE-history": "PRIVATE-should-never-render"}
		data["person_user"] = map[string]any{"id": int64(732)}
		data["photo_url"] = "/api/media/file/synthetic-signed-reference/"
	case "web/activity_log.html":
		data["entries"] = []map[string]any{{"label": "Generated <action>", "when": stamp, "PRIVATE-payload": "PRIVATE-should-never-render"}}
	case "web/safety_record.html":
		data["decisions"] = []any{map[string]any{"action_id": int64(93), "action_label": "Remove content", "reason_label": "Other <reason>", "scope": "one of your posts", "created_at": stamp, "is_sanction": false, "is_active": false, "content_author_deleted": true, "can_appeal": true, "appeal_status_label": nil, "moderator": "PRIVATE-should-never-render"}}
		data["reports"] = []any{map[string]any{"reason_label": "Spam", "status_label": "Actioned", "detail": "Generated <detail>", "resolution": "Generated <outcome>", "created_at": stamp, "handled_at": stamp, "private_notes": "PRIVATE-should-never-render"}}
	case "web/my_privacy.html":
		data["provenance"] = map[string]any{"has_row": true, "band_display": "Adult (18+)", "provider": "Generated <provider>", "identity_document": "PRIVATE-should-never-render"}
		data["access_set"] = true
		data["api_token_exists"] = true
		data["has_guardians"] = true
		data["interests_count"] = int64(2)
		data["donations_count"] = int64(3)
		data["decisions_count"] = 1
		data["reports_count"] = 4
		data["muted_labels"] = []string{"Mention", "Organizer prep reminder"}
		data["retention"] = []map[string]string{{"category": "Generated retention", "ttl_description": "Generated <duration>"}}
		data["api_token"] = "PRIVATE-should-never-render"
		data["date_of_birth"] = "PRIVATE-should-never-render"
	case "web/verify_age.html":
		data["verified"] = false
		data["native_available"] = false
		data["sandbox"] = false
	case "web/account_delete.html":
		destroyed := map[string]int64{}
		for _, row := range trustDestroyedCopy {
			destroyed[row[0]] = 2
		}
		data["preview"] = map[string]any{"destroyed": destroyed, "retained": map[string]int64{"donations_anonymised": 1, "audit_entries_retained": 2}}
	case "web/report.html":
		data["target_type"] = "user"
		data["target_id"] = int64(732)
		data["target_label"] = "Generated <safe label>"
		data["form"] = map[string]any{"fields": []any{map[string]any{"name": "reason", "value": "spam"}, map[string]any{"name": "detail", "value": "Generated </textarea><script>detail</script>"}}, "as_p": "PRIVATE-should-never-render", "errors": "", "native_trust_error": "Choose a valid report reason."}
	}
	return NewRenderer(root), request, data
}

func TestNativeTrustTenDestinationsFragmentAndLegalDraft(t *testing.T) {
	names := []string{"web/person.html", "web/_person_card.html", "web/activity_log.html", "web/safety_record.html", "web/my_privacy.html", "web/verify_age.html", "web/account_delete.html", "web/report.html", "web/privacy.html", "web/terms.html"}
	if len(nativeTrustTemplates) != len(names) {
		t.Fatal("dispatch scope differs")
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			r, request, data := trustFixture(t, name)
			page, err := r.trustPage(request, name, data)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := r.renderTrust(request, name, data)
			if err != nil {
				t.Fatal(err)
			}
			body := string(raw)
			if strings.Contains(body, "PRIVATE-should-never-render") || strings.Contains(body, "ZgotmplZ") || strings.Contains(body, "{%") || strings.Contains(body, `href="#"`) {
				t.Fatal("unprojected data, invalid URL or legacy instruction rendered")
			}
			if page.Fragment {
				if strings.Contains(body, "<html") || strings.Contains(body, "site-header") || !strings.Contains(body, `class="person-card"`) || !strings.Contains(body, `width="56"`) {
					t.Fatal("routed fragment became a full shell")
				}
			} else if !strings.Contains(body, "<!DOCTYPE html>") || !strings.Contains(body, `value="synthetic-trust-csrf"`) || !strings.Contains(body, `data-meetups-owner="33333333-3333-4333-8333-333333333333"`) {
				t.Fatal("full shell lost form or purge owner")
			}
			if name == "web/privacy.html" || name == "web/terms.html" {
				for _, literal := range []string{"DRAFT — to be finalized by the DPO/legal before launch.", "This text is placeholder copy and is not legally binding in its current form."} {
					if !strings.Contains(html.UnescapeString(body), literal) {
						t.Fatal("legal draft status changed")
					}
				}
			}
			// Exercise the actual Renderer branch with its own nonce/default request.
			delete(data, "request")
			recorder := httptest.NewRecorder()
			if err := r.Render(recorder, request, name, data); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "text/html; charset=utf-8" || recorder.Header().Get("Content-Security-Policy-Report-Only") == "" {
				t.Fatal("outer response/CSP differs")
			}
		})
	}
	r, request, data := trustFixture(t, "web/privacy.html")
	if _, err := r.trustPage(request, "web/spa.html", data); err == nil {
		t.Fatal("SPA shell captured by trust dispatch")
	}
}

func TestNativeTrustProfileTiersAndSuppliedCapabilities(t *testing.T) {
	for _, fragment := range []bool{false, true} {
		for _, tier := range []string{"stranger", "shared", "connected"} {
			name := "web/person.html"
			if fragment {
				name = "web/_person_card.html"
			}
			r, request, data := trustFixture(t, name)
			card := data["card"].(map[string]any)
			card["tier"] = tier
			if tier == "stranger" {
				// A minimal card stays minimal even if an unexpected extra field arrives.
				card["username"] = "PRIVATE-clamped"
				card["interests"] = []string{"PRIVATE-clamped"}
				card["shared"] = map[string]any{"activities": []string{"PRIVATE-clamped"}}
				card["verified"] = true
				card["can_connect"] = true
				card["can_message"] = true
				data["photo_url"] = "https://PRIVATE-clamped.example/picture"
			} else {
				card["can_connect"] = true
				card["request_pending"] = true
				card["can_message"] = false
				card["show_photo"] = tier == "connected"
			}
			page, err := r.trustPage(request, name, data)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := r.renderTrust(request, name, data)
			if err != nil {
				t.Fatal(err)
			}
			body := string(raw)
			if strings.Contains(body, "PRIVATE-clamped") {
				t.Fatal("minimal tier leaked an unexpected private extra")
			}
			if tier == "stranger" && (page.Person.Verified || page.Person.CanConnect || page.Person.CanMessage || page.Person.Photo != "") {
				t.Fatal("minimal tier acquired capability")
			}
			if tier != "stranger" && (page.Person.CanConnect != true || page.Person.Pending != true || page.Person.CanMessage) {
				t.Fatal("view replaced supplied capabilities with actor/cohort policy")
			}
			if tier != "stranger" && !fragment && (!strings.Contains(body, "Connection request pending") || strings.Contains(body, `action="/connections/message/"`)) {
				t.Fatal("pending/message controls differ")
			}
			if !fragment && !strings.Contains(body, `href="/report/?type=user&amp;id=732"`) {
				t.Fatal("guarded internal report target changed")
			}
			if (page.Person.Photo != "") != (tier == "connected" && !fragment) {
				t.Fatal("photo capability or fragment clamp changed")
			}
			if strings.Contains(body, "<target>") || !strings.Contains(html.UnescapeString(body), "Generated <target> & person") {
				t.Fatal("display was not escaped or was lost")
			}
		}
	}
}

func TestNativeTrustSelfRecordsEmptyAndPrivateData(t *testing.T) {
	for _, name := range []string{"web/activity_log.html", "web/safety_record.html", "web/my_privacy.html"} {
		r, request, data := trustFixture(t, name)
		page, err := r.trustPage(request, name, data)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := r.renderTrust(request, name, data)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "PRIVATE-should-never-render") || strings.Contains(string(raw), "<detail>") {
			t.Fatal("record boundary failed")
		}
		if name == "web/safety_record.html" {
			if len(page.Decisions) != 1 || !page.Decisions[0].CanAppeal || !page.Decisions[0].AuthorDeleted || !strings.Contains(string(raw), `name="action_id" value="93"`) || !strings.Contains(string(raw), `maxlength="2000"`) {
				t.Fatal("own appeal contract changed")
			}
			row := data["decisions"].([]any)[0].(map[string]any)
			row["can_appeal"] = false
			row["appeal_status_label"] = "Pending review"
			raw, err = r.renderTrust(request, name, data)
			if err != nil || strings.Contains(string(raw), `name="action_id"`) || !strings.Contains(string(raw), "Contest status: Pending review") {
				t.Fatal("record-status branch changed")
			}
			data["decisions"] = []any{}
			data["reports"] = []any{}
		} else if name == "web/activity_log.html" {
			data["entries"] = []map[string]any{}
		} else {
			if !page.Privacy.HasToken || !page.HasGuardians || len(page.Privacy.Retention) != 1 || !strings.Contains(string(raw), `href="/settings/"`) {
				t.Fatal("privacy inventory/capability changed")
			}
			data["provenance"] = nil
			data["access_set"] = false
			data["api_token_exists"] = false
			data["has_guardians"] = false
			data["muted_labels"] = []string{}
		}
		if _, err := r.renderTrust(request, name, data); err != nil {
			t.Fatal("empty source state refused", err)
		}
	}
}

func TestNativeTrustWalletPublicRequestAndBlankProofs(t *testing.T) {
	r, request, data := trustFixture(t, "web/verify_age.html")
	source := trustWalletRequest{Nonce: "generated-nonce</script>", Audience: "https://wallet.example", State: "generated-public-state", Definition: trustWalletDefinition{ID: "age-verification", Inputs: []trustWalletInput{{ID: "age-attestation", Format: trustWalletFormat{JWT: trustWalletAlgorithm{Algorithms: []string{"ES256"}}}, Constraints: trustWalletConstraints{Fields: []trustWalletClaim{{Path: []string{"$.age_over_16"}}, {Path: []string{"$.age_over_18"}}}}}}}}
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var challenge map[string]any
	if err = json.Unmarshal(raw, &challenge); err != nil {
		t.Fatal(err)
	}
	challenge["vp_token"] = "PRIVATE-should-never-render"
	challenge["holder_binding_proof"] = "PRIVATE-should-never-render"
	challenge["date_of_birth"] = "PRIVATE-should-never-render"
	data["native_available"] = true
	data["native_challenge"] = challenge
	data["vp_token"] = "PRIVATE-should-never-render"
	data["native_request_json"] = "PRIVATE-should-never-render"
	page, err := r.trustPage(request, "web/verify_age.html", data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(page.Wallet.RequestText, "PRIVATE-") || strings.Contains(string(page.Wallet.Request), "</script>") {
		t.Fatal("raw challenge extra or script terminator copied")
	}
	var projected trustWalletRequest
	if err = json.Unmarshal([]byte(page.Wallet.Request), &projected); err != nil || !reflect.DeepEqual(projected, source) {
		t.Fatal("public request changed")
	}
	output, err := r.renderTrust(request, "web/verify_age.html", data)
	if err != nil {
		t.Fatal(err)
	}
	body := string(output)
	for _, literal := range []string{`id="age-verification-request"`, `name="action" value="verify"`, `name="state" value="generated-public-state"`, `name="vp_token" maxlength="32768" rows="4" autocomplete="off" required></textarea>`, `name="holder_binding_proof" maxlength="32768" rows="4" autocomplete="off" required></textarea>`} {
		if !strings.Contains(body, literal) {
			t.Fatal("wallet field contract changed")
		}
	}
	if strings.Contains(body, "PRIVATE-should-never-render") {
		t.Fatal("posted proof echoed")
	}
	definition := challenge["presentation_definition"].(map[string]any)
	descriptor := definition["input_descriptors"].([]any)[0].(map[string]any)
	descriptor["constraints"].(map[string]any)["fields"] = []any{}
	if raw, err := r.renderTrust(request, "web/verify_age.html", data); err == nil || raw != nil {
		t.Fatal("malformed wallet request rendered")
	}
	delete(data, "native_challenge")
	output, err = r.renderTrust(request, "web/verify_age.html", data)
	if err != nil || !strings.Contains(string(output), `value="start"`) {
		t.Fatal("start branch changed")
	}
	data["native_available"] = false
	data["sandbox"] = true
	output, err = r.renderTrust(request, "web/verify_age.html", data)
	if err != nil || !strings.Contains(string(output), `name="age" value="adult" checked`) {
		t.Fatal("existing demo branch changed")
	}
	data["sandbox"] = false
	data["verified"] = true
	output, err = r.renderTrust(request, "web/verify_age.html", data)
	if err != nil || !strings.Contains(html.UnescapeString(string(output)), "isn't configured on this server yet.") {
		t.Fatal("unavailable branch changed")
	}
}

func TestNativeTrustReportBindingDeletionAndLegalMarkup(t *testing.T) {
	r, request, data := trustFixture(t, "web/report.html")
	output, err := r.renderTrust(request, "web/report.html", data)
	if err != nil {
		t.Fatal(err)
	}
	body := string(output)
	if !strings.Contains(body, `value="spam" selected`) || !strings.Contains(body, "Choose a valid report reason.") || strings.Contains(body, "<script>detail</script>") || strings.Count(body, `name="reason"`) != 1 {
		t.Fatal("bound report/error/source choices changed")
	}
	form := data["form"].(map[string]any)
	delete(form, "native_trust_error")
	if _, err := r.renderTrust(request, "web/report.html", data); err != nil {
		t.Fatal("clean report state refused")
	}
	for _, n := range []int64{0, 1, 2} {
		r, request, data = trustFixture(t, "web/account_delete.html")
		preview := data["preview"].(map[string]any)
		for key := range preview["destroyed"].(map[string]int64) {
			preview["destroyed"].(map[string]int64)[key] = n
		}
		page, err := r.trustPage(request, "web/account_delete.html", data)
		if err != nil || len(page.Destroyed) != 11 || len(page.Retained) != 2 {
			t.Fatal("erasure preview shape changed")
		}
		if n == 1 && page.Destroyed[0] != "1 activity membership" || n == 2 && page.Destroyed[0] != "2 activity memberships" {
			t.Fatal("plural preview changed")
		}
		output, err = r.renderTrust(request, "web/account_delete.html", data)
		if err != nil || !strings.Contains(string(output), `action="/account/delete/"`) {
			t.Fatal("existing erasure action changed")
		}
	}
	source := `Read <strong>carefully</strong> and <a href="%(privacy_url)s">Privacy Policy</a>.`
	runs := trustRichRuns(source, `Read <script>unsafe</script>.`, "/privacy/")
	if !reflect.DeepEqual(runs, trustRichRuns(source, source, "/privacy/")) {
		t.Fatal("catalogue introduced markup")
	}
	for _, name := range []string{"web/privacy.html", "web/terms.html"} {
		r, _, data = trustFixture(t, name)
		request = httptest.NewRequest(http.MethodGet, "https://trust.example/generated-trust/", nil)
		page, err := r.trustPage(request, name, data)
		if err != nil || page.Authenticated {
			t.Fatal("public legal page acquired auth requirement")
		}
		output, err = r.renderTrust(request, name, data)
		if err != nil || strings.Contains(string(output), `data-meetups-owner=`) || strings.Contains(string(output), `action="/logout/"`) {
			t.Fatal("anonymous legal shell exposed account controls")
		}
	}
}

func TestNativeTrustRejectsMalformedBeforeWriting(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(pongo2.Context)
	}{
		{"web/my_privacy.html", func(d pongo2.Context) { d["interests_count"] = -1 }},
		{"web/my_privacy.html", func(d pongo2.Context) { d["retention"] = []any{map[string]any{"category": "wrong shape"}} }},
		{"web/activity_log.html", func(d pongo2.Context) { d["entries"] = "wrong shape" }},
		{"web/safety_record.html", func(d pongo2.Context) { d["decisions"].([]any)[0].(map[string]any)["can_appeal"] = "true" }},
		{"web/person.html", func(d pongo2.Context) { d["card"].(map[string]any)["avatar"] = "javascript:alert(1)" }},
		{"web/person.html", func(d pongo2.Context) { d["photo_url"] = "//untrusted.example/photo/" }},
		{"web/report.html", func(d pongo2.Context) { d["form"].(map[string]any)["native_trust_error"] = "PRIVATE-provider-error" }},
		{"web/report.html", func(d pongo2.Context) { d["form"].(map[string]any)["fields"] = []any{} }},
		{"web/account_delete.html", func(d pongo2.Context) { d["preview"].(map[string]any)["destroyed"] = map[string]int64{} }},
		{"web/verify_age.html", func(d pongo2.Context) { d["native_available"] = "true" }},
		{"web/terms.html", func(d pongo2.Context) { d["canonical_url"] = "javascript:alert(1)" }},
		{"web/report.html", func(d pongo2.Context) { d["csrf"] = "" }},
		{"web/my_privacy.html", func(d pongo2.Context) { d["user"].(map[string]any)["id"] = int64(999) }},
	}
	for _, c := range cases {
		r, request, data := trustFixture(t, c.name)
		c.mutate(data)
		delete(data, "request")
		recorder := httptest.NewRecorder()
		if err := r.Render(recorder, request, c.name, data); err == nil || recorder.Body.Len() != 0 || recorder.Header().Get("Content-Type") != "" {
			t.Fatal("malformed snapshot wrote a partial response", c.name)
		}
	}
	for _, name := range []string{"web/person.html", "web/_person_card.html", "web/activity_log.html", "web/safety_record.html", "web/my_privacy.html", "web/verify_age.html", "web/account_delete.html", "web/report.html"} {
		r, _, data := trustFixture(t, name)
		request := httptest.NewRequest(http.MethodGet, "https://trust.example/generated-trust/", nil)
		if _, err := r.trustPage(request, name, data); err == nil {
			t.Fatal("private page accepted absent authentication", name)
		}
	}
}

func TestNativeTrustCacheConcurrencyAndTemplateFailure(t *testing.T) {
	first, err := loadTrustTemplates()
	if err != nil {
		t.Fatal(err)
	}
	r, request, data := trustFixture(t, "web/my_privacy.html")
	page, err := r.trustPage(request, "web/my_privacy.html", data)
	if err != nil {
		t.Fatal(err)
	}
	want, err := executeTrustTemplate(first, page)
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	failures := make(chan string, 16)
	for i := 0; i < 16; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			parsed, err := loadTrustTemplates()
			if err != nil || parsed != first {
				failures <- "cache not shared"
				return
			}
			raw, err := executeTrustTemplate(parsed, page)
			if err != nil || string(raw) != string(want) {
				failures <- "concurrent render changed"
			}
		}()
	}
	wait.Wait()
	close(failures)
	for failure := range failures {
		t.Fatal(failure)
	}
	broken, err := template.New("trust").Parse(`{{define "trust"}}partial{{.MissingField}}{{end}}`)
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := executeTrustTemplate(broken, page); err == nil || raw != nil {
		t.Fatal("template failure leaked buffered bytes")
	}
}

func TestNativeTrustLegalPublicDataFallbackAllowsEmptyCSRF(t *testing.T) {
	// Server.page leaves CSRF empty for public ?_data=1 with SPA enabled.
	// BuildSPA does not own privacy/terms, so both signed and anonymous callers
	// legitimately fall through to these existing HTML read transports.
	for _, name := range []string{"web/privacy.html", "web/terms.html"} {
		for _, signed := range []bool{false, true} {
			r, request, data := trustFixture(t, name)
			if !signed {
				request = httptest.NewRequest(http.MethodGet, "https://trust.example/generated-trust/?_data=1", nil)
			} else {
				request.URL.RawQuery = "_data=1"
			}
			data["csrf"] = ""
			page, err := r.trustPage(request, name, data)
			if err != nil || page.Authenticated != signed {
				t.Fatal("existing public read fallback refused", name, signed, err)
			}
			raw, err := r.renderTrust(request, name, data)
			if err != nil || !strings.Contains(html.UnescapeString(string(raw)), "This text is placeholder copy and is not legally binding in its current form.") {
				t.Fatal("public legal fallback lost copy", err)
			}
			delete(data, "request")
			recorder := httptest.NewRecorder()
			if err := r.Render(recorder, request, name, data); err != nil || recorder.Body.Len() == 0 || recorder.Header().Get("Content-Type") != "text/html; charset=utf-8" {
				t.Fatal("existing public read transport changed", name, signed, err)
			}
		}
	}
}
