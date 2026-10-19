package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/flosch/pongo2/v6"
)

func groupCreateProjectionFixture(staff bool) (*http.Request, pongo2.Context) {
	actor := platform.Actor{ID: 7, PublicID: "PRIVATE-GROUP-PUBLIC-ID", Username: "generated-group-owner", Cohort: "adult", AgeBand: "adult", IdentityVerified: true, IsActive: true, IsStaff: staff}
	request := platform.WithActor(httptest.NewRequest("GET", "https://fixture.local/groups/new/", nil), actor)
	fields := []groupCreateInput{
		{Name: "city", Value: "Cluj-Napoca"},
		{Name: "activity_type", Value: "7", Options: []option{{Value: "7", Label: "Basketball"}, {Value: "8", Label: "Reading"}}},
		{Name: "title", Value: "Generated reading group"},
		{Name: "description", Value: "Generated description"},
	}
	if staff {
		fields = append(fields, groupCreateInput{Name: "cohort", Options: []option{{Value: "adult", Label: "Adult"}, {Value: "teen", Label: "Teen"}, {Value: "child", Label: "Child"}}})
	}
	return request, pongo2.Context{"language": "en", "display_theme": "dark", "display_text": "large", "display_motion": "reduce", "site_name": "Synthetic Activities", "csrf": "synthetic-group-proof", "connections_enabled": true, "form": map[string]any{"native_group": groupCreateForm{Staff: staff, Fields: fields}, "errors": "", "as_p": pongo2.AsSafeValue("PRIVATE-LEGACY-WIDGET-HTML"), "owner": "PRIVATE-FOREIGN-OWNER"}}
}

func TestNativeGroupProjectionEscapesValuesAndKeepsNativeFormSemantics(t *testing.T) {
	request, data := groupCreateProjectionFixture(false)
	r := NewRenderer(t.TempDir())
	form := data["form"].(map[string]any)
	snapshot := form["native_group"].(groupCreateForm)
	snapshot.Fields[0].Value = `"><script>alert("city")</script>`
	snapshot.Fields[1].Options[0].Label = `<img src=x onerror="alert(1)">`
	snapshot.Fields[2].Value = `<b>Generated & private</b>`
	snapshot.Fields[3].Value = `</textarea><script>PRIVATE-ESCAPE</script>`
	form["native_group"] = snapshot
	form["errors"] = "city: This field is required."
	raw, err := r.renderGroupCreate(request, data)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"<script", "<img src=x", "PRIVATE-LEGACY-WIDGET-HTML", "PRIVATE-FOREIGN-OWNER", "PRIVATE-GROUP-PUBLIC-ID", `name="owner"`, `name="cohort"`} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Fatal("unadmitted HTML, identity or field reached native view", forbidden)
		}
	}
	for _, want := range []string{`action="/groups/new/"`, `name="csrfmiddlewaretoken" value="synthetic-group-proof"`, `id="id_city" name="city" required maxlength="128"`, `id="id_title" name="title" required maxlength="200"`, `<option value="7" selected>`, `&lt;script&gt;`, `role="alert"`, `aria-invalid="true" aria-describedby="id_city_error"`, `id="id_city_error">This field is required.`, `&lt;b&gt;Generated &amp; private&lt;/b&gt;`, `/static/css/group-create-native.css`, `href="/communities/"`, `href="/connections/"`} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Fatal("native form contract missing", want)
		}
	}
	start := strings.Index(string(raw), `<textarea id="id_description"`)
	if start < 0 {
		t.Fatal("description control missing")
	}
	end := strings.Index(string(raw[start:]), ">")
	if end < 0 || strings.Contains(string(raw[start:start+end]), "maxlength") {
		t.Fatal("description acquired an HTML UTF-16 length restriction")
	}
	first, err := r.groupCreatePage(request, data)
	if err != nil {
		t.Fatal(err)
	}
	first.Fields[1].Options[0].Label = "changed private result"
	second, err := r.groupCreatePage(request, data)
	if err != nil || second.Fields[1].Options[0].Label == "changed private result" {
		t.Fatal("rendered option slices share mutable per-request state", err)
	}
}

func TestNativeGroupProjectionStaffChoicesUnknownValuesAndFiniteErrors(t *testing.T) {
	for _, lang := range []string{"en", "ro"} {
		request, data := groupCreateProjectionFixture(true)
		data["language"] = lang
		form := data["form"].(map[string]any)
		snapshot := form["native_group"].(groupCreateForm)
		snapshot.Fields[1].Value = "999999"
		snapshot.Fields[4].Value = "adult"
		form["native_group"] = snapshot
		form["errors"] = "PRIVATE-SQL-PROVIDER-ERROR"
		r := NewRenderer(t.TempDir())
		page, err := r.groupCreatePage(request, data)
		if err != nil || len(page.Fields) != 5 || !page.Fields[4].Options[0].Selected || page.Fields[1].Options[0].Selected || page.Fields[1].Options[1].Selected {
			t.Fatal("staff form choices or invalid selection changed", lang, err)
		}
		raw, err := r.renderGroupCreate(request, data)
		if err != nil || bytes.Contains(raw, []byte("PRIVATE-SQL-PROVIDER-ERROR")) || !bytes.Contains(raw, []byte(`name="cohort"`)) || !bytes.Contains(raw, []byte(`role="alert"`)) {
			t.Fatal("finite error mapping or staff-only control changed", lang, err)
		}
		if lang == "ro" && !bytes.Contains(raw, []byte("Cel mult 2.000 de caractere.")) {
			t.Fatal("description help missing in Romanian")
		}
	}
}

func TestNativeGroupProjectionRefusesMalformedSnapshotsBeforeWriting(t *testing.T) {
	probes := []struct {
		name   string
		change func(pongo2.Context)
	}{
		{"missing-form", func(d pongo2.Context) { delete(d, "form") }},
		{"safe-html-instead-of-semantic-data", func(d pongo2.Context) { d["form"].(map[string]any)["native_group"] = pongo2.AsSafeValue("<form>") }},
		{"wrong-staff-snapshot", func(d pongo2.Context) {
			f := d["form"].(map[string]any)
			v := f["native_group"].(groupCreateForm)
			v.Staff = true
			f["native_group"] = v
		}},
		{"missing-field", func(d pongo2.Context) {
			f := d["form"].(map[string]any)
			v := f["native_group"].(groupCreateForm)
			v.Fields = v.Fields[:3]
			f["native_group"] = v
		}},
		{"wrong-field-order", func(d pongo2.Context) {
			v := d["form"].(map[string]any)["native_group"].(groupCreateForm)
			v.Fields[0].Name = "owner"
		}},
		{"nonpositive-option", func(d pongo2.Context) {
			v := d["form"].(map[string]any)["native_group"].(groupCreateForm)
			v.Fields[1].Options[0].Value = "0"
		}},
		{"duplicate-option", func(d pongo2.Context) {
			v := d["form"].(map[string]any)["native_group"].(groupCreateForm)
			v.Fields[1].Options[1].Value = "7"
		}},
		{"unknown-theme", func(d pongo2.Context) { d["display_theme"] = "posted-unknown" }},
		{"missing-csrf", func(d pongo2.Context) { d["csrf"] = "" }},
		{"untyped-errors", func(d pongo2.Context) { d["form"].(map[string]any)["errors"] = 1 }},
	}
	for _, probe := range probes {
		t.Run(probe.name, func(t *testing.T) {
			request, data := groupCreateProjectionFixture(false)
			probe.change(data)
			w := httptest.NewRecorder()
			if err := NewRenderer(t.TempDir()).Render(w, request, "web/group_form.html", data); err == nil || w.Body.Len() != 0 || w.Header().Get("Content-Type") != "" {
				t.Fatal("malformed native snapshot wrote a response", err)
			}
		})
	}
}

func TestNativeGroupSemanticSnapshotUsesExistingBoundValuesAndCleaner(t *testing.T) {
	s, actor, _, typ := socialLegacyFixture(t)
	values := url.Values{"city": {"First city", "Last city"}, "activity_type": {fmt.Sprint(typ)}, "title": {"Generated group"}, "description": {"  Generated details  "}, "cohort": {"child"}, "owner": {"999999"}}
	request := socialLegacyRequest("POST", "/groups/new/", actor, 0, values)
	form, err := s.form(request, actor, "GroupCreateForm", nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, ok := form["native_group"].(groupCreateForm)
	if !ok || snapshot.Staff || len(snapshot.Fields) != 4 || snapshot.Fields[0].Value != "Last city" || snapshot.Fields[1].Value != fmt.Sprint(typ) || snapshot.Fields[3].Value != "  Generated details  " {
		t.Fatal("native metadata did not preserve original displayed values")
	}
	cleaned, err := socialCleanForm(request, actor, "group_create")
	if err != nil || cleaned["city"] != "First city" || cleaned["description"] != "Generated details" || cleaned["cohort"] != nil || cleaned["owner"] != nil {
		t.Fatal("native presentation changed original first-value cleaning or authority boundary", err)
	}
	if form["as_p"] == nil || form["fields"] == nil || form["city"].(map[string]any)["widget"] == nil {
		t.Fatal("legacy form fields disappeared")
	}
	other, err := s.form(socialLegacyRequest("GET", "/gauges/new/", actor, 0, nil), actor, "GaugeForm", nil)
	if err != nil || other["native_group"] != nil {
		t.Fatal("group metadata leaked into unrelated form shape", err)
	}
	// One bounded synthetic capture from this SAME actual SQL/form-builder call.
	// It contains no credential, token or real account data. The committed QA
	// extractor requires this root's actual PASS and the complete source frame.
	classic, ok := form["as_p"].(*pongo2.Value)
	if !ok {
		t.Fatal("actual legacy safe widget projection missing")
	}
	projection, err := json.Marshal(map[string]any{
		"schema": 1, "as_p": classic.String(), "native_group": snapshot, "errors": form["errors"],
		"actor": map[string]any{"id": actor.ID, "public_id": actor.PublicID, "username": actor.Username, "display_name": actor.DisplayName,
			"age_band": actor.AgeBand, "cohort": actor.Cohort, "role": actor.Role, "identity_verified": actor.IdentityVerified,
			"is_active": actor.IsActive, "is_staff": actor.IsStaff, "is_superuser": actor.IsSuperuser},
	})
	if err != nil || len(projection) > 128<<10 {
		t.Fatal("bounded actual synthetic form projection unavailable", err)
	}
	t.Log("NATIVE_GROUP_FORM_V1 " + string(projection))
}

func TestNativeGroupDescriptionPreservesSupplementaryPlaneRuneBound(t *testing.T) {
	s, actor, _, typ := socialLegacyFixture(t)
	s.Social.AllowUserGroups = true
	ctx := context.Background()
	for _, length := range []int{2000, 2001} {
		description := strings.Repeat("😀", length)
		request := socialLegacyRequest("POST", "/groups/new/", actor, 0, url.Values{"city": {"Generated Unicode City"}, "activity_type": {fmt.Sprint(typ)}, "title": {"Generated Unicode group"}, "description": {"  " + description + "  "}})
		body, err := socialCleanForm(request, actor, "group_create")
		if err != nil || utf8.RuneCountInString(body["description"].(string)) != length {
			t.Fatal("existing adapter altered description rune count", length, err)
		}
		var input social.GroupInput
		if err = socialDecodeBody(body, &input); err != nil {
			t.Fatal(err)
		}
		var before int
		if err = s.DB.QueryRow(ctx, `SELECT count(*) FROM social_group WHERE owner_id=$1`, actor.ID).Scan(&before); err != nil {
			t.Fatal(err)
		}
		id, err := s.Social.CreateGroup(ctx, actor, input)
		if length == 2000 {
			if err != nil || id < 1 {
				t.Fatal("2,000 supplementary-plane characters were refused", err)
			}
			var stored string
			if err = s.DB.QueryRow(ctx, `SELECT description FROM social_group WHERE id=$1`, id).Scan(&stored); err != nil || stored != description {
				t.Fatal("accepted Unicode description was not retained exactly", err)
			}
		} else {
			var after int
			if err == nil || id != 0 {
				t.Fatal("overlong server-rune description was accepted")
			}
			if err = s.DB.QueryRow(ctx, `SELECT count(*) FROM social_group WHERE owner_id=$1`, actor.ID).Scan(&after); err != nil || after != before {
				t.Fatal("rejected description created a group", err)
			}
		}
	}
}

func TestNativeGroupRendererUsesEmbeddedTemplateAndExistingHeaders(t *testing.T) {
	request, data := groupCreateProjectionFixture(false)
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	r := NewRenderer(root)
	r.CSPEnforce = true
	w := httptest.NewRecorder()
	if err = r.Render(w, request, "web/group_form.html", data); err != nil {
		t.Fatal(err)
	}
	if w.Header().Get("Cache-Control") != "private, no-cache" || w.Header().Get("Vary") != "Cookie" || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || !strings.Contains(w.Header().Get("Content-Security-Policy"), "form-action 'self'") || w.Header().Get("Reporting-Endpoints") == "" {
		t.Fatal("group dispatch bypassed existing shared response policy")
	}
	// A source-independent temporary root has no legacy templates, but the same
	// explicitly selected cached native template must still render.
	if err = NewRenderer(t.TempDir()).Render(httptest.NewRecorder(), request, "web/group_form.html", data); err != nil {
		t.Fatal("native dispatch fell through to disk widgets/templates", err)
	}
}
