package web

import (
	"bytes"
	"html/template"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func workflowFormTestSnapshot(name string, actor platform.Actor) map[string]any {
	defs := definitions[name]
	names := []string{}
	result := map[string]any{"errors": "", "non_field_errors": ""}
	for key, def := range defs {
		if key == "supervised" && actor.Cohort != "child" || key == "cohort" && !actor.IsStaff {
			continue
		}
		names = append(names, key)
		label := def.Label
		if label == "" {
			label = strings.ReplaceAll(key, "_", " ")
		}
		result[key] = map[string]any{"value": "", "label": label, "errors": ""}
	}
	sort.Slice(names, func(i, j int) bool { return defs[names[i]].Order < defs[names[j]].Order })
	addWorkflowSnapshot(result, name, defs, names, map[string][]option{"activity_type": {{"7", "Court"}, {"8", "Trail"}}, "secondary_types": {{"7", "Court"}, {"8", "Trail"}}})
	return result
}

func TestWorkflowOfficialImageTypedFormAndEscaping(t *testing.T) {
	actor := platform.Actor{ID: 1, Cohort: "adult"}
	snapshot := workflowFormTestSnapshot("PlaceOfficialImageForm", actor)
	original := snapshot["workflow_native"].(workflowForm)
	original.Fields[1].Value = `"><script>alert(1)</script>`
	snapshot["workflow_native"] = original
	d := &groupDetailDecoder{}
	form := (&Renderer{}).workflowForm(snapshot, "PlaceOfficialImageForm", actor, d, func(s string) string { return s })
	if d.err != nil || len(form.Fields) != 3 {
		t.Fatalf("projection: %v %#v", d.err, form)
	}
	if f := form.Field("image"); f.Type != "file" || !f.Required {
		t.Fatalf("image: %#v", f)
	}
	if f := form.Field("alt_text"); f.MaxLength != 140 || f.Required {
		t.Fatalf("alt: %#v", f)
	}
	if f := form.Field("rights_confirmed"); f.Type != "checkbox" || !f.Required || f.Value != "on" {
		t.Fatalf("rights: %#v", f)
	}
	source, err := workflowFormPages.ReadFile("pages/workflow-fields.gohtml")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := template.New("fields").Parse(string(source))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err = parsed.ExecuteTemplate(&output, "workflow-fields", form); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "<script>") || !strings.Contains(output.String(), "&lt;script&gt;") {
		t.Fatalf("unescaped form: %s", output.String())
	}
	form.Fields[1].Value = "changed"
	if snapshot["workflow_native"].(workflowForm).Fields[1].Value != original.Fields[1].Value {
		t.Fatal("projection aliases source fields")
	}
}

func TestWorkflowBoundRepairSelectionsAndUncheckedPost(t *testing.T) {
	actor := platform.Actor{ID: 1, Cohort: "child"}
	snapshot := workflowFormTestSnapshot("ActivityForm", actor)
	before := snapshot["workflow_native"].(workflowForm)
	for i := range before.Fields {
		if before.Fields[i].Type == "checkbox" {
			before.Fields[i].Checked = true
		}
	}
	snapshot["workflow_native"] = before
	request := httptest.NewRequest("POST", "/activities/new/", nil)
	request.PostForm = url.Values{"secondary_types": {"8", "7"}, "title": {"first", "last"}}
	repairWorkflowSnapshot(request, snapshot, map[string]any{}, false)
	after := snapshot["workflow_native"].(workflowForm)
	if after.Field("beginners_welcome").Checked || after.Field("supervised").Checked {
		t.Fatal("unchecked POST retained initial checked state")
	}
	chosen := []string{}
	for _, option := range after.Field("secondary_types").Options {
		if option.Selected {
			chosen = append(chosen, option.Value)
		}
	}
	if !reflect.DeepEqual(chosen, []string{"7", "8"}) {
		t.Fatalf("selection/order: %#v", chosen)
	}
	edit := workflowFormTestSnapshot("ActivityEditForm", actor)
	repairWorkflowSnapshot(request, edit, map[string]any{"activity_type": "7", "secondary_types": []string{"8"}}, true)
	primary := edit["workflow_native"].(workflowForm).Field("activity_type")
	if !primary.Disabled || primary.Value != "7" || !primary.Options[1].Selected || primary.Options[2].Selected {
		t.Fatalf("immutable primary type: %#v", primary)
	}
}

func TestWorkflowFormStrictMetadataAndCopiedChoices(t *testing.T) {
	actor := platform.Actor{ID: 1, Cohort: "adult"}
	snapshot := workflowFormTestSnapshot("PlaceProposeForm", actor)
	d := &groupDetailDecoder{}
	form := (&Renderer{}).workflowForm(snapshot, "PlaceProposeForm", actor, d, func(s string) string { return s })
	if d.err != nil {
		t.Fatal(d.err)
	}
	form.Fields[3].Options[1].Label = "changed"
	if snapshot["workflow_native"].(workflowForm).Fields[3].Options[1].Label == "changed" {
		t.Fatal("choice aliases source")
	}
	for _, bad := range []any{nil, "legacy HTML", map[string]any{"Name": "PlaceProposeForm"}, workflowForm{Name: "PlaceClaimForm"}} {
		snapshot["workflow_native"] = bad
		d = &groupDetailDecoder{}
		(&Renderer{}).workflowForm(snapshot, "PlaceProposeForm", actor, d, func(s string) string { return s })
		if d.err == nil {
			t.Fatalf("accepted malformed metadata %#v", bad)
		}
	}
}
