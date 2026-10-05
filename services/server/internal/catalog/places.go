package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

const placeCorrectionName = `coalesce((SELECT proposed_value FROM places_placecorrection WHERE place_id=p.id AND field='name' AND status='published' ORDER BY coalesce(published_at,created_at) DESC,id DESC LIMIT 1),'')`
const placeNameSQL = `coalesce(nullif(btrim(` + placeCorrectionName + `),''),nullif(btrim(p.name),''),(SELECT CASE WHEN coalesce(parent.slug,c.slug)='sport' THEN 'Teren ' ELSE 'Spațiu ' END||t.name FROM places_placeactivity pa JOIN taxonomy_activitytype t ON t.id=pa.activity_id JOIN taxonomy_activitycategory c ON c.id=t.category_id LEFT JOIN taxonomy_activitycategory parent ON parent.id=c.parent_id WHERE pa.place_id=p.id AND NOT pa.is_disputed ORDER BY pa.confidence DESC,pa.id DESC LIMIT 1),'')`
const placeAddressSQL = `coalesce(nullif((SELECT proposed_value FROM places_placecorrection WHERE place_id=p.id AND field='address' AND status='published' ORDER BY coalesce(published_at,created_at) DESC,id DESC LIMIT 1),''),concat_ws(', ',nullif(btrim(p.address_street||' '||p.address_housenumber),''),nullif(p.address_city,'')))`
const correctionHoursSQL = `coalesce((SELECT proposed_value FROM places_placecorrection WHERE place_id=p.id AND field='hours' AND status='published' ORDER BY coalesce(published_at,created_at) DESC,id DESC LIMIT 1),'')`
const publicActivitySQL = `a.cohort='adult' AND a.is_publicly_listed AND a.status='open' AND NOT a.is_hidden AND a.starts_at>=now() AND owner.is_active`

func hasUpcomingSQL(viewer platform.Actor) string {
	activities := publicActivitySQL
	if viewer.ID > 0 && viewer.IsActive {
		if viewer.Cohort == "" || viewer.Cohort == "unassigned" {
			activities = "false"
		} else {
			activities = `a.cohort=` + sqlText(viewer.Cohort) + ` AND NOT a.is_hidden AND a.status='open' AND a.starts_at>=now() AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=` + strconv.FormatInt(viewer.ID, 10) + ` AND b.blocked_id=a.owner_id) OR (b.blocker_id=a.owner_id AND b.blocked_id=` + strconv.FormatInt(viewer.ID, 10) + `))`
		}
	}
	return `EXISTS(SELECT 1 FROM social_activity a JOIN accounts_user owner ON owner.id=a.owner_id WHERE a.place_id=p.id AND ` + activities + `) OR EXISTS(SELECT 1 FROM events_event e WHERE e.place_id=p.id AND e.starts_at>=now() AND NOT e.is_tombstone AND NOT e.is_import_held AND e.lifecycle_status IN('scheduled','rescheduled','sold_out'))`
}
func sqlText(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

var placeBaseProperties = `jsonb_build_object('name',` + placeNameSQL + `,'display_address',` + placeAddressSQL + `,'address_street',p.address_street,'address_housenumber',p.address_housenumber,'address_city',p.address_city,'address_postcode',p.address_postcode,'address_country',p.address_country,'opening_hours_raw',p.opening_hours_raw,'opening_hours',p.opening_hours,'open_now',NULL,'categories',coalesce((SELECT jsonb_agg(cat.slug ORDER BY cat.first_edge) FROM (SELECT coalesce(parent.slug,c.slug) slug,min(pa.id) first_edge FROM places_placeactivity pa JOIN taxonomy_activitytype t ON t.id=pa.activity_id JOIN taxonomy_activitycategory c ON c.id=t.category_id LEFT JOIN taxonomy_activitycategory parent ON parent.id=c.parent_id WHERE pa.place_id=p.id AND NOT pa.is_disputed GROUP BY coalesce(parent.slug,c.slug)) cat),'[]'::jsonb),'category_labels',coalesce((SELECT jsonb_agg(cat.name ORDER BY cat.first_edge) FROM (SELECT coalesce(parent.name,c.name) name,min(pa.id) first_edge FROM places_placeactivity pa JOIN taxonomy_activitytype t ON t.id=pa.activity_id JOIN taxonomy_activitycategory c ON c.id=t.category_id LEFT JOIN taxonomy_activitycategory parent ON parent.id=c.parent_id WHERE pa.place_id=p.id AND NOT pa.is_disputed GROUP BY coalesce(parent.name,c.name)) cat),'[]'::jsonb),'has_upcoming',HASUPCOMING,'image_thumb',NULL,'website',p.website,'phone',p.phone,'is_bookable',p.website<>'','source',p.source,'osm_type',p.osm_type,'osm_id',p.osm_id,'attribution',p.attribution,'license_name',p.license_name,'provenance_url',p.provenance_url,'attribution_credit',` + creditSQL("p") + `,'activities',coalesce((SELECT jsonb_agg(jsonb_build_object('slug',t.slug,'name',t.name,'confidence',pa.confidence,'origin',pa.origin,'source',pa.source,'mapping_rule',pa.mapping_rule) ORDER BY pa.id) FROM places_placeactivity pa JOIN taxonomy_activitytype t ON t.id=pa.activity_id WHERE pa.place_id=p.id AND NOT pa.is_disputed),'[]'::jsonb),'distance_m',DISTANCE)`

func placeProjection(viewer platform.Actor, distance string) string {
	return DefaultPolicy().placeProjection(viewer, distance)
}
func (policy Policy) placeProjection(viewer platform.Actor, distance string) string {
	properties := strings.ReplaceAll(placeBaseProperties, "HASUPCOMING", "("+hasUpcomingSQL(viewer)+")")
	properties = strings.ReplaceAll(properties, "DISTANCE", distance)
	return `jsonb_build_object('id',p.id,'type','Feature','geometry',ST_AsGeoJSON(p.location::geometry)::jsonb,'properties',` + properties + `,'_corrected_hours',` + correctionHoursSQL + `,'_reports',(SELECT COUNT(*) FROM places_opennowreport WHERE place_id=p.id AND created_at>=now()-` + intervalSQL(policy.WithDefaults().OpenNowReportDecay) + `))`
}

type Near struct{ Lon, Lat, Radius *float64 }

func ParseNear(q url.Values, strict bool) (Near, error) {
	var out Near
	lonRaw, hasLon := q["near_lon"]
	latRaw, hasLat := q["near_lat"]
	if !hasLon || !hasLat {
		return out, nil
	}
	lon, e1 := strconv.ParseFloat(lonRaw[0], 64)
	lat, e2 := strconv.ParseFloat(latRaw[0], 64)
	if e1 != nil || e2 != nil || math.IsNaN(lon) || math.IsNaN(lat) || math.IsInf(lon, 0) || math.IsInf(lat, 0) || math.Abs(lon) > 180 || math.Abs(lat) > 90 {
		return out, platform.ErrInvalid
	}
	out.Lon, out.Lat = &lon, &lat
	if raw := q.Get("radius_m"); raw != "" {
		r, err := strconv.ParseFloat(raw, 64)
		if err == nil && !math.IsNaN(r) && !math.IsInf(r, 0) {
			out.Radius = &r
		} else if strict {
			return out, platform.ErrInvalid
		}
	}
	return out, nil
}
func (s *Service) finalizePlaces(ctx context.Context, data []json.RawMessage) ([]json.RawMessage, error) {
	ids := make([]int64, len(data))
	decoded := make([]map[string]any, len(data))
	for i, raw := range data {
		var obj map[string]any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&obj); err != nil {
			return nil, err
		}
		var id struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(raw, &id); err != nil {
			return nil, err
		}
		ids[i] = id.ID
		decoded[i] = obj
	}
	var visuals map[int64]any
	var err error
	if s.PlaceVisuals != nil && len(ids) > 0 {
		visuals, err = s.PlaceVisuals(ctx, s.DB, ids)
		if err != nil {
			return nil, err
		}
	}
	location, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		return nil, err
	}
	for i, obj := range decoded {
		props := obj["properties"].(map[string]any)
		corrected, _ := obj["_corrected_hours"].(string)
		var schedule Schedule
		if corrected != "" {
			schedule = ParseOpeningHours(corrected)
			props["opening_hours"] = schedule
		} else if raw := props["opening_hours"]; raw != nil {
			serialized, err := json.Marshal(raw)
			if err != nil {
				return nil, err
			}
			if json.Unmarshal(serialized, &schedule) != nil {
				schedule = nil
			}
		}
		open := OpenAt(schedule, s.Now().In(location))
		props["open_now"] = open
		reports, ok := obj["_reports"].(json.Number)
		if !ok {
			return nil, platform.ErrInvalid
		}
		reportCount, err := reports.Int64()
		if err != nil || reportCount < 0 {
			return nil, platform.ErrInvalid
		}
		if open != nil && reportCount >= int64(s.policy().OpenNowReportThreshold) {
			props["open_now"] = "unverified"
		}
		delete(obj, "_corrected_hours")
		delete(obj, "_reports")
		var visual any
		if visuals != nil {
			visual = visuals[ids[i]]
		} else if s.PlaceVisual != nil {
			visual, err = s.PlaceVisual(ctx, s.DB, ids[i])
			if err != nil {
				return nil, err
			}
		}
		if value, ok := visual.(map[string]any); ok && value["kind"] == "place_cover_photo" {
			props["image_thumb"] = value["url"]
		}
		encoded, err := json.Marshal(obj)
		if err != nil {
			return nil, err
		}
		data[i] = encoded
	}
	return data, nil
}

type PlaceQuery struct {
	City, Source, Activity, Category string
	MinConfidence                    *float64
	HasUpcoming                      *bool
	Near                             Near
	BBox                             []float64
}

func placeQuery(q url.Values) (PlaceQuery, error) {
	v := PlaceQuery{City: q.Get("city"), Source: q.Get("source"), Activity: q.Get("activity"), Category: q.Get("category")}
	if raw := q.Get("min_confidence"); raw != "" {
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return v, platform.ErrInvalid
		}
		v.MinConfidence = &n
	}
	if raw := q.Get("has_upcoming"); raw == "true" || raw == "false" {
		value := raw == "true"
		v.HasUpcoming = &value
	}
	var err error
	v.Near, err = ParseNear(q, false)
	if err != nil {
		return v, err
	}
	if raw := q.Get("in_bbox"); raw != "" {
		parts := strings.Split(raw, ",")
		if len(parts) != 4 {
			return v, platform.ErrInvalid
		}
		for _, part := range parts {
			n, err := strconv.ParseFloat(part, 64)
			if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
				return v, platform.ErrInvalid
			}
			v.BBox = append(v.BBox, n)
		}
	}
	return v, nil
}
func placeWhere(v PlaceQuery, a platform.Actor) (string, []any) {
	return DefaultPolicy().placeWhere(v, a)
}
func (policy Policy) placeWhere(v PlaceQuery, a platform.Actor) (string, []any) {
	where := policy.PlaceSQL() + ` AND ($1::text='' OR lower(p.address_city)=lower($1)) AND ($2::text='' OR lower(p.source)=lower($2)) AND ($3::text='' OR EXISTS(SELECT 1 FROM places_placeactivity pa JOIN taxonomy_activitytype t ON t.id=pa.activity_id WHERE pa.place_id=p.id AND lower(t.slug)=lower($3) AND NOT pa.is_disputed AND ($4::double precision IS NULL OR pa.confidence>=$4))) AND ($5::text='' OR EXISTS(SELECT 1 FROM places_placeactivity pa JOIN taxonomy_activitytype t ON t.id=pa.activity_id JOIN taxonomy_activitycategory c ON c.id=t.category_id LEFT JOIN taxonomy_activitycategory parent ON parent.id=c.parent_id WHERE pa.place_id=p.id AND NOT pa.is_disputed AND (lower(c.slug)=lower($5) OR lower(parent.slug)=lower($5)) AND ($4::double precision IS NULL OR pa.confidence>=$4))) AND ($4::double precision IS NULL OR EXISTS(SELECT 1 FROM places_placeactivity pa WHERE pa.place_id=p.id AND pa.confidence>=$4)) AND ($6::boolean IS NULL OR (` + hasUpcomingSQL(a) + `)=$6) AND ($7::double precision IS NULL OR $8::double precision IS NULL OR $9::double precision IS NULL OR ST_DWithin(p.location,ST_SetSRID(ST_MakePoint($7,$8),4326)::geography,$9))`
	args := []any{v.City, v.Source, v.Activity, v.MinConfidence, v.Category, v.HasUpcoming, v.Near.Lon, v.Near.Lat, v.Near.Radius}
	if len(v.BBox) == 4 {
		where += ` AND p.location::geometry && ST_MakeEnvelope($10,$11,$12,$13,4326)`
		for _, n := range v.BBox {
			args = append(args, n)
		}
	}
	return where, args
}
func (s *Service) Places(ctx context.Context, a platform.Actor, q PlaceQuery, limit, offset int) ([]json.RawMessage, int64, error) {
	where, args := s.policy().placeWhere(q, a)
	var count int64
	if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM places_place p WHERE `+where, args...).Scan(&count); err != nil {
		return nil, 0, err
	}
	distance := "NULL"
	order := "p.id"
	if q.Near.Lon != nil && q.Near.Lat != nil {
		distance = `round(ST_Distance(p.location,ST_SetSRID(ST_MakePoint($7,$8),4326)::geography)::numeric,1)`
		order = `ST_Distance(p.location,ST_SetSRID(ST_MakePoint($7,$8),4326)::geography),p.id`
	}
	next := len(args) + 1
	sql := `SELECT ` + s.policy().placeProjection(a, distance) + ` FROM places_place p WHERE ` + where + ` ORDER BY ` + order + ` LIMIT $` + strconv.Itoa(next) + ` OFFSET $` + strconv.Itoa(next+1)
	data, err := rows(ctx, s.DB, sql, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	data, err = s.finalizePlaces(ctx, data)
	return data, count, err
}
func (s *Service) Place(ctx context.Context, a platform.Actor, id int64) (json.RawMessage, error) {
	raw, err := object(ctx, s.DB, `SELECT `+s.policy().placeProjection(a, "NULL")+` FROM places_place p WHERE p.id=$1 AND `+s.policy().PlaceSQL(), id)
	if err != nil {
		return nil, err
	}
	data, err := s.finalizePlaces(ctx, []json.RawMessage{raw})
	if err != nil {
		return nil, err
	}
	return data[0], nil
}
func (s *Service) places(w http.ResponseWriter, r *http.Request) {
	a, _ := platform.ActorFrom(r)
	q, err := placeQuery(r.URL.Query())
	if err != nil {
		if q.Near.Lon == nil && q.Near.Lat == nil && r.URL.Query().Get("in_bbox") == "" && r.URL.Query().Get("min_confidence") == "" {
			platform.JSON(w, 200, map[string]any{"type": "FeatureCollection", "count": 0, "next": nil, "previous": nil, "features": []any{}})
			return
		}
		platform.Fail(w, err)
		return
	}
	size := 50
	if n, e := strconv.Atoi(r.URL.Query().Get("page_size")); e == nil && n > 0 {
		size = min(n, 500)
	}
	number := 1
	if raw := r.URL.Query().Get("page"); raw != "" && raw != "last" {
		number, err = strconv.Atoi(raw)
		if err != nil || number < 1 {
			platform.Fail(w, platform.ErrNotFound)
			return
		}
	}
	data, count, err := s.Places(r.Context(), a, q, size, (number-1)*size)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	pages := max(int64(1), (count+int64(size)-1)/int64(size))
	if r.URL.Query().Get("page") == "last" {
		number = int(pages)
		data, _, err = s.Places(r.Context(), a, q, size, (number-1)*size)
		if err != nil {
			platform.Fail(w, err)
			return
		}
	}
	if int64(number) > pages {
		platform.Fail(w, platform.ErrNotFound)
		return
	}
	link := func(n int) string {
		u := *r.URL
		values := u.Query()
		if n == 1 {
			values.Del("page")
		} else {
			values.Set("page", strconv.Itoa(n))
		}
		u.RawQuery = values.Encode()
		u.Host = r.Host
		u.Scheme = "http"
		if r.TLS != nil {
			u.Scheme = "https"
		}
		return u.String()
	}
	var next, previous any
	if int64(number) < pages {
		next = link(number + 1)
	}
	if number > 1 {
		previous = link(number - 1)
	}
	platform.JSON(w, 200, map[string]any{"type": "FeatureCollection", "count": count, "next": next, "previous": previous, "features": data})
}
func (s *Service) place(w http.ResponseWriter, r *http.Request) {
	id, err := PositiveID(r.PathValue("id"))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	a, _ := platform.ActorFrom(r)
	data, err := s.Place(r.Context(), a, id)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, data)
}
