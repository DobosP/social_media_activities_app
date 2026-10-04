package commands

import (
	"context"
	"encoding/json"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

type EnrichPlace struct {
	ID                         int64
	Lat, Lon                   float64
	Name, City, Website, Phone string
	Tags                       map[string]any
}
type EnrichResult struct {
	Resolved       bool
	Tags           map[string]any
	Website, Phone string
}

func (s *Service) enrichExternal(ctx context.Context, ids []int64, google, wikidata bool, counts map[string]int) error {
	if google && s.Config.GoogleEnrich == nil {
		return missingDependency("Google enricher")
	}
	if wikidata && s.Config.WikidataEnrich == nil {
		return missingDependency("Wikidata enricher")
	}
	rows, err := s.Runner.DB.Query(ctx, `SELECT id,name,address_city,website,phone,ST_Y(location::geometry),ST_X(location::geometry),raw_tags FROM places_place WHERE id=ANY($1) ORDER BY id`, ids)
	if err != nil {
		return err
	}
	places := []EnrichPlace{}
	for rows.Next() {
		var p EnrichPlace
		var raw []byte
		if err = rows.Scan(&p.ID, &p.Name, &p.City, &p.Website, &p.Phone, &p.Lat, &p.Lon, &raw); err != nil {
			rows.Close()
			return err
		}
		_ = json.Unmarshal(raw, &p.Tags)
		places = append(places, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	save := func(p EnrichPlace, r EnrichResult) error {
		for key := range r.Tags {
			if key != "google" && key != "wikidata" && key != "wikidata_enriched" {
				return platform.ErrInvalid
			}
		}
		raw, _ := json.Marshal(r.Tags)
		_, err := s.Runner.DB.Exec(ctx, `UPDATE places_place SET website=CASE WHEN website='' THEN $2 ELSE website END,phone=CASE WHEN phone='' THEN $3 ELSE phone END,raw_tags=CASE WHEN jsonb_typeof(raw_tags)='object' THEN raw_tags ELSE '{}'::jsonb END||$4::jsonb WHERE id=$1`, p.ID, external(r.Website), r.Phone, raw)
		return err
	}
	if google {
		for i, p := range places {
			r, err := s.Config.GoogleEnrich(ctx, p)
			if err != nil {
				counts["google_error"]++
				continue
			}
			if !r.Resolved {
				counts["google_unresolved"]++
				continue
			}
			if err = save(p, r); err != nil {
				return err
			}
			counts["google_enriched"]++
			if places[i].Website == "" {
				places[i].Website = external(r.Website)
			}
			if places[i].Phone == "" {
				places[i].Phone = r.Phone
			}
		}
	}
	if wikidata {
		results, err := s.Config.WikidataEnrich(ctx, places)
		if err != nil {
			return err
		}
		for _, p := range places {
			if r, ok := results[p.ID]; ok && r.Resolved {
				if err = save(p, r); err != nil {
					return err
				}
				counts["wikidata_enriched"]++
			}
		}
	}
	return nil
}
