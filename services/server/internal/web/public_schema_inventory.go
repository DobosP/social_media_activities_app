package web

import (
	_ "embed"
	"encoding/json"
	"strings"
)

// Native route declarations are generated offline from the Go registrations.
// Field-level catalog schemas remain explicit above; untyped private responses
// make no false claim about a broader serialization contract.
//
//go:embed public_routes.json
var nativeRouteInventory []byte

func (s *Server) publicInventorySchema(paths, schemas map[string]any) int {
	var records []struct{ Method, Path, Source string }
	if json.Unmarshal(nativeRouteInventory, &records) != nil {
		return 0
	}
	operations := 0
	for _, record := range records {
		if !strings.HasPrefix(record.Path, "/api/") {
			continue
		}
		operations++
		path := paths[record.Path]
		if path == nil {
			path = map[string]any{}
			paths[record.Path] = path
		}
		ops := path.(map[string]any)
		method := strings.ToLower(record.Method)
		if ops[method] != nil {
			continue
		}
		params := []any{}
		for _, segment := range strings.Split(record.Path, "/") {
			if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
				params = append(params, map[string]any{"in": "path", "name": strings.Trim(segment, "{}"), "required": true, "schema": map[string]any{"type": "string"}})
			}
		}
		public := false
		for _, prefix := range []string{"/api/events/", "/api/v1/events/", "/api/places/", "/api/v1/places/", "/api/taxonomy/", "/api/v1/taxonomy/", "/api/discovery/public/", "/api/v1/discovery/public/", "/api/media/place-cover-file/", "/api/v1/media/place-cover-file/"} {
			if strings.HasPrefix(record.Path, prefix) {
				public = true
			}
		}

		if record.Path == "/api/discovery/near-me/" || record.Path == "/api/v1/discovery/near-me/" || record.Path == "/api/discovery/happening/" || record.Path == "/api/v1/discovery/happening/" || record.Path == "/api/auth/signup" || record.Path == "/api/auth/login" || record.Path == "/api/auth/csrf" || record.Path == "/api/auth/providers" || strings.HasPrefix(record.Path, "/api/auth/oauth/") {
			public = true
		}
		security := []any{map[string]any{"cookieAuth": []string{}}, map[string]any{"tokenAuth": []string{}}}
		if public {
			security = []any{}
		}
		operation := map[string]any{"summary": record.Method + " " + record.Path, "description": "Native Go endpoint. Authorization, cohort, consent, mutual blocks and publication gates apply before its domain operation. Mutations additionally require same-origin CSRF. This inventory entry does not assert a field-level private DTO schema.", "security": security, "parameters": params, "x-native-source": record.Source, "responses": map[string]any{"200": map[string]any{"description": "Native domain result; field-level private DTO schema not yet specified"}, "400": map[string]any{"description": "Invalid request"}, "401": map[string]any{"description": "Authentication required"}, "403": map[string]any{"description": "Domain authorization denied"}, "404": map[string]any{"description": "Not found or not visible"}}}
		if record.Method != "GET" && record.Method != "HEAD" && record.Method != "OPTIONS" {
			operation["parameters"] = append(params, map[string]any{"in": "header", "name": "X-CSRFToken", "required": true, "schema": map[string]any{"type": "string"}})
		}
		ops[method] = operation
	}
	return operations
}
