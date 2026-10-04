package web

import (
	_ "embed"
	"encoding/json"
	"strconv"
	"strings"
)

// Native route declarations are generated offline from the Go registrations.
// Field contracts are reviewed alongside actual DTOs/map/SQL projections.
//
//go:embed public_routes.json
var nativeRouteInventory []byte

func (s *Server) publicInventorySchema(paths, schemas map[string]any) int {
	privateSchemas(schemas)
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
				name := strings.Trim(segment, "{}")
				shape := schemaKind("str")
				if name == "id" || name == "activity" || name == "thread" || name == "message_id" {
					shape = schemaKind("int")
					shape["minimum"] = 1
				}
				if name == "public_id" {
					shape = schemaKind("uuid")
				}
				params = append(params, map[string]any{"in": "path", "name": name, "required": true, "schema": shape})
			}
		}
		contract, documented := privateContract(record.Method, record.Path)
		if !documented {
			panic("missing native API field contract: " + record.Method + " " + record.Path)
		}
		security := []any{map[string]any{"cookieAuth": []string{}}, map[string]any{"tokenAuth": []string{}}}
		if contract.public {
			security = []any{}
		}
		responses := map[string]any{}
		for code, description := range map[string]string{"400": "Invalid request", "401": "Authentication required", "403": "Current authorization or CSRF denied", "404": "Not found or not visible", "429": "Rate limited", "500": "Native service failure", "503": "Native dependency or authentication unavailable"} {
			shape := schemaKind("APIError")
			if record.Source == "../authcore/service.go" {
				shape = map[string]any{"oneOf": []any{schemaKind("AuthError"), schemaKind("APIError")}}
			}
			responses[code] = map[string]any{"description": description, "content": map[string]any{"application/json": map[string]any{"schema": shape}}}
		}
		response := map[string]any{"description": "Gate-filtered native domain result"}
		if contract.response != nil {
			response["content"] = map[string]any{"application/json": map[string]any{"schema": contract.response}}
		}
		responses[strconv.Itoa(contract.status)] = response
		description := "Current authentication, cohort, consent, membership, blocking and publication gates apply before access. Cookie mutations require same-origin CSRF; API tokens remain subject to current actor authorization."
		if contract.description != "" {
			description = contract.description
		}
		operation := map[string]any{"summary": record.Method + " " + record.Path, "description": description, "security": security, "parameters": params, "x-native-source": record.Source, "responses": responses}
		params = append(params, privateQueryParameters(record.Method, record.Path)...)
		operation["parameters"] = params
		if contract.request != nil {
			operation["requestBody"] = map[string]any{"required": !contract.optionalBody, "content": map[string]any{contract.requestType: map[string]any{"schema": contract.request}}}
		}
		webhook := strings.HasSuffix(record.Path, "/donations/webhook/")
		csp := strings.HasSuffix(record.Path, "/ops/csp-report/")
		if record.Method != "GET" && record.Method != "HEAD" && record.Method != "OPTIONS" && !webhook && !csp {
			authWrite := record.Source == "../authcore/service.go"
			csrfDescription := "Required with same-origin session-cookie mutations; native domain token requests use current actor authorization."
			if authWrite {
				csrfDescription = "Auth login/signup/logout always require a CSRF cookie, matching header and same-origin Origin or Referer, including anonymous and API-token requests."
			}
			operation["parameters"] = append(params, map[string]any{"in": "header", "name": "X-CSRFToken", "required": authWrite, "description": csrfDescription, "schema": schemaKind("str")})
		}
		if strings.HasSuffix(record.Path, "/auth/token/") && record.Method == "DELETE" {
			operation["security"] = []any{map[string]any{"tokenAuth": []string{}}}
		}
		if webhook {
			operation["security"] = []any{map[string]any{"stripeSignature": []string{}}, map[string]any{"webhookSecret": []string{}}}
		}
		if contract.status == 302 || contract.status == 303 {
			response["headers"] = map[string]any{"Location": map[string]any{"schema": schemaKind("str"), "description": "Validated OAuth destination"}}
		}
		if contract.response != nil && contract.response["format"] == "binary" {
			content := map[string]any{}
			kinds := []string{"image/avif", "image/webp"}
			if strings.Contains(record.Path, "/attachment/") {
				kinds = append(kinds, "application/pdf", "video/mp4")
			}
			for _, kind := range kinds {
				content[kind] = map[string]any{"schema": contract.response}
			}
			response["content"] = content
			response["headers"] = map[string]any{"Accept-Ranges": map[string]any{"schema": schemaKind("str")}, "Content-Disposition": map[string]any{"schema": schemaKind("str")}}
			responses["206"] = map[string]any{"description": "One authorized byte range", "content": content, "headers": map[string]any{"Content-Range": map[string]any{"schema": schemaKind("str")}}}
			responses["307"] = map[string]any{"description": "Short-lived private storage redirect", "headers": map[string]any{"Location": map[string]any{"schema": schemaKind("str")}}}
			responses["416"] = map[string]any{"description": "Invalid byte range", "headers": map[string]any{"Content-Range": map[string]any{"schema": schemaKind("str")}}}
			operation["parameters"] = append(params, map[string]any{"in": "header", "name": "Range", "required": false, "schema": schemaKind("str"), "description": "A single bytes range"})
		}
		if strings.Contains(record.Path, "/ready") {
			responses["503"] = response
		}
		if record.Source == "../authcore/service.go" {
			responses["409"], responses["503"] = responses["400"], responses["500"]
		}
		if record.Source == "internal/booking/service.go" || record.Source == "internal/donations/service.go" {
			responses["502"] = responses["500"]
		}
		if record.Source == "internal/media/http.go" {
			responses["413"] = responses["400"]
		}
		if csp {
			operation["requestBody"].(map[string]any)["content"] = map[string]any{"application/csp-report": map[string]any{"schema": contract.request}, "application/reports+json": map[string]any{"schema": contract.request}, "application/json": map[string]any{"schema": contract.request}}
		}
		if record.Method == "POST" && strings.HasSuffix(record.Path, "/social/place-proposals/") {
			responses["400"].(map[string]any)["content"] = map[string]any{"application/json": map[string]any{"schema": map[string]any{"oneOf": []any{schemaKind("APIError"), schemaFields("detail:str soft:bool duplicate_place_name:str duplicate_place_id~:int")}}}}
		}
		ops[method] = operation
	}
	return operations
}
