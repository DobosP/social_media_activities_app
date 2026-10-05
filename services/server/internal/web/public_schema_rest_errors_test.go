package web

import "testing"

func TestSocialRESTFieldErrorsHaveScoped400Schemas(t *testing.T) {
	doc := (&Server{}).publicSchema()
	paths := doc["paths"].(map[string]any)
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	response := func(path, method, status string) map[string]any {
		t.Helper()
		return paths[path].(map[string]any)[method].(map[string]any)["responses"].(map[string]any)[status].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	}
	for _, base := range []string{"/api/social", "/api/v1/social"} {
		for _, contract := range []struct{ path, field string }{
			{base + "/activities/", "description"},
			{base + "/activities/{id}/posts/", "body"},
			{base + "/activities/{id}/announce/", "body"},
			{base + "/groups/{id}/posts/", "body"},
			{base + "/groups/{id}/announce/", "body"},
		} {
			t.Run(contract.path, func(t *testing.T) {
				shape := response(contract.path, "post", "400")
				for _, valid := range []any{
					map[string]any{"detail": "Invalid request."},
					map[string]any{contract.field: []any{"Ensure this field has no more than the allowed characters."}},
				} {
					if err := validateSchemaValue(shape, valid, schemas); err != nil {
						t.Fatal("actual REST error alternative rejected", err)
					}
				}
				other := "body"
				if contract.field == "body" {
					other = "description"
				}
				for _, invalid := range []any{
					map[string]any{other: []any{"wrong field"}},
					map[string]any{contract.field: "wrong scalar"},
					map[string]any{contract.field: []any{123}},
					map[string]any{"detail": "ambiguous", contract.field: []any{"both shapes"}},
				} {
					if validateSchemaValue(shape, invalid, schemas) == nil {
						t.Fatal("REST error schema widened beyond the operation's exact alternatives", invalid)
					}
				}
				if validateSchemaValue(response(contract.path, "post", "403"), map[string]any{contract.field: []any{"not an authorization response"}}, schemas) == nil {
					t.Fatal("field errors leaked into other status schemas")
				}
			})
		}
		for _, op := range []struct{ path, method string }{
			{base + "/activities/", "get"},
			{base + "/activities/{id}/", "patch"},
			{base + "/activities/{id}/transit/", "post"},
			{base + "/groups/", "post"},
		} {
			if validateSchemaValue(response(op.path, op.method, "400"), map[string]any{"body": []any{"unrelated operation"}}, schemas) == nil {
				t.Fatal("field error alternative leaked into another operation", op)
			}
		}
	}
}
