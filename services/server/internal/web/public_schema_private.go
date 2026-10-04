package web

import (
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/booking"
	"github.com/DobosP/social_media_activities_app/services/server/internal/donations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/messaging"
	"github.com/DobosP/social_media_activities_app/services/server/internal/recommendations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/safety"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
)

// schemaFields describes reviewed map/SQL projections, using ? for null and ~
// for an omitted field. Struct DTOs use their actual json tags via schemaDTO.
// Required fields describe serialization, not permission to see a record.
func schemaFields(fields string) map[string]any {
	props, required := map[string]any{}, []string{}
	for _, field := range strings.Fields(fields) {
		parts := strings.SplitN(field, ":", 2)
		name, kind := parts[0], parts[1]
		optional := strings.HasSuffix(name, "~")
		name = strings.TrimSuffix(name, "~")
		props[name] = schemaKind(kind)
		if !optional {
			required = append(required, name)
		}
	}
	shape := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		shape["required"] = required
	}
	return shape
}
func schemaKind(kind string) map[string]any {
	if strings.HasSuffix(kind, "?") {
		shape := schemaKind(strings.TrimSuffix(kind, "?"))
		if _, ok := shape["$ref"]; ok {
			return map[string]any{"oneOf": []any{shape, map[string]any{"type": "object", "nullable": true, "enum": []any{nil}}}}
		}
		shape["nullable"] = true
		return shape
	}
	if strings.HasPrefix(kind, "[]") {
		return map[string]any{"type": "array", "items": schemaKind(kind[2:])}
	}
	switch kind {
	case "str":
		return map[string]any{"type": "string"}
	case "int":
		return map[string]any{"type": "integer", "format": "int64"}
	case "num":
		return map[string]any{"type": "number"}
	case "bool":
		return map[string]any{"type": "boolean"}
	case "date":
		return map[string]any{"type": "string", "format": "date-time"}
	case "uuid":
		return map[string]any{"type": "string", "format": "uuid"}
	case "binary":
		return map[string]any{"type": "string", "format": "binary"}
	default:
		return map[string]any{"$ref": "#/components/schemas/" + kind}
	}
}

// Reflection is restricted to DTOs explicitly supplied below. It cannot expose
// database/service structs or unexported fields, and invokes no marshaler code.
func schemaDTO(value any, required ...string) map[string]any {
	shape := schemaType(reflect.TypeOf(value))
	if len(required) > 0 {
		shape["required"] = required
	} else {
		delete(shape, "required")
	}
	return shape
}
func schemaType(t reflect.Type) map[string]any {
	if t.Kind() == reflect.Pointer {
		shape := schemaType(t.Elem())
		if shape["type"] == nil {
			return map[string]any{"oneOf": []any{shape, map[string]any{"type": "object", "nullable": true, "enum": []any{nil}}}}
		}
		shape["nullable"] = true
		return shape
	}
	if t == reflect.TypeOf(time.Time{}) {
		return schemaKind("date")
	}
	if t == reflect.TypeOf(social.Decimal("")) {
		return map[string]any{"oneOf": []any{schemaKind("str"), schemaKind("num")}, "description": "Decimal amount accepts a JSON string or number; native validation rejects invalid or excessive precision."}
	}
	if t == reflect.TypeOf(json.RawMessage{}) || t.Kind() == reflect.Interface {
		return map[string]any{"description": "Explicit extensible JSON value, validated by the owning domain.", "x-native-flexible-json": true}
	}
	switch t.Kind() {
	case reflect.Bool:
		return schemaKind("bool")
	case reflect.String:
		return schemaKind("str")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint64:
		return schemaKind("int")
	case reflect.Float32, reflect.Float64:
		return schemaKind("num")
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return map[string]any{"type": "string", "format": "byte", "nullable": true}
		}
		return map[string]any{"type": "array", "nullable": true, "items": schemaType(t.Elem())}
	case reflect.Array:
		return map[string]any{"type": "array", "items": schemaType(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaType(t.Elem()), "description": "Explicit extensible JSON object, validated by the owning domain.", "x-native-flexible-json": true}
	case reflect.Struct:
		props, required := map[string]any{}, []string{}
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath != "" {
				continue
			}
			tag := strings.Split(field.Tag.Get("json"), ",")
			if tag[0] == "-" {
				continue
			}
			if field.Anonymous && tag[0] == "" {
				embedded := schemaType(field.Type)
				for name, value := range embedded["properties"].(map[string]any) {
					props[name] = value
				}
				if names, ok := embedded["required"].([]string); ok {
					required = append(required, names...)
				}
				continue
			}
			name := tag[0]
			if name == "" {
				name = field.Name
			}
			props[name] = schemaType(field.Type)
			if !strings.Contains(field.Tag.Get("json"), ",omitempty") {
				required = append(required, name)
			}
		}
		return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
	default:
		panic("unsupported documented DTO: " + t.String())
	}
}

type nativeAPIContract struct {
	request, response map[string]any
	status            int
	requestType       string
	responseType      string
	optionalBody      bool
	public            bool
	description       string
}

func privateSchemas(schemas map[string]any) {
	publicDocumentationSchemas(schemas)
	definitions := map[string]string{
		"APIError":                "detail:str",
		"AccessPreferences":       "needs_step_free:bool needs_accessible_toilet:bool needs_hearing_loop:bool prefers_quiet:bool",
		"AccountSettings":         "muted_kinds:[]str access:AccessPreferences",
		"AvatarChoice":            "generation:int name:str",
		"AvatarPreview":           "generation:int name:str uri:str current:bool",
		"AvatarStyle":             "generation:int generation_name:str available:[]AvatarChoice previews~:[]AvatarPreview",
		"Progression":             "count:int level:int max_level:int",
		"SelfAccount":             "public_id:uuid username:str display_name:str age_band:str cohort:str role:str is_identity_verified:bool requires_parental_consent:bool is_guardian:bool can_participate:bool progression:Progression avatar:str avatar_style:AvatarStyle",
		"WardAccount":             "public_id:uuid username:str display_name:str age_band:str cohort:str is_active:bool can_participate:bool",
		"GuardianInvite":          "token:str guardian:str guardian_public_id:uuid ward:str ward_public_id:uuid relationship:str status:str created_at:date expires_at:date",
		"GuardianRelationship":    "id:int guardian:uuid ward:uuid relationship:str status:str",
		"Credentials":             "username:str password:str email~:str name~:str",
		"AuthSession":             "authenticated:bool user:AuthUser?",
		"AuthProviders":           "password:bool google:bool facebook:bool",
		"Activity":                "id:int title:str description:str meeting_point:str what_to_bring:str organizer_note:str cost_band:str difficulty:str accessibility_notes:str beginners_welcome:bool owner:str place:int? activity_type:str cohort:str starts_at:date ends_at:date? join_threshold:num owner_can_override:bool capacity:int? min_to_go:int? status:str guardian_accompanied:bool supervised:bool is_publicly_listed:bool open_positions:int? created_at:date",
		"Membership":              "id:int activity:int user:str role:str state:str attendance_intent:str arrived_at:date? transit_status:str departing_at:date? created_at:date decided_at:date?",
		"SharePreview":            "kind:str id~:int title~:str",
		"Post":                    "id:int author:str body:str is_announcement:bool reply_to:int? share:SharePreview? created_at:date",
		"Group":                   "id:int title:str description:str owner:str area:str category:str? activity_type:str? tier:str cohort:str status:str is_staff_curated:bool is_publicly_listed:bool created_at:date",
		"GroupMember":             "public_id:uuid display_name:str role:str",
		"Series":                  "id:int title:str description:str meeting_point:str what_to_bring:str organizer_note:str cost_band:str difficulty:str accessibility_notes:str beginners_welcome:bool owner:str place:int activity_type:str cohort:str cadence:str status:str next_starts_at:date join_threshold:num capacity:int? min_to_go:int? guardian_accompanied:bool supervised:bool created_at:date",
		"Gauge":                   "id:int proposer:str place:str activity_type:str cohort:str coarse_window:str ready:bool remaining:int expires_at:date created_at:date",
		"PlaceProposal":           "id:int place_id:int place_name:str lon:num lat:num status:str required_confirmations:int confirmations_count:int created_at:date",
		"UserReference":           "public_id:uuid display_name:str",
		"Connection":              "id:int status:str requester:UserReference addressee:UserReference created_at:date",
		"SharedContext":           "activities:[]str activity_count:int activity_overflow:int groups:[]str group_count:int group_overflow:int join_request:bool",
		"ProfileCard":             "tier:str public_id:uuid display:str avatar:str minor:bool username~:str verified~:bool shared~:SharedContext connected~:bool can_connect~:bool request_pending~:bool can_message~:bool interests~:[]str? show_photo~:bool",
		"Community":               "slug:str name:str tier:str area:str category:str? activity_type:str?",
		"CommunityGraphNode":      "id:str label:str kind:str drill~:str activity_count~:int",
		"CommunityGraphEdge":      "source:str target:str kind:str",
		"CommunityGraph":          "nodes:[]CommunityGraphNode links:[]CommunityGraphEdge",
		"Attendance":              "going:int total:int min_to_go:int? met_minimum:bool? remaining_needed:int?",
		"ConsoleReadiness":        "missing_what_to_bring:bool near_capacity:bool",
		"ConsoleActivity":         "id:int title:str starts_at:date pending_joins:int support_companions:int needs_supervisor:bool missing_meeting_point:bool readiness:ConsoleReadiness quorum:Attendance venue_flag:bool",
		"ConsoleSeries":           "id:int title:str cadence:str next_starts_at:date",
		"ConsoleGroup":            "id:int title:str",
		"OrganizerConsole":        "activities:[]ConsoleActivity series:[]ConsoleSeries groups:[]ConsoleGroup",
		"MessagingUser":           "public_id:uuid username:str display_name:str avatar:str",
		"ConversationParticipant": "user:MessagingUser? state:str role:str joined_at:date?",
		"Conversation":            "id:int kind:str title:str cohort:str disappearing_seconds:int created_at:date updated_at:date my_state:str? my_role:str? participants:[]ConversationParticipant",
		"OwnMessagingKey":         "key_id:str algorithm:str public_jwk:PublicJWK wrapped_private_jwk:WrappedPrivateKey created_at:date",
		"ContactMessagingKey":     "key_id:str algorithm:str public_jwk:PublicJWK user:MessagingUser created_at:date fingerprint:str verified:bool",
		"ParticipantKey":          "public_id:uuid username:str display_name:str role:str public_jwk:PublicJWK fingerprint:str",
		"KeyVerification":         "fingerprint:str verified:bool",
		"MessageRecipientKey":     "ephemeral_public_jwk:PublicJWK wrapped_key:str wrap_iv:str",
		"EncryptedMessage":        "id:int conversation:int sender:MessagingUser? algorithm:str ciphertext:str iv:str created_at:date key:MessageRecipientKey?",
		"Notification":            "id:int kind:str title:str body:str url:str is_read:bool reason:str created_at:date",
		"Booking":                 "id:int place:int activity:int? provider:str external_ref:str status:str starts_at:date ends_at:date? party_size:int deep_link:str created_at:date",
		"BookingOption":           "provider:str bookable_in_app:bool deep_link:str instructions:str",
		"BookingProvider":         "slug:str supports_realtime:bool",
		"Donation":                "id:int amount_cents:int currency:str recurring:bool campaign:int? provider:str status:str created_at:date",
		"DonationCheckout":        "id:int amount_cents:int currency:str recurring:bool campaign:int? provider:str status:str created_at:date checkout_url:str",
		"SavedSearch":             "id:int activity_type:str? category:str? area:str? beginners:bool cost_band:str coarse_window:str created_at:date",
		"TaxonomyOption":          "slug:str name:str",
		"Report":                  "id:int reason:str detail:str status:str created_at:date",
		"ReportTriage":            "severity:int involves_child:bool open_duplicates:int contact_hint:bool contact_terms:[]str",
		"ModeratorReport":         "id:int reason:str detail:str status:str target_type:int target_id:int target:str? reporter:int? handled_by:int? handled_at:date? resolution:str created_at:date triage:ReportTriage?",
		"OwnAppeal":               "action_label:str reason_label:str status:str status_label:str statement:str created_at:date decided_at:date?",
		"ModeratorAppeal":         "id:int action:int appellant:int? statement:str status:str decided_by:int? decision_notes:str decided_at:date? created_at:date content_author_deleted:bool",
		"ReferralProof":           "subject_ref:str reason_label:str authority:str reference:str created_at:date anchor_hash:str chain_valid:bool",
		"Concern":                 "id:int kind:str post:int? subject_user:int? payload:ConcernPayload status:str handled_by:int? handled_at:date? created_at:date",
		"SafetyDecision":          "action_id:int action_label:str reason_label:str scope:str created_at:date is_sanction:bool is_active:bool can_appeal:bool appeal_status_label:str? content_author_deleted:bool",
		"OwnReportRecord":         "reason_label:str status_label:str created_at:date handled_at:date? detail:str resolution:str",
		"SafetyRecord":            "decisions:[]SafetyDecision reports:[]OwnReportRecord decisions_total:int decisions_truncated:bool reports_total:int reports_truncated:bool",
		"Health":                  "status:str version:str",
		"Readiness":               "status:str draining:bool database~:bool cache~:bool storage~:bool",
		"OpsStats":                "users:int activities:int posts:int bookings:int donations_completed:int donations_total_cents:int",
		"DiscoveryActivity":       "id:int title:str cohort:str starts_at:date status:str activity_type:str place_id:int? distance_m:num? description~:str place_name~:str? visual~:Visual reason~:str match_score~:num",
		"DiscoveryPlace":          "id:int name:str address_city:str lon:num? lat:num? distance_m:num? is_bookable:bool website:str activities:[]str",
		"DiscoveryEvent":          "id:int title:str starts_at:date ends_at:date? url:str activity_type:str? place_id:int? place_name:str? distance_m:num? reason~:str",
		"GroupUpdate":             "group_id:int group_title:str body:str created_at:date",
		"DiscoveryFeed":           "recommended:[]DiscoveryActivity beginners:[]DiscoveryActivity events:[]DiscoveryEvent group_updates:[]GroupUpdate",
		"ActivityDeck":            "deck_seed:str next_cursor:str items:[]DeckActivity",
		"Visual":                  "kind:str url~:str alt~:str attribution~:str license_name~:str source_page_url~:str source~:str",
		"AttributionCredit":       "attribution:str license_name:str provenance_url:str",
	}
	for name, fields := range definitions {
		schemas[name] = schemaFields(fields)
	}
	for name, value := range map[string]any{
		"AuthUser": authcore.User{}, "ActivityInput": social.ActivityInput{}, "SeriesInput": social.SeriesInput{}, "PostInput": social.PostInput{}, "GroupInput": social.GroupInput{}, "GaugeInput": social.GaugeInput{}, "GaugeConversion": social.GaugeConversion{}, "PlaceProposalInput": social.PlaceProposalInput{}, "BookingInput": booking.Request{}, "DonationInput": donations.Request{}, "SavedSearchInput": recommendations.SavedSearchInput{}, "ModerationActionInput": safety.ActionInput{}, "ReferralInput": safety.ReferralInput{}, "EncryptedMessageInput": messaging.MessageInput{}, "Photo": media.Photo{}, "ActivityCover": media.Cover{},
	} {
		schemas[name] = schemaDTO(value)
	}
	// Struct responses retain required serialization fields; request DTOs permit
	// domain defaults. No required field is inferred from a Go zero value.
	for name, value := range map[string]any{"AuthUser": authcore.User{}, "Photo": media.Photo{}, "ActivityCover": media.Cover{}} {
		schemas[name] = schemaType(reflect.TypeOf(value))
	}
	schemas["AccessPreferencesInput"] = schemaFields("needs_step_free~:bool needs_accessible_toilet~:bool needs_hearing_loop~:bool prefers_quiet~:bool")
	schemas["PublicJWK"] = schemaFields("kty:str x:str crv~:str y~:str alg~:str use~:str kid~:str ext~:bool key_ops~:[]str")
	schemas["PublicJWK"].(map[string]any)["properties"].(map[string]any)["kty"] = map[string]any{"type": "string", "enum": []string{"EC", "OKP"}}
	schemas["PublicJWK"].(map[string]any)["description"] = "Public JSON Web Key. The native EC/OKP field whitelist rejects private key material and unknown fields."
	schemas["WrappedPrivateKey"] = map[string]any{"type": "object", "nullable": true, "additionalProperties": true, "description": "Optional client-encrypted recovery object; native validation rejects plaintext private-key material.", "x-native-flexible-json": true}
	schemas["ConcernPayload"] = schemaFields("post_ids~:[]int window_days~:int author_id~:int flagger_set_size~:int")
	schemas["ConcernPayload"].(map[string]any)["description"] = "Moderator-only incident facts determined by concern kind; never a public profile."
	for name, required := range map[string][]string{"ActivityInput": {"place", "activity_type", "title", "starts_at"}, "SeriesInput": {"place", "activity_type", "title", "first_starts_at", "cadence"}, "GroupInput": {"city", "activity_type", "title"}, "GaugeConversion": {"title", "starts_at"}, "BookingInput": {"place", "starts_at"}, "DonationInput": {"amount_cents"}, "EncryptedMessageInput": {"ciphertext", "iv", "recipient_keys"}, "GaugeInput": {"place", "activity_type", "coarse_window"}, "PlaceProposalInput": {"name", "activity_type"}} {
		schemas[name].(map[string]any)["required"] = required
	}
	input := schemas["EncryptedMessageInput"].(map[string]any)["properties"].(map[string]any)["recipient_keys"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	input["ephemeral_public_jwk"] = schemaKind("PublicJWK")
	schemas["PostInput"].(map[string]any)["description"] = "A nonempty body or exactly one share target is required; the service rechecks thread write access and share visibility."
	privateExportSchemas(schemas)
}

func listingSchema() map[string]any {
	return map[string]any{"oneOf": []any{schemaFields("listed:bool"), schemaFields("is_publicly_listed:bool")}, "description": "Exactly one spelling is accepted; both or neither are invalid."}
}

func cursorSchema(item string, versioned bool) map[string]any {
	if versioned {
		return schemaFields("next_cursor:str limit:int results:[]" + item)
	}
	return schemaKind("[]" + item)
}
func offsetSchema(item string) map[string]any {
	return schemaFields("count:int next:str? previous:str? results:[]" + item)
}

func privateContract(method, path string) (nativeAPIContract, bool) {
	versioned := strings.HasPrefix(path, "/api/v1/")
	path = strings.Replace(path, "/api/v1/", "/api/", 1)
	c := nativeAPIContract{status: 200, requestType: "application/json"}
	ref := schemaKind
	req := func(fields string) { c.request = schemaFields(fields) }
	read := func(model string) { c.response = ref(model) }
	create := func(input, model string) { c.request, c.response, c.status = ref(input), ref(model), 201 }
	switch path {
	case "/api/schema/":
		c.public = true
		read("OpenAPIDocument")
		c.description = "Public OpenAPI 3.0.3 contract metadata; contains no live records or credential values."
	case "/api/docs/":
		c.public = true
		c.response = schemaKind("str")
		c.responseType = "text/html"
		c.description = "Public native API documentation page linking the machine-readable schema."
	case "/api/auth/csrf":
		c.public = true
		c.response = schemaFields("csrf_token:str")
	case "/api/auth/me":
		c.public = true
		read("AuthSession")
	case "/api/auth/providers":
		c.public = true
		read("AuthProviders")
	case "/api/auth/login", "/api/auth/signup":
		c.public = true
		c.request = ref("Credentials")
		read("AuthSession")
		if strings.HasSuffix(path, "signup") {
			c.status = 201
		}
	case "/api/auth/logout":
		c.public = true
		c.response = schemaFields("authenticated:bool")
	case "/api/auth/oauth/{provider}/start", "/api/auth/oauth/{provider}/callback":
		c.public = true
		c.status = 302
		if strings.Contains(path, "callback") {
			c.status = 303
		}
		c.description = "OAuth browser redirect; provider activation and state/nonce checks apply."
	case "/api/auth/token/":
		c.public = true
		if method == "DELETE" {
			c.public = false
			c.status = 204
		} else {
			req("username:str password:str")
			c.response = schemaFields("token:str")
		}
	case "/api/accounts/me/":
		if method == "DELETE" {
			c.status = 204
		} else {
			read("SelfAccount")
		}
	case "/api/accounts/me/settings/":
		read("AccountSettings")
		if method == "PUT" {
			req("muted_kinds~:[]str? access~:AccessPreferencesInput?")
		}
	case "/api/accounts/me/avatar-style/":
		read("AvatarStyle")
		if method == "POST" {
			req("generation:int")
		}
	case "/api/accounts/me/export/", "/api/accounts/wards/{public_id}/export/":
		read("AccountExport")
	case "/api/accounts/wards/":
		read("[]WardAccount")
	case "/api/accounts/wards/{public_id}/":
		if method == "DELETE" {
			c.status = 204
		} else {
			read("WardAccount")
			if method == "PATCH" {
				req("display_name:str")
			}
		}
	case "/api/accounts/wards/{public_id}/consent/":
		if method == "DELETE" {
			c.status = 204
		} else {
			c.status = 201
			read("WardAccount")
		}
	case "/api/accounts/guardian-links/":
		if method == "GET" {
			read("[]GuardianInvite")
		} else {
			req("ward:str relationship~:str")
			c.status = 201
			read("GuardianInvite")
		}
	case "/api/accounts/guardian-links/{token}/accept/":
		read("SelfAccount")
	case "/api/accounts/guardian-links/{token}/decline/":
		c.status = 204
	case "/api/accounts/verify-age/start/":
		read("AgePresentation")
	case "/api/accounts/verify-age/":
		req("state:str vp_token:str holder_binding_proof~:str")
		read("SelfAccount")
	case "/api/social/activities/":
		if method == "POST" {
			create("ActivityInput", "Activity")
		} else {
			c.response = offsetSchema("Activity")
		}
	case "/api/social/activities/{id}/":
		read("Activity")
		if method == "PATCH" {
			read("Activity")
			c.request = ref("ActivityPatch")
		}
	case "/api/social/activities/mine/":
		c.response = cursorSchema("Membership", versioned)
	case "/api/social/memberships/":
		c.response = offsetSchema("Membership")
	case "/api/social/memberships/{id}/", "/api/social/memberships/{id}/admit/":
		read("Membership")
	case "/api/social/memberships/{id}/vote/":
		req("approve:bool")
		read("Membership")
	case "/api/social/groups/":
		if method == "POST" {
			create("GroupInput", "Group")
		} else {
			c.response = cursorSchema("Group", versioned)
		}
	case "/api/social/groups/{id}/":
		read("Group")
	case "/api/social/groups/{id}/roster/":
		c.response = schemaFields("members:[]str")
	case "/api/social/groups/{id}/activities/":
		read("[]DiscoveryActivity")
	case "/api/social/series/":
		if method == "POST" {
			create("SeriesInput", "Series")
		} else {
			c.response = cursorSchema("Series", versioned)
		}
	case "/api/social/series/{id}/":
		read("Series")
	case "/api/social/gauges/":
		if method == "POST" {
			create("GaugeInput", "Gauge")
		} else {
			c.response = cursorSchema("Gauge", versioned)
		}
	case "/api/social/gauges/{id}/":
		read("Gauge")
	case "/api/social/place-proposals/":
		if method == "POST" {
			create("PlaceProposalInput", "PlaceProposal")
		} else {
			c.response = cursorSchema("PlaceProposal", versioned)
		}
	case "/api/social/place-proposals/{id}/confirm/":
		read("PlaceProposal")
	case "/api/social/organizer-console/":
		read("OrganizerConsole")
	case "/api/connections/connections/", "/api/connections/connections/search/":
		read("[]UserReference")
	case "/api/connections/connections/pending/":
		c.response = schemaFields("incoming:[]Connection outgoing:[]Connection")
	case "/api/connections/connections/request_to/":
		req("public_id:uuid")
		read("Connection")
		c.status = 201
	case "/api/connections/connections/remove/":
		req("public_id:uuid")
		c.status = 204
	case "/api/connections/people/{public_id}/":
		read("ProfileCard")
		c.description = "Current same-cohort visibility tier determines optional fields. A stranger receives only tier/public_id/display/avatar/minor; minor pairs never receive declared interests or photo permission. Blocked, inactive and cross-cohort targets are indistinguishable from missing targets."
	case "/api/communities/communities/":
		read("[]Community")
	case "/api/communities/communities/{slug}/":
		read("Community")
	case "/api/communities/communities/{slug}/activities/":
		read("[]DiscoveryActivity")
	case "/api/communities/communities/graph/":
		read("CommunityGraph")
	case "/api/messaging/keys/":
		read("OwnMessagingKey")
		if method == "POST" {
			req("public_jwk:PublicJWK algorithm~:str wrapped_private_jwk~:WrappedPrivateKey")
			c.status = 201
		}
	case "/api/messaging/keys/{username}/":
		read("ContactMessagingKey")
	case "/api/messaging/verify/":
		req("username:str fingerprint:str")
		read("KeyVerification")
	case "/api/messaging/conversations/":
		if method == "POST" {
			req("kind~:str username~:str usernames~:UsernameList title~:str")
			c.status = 201
			read("Conversation")
		} else {
			c.response = cursorSchema("Conversation", versioned)
		}
	case "/api/messaging/guardian/conversations/":
		c.response = cursorSchema("Conversation", versioned)
	case "/api/messaging/conversations/{id}/participants/":
		req("username~:str")
		c.optionalBody = true
		if method == "DELETE" {
			c.status = 204
		} else {
			c.status = 201
			read("Conversation")
		}
	case "/api/messaging/conversations/{id}/disappearing/":
		req("seconds:int")
		read("Conversation")
	case "/api/messaging/conversations/{id}/guardian/":
		read("Conversation")
		c.status = 201
	case "/api/messaging/conversations/{id}/keys/":
		read("[]ParticipantKey")
	case "/api/messaging/conversations/{id}/messages/":
		if method == "POST" {
			create("EncryptedMessageInput", "EncryptedMessage")
		} else {
			c.response = cursorSchema("EncryptedMessage", versioned)
		}
	case "/api/messaging/conversations/{id}/messages/{message_id}/report/":
		req("reason:str detail~:str decrypted_excerpt~:str")
		c.status = 201
		c.response = schemaFields("detail:str")
	case "/api/media/photos/":
		req("file:binary kind~:str thread~:int")
		c.requestType = "multipart/form-data"
		c.status = 201
		read("Photo")
	case "/api/media/photos/{id}/":
		if method == "DELETE" {
			c.status = 204
		} else {
			read("Photo")
		}
	case "/api/media/threads/{thread}/photos/":
		read("[]Photo")
	case "/api/media/activity-covers/{activity}/":
		if method == "DELETE" {
			c.status = 204
		} else {
			read("ActivityCover")
			if method == "PUT" {
				req("file:binary alt_text~:str")
				c.requestType = "multipart/form-data"
			} else {
				c.public = true
			}
		}
	case "/api/media/file/{token}/", "/api/media/attachment/{token}/", "/api/media/activity-cover-file/{token}/", "/api/media/place-cover-file/{token}/":
		c.response = ref("binary")
		c.public = strings.Contains(path, "cover-file")
		c.description = "Signed media delivery rechecks current scan, privacy, cohort and ownership gates; cover tokens may be public only for a currently published adult activity or venue. Supports one byte range and optional private storage redirect."
	case "/api/booking/options/":
		read("BookingOption")
	case "/api/booking/providers/":
		read("[]BookingProvider")
	case "/api/booking/bookings/":
		if method == "POST" {
			create("BookingInput", "Booking")
		} else {
			c.response = offsetSchema("Booking")
		}
	case "/api/booking/bookings/{id}/", "/api/booking/bookings/{id}/cancel/":
		read("Booking")
	case "/api/donations/":
		c.public = true
		create("DonationInput", "DonationCheckout")
	case "/api/donations/mine/":
		read("[]Donation")
	case "/api/donations/total/":
		c.public = true
		c.response = schemaFields("currency:str total_cents:int")
	case "/api/donations/webhook/":
		c.public = true
		c.request = ref("PaymentWebhook")
		c.response = map[string]any{"anyOf": []any{ref("Donation"), schemaFields("status:str")}}
		c.description = "Provider-authenticated webhook: signature validation and idempotent native payment transitions apply; browser session/CSRF does not authorize this route."
	case "/api/notifications/":
		c.response = schemaFields("unread_count:int results:[]Notification")
		if versioned {
			c.response = schemaFields("unread_count:int next_cursor:str limit:int results:[]Notification")
		}
	case "/api/notifications/{id}/read/":
		read("Notification")
	case "/api/notifications/read-all/":
		c.response = schemaFields("marked_read:int")
	case "/api/recommendations/interests/":
		c.response = schemaFields("interests:[]str")
		if method == "PUT" {
			req("interests~:[]str activity_types~:[]str")
			c.response = schemaFields("interests:[]str ignored:[]str")
		}
	case "/api/recommendations/topics/":
		c.response = schemaFields("topics:[]str")
		if method == "PUT" {
			req("topics:[]str")
			c.response = schemaFields("topics:[]str ignored:[]str")
		}
	case "/api/recommendations/interests/options/":
		c.response = schemaFields("options:[]TaxonomyOption")
	case "/api/recommendations/activities/":
		c.response = schemaFields("results:[]RecommendedActivity")
	case "/api/saved-searches/saved-searches/":
		if method == "POST" {
			create("SavedSearchInput", "SavedSearch")
		} else {
			read("[]SavedSearch")
		}
	case "/api/saved-searches/saved-searches/{id}/":
		if method == "DELETE" {
			c.status = 204
		} else {
			read("SavedSearch")
		}
	case "/api/safety/reports/":
		req("target_type:str target_id:int reason:str detail~:str")
		c.status = 201
		read("Report")
	case "/api/safety/blocks/":
		req("user_id:int")
		c.status = 204
	case "/api/safety/appeals/":
		if method == "POST" {
			req("action_id:int statement:str")
			c.status = 201
			c.response = schemaFields("status:str detail:str")
		} else {
			read("[]OwnAppeal")
		}
	case "/api/safety/me/record/":
		read("SafetyRecord")
	case "/api/safety/moderation/reports/":
		read("[]ModeratorReport")
	case "/api/safety/moderation/reports/{id}/resolve/":
		c.request = ref("ModerationActionInput")
		read("ModeratorReport")
	case "/api/safety/moderation/appeals/":
		read("[]ModeratorAppeal")
	case "/api/safety/moderation/appeals/{id}/resolve/":
		req("grant:bool notes~:str")
		read("ModeratorAppeal")
	case "/api/safety/moderation/referrals/":
		create("ReferralInput", "ReferralProof")
	case "/api/safety/moderation/referrals/{id}/proof/":
		read("ReferralProof")
	case "/api/safety/moderation/concerns/":
		read("[]Concern")
	case "/api/safety/moderation/concerns/{id}/resolve/":
		req("action:str note~:str")
		c.response = schemaFields("ok:bool note_delivered:bool")
	case "/api/health", "/api/health/":
		c.public = true
		read("Health")
	case "/api/ready", "/api/ready/":
		c.public = true
		read("Readiness")
	case "/api/ops/stats/":
		read("OpsStats")
	case "/api/ops/csp-report/":
		c.public = true
		c.request = ref("CSPReport")
		c.requestType = "application/csp-report"
		c.status = 204
		c.optionalBody = true
	case "/api/discovery/near-me/":
		c.public = true
		c.response = cursorSchema("DiscoveryPlace", versioned)
	case "/api/discovery/happening/":
		c.public = true
		c.response = cursorSchema("DiscoveryEvent", versioned)
	case "/api/discovery/activities/":
		c.response = cursorSchema("DiscoveryActivity", versioned)
	case "/api/discovery/activity-deck/":
		read("ActivityDeck")
	case "/api/discovery/feed/":
		read("DiscoveryFeed")
	default:
		if strings.HasPrefix(path, "/api/social/activities/{id}/") && method == "POST" {
			action := strings.TrimSuffix(strings.TrimPrefix(path, "/api/social/activities/{id}/"), "/")
			read("Activity")
			switch action {
			case "join", "guardians":
				c.status = 201
				read("Membership")
				req("on_behalf_of~:str")
				c.optionalBody = true
				if action == "guardians" {
					req("user_id:int on_behalf_of~:str")
					c.optionalBody = false
				}
			case "leave", "arrived", "departing", "grant_organizer", "revoke_organizer":
				read("Membership")
				if action == "leave" {
					req("on_behalf_of~:str")
					c.optionalBody = true
				}
				if strings.Contains(action, "organizer") {
					req("user_id:int")
				}
			case "transfer":
				req("user_id:int")
			case "cancel":
				req("reason~:str on_behalf_of~:str")
				c.optionalBody = true
			case "rsvp":
				req("intent:str on_behalf_of~:str")
				read("Attendance")
			case "transit":
				req("status:str")
				read("Membership")
			case "set_public_listing":
				c.request = listingSchema()
			case "supervision":
				req("supervised:bool")
			case "met_confirmed":
				req("confirmed~:bool")
				c.optionalBody = true
			case "support_companion":
				req("brings:bool")
			case "move":
				req("place:int")
			case "posts", "announce":
				create("PostInput", "Post")
			default:
				return c, false
			}
		} else if (strings.HasPrefix(path, "/api/social/activities/{id}/") || strings.HasPrefix(path, "/api/social/groups/{id}/")) && strings.HasSuffix(path, "/posts/") {
			if method == "POST" {
				create("PostInput", "Post")
			} else {
				c.response = cursorSchema("Post", versioned)
			}
		} else if strings.HasPrefix(path, "/api/social/groups/{id}/") && method == "POST" {
			action := strings.TrimSuffix(strings.TrimPrefix(path, "/api/social/groups/{id}/"), "/")
			read("Group")
			switch action {
			case "join", "archive":
			case "leave":
				c.response = schemaFields("left:bool")
			case "ask":
				req("prompt:str")
				c.response = schemaFields("sent:bool")
			case "set_public_listing":
				c.request = listingSchema()
			case "posts", "announce":
				create("PostInput", "Post")
			default:
				return c, false
			}
		} else if strings.HasPrefix(path, "/api/social/series/{id}/") && method == "POST" {
			read("Series")
			if strings.HasSuffix(path, "/next_note/") {
				req("note:str")
			}
		} else if strings.HasPrefix(path, "/api/social/gauges/{id}/") && method == "POST" {
			read("Gauge")
			if strings.HasSuffix(path, "/convert/") {
				create("GaugeConversion", "Activity")
			}
		} else if strings.HasPrefix(path, "/api/social/posts/{id}/") {
			if method == "DELETE" {
				c.status = 204
			} else if method == "PATCH" {
				req("body:str")
				read("Post")
			} else {
				req("")
				if strings.HasSuffix(path, "/reaction/") {
					req("emoji:str")
				}
				c.response = schemaFields("added:bool")
			}
		} else if strings.HasPrefix(path, "/api/connections/connections/{id}/") {
			read("Connection")
		} else if strings.HasPrefix(path, "/api/messaging/conversations/{id}/") {
			read("Conversation")
		} else {
			return c, false
		}
	}
	return c, true
}
