package web

import (
	"bytes"
	"strings"
	"testing"
)

func TestNativeGroupMissingRomanianCopyIsFiniteAndPreservesEnglishAndCatalogue(t *testing.T) {
	r := NewRenderer(t.TempDir())
	for _, item := range [][2]string{
		{"Title", "Titlu"},
		{"Description", "Descriere"},
		{"Cohort", "Grupă de vârstă"},
		{"This field is required.", "Acest câmp este obligatoriu."},
		{"This value is too long.", "Valoarea este prea lungă."},
		{"Enter a positive whole number.", "Introduceți un număr întreg pozitiv."},
	} {
		if got := r.groupCreateCopy("ro", item[0]); got != item[1] {
			t.Fatal("missing ordinary Romanian copy did not use its finite fallback", item[0], got)
		}
		if got := r.groupCreateCopy("en", item[0]); got != item[0] {
			t.Fatal("Romanian fallback changed English output", item[0], got)
		}
	}
	for _, message := range []string{"That action isn't available for your account.", "Check the form and try again.", "This action is temporarily unavailable. Please try again.", "PRIVATE-UNKNOWN-COPY"} {
		if got := r.groupCreateCopy("ro", message); got != message {
			t.Fatal("finite UI fallback changed a service or unclassified message", message, got)
		}
	}
	r.catalog["Title"] = translation{Text: map[int]string{0: "Titlu din catalog"}}
	if got := r.groupCreateCopy("ro", "Title"); got != "Titlu din catalog" || r.groupCreateCopy("en", "Title") != "Title" {
		t.Fatal("Go fallback overrode an existing catalogue translation or English")
	}
	// Translated copy still crosses the ordinary escaped-string boundary.
	r.catalog["Title"] = translation{Text: map[int]string{0: "<b>Titlu din catalog</b>"}}
	request, data := groupCreateProjectionFixture(true)
	data["language"] = "ro"
	raw, err := r.renderGroupCreate(request, data)
	if err != nil || bytes.Contains(raw, []byte("<b>Titlu din catalog</b>")) || !bytes.Contains(raw, []byte("&lt;b&gt;Titlu din catalog&lt;/b&gt;")) {
		t.Fatal("fallback path introduced trusted translation HTML", err)
	}
}

func TestNativeGroupRomanianFieldErrorsKeepClassificationAndControlContracts(t *testing.T) {
	for _, probe := range []struct{ field, source, romanian string }{
		{"title", "This field is required.", "Acest câmp este obligatoriu."},
		{"title", "This value is too long.", "Valoarea este prea lungă."},
		{"activity_type", "Enter a positive whole number.", "Introduceți un număr întreg pozitiv."},
	} {
		request, data := groupCreateProjectionFixture(true)
		data["language"] = "ro"
		form := data["form"].(map[string]any)
		form["errors"] = probe.field + ": " + probe.source
		r := NewRenderer(t.TempDir())
		page, err := r.groupCreatePage(request, data)
		if err != nil || page.Fields[2].Label != "Titlu" || page.Fields[3].Label != "Descriere" || page.Fields[4].Label != "Grupă de vârstă" || page.ErrorSummary != probe.field+": "+probe.romanian {
			t.Fatal("ordinary Romanian labels or classified summary missing", err)
		}
		found := false
		for _, field := range page.Fields {
			if field.Name == probe.field {
				found = field.Error == probe.romanian
			}
		}
		if !found || page.Fields[1].Options[0].Value != "7" || !page.Fields[1].Options[0].Selected || page.Fields[4].Options[0].Value != "adult" {
			t.Fatal("localized copy changed field-error association or posted choice values")
		}
		raw, err := r.renderGroupCreate(request, data)
		if err != nil || !bytes.Contains(raw, []byte(probe.romanian)) || !bytes.Contains(raw, []byte(`name="`+probe.field+`"`)) || !bytes.Contains(raw, []byte(`aria-describedby="id_`+probe.field+`_error"`)) || !bytes.Contains(raw, []byte(`action="/groups/new/"`)) {
			t.Fatal("localized form lost its fixed action, original name or accessible error", err)
		}
		data["language"] = "en"
		page, err = r.groupCreatePage(request, data)
		if err != nil || page.ErrorSummary != probe.field+": "+probe.source || page.Fields[2].Label != "Title" || page.Fields[3].Label != "Description" || page.Fields[4].Label != "Cohort" {
			t.Fatal("Romanian addendum changed the English form", err)
		}
		data["language"] = "ro"
		form["errors"] = "PRIVATE-UNCLASSIFIED: " + probe.source
		page, err = r.groupCreatePage(request, data)
		if err != nil || page.ErrorSummary != "Check the form and try again." || strings.Contains(page.ErrorSummary, "PRIVATE-") {
			t.Fatal("unclassified error acquired a field translation or leaked internal text", err)
		}
	}
}
