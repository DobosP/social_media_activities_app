package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/flosch/pongo2/v6"
)

var socialFormNames = map[string]string{"activity_create": "ActivityForm", "activity_edit": "ActivityEditForm", "series_create": "SeriesForm", "group_create": "GroupCreateForm", "gauge_create": "GaugeForm", "gauge_convert": "GaugeConvertForm"}
var socialFormTemplates = map[string]string{"activity_create": "web/activity_form.html", "activity_edit": "web/activity_edit.html", "series_create": "web/series_form.html", "group_create": "web/group_form.html", "gauge_create": "web/gauge_form.html", "gauge_convert": "web/gauge_convert.html"}

func (s *Server) socialFormPage(r *http.Request, a platform.Actor, name string) (pongo2.Context, string, error) {
	data := pongo2.Context{"user": socialActor(a)}
	initial := map[string]any{"cost_band": "unspecified", "difficulty": "unspecified", "cadence": "weekly"}
	ctx := r.Context()
	if name == "group_create" {
		if !a.IsStaff && !s.Social.AllowUserGroups {
			data["redirect"] = "/communities/"
			return data, socialFormTemplates[name], nil
		}
	} else if name != "activity_edit" && name != "gauge_convert" {
		if err := platform.Participate(ctx, s.DB, a); err != nil {
			if errors.Is(err, platform.ErrForbidden) {
				data["redirect"] = "/profile/"
				return data, socialFormTemplates[name], nil
			}
			return nil, "", err
		}
	}
	if name == "activity_edit" {
		activity, err := s.socialActivity(ctx, a, id(r, "pk"))
		if err != nil {
			return nil, "", err
		}
		var organizer bool
		err = s.DB.QueryRow(ctx, `SELECT owner_id=$1 OR EXISTS(SELECT 1 FROM social_membership m WHERE m.activity_id=a.id AND m.user_id=$1 AND m.state='member' AND m.role='co_organizer') FROM social_activity a WHERE id=$2`, a.ID, id(r, "pk")).Scan(&organizer)
		if err != nil {
			return nil, "", err
		}
		if !organizer {
			return nil, "", platform.ErrForbidden
		}
		data["activity"] = activity
		for key := range definitions["ActivityEditForm"] {
			initial[key] = activity[key]
		}
		initial["place"] = activity["place_id"]
		initial["activity_type"] = activity["activity_type_id"]
		secondary := []string{}
		for _, v := range spaRows(spaMap(activity["secondary_types"])["all"]) {
			secondary = append(secondary, fmt.Sprint(v["id"]))
		}
		initial["secondary_types"] = secondary
	}
	if name == "gauge_convert" {
		rows, err := s.socialGaugeRows(r, a, id(r, "pk"))
		if err != nil {
			return nil, "", err
		}
		if len(rows) != 1 || !spaBool(rows[0]["is_proposer"]) {
			return nil, "", platform.ErrNotFound
		}
		data["gauge"] = rows[0]
	}
	if r.Method == "GET" {
		if name == "group_create" {
			if city := strings.TrimSpace(r.URL.Query().Get("city")); city != "" {
				initial["city"] = string([]rune(city)[:min(128, utf8.RuneCountInString(city))])
			}
			if slug := r.URL.Query().Get("type"); slug != "" {
				var n int64
				if err := s.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug=$1 AND is_active`, slug).Scan(&n); err == nil {
					initial["activity_type"] = n
				}
			}
		}
		if name == "gauge_create" {
			if event, err := strconv.ParseInt(r.URL.Query().Get("event"), 10, 64); err == nil && event > 0 {
				rows, err := socialRows(ctx, s.DB, `SELECT jsonb_build_object('place',e.place_id,'activity_type',e.activity_type_id) FROM events_event e LEFT JOIN places_place p ON p.id=e.place_id WHERE e.id=$1 AND e.starts_at>=now() AND `+catalog.PolicyFromContext(r.Context()).EventSQL(), event)
				if err != nil {
					return nil, "", err
				}
				if len(rows) > 0 {
					for key, v := range rows[0] {
						initial[key] = v
					}
				}
			}
		}
		if name == "activity_create" {
			if err := s.socialDraft(r, a, initial); err != nil {
				return nil, "", err
			}
		}
	}
	zone, _ := time.LoadLocation("Europe/Bucharest")
	for _, key := range []string{"starts_at", "ends_at", "first_starts_at"} {
		if t, ok := spaDateValue(initial[key]); ok {
			initial[key] = t.In(zone).Format("2006-01-02T15:04")
		}
	}
	form, err := s.form(r, a, socialFormNames[name], initial)
	if err != nil {
		return nil, "", err
	}
	socialBoundFormRepair(r, form, initial, name == "activity_edit")
	data["form"] = form
	if name == "activity_create" || name == "activity_edit" {
		data["type_vocabulary"], err = s.socialVocabulary(r)
		if err != nil {
			return nil, "", err
		}
	}
	return data, socialFormTemplates[name], nil
}

func (s *Server) socialVocabulary(r *http.Request) ([]map[string]any, error) {
	// Aliases and symmetric synonym/variant neighbours share the source
	// concept-combobox contract; no behavioral ranking is introduced.
	return socialRows(r.Context(), s.DB, `SELECT jsonb_build_object('id',t.id,'name',t.name,'slug',t.slug,'category',COALESCE(parent.name,c.name),'aliases',COALESCE((SELECT jsonb_agg(DISTINCT alias) FROM (SELECT x.value#>>'{}' alias FROM jsonb_array_elements(CASE WHEN jsonb_typeof(t.aliases)='array' THEN t.aliases ELSE '[]'::jsonb END) x WHERE jsonb_typeof(x.value)='string' UNION SELECT peer.name FROM taxonomy_activityrelation rel JOIN taxonomy_activitytype peer ON peer.id=rel.target_id WHERE rel.source_id=t.id AND rel.kind IN ('synonym','variant') AND peer.is_active UNION SELECT peer.slug FROM taxonomy_activityrelation rel JOIN taxonomy_activitytype peer ON peer.id=rel.target_id WHERE rel.source_id=t.id AND rel.kind IN ('synonym','variant') AND peer.is_active UNION SELECT peer.name FROM taxonomy_activityrelation rel JOIN taxonomy_activitytype peer ON peer.id=rel.source_id WHERE rel.target_id=t.id AND rel.symmetric AND rel.kind IN ('synonym','variant') AND peer.is_active UNION SELECT peer.slug FROM taxonomy_activityrelation rel JOIN taxonomy_activitytype peer ON peer.id=rel.source_id WHERE rel.target_id=t.id AND rel.symmetric AND rel.kind IN ('synonym','variant') AND peer.is_active) al WHERE alias<>''),'[]'::jsonb)) FROM taxonomy_activitytype t JOIN taxonomy_activitycategory c ON c.id=t.category_id LEFT JOIN taxonomy_activitycategory parent ON parent.id=c.parent_id WHERE t.is_active ORDER BY t.name,t.id`)
}

func (s *Server) socialDraft(r *http.Request, a platform.Actor, initial map[string]any) error {
	ctx := r.Context()
	q := r.URL.Query()
	for _, key := range []string{"place", "activity_type"} {
		if n, e := strconv.ParseInt(q.Get(key), 10, 64); e == nil && n > 0 {
			var exists bool
			query := `SELECT EXISTS(SELECT 1 FROM taxonomy_activitytype WHERE id=$1 AND is_active)`
			if key == "place" {
				query = `SELECT EXISTS(SELECT 1 FROM places_place p WHERE p.id=$1 AND ` + catalog.PolicyFromContext(r.Context()).PlaceSQL() + `)`
			}
			if e = s.DB.QueryRow(ctx, query, n).Scan(&exists); e != nil {
				return e
			}
			if exists {
				initial[key] = n
			}
		}
	}
	if t, e := socialFormDate(q.Get("starts_at"), false); e == nil && t != nil {
		initial["starts_at"] = *t
	}
	if n, e := strconv.ParseInt(q.Get("from"), 10, 64); e == nil && n > 0 {
		var organizer bool
		e = s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM social_activity a WHERE a.id=$2 AND (a.owner_id=$1 OR EXISTS(SELECT 1 FROM social_membership m WHERE m.activity_id=a.id AND m.user_id=$1 AND m.state='member' AND m.role='co_organizer')))`, a.ID, n).Scan(&organizer)
		if e != nil {
			return e
		}
		if organizer {
			row, e := s.socialActivity(ctx, a, n)
			if e != nil {
				return e
			}
			for _, key := range []string{"title", "description", "meeting_point", "what_to_bring", "organizer_note", "cost_band", "cost_amount", "cost_note", "difficulty", "accessibility_notes", "beginners_welcome", "capacity", "min_to_go"} {
				initial[key] = row[key]
			}
			if initial["place"] == nil {
				initial["place"] = row["place_id"]
			}
			if initial["activity_type"] == nil {
				initial["activity_type"] = row["activity_type_id"]
			}
		}
	}
	if initial["activity_type"] != nil {
		tr := func(v string) string { return s.Renderer.catalog.translate(language(r), v, 1) }
		var typ string
		if err := s.DB.QueryRow(ctx, `SELECT name FROM taxonomy_activitytype WHERE id=$1 AND is_active`, initial["activity_type"]).Scan(&typ); err != nil {
			return err
		}
		place := ""
		if initial["place"] != nil {
			_ = s.DB.QueryRow(ctx, `SELECT name FROM places_place p WHERE id=$1 AND `+catalog.PolicyFromContext(r.Context()).PlaceSQL(), initial["place"]).Scan(&place)
		}
		title := typ
		if place != "" {
			title = strings.NewReplacer("%(type)s", typ, "%(place)s", place).Replace(tr("%(type)s at %(place)s"))
		}
		if initial["title"] == nil {
			initial["title"] = string([]rune(title)[:min(200, utf8.RuneCountInString(title))])
		}
		where := ""
		if place != "" {
			where = strings.ReplaceAll(tr(" at %(place)s"), "%(place)s", place)
		}
		when := ""
		if t, ok := spaDateValue(initial["starts_at"]); ok {
			when = strings.ReplaceAll(tr(" on %(when)s"), "%(when)s", t.Format("Mon 02 Jan, 15:04"))
		}
		if initial["description"] == nil {
			description := strings.NewReplacer("%(type)s", typ, "%(where)s", where, "%(when)s", when).Replace(tr("A %(type)s meetup%(where)s%(when)s. Add any details below before you post."))
			if a.Cohort == "child" || a.Cohort == "teen" {
				description += "\n\n" + tr("Tip: meet in a public place and bring a friend.")
			}
			initial["description"] = description
		}
	}
	return nil
}

func socialFormDate(raw string, required bool) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if required {
			return nil, platform.ErrInvalid
		}
		return nil, nil
	}
	zone, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		return nil, err
	}
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05"} {
		if t, e := time.ParseInLocation(layout, raw, zone); e == nil {
			if t.Format(layout) != raw {
				return nil, platform.ErrInvalid
			}
			// Django rejects an ambiguous local clock at the DST fall-back.
			// Accepting one arbitrarily could send members an hour apart.
			if t.Add(-time.Hour).In(zone).Format(layout) == raw || t.Add(time.Hour).In(zone).Format(layout) == raw {
				return nil, platform.ErrInvalid
			}
			return &t, nil
		}
	}
	return nil, platform.ErrInvalid
}

func socialPositive(raw string, required bool) (*int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if required {
			return nil, platform.ErrInvalid
		}
		return nil, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return nil, platform.ErrInvalid
	}
	return &n, nil
}

func socialCost(raw string) (*social.Decimal, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if len(raw) > 128 {
		return nil, platform.ErrInvalid
	}
	match := donationDecimal.FindStringSubmatch(raw)
	if match == nil {
		return nil, platform.ErrInvalid
	}
	parts := strings.Split(match[1], ".")
	exponent := 0
	if match[2] != "" {
		exponent, _ = strconv.Atoi(match[2])
	}
	scale := 0
	if len(parts) == 2 {
		scale = len(parts[1])
	}
	scale -= exponent
	if scale > 2 {
		return nil, platform.ErrInvalid
	}
	digits := strings.TrimLeft(strings.Join(parts, ""), "0")
	if digits == "" {
		digits = "0"
	}
	total := len(digits)
	if scale < 0 {
		total -= scale
	} else {
		total = max(total, scale)
	}
	if total > 7 || total-max(0, scale) > 5 {
		return nil, platform.ErrInvalid
	}
	cents, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return nil, platform.ErrInvalid
	}
	for i := scale; i < 2; i++ {
		cents *= 10
	}
	if cents < 0 || cents > 9999999 {
		return nil, platform.ErrInvalid
	}
	v := social.Decimal(fmt.Sprintf("%d.%02d", cents/100, cents%100))
	return &v, nil
}

// socialCleanForm accepts only declared form fields. Hidden API proxy/owner/
// cohort keys cannot cross from a classic form into a broader domain command.
func socialCleanForm(r *http.Request, a platform.Actor, name string) (map[string]any, error) {
	formName := socialFormNames[name]
	defs := definitions[formName]
	if defs == nil {
		return nil, platform.ErrInvalid
	}
	body := map[string]any{}
	for key, def := range defs {
		if key == "supervised" && a.Cohort != "child" || key == "cohort" && !a.IsStaff {
			continue
		}
		raw := strings.TrimSpace(r.PostForm.Get(key))
		if def.Required && raw == "" && def.Kind != "BooleanField" {
			return nil, fmt.Errorf("%s: This field is required.", key)
		}
		if def.MaxLength > 0 && utf8.RuneCountInString(raw) > def.MaxLength {
			return nil, fmt.Errorf("%s: This value is too long.", key)
		}
		switch key {
		case "starts_at", "ends_at", "first_starts_at":
			t, err := socialFormDate(raw, def.Required)
			if err != nil {
				return nil, fmt.Errorf("%s: Enter a valid date and time.", key)
			}
			body[key] = t
		case "place", "activity_type", "capacity", "min_to_go":
			n, err := socialPositive(raw, def.Required)
			if err != nil {
				return nil, fmt.Errorf("%s: Enter a positive whole number.", key)
			}
			body[key] = n
		case "cost_amount":
			n, err := socialCost(raw)
			if err != nil {
				return nil, fmt.Errorf("cost_amount: Enter an amount from 0 to 99999.99 with at most two decimal places.")
			}
			body[key] = n
		case "secondary_types":
			ids := []int64{}
			seen := map[int64]bool{}
			for _, v := range r.PostForm[key] {
				if strings.TrimSpace(v) == "" {
					continue
				}
				n, err := strconv.ParseInt(v, 10, 64)
				if err != nil || n < 1 {
					return nil, platform.ErrInvalid
				}
				if !seen[n] {
					ids = append(ids, n)
					seen[n] = true
				}
			}
			body[key] = ids
		case "beginners_welcome", "supervised":
			body[key] = raw != "" && raw != "false" && raw != "False" && raw != "0"
		default:
			body[key] = raw
		}
	}
	if formName == "ActivityForm" || formName == "ActivityEditForm" || formName == "SeriesForm" {
		if body["cost_band"] == "" {
			body["cost_band"] = "unspecified"
		}
		if body["difficulty"] == "" {
			body["difficulty"] = "unspecified"
		}
		if v, ok := body["cost_amount"].(*social.Decimal); ok && v != nil {
			if body["cost_band"] == "free" {
				return nil, fmt.Errorf("cost_amount: A free meetup can't also have a cost amount.")
			}
			if body["cost_band"] == "unspecified" {
				body["cost_band"] = "paid"
			}
		}
		startKey := "starts_at"
		if formName == "SeriesForm" {
			startKey = "first_starts_at"
		}
		start, _ := body[startKey].(*time.Time)
		end, _ := body["ends_at"].(*time.Time)
		if start != nil && end != nil && end.Before(*start) {
			return nil, fmt.Errorf("ends_at: End time cannot be before the start time.")
		}
	}
	return body, nil
}

var socialOptionTag = regexp.MustCompile(`<option value="([0-9]+)"(?: selected)?>`)

// Keep classic source BoundField behavior, including an immutable primary type,
// selected multiple choices, hidden quote target and explicit label IDs.
func socialBoundFormRepair(r *http.Request, form map[string]any, initial map[string]any, edit bool) {
	for key, value := range form {
		field, ok := value.(map[string]any)
		if !ok || field["widget"] == nil {
			continue
		}
		field["id_for_label"] = "id_" + key
		old := fmt.Sprint(field["widget"])
		widget := old
		if key == "secondary_types" {
			selected := map[string]bool{}
			if r.Method == "POST" {
				for _, v := range r.PostForm[key] {
					selected[v] = true
				}
			} else {
				switch values := initial[key].(type) {
				case []string:
					for _, v := range values {
						selected[v] = true
					}
				case []int64:
					for _, v := range values {
						selected[strconv.FormatInt(v, 10)] = true
					}
				}
			}
			widget = socialOptionTag.ReplaceAllStringFunc(widget, func(tag string) string {
				value := socialOptionTag.FindStringSubmatch(tag)[1]
				if selected[value] {
					return `<option value="` + value + `" selected>`
				}
				return `<option value="` + value + `">`
			})
		}
		if key == "activity_type" && edit {
			selected := fmt.Sprint(initial[key])
			widget = socialOptionTag.ReplaceAllStringFunc(widget, func(tag string) string {
				value := socialOptionTag.FindStringSubmatch(tag)[1]
				if value == selected {
					return `<option value="` + value + `" selected>`
				}
				return `<option value="` + value + `">`
			})
			field["value"] = selected
			widget = strings.Replace(widget, "<select", "<select disabled", 1)
		}
		if r.Method == "POST" && strings.Contains(widget, `type="checkbox"`) && (r.PostForm.Get(key) == "" || strings.EqualFold(r.PostForm.Get(key), "false")) {
			widget = strings.ReplaceAll(widget, " checked", "")
		}
		if key == "reply_to" {
			value := ""
			if initial[key] != nil {
				value = fmt.Sprint(initial[key])
			}
			if r.Method == "POST" {
				value = r.PostForm.Get(key)
			}
			widget = `<input type="hidden" name="reply_to" id="id_reply_to" value="` + escape(value) + `">`
		}
		if widget != old {
			field["widget"] = pongo2.AsSafeValue(widget)
			if p, ok := form["as_p"]; ok {
				form["as_p"] = pongo2.AsSafeValue(strings.Replace(fmt.Sprint(p), old, widget, 1))
			}
		}
	}
}

func socialDecodeBody(body map[string]any, target any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, target); err != nil {
		return platform.ErrInvalid
	}
	return nil
}
