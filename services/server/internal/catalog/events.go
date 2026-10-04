package catalog

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func safeURLSQL(value string) string {
	return `CASE WHEN lower(btrim(` + value + `)) LIKE 'http://%' OR lower(btrim(` + value + `)) LIKE 'https://%' THEN btrim(` + value + `) ELSE '' END`
}
func creditSQL(alias string) string {
	safe := safeURLSQL(alias + ".provenance_url")
	return `CASE WHEN btrim(` + alias + `.license_name)='' AND btrim(` + alias + `.attribution)='' AND (` + safe + `)='' THEN NULL ELSE jsonb_build_object('license_name',btrim(` + alias + `.license_name),'attribution',btrim(` + alias + `.attribution),'provenance_url',` + safe + `) END`
}

type EventQuery struct {
	Activity, City, Search string
	Place                  *int64
	From, To               *time.Time
	IncludePast            bool
	Lon, Lat, Radius       *float64
}

func Bound(raw string, endDate bool) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return &t, nil
	}
	location, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		return nil, err
	}
	for _, layout := range []string{"2006-01-02", "2006-01-02T15:04:05.999999999", "2006-01-02T15:04", "2006-01-02 15:04:05.999999999", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, raw, location); err == nil {
			if layout == "2006-01-02" && endDate {
				t = t.AddDate(0, 0, 1)
			}
			return &t, nil
		}
	}
	return nil, platform.ErrInvalid
}
func parseEventQuery(q url.Values) (EventQuery, error) {
	v := EventQuery{Activity: q.Get("activity"), City: strings.TrimSpace(q.Get("city")), Search: strings.TrimSpace(q.Get("q")), IncludePast: q.Get("include_past") == "1" || q.Get("include_past") == "true"}
	if len([]rune(v.Search)) < 2 {
		v.Search = ""
	}
	if raw := q.Get("place"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return v, platform.ErrInvalid
		}
		v.Place = &id
	}
	var err error
	v.From, err = Bound(q.Get("from"), false)
	if err != nil {
		return v, err
	}
	v.To, err = Bound(q.Get("to"), true)
	if err != nil {
		return v, err
	}
	_, hasLon := q["near_lon"]
	_, hasLat := q["near_lat"]
	if hasLon && hasLat {
		lon, e1 := strconv.ParseFloat(q.Get("near_lon"), 64)
		lat, e2 := strconv.ParseFloat(q.Get("near_lat"), 64)
		if e1 != nil || e2 != nil || math.IsNaN(lon) || math.IsNaN(lat) || math.IsInf(lon, 0) || math.IsInf(lat, 0) || math.Abs(lon) > 180 || math.Abs(lat) > 90 {
			return v, platform.ErrInvalid
		}
		v.Lon, v.Lat = &lon, &lat
		if raw := q.Get("radius_m"); raw != "" {
			radius, err := strconv.ParseFloat(raw, 64)
			if err != nil || math.IsNaN(radius) || math.IsInf(radius, 0) {
				return v, platform.ErrInvalid
			}
			v.Radius = &radius
		}
	}
	return v, nil
}
func escapeLike(v string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(v)
}

const eventJoin = ` FROM events_event e LEFT JOIN places_place p ON p.id=e.place_id LEFT JOIN taxonomy_activitytype t ON t.id=e.activity_type_id `

func eventWhere(v EventQuery) (string, []any, string) {
	where := publicEventSQL + ` AND ($1::text='' OR t.slug=$1) AND ($2::bigint IS NULL OR e.place_id=$2) AND ($3::timestamptz IS NULL OR e.starts_at>=$3) AND ($4::timestamptz IS NULL OR e.starts_at<$4) AND ($5 OR (e.starts_at>=now() AND e.lifecycle_status IN('scheduled','rescheduled','sold_out'))) AND ($6::text='' OR UPPER(e.title) LIKE '%'||UPPER($6)||'%' OR UPPER(e.description) LIKE '%'||UPPER($6)||'%' OR UPPER(p.name) LIKE '%'||UPPER($6)||'%') AND ($7::text='' OR lower(p.address_city)=lower($7)) AND ($8::double precision IS NULL OR $9::double precision IS NULL OR $10::double precision IS NULL OR ST_DWithin(p.location,ST_SetSRID(ST_MakePoint($8,$9),4326)::geography,$10))`
	args := []any{v.Activity, v.Place, v.From, v.To, v.IncludePast, escapeLike(v.Search), v.City, v.Lon, v.Lat, v.Radius}
	order := "e.starts_at,e.id"
	if v.Lon != nil && v.Lat != nil {
		order = `ST_Distance(p.location,ST_SetSRID(ST_MakePoint($8,$9),4326)::geography),e.starts_at,e.id`
	}
	return where, args, order
}
func (s *Service) Event(ctx context.Context, id int64, q EventQuery) (any, error) {
	where, args, _ := eventWhere(q)
	args = append(args, id)
	return object(ctx, s.DB, `SELECT `+eventProjection+eventJoin+` WHERE e.id=$11 AND `+where, args...)
}
func (s *Service) events(w http.ResponseWriter, r *http.Request) {
	q, err := parseEventQuery(r.URL.Query())
	if err != nil {
		platform.Fail(w, err)
		return
	}
	where, args, order := eventWhere(q)
	limit, offset := paging(r)
	var count int64
	if err := s.DB.QueryRow(r.Context(), `SELECT COUNT(*)`+eventJoin+` WHERE `+where, args...).Scan(&count); err != nil {
		platform.Fail(w, err)
		return
	}
	data, err := rows(r.Context(), s.DB, `SELECT `+eventProjection+eventJoin+` WHERE `+where+` ORDER BY `+order+` LIMIT $11 OFFSET $12`, append(args, limit, offset)...)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, page(r, count, data, limit, offset))
}
func (s *Service) event(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		platform.Fail(w, platform.ErrNotFound)
		return
	}
	q, err := parseEventQuery(r.URL.Query())
	if err != nil {
		platform.Fail(w, err)
		return
	}
	data, err := s.Event(r.Context(), id, q)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, data)
}
func PositiveID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		return 0, platform.ErrNotFound
	}
	return id, nil
}
func queryError(name string) error { return fmt.Errorf("%w: %s", platform.ErrInvalid, name) }

func validateQuery(raw string) error {
	query, err := url.ParseQuery(raw)
	if err != nil || len(raw) > 4096 || len(query) > 32 {
		return platform.ErrInvalid
	}
	for _, values := range query {
		if len(values) != 1 {
			return platform.ErrInvalid
		}
	}
	return nil
}
