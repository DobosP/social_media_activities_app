package catalog

// These projections share the canonical producer gates with the HTTP catalog.
// The exporter receives facts only: no raw OSM tags, private vote identifiers,
// account or membership fields are present in either projection.
func PublicEventsSQL() string          { return publicEventSQL }
func PublicEventProjectionSQL() string { return eventProjection }
func PlaceDisplayNameSQL() string      { return placeNameSQL }
func PlaceExportProjectionSQL() string {
	return `jsonb_build_object('id',p.id,'name',` + placeNameSQL + `,'lat',ST_Y(p.location::geometry),'lon',ST_X(p.location::geometry),'address',` + placeAddressSQL + `,'city',p.address_city,'postcode',p.address_postcode,'country',p.address_country,'website',p.website,'phone',p.phone,'opening_hours_text',coalesce((SELECT proposed_value FROM places_placecorrection WHERE place_id=p.id AND field='hours' AND status='published' ORDER BY coalesce(published_at,created_at) DESC,id DESC LIMIT 1),p.opening_hours_raw),'activity_types',coalesce((SELECT jsonb_agg(t.slug ORDER BY pa.id) FROM places_placeactivity pa JOIN taxonomy_activitytype t ON t.id=pa.activity_id WHERE pa.place_id=p.id AND NOT pa.is_disputed),'[]'::jsonb),'attribution',p.attribution,'license_name',p.license_name,'provenance_url',p.provenance_url)`
}
