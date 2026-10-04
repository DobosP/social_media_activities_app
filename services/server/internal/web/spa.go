package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

// Source UI msgids and named routes are extracted from views_spa.py at port time.
// Serving has no dependency on the Python source or interpreter.
var spaStaticJSON = []byte(`
{"home":{"urls":{"browse":"/activities/","organizeNew":"/activities/new/","places":"/places/","series":"/activities/series/","interestsAction":"/interests/"},"ui":{"guardianRequests":"Guardian requests","accept":"Accept","decline":"Decline","organise":"Organise an activity","findPlace":"Find a place","series":"Recurring series","search":"Search","fromGroups":"From your groups","starterHead":"New here? Pick what you'd come to","starterSave":"Save & see matches","recommended":"Recommended for you","beginnersHead":"New here? These welcome beginners","mine":"Your activities","upcoming":"Upcoming for you","eventsHead":"Events you may like"},"title":"Social Activities"},"browse":{"urls":{"action":"/activities/","organizeNew":"/activities/new/"},"ui":{"title":"Upcoming activities","organizeOne":"Organize one →","search":"Search","clear":"clear","beginnersOnly":"beginners welcome only","showAll":"show all","list":"List","cards":"Cards","empty":"No upcoming activities match your search.","prev":"← Prev","next":"Next →","shuffle":"Shuffle"},"title":"Activities"},"organize":{"urls":{"organizeNew":"/activities/new/"},"ui":{"title":"Run my meetups","intro":"Everything you organise, with what each one needs next. Tap through to act.","activities":"Activities","allClear":"Nothing needed right now.","emptyLead":"You're not organising any upcoming activities.","emptyCta":"Organise one","seriesHead":"Recurring series","groupsHead":"Standing groups"},"title":"Run my meetups"},"events":{"urls":{"action":"/events/","rss":"/events/feed/","thingsIndex":"/things-to-do/"},"ui":{"title":"What's happening","subscribe":"Subscribe (RSS)","browseBy":"Browse by city & activity","lead":"Upcoming events at places near you. Find one you like, then organise an activity to go together.","searchPlaceholder":"Search events…","searchLabel":"Search events","anyArea":"Any area","areaLabel":"Area","search":"Search","clear":"clear","empty":"No upcoming events yet."},"title":"What's happening","public":true,"seo":{"description":"Upcoming events at real places in Cluj-Napoca — sport, outdoors, games, reading and culture you can join in person.","rss":{"url":"/events/feed/","title":"Upcoming events"}},"snapshot_template":"web/snapshots/events.html"},"places":{"urls":{"action":"/places/list/","map":"/places/"},"ui":{"title":"Places","lead":"A plain text list of venues — no map or JavaScript needed.","toMap":"Switch to the map","city":"City","activity":"Activity","filter":"Filter","truncated":"Showing the first 200 places — narrow with a city or activity filter.","empty":"No places match.","accessMatch":"Matches your access needs"},"title":"Places (text list)","public":true,"seo":{"description":"Parks, libraries and sports venues in Cluj-Napoca where people meet up for in-person group activities."},"snapshot_template":"web/snapshots/places.html"},"things-index":{"ui":{"title":"Things to do","lead":"Browse public venues and upcoming events by city and activity.","empty":"Nothing here yet — check back soon."},"title":"Things to do","public":true,"seo":{"description":"Find things to do in person — venues and upcoming events by city and activity, from sport to reading. A nonprofit, text-first way to meet up."},"snapshot_template":"web/snapshots/landing_index.html"},"things-city":{"ui":{},"public":true,"seo":{},"snapshot_template":"web/snapshots/landing_city.html"},"things-detail":{"urls":{"exploreCity":"/things-to-do/<slug:area_slug>/"},"ui":{"upcoming":"Upcoming events","places":"Places","subscribe":"Subscribe to this list (RSS)"},"public":true,"seo":{},"snapshot_template":"web/snapshots/landing_detail.html"},"you":{"ui":{"title":"Account & settings"},"title":"Account & settings"},"settings":{"ui":{"title":"Settings","language":"Language","languageHelp":"Choose the language this site is shown in.","save":"Save","apiAccess":"API access","revoke":"Revoke API access","noToken":"No device currently holds API access to your account.","yourAccount":"Your account","download":"Download my data","delete":"Delete my account"},"title":"Settings"},"profile":{"actions":{"avatarUpload":"/profile/avatar/","avatarStyle":"/profile/avatar-style/","connectionMessage":"/connections/message/","unblock":"/users/{pk}/unblock/","verifyAge":"/verify-age/","interestsEdit":"/interests/","connections":"/connections/"},"ui":{"title":"Profile","updatePhoto":"Update photo","avatarStyle":"Your avatar style","useStyle":"Use this style","journey":"Your journey","interests":"Interests","edit":"edit","connections":"Connections","message":"Message","review":"review","ageVerification":"Age verification","verifiedAs":"Verified as:","current":"Current","reVerify":"Re-verify","verifyEudi":"Verify with EU Digital Identity","blocked":"Blocked users","unblock":"unblock"},"title":"Profile"},"interests":{"action":"/interests/","ui":{"title":"Your interests","starterHead":"Popular near you right now","save":"Save interests"},"title":"Your interests"},"topics":{"action":"/topics/","ui":{"title":"Your topics","lean":"Topics to lean toward","empty":"No topics are available yet.","save":"Save topics"},"title":"Your topics"},"access":{"action":"/access/","ui":{"title":"Access preferences","save":"Save preferences"},"title":"Access preferences"},"notifications":{"actions":{"readAll":"/notifications/read-all/"},"urls":{"preferences":"/notifications/preferences/"},"ui":{"title":"Notifications","markAllRead":"Mark all read","settings":"Notification settings","new":"new","view":"view","empty":"No notifications yet."},"title":"Notifications"},"notification-preferences":{"action":"/notifications/preferences/","ui":{"title":"Notification settings","save":"Save settings"},"title":"Notification settings"},"connections":{"actions":{"search":"/connections/","request":"/connections/request/","respond":"/connections/{pk}/respond/","withdraw":"/connections/{pk}/withdraw/","message":"/connections/message/","remove":"/connections/remove/"},"ui":{"title":"Connections","searchLabel":"Search people you've met","search":"Search","resultsHead":"Search results","connect":"Connect","incoming":"Requests to you","accept":"Accept","decline":"Decline","outgoing":"Sent requests","pending":"pending","withdraw":"Withdraw","yours":"Your connections","filterLabel":"Filter your connections","filter":"Filter","clear":"clear","message":"Message","remove":"Remove","prev":"Previous","next":"Next"},"title":"Connections"},"saved-searches":{"actions":{"create":"/saved-searches/create/","delete":"/saved-searches/{pk}/delete/"},"ui":{"title":"Saved searches","createHead":"Save a new search","activityType":"Activity type","orCategory":"...or a whole category","city":"City (optional)","cost":"Cost (optional)","when":"When (optional)","beginners":"Beginners-welcome activities only","save":"Save search","yours":"Your saved searches","remove":"Remove"},"title":"Saved searches"},"communities":{"urls":{"graph":"/communities/graph/","createGroup":"/groups/new/","action":"/communities/"},"ui":{"title":"Communities & groups","graph":"Explore as a 3D graph","startGroup":"+ Start a group","groupsHead":"Groups — join and belong","groupsEmpty":"No groups here yet.","startFirst":"Start the first one.","communitiesHead":"Around your city","prev":"Previous","next":"Next"},"title":"Communities & groups"},"community-detail":{"urls":{"communities":"/communities/","organizeNew":"/activities/new/"},"ui":{"back":"Communities","empty":"No upcoming activities here right now.","organise":"Organise one"}}}
`)
var spaRouteAliases = map[string]string{"activity_list": "browse", "events_list": "events", "places_list": "places", "things_to_do_index": "things-index", "things_to_do_city": "things-city", "things_to_do": "things-detail", "topic_preferences": "topics", "access_preferences": "access", "notification_preferences": "notification-preferences", "saved_searches": "saved-searches", "community_detail": "community-detail"}

func spaCopy(value any) any {
	raw, _ := json.Marshal(value)
	var copy any
	_ = json.Unmarshal(raw, &copy)
	return copy
}
func (s *Server) spaTranslate(r *http.Request, value any) any {
	switch v := value.(type) {
	case string:
		return s.Renderer.catalog.translate(language(r), v, 1)
	case map[string]any:
		for key, item := range v {
			v[key] = s.spaTranslate(r, item)
		}
	case []any:
		for i, item := range v {
			v[i] = s.spaTranslate(r, item)
		}
	}
	return value
}
func spaMap(v any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	if v, ok := v.(map[string]any); ok {
		return v
	}
	if v, ok := v.(pongo2.Context); ok {
		return map[string]any(v)
	}
	raw, _ := json.Marshal(v)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		return map[string]any{}
	}
	return out
}
func spaRows(v any) []map[string]any {
	if v == nil {
		return []map[string]any{}
	}
	if v, ok := v.([]map[string]any); ok {
		return v
	}
	if obj := spaMap(v); obj["object_list"] != nil {
		return spaRows(obj["object_list"])
	}
	raw, _ := json.Marshal(v)
	var out []map[string]any
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		return []map[string]any{}
	}
	return out
}
func spaText(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
func spaBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	case string:
		return x != ""
	}
	return false
}
func spaFallback(v, f any) any {
	if v == nil || v == "" {
		return f
	}
	return v
}
func spaName(m map[string]any) string { return spaText(spaFallback(m["display_name"], m["username"])) }
func spaUsers(v any) []map[string]any {
	out := []map[string]any{}
	for _, u := range spaRows(v) {
		out = append(out, map[string]any{"publicId": spaFallback(u["public_id"], u["publicId"]), "name": spaFallback(u["name"], spaName(u))})
	}
	return out
}
func (s *Server) spaPage(v any, includeCount bool) map[string]any {
	p := spaMap(v)
	paginator := spaMap(p["paginator"])
	number := spaInt(p["number"])
	if number < 1 {
		number = 1
	}
	pages := spaInt(spaFallback(p["num_pages"], paginator["num_pages"]))
	if pages < 1 {
		pages = 1
	}
	var previous, next any
	if number > 1 {
		previous = number - 1
	}
	if number < pages {
		next = number + 1
	}
	out := map[string]any{"number": number, "numPages": pages, "previous": previous, "next": next}
	if includeCount {
		count := p["count"]
		if count == nil {
			count = paginator["count"]
		}
		out["count"] = spaInt(count)
	}
	return out
}

// BuildSPA projects the already-gated legacy context into the existing twenty
// React contracts. The public flag omits CSRF; cookie nav stays server-private.
func (s *Server) BuildSPA(ctx context.Context, r *http.Request, a platform.Actor, route string, legacy pongo2.Context) (payload map[string]any, title string, public bool, seo map[string]any, err error) {
	if alias := spaRouteAliases[route]; alias != "" {
		route = alias
	}
	var definitions map[string]map[string]any
	if json.Unmarshal(spaStaticJSON, &definitions) != nil {
		return nil, "", false, nil, platform.ErrInvalid
	}
	definition, ok := definitions[route]
	if !ok {
		return nil, "", false, nil, platform.ErrNotFound
	}
	data := map[string]any{}
	for _, key := range []string{"ui", "urls", "actions", "action"} {
		if value := definition[key]; value != nil {
			if key == "ui" {
				value = s.spaTranslate(r, value)
			}
			data[key] = value
		}
	}
	title = spaText(s.spaTranslate(r, definition["title"]))
	public = spaBool(definition["public"])
	if !public && a.ID < 1 {
		return nil, "", false, nil, platform.ErrForbidden
	}
	seo = spaMap(definition["seo"])
	for key, value := range seo {
		if key == "description" {
			seo[key] = s.spaTranslate(r, value)
		}
	}
	if legacy["structured_data"] != nil {
		seo["structured_data"] = legacy["structured_data"]
	}
	if legacy["breadcrumb_data"] != nil {
		seo["breadcrumb_data"] = legacy["breadcrumb_data"]
	}
	if route == "events" || route == "places" {
		seo["robots"] = ""
	}
	if spaBool(legacy["filtered"]) {
		seo["robots"] = "noindex, follow"
	}
	if definition["snapshot_template"] != nil {
		seo["snapshot_template"] = definition["snapshot_template"]
	}
	ui := spaMap(data["ui"])
	tr := func(text string) string { return s.Renderer.catalog.translate(language(r), text, 1) }
	nav := spaMap(legacy["nav"])
	if len(nav) == 0 {
		nav = map[string]any(legacy)
	}
	tabs, accountNav := s.spaAccountNav(r, nav)
	name := a.DisplayName
	if name == "" {
		name = a.Username
	}
	cards := func(key string) ([]map[string]any, error) { return s.spaActivityCards(ctx, r, a, legacy[key]) }
	switch route {
	case "home":
		sections := map[string]any{}
		for _, key := range []string{"recommended", "beginners", "upcoming", "mine"} {
			value, e := cards(key)
			if e != nil {
				return nil, "", false, nil, e
			}
			sections[key] = value
		}
		data["sections"] = sections
		data["starterTypes"] = spaChoices(legacy["starter_types"])
		events := []map[string]any{}
		for _, e := range spaRows(legacy["events"]) {
			meta := s.spaDate(r, e["starts_at"], "D j M, H:i")
			place := spaText(spaFallback(e["place_name"], spaMap(e["place"])["name"]))
			if place != "" {
				meta += " · " + place
			}
			events = append(events, map[string]any{"pk": spaID(e), "url": routeURL("event_detail", spaID(e)), "title": e["title"], "reason": spaFallback(e["feed_reason"], spaFallback(e["reason"], "")), "meta": meta})
		}
		data["events"] = events
		updates := []map[string]any{}
		for _, p := range spaRows(legacy["group_updates"]) {
			g := spaMap(spaMap(p["thread"])["group"])
			updates = append(updates, map[string]any{"url": routeURL("group_detail", spaFallback(p["group_id"], spaID(g))), "groupTitle": spaFallback(p["group_title"], g["title"]), "when": s.spaDate(r, p["created_at"], "D j M, H:i"), "snippet": spaTruncate(spaText(p["body"]), 18)})
		}
		data["groupUpdates"] = updates
		invites := []map[string]any{}
		for _, gi := range spaRows(legacy["guardian_invites"]) {
			invites = append(invites, map[string]any{"name": spaName(spaMap(gi["guardian"])), "relationship": gi["relationship"], "acceptAction": routeURL("guardian_invite_accept", gi["token"]), "declineAction": routeURL("guardian_invite_decline", gi["token"])})
		}
		data["guardianInvites"] = invites
		data["flags"] = map[string]any{"nearActive": spaBool(legacy["near_active"]), "beginnersOnly": spaBool(legacy["beginners_only"])}
		ui["greeting"] = strings.ReplaceAll(tr("Hi %(name)s"), "%(name)s", name)
	case "browse":
		value, e := cards("activities")
		if e != nil {
			return nil, "", false, nil, e
		}
		data["cards"] = value
		data["filters"] = map[string]any{"query": spaText(legacy["query"]), "beginnersOnly": spaBool(legacy["beginners_only"]), "nearActive": spaBool(legacy["near_active"]), "didYouMean": spaText(legacy["did_you_mean"]), "didYouMeanQ": spaText(legacy["did_you_mean_q"])}
		data["viewMode"] = spaFallback(legacy["view_mode"], "list")
		data["baseQs"] = spaText(legacy["base_qs"])
		data["page"] = s.spaPage(legacy["page_obj"], true)
	case "organize":
		value, e := s.spaOrganizer(ctx, r, a, legacy["activities"])
		if e != nil {
			return nil, "", false, nil, e
		}
		data["activities"] = value
		series := []map[string]any{}
		for _, item := range spaRows(legacy["series"]) {
			next := ""
			if item["next_starts_at"] != nil {
				next = strings.ReplaceAll(tr("Next: %(when)s"), "%(when)s", s.spaDate(r, item["next_starts_at"], "D d M, H:i"))
			}
			series = append(series, map[string]any{"pk": spaID(item), "url": routeURL("series_detail", spaID(item)), "title": item["title"], "cadence": tr(spaCadence(spaText(item["cadence"]))), "next": next})
		}
		data["series"] = series
		groups := []map[string]any{}
		for _, g := range spaRows(legacy["groups"]) {
			groups = append(groups, map[string]any{"pk": spaID(g), "url": routeURL("group_detail", spaID(g)), "title": g["title"]})
		}
		data["groups"] = groups
	case "events":
		data["events"], err = s.spaEventRows(ctx, r, legacy["events"])
		data["filters"] = map[string]any{"query": spaText(legacy["query"]), "activity": spaText(legacy["activity"]), "area": spaText(legacy["area"]), "areaName": spaText(legacy["area_name"])}
		data["areas"] = spaChoices(legacy["areas"])
	case "places":
		data["places"], err = s.spaPlaceRows(ctx, r, legacy["places"])
		data["filters"] = spaMap(legacy["filters"])
		data["flags"] = map[string]any{"nearActive": spaBool(legacy["near_active"]), "truncated": spaBool(legacy["truncated"])}
	case "things-index":
		cities := []map[string]any{}
		for _, row := range spaGrouped(legacy["grouped"]) {
			area := spaMap(row[0])
			cities = append(cities, map[string]any{"name": area["name"], "url": routeURL("things_to_do_city", area["slug"]), "links": s.spaLandingLinks(r, area, row[1])})
		}
		data["cities"] = cities
	case "things-city", "things-detail":
		area := spaMap(legacy["area"])
		city := spaText(area["name"])
		data["city"] = city
		breadcrumbs := []map[string]any{{"name": tr("Home"), "url": "/"}, {"name": tr("Things to do"), "url": routeURL("things_to_do_index")}, {"name": city, "url": nil}}
		if route == "things-city" {
			title = strings.ReplaceAll(tr("Things to do in %(city)s"), "%(city)s", city)
			data["links"] = s.spaLandingLinks(r, area, legacy["activities"])
			seo["description"] = strings.ReplaceAll(tr("Venues and upcoming events in %(city)s — by activity. Find people and go, in person."), "%(city)s", city)
		} else {
			typ := spaMap(legacy["activity_type"])
			activity := spaText(typ["name"])
			data["activity"] = activity
			title = strings.NewReplacer("%(activity)s", activity, "%(city)s", city).Replace(tr("%(activity)s in %(city)s"))
			data["events"], err = s.spaEventRows(ctx, r, legacy["events"])
			places := []map[string]any{}
			for _, p := range spaRows(legacy["places"]) {
				p = spaPlace(p)
				places = append(places, map[string]any{"url": s.spaPlacePath(p), "name": spaFallback(p["display_name"], p["name"]), "city": spaText(p["address_city"])})
			}
			data["places"] = places
			breadcrumbs[2]["url"] = routeURL("things_to_do_city", area["slug"])
			breadcrumbs = append(breadcrumbs, map[string]any{"name": activity, "url": nil})
			data["urls"] = map[string]any{"exploreCity": routeURL("things_to_do_city", area["slug"]), "rss": routeURL("events_feed") + "?activity=" + spaText(typ["slug"]) + "&area=" + spaText(area["slug"])}
			ui["exploreCity"] = strings.ReplaceAll(tr("Browse other activities in %(city)s →"), "%(city)s", city)
			seo["description"] = strings.NewReplacer("%(activity)s", activity, "%(city)s", city).Replace(tr("Where to do %(activity)s in %(city)s — public venues and upcoming events you can join in person. Nonprofit, no ads, no tracking."))
		}
		data["breadcrumbs"] = breadcrumbs
		ui["title"] = title
	case "you":
		data["name"] = name
		data["username"] = a.Username
		data["isGuardian"] = spaBool(legacy["is_guardian"])
		data["tabs"] = tabs
		data["nav"] = accountNav
	case "settings":
		data["tabs"] = tabs
		data["nav"] = accountNav
		data["language"] = map[string]any{"action": routeURL("set_language"), "next": r.URL.Path, "current": spaFallback(legacy["current_language"], language(r)), "options": spaLanguageOptions(legacy["languages"])}
		created := legacy["api_token_created"]
		revoke := ""
		if created != nil {
			revoke = routeURL("api_token_revoke")
		}
		data["apiToken"] = map[string]any{"created": s.spaDate(r, created, "D d M Y, H:i"), "revokeAction": revoke}
		data["account"] = map[string]any{"export": routeURL("account_export"), "delete": routeURL("account_delete")}
	case "profile":
		err = s.spaProfile(ctx, r, a, legacy, data, tabs)
	case "interests":
		chosen := spaSet(legacy["chosen"])
		groups := []map[string]any{}
		for _, row := range spaGrouped(legacy["groups"]) {
			groups = append(groups, map[string]any{"category": spaMap(row[0])["name"], "types": spaChecked(row[1], chosen, false)})
		}
		data["groups"] = groups
		data["starter"] = spaChecked(legacy["starter"], chosen, false)
		data["chosenCount"] = spaInt(legacy["chosen_count"])
	case "topics":
		data["topics"] = spaChecked(legacy["categories"], spaSet(legacy["chosen"]), true)
	case "access":
		pref := spaMap(legacy["pref"])
		fields := []map[string]any{}
		for _, field := range [][2]string{{"needs_step_free", "I need step-free access"}, {"needs_accessible_toilet", "I need an accessible toilet"}, {"needs_hearing_loop", "I need a hearing loop"}, {"prefers_quiet", "I prefer quiet / sensory-friendly places"}} {
			fields = append(fields, map[string]any{"name": field[0], "label": tr(field[1]), "checked": spaBool(pref[field[0]])})
		}
		data["fields"] = fields
	case "notifications":
		items := []map[string]any{}
		for _, n := range spaRows(legacy["items"]) {
			items = append(items, map[string]any{"title": n["title"], "body": spaText(n["body"]), "why": spaText(n["why"]), "when": s.spaDate(r, n["created_at"], "D d M, H:i"), "url": spaSafeHref(spaText(n["url"])), "unread": n["read_at"] == nil})
		}
		data["items"] = items
	case "notification-preferences":
		rows := []map[string]any{}
		for _, item := range spaRows(legacy["rows"]) {
			rows = append(rows, map[string]any{"value": item["value"], "label": item["label"], "reason": item["reason"], "muted": spaBool(item["muted"])})
		}
		data["rows"] = rows
	case "connections":
		data["searchQuery"] = spaText(legacy["query"])
		data["results"] = spaUsers(legacy["results"])
		data["connections"] = spaUsers(legacy["connections"])
		data["filterQuery"] = spaText(legacy["conn_query"])
		data["total"] = spaInt(legacy["conn_total"])
		data["page"] = s.spaPage(legacy["conn_page"], false)
		for _, key := range []string{"incoming", "outgoing"} {
			items := []map[string]any{}
			field := "requester"
			if key == "outgoing" {
				field = "addressee"
			}
			for _, item := range spaRows(legacy[key]) {
				user := spaMap(item[field])
				if len(user) == 0 {
					user = item
				}
				items = append(items, map[string]any{"pk": spaID(item), "user": spaUsers([]map[string]any{user})[0]})
			}
			data[key] = items
		}
	case "saved-searches":
		data["items"], err = s.spaSavedSearchRows(ctx, r, a, legacy["items"])
		data["options"] = map[string]any{"activityTypes": spaChoices(legacy["activity_types"]), "categories": spaChoices(legacy["categories"]), "costBands": spaValueLabels(legacy["cost_bands"]), "coarseWindows": spaValueLabels(legacy["coarse_windows"])}
		data["next"] = r.URL.Path
	case "communities":
		groups := []map[string]any{}
		for _, g := range spaRows(legacy["groups_page"]) {
			groups = append(groups, map[string]any{"pk": spaID(g), "url": routeURL("group_detail", spaID(g)), "title": g["title"], "type": spaModelLabel(g["activity_type"]), "category": spaModelLabel(g["category"]), "area": spaModelLabel(g["area"]), "description": spaTruncate(spaText(g["description"]), 22)})
		}
		communities := []map[string]any{}
		for _, c := range spaRows(legacy["page"]) {
			communities = append(communities, map[string]any{"slug": c["slug"], "url": routeURL("community_detail", c["slug"]), "name": c["name"], "tier": spaText(c["tier"]), "category": spaModelLabel(c["category"]), "area": spaModelLabel(c["area"])})
		}
		data["groups"] = groups
		data["communities"] = communities
		data["pages"] = map[string]any{"communities": s.spaPage(legacy["page"], false), "groups": s.spaPage(legacy["groups_page"], false)}
		data["canCreate"] = spaBool(legacy["can_create"])
	case "community-detail":
		c := spaMap(legacy["community"])
		title = spaText(c["name"])
		data["name"] = title
		typeName := spaModelLabel(c["activity_type"])
		if typeName == "" {
			typeName = spaModelLabel(c["category"])
		}
		data["lead"] = strings.NewReplacer("%(type_name)s", typeName, "%(area)s", spaModelLabel(c["area"])).Replace(tr("Upcoming %(type_name)s activities in %(area)s — soonest first."))
		var linked any
		if g := spaMap(legacy["linked_group"]); len(g) > 0 {
			linked = map[string]any{"url": routeURL("group_detail", spaID(g)), "label": strings.ReplaceAll(tr("Join the standing group: %(title)s"), "%(title)s", spaText(g["title"]))}
		}
		data["linkedGroup"] = linked
		data["cards"], err = cards("activities")
	}
	if err != nil {
		return nil, "", false, nil, err
	}
	csrf := ""
	if !public {
		csrf = spaText(spaFallback(legacy["csrf_token"], legacy["csrf_key"]))
		if csrf == "" {
			csrf = spaText(legacy["csrf"])
		}
		if csrf == "" {
			csrf = spaText(legacy["csrfmiddlewaretoken"])
		}
		if csrf == "" {
			return nil, "", false, nil, platform.ErrForbidden
		}
	}
	payload = map[string]any{"route": route, "title": title, "csrf": csrf, "data": data}
	return payload, title, public, seo, nil
}
