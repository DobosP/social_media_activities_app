package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

type Field struct {
	Kind               string
	Max                int
	Required           bool
	Min                int64
	Choices, Reference string
	Default            any
}

func text(n int, required bool) Field {
	return Field{Kind: "string", Max: n, Required: required, Default: ""}
}
func integer(min int64, required bool) Field {
	return Field{Kind: "integer", Min: min, Required: required}
}
func boolean(defaultValue bool) Field { return Field{Kind: "boolean", Default: defaultValue} }
func foreign(table string, required bool) Field {
	return Field{Kind: "integer", Min: 1, Required: required, Reference: table}
}
func choice(values, defaultValue string) Field {
	return Field{Kind: "string", Required: true, Choices: values, Default: defaultValue}
}
func webURL(n int, required bool) Field {
	return Field{Kind: "url", Max: n, Required: required, Default: ""}
}

var curatedFields = map[string]map[string]Field{
	"taxonomy.activitycategory":    {"name": text(128, true), "slug": Field{Kind: "slug", Max: 64, Required: true}, "description": text(20000, false), "parent_id": foreign("taxonomy_activitycategory", false)},
	"taxonomy.activitytype":        {"name": text(128, true), "slug": Field{Kind: "slug", Max: 64, Required: true}, "category_id": foreign("taxonomy_activitycategory", true), "parent_id": foreign("taxonomy_activitytype", false), "aliases": Field{Kind: "strings", Default: []string{}}, "is_active": boolean(true), "wellness": boolean(false), "family_friendly": boolean(false)},
	"taxonomy.activityrelation":    {"source_id": foreign("taxonomy_activitytype", true), "target_id": foreign("taxonomy_activitytype", true), "kind": choice("related,synonym,variant,requires", "related"), "symmetric": boolean(false), "note": text(255, false)},
	"places.partner":               {"name": text(255, true), "kind": choice("business,ngo,library,school,civic,healthcare,cultural,other", "business"), "blurb": text(280, false), "website": webURL(500, false), "is_verified": boolean(false), "is_active": boolean(true), "place_id": foreign("places_place", false)},
	"places.childvenueclass":       {"key": text(64, true), "label": text(120, true), "osm_match": Field{Kind: "criteria", Default: map[string]string{}}, "overture_categories": Field{Kind: "strings", Default: []string{}}, "is_active": boolean(true)},
	"places.approvedchildvenue":    {"place_id": foreign("places_place", true), "note": text(255, false)},
	"communities.area":             {"name": text(128, true), "slug": Field{Kind: "slug", Max: 96, Required: true}, "city": text(128, true), "derive_method": choice("city", "city"), "min_radius_m": Field{Kind: "integer", Min: 0, Default: int64(0)}, "is_active": boolean(true)},
	"events.eventfeed":             {"name": text(120, true), "url": webURL(500, true), "is_active": boolean(true), "place_id": foreign("places_place", false), "activity_type_id": foreign("taxonomy_activitytype", false)},
	"booking.placebookinginfo":     {"place_id": foreign("places_place", true), "provider": text(32, true), "deep_link": webURL(200, false), "instructions": text(500, false), "provider_place_ref": text(128, false)},
	"donations.campaign":           {"title": text(120, true), "slug": Field{Kind: "slug", Max: 64, Required: true}, "description": text(20000, false), "goal_cents": integer(100, true), "currency": Field{Kind: "currency", Max: 3, Default: "EUR"}, "is_active": boolean(true), "partner_id": foreign("places_partner", false), "closed_at": Field{Kind: "datetime"}, "outcome": text(280, false)},
	"donations.spendentry":         {"category": text(120, true), "amount_cents": integer(0, true), "currency": Field{Kind: "currency", Max: 3, Default: "EUR"}, "period": text(60, false), "note": text(300, false), "campaign_id": foreign("donations_campaign", false)},
	"donations.costanchor":         {"label": text(280, true), "amount_cents": integer(1, true), "currency": Field{Kind: "currency", Max: 3, Default: "EUR"}, "spend_category": text(120, false), "is_active": boolean(true)},
	"donations.inkindcontribution": {"category": text(120, true), "quantity": integer(0, false), "unit_text": text(60, false), "value_cents": integer(0, false), "currency": Field{Kind: "currency", Max: 3, Default: "EUR"}, "period": text(60, false), "note": text(300, false), "partner_id": foreign("places_partner", false)},
	"donations.civicoutcome":       {"headline": text(280, true), "detail": text(300, false), "period": text(60, false), "is_active": boolean(true), "partner_id": foreign("places_partner", false)},
}
var slugPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func cleanField(raw json.RawMessage, f Field) (any, error) {
	if string(raw) == "null" {
		if f.Required {
			return nil, platform.ErrInvalid
		}
		return nil, nil
	}
	switch f.Kind {
	case "string", "slug", "url", "currency":
		var v string
		if json.Unmarshal(raw, &v) != nil {
			return nil, platform.ErrInvalid
		}
		v = strings.TrimSpace(v)
		if f.Required && v == "" || f.Max > 0 && utf8.RuneCountInString(v) > f.Max || strings.ContainsRune(v, 0) {
			return nil, platform.ErrInvalid
		}
		if f.Choices != "" {
			found := false
			for _, value := range strings.Split(f.Choices, ",") {
				found = found || v == value
			}
			if !found {
				return nil, platform.ErrInvalid
			}
		}
		if f.Kind == "slug" && !slugPattern.MatchString(v) {
			return nil, platform.ErrInvalid
		}
		if f.Kind == "currency" && (len(v) != 3 || strings.ToUpper(v) != v || !regexp.MustCompile(`^[A-Z]{3}$`).MatchString(v)) {
			return nil, platform.ErrInvalid
		}
		if f.Kind == "url" && v != "" {
			u, err := url.Parse(v)
			if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" && (strings.ContainsAny(u.Fragment, "\r\n")) || (u.Scheme != "http" && u.Scheme != "https") {
				return nil, platform.ErrInvalid
			}
		}
		return v, nil
	case "integer":
		var n int64
		if json.Unmarshal(raw, &n) != nil || n < f.Min || n > 2147483647 {
			return nil, platform.ErrInvalid
		}
		return n, nil
	case "boolean":
		var v bool
		if json.Unmarshal(raw, &v) != nil {
			return nil, platform.ErrInvalid
		}
		return v, nil
	case "datetime":
		var v string
		if json.Unmarshal(raw, &v) != nil {
			return nil, platform.ErrInvalid
		}
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return nil, platform.ErrInvalid
		}
		return t, nil
	case "strings":
		var v []string
		if json.Unmarshal(raw, &v) != nil || len(v) > 1000 {
			return nil, platform.ErrInvalid
		}
		for _, item := range v {
			if len([]rune(item)) > 255 {
				return nil, platform.ErrInvalid
			}
		}
		return json.Marshal(v)
	case "criteria":
		var v map[string]string
		if json.Unmarshal(raw, &v) != nil || len(v) > 32 {
			return nil, platform.ErrInvalid
		}
		for k, value := range v {
			if k == "" || value == "" || len(k) > 128 || len(value) > 255 {
				return nil, platform.ErrInvalid
			}
		}
		return json.Marshal(v)
	}
	return nil, platform.ErrInvalid
}

// Save accepts only curated public-data fields. Provenance ledgers, scan verdicts,
// identity/age/consent, memberships, ciphertext and audit fields are immutable here.
func (s *Service) Save(ctx context.Context, a platform.Actor, model string, id int64, input map[string]json.RawMessage) (int64, error) {
	if err := s.Gate(ctx, a); err != nil {
		return 0, err
	}
	defs, ok := curatedFields[model]
	if !ok {
		return 0, platform.ErrForbidden
	}
	if len(input) == 0 || len(input) > len(defs) || id < 0 {
		return 0, platform.ErrInvalid
	}
	values := map[string]any{}
	for key, raw := range input {
		f, ok := defs[key]
		if !ok {
			return 0, fmt.Errorf("%w: field %s is not editable", platform.ErrInvalid, key)
		}
		v, err := cleanField(raw, f)
		if err != nil {
			return 0, fmt.Errorf("%w: field %s", platform.ErrInvalid, key)
		}
		values[key] = v
	}
	if id == 0 {
		for key, f := range defs {
			if _, ok := values[key]; !ok {
				if f.Required && f.Default == nil {
					return 0, fmt.Errorf("%w: required %s", platform.ErrInvalid, key)
				}
				v := f.Default
				if f.Kind == "strings" || f.Kind == "criteria" {
					v, _ = json.Marshal(v)
				}
				values[key] = v
			}
		}
	}
	keys := []string{}
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if err := s.gateTx(ctx, tx, a); err != nil {
			return err
		}
		if model == "events.event" && id > 0 {
			var source string
			if err := tx.QueryRow(ctx, `SELECT source FROM events_event WHERE id=$1 FOR UPDATE`, id).Scan(&source); err != nil {
				return err
			}
			if source != "manual" {
				return platform.ErrForbidden
			}
		}
		if id > 0 {
			var locked int64
			if err := tx.QueryRow(ctx, `SELECT id FROM `+pgx.Identifier{models[model].Table}.Sanitize()+` WHERE id=$1 FOR UPDATE`, id).Scan(&locked); err != nil {
				return err
			}
		}
		for key, v := range values {
			f := defs[key]
			if v != nil && f.Reference != "" {
				var exists bool
				query := `SELECT EXISTS(SELECT 1 FROM ` + pgx.Identifier{f.Reference}.Sanitize() + ` WHERE id=$1)`
				if key == "partner_id" {
					query = `SELECT EXISTS(SELECT 1 FROM places_partner WHERE id=$1 AND is_active AND is_verified)`
				}
				if err := tx.QueryRow(ctx, query, v).Scan(&exists); err != nil {
					return err
				}
				if !exists {
					return platform.ErrInvalid
				}
			}
		}
		if parent, ok := values["parent_id"].(int64); ok && id > 0 {
			table := pgx.Identifier{models[model].Table}.Sanitize()
			var cycle bool
			if err := tx.QueryRow(ctx, `WITH RECURSIVE ancestors AS(SELECT id,parent_id,ARRAY[id] visited FROM `+table+` WHERE id=$1 UNION ALL SELECT p.id,p.parent_id,ancestors.visited||p.id FROM `+table+` p JOIN ancestors ON p.id=ancestors.parent_id WHERE NOT p.id=ANY(ancestors.visited)) SELECT EXISTS(SELECT 1 FROM ancestors WHERE id=$2)`, parent, id).Scan(&cycle); err != nil {
				return err
			}
			if cycle {
				return platform.ErrInvalid
			}
		}
		if model == "places.childvenueclass" {
			if active, _ := values["is_active"].(bool); active && id == 0 {
				var criteria map[string]string
				var categories []string
				_ = json.Unmarshal(values["osm_match"].([]byte), &criteria)
				_ = json.Unmarshal(values["overture_categories"].([]byte), &categories)
				if len(criteria) == 0 && len(categories) == 0 {
					return platform.ErrInvalid
				}
			}
		}
		if model == "places.approvedchildvenue" {
			values["approved_by_id"] = a.ID
			keys = append(keys, "approved_by_id")
		}
		columns, params, updates := []string{}, []string{}, []string{}
		args := []any{}
		for _, key := range keys {
			columns = append(columns, pgx.Identifier{key}.Sanitize())
			args = append(args, values[key])
			param := "$" + fmt.Sprint(len(args))
			if f, ok := defs[key]; ok && (f.Kind == "strings" || f.Kind == "criteria") {
				param += "::jsonb"
			}
			params = append(params, param)
			updates = append(updates, pgx.Identifier{key}.Sanitize()+"="+param)
		}
		stamps := map[string][]string{"taxonomy.activitycategory": {"created_at", "updated_at"}, "taxonomy.activitytype": {"created_at", "updated_at"}, "places.partner": {"created_at", "updated_at"}, "places.childvenueclass": {"created_at"}, "places.approvedchildvenue": {"created_at"}, "communities.area": {"created_at"}, "events.eventfeed": {"created_at"}, "booking.placebookinginfo": {"created_at", "updated_at"}, "donations.campaign": {"created"}, "donations.spendentry": {"created_at"}, "donations.costanchor": {"created_at"}, "donations.inkindcontribution": {"created_at"}, "donations.civicoutcome": {"created_at"}}
		if id == 0 {
			for _, stamp := range stamps[model] {
				columns = append(columns, pgx.Identifier{stamp}.Sanitize())
				params = append(params, "now()")
			}
			if model == "events.event" {
				columns = append(columns, "source", "external_id", "created_at", "updated_at", "is_import_held", "is_tombstone", "source_category", "source_city", "source_confidence", "source_first_seen_at", "source_last_seen_at", "source_pack_id", "source_release_id", "source_snapshot_generated_at", "source_snapshot_id", "source_updated_at", "source_venue_id", "source_availability", "source_currency", "source_is_free", "source_price_max", "source_price_min", "source_recurrence", "source_timezone")
				params = append(params, "'manual'", "''", "now()", "now()", "false", "false", "''", "''", "NULL", "NULL", "NULL", "''", "''", "NULL", "''", "NULL", "''", "''", "''", "NULL", "NULL", "NULL", "''", "''")
			}
			if model == "events.eventfeed" {
				columns = append(columns, "last_status", "last_synced_at")
				params = append(params, "''", "NULL")
			}
			if err := tx.QueryRow(ctx, `INSERT INTO `+pgx.Identifier{models[model].Table}.Sanitize()+` (`+strings.Join(columns, ",")+`) VALUES (`+strings.Join(params, ",")+`) RETURNING id`, args...).Scan(&id); err != nil {
				return err
			}
		} else {
			for _, stamp := range stamps[model] {
				if stamp == "updated_at" {
					updates = append(updates, "updated_at=now()")
				}
			}
			args = append(args, id)
			if _, err := tx.Exec(ctx, `UPDATE `+pgx.Identifier{models[model].Table}.Sanitize()+` SET `+strings.Join(updates, ",")+` WHERE id=$`+fmt.Sprint(len(args)), args...); err != nil {
				return err
			}
		}
		return platform.RecordAudit(ctx, tx, a, "admin.curated_saved", fmt.Sprintf("%s:%d", model, id), map[string]any{"fields": keys})
	})
	return id, err
}
func EditableFields(model string) map[string]Field {
	out := map[string]Field{}
	for k, v := range curatedFields[model] {
		out[k] = v
	}
	return out
}

// Imported facts remain owned by their source pack. Manual event editing is a
// separate native path; releasing a held import uses ReviewEvent below.
func init() {
	curatedFields["events.event"] = map[string]Field{"title": text(255, true), "description": text(20000, false), "starts_at": Field{Kind: "datetime", Required: true}, "ends_at": Field{Kind: "datetime"}, "url": webURL(500, false), "place_id": foreign("places_place", false), "activity_type_id": foreign("taxonomy_activitytype", false), "attribution": text(255, false), "license_name": text(120, false), "provenance_url": webURL(500, false), "lifecycle_status": choice("scheduled,rescheduled,postponed,cancelled,sold_out,moved_online,expired,removed,unknown", "scheduled")}
}
