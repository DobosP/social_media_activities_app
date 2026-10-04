package web

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
)

// Test the emitted JSON, including pointers, omitempty, nested encryption keys
// and private unexported storage fields. These are synthetic values, no DB or
// credential/provider/network calls. The validator exercises our OAS3 subset.
func TestNativeOpenAPIWireValues(t *testing.T) {
	doc := (&Server{}).publicSchema()
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	timestamp := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	fixtures := []struct {
		schema string
		value  any
	}{
		{"Photo", media.Photo{ID: 1, Kind: "profile", ContentType: "image/avif", CreatedAt: timestamp}},
		{"ActivityCover", media.Cover{ID: 1, Activity: 1, ContentType: "image/webp", CreatedAt: timestamp, UpdatedAt: timestamp}},
		{"AuthSession", map[string]any{"authenticated": false, "user": nil}},
		{"AuthSession", map[string]any{"authenticated": true, "user": authcore.User{ID: "1", Username: "fixture"}}},
		{"OwnMessagingKey", map[string]any{"key_id": "00000000-0000-4000-8000-000000000001", "algorithm": "ECDH-P256", "public_jwk": map[string]any{"kty": "EC", "x": "fixture", "crv": "P-256", "ext": true}, "wrapped_private_jwk": nil, "created_at": timestamp}},
		{"Conversation", map[string]any{"id": 1, "kind": "group", "title": "fixture", "cohort": "child", "disappearing_seconds": 0, "created_at": timestamp, "updated_at": timestamp, "my_state": nil, "my_role": nil, "participants": []any{}}},
		{"Post", map[string]any{"id": 1, "author": "fixture", "body": "fixture", "is_announcement": false, "reply_to": nil, "share": nil, "created_at": timestamp}},
		{"ProfileCard", map[string]any{"tier": "stranger", "public_id": "00000000-0000-4000-8000-000000000001", "display": "A member", "avatar": "data:image/svg+xml;base64,fixture", "minor": true}},
		{"ActivityInput", social.ActivityInput{Place: 1, ActivityType: 1, Title: "fixture", StartsAt: timestamp}},
		{"PostInput", social.PostInput{SharePlace: ptrInt64(1)}},
		{"DeckActivity", map[string]any{"id": 1, "title": "fixture", "starts_at": timestamp, "activity_type": "walk", "place_id": 1, "distance_m": nil, "description": "fixture", "place_name": nil, "visual": map[string]any{"kind": "generated_accent"}, "actions": map[string]any{"detail_url": "/api/v1/social/activities/1/", "web_url": "/activities/1/"}}},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.schema, func(t *testing.T) {
			raw, err := json.Marshal(fixture.value)
			if err != nil {
				t.Fatal(err)
			}
			var value any
			if err = json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			if err = validateSchemaValue(schemas[fixture.schema].(map[string]any), value, schemas); err != nil {
				t.Fatal(err)
			}
		})
	}
	settings, _ := privateContract("PUT", "/api/accounts/me/settings/")
	if err := validateSchemaValue(settings.request, map[string]any{"access": map[string]any{"needs_step_free": true}}, schemas); err != nil {
		t.Fatalf("valid partial settings DTO: %v", err)
	}
	listing, _ := privateContract("POST", "/api/social/activities/{id}/set_public_listing/")
	for _, valid := range []any{map[string]any{"listed": true}, map[string]any{"is_publicly_listed": false}} {
		if err := validateSchemaValue(listing.request, valid, schemas); err != nil {
			t.Fatal(err)
		}
	}
	for _, invalid := range []any{map[string]any{}, map[string]any{"listed": true, "is_publicly_listed": true}} {
		if validateSchemaValue(listing.request, invalid, schemas) == nil {
			t.Fatal("listing schema accepted ambiguous input")
		}
	}
	for _, invalid := range []any{map[string]any{"kty": "EC", "x": "fixture", "d": "plaintext"}, map[string]any{"kty": "RSA", "x": "fixture"}, map[string]any{"kty": "EC"}} {
		if validateSchemaValue(schemas["PublicJWK"].(map[string]any), invalid, schemas) == nil {
			t.Fatal("public key schema accepted invalid key fields")
		}
	}
	if validateSchemaValue(schemas["WrappedPrivateKey"].(map[string]any), "cleartext", schemas) == nil {
		t.Fatal("recovery schema accepted scalar")
	}
	if err := validateSchemaValue(schemas["UsernameList"].(map[string]any), nil, schemas); err != nil {
		t.Fatalf("legacy null username list rejected: %v", err)
	}
}
func ptrInt64(value int64) *int64 { return &value }

func TestNativeOpenAPIAuthorizationAndMediaContracts(t *testing.T) {
	doc := (&Server{}).publicSchema()
	components := doc["components"].(map[string]any)
	schemes := components["securitySchemes"].(map[string]any)
	paths := doc["paths"].(map[string]any)
	operation := func(method, path string) map[string]any { return paths[path].(map[string]any)[method].(map[string]any) }
	for path, item := range paths {
		for method, value := range item.(map[string]any) {
			op := value.(map[string]any)
			for _, security := range op["security"].([]any) {
				for scheme := range security.(map[string]any) {
					if schemes[scheme] == nil {
						t.Errorf("unknown security scheme %s for %s %s", scheme, method, path)
					}
				}
			}
			params, _ := op["parameters"].([]any)
			for _, value := range params {
				p := value.(map[string]any)
				if p["in"] == "path" {
					name := p["name"].(string)
					shape := p["schema"].(map[string]any)
					if name == "id" || name == "activity" || name == "thread" || name == "message_id" {
						if shape["type"] != "integer" {
							t.Errorf("numeric path param %s at %s", name, path)
						}
					}
				}
			}
		}
	}
	for _, path := range []string{"/api/auth/login", "/api/auth/signup", "/api/auth/logout"} {
		op := operation("post", path)
		required := false
		for _, value := range op["parameters"].([]any) {
			p := value.(map[string]any)
			if p["name"] == "X-CSRFToken" {
				required = p["required"] == true
			}
		}
		if !required {
			t.Errorf("authcore CSRF not required: %s", path)
		}
	}
	want := []any{map[string]any{"tokenAuth": []string{}}}
	if !reflect.DeepEqual(operation("delete", "/api/auth/token/")["security"], want) {
		t.Error("API-token revocation must require the current API token")
	}
	for _, path := range []string{"/api/donations/", "/api/donations/total/", "/api/health", "/api/ready", "/api/media/activity-covers/{activity}/"} {
		method := "get"
		if path == "/api/donations/" {
			method = "post"
		}
		if len(operation(method, path)["security"].([]any)) != 0 {
			t.Errorf("anonymous-capable route falsely requires session/token: %s", path)
		}
	}
	for _, base := range []string{"/api", "/api/v1"} {
		for _, kind := range []string{"file", "activity-cover-file", "place-cover-file", "attachment"} {
			op := operation("get", base+"/media/"+kind+"/{token}/")
			responses := op["responses"].(map[string]any)
			for _, status := range []string{"200", "206", "307", "416"} {
				if responses[status] == nil {
					t.Errorf("media missing status %s", status)
				}
			}
			content := responses["200"].(map[string]any)["content"].(map[string]any)
			if kind != "attachment" && (content["application/pdf"] != nil || content["video/mp4"] != nil) {
				t.Errorf("image route advertises nonimage bytes: %s", kind)
			}
		}
	}
	for _, name := range []string{"ProfileCard", "PublicActivityCard", "PublicGroupCard", "UserReference"} {
		props := schemaProperties(t, components["schemas"].(map[string]any)[name].(map[string]any), components["schemas"].(map[string]any))
		for _, prohibited := range []string{"age_band", "birthdate", "guardian_identifier", "wrapped_private_jwk", "ciphertext", "scanner_status", "payment_secret"} {
			if props[prohibited] != nil {
				t.Errorf("private field %s leaked into %s", prohibited, name)
			}
		}
	}
}

func validateSchemaValue(shape map[string]any, value any, schemas map[string]any) error {
	if ref, ok := shape["$ref"].(string); ok {
		return validateSchemaValue(schemas[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any), value, schemas)
	}
	for _, keyword := range []string{"oneOf", "anyOf"} {
		if options, ok := shape[keyword].([]any); ok {
			matches := 0
			for _, option := range options {
				if validateSchemaValue(option.(map[string]any), value, schemas) == nil {
					matches++
				}
			}
			if matches == 0 || keyword == "oneOf" && matches != 1 {
				return fmt.Errorf("%s: matched %d branches", keyword, matches)
			}
			return nil
		}
	}
	if value == nil {
		if shape["nullable"] == true || shape["x-native-flexible-json"] == true && shape["type"] == nil {
			return nil
		}
		return fmt.Errorf("null not allowed")
	}
	if enum, ok := shape["enum"]; ok {
		allowed := false
		switch values := enum.(type) {
		case []any:
			for _, want := range values {
				allowed = allowed || reflect.DeepEqual(value, want)
			}
		case []string:
			for _, want := range values {
				allowed = allowed || value == want
			}
		case []int:
			for _, want := range values {
				allowed = allowed || value == float64(want)
			}
		}
		if !allowed {
			return fmt.Errorf("value outside enum")
		}
	}
	switch shape["type"] {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("expected object")
		}
		props, _ := shape["properties"].(map[string]any)
		if required, ok := shape["required"].([]string); ok {
			for _, name := range required {
				if _, present := object[name]; !present {
					return fmt.Errorf("required field %s absent", name)
				}
			}
		}
		for name, value := range object {
			if child, ok := props[name].(map[string]any); ok {
				if err := validateSchemaValue(child, value, schemas); err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
			} else if shape["additionalProperties"] == false {
				return fmt.Errorf("unexpected field %s", name)
			} else if child, ok := shape["additionalProperties"].(map[string]any); ok {
				if err := validateSchemaValue(child, value, schemas); err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
			}
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("expected array")
		}
		for _, item := range items {
			if err := validateSchemaValue(shape["items"].(map[string]any), item, schemas); err != nil {
				return err
			}
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("expected string")
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("expected boolean")
		}
	case "integer":
		number, ok := value.(float64)
		if !ok || number != float64(int64(number)) {
			return fmt.Errorf("expected integer")
		}
	case "number":
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("expected number")
		}
	default:
		if shape["x-native-flexible-json"] != true {
			return fmt.Errorf("unspecified value shape")
		}
	}
	return nil
}
