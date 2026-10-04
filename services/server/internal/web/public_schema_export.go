package web

import (
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"reflect"
)

// The portability export is a subject-scoped read projection. These schemas do
// not advertise generic database writes, raw age/consent edits or access to
// another subject's data; self and guardian exports use different redactions.
func privateExportSchemas(schemas map[string]any) {
	definitions := map[string]string{
		"ExportProfile":         "public_id:uuid username:str display_name:str age_band:str cohort:str role:str is_identity_verified:bool identity_verified_at:date? is_active:bool date_joined:date",
		"ExportAPIAccess":       "api_token_issued:bool issued_at:date?",
		"ExportAvatarStyle":     "generation:int name:str",
		"ExportPrivacy":         "muted_notification_kinds:[]str access_preferences:AccessPreferences? avatar_style:ExportAvatarStyle",
		"ExportDonation":        "amount_cents:int currency:str recurring:bool campaign:str? provider:str status:str external_ref:str created_at:date completed_at:date?",
		"ExportDonations":       "count:int completed_count:int completed_total_cents:int items:[]ExportDonation",
		"AgeEvidence":           "age_over_16:bool age_over_18:bool format:str holder_proof:str",
		"ExportAgeAssurance":    "provider:str method:str age_band:str verified_at:date expires_at:date? evidence:AgeEvidence",
		"ExportMembership":      "activity_id:int activity_title:str role:str state:str created_at:date decided_at:date?",
		"ExportActivity":        "id:int title:str status:str cohort:str starts_at:date created_at:date",
		"ExportGroup":           "id:int title:str status:str cohort:str area:str is_staff_curated:bool created_at:date",
		"ExportGroupMembership": "group_id:int group_title:str role:str state:str joined_at:date",
		"ExportBlock":           "blocked:str blocked_public_id:uuid created_at:date",
		"ExportConsent":         "status:str scope:str guardian_identifier:str granted_at:date? expires_at:date? revoked_at:date? created_at:date",
		"ExportConsents":        "as_minor:[]ExportConsent",
		"ExportWardLink":        "ward_public_id:uuid relationship:str status:str created_at:date",
		"ExportGuardianLink":    "guardian_public_id:uuid relationship:str status:str created_at:date",
		"ExportGuardianships":   "as_guardian_of:[]ExportWardLink guarded_by:[]ExportGuardianLink",
		"ExportPost":            "thread_kind:str thread_id:int thread_title:str body:str status:str is_announcement:bool edited:bool had_attachment:bool created_at:date",
		"ExportPosts":           "items:[]ExportPost total:int truncated:bool",
		"ExportReaction":        "post_id:int created_at:date facet:str",
		"ExportSentimentAction": "post_id:int created_at:date",
		"ExportSentiment":       "reactions:[]ExportReaction dissents:[]ExportSentimentAction concerns:[]ExportSentimentAction",
		"AccountExport":         "schema_version:int generated_at:date profile:ExportProfile api_access:ExportAPIAccess privacy_settings:ExportPrivacy donations:ExportDonations age_assurance:[]ExportAgeAssurance memberships:[]ExportMembership owned_activities:[]ExportActivity owned_groups:[]ExportGroup group_memberships:[]ExportGroupMembership blocks:[]ExportBlock consents:ExportConsents guardianships:ExportGuardianships thread_posts:ExportPosts safety_record:SafetyRecord own_sentiment_actions:ExportSentiment",
		"AgeField":              "path:[]str",
		"AgeConstraints":        "fields:[]AgeField",
		"JWTVCAlgorithms":       "alg:[]str",
		"AgeFormat":             "jwt_vc:JWTVCAlgorithms",
		"AgeDescriptor":         "id:str format:AgeFormat constraints:AgeConstraints",
		"AgeDefinition":         "id:str input_descriptors:[]AgeDescriptor",
		"AgePresentation":       "nonce:str audience:str state:str presentation_definition:AgeDefinition",
		"DeckActions":           "detail_url:str web_url:str",
		"DeckActivity":          "id:int title:str starts_at:date activity_type:str place_id:int? distance_m:num? description:str place_name:str? visual:Visual actions:DeckActions",
		"AuthError":             "error:str",
		"StripePaymentObject":   "id:str payment_status:str",
		"StripePaymentData":     "object:StripePaymentObject",
		"StripeWebhook":         "type:str data:StripePaymentData",
		"CSPViolationInput":     "effective-directive~:str violated-directive~:str blocked-uri~:str blockedURL~:str document-uri~:str documentURL~:str",
	}
	for name, fields := range definitions {
		schemas[name] = schemaFields(fields)
	}
	// Legacy imported assurance evidence is a domain-owned JSON object. Native
	// assurance writes only minimized age predicates, format and holder proof.
	schemas["AgeEvidence"].(map[string]any)["additionalProperties"] = true
	schemas["AgeEvidence"].(map[string]any)["x-native-flexible-json"] = true
	delete(schemas["AgeEvidence"].(map[string]any), "required")
	schemas["AgeEvidence"].(map[string]any)["description"] = "Minimized assurance evidence; historic imported evidence may contain provider-specific fields."
	schemas["AccountExport"].(map[string]any)["description"] = "Own or explicitly linked ward portability export, schema version 5. Contains subject-only data. Hidden post bodies are redacted according to standing removal and self/guardian authority. No session/API token, password hash or E2EE ciphertext/key material is exported."
	schemas["AccountExport"].(map[string]any)["properties"].(map[string]any)["schema_version"] = map[string]any{"type": "integer", "enum": []int{5}}
	schemas["PaymentWebhook"] = map[string]any{"anyOf": []any{schemaFields("external_ref:str"), schemaKind("StripeWebhook")}, "description": "Native checkout uses external_ref; Stripe uses its signed event envelope. Extra signed provider envelope fields are accepted."}
	schemas["StripeWebhook"].(map[string]any)["additionalProperties"] = true
	schemas["StripePaymentData"].(map[string]any)["additionalProperties"] = true
	schemas["StripePaymentObject"].(map[string]any)["additionalProperties"] = true
	schemas["UsernameList"] = map[string]any{"oneOf": []any{schemaKind("str"), schemaKind("[]str"), map[string]any{"type": "object", "nullable": true, "enum": []any{nil}}}, "description": "Legacy single username, list of usernames or null."}
	violation := schemaKind("CSPViolationInput")
	envelope := schemaFields("csp-report~:CSPViolationInput body~:CSPViolationInput")
	envelope["additionalProperties"] = true
	schemas["CSPViolationInput"].(map[string]any)["additionalProperties"] = true
	schemas["CSPReport"] = map[string]any{"anyOf": []any{violation, envelope, map[string]any{"type": "array", "items": map[string]any{"anyOf": []any{violation, envelope}}}}, "description": "Legacy CSP violation or Reporting API list; accepted fields are sanitized and URI query/fragment data are discarded. The bounded collector always returns 204."}
	patch := schemaFields("title~:str description~:str meeting_point~:str what_to_bring~:str organizer_note~:str first_time_note~:str accessibility_notes~:str cost_note~:str cost_amount~:DecimalAmount? secondary_types~:[]int cost_band~:str difficulty~:str starts_at~:date ends_at~:date? capacity~:int? min_to_go~:int? beginners_welcome~:bool on_behalf_of~:str")
	schemas["ActivityPatch"] = patch
	schemas["DecimalAmount"] = schemaType(reflect.TypeOf(social.Decimal("")))
	activity := schemas["Activity"].(map[string]any)
	props := map[string]any{}
	for name, value := range activity["properties"].(map[string]any) {
		props[name] = value
	}
	props["match_score"] = schemaKind("num")
	schemas["RecommendedActivity"] = map[string]any{"type": "object", "properties": props, "required": activity["required"], "additionalProperties": false}
}
