package web

func (s *Server) publicSchema() map[string]any {
	scalar := func(kind string) map[string]any { return map[string]any{"type": kind} }
	nullable := func(kind string) map[string]any { return map[string]any{"type": kind, "nullable": true} }
	ref := func(name string) map[string]any { return map[string]any{"$ref": "#/components/schemas/" + name} }
	credit := map[string]any{"type": "object", "nullable": true, "properties": map[string]any{"attribution": scalar("string"), "license_name": scalar("string"), "provenance_url": scalar("string")}}
	event := map[string]any{}
	for _, key := range []string{"title", "description", "url", "source", "source_category", "lifecycle_status", "source_recurrence", "source_timezone", "source_currency", "source_availability", "attribution", "license_name", "provenance_url", "place_name", "activity"} {
		event[key] = scalar("string")
	}
	event["id"], event["place"], event["source_is_free"], event["attribution_credit"] = scalar("integer"), nullable("integer"), nullable("boolean"), credit
	event["source_price_min"], event["source_price_max"] = nullable("string"), nullable("string")
	event["source_confidence"] = nullable("number")
	event["activity"] = nullable("string")
	for _, key := range []string{"starts_at", "ends_at"} {
		event[key] = map[string]any{"type": "string", "format": "date-time", "nullable": key == "ends_at"}
	}
	place := map[string]any{}
	for _, key := range []string{"name", "display_address", "address_street", "address_housenumber", "address_city", "address_postcode", "address_country", "opening_hours_raw", "website", "phone", "source", "osm_type", "attribution", "license_name", "provenance_url"} {
		place[key] = scalar("string")
	}
	place["osm_id"], place["image_thumb"], place["distance_m"], place["attribution_credit"] = nullable("integer"), nullable("string"), nullable("number"), credit
	place["has_upcoming"], place["is_bookable"] = scalar("boolean"), scalar("boolean")
	for _, key := range []string{"categories", "category_labels"} {
		place[key] = map[string]any{"type": "array", "items": scalar("string")}
	}
	place["open_now"] = map[string]any{"oneOf": []any{scalar("boolean"), map[string]any{"type": "string", "enum": []string{"unverified"}}, map[string]any{"type": "object", "nullable": true, "enum": []any{nil}}}}
	schedule := map[string]any{}
	for _, day := range []string{"mo", "tu", "we", "th", "fr", "sa", "su"} {
		schedule[day] = map[string]any{"type": "array", "items": map[string]any{"type": "array", "items": scalar("integer"), "minItems": 2, "maxItems": 2}}
	}
	place["opening_hours"] = map[string]any{"type": "object", "nullable": true, "properties": schedule, "additionalProperties": false}
	place["activities"] = map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"slug": scalar("string"), "name": scalar("string"), "confidence": scalar("number"), "origin": scalar("string"), "source": scalar("string"), "mapping_rule": scalar("string")}}}
	schemas := map[string]any{"Event": map[string]any{"type": "object", "properties": event}, "PlaceFeature": map[string]any{"type": "object", "properties": map[string]any{"id": scalar("integer"), "type": map[string]any{"type": "string", "enum": []string{"Feature"}}, "geometry": map[string]any{"type": "object", "properties": map[string]any{"type": map[string]any{"type": "string", "enum": []string{"Point"}}, "coordinates": map[string]any{"type": "array", "items": scalar("number"), "minItems": 2, "maxItems": 2}}}, "properties": map[string]any{"type": "object", "properties": place}}}}
	paths := map[string]any{}
	for _, base := range []string{"/api", "/api/v1"} {
		for _, kind := range []string{"events", "places"} {
			model, key := "Event", "results"
			filters := []string{"activity", "place", "city", "from", "to", "q", "include_past", "near_lon", "near_lat", "radius_m", "limit", "offset"}
			if kind == "places" {
				model, key = "PlaceFeature", "features"
				filters = []string{"activity", "category", "city", "source", "min_confidence", "has_upcoming", "near_lon", "near_lat", "radius_m", "in_bbox", "page", "page_size"}
			}
			params := []any{}
			for _, f := range filters {
				typ := "string"
				switch f {
				case "near_lon", "near_lat", "radius_m", "min_confidence":
					typ = "number"
				case "place", "limit", "offset", "page_size":
					typ = "integer"
				case "include_past", "has_upcoming":
					typ = "boolean"
				}
				params = append(params, map[string]any{"in": "query", "name": f, "required": false, "schema": scalar(typ)})
			}
			collection := map[string]any{"type": "object", "properties": map[string]any{"count": scalar("integer"), "next": nullable("string"), "previous": nullable("string"), key: map[string]any{"type": "array", "items": ref(model)}}}
			if kind == "places" {
				collection["properties"].(map[string]any)["type"] = map[string]any{"type": "string", "enum": []string{"FeatureCollection"}}
			}
			response := func(shape any) map[string]any {
				return map[string]any{"200": map[string]any{"description": "Public data with source license and attribution.", "content": map[string]any{"application/json": map[string]any{"schema": shape}}}, "400": map[string]any{"description": "Invalid query"}, "404": map[string]any{"description": "Not found or unpublished"}, "429": map[string]any{"description": "Rate limited"}}
			}
			paths[base+"/"+kind+"/"] = map[string]any{"get": map[string]any{"summary": "Published " + kind, "security": []any{}, "parameters": params, "responses": response(collection)}}
			paths[base+"/"+kind+"/{id}/"] = map[string]any{"get": map[string]any{"security": []any{}, "parameters": []any{map[string]any{"in": "path", "name": "id", "required": true, "schema": scalar("integer")}}, "responses": response(ref(model))}}
		}
	}
	s.publicCatalogSchema(paths, schemas)
	operationCount := s.publicInventorySchema(paths, schemas)
	return map[string]any{"openapi": "3.0.3", "info": map[string]any{"title": "Activities native API", "version": "1.0.0", "description": "Public venues and events retain license and access metadata. Private account/social APIs require authenticated cohort authorization; session-cookie mutations require same-origin CSRF, and login/signup/logout always require CSRF. Minor meetups and identities are never published."}, "paths": paths, "x-native-api-operation-count": operationCount, "components": map[string]any{"schemas": schemas, "securitySchemes": map[string]any{"stripeSignature": map[string]any{"type": "apiKey", "in": "header", "name": "Stripe-Signature", "description": "Stripe signature validation over the raw event body."}, "webhookSecret": map[string]any{"type": "apiKey", "in": "header", "name": "X-Webhook-Secret", "description": "Native payment provider webhook authentication."}, "cookieAuth": map[string]any{"type": "apiKey", "in": "cookie", "name": "sessionid"}, "tokenAuth": map[string]any{"type": "apiKey", "in": "header", "name": "Authorization", "description": "Use Token followed by a space and the 40-character hexadecimal API token; Bearer is not accepted."}}}}
}
