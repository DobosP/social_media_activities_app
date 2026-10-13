package catalog

import (
	"encoding/json"
	"net/http"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

type eventCityFacet struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Count int64  `json:"count"`
}

type eventPlaceFacet struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	City    string `json:"city"`
	CityKey string `json:"city_key"`
	Count   int64  `json:"count"`
}

type eventFacetPage[T any] struct {
	Count     int64 `json:"count"`
	Limit     int   `json:"limit"`
	Offset    int   `json:"offset"`
	Complete  bool  `json:"complete"`
	Truncated bool  `json:"truncated"`
	Results   []T   `json:"results"`
}

func (p *eventFacetPage[T]) setBounds(limit, offset int) {
	p.Limit, p.Offset = limit, offset
	p.Complete = int64(len(p.Results)) == p.Count
	p.Truncated = !p.Complete
}

type eventFacetResponse struct {
	Count             int64                           `json:"count"`
	MissingCityCount  int64                           `json:"missing_city_count"`
	MissingPlaceCount int64                           `json:"missing_place_count"`
	LocalPOC          bool                            `json:"local_poc"`
	PublicationNotice *string                         `json:"publication_notice"`
	Cities            eventFacetPage[eventCityFacet]  `json:"cities"`
	Places            eventFacetPage[eventPlaceFacet] `json:"places"`
}

func (s *Service) eventFacets(w http.ResponseWriter, r *http.Request) {
	q, err := parseEventQuery(r.URL.Query())
	if err != nil {
		failEventQuery(w, err)
		return
	}
	where, args, _ := s.policy().eventWhere(q)
	limit, offset := paging(r)
	// One statement gives counts and both pages the same PostgreSQL read snapshot.
	// Admission precedes aggregation and pagination. No event identity or raw
	// provenance enters the projection, and same-location place IDs stay separate.
	query := `WITH eligible AS MATERIALIZED (
		SELECT p.id AS place_id,p.name AS place_name,p.address_city AS city,
		coalesce(` + cityKeySQL("p.address_city") + `,'') AS city_key,
		(` + EventLocalPOCSQL + ` OR coalesce(p.raw_tags->'roedu_local_poc'='true'::jsonb,false)) AS local_poc
		` + eventJoin + ` WHERE ` + where + `
	), city_buckets AS MATERIALIZED (
		SELECT city_key,min(city COLLATE "C") AS label,count(*) AS event_count
		FROM eligible WHERE city_key<>'' GROUP BY city_key
	), place_buckets AS MATERIALIZED (
		SELECT place_id,place_name,city,city_key,count(*) AS event_count
		FROM eligible WHERE place_id IS NOT NULL GROUP BY place_id,place_name,city,city_key
	), city_page AS (
		SELECT * FROM city_buckets ORDER BY event_count DESC,city_key COLLATE "C" LIMIT $11 OFFSET $12
	), place_page AS (
		SELECT * FROM place_buckets ORDER BY event_count DESC,place_id LIMIT $11 OFFSET $12
	)
	SELECT jsonb_build_object(
		'count',(SELECT count(*) FROM eligible),
		'missing_city_count',(SELECT count(*) FROM eligible WHERE city_key=''),
		'missing_place_count',(SELECT count(*) FROM eligible WHERE place_id IS NULL),
		'local_poc',coalesce((SELECT bool_or(local_poc) FROM eligible),false),
		'cities',jsonb_build_object('count',(SELECT count(*) FROM city_buckets),'results',
			coalesce((SELECT jsonb_agg(jsonb_build_object('key',city_key,'label',label,'count',event_count) ORDER BY event_count DESC,city_key COLLATE "C") FROM city_page),'[]'::jsonb)),
		'places',jsonb_build_object('count',(SELECT count(*) FROM place_buckets),'results',
			coalesce((SELECT jsonb_agg(jsonb_build_object('id',place_id,'name',place_name,'city',city,'city_key',city_key,'count',event_count) ORDER BY event_count DESC,place_id) FROM place_page),'[]'::jsonb)))`
	var raw json.RawMessage
	if err := s.DB.QueryRow(r.Context(), query, append(args, limit, offset)...).Scan(&raw); err != nil {
		platform.Fail(w, err)
		return
	}
	var response eventFacetResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		platform.Fail(w, err)
		return
	}
	response.Cities.setBounds(limit, offset)
	response.Places.setBounds(limit, offset)
	if response.LocalPOC {
		marker := LocalPOCMarker
		response.PublicationNotice = &marker
	}
	platform.JSON(w, http.StatusOK, response)
}
