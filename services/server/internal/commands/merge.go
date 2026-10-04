package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/jobs"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

type mergePlace struct {
	ID           int64
	Name, Source string
	Tags         map[string]any
	Distance     float64
}

func placePriority(source string) int {
	for i, v := range []string{"osm", "overture", "google", "user"} {
		if source == v {
			return i
		}
	}
	return 4
}
func (s *Service) dedupPlaces(ctx context.Context, input map[string]json.RawMessage) (any, error) {
	var in struct {
		City        string
		Apply       bool
		MaxDistance float64 `json:"max_distance_m"`
		MinName     float64 `json:"min_name_ratio"`
	}
	if err := options(input, &in); err != nil {
		return nil, err
	}
	if in.MaxDistance == 0 {
		in.MaxDistance = 75
	}
	if in.MinName == 0 {
		in.MinName = .82
	}
	if in.MaxDistance < 0 || in.MaxDistance > 10000 || in.MinName < 0 || in.MinName > 1 || math.IsNaN(in.MaxDistance) || math.IsNaN(in.MinName) {
		return nil, platform.ErrInvalid
	}
	rows, err := s.Runner.DB.Query(ctx, `SELECT id,name,source FROM places_place WHERE ($1::text='' OR lower(address_city)=lower($1)) ORDER BY id`, in.City)
	if err != nil {
		return nil, err
	}
	places := []mergePlace{}
	for rows.Next() {
		var p mergePlace
		if err = rows.Scan(&p.ID, &p.Name, &p.Source); err != nil {
			rows.Close()
			return nil, err
		}
		places = append(places, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(places, func(i, j int) bool { return placePriority(places[i].Source) > placePriority(places[j].Source) })
	counts := map[string]int{"pairs": 0, "merged": 0, "skipped_dependents": 0}
	deleted := map[int64]bool{}
	for _, p := range places {
		if deleted[p.ID] || strings.TrimSpace(p.Name) == "" {
			continue
		}
		nearby, err := s.Runner.DB.Query(ctx, `SELECT candidate.id,candidate.name,candidate.source FROM places_place candidate JOIN places_place original ON original.id=$1 WHERE candidate.id<>original.id AND ST_DWithin(candidate.location,original.location,$2) ORDER BY ST_Distance(candidate.location,original.location),candidate.id LIMIT 50`, p.ID, in.MaxDistance)
		if err != nil {
			return counts, err
		}
		candidates := []mergePlace{}
		for nearby.Next() {
			var c mergePlace
			if err = nearby.Scan(&c.ID, &c.Name, &c.Source); err != nil {
				nearby.Close()
				return counts, err
			}
			candidates = append(candidates, c)
		}
		err = nearby.Err()
		nearby.Close()
		if err != nil {
			return counts, err
		}
		var match *mergePlace
		for _, c := range candidates {
			if !deleted[c.ID] && jobs.PlaceNameSimilarity(p.Name, c.Name) >= in.MinName {
				candidate := c
				match = &candidate
				break
			}
		}
		if match == nil {
			continue
		}
		canonical, duplicate := *match, p
		if placePriority(p.Source) < placePriority(match.Source) || placePriority(p.Source) == placePriority(match.Source) && p.ID < match.ID {
			canonical, duplicate = p, *match
		}
		counts["pairs"]++
		if !in.Apply {
			continue
		}
		applied, err := s.mergePlaces(ctx, canonical.ID, duplicate.ID, false)
		if err != nil {
			return counts, err
		}
		if applied {
			counts["merged"]++
			deleted[duplicate.ID] = true
		} else {
			counts["skipped_dependents"]++
		}
	}
	return counts, nil
}
func (s *Service) aggregate(ctx context.Context, input map[string]json.RawMessage) (any, error) {
	var in struct {
		Source, City, BBox string
		DryRun             bool `json:"dry_run"`
	}
	if err := options(input, &in); err != nil {
		return nil, err
	}
	if in.Source == "" {
		in.Source = "all"
	}
	if in.Source != "all" && in.Source != "osm" && in.Source != "overture" {
		return nil, platform.ErrInvalid
	}
	var bbox []float64
	if in.BBox != "" {
		parts := strings.Split(in.BBox, ",")
		if len(parts) != 4 {
			return nil, platform.ErrInvalid
		}
		for _, part := range parts {
			var f float64
			if _, err := fmt.Sscan(part, &f); err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
				return nil, platform.ErrInvalid
			}
			bbox = append(bbox, f)
		}
	}
	where := `name='' AND source IN('osm','overture') AND ($1='all' OR source=$1) AND ($2='' OR lower(address_city)=lower($2))`
	args := []any{in.Source, in.City}
	if len(bbox) == 4 {
		where += ` AND ST_Within(location::geometry,ST_MakeEnvelope($3,$4,$5,$6,4326))`
		for _, f := range bbox {
			args = append(args, f)
		}
	}
	rows, err := s.Runner.DB.Query(ctx, `SELECT id,raw_tags FROM places_place WHERE `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	children := []mergePlace{}
	for rows.Next() {
		var p mergePlace
		var raw []byte
		if err = rows.Scan(&p.ID, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		_ = json.Unmarshal(raw, &p.Tags)
		children = append(children, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	counts := map[string]int{"would_merge": 0, "merged": 0, "skipped_no_parent": 0, "skipped_dependents": 0, "skipped_not_subvenue": 0}
	for _, child := range children {
		leisure, _ := child.Tags["leisure"].(string)
		if leisure != "pitch" && leisure != "track" && leisure != "court" && child.Tags["sport"] == nil {
			counts["skipped_not_subvenue"]++
			continue
		}
		var parent int64
		err = s.Runner.DB.QueryRow(ctx, `SELECT p.id FROM places_place p JOIN places_place child ON child.id=$1 WHERE p.id<>child.id AND p.name<>'' AND `+catalog.PublicPlaceSQL+` AND ST_DWithin(p.location,child.location,150) AND (p.raw_tags->>'leisure' IN('sports_centre','stadium','park','recreation_ground') OR p.raw_tags->>'amenity'='school') ORDER BY CASE WHEN p.raw_tags->>'leisure' IN('sports_centre','stadium') THEN 0 WHEN p.raw_tags->>'leisure' IN('park','recreation_ground') THEN 1 ELSE 2 END,ST_Distance(p.location,child.location),p.id LIMIT 1`, child.ID).Scan(&parent)
		if err == pgx.ErrNoRows {
			counts["skipped_no_parent"]++
			continue
		}
		if err != nil {
			return counts, err
		}
		blocked, err := placeDependents(ctx, s.Runner.DB, child.ID)
		if err != nil {
			return counts, err
		}
		if blocked {
			counts["skipped_dependents"]++
			continue
		}
		if in.DryRun {
			counts["would_merge"]++
			continue
		}
		applied, err := s.mergePlaces(ctx, parent, child.ID, true)
		if err != nil {
			return counts, err
		}
		if applied {
			counts["merged"]++
		} else {
			counts["skipped_dependents"]++
		}
	}
	return counts, nil
}

// Foreign-key checks protect every ownership/publication/media overlay from
// destructive merging. The source aggregation already declines dependent rows;
// native retroactive dedup applies the same conservative floor.
func placeDependents(ctx context.Context, q platform.Querier, id int64) (bool, error) {
	rows, err := q.Query(ctx, `SELECT relation.relname,attribute.attname FROM pg_constraint c JOIN pg_class relation ON relation.oid=c.conrelid JOIN pg_namespace ns ON ns.oid=relation.relnamespace JOIN pg_attribute attribute ON attribute.attrelid=c.conrelid AND attribute.attnum=c.conkey[1] WHERE c.contype='f' AND c.confrelid='places_place'::regclass AND ns.nspname=current_schema() AND relation.relname<>'places_placeactivity'`)
	if err != nil {
		return false, err
	}
	pairs := [][2]string{}
	for rows.Next() {
		var pair [2]string
		if err = rows.Scan(&pair[0], &pair[1]); err != nil {
			rows.Close()
			return false, err
		}
		pairs = append(pairs, pair)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	for _, pair := range pairs {
		var exists bool
		if err = q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+pgx.Identifier{pair[0]}.Sanitize()+` WHERE `+pgx.Identifier{pair[1]}.Sanitize()+`=$1)`, id).Scan(&exists); err != nil {
			return false, err
		}
		if exists {
			return true, nil
		}
	}
	return false, nil
}
func (s *Service) mergePlaces(ctx context.Context, canonical, duplicate int64, aggregate bool) (bool, error) {
	applied := false
	err := platform.Transaction(ctx, s.Runner.DB, func(tx pgx.Tx) error {
		for _, id := range []int64{min(canonical, duplicate), max(canonical, duplicate)} {
			var locked int64
			if err := tx.QueryRow(ctx, `SELECT id FROM places_place WHERE id=$1 FOR UPDATE`, id).Scan(&locked); err != nil {
				return err
			}
		}
		blocked, err := placeDependents(ctx, tx, duplicate)
		if err != nil || blocked {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id,activity_id,origin,confidence,source,mapping_rule,is_disputed FROM places_placeactivity WHERE place_id=$1 ORDER BY id FOR UPDATE`, duplicate)
		if err != nil {
			return err
		}
		type edge struct {
			id, activity         int64
			origin, source, rule string
			confidence           float64
			disputed             bool
		}
		edges := []edge{}
		for rows.Next() {
			var e edge
			if err = rows.Scan(&e.id, &e.activity, &e.origin, &e.confidence, &e.source, &e.rule, &e.disputed); err != nil {
				rows.Close()
				return err
			}
			edges = append(edges, e)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, e := range edges {
			if aggregate && e.disputed {
				continue
			}
			var existing int64
			var origin string
			var confidence float64
			err = tx.QueryRow(ctx, `SELECT id,origin,confidence FROM places_placeactivity WHERE place_id=$1 AND activity_id=$2 FOR UPDATE`, canonical, e.activity).Scan(&existing, &origin, &confidence)
			if err == pgx.ErrNoRows {
				if aggregate {
					_, err = tx.Exec(ctx, `INSERT INTO places_placeactivity(place_id,activity_id,origin,confidence,source,mapping_rule,is_disputed,created_at,updated_at) VALUES($1,$2,'inferred',$3,$4,$5,false,now(),now())`, canonical, e.activity, e.confidence, e.source, e.rule)
				} else {
					_, err = tx.Exec(ctx, `UPDATE places_placeactivity SET place_id=$2 WHERE id=$1`, e.id, canonical)
				}
			} else if err == nil && confidence < e.confidence && origin != "manual" && origin != "confirmed" {
				next := e.origin
				if aggregate {
					next = origin
				}
				_, err = tx.Exec(ctx, `UPDATE places_placeactivity SET origin=$2,confidence=$3,source=$4,mapping_rule=$5 WHERE id=$1`, existing, next, e.confidence, e.source, e.rule)
			}
			if err != nil {
				return err
			}
		}
		if !aggregate {
			_, err = tx.Exec(ctx, `UPDATE places_place p SET raw_tags=jsonb_set(CASE WHEN jsonb_typeof(p.raw_tags)='object' THEN p.raw_tags ELSE '{}'::jsonb END,'{merged_sources}',coalesce(p.raw_tags->'merged_sources','[]'::jsonb)||jsonb_build_array(jsonb_build_object('source',d.source,'external_id',d.external_id,'osm_type',d.osm_type,'osm_id',d.osm_id,'attribution',d.attribution,'license_name',d.license_name,'provenance_url',d.provenance_url))),opening_hours_raw=CASE WHEN p.opening_hours_raw='' THEN d.opening_hours_raw ELSE p.opening_hours_raw END,opening_hours=CASE WHEN p.opening_hours_raw='' THEN d.opening_hours ELSE p.opening_hours END FROM places_place d WHERE p.id=$1 AND d.id=$2`, canonical, duplicate)
			if err != nil {
				return err
			}
		}
		// In a dependency-free row only the duplicate edges remain to be reclaimed.
		if _, err = tx.Exec(ctx, `DELETE FROM places_activityedgevote WHERE edge_id IN(SELECT id FROM places_placeactivity WHERE place_id=$1)`, duplicate); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM places_placeactivity WHERE place_id=$1`, duplicate); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM places_place WHERE id=$1`, duplicate); err != nil {
			return err
		}
		applied = true
		return nil
	})
	return applied, err
}
