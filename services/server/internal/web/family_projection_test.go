package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

func familyProjectionFixture() (*http.Request, pongo2.Context) {
	actor := platform.Actor{ID: 7, PublicID: "synthetic-family-public-id", Username: "generated-guardian", Cohort: "adult", AgeBand: "adult", IdentityVerified: true, IsActive: true}
	request := platform.WithActor(httptest.NewRequest("GET", "https://fixture.local/wards/", nil), actor)
	hours := []int{}
	for i := 0; i < 24; i++ {
		hours = append(hours, i)
	}
	caps := func() map[string]any {
		return map[string]any{
			"relationship": "parent", "can_get_arrival_pings": true, "can_observe_messaging": true, "requires_consent": true, "consent_active": true, "can_grant_consent": true, "can_set_guardrails": true, "guardrail_supervised_only": false, "guardrail_combined_blocks_all": false,
			"guardrail_earliest_start_hour": 0, "guardrail_latest_start_hour": nil, "guardrail_max_open_joins": 2, "guardrail_allowed_weekday_ints": []int{1, 3}, "guardrail_allowed_categories": []string{"sport"},
		}
	}
	ward := map[string]any{"id": json.Number("41"), "username": "generated-child", "display_name": "", "cohort": "child", "caps": caps(), "can_set_topics": true, "topic_slugs": []string{"read"}, "guardrail_preview": map[string]int{"eligible": 0, "total": 2}, "meetups": []map[string]any{{"starts_at": "2026-10-20T10:00:00Z", "ends_at": "2026-10-20T11:00:00Z", "meeting_point": "Generated meeting point", "place": map[string]any{"name": "Generated venue", "address_city": "Cluj-Napoca"}, "activity_type": map[string]any{"name": "Generated activity"}, "supervised": true, "supervision_live": false, "child_venue_rationale": "staff_verified", "unadmitted_raw": "PRIVATE-MEETUP-RAW"}}, "private_email": "PRIVATE-ACCOUNT-EMAIL", "posted_action": "https://outside.invalid/foreign"}
	second := map[string]any{"id": json.Number("42"), "username": "other-generated-child", "display_name": "", "cohort": "child", "caps": caps(), "can_set_topics": true, "topic_slugs": []string{"sport"}, "meetups": []map[string]any{}}
	second["caps"].(map[string]any)["guardrail_allowed_weekday_ints"] = []int{2}
	second["caps"].(map[string]any)["guardrail_allowed_categories"] = []string{"read"}
	return request, pongo2.Context{"language": "en", "display_theme": "dark", "display_text": "large", "display_motion": "reduce", "site_name": "Synthetic Activities", "csrf": "synthetic-family-proof", "connections_enabled": true, "has_guardians": false, "minor_onboarding": true, "guardrail_hours": hours, "guardrail_weekdays": []any{[]any{1, "Mon"}, []any{2, "Tue"}, []any{3, "Wed"}, []any{4, "Thu"}, []any{5, "Fri"}, []any{6, "Sat"}, []any{7, "Sun"}}, "guardrail_categories": []any{[]string{"sport", "Sport"}, []string{"read", "Reading"}}, "wards": []map[string]any{ward, second}}
}

func TestNativeFamilyProjectionPreservesZeroNilAndIndependentSelections(t *testing.T) {
	request, data := familyProjectionFixture()
	r := NewRenderer(t.TempDir())
	page, err := r.familyPage(request, "web/wards.html", data)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Wards) != 2 || !page.Invite || page.InviteUnavailable != "" {
		t.Fatal("authorized rows/invitation availability diverged")
	}
	first, second := page.Wards[0], page.Wards[1]
	if first.ID != 41 || first.Name != "generated-child" || !first.Earliest[1].Selected || first.Earliest[0].Selected || !first.Latest[0].Selected {
		t.Fatal("nil and midnight became the same limit")
	}
	if !first.Weekdays[0].Selected || first.Weekdays[1].Selected || second.Weekdays[0].Selected || !second.Weekdays[1].Selected || !first.Categories[0].Selected || second.Categories[0].Selected || !first.Topics[1].Selected || !second.Topics[0].Selected {
		t.Fatal("ward selections leaked through shared option slices")
	}
	if first.Preview == nil || first.Preview.Total != 2 || first.Preview.Eligible != 0 || second.Preview != nil || !strings.Contains(first.Preview.Text, "0 of 2") {
		t.Fatal("zero eligible and unavailable preview were conflated")
	}
	if first.LimitsURL != "/wards/41/limits/" || first.TopicsURL != "/wards/41/topics/" || first.RevokeURL != "/wards/41/revoke/" || first.Meetups[0].MeetingPoint != "Generated meeting point" {
		t.Fatal("fixed owned destinations or admitted child facts lost")
	}
	raw, err := r.renderFamily(request, "web/wards.html", data)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"PRIVATE-MEETUP-RAW", "PRIVATE-ACCOUNT-EMAIL", "outside.invalid", "synthetic-family-public-id"} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatalf("unadmitted input crossed native VM: %s", private)
		}
	}
	for _, destination := range []string{"/my-meetups/", "/organize/", "/connections/", "/gauges/", "/partners/"} {
		if !bytes.Contains(raw, []byte(destination)) {
			t.Fatal("existing navigation destination lost", destination)
		}
	}
}

func TestNativeFamilyProjectionRejectsMalformedSnapshotBeforeWriting(t *testing.T) {
	probes := []struct {
		name   string
		change func(pongo2.Context)
	}{
		{"missing-rows", func(d pongo2.Context) { delete(d, "wards") }},
		{"fractional-id", func(d pongo2.Context) { d["wards"].([]map[string]any)[0]["id"] = 41.5 }},
		{"nonpositive-id", func(d pongo2.Context) { d["wards"].([]map[string]any)[0]["id"] = 0 }},
		{"duplicate-id", func(d pongo2.Context) { d["wards"].([]map[string]any)[1]["id"] = 41 }},
		{"unknown-cohort", func(d pongo2.Context) { d["wards"].([]map[string]any)[0]["cohort"] = "unknown" }},
		{"untyped-capability", func(d pongo2.Context) {
			d["wards"].([]map[string]any)[0]["caps"].(map[string]any)["can_set_guardrails"] = "true"
		}},
		{"out-of-range-hour", func(d pongo2.Context) {
			d["wards"].([]map[string]any)[0]["caps"].(map[string]any)["guardrail_earliest_start_hour"] = 24
		}},
		{"bad-weekday", func(d pongo2.Context) {
			d["wards"].([]map[string]any)[0]["caps"].(map[string]any)["guardrail_allowed_weekday_ints"] = []int{0}
		}},
		{"preview-missing-key", func(d pongo2.Context) {
			d["wards"].([]map[string]any)[0]["guardrail_preview"] = map[string]int{"total": 0, "other": 0}
		}},
		{"preview-contradiction", func(d pongo2.Context) {
			d["wards"].([]map[string]any)[0]["guardrail_preview"] = map[string]int{"total": 0, "eligible": 1}
		}},
		{"unknown-rationale", func(d pongo2.Context) {
			d["wards"].([]map[string]any)[0]["meetups"].([]map[string]any)[0]["child_venue_rationale"] = "provider-approved"
		}},
		{"date-invalid", func(d pongo2.Context) {
			d["wards"].([]map[string]any)[0]["meetups"].([]map[string]any)[0]["starts_at"] = "next-week"
		}},
		{"unknown-theme", func(d pongo2.Context) { d["display_theme"] = "anything" }},
		{"compound-theme", func(d pongo2.Context) { d["display_theme"] = "auto|light" }},
		{"compound-text", func(d pongo2.Context) { d["display_text"] = "normal|large" }},
		{"compound-motion", func(d pongo2.Context) { d["display_motion"] = "reduce|full" }},
		{"raw-error", func(d pongo2.Context) { d["messages"] = []string{"PRIVATE-SQL"} }},
	}
	for _, probe := range probes {
		t.Run(probe.name, func(t *testing.T) {
			request, data := familyProjectionFixture()
			probe.change(data)
			r := NewRenderer(t.TempDir())
			w := httptest.NewRecorder()
			if err := r.Render(w, request, "web/wards.html", data); err == nil || w.Body.Len() != 0 || w.Header().Get("Content-Type") != "" {
				t.Fatal("malformed family snapshot escaped before preparation completed")
			}
		})
	}
}

func TestNativeFamilyProjectionKeepsTeenFactsAndWardExplanationReadOnly(t *testing.T) {
	request, data := familyProjectionFixture()
	ward := data["wards"].([]map[string]any)[0]
	ward["cohort"] = "teen"
	ward["can_set_topics"] = false
	delete(ward, "guardrail_preview")
	caps := ward["caps"].(map[string]any)
	for _, key := range []string{"can_get_arrival_pings", "can_observe_messaging", "can_set_guardrails", "requires_consent", "can_grant_consent"} {
		caps[key] = false
	}
	r := NewRenderer(t.TempDir())
	page, err := r.familyPage(request, "web/wards.html", data)
	if err != nil {
		t.Fatal(err)
	}
	if page.Wards[0].CanLimits || page.Wards[0].CanTopics || page.Wards[0].Arrival || page.Wards[0].Observe || page.Wards[0].Meetups[0].MeetingPoint != "" || page.Wards[0].Meetups[0].End != "" || page.Wards[0].Meetups[0].Rationale != "" {
		t.Fatal("child-only fact or control survived current teen projection")
	}
	caps["can_set_guardrails"] = true
	if _, err = r.familyPage(request, "web/wards.html", data); err == nil {
		t.Fatal("contradictory nonchild capability accepted")
	}
	caps["can_set_guardrails"] = false
	data["guardians"] = []map[string]any{{"id": 999, "username": "generated-parent", "display_name": "", "caps": caps, "private_token": "PRIVATE-INVITATION-TOKEN", "revoke_url": "/wards/999/revoke/"}}
	page, err = r.familyPage(request, "web/guardianship.html", data)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Guardians) != 1 || page.Wards != nil || page.Invite || page.Guardians[0].Name != "generated-parent" {
		t.Fatal("ward received wrong relationship view")
	}
	raw, err := r.renderFamily(request, "web/guardianship.html", data)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"PRIVATE-INVITATION-TOKEN", "/revoke/", "/limits/", "/topics/", "name=\"ward_username\"", "999"} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatal("ward explanation acquired raw identifier/action", private)
		}
	}
}

func TestNativeFamilyRendererUsesEmbeddedBranchAndExistingHeaderPolicy(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "templates/web"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "templates/web/wards.html"), []byte("LEGACY-FAMILY-SENTINEL"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "templates/web/other.html"), []byte("OTHER-PONGO-SENTINEL"), 0600); err != nil {
		t.Fatal(err)
	}
	r := NewRenderer(root)
	for _, enforce := range []bool{false, true} {
		request, data := familyProjectionFixture()
		r.CSPEnforce = enforce
		w := httptest.NewRecorder()
		if err := r.Render(w, request, "web/wards.html", data); err != nil {
			t.Fatal(err)
		}
		header := "Content-Security-Policy-Report-Only"
		if enforce {
			header = "Content-Security-Policy"
		}
		if w.Header().Get("Content-Type") != "text/html; charset=utf-8" || w.Header().Get("Cache-Control") != "private, no-cache" || !strings.Contains(w.Header().Get("Vary"), "Cookie") || !strings.Contains(w.Header().Get(header), "script-src 'self' 'nonce-") || w.Header().Get("Reporting-Endpoints") != `csp="/api/v1/ops/csp-report/"` {
			t.Fatal("native branch forked existing header decision")
		}
		if strings.Contains(w.Body.String(), "LEGACY-FAMILY-SENTINEL") || !strings.Contains(w.Body.String(), "Your wards") {
			t.Fatal("selected family name still uses Pongo")
		}
	}
	request, _ := familyProjectionFixture()
	w := httptest.NewRecorder()
	if err := r.Render(w, request, "web/other.html", pongo2.Context{}); err != nil || w.Body.String() != "OTHER-PONGO-SENTINEL" {
		t.Fatal("unselected Pongo identity changed", err)
	}
}

func TestNativeFamilyCopyUsesExistingCatalogAndEscapesAfterTranslation(t *testing.T) {
	request, data := familyProjectionFixture()
	data["language"] = "ro"
	r := NewRenderer(t.TempDir())
	original := familyCopySources["Children you are the registered parent/legal guardian of. Guardianship is set up alongside parental consent. You can see where and when they're meeting in person — not their conversations, photos or who else is there."]
	r.catalog = translationCatalog{original: translation{Text: map[int]string{0: "Copii <strong>în grija ta</strong> &mdash; <img src=x onerror=private>"}}, "Your wards": translation{Text: map[int]string{0: "Copiii tăi"}}, "&#10003; Limit set: meetups starting no earlier than %(hour)s:00.": translation{Text: map[int]string{0: "Începe după %(hour)s:00."}}}
	raw, err := r.renderFamily(request, "web/wards.html", data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("Copiii tăi")) || !bytes.Contains(raw, []byte("Copii în grija ta — &lt;img")) || bytes.Contains(raw, []byte("<img src=x")) || !bytes.Contains(raw, []byte("Începe după 0:00.")) {
		t.Fatal("native copy bypassed existing exact msgid or contextual escaping")
	}
	if !strings.Contains(familyPlainCopy(translationCatalog{}, "en", "You can be in at most %(n)s open meetup at a time.", 2), "meetups") {
		t.Fatal("existing English plural semantics lost")
	}
}

func TestNativeFamilyInvitationAvailabilityAndPublicErrorsAreFinite(t *testing.T) {
	request, data := familyProjectionFixture()
	r := NewRenderer(t.TempDir())
	data["minor_onboarding"] = false
	page, err := r.familyPage(request, "web/wards.html", data)
	if err != nil || page.Invite || !strings.Contains(page.InviteUnavailable, "disabled") {
		t.Fatal("environment-disabled invitation remained enabled or unexplained", err)
	}
	data["minor_onboarding"] = true
	actor, _ := platform.ActorFrom(request)
	actor.IdentityVerified = false
	request = platform.WithActor(request, actor)
	data["messages"] = []socialMessage{{"message": "PRIVATE-SQL-ERROR", "tags": "error"}}
	page, err = r.familyPage(request, "web/wards.html", data)
	if err != nil || page.Invite || !strings.Contains(page.InviteUnavailable, "verified adult") || reflect.DeepEqual(page.Shell.Messages, []string{"PRIVATE-SQL-ERROR"}) {
		t.Fatal("unverified actor acquired invite or raw error", err)
	}
	raw, err := r.renderFamily(request, "web/wards.html", data)
	if err != nil || bytes.Contains(raw, []byte("PRIVATE-SQL-ERROR")) || bytes.Contains(raw, []byte("/wards/invite/")) || !bytes.Contains(raw, []byte(page.InviteUnavailable)) {
		t.Fatal("finite unavailable invitation/error output diverged", err)
	}
	request = httptest.NewRequest("GET", "https://fixture.local/wards/", nil)
	if _, err = r.familyPage(request, "web/wards.html", data); err == nil {
		t.Fatal("anonymous request bypassed projection admission")
	}
}

func TestNativeFamilyProjectionPreservesValidCustomRelationshipAndStoredCategory(t *testing.T) {
	request, data := familyProjectionFixture()
	r := NewRenderer(t.TempDir())
	caps := data["wards"].([]map[string]any)[0]["caps"].(map[string]any)
	caps["guardrail_allowed_categories"] = []string{"stored-child-category"}
	page, err := r.familyPage(request, "web/wards.html", data)
	if err != nil {
		t.Fatal(err)
	}
	choices := page.Wards[0].Categories
	if len(choices) != 3 || choices[2].Value != "stored-child-category" || !choices[2].Selected || choices[2].Label != "stored-child-category" {
		t.Fatal("valid stored restriction outside top-level options disappeared")
	}
	caps["relationship"] = "grandparent"
	data["guardians"] = []map[string]any{{"username": "generated-custom-guardian", "display_name": "", "caps": caps}}
	page, err = r.familyPage(request, "web/guardianship.html", data)
	if err != nil || len(page.Guardians) != 1 || page.Guardians[0].Relationship != "grandparent" {
		t.Fatal("valid existing custom relationship became a renderer policy denial", err)
	}
}

func TestNativeFamilyLimitCopyKeepsParentAndWardPerspective(t *testing.T) {
	request, data := familyProjectionFixture()
	r := NewRenderer(t.TempDir())
	page, err := r.familyPage(request, "web/wards.html", data)
	if err != nil {
		t.Fatal(err)
	}
	parent := strings.Join(page.Wards[0].ChosenLimits, " ")
	if !strings.Contains(parent, "Limit set: at most 2 open meetups") || strings.Contains(parent, "You can be in") {
		t.Fatal("child restrictions addressed the guardian as participant")
	}
	ward := data["wards"].([]map[string]any)[0]
	data["guardians"] = []map[string]any{{"username": "generated-parent", "display_name": "", "caps": ward["caps"]}}
	page, err = r.familyPage(request, "web/guardianship.html", data)
	if err != nil || !strings.Contains(strings.Join(page.Guardians[0].ChosenLimits, " "), "You can be in at most 2 open meetups") {
		t.Fatal("ward explanation lost direct readable limit copy", err)
	}
}
