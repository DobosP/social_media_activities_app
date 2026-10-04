package catalog

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

// Reference values are generated from the canonical Django data migrations,
// including later Romanian aliases. No users, auth data or application rows.
//
//go:embed testdata/reference_seed.json
var referenceSeed []byte

func (s *Service) Migrate(ctx context.Context) error {
	var data struct {
		Categories []struct {
			ID                      int64 `json:"id"`
			Slug, Name, Description string
			Parent                  *int64 `json:"parent_id"`
		}
		Types []struct {
			ID             int64 `json:"id"`
			Slug, Name     string
			Active         bool `json:"is_active"`
			Wellness       bool
			FamilyFriendly bool   `json:"family_friendly"`
			Category       int64  `json:"category_id"`
			Parent         *int64 `json:"parent_id"`
			Aliases        json.RawMessage
		}
		Relations []struct {
			ID         int64 `json:"id"`
			Kind, Note string
			Symmetric  bool
			Source     int64 `json:"source_id"`
			Target     int64 `json:"target_id"`
		}
		Venues []struct {
			ID         int64 `json:"id"`
			Key, Label string
			Match      json.RawMessage `json:"osm_match"`
			Categories json.RawMessage `json:"overture_categories"`
			Active     bool            `json:"is_active"`
		} `json:"child_venues"`
		ContentTypes []struct {
			ID    int64  `json:"id"`
			App   string `json:"app_label"`
			Model string
		} `json:"content_types"`
	}
	if json.Unmarshal(referenceSeed, &data) != nil {
		return fmt.Errorf("invalid native reference seed")
	}
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(683475951217)`); err != nil {
			return err
		}
		var initialized bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM taxonomy_activitytype)`).Scan(&initialized); err != nil {
			return err
		}
		if !initialized {
			for _, c := range data.Categories {
				if _, err := tx.Exec(ctx, `INSERT INTO taxonomy_activitycategory(id,slug,name,description,parent_id,created_at,updated_at) VALUES($1,$2,$3,$4,$5,now(),now())`, c.ID, c.Slug, c.Name, c.Description, c.Parent); err != nil {
					return err
				}
			}
			for _, t := range data.Types {
				if _, err := tx.Exec(ctx, `INSERT INTO taxonomy_activitytype(id,slug,name,aliases,is_active,wellness,family_friendly,category_id,parent_id,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,now(),now())`, t.ID, t.Slug, t.Name, t.Aliases, t.Active, t.Wellness, t.FamilyFriendly, t.Category, t.Parent); err != nil {
					return err
				}
			}
			for _, r := range data.Relations {
				if _, err := tx.Exec(ctx, `INSERT INTO taxonomy_activityrelation(id,kind,note,"symmetric",source_id,target_id) VALUES($1,$2,$3,$4,$5,$6)`, r.ID, r.Kind, r.Note, r.Symmetric, r.Source, r.Target); err != nil {
					return err
				}
			}
			for _, v := range data.Venues {
				if _, err := tx.Exec(ctx, `INSERT INTO places_childvenueclass(id,key,label,osm_match,overture_categories,is_active,created_at) VALUES($1,$2,$3,$4,$5,$6,now())`, v.ID, v.Key, v.Label, v.Match, v.Categories, v.Active); err != nil {
					return err
				}
			}
			for _, table := range []string{"taxonomy_activitycategory", "taxonomy_activitytype", "taxonomy_activityrelation", "places_childvenueclass"} {
				if _, err := tx.Exec(ctx, `SELECT setval(pg_get_serial_sequence($1,'id'),COALESCE((SELECT MAX(id) FROM `+table+`),1),true)`, table); err != nil {
					return err
				}
			}
		}
		var hasTypes bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM django_content_type)`).Scan(&hasTypes); err != nil {
			return err
		}
		if !hasTypes {
			for _, c := range data.ContentTypes {
				if _, err := tx.Exec(ctx, `INSERT INTO django_content_type(id,app_label,model) VALUES($1,$2,$3)`, c.ID, c.App, c.Model); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `SELECT setval(pg_get_serial_sequence('django_content_type','id'),COALESCE((SELECT MAX(id) FROM django_content_type),1),true)`); err != nil {
				return err
			}
		}
		return nil
	})
}
