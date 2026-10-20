package web

// Facets are an additive read-only contract. Existing event/place response
// schemas and query behavior remain unchanged.
func publicEventFacetsSchema(paths, schemas map[string]any) {
	integer := func() map[string]any { return map[string]any{"type": "integer", "minimum": 0} }
	text := func() map[string]any { return map[string]any{"type": "string"} }
	object := func(fields map[string]any, required []string) map[string]any {
		return map[string]any{"type": "object", "properties": fields, "required": required, "additionalProperties": false}
	}
	city := object(map[string]any{"key": text(), "label": text(), "count": integer()}, []string{"key", "label", "count"})
	place := object(map[string]any{"id": map[string]any{"type": "integer", "minimum": 1}, "name": text(), "city": text(), "city_key": text(), "count": integer()}, []string{"id", "name", "city", "city_key", "count"})
	page := func(bucket any) map[string]any {
		return object(map[string]any{
			"count": integer(), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
			"offset":   map[string]any{"type": "integer", "minimum": 0, "maximum": 1000000},
			"complete": map[string]any{"type": "boolean"}, "truncated": map[string]any{"type": "boolean"},
			"results": map[string]any{"type": "array", "maxItems": 200, "items": bucket},
		}, []string{"count", "limit", "offset", "complete", "truncated", "results"})
	}
	schemas["EventFacets"] = object(map[string]any{
		"count": integer(), "missing_city_count": integer(), "missing_place_count": integer(),
		"local_poc":          map[string]any{"type": "boolean"},
		"publication_notice": map[string]any{"type": "string", "nullable": true},
		"cities":             page(city), "places": page(place),
	}, []string{"count", "missing_city_count", "missing_place_count", "local_poc", "publication_notice", "cities", "places"})
	for _, base := range []string{"/api", "/api/v1"} {
		existing := paths[base+"/events/"].(map[string]any)["get"].(map[string]any)
		parameters := []any{}
		for _, value := range existing["parameters"].([]any) {
			// Copy only the two new-operation bounds; do not modify the legacy
			// event operation's parameter maps while documenting this contract.
			parameter := value.(map[string]any)
			if name := parameter["name"]; name == "limit" || name == "offset" {
				copy := map[string]any{}
				for key, field := range parameter {
					copy[key] = field
				}
				copy["schema"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "default": 50}
				if name == "offset" {
					copy["schema"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 1000000, "default": 0}
				}
				value = copy
			}
			parameters = append(parameters, value)
		}
		paths[base+"/events/facets/"] = map[string]any{"get": map[string]any{
			"summary": "City and venue facets over matching public events", "security": []any{}, "parameters": parameters,
			"description": "Counts and bucket pages use one read snapshot over the full gated event query. City keys fold Romanian spelling aliases; labels retain stored facts. Places remain separate by ID. Shared limit/offset independently page cities and places; complete means the page contains every bucket, truncated is its inverse. Empty city/place facts are counted separately. LOCAL POC aggregates carry the publication notice. These are event inventory counts, never participation or engagement counts.",
			"responses": map[string]any{
				"200": map[string]any{"description": "Gate-filtered full-query counts and bounded facets", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/EventFacets"}}}},
				"400": map[string]any{"description": "Invalid query"}, "429": map[string]any{"description": "Rate limited"},
			},
		}}
	}
}
