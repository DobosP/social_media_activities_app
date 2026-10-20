package web

import (
	"embed"
	"fmt"
	"net/http"
	"strings"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

// workflowForm is an additive, copied presentation snapshot beside the legacy
// widgets. It carries no trusted HTML and never decides validation or authority.
//
//go:embed pages/workflow-fields.gohtml
var workflowFormPages embed.FS

type workflowForm struct {
	Name                   string
	Fields                 []workflowField
	Steps                  []workflowStep
	Errors, NonFieldErrors string
}
type workflowField struct {
	Name, Type, Value, Label, Help, Errors, Combo string
	Required, Checked, Multiple, Disabled         bool
	MaxLength, MinLength                          int
	Cost                                          bool
	Options                                       []workflowOption
}
type workflowOption struct {
	Value, Label string
	Selected     bool
}
type workflowStep struct {
	Key, Label    string
	Index, Number int
	Fields        []workflowField
}

// Called once after the current form producer has assembled its authoritative
// options and values. Existing widgets, queries and legacy form output remain.
func addWorkflowSnapshot(result map[string]any, name string, fields map[string]fieldDef, names []string, options map[string][]option) {
	switch name {
	case "ActivityForm", "ActivityEditForm", "PostForm", "PlaceProposeForm", "PlaceClaimForm", "PlaceOfficialImageForm":
	default:
		return
	}
	snapshot := workflowForm{Name: name}
	for _, key := range names {
		def := fields[key]
		current, _ := result[key].(map[string]any)
		value, _ := current["value"].(string)
		label, _ := current["label"].(string)
		f := workflowField{Name: key, Type: "text", Value: value, Label: label, Help: def.HelpText, Required: def.Required, MaxLength: def.MaxLength, MinLength: def.MinLength}
		if choices, exists := options[key]; exists {
			f.Type = "select"
			f.Multiple = def.Kind == "ModelMultipleChoiceField"
			if f.Multiple {
				f.Combo = "multiple"
			} else if key == "activity_type" {
				f.Combo = "single"
			}
			f.Options = append(f.Options, workflowOption{})
			f.Options[0].Label = "---------"
			for _, o := range choices {
				f.Options = append(f.Options, workflowOption{Value: o.Value, Label: o.Label, Selected: value == o.Value})
			}
		} else if def.Textarea || def.Kind == "_logistics_field" {
			f.Type = "textarea"
		} else {
			switch def.Kind {
			case "IntegerField", "_cost_amount_field":
				f.Type = "number"
			case "BooleanField":
				f.Type = "checkbox"
				f.Checked = value == "true" || value == "on"
				f.Value = "on"
			case "FileField":
				f.Type = "file"
			case "EmailField":
				f.Type = "email"
			case "URLField":
				f.Type = "url"
			case "_dt_field":
				f.Type = "datetime-local"
			}
			f.Cost = def.Kind == "_cost_amount_field"
		}
		snapshot.Fields = append(snapshot.Fields, f)
	}
	result["workflow_native"] = snapshot
}

// Apply the exact bound-widget repairs to the additive snapshot as well. This
// preserves repeated selections, immutable primary type and unchecked POSTs.
func repairWorkflowSnapshot(r *http.Request, form map[string]any, initial map[string]any, edit bool) {
	snapshot, ok := form["workflow_native"].(workflowForm)
	if !ok {
		return
	}
	for i := range snapshot.Fields {
		f := &snapshot.Fields[i]
		if f.Name == "secondary_types" {
			selected := map[string]bool{}
			if r.Method == "POST" {
				for _, value := range r.PostForm[f.Name] {
					selected[value] = true
				}
			} else {
				switch values := initial[f.Name].(type) {
				case []string:
					for _, value := range values {
						selected[value] = true
					}
				case []int64:
					for _, value := range values {
						selected[fmt.Sprint(value)] = true
					}
				}
			}
			for j := range f.Options {
				f.Options[j].Selected = selected[f.Options[j].Value]
			}
		}
		if f.Name == "activity_type" && edit {
			f.Value = fmt.Sprint(initial[f.Name])
			f.Disabled = true
			for j := range f.Options {
				f.Options[j].Selected = f.Options[j].Value == f.Value
			}
		}
		if r.Method == "POST" && f.Type == "checkbox" && (r.PostForm.Get(f.Name) == "" || strings.EqualFold(r.PostForm.Get(f.Name), "false")) {
			f.Checked = false
		}
		if f.Name == "reply_to" {
			f.Type = "hidden"
			f.Value = ""
			if initial[f.Name] != nil {
				f.Value = fmt.Sprint(initial[f.Name])
			}
			if r.Method == "POST" {
				f.Value = r.PostForm.Get(f.Name)
			}
		}
	}
	form["workflow_native"] = snapshot
}

func (r *Renderer) workflowForm(value any, name string, actor platform.Actor, d *groupDetailDecoder, tr func(string) string) workflowForm {
	row := d.row(value)
	snapshot, ok := row["workflow_native"].(workflowForm)
	if !ok || snapshot.Name != name {
		d.fail()
		return workflowForm{}
	}
	out := workflowForm{Name: name, Errors: d.optionalText(row["errors"]), NonFieldErrors: d.optionalText(row["non_field_errors"])}
	defs, ok := definitions[name]
	if !ok {
		d.fail()
		return out
	}
	seen := map[string]bool{}
	for _, original := range snapshot.Fields {
		f := original
		def, exists := defs[f.Name]
		if !exists || seen[f.Name] || f.Name == "supervised" && actor.Cohort != "child" || f.Name == "cohort" && !actor.IsStaff {
			d.fail()
		}
		seen[f.Name] = true
		f.Name = d.text(f.Name)
		f.Value = d.text(f.Value)
		f.Label = tr(d.text(f.Label))
		f.Help = tr(d.text(f.Help))
		f.Errors = d.optionalText(d.row(row[f.Name])["errors"])
		if f.Required != def.Required || f.MaxLength != def.MaxLength || f.MinLength != def.MinLength {
			d.fail()
		}
		switch f.Type {
		case "text", "number", "checkbox", "file", "email", "url", "datetime-local", "textarea", "select", "hidden":
		default:
			d.fail()
		}
		f.Options = nil
		for _, option := range original.Options {
			f.Options = append(f.Options, workflowOption{Value: d.text(option.Value), Label: tr(d.text(option.Label)), Selected: option.Selected})
		}
		out.Fields = append(out.Fields, f)
	}
	for key := range defs {
		if key == "supervised" && actor.Cohort != "child" || key == "cohort" && !actor.IsStaff {
			continue
		}
		if !seen[key] {
			d.fail()
		}
	}
	if name == "ActivityForm" || name == "ActivityEditForm" {
		for i, step := range []struct {
			key, label string
			names      []string
		}{
			{"what", "Ce faceți", []string{"activity_type", "secondary_types", "title", "description"}},
			{"where", "Unde", []string{"place", "supervised"}},
			{"when", "Când și cât", []string{"starts_at", "ends_at", "capacity", "min_to_go", "cost_band", "cost_amount", "cost_note"}},
			{"details", "Detalii", []string{"difficulty", "accessibility_notes", "beginners_welcome", "first_time_note", "meeting_point", "what_to_bring", "organizer_note"}},
		} {
			group := workflowStep{Key: step.key, Label: step.label, Index: i, Number: i + 1}
			for _, key := range step.names {
				for _, field := range out.Fields {
					if field.Name == key {
						group.Fields = append(group.Fields, field)
					}
				}
			}
			out.Steps = append(out.Steps, group)
		}
	}
	return out
}

func (f workflowForm) Field(name string) workflowField {
	for _, field := range f.Fields {
		if field.Name == name {
			return field
		}
	}
	return workflowField{}
}
