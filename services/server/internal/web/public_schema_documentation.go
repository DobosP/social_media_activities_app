package web

// The document endpoint returns this native OpenAPI protocol, not domain rows.
// Named references describe recursive Schema Objects without creating Go map
// cycles. Dictionary entries retain their actual child contracts.
func publicDocumentationSchemas(schemas map[string]any) {
	definitions := map[string]string{
		"OpenAPIDocument":       "openapi:str info:OpenAPIInfo paths:OpenAPIPaths components:OpenAPIComponents x-native-api-operation-count:int",
		"OpenAPIInfo":           "title:str version:str description:str",
		"OpenAPIComponents":     "schemas:OpenAPISchemaMap securitySchemes:OpenAPISecuritySchemes",
		"OpenAPIPathItem":       "get~:OpenAPIOperation post~:OpenAPIOperation put~:OpenAPIOperation patch~:OpenAPIOperation delete~:OpenAPIOperation head~:OpenAPIOperation options~:OpenAPIOperation trace~:OpenAPIOperation",
		"OpenAPIOperation":      "summary~:str description~:str security~:[]OpenAPISecurityRequirement parameters~:[]OpenAPIParameter requestBody~:OpenAPIRequestBody x-native-source~:str responses:OpenAPIResponses",
		"OpenAPIParameter":      "name:str in:str required~:bool description~:str schema:OpenAPISchemaObject",
		"OpenAPIRequestBody":    "required~:bool content:OpenAPIContent",
		"OpenAPIResponse":       "description:str content~:OpenAPIContent headers~:OpenAPIHeaders",
		"OpenAPIMediaType":      "schema:OpenAPISchemaObject",
		"OpenAPIHeader":         "description~:str schema:OpenAPISchemaObject",
		"OpenAPISecurityScheme": "type:str in:str name:str description~:str",
		"OpenAPISchemaObject":   "$ref~:str type~:str format~:str description~:str nullable~:bool properties~:OpenAPISchemaMap required~:[]str additionalProperties~:OpenAPIAdditionalProperties enum~:[]OpenAPIJSONValue items~:OpenAPISchemaObject oneOf~:[]OpenAPISchemaObject anyOf~:[]OpenAPISchemaObject allOf~:[]OpenAPISchemaObject minimum~:num maximum~:num minItems~:int maxItems~:int minLength~:int maxLength~:int pattern~:str default~:OpenAPIJSONValue example~:OpenAPIJSONValue x-native-flexible-json~:bool",
	}
	for name, fields := range definitions {
		schemas[name] = schemaFields(fields)
	}
	dictionary := func(child string) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": schemaKind(child)}
	}
	for name, child := range map[string]string{
		"OpenAPIPaths":               "OpenAPIPathItem",
		"OpenAPIResponses":           "OpenAPIResponse",
		"OpenAPIContent":             "OpenAPIMediaType",
		"OpenAPIHeaders":             "OpenAPIHeader",
		"OpenAPISchemaMap":           "OpenAPISchemaObject",
		"OpenAPISecuritySchemes":     "OpenAPISecurityScheme",
		"OpenAPISecurityRequirement": "[]str",
	} {
		schemas[name] = dictionary(child)
	}
	schemas["OpenAPIAdditionalProperties"] = map[string]any{"oneOf": []any{schemaKind("bool"), schemaKind("OpenAPISchemaObject")}}
	schemas["OpenAPIJSONValue"] = map[string]any{"oneOf": []any{
		schemaKind("str"), schemaKind("num"), schemaKind("bool"), schemaKind("[]OpenAPIJSONValue"), dictionary("OpenAPIJSONValue"),
		map[string]any{"type": "object", "nullable": true, "enum": []any{nil}},
	}}
	schemas["OpenAPIDocument"].(map[string]any)["properties"].(map[string]any)["openapi"] = map[string]any{"type": "string", "enum": []string{"3.0.3"}}
}
