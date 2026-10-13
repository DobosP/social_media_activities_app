package web

import (
	"reflect"
	"testing"
)

func TestEventFacetsOpenAPIAdditiveContract(t *testing.T) {
	paths, schemas := map[string]any{}, map[string]any{}
	// Standalone additive assembly proves its input operations are unchanged.
	for _, base := range []string{"/api", "/api/v1"} {
		paths[base+"/events/"] = map[string]any{"get": map[string]any{"parameters": []any{
			map[string]any{"name": "city", "in": "query", "schema": map[string]any{"type": "string"}},
			map[string]any{"name": "limit", "in": "query", "schema": map[string]any{"type": "integer"}},
			map[string]any{"name": "offset", "in": "query", "schema": map[string]any{"type": "integer"}},
		}}}
	}
	publicEventFacetsSchema(paths, schemas)
	for _, base := range []string{"/api", "/api/v1"} {
		legacy := paths[base+"/events/"].(map[string]any)["get"].(map[string]any)["parameters"].([]any)
		if !reflect.DeepEqual(legacy[1].(map[string]any)["schema"], map[string]any{"type": "integer"}) || !reflect.DeepEqual(legacy[2].(map[string]any)["schema"], map[string]any{"type": "integer"}) {
			t.Fatal("facet schema rewrote existing event contract", legacy)
		}
	}
	doc := (&Server{}).publicSchema()
	actual := doc["paths"].(map[string]any)
	for _, base := range []string{"/api", "/api/v1"} {
		events := actual[base+"/events/"].(map[string]any)["get"].(map[string]any)
		facets := actual[base+"/events/facets/"].(map[string]any)["get"].(map[string]any)
		oldParams, newParams := events["parameters"].([]any), facets["parameters"].([]any)
		if len(facets["security"].([]any)) != 0 || len(oldParams) != len(newParams) {
			t.Fatal("facet auth/filter scope differs", base)
		}
		for n, param := range oldParams {
			old, next := param.(map[string]any), newParams[n].(map[string]any)
			if old["name"] != next["name"] || (old["name"] != "limit" && old["name"] != "offset" && !reflect.DeepEqual(old, next)) {
				t.Fatal("facet filter contract drift", old, next)
			}
		}
	}
	schema := schemas["EventFacets"].(map[string]any)
	if schema["additionalProperties"] != false || len(schema["required"].([]string)) != 7 {
		t.Fatal("facet response schema lost its exact public projection", schema)
	}
}
