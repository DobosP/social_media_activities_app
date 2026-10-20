package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

func informationNativeFixture(t *testing.T, name string, signed bool) (*Renderer, *http.Request, pongo2.Context) {
	t.Helper()
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{"donate": "/donate/", "transparency": "/transparency/", "campaigns": "/campaigns/", "partners": "/partners/", "my_donations": "/my-donations/"}
	request := httptest.NewRequest(http.MethodGet, "https://information.example"+paths[name], nil)
	actor := platform.Actor{ID: 981, PublicID: "55555555-5555-4555-8555-555555555981", Username: "generated-donor", DisplayName: "Generated <person>", Cohort: "adult", AgeBand: "adult", IsActive: true}
	user := map[string]any{"is_authenticated": false, "public_id": "", "username": "", "display_name": "", "avatar_uri": nil}
	if signed {
		request = platform.WithActor(request, actor)
		user = map[string]any{"is_authenticated": true, "public_id": actor.PublicID, "username": actor.Username, "display_name": actor.DisplayName, "avatar_uri": nil}
	}
	data := pongo2.Context{"language": "en", "display_theme": "auto", "display_text": "normal", "display_motion": "auto", "site_name": "Generated information site", "canonical_url": request.URL.String(), "og_locale": "en_GB", "csrf": "synthetic-information-csrf", "user": user, "request": map[string]any{"path": request.URL.Path, "csp_nonce": "synthetic-information-nonce"}, "connections_enabled": false, "has_guardians": false,
		"form":         donateForm(request, []map[string]any{{"id": int64(7), "title": "Zed supplied campaign", "slug": "zed"}, {"id": int64(2), "title": "Alpha supplied campaign", "slug": "alpha"}}, ""),
		"cost_anchors": []map[string]any{{"currency": "EUR", "amount_cents": int64(199), "label": "Generated hosting"}}, "raised_cents": int64(199), "spend_total_cents": int64(100), "currency": "EUR", "spend_rows": []map[string]any{{"category": "Zed category", "total_cents": int64(100)}, {"category": "Alpha category", "total_cents": int64(99)}},
		"in_kind_rows": []map[string]any{{"category": "Generated rooms", "unit_text": "hours", "total_quantity": "1.5", "total_cents": int64(500)}}, "civic_outcomes": []map[string]any{{"headline": "Generated civic note", "detail": "Generated narrative", "period": "2026", "partner_name": "Generated partner"}},
		"campaigns":           []map[string]any{{"title": "Generated active campaign", "slug": "active", "description": "First line\nSecond line\n\nAnother paragraph", "raised_cents": int64(199), "goal_cents": int64(600), "currency": "EUR", "percent": "33.1666666666666667", "partner_name": "Generated partner", "partner_blurb": "Generated credit", "partner_website": "https://example.org/partner?a=1&b=2"}},
		"completed_campaigns": []map[string]any{{"title": "Generated completed campaign", "outcome": "Generated completed outcome", "closed_at": "2026-10-01T00:00:00Z", "raised_cents": int64(199), "currency": "EUR", "spend_entries": []any{map[string]any{"category": "Books", "amount_cents": int64(199), "currency": "EUR", "period": "2026"}}}},
		"partners":            []map[string]any{{"name": "Zed supplied partner", "get_kind_display": "Library", "blurb": "Generated blurb", "website": "https://example.org/partner", "place": map[string]any{"id": int64(7), "pk": int64(7), "name": "Generated venue"}}, {"name": "Alpha supplied partner", "get_kind_display": "School", "blurb": "", "website": "javascript:alert(1)", "place": nil}},
		"donations":           []map[string]any{{"amount_cents": int64(199), "currency": "EUR", "get_status_display": "Completed", "recurring": true, "created_at": "2026-10-01T00:00:00Z", "external_ref": "generated-own-reference", "campaign": map[string]any{"title": "Generated completed campaign"}, "provider": "PRIVATE_provider", "donor_id": int64(981)}}, "PRIVATE_unknown": "PRIVATE_ignored"}
	return NewRenderer(root), request, data
}
func informationNeed(t testing.TB, raw []byte, values ...string) {
	t.Helper()
	for _, v := range values {
		if !bytes.Contains(raw, []byte(v)) {
			t.Fatalf("missing information fact %q", v)
		}
	}
}
func TestNativeInformationProjectionPreservesOrderCopiesAndSeparatesPrivateReceipts(t *testing.T) {
	for _, name := range []string{"donate", "transparency", "campaigns", "partners", "my_donations"} {
		r, request, data := informationNativeFixture(t, name, name == "my_donations")
		before, _ := json.Marshal(data)
		p, err := r.informationPage(request, "web/"+name+".html", data)
		if err != nil {
			t.Fatal(name, err)
		}
		after, _ := json.Marshal(data)
		if !bytes.Equal(before, after) {
			t.Fatal("producer snapshot mutated")
		}
		if name == "transparency" && (len(p.Spend) != 2 || p.Spend[0].Category != "Zed category" || p.Spend[1].Category != "Alpha category" || len(p.InKind) != 1 || p.InKind[0].Quantity != "1.5") {
			t.Fatal("ordered ledger or separate noncash facts changed")
		}
		if name == "partners" && (len(p.Partners) != 2 || p.Partners[0].Name != "Zed supplied partner" || p.Partners[1].Name != "Alpha supplied partner" || p.Partners[1].Website != "") {
			t.Fatal("ordered partner credit/safe_href changed")
		}
		if name == "donate" {
			p.Form.Choices[0].Label = "Changed copy"
			native := data["form"].(map[string]any)["native"].(informationDonationForm)
			if native.Choices[0].Label != "Zed supplied campaign" {
				t.Fatal("form choices alias source")
			}
		}
		body, err := r.renderInformation(request, "web/"+name+".html", data)
		if err != nil || bytes.Contains(body, []byte("PRIVATE_")) {
			t.Fatal("private raw fields escaped allowlist")
		}
		if name != "my_donations" && bytes.Contains(body, []byte("generated-own-reference")) {
			t.Fatal("owner receipt reached public page")
		}
	}
}
func TestNativeInformationDonorFormPreservesBoundInputErrorsAndOriginalFragment(t *testing.T) {
	r, request, data := informationNativeFixture(t, "donate", false)
	request.Method = http.MethodPost
	request.PostForm = url.Values{"amount": {"1.005<script>"}, "campaign": {"7"}}
	message := "The payment provider is temporarily unavailable. Please try again."
	choices := []map[string]any{{"id": int64(7), "title": "<script>Campaign</script>", "slug": "safe"}}
	data["form"] = donateForm(request, choices, message)
	form := data["form"].(map[string]any)
	legacy := form["as_p"].(*pongo2.Value).String()
	if !strings.Contains(legacy, `value="7" selected`) || !strings.Contains(legacy, "1.005&lt;script&gt;") {
		t.Fatal("original form fragment changed")
	}
	p, err := r.informationPage(request, "web/donate.html", data)
	if err != nil || p.Form.Amount != "1.005<script>" || p.Form.Campaign != "7" || p.Form.Error != message || !p.Form.Choices[0].Selected {
		t.Fatal("bound form snapshot changed")
	}
	body, err := r.renderInformation(request, "web/donate.html", data)
	if err != nil {
		t.Fatal(err)
	}
	informationNeed(t, body, `method="post"`, `name="csrfmiddlewaretoken" value="synthetic-information-csrf"`, `name="amount" value="1.005&lt;script&gt;"`, `min="1" step="0.01" required id="id_amount"`, `name="campaign" id="id_campaign"`, `value="7" selected`, message, "&lt;script&gt;Campaign&lt;/script&gt;")
	if bytes.Contains(body, []byte("<script>Campaign")) {
		t.Fatal("form title escaped incorrectly")
	}
}
func TestNativeInformationFractionalMeterAndNeutralCompletedDisclosure(t *testing.T) {
	r, request, data := informationNativeFixture(t, "campaigns", false)
	body, err := r.renderInformation(request, "web/campaigns.html", data)
	if err != nil {
		t.Fatal(err)
	}
	informationNeed(t, body, `aria-valuenow="33.1666666666666667"`, `width="33.1666666666666667%"`, `raised 1.99 of 6.00 EUR goal`, `href="/donate/?campaign=active"`, "Generated completed outcome", "Books — 1.99 EUR")
	data["campaigns"] = []map[string]any{}
	body, err = r.renderInformation(request, "web/campaigns.html", data)
	if err != nil || bytes.Contains(body, []byte("progressbar")) || bytes.Contains(body, []byte("aria-valuenow")) {
		t.Fatal("closeout gained active-goal meter")
	}
	informationNeed(t, body, "No active campaigns right now.", "Generated completed outcome", "plain record, no targets or scores.")
	for _, v := range []any{int64(0), int64(199), int64(-1), float64(33.125), "1000000000000000000", "0.000"} {
		raw, money, truth, ok := informationNumber(v)
		if !ok || raw != pongo2.AsValue(v).String() || money != fmt.Sprintf("%.2f", pongo2.AsValue(v).Float()/100) || truth != pongo2.AsValue(v).IsTrue() {
			t.Fatal("existing numeric rendering changed")
		}
	}
}
func TestNativeInformationEscapingTranslationAndSelfAdmission(t *testing.T) {
	for _, name := range []string{"donate", "transparency", "campaigns", "partners", "my_donations"} {
		r, request, data := informationNativeFixture(t, name, name == "my_donations")
		data["language"] = "ro"
		r.catalog["Campaigns"] = translation{Text: map[int]string{0: "Campanii existente"}}
		if name == "partners" {
			data["partners"].([]map[string]any)[0]["name"] = `<script>partner</script>`
			data["partners"].([]map[string]any)[0]["place"].(map[string]any)["name"] = `<script>venue</script>`
		}
		if name == "my_donations" {
			data["donations"].([]map[string]any)[0]["external_ref"] = `<script>reference</script>`
		}
		body, err := r.renderInformation(request, "web/"+name+".html", data)
		if err != nil || bytes.Contains(body, []byte("<script>")) {
			t.Fatal("unsafe content or valid snapshot unavailable")
		}
		if name == "campaigns" {
			informationNeed(t, body, "Campanii existente")
		}
	}
	r, request, data := informationNativeFixture(t, "donate", false)
	body, err := r.renderInformation(request, "web/donate.html", data)
	if err != nil {
		t.Fatal(err)
	}
	informationNeed(t, body, "<strong>nonprofit</strong>")
	r, request, data = informationNativeFixture(t, "my_donations", false)
	if _, err := r.renderInformation(request, "web/my_donations.html", data); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("anonymous owner-receipt render admitted")
	}
	if got := r.informationBlock("en", "UNREVIEWED <script>"); got != "" {
		t.Fatal("dynamic policy text crossed trusted-copy seam")
	}
}
func TestNativeInformationMalformedSnapshotsRefuseBeforeOutput(t *testing.T) {
	for _, alter := range []func(pongo2.Context){func(d pongo2.Context) { d["campaigns"] = true }, func(d pongo2.Context) { d["campaigns"].([]map[string]any)[0]["percent"] = "NaN" }, func(d pongo2.Context) { d["campaigns"].([]map[string]any)[0]["percent"] = "100.1" }, func(d pongo2.Context) { d["campaigns"].([]map[string]any)[0]["title"] = map[string]any{} }, func(d pongo2.Context) { d["display_theme"] = "unknown" }, func(d pongo2.Context) { d["request"].(map[string]any)["path"] = "/other/" }} {
		r, request, data := informationNativeFixture(t, "campaigns", false)
		alter(data)
		w := httptest.NewRecorder()
		if err := r.Render(w, request, "web/campaigns.html", data); err == nil || w.Body.Len() != 0 {
			t.Fatal("malformed native information wrote output")
		}
	}
	r, request, data := informationNativeFixture(t, "donate", false)
	delete(data["form"].(map[string]any), "native")
	if body, err := r.renderInformation(request, "web/donate.html", data); err == nil || len(body) != 0 {
		t.Fatal("untyped safe HTML was admitted as native form")
	}
}
func TestNativeInformationCachedConcurrentRenderingAndDispatchIsolation(t *testing.T) {
	a, err := loadInformationTemplates()
	b, err2 := loadInformationTemplates()
	if err != nil || err2 != nil || a != b {
		t.Fatal("native information template not cached")
	}
	const count = 10
	var wait sync.WaitGroup
	fail := make(chan bool, count)
	for i := 0; i < count; i++ {
		r, request, data := informationNativeFixture(t, "partners", false)
		wait.Add(1)
		go func(i int, r *Renderer, request *http.Request, data pongo2.Context) {
			defer wait.Done()
			name := fmt.Sprintf("Generated request %02d", i)
			data["partners"].([]map[string]any)[0]["name"] = name
			body, err := r.renderInformation(request, "web/partners.html", data)
			fail <- err != nil || !bytes.Contains(body, []byte(name))
		}(i, r, request, data)
	}
	wait.Wait()
	close(fail)
	for failed := range fail {
		if failed {
			t.Fatal("cached template shared mutable request facts")
		}
	}
	for _, name := range []string{"web/donate.html", "web/transparency.html", "web/campaigns.html", "web/partners.html", "web/my_donations.html"} {
		if !informationTemplate(name) {
			t.Fatal("five-page dispatch missing")
		}
	}
	for _, name := range []string{"web/privacy.html", "web/terms.html", "web/you.html", "web/topic_preferences.html", "web/activity_list.html"} {
		if informationTemplate(name) {
			t.Fatal("unrelated or existing Preact page intercepted")
		}
	}
	saved := loadInformationTemplates
	defer func() { loadInformationTemplates = saved }()
	loadInformationTemplates = func() (*template.Template, error) { return nil, errors.New("synthetic template load failure") }
	r, request, data := informationNativeFixture(t, "partners", false)
	w := httptest.NewRecorder()
	if err := r.Render(w, request, "web/partners.html", data); err == nil || w.Body.Len() != 0 {
		t.Fatal("load failure wrote partial output")
	}
	broken, err := template.New("information").Parse(`{{define "information"}}prefix{{.NoSuchField}}{{end}}`)
	if err != nil {
		t.Fatal(err)
	}
	loadInformationTemplates = func() (*template.Template, error) { return broken, nil }
	w = httptest.NewRecorder()
	if err := r.Render(w, request, "web/partners.html", data); err == nil || w.Body.Len() != 0 {
		t.Fatal("execute failure wrote partial output")
	}
}
