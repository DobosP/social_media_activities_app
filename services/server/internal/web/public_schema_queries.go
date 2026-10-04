package web

import "strings"

// Query contracts mirror the native HTTP adapters. Versioned cursor endpoints
// and legacy arrays deliberately do not advertise interchangeable pagination.
func privateQueryParameters(method, path string) []any {
	versioned := strings.HasPrefix(path, "/api/v1/")
	path = strings.Replace(path, "/api/v1/", "/api/", 1)
	fields := ""
	switch path {
	case "/api/auth/oauth/{provider}/start":
		fields = "next:str"
	case "/api/auth/oauth/{provider}/callback":
		fields = "state:str code:str error:str"
	case "/api/booking/options/":
		fields = "place:int"
	case "/api/booking/bookings/":
		if method == "GET" {
			fields = "limit:int offset:int"
		}
	case "/api/social/activities/":
		if method == "GET" {
			fields = "limit:int offset:int q:str"
		}
	case "/api/social/memberships/":
		fields = "limit:int offset:int"
	case "/api/social/activities/mine/":
		fields = "on_behalf_of:uuid"
		if versioned {
			fields += " limit:int cursor:str"
		}
	case "/api/social/groups/", "/api/social/series/", "/api/social/gauges/", "/api/social/place-proposals/":
		if method == "GET" && versioned {
			fields = "limit:int cursor:str"
		}
	case "/api/social/groups/{id}/posts/", "/api/social/activities/{id}/posts/":
		if method == "GET" {
			fields = "limit:int cursor:str on_behalf_of:uuid"
		}
	case "/api/connections/connections/search/":
		fields = "q:str"
	case "/api/messaging/conversations/", "/api/messaging/guardian/conversations/":
		if method == "GET" {
			fields = "q:str"
			if versioned {
				fields += " limit:int cursor:str"
			}
		}
	case "/api/messaging/conversations/{id}/messages/":
		if method == "GET" {
			fields = "limit:int after:int"
			if versioned {
				fields += " cursor:str"
			}
		}
	case "/api/messaging/conversations/{id}/participants/":
		fields = "username:str"
	case "/api/notifications/":
		fields = "unread:bool"
		if versioned {
			fields += " limit:int cursor:str"
		}
	case "/api/safety/moderation/appeals/", "/api/safety/moderation/concerns/", "/api/safety/moderation/reports/":
		fields = "status:str"
	case "/api/recommendations/activities/":
		fields = "limit:int near_lon:num near_lat:num radius_m:num"
	case "/api/discovery/activities/":
		fields = "activity:str near_lon:num near_lat:num radius_m:num"
		if versioned {
			fields += " limit:int cursor:str"
		}
	case "/api/discovery/activity-deck/":
		fields = "seed:str cursor:str limit:int activity:str beginners:bool near_lon:num near_lat:num radius_m:num"
	case "/api/discovery/feed/":
		fields = "near_lon:num near_lat:num radius_m:num"
	case "/api/discovery/near-me/":
		fields = "activity:str bookable:bool wellness:bool family_friendly:bool has_events:bool near_lon:num near_lat:num radius_m:num"
		if versioned {
			fields += " limit:int cursor:str"
		}
	case "/api/discovery/happening/":
		fields = "activity:str q:str days:int near_lon:num near_lat:num radius_m:num"
		if versioned {
			fields += " limit:int cursor:str"
		}
	case "/api/donations/total/":
		fields = "currency:str"
	}
	params := []any{}
	for _, field := range strings.Fields(fields) {
		parts := strings.SplitN(field, ":", 2)
		params = append(params, map[string]any{"in": "query", "name": parts[0], "required": path == "/api/booking/options/" && parts[0] == "place", "schema": schemaKind(parts[1])})
	}
	return params
}
