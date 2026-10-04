package web

import (
	"encoding/json"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestNativePublicDocumentationAssembledMux(t *testing.T) {
	// Include a domain registrar and the real catch-all web registration. No
	// database, authentication service or direct PublicDownload call supplies the
	// response: the registered GET handlers must win normal mux dispatch.
	mux := http.NewServeMux()
	catalog.New(nil).Register(mux)
	s := &Server{}
	s.Register(mux)
	var document map[string]any
	for _, route := range []struct{ path, kind string }{{"/api/schema/", "application/json"}, {"/api/docs/", "text/html"}} {
		request := httptest.NewRequest(http.MethodGet, route.path+"?private=untrusted-query", nil)
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, request)
		kind, _, err := mime.ParseMediaType(out.Header().Get("Content-Type"))
		if err != nil || out.Code != http.StatusOK || kind != route.kind {
			t.Fatalf("anonymous documentation route %s: status=%d MIME=%s err=%v", route.path, out.Code, kind, err)
		}
		if len(out.Result().Cookies()) != 0 || out.Header().Get("Cache-Control") != "public, max-age=3600" || out.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("public contract metadata set cookies or lost download headers", route.path)
		}
		if strings.Contains(out.Body.String(), "untrusted-query") {
			t.Fatal("documentation reflected request data", route.path)
		}
		other := httptest.NewRecorder()
		mux.ServeHTTP(other, platform.WithActor(request, platform.Actor{ID: 1, IsActive: true, IsStaff: true, Role: "admin"}))
		if other.Code != out.Code || other.Body.String() != out.Body.String() {
			t.Fatal("contract metadata changed with the request actor", route.path)
		}
		if route.kind == "application/json" {
			if err := json.Unmarshal(out.Body.Bytes(), &document); err != nil {
				t.Fatal("documentation route did not return JSON", err)
			}
		} else if !strings.Contains(out.Body.String(), `href="/api/schema/"`) || !strings.Contains(out.Body.String(), "Activities API") {
			t.Fatal("HTML documentation did not link its schema")
		}
	}
	if document["openapi"] != "3.0.3" || document["x-native-api-operation-count"] != float64(380) || len(document["paths"].(map[string]any)) != 318 {
		t.Fatal("documentation route returned stale protocol/route coverage")
	}
	contract := s.publicSchema()
	schemas := contract["components"].(map[string]any)["schemas"].(map[string]any)
	if err := validateSchemaValue(schemaKind("OpenAPIDocument"), document, schemas); err != nil {
		t.Fatal("actual JSON document does not match its protocol response contract", err)
	}
	paths := document["paths"].(map[string]any)
	for path, kind := range map[string]string{"/api/schema/": "application/json", "/api/docs/": "text/html"} {
		operation := paths[path].(map[string]any)["get"].(map[string]any)
		if len(operation["security"].([]any)) != 0 {
			t.Fatal("public documentation metadata requires account authority", path)
		}
		response := operation["responses"].(map[string]any)["200"].(map[string]any)
		content := response["content"].(map[string]any)
		if len(content) != 1 || content[kind] == nil {
			t.Fatal("documentation response has the wrong protocol content type", path)
		}
	}
	t.Logf("anonymous mux delivers 380 operations, 318 paths, %d component schemas", len(schemas))
}

func TestNativeOpenAPIDocumentProtocolTypedDictionaries(t *testing.T) {
	schemas := map[string]any{}
	publicDocumentationSchemas(schemas)
	// Protocol containers must validate their dictionary children, including the
	// recursive schema map, rather than treating the whole document as opaque.
	for _, fixture := range []struct {
		model string
		value any
	}{
		{"OpenAPISchemaMap", map[string]any{"Model": "not-a-schema-object"}},
		{"OpenAPIPaths", map[string]any{"/path/": map[string]any{"get": "not-an-operation"}}},
		{"OpenAPIResponses", map[string]any{"200": map[string]any{"description": float64(42)}}},
		{"OpenAPIContent", map[string]any{"application/json": map[string]any{"schema": "not-a-schema-object"}}},
		{"OpenAPISecurityRequirement", map[string]any{"cookieAuth": "not-an-array"}},
	} {
		if err := validateSchemaValue(schemaKind(fixture.model), fixture.value, schemas); err == nil {
			t.Fatal("protocol dictionary silently accepted an invalid child", fixture.model)
		}
	}
}
