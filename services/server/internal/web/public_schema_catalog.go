package web

// These are the remaining read-only catalog/discovery shapes. Their schemas
// describe field contracts, never live records or account/person examples.
func (s *Server) publicCatalogSchema(paths, schemas map[string]any) {
	str := func() map[string]any { return map[string]any{"type": "string"} }
	nullable := func(kind string) map[string]any { return map[string]any{"type": kind, "nullable": true} }
	ref := func(name string) map[string]any { return map[string]any{"$ref": "#/components/schemas/" + name} }
	schemas["Category"] = map[string]any{"type": "object", "properties": map[string]any{"slug": str(), "name": str(), "description": str(), "parent": nullable("string")}}
	schemas["ActivityType"] = map[string]any{"type": "object", "properties": map[string]any{"slug": str(), "name": str(), "category": nullable("string"), "parent": nullable("string"), "aliases": map[string]any{"type": "array", "items": str()}, "is_active": map[string]any{"type": "boolean"}, "wellness": map[string]any{"type": "boolean"}, "family_friendly": map[string]any{"type": "boolean"}, "related": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"target": str(), "kind": str(), "symmetric": map[string]any{"type": "boolean"}, "note": str()}}}}}
	schemas["PublicActivityCard"] = map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "integer"}, "title": str(), "cohort": map[string]any{"type": "string", "enum": []string{"adult"}}, "starts_at": map[string]any{"type": "string", "format": "date-time"}, "status": map[string]any{"type": "string", "enum": []string{"open"}}, "activity_type": str(), "place_id": nullable("integer"), "distance_m": nullable("number"), "visual": map[string]any{"type": "object", "description": "Gate-filtered contextual adult cover or generated accent"}}}
	schemas["PublicGroupCard"] = map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "integer"}, "title": str(), "cohort": map[string]any{"type": "string", "enum": []string{"adult"}}, "city": str(), "activity_type": nullable("string"), "description": str()}}
	response := func(shape any) map[string]any {
		return map[string]any{"200": map[string]any{"description": "Read-only public catalog projection", "content": map[string]any{"application/json": map[string]any{"schema": shape}}}, "400": map[string]any{"description": "Invalid query"}, "404": map[string]any{"description": "Not found"}, "429": map[string]any{"description": "Rate limited"}}
	}
	for _, base := range []string{"/api", "/api/v1"} {
		for _, item := range [][2]string{{"categories", "Category"}, {"activities", "ActivityType"}} {
			collection := map[string]any{"type": "object", "properties": map[string]any{"count": map[string]any{"type": "integer"}, "next": nullable("string"), "previous": nullable("string"), "results": map[string]any{"type": "array", "items": ref(item[1])}}}
			paths[base+"/taxonomy/"+item[0]+"/"] = map[string]any{"get": map[string]any{"security": []any{}, "responses": response(collection)}}
			paths[base+"/taxonomy/"+item[0]+"/{slug}/"] = map[string]any{"get": map[string]any{"security": []any{}, "parameters": []any{map[string]any{"in": "path", "name": "slug", "required": true, "schema": str()}}, "responses": response(ref(item[1]))}}
		}
		for _, item := range [][2]string{{"activities", "PublicActivityCard"}, {"groups", "PublicGroupCard"}} {
			array := map[string]any{"type": "array", "items": ref(item[1])}
			var shape any = array
			if base == "/api/v1" {
				shape = map[string]any{"type": "object", "properties": map[string]any{"next_cursor": str(), "limit": map[string]any{"type": "integer"}, "results": array}}
			}
			parameters := []any{map[string]any{"in": "query", "name": "activity", "schema": str()}}
			if base == "/api/v1" {
				parameters = append(parameters, map[string]any{"in": "query", "name": "cursor", "schema": str()}, map[string]any{"in": "query", "name": "limit", "schema": map[string]any{"type": "integer", "minimum": 1, "maximum": 200}})
			}
			paths[base+"/discovery/public/"+item[0]+"/"] = map[string]any{"get": map[string]any{"security": []any{}, "parameters": parameters, "responses": response(shape)}}
		}
	}
}
