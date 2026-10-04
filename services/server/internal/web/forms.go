package web

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
	"net/http"
	"sort"
	"strings"
)

//go:embed forms.json
var formDefinitions []byte

type fieldDef struct {
	Kind, Label, HelpText       string
	Required, Textarea          bool
	MaxLength, MinLength, Order int
}

var definitions = func() map[string]map[string]fieldDef {
	var raw map[string]map[string]struct {
		Kind, Label        string
		HelpText           string `json:"help_text"`
		Required, Textarea bool
		MaxLength          int `json:"max_length"`
		MinLength          int `json:"min_length"`
		Order              int
	}
	_ = json.Unmarshal(formDefinitions, &raw)
	result := map[string]map[string]fieldDef{}
	for form, fields := range raw {
		result[form] = map[string]fieldDef{}
		for name, f := range fields {
			result[form][name] = fieldDef{f.Kind, f.Label, f.HelpText, f.Required, f.Textarea, f.MaxLength, f.MinLength, f.Order}
		}
	}
	return result
}()

type option struct{ Value, Label string }

func (s *Server) form(r *http.Request, a platform.Actor, name string, initial map[string]any) (map[string]any, error) {
	fields, ok := definitions[name]
	if !ok {
		return nil, platform.ErrNotFound
	}
	names := []string{}
	for key := range fields {
		if key == "supervised" && a.Cohort != "child" {
			continue
		}
		if key == "cohort" && !a.IsStaff {
			continue
		}
		names = append(names, key)
	}
	sort.Slice(names, func(i, j int) bool { return fields[names[i]].Order < fields[names[j]].Order })
	result := map[string]any{"errors": "", "non_field_errors": ""}
	options := map[string][]option{}
	if _, yes := fields["activity_type"]; yes {
		rows, err := s.DB.Query(r.Context(), `SELECT id::text,name FROM taxonomy_activitytype WHERE is_active ORDER BY name,id`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var o option
			if err = rows.Scan(&o.Value, &o.Label); err != nil {
				rows.Close()
				return nil, err
			}
			options["activity_type"] = append(options["activity_type"], o)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		options["secondary_types"] = options["activity_type"]
	}
	if _, yes := fields["place"]; yes {
		ownPending := name == "ActivityForm" || name == "ActivityEditForm" || name == "SeriesForm"
		rows, err := s.DB.Query(r.Context(), `SELECT p.id::text,p.name||CASE WHEN p.address_city<>'' THEN ' — '||p.address_city ELSE '' END FROM places_place p WHERE (`+catalog.PublicPlaceSQL+` OR ($1 AND $2='adult' AND EXISTS(SELECT 1 FROM social_userplaceproposal proposal WHERE proposal.place_id=p.id AND proposal.status='pending' AND proposal.proposer_id=$3))) ORDER BY p.name,p.id LIMIT 1000`, ownPending, a.Cohort, a.ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var o option
			if err = rows.Scan(&o.Value, &o.Label); err != nil {
				rows.Close()
				return nil, err
			}
			options["place"] = append(options["place"], o)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	options["cost_band"] = []option{{"unspecified", "Not specified"}, {"free", "Free"}, {"low", "Low cost"}, {"paid", "Paid"}}
	options["difficulty"] = []option{{"unspecified", "Not specified"}, {"easy", "Easy"}, {"moderate", "Moderate"}, {"challenging", "Challenging"}}
	options["cadence"] = []option{{"weekly", "Weekly"}, {"biweekly", "Every two weeks"}, {"monthly", "Monthly"}}
	options["cohort"] = []option{{"adult", "Adult"}, {"teen", "Teen"}, {"child", "Child"}}
	options["coarse_window"] = []option{{"weekday_daytime", "Weekday daytime"}, {"weekday_evening", "Weekday evening"}, {"weekend_daytime", "Weekend daytime"}, {"weekend_evening", "Weekend evening"}}
	all := []any{}
	var paragraphs strings.Builder
	for _, key := range names {
		f := fields[key]
		label := f.Label
		if label == "" {
			label = strings.ReplaceAll(key, "_", " ")
		}
		value := ""
		if initial[key] != nil {
			value = fmt.Sprint(initial[key])
		}
		if r.PostForm != nil {
			if values, ok := r.PostForm[key]; ok && len(values) > 0 {
				value = values[len(values)-1]
			}
		}
		required := ""
		if f.Required {
			required = " required"
		}
		attrs := ` id="id_` + escape(key) + `" name="` + escape(key) + `"` + required
		if f.MaxLength > 0 {
			attrs += fmt.Sprintf(` maxlength="%d"`, f.MaxLength)
		}
		if f.MinLength > 0 {
			attrs += fmt.Sprintf(` minlength="%d"`, f.MinLength)
		}
		widget := ""
		if choices, ok := options[key]; ok {
			multiple := ""
			if f.Kind == "ModelMultipleChoiceField" {
				multiple = ` multiple data-combobox="multiple" data-combobox-max="2"`
			} else if key == "activity_type" {
				multiple = ` data-combobox="single"`
			}
			widget = "<select" + attrs + multiple + "><option value=\"\">---------</option>"
			for _, o := range choices {
				selected := ""
				if value == o.Value {
					selected = " selected"
				}
				widget += `<option value="` + escape(o.Value) + `"` + selected + `>` + escape(o.Label) + `</option>`
			}
			widget += "</select>"
		} else if f.Textarea || f.Kind == "_logistics_field" {
			widget = "<textarea" + attrs + ">" + escape(value) + "</textarea>"
		} else {
			kind := "text"
			switch f.Kind {
			case "IntegerField", "_cost_amount_field":
				kind = "number"
			case "BooleanField":
				kind = "checkbox"
			case "FileField":
				kind = "file"
			case "EmailField":
				kind = "email"
			case "URLField":
				kind = "url"
			case "_dt_field":
				kind = "datetime-local"
			}
			extra := ""
			if f.Kind == "_cost_amount_field" {
				extra = ` step="0.01" min="0"`
			}
			if kind == "checkbox" {
				if value == "true" || value == "on" {
					extra += " checked"
				}
				value = "on"
			}
			widget = "<input type=\"" + kind + "\"" + attrs + extra + ` value="` + escape(value) + `">`
		}
		labelTag := `<label for="id_` + escape(key) + `">` + escape(label) + `</label>`
		field := map[string]any{"name": key, "value": value, "label": label, "label_tag": pongo2.AsSafeValue(labelTag), "help_text": f.HelpText, "errors": "", "widget": pongo2.AsSafeValue(widget)}
		result[key] = field
		all = append(all, field)
		paragraphs.WriteString("<p>" + labelTag + widget + "<span class=\"helptext\">" + escape(f.HelpText) + "</span></p>")
	}
	result["as_p"] = pongo2.AsSafeValue(paragraphs.String())
	result["fields"] = all
	if name == "ActivityForm" || name == "ActivityEditForm" {
		steps := []any{}
		for _, step := range []struct {
			Key, Label string
			Names      []string
		}{{"what", "Ce faceți", []string{"activity_type", "secondary_types", "title", "description"}}, {"where", "Unde", []string{"place", "supervised"}}, {"when", "Când și cât", []string{"starts_at", "ends_at", "capacity", "min_to_go", "cost_band", "cost_amount", "cost_note"}}, {"details", "Detalii", []string{"difficulty", "accessibility_notes", "beginners_welcome", "first_time_note", "meeting_point", "what_to_bring", "organizer_note"}}} {
			subset := []any{}
			for _, key := range step.Names {
				if field, ok := result[key]; ok {
					subset = append(subset, field)
				}
			}
			steps = append(steps, []any{step.Key, step.Label, subset})
		}
		result["steps"] = steps
	}
	return result, nil
}
