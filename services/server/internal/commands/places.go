package commands

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/jobs"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

type RawPlace struct {
	Source, ExternalID, Name, OSMType                              string
	OSMID                                                          *int64
	Lon, Lat                                                       float64
	Tags                                                           map[string]any
	Address                                                        map[string]string
	OpeningHours, Website, Phone, Attribution, License, Provenance string
}
type PlaceOptions struct {
	Source, City, BBox, OverpassURL, OverturePath string
	Limit                                         int
	DryRun, NoDedup, WithWebsite, Aggregate       bool
	MinConfidence                                 float64
}
type PlaceSource interface {
	Fetch(context.Context, PlaceOptions, func(RawPlace) error) error
}

//go:embed overpass-selectors.json
var selectorsJSON []byte

//go:embed overture-mapping.json
var overtureMappingJSON []byte
var selectors = func() []string { var out []string; _ = json.Unmarshal(selectorsJSON, &out); return out }()
var overtureMapping = func() map[string]map[string][]json.RawMessage {
	var out map[string]map[string][]json.RawMessage
	_ = json.Unmarshal(overtureMappingJSON, &out)
	return out
}()

func MatchOverture(category string, alternates []string) []jobs.PlaceMatch {
	out := []jobs.PlaceMatch{}
	indices := map[string]int{}
	offer := func(slug, rule string, confidence float64) {
		i, ok := indices[slug]
		if !ok {
			indices[slug] = len(out)
			out = append(out, jobs.PlaceMatch{Slug: slug, RuleID: rule, Confidence: confidence})
		} else if out[i].Confidence < confidence {
			out[i] = jobs.PlaceMatch{Slug: slug, RuleID: rule, Confidence: confidence}
		}
	}
	match := func(cat string, scale float64) {
		cat = strings.ToLower(strings.TrimSpace(cat))
		if v, ok := overtureMapping["OVERTURE_CATEGORY_MAP"][cat]; ok {
			var slug string
			var confidence float64
			_ = json.Unmarshal(v[0], &slug)
			_ = json.Unmarshal(v[1], &confidence)
			offer(slug, "overture:"+cat, math.RoundToEven(confidence*scale*1000)/1000)
		}
		if v, ok := overtureMapping["OVERTURE_GENERIC"][cat]; ok {
			var slugs []string
			var confidence float64
			_ = json.Unmarshal(v[0], &slugs)
			_ = json.Unmarshal(v[1], &confidence)
			for _, slug := range slugs {
				offer(slug, "overture:"+cat, math.RoundToEven(confidence*scale*1000)/1000)
			}
		}
	}
	match(category, 1)
	for _, cat := range alternates {
		match(cat, .7)
	}
	return out
}
func OverpassQuery(city, bbox string) (string, error) {
	region := ""
	if bbox != "" {
		parts := strings.Split(bbox, ",")
		if len(parts) != 4 {
			return "", platform.ErrInvalid
		}
		coordinates := []float64{}
		for _, part := range parts {
			n, err := strconv.ParseFloat(part, 64)
			if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
				return "", platform.ErrInvalid
			}
			coordinates = append(coordinates, n)
		}
		if math.Abs(coordinates[0]) > 180 || math.Abs(coordinates[2]) > 180 || math.Abs(coordinates[1]) > 90 || math.Abs(coordinates[3]) > 90 || coordinates[0] > coordinates[2] || coordinates[1] > coordinates[3] {
			return "", platform.ErrInvalid
		}
		region = fmt.Sprintf("(%g,%g,%g,%g)", coordinates[1], coordinates[0], coordinates[3], coordinates[2])
	} else {
		if city == "" || len([]rune(city)) > 128 {
			return "", platform.ErrInvalid
		}
		region = "(area.a)"
	}
	var out strings.Builder
	out.WriteString("[out:json][timeout:180];\n")
	if bbox == "" {
		out.WriteString("area[\"name\"=" + strconv.Quote(city) + "][\"boundary\"=\"administrative\"]->.a;\n")
	}
	out.WriteString("(\n")
	for _, selector := range selectors {
		out.WriteString("  nwr" + selector + region + ";\n")
	}
	out.WriteString(");\nout center tags;")
	return out.String(), nil
}
func ParseOverpass(raw []byte) ([]RawPlace, error) {
	var data struct {
		Elements []struct {
			Type     string
			ID       int64
			Lon, Lat *float64
			Center   *struct{ Lon, Lat *float64 }
			Tags     map[string]any
		}
	}
	if len(raw) > 200<<20 || json.Unmarshal(raw, &data) != nil {
		return nil, platform.ErrInvalid
	}
	out := []RawPlace{}
	for _, e := range data.Elements {
		lon, lat := e.Lon, e.Lat
		if e.Type != "node" && e.Center != nil {
			lon, lat = e.Center.Lon, e.Center.Lat
		}
		if lon == nil || lat == nil {
			continue
		}
		if e.Type != "node" && e.Type != "way" && e.Type != "relation" {
			continue
		}
		get := func(keys ...string) string {
			for _, k := range keys {
				if v, ok := e.Tags[k].(string); ok && v != "" {
					return v
				}
			}
			return ""
		}
		id := e.ID
		out = append(out, RawPlace{Source: "osm", OSMType: e.Type, OSMID: &id, Name: get("name"), Lon: *lon, Lat: *lat, Tags: e.Tags, Address: map[string]string{"street": get("addr:street"), "housenumber": get("addr:housenumber"), "city": get("addr:city"), "postcode": get("addr:postcode"), "country": get("addr:country")}, OpeningHours: get("opening_hours"), Website: get("website", "contact:website", "url", "contact:url"), Phone: get("phone", "contact:phone")})
	}
	return out, nil
}
func (s *Service) ingestPlaces(ctx context.Context, input map[string]json.RawMessage) (any, error) {
	var in struct {
		Source, City, BBox string
		OverpassURL        string `json:"overpass_url"`
		OverturePath       string `json:"overture_path"`
		DryRun             bool   `json:"dry_run"`
		Limit              int
		NoDedup            bool `json:"no_dedup"`
		Dedup              *bool
		MinConfidence      float64 `json:"min_confidence"`
		WithWebsite        bool    `json:"with_website"`
		Aggregate          bool
	}
	if err := options(input, &in); err != nil {
		return nil, err
	}
	if in.Source == "" {
		in.Source = "osm"
	}
	if in.City == "" {
		in.City = s.Config.DefaultCity
		if in.City == "" {
			in.City = "Cluj-Napoca"
		}
	}
	if in.OverpassURL == "" {
		in.OverpassURL = s.Config.OverpassURL
	}
	if in.Dedup != nil {
		in.NoDedup = !*in.Dedup
	}
	if in.MinConfidence < 0 || in.MinConfidence > 1 || in.Limit < 0 {
		return nil, platform.ErrInvalid
	}
	opts := PlaceOptions{Source: in.Source, City: in.City, BBox: in.BBox, OverpassURL: in.OverpassURL, OverturePath: in.OverturePath, Limit: in.Limit, DryRun: in.DryRun, NoDedup: in.NoDedup, MinConfidence: in.MinConfidence, WithWebsite: in.WithWebsite, Aggregate: in.Aggregate}
	counts := map[string]int{"seen": 0, "would_map": 0, "place_created": 0, "place_updated": 0, "deduped": 0, "edge_created": 0, "edge_updated": 0, "skipped_no_website": 0}
	seen := 0
	emit := func(raw RawPlace) error {
		if opts.Limit > 0 && seen >= opts.Limit {
			return nil
		}
		seen++
		if raw.Source != opts.Source || math.IsNaN(raw.Lat) || math.IsNaN(raw.Lon) || math.Abs(raw.Lat) > 90 || math.Abs(raw.Lon) > 180 {
			return platform.ErrInvalid
		}
		if opts.WithWebsite && raw.Website == "" {
			counts["skipped_no_website"]++
			return nil
		}
		matches := jobs.MatchPlaceTags(raw.Tags)
		if raw.Source == "overture" {
			var alternate []string
			serialized, _ := json.Marshal(raw.Tags["overture:alternate"])
			_ = json.Unmarshal(serialized, &alternate)
			matches = MatchOverture(fmt.Sprint(raw.Tags["overture:category"]), alternate)
		}
		filtered := []jobs.PlaceMatch{}
		for _, m := range matches {
			if m.Confidence >= opts.MinConfidence {
				filtered = append(filtered, m)
			}
		}
		if opts.DryRun {
			counts["seen"]++
			if len(filtered) > 0 {
				counts["would_map"]++
			}
			return nil
		}
		return s.upsertPlace(ctx, raw, filtered, !opts.NoDedup, counts)
	}
	if source := s.Config.Sources[in.Source]; source != nil {
		if err := source.Fetch(ctx, opts, emit); err != nil {
			return counts, err
		}
	} else if in.Source == "osm" {
		query, err := OverpassQuery(in.City, in.BBox)
		if err != nil {
			return counts, err
		}
		if in.OverpassURL == "" {
			return counts, missingDependency("OVERPASS_URL")
		}
		var bytes []byte
		if s.Config.FetchOverpass != nil {
			bytes, err = s.Config.FetchOverpass(ctx, in.OverpassURL, query)
		} else {
			u, e := url.Parse(in.OverpassURL)
			if e != nil {
				return counts, platform.ErrInvalid
			}
			q := u.Query()
			q.Set("data", query)
			u.RawQuery = q.Encode()
			bytes, err = jobs.FetchFeed(ctx, u.String())
		}
		if err != nil {
			return counts, err
		}
		places, err := ParseOverpass(bytes)
		if err != nil {
			return counts, err
		}
		for _, raw := range places {
			if err = emit(raw); err != nil {
				return counts, err
			}
		}
	} else {
		return counts, missingDependency("place source " + in.Source)
	}
	if opts.Aggregate && (in.Source == "osm" || in.Source == "overture") {
		raw, _ := json.Marshal(map[string]any{"source": in.Source, "city": in.City, "bbox": in.BBox, "dry_run": in.DryRun})
		var args map[string]json.RawMessage
		_ = json.Unmarshal(raw, &args)
		if _, err := s.aggregate(ctx, args); err != nil {
			return counts, err
		}
	}
	return counts, nil
}
func (s *Service) upsertPlace(ctx context.Context, raw RawPlace, matches []jobs.PlaceMatch, dedup bool, counts map[string]int) error {
	return platform.Transaction(ctx, s.Runner.DB, func(tx pgx.Tx) error {
		if raw.Tags == nil {
			raw.Tags = map[string]any{}
		}
		if raw.Address == nil {
			raw.Address = map[string]string{}
		}
		tags, _ := json.Marshal(raw.Tags)
		hours, _ := json.Marshal(catalog.ParseOpeningHours(raw.OpeningHours))
		identity := raw.Source + ":" + raw.ExternalID
		if raw.Source == "osm" {
			if raw.OSMID == nil || *raw.OSMID < 1 {
				return platform.ErrInvalid
			}
			identity = raw.OSMType + ":" + fmt.Sprint(*raw.OSMID)
		} else if raw.ExternalID == "" {
			return platform.ErrInvalid
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, identity); err != nil {
			return err
		}
		var place int64
		if dedup && raw.Source != "osm" && strings.TrimSpace(raw.Name) != "" {
			rows, err := tx.Query(ctx, `SELECT id,name FROM places_place WHERE source<>$1 AND ST_DWithin(location,ST_SetSRID(ST_MakePoint($2,$3),4326)::geography,75) ORDER BY ST_Distance(location,ST_SetSRID(ST_MakePoint($2,$3),4326)::geography),id LIMIT 50`, raw.Source, raw.Lon, raw.Lat)
			if err != nil {
				return err
			}
			type candidate struct {
				id   int64
				name string
			}
			candidates := []candidate{}
			for rows.Next() {
				var c candidate
				if err = rows.Scan(&c.id, &c.name); err != nil {
					rows.Close()
					return err
				}
				candidates = append(candidates, c)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			for _, c := range candidates {
				if jobs.PlaceNameSimilarity(c.name, raw.Name) >= .82 {
					place = c.id
					break
				}
			}
			if place > 0 {
				entry, _ := json.Marshal(map[string]any{"source": raw.Source, "external_id": raw.ExternalID, "attribution": raw.Attribution, "license_name": raw.License, "provenance_url": external(raw.Provenance)})
				_, err = tx.Exec(ctx, `UPDATE places_place SET raw_tags=jsonb_set(CASE WHEN jsonb_typeof(raw_tags)='object' THEN raw_tags ELSE '{}'::jsonb END,'{merged_sources}',coalesce(raw_tags->'merged_sources','[]'::jsonb)||jsonb_build_array($2::jsonb)),last_seen_at=now(),opening_hours_raw=CASE WHEN opening_hours_raw='' THEN $3 ELSE opening_hours_raw END,opening_hours=CASE WHEN opening_hours_raw='' THEN $4::jsonb ELSE opening_hours END,website=CASE WHEN website='' THEN $5 ELSE website END,phone=CASE WHEN phone='' THEN $6 ELSE phone END,attribution=CASE WHEN attribution='' THEN $7 ELSE attribution END,license_name=CASE WHEN license_name='' THEN $8 ELSE license_name END,provenance_url=CASE WHEN provenance_url='' THEN $9 ELSE provenance_url END WHERE id=$1`, place, entry, raw.OpeningHours, hours, external(raw.Website), raw.Phone, raw.Attribution, raw.License, external(raw.Provenance))
				if err != nil {
					return err
				}
				counts["deduped"]++
			}
		}
		if place == 0 {
			err := tx.QueryRow(ctx, `SELECT id FROM places_place WHERE source=$1 AND (($1='osm' AND osm_type=$2 AND osm_id=$3) OR ($1<>'osm' AND external_id=$4)) FOR UPDATE`, raw.Source, raw.OSMType, raw.OSMID, raw.ExternalID).Scan(&place)
			if err != nil && err != pgx.ErrNoRows {
				return err
			}
			args := []any{raw.Name, raw.Source, raw.OSMType, raw.OSMID, raw.ExternalID, raw.Lon, raw.Lat, tags, raw.Address["street"], raw.Address["housenumber"], raw.Address["city"], raw.Address["postcode"], raw.Address["country"], raw.OpeningHours, hours, raw.Phone, external(raw.Website), raw.Attribution, raw.License, external(raw.Provenance)}
			if err == pgx.ErrNoRows {
				err = tx.QueryRow(ctx, `INSERT INTO places_place(name,source,osm_type,osm_id,external_id,location,raw_tags,address_street,address_housenumber,address_city,address_postcode,address_country,opening_hours_raw,opening_hours,phone,website,first_seen_at,last_seen_at,attribution,license_name,provenance_url) VALUES($1,$2,$3,$4,$5,ST_SetSRID(ST_MakePoint($6,$7),4326),$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,now(),now(),$18,$19,$20) RETURNING id`, args...).Scan(&place)
				if err != nil {
					return err
				}
				counts["place_created"]++
			} else {
				args = append(args, place)
				_, err = tx.Exec(ctx, `UPDATE places_place SET name=$1,source=$2,osm_type=$3,osm_id=$4,external_id=$5,location=ST_SetSRID(ST_MakePoint($6,$7),4326),raw_tags=$8,address_street=$9,address_housenumber=$10,address_city=$11,address_postcode=$12,address_country=$13,opening_hours_raw=$14,opening_hours=$15,phone=$16,website=$17,attribution=$18,license_name=$19,provenance_url=$20,last_seen_at=now() WHERE id=$21`, args...)
				if err != nil {
					return err
				}
				counts["place_updated"]++
			}
		}
		for _, m := range matches {
			var typ int64
			if err := tx.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug=$1`, m.Slug).Scan(&typ); err != nil {
				return err
			}
			var edge int64
			var origin string
			var confidence float64
			err := tx.QueryRow(ctx, `SELECT id,origin,confidence FROM places_placeactivity WHERE place_id=$1 AND activity_id=$2 FOR UPDATE`, place, typ).Scan(&edge, &origin, &confidence)
			if err == pgx.ErrNoRows {
				_, err = tx.Exec(ctx, `INSERT INTO places_placeactivity(place_id,activity_id,origin,confidence,source,mapping_rule,is_disputed,created_at,updated_at) VALUES($1,$2,'inferred',$3,$4,$5,false,now(),now())`, place, typ, m.Confidence, raw.Source, m.RuleID)
				if err != nil {
					return err
				}
				counts["edge_created"]++
			} else if err != nil {
				return err
			} else if origin != "confirmed" && origin != "manual" && m.Confidence >= confidence {
				_, err = tx.Exec(ctx, `UPDATE places_placeactivity SET origin='inferred',confidence=$2,source=$3,mapping_rule=$4,updated_at=now() WHERE id=$1`, edge, m.Confidence, raw.Source, m.RuleID)
				if err != nil {
					return err
				}
				counts["edge_updated"]++
			}
		}
		return nil
	})
}
