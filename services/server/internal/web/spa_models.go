package web

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	snapshot "github.com/DobosP/social_media_activities_app/services/server/internal/export"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
)

func spaInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := strconv.Atoi(string(v))
		return n
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}
func spaID(row map[string]any) int64 { return int64(spaInt(spaFallback(row["pk"], row["id"]))) }
func spaModelLabel(v any) string {
	if row := spaMap(v); len(row) > 0 {
		return spaText(row["name"])
	}
	if text, ok := v.(string); ok {
		return text
	}
	return ""
}
func spaTruncate(body string, count int) string {
	words := strings.Fields(body)
	if len(words) <= count {
		return strings.Join(words, " ")
	}
	return strings.Join(words[:count], " ") + " …"
}
func spaDateValue(value any) (time.Time, bool) {
	switch v := value.(type) {
	case time.Time:
		return v, true
	case *time.Time:
		if v != nil {
			return *v, true
		}
	case string:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02"} {
			if date, err := time.Parse(layout, v); err == nil {
				return date, true
			}
		}
	}
	return time.Time{}, false
}
func (s *Server) spaDate(r *http.Request, value any, format string) string {
	date, ok := spaDateValue(value)
	if !ok {
		return ""
	}
	weekdays := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	months := []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
	if language(r) == "ro" {
		weekdays = []string{"Dum", "Lun", "Mar", "Mie", "Joi", "Vin", "Sâm"}
		months = []string{"Ian", "Feb", "Mar", "Apr", "Mai", "Iun", "Iul", "Aug", "Sep", "Oct", "Noi", "Dec"}
	}
	var out strings.Builder
	for _, c := range format {
		switch c {
		case 'D':
			out.WriteString(weekdays[date.Weekday()])
		case 'j':
			out.WriteString(strconv.Itoa(date.Day()))
		case 'd':
			out.WriteString(fmt.Sprintf("%02d", date.Day()))
		case 'M':
			out.WriteString(months[date.Month()-1])
		case 'Y':
			out.WriteString(strconv.Itoa(date.Year()))
		case 'H':
			out.WriteString(fmt.Sprintf("%02d", date.Hour()))
		case 'i':
			out.WriteString(fmt.Sprintf("%02d", date.Minute()))
		default:
			out.WriteRune(c)
		}
	}
	return out.String()
}
func spaChoices(value any) []map[string]any {
	out := []map[string]any{}
	for _, row := range spaRows(value) {
		out = append(out, map[string]any{"slug": row["slug"], "name": row["name"]})
	}
	return out
}
func spaSet(value any) map[string]bool {
	out := map[string]bool{}
	raw, _ := json.Marshal(value)
	var strings []string
	if json.Unmarshal(raw, &strings) == nil {
		for _, v := range strings {
			out[v] = true
		}
		return out
	}
	for k, v := range spaMap(value) {
		out[k] = spaBool(v)
	}
	return out
}
func spaChecked(value any, chosen map[string]bool, description bool) []map[string]any {
	out := spaChoices(value)
	original := spaRows(value)
	for i, item := range out {
		item["checked"] = chosen[spaText(item["slug"])]
		if description {
			item["description"] = spaText(original[i]["description"])
		}
	}
	return out
}
func spaGrouped(value any) [][]any {
	raw, _ := json.Marshal(value)
	var out [][]any
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		return [][]any{}
	}
	return out
}
func spaValueLabels(value any) []map[string]any {
	out := []map[string]any{}
	for _, row := range spaGrouped(value) {
		if len(row) >= 2 {
			out = append(out, map[string]any{"value": row[0], "label": row[1]})
		}
	}
	if len(out) == 0 {
		for _, row := range spaRows(value) {
			out = append(out, map[string]any{"value": row["value"], "label": row["label"]})
		}
	}
	return out
}
func spaLanguageOptions(value any) []map[string]any {
	out := []map[string]any{}
	for _, row := range spaGrouped(value) {
		if len(row) >= 2 {
			out = append(out, map[string]any{"code": row[0], "name": row[1]})
		}
	}
	return out
}
func spaCadence(value string) string {
	return map[string]string{"weekly": "Weekly", "monthly": "Monthly"}[value]
}
func spaSafeHref(value string) string {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") {
		return value
	}
	return ""
}
func spaPlace(row map[string]any) map[string]any {
	if properties := spaMap(row["properties"]); len(properties) > 0 {
		properties["id"] = row["id"]
		properties["display_name"] = properties["name"]
		return properties
	}
	return row
}
func (s *Server) spaPlacePath(row map[string]any) string {
	return fmt.Sprintf("/places/%d/%s/", spaID(row), snapshot.Slug(spaText(spaFallback(row["display_name"], row["name"])), "place"))
}
func (s *Server) spaLandingLinks(r *http.Request, area map[string]any, value any) []map[string]any {
	out := []map[string]any{}
	for _, typ := range spaRows(value) {
		out = append(out, map[string]any{"url": routeURL("things_to_do", area["slug"], typ["slug"]), "label": spaText(typ["name"]) + " " + s.Renderer.catalog.translate(language(r), "in", 1) + " " + spaText(area["name"])})
	}
	return out
}
func (s *Server) spaAccountNav(r *http.Request, nav map[string]any) ([]map[string]any, map[string]any) {
	tr := func(text string) string { return s.Renderer.catalog.translate(language(r), text, 1) }
	link := func(label, name string) map[string]any {
		return map[string]any{"label": tr(label), "url": routeURL(name)}
	}
	tabs := []map[string]any{link("Overview", "you"), link("Profile", "profile"), link("Interests", "interests"), link("Display", "display_preferences"), link("Privacy & data", "my_privacy"), link("Donations", "my_donations")}
	if spaBool(nav["has_guardians"]) {
		tabs = append(tabs, link("Guardians", "guardianship"))
	}
	tabs = append(tabs, link("Settings", "settings"))
	profile := []map[string]any{link("Your profile", "profile"), link("Interests", "interests"), link("Your topics (tune suggestions)", "topic_preferences"), link("Access needs", "access_preferences"), link("Display (theme, text size, motion)", "display_preferences"), link("Age verification", "verify_age"), link("Notifications", "notifications"), link("Messages", "messages")}
	profile[6]["pill"] = spaInt(nav["unread_notifications"])
	if spaBool(nav["connections_enabled"]) {
		profile = append(profile, link("Connections", "connections"))
	}
	profile = append(profile, link("Saved searches", "saved_searches"), link("Notification settings", "notification_preferences"))
	privacy := []map[string]any{link("Privacy & your data", "my_privacy"), link("Your safety record", "safety_record"), link("Your activity log", "activity_log"), link("Wards you look after", "wards")}
	if spaBool(nav["has_guardians"]) {
		privacy = append(privacy, link("Your guardians", "guardianship"))
	}
	giving := []map[string]any{link("My donations", "my_donations"), link("Support this platform", "donate"), link("Campaigns", "campaigns"), link("Where the money goes", "transparency")}
	return tabs, map[string]any{"groups": []map[string]any{{"title": tr("Profile & preferences"), "links": profile}, {"title": tr("Privacy & safety"), "links": privacy}, {"title": tr("Giving"), "links": giving}}, "logoutAction": routeURL("logout"), "logoutLabel": tr("Log out")}
}

// HydrateActivities retains caller presentation annotations while resolving all
// private model facts through the canonical cohort/block gate in one query.
func (s *Server) HydrateActivities(ctx context.Context, a platform.Actor, value any) ([]map[string]any, error) {
	items := spaRows(value)
	if len(items) == 0 {
		return items, nil
	}
	ids := make([]int64, len(items))
	for i, item := range items {
		ids[i] = spaID(item)
	}
	if s.DB == nil {
		return nil, platform.ErrForbidden
	}
	rows, err := s.DB.Query(ctx, `SELECT `+social.ActivityProjectionSQL()+` || jsonb_build_object('activity_type_obj',jsonb_build_object('id',t.id,'slug',t.slug,'name',t.name),'place_obj',jsonb_build_object('id',p.id,'name',p.name,'display_name',`+catalog.PlaceDisplayNameSQL()+`,'address_city',p.address_city),'secondary_types',coalesce((SELECT jsonb_agg(jsonb_build_object('id',sec.id,'name',sec.name,'slug',sec.slug) ORDER BY sec.slug) FROM social_activity_secondary_types st JOIN taxonomy_activitytype sec ON sec.id=st.activitytype_id WHERE st.activity_id=a.id),'[]'::jsonb)) FROM social_activity a JOIN accounts_user u ON u.id=a.owner_id JOIN taxonomy_activitytype t ON t.id=a.activity_type_id LEFT JOIN places_place p ON p.id=a.place_id WHERE `+social.ActivityVisibilitySQL()+` AND a.id=ANY($3)`, a.ID, a.Cohort, ids)
	if err != nil {
		return nil, err
	}
	hydrated := map[int64]map[string]any{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		var item map[string]any
		if err := json.Unmarshal(raw, &item); err != nil {
			rows.Close()
			return nil, err
		}
		hydrated[spaID(item)] = item
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, item := range items {
		model, ok := hydrated[spaID(item)]
		if !ok {
			continue
		}
		for _, key := range []string{"rec_reason", "reason", "match_pct", "distance", "distance_m", "visual"} {
			if v, ok := item[key]; ok {
				model[key] = v
			}
		}
		out = append(out, model)
	}
	if len(out) > 0 && s.Social.ActivityVisuals != nil {
		ids := make([]int64, len(out))
		for i, item := range out {
			ids[i] = spaID(item)
		}
		visuals, err := s.Social.ActivityVisuals(ctx, s.DB, a, ids)
		if err != nil {
			return nil, err
		}
		for _, item := range out {
			if visual, ok := visuals[spaID(item)]; ok {
				item["visual"] = visual
			} else {
				return nil, platform.ErrForbidden
			}
		}
	}
	return out, nil
}
func (s *Server) spaActivityCards(ctx context.Context, r *http.Request, a platform.Actor, value any) ([]map[string]any, error) {
	items, err := s.HydrateActivities(ctx, a, value)
	if err != nil {
		return nil, err
	}
	return s.spaCardsFromModels(r, items)
}

// Pure presentation projection over models already authorized by hydration.
func (s *Server) spaCardsFromModels(r *http.Request, items []map[string]any) ([]map[string]any, error) {
	out := []map[string]any{}
	tr := func(text string) string { return s.Renderer.catalog.translate(language(r), text, 1) }
	for _, item := range items {
		typ := spaMap(spaFallback(item["activity_type_obj"], item["activity_type"]))
		place := spaMap(spaFallback(item["place_obj"], item["place"]))
		tags := []string{spaText(typ["name"])}
		for _, sec := range spaRows(item["secondary_types"]) {
			tags = append(tags, spaText(sec["name"]))
		}
		labels := map[string]string{"free": "Free", "low": "Low cost", "paid": "Paid", "easy": "Easy", "moderate": "Moderate", "challenging": "Challenging", "cancelled": "Cancelled", "done": "Done"}
		for _, key := range []string{"cost_band", "difficulty"} {
			v := spaText(item[key])
			if v != "" && v != "unspecified" {
				tags = append(tags, tr(labels[v]))
			}
		}
		if spaBool(item["beginners_welcome"]) {
			tags = append(tags, tr("beginners welcome"))
		}
		status := spaText(item["status"])
		if status != "" && status != "open" {
			tags = append(tags, tr(labels[status]))
		}
		if spaBool(item["guardian_accompanied"]) {
			tags = append(tags, tr("guardian-accompanied"))
		}
		meta := s.spaDate(r, item["starts_at"], "D j M, H:i") + " · " + spaText(spaFallback(place["display_name"], spaFallback(place["name"], tr("a place"))))
		if city := spaText(place["address_city"]); city != "" {
			meta += ", " + city
		}
		distance := item["distance_m"]
		if distance == nil {
			distance = item["distance"]
		}
		if metres, ok := distance.(float64); ok {
			meta += " · " + strings.ReplaceAll(tr("%(km)s km away"), "%(km)s", fmt.Sprintf("%.1f", metres/1000))
		}
		var score any
		if reason := spaText(spaFallback(item["rec_reason"], item["reason"])); reason != "" {
			score = reason
		} else if spaInt(item["match_pct"]) != 0 {
			score = strings.ReplaceAll(tr("%(pct)s%% match"), "%(pct)s", spaText(item["match_pct"]))
			score = strings.ReplaceAll(score.(string), "%%", "%")
		}
		visual := spaMap(item["visual"])
		vis := map[string]any{"kind": "accent", "svg": activitySVG(spaText(typ["slug"]) + ":" + spaText(item["title"])).String()}
		if visual["kind"] == "activity_cover_photo" {
			vis = map[string]any{"kind": "photo", "url": visual["url"], "alt": spaFallback(visual["alt"], item["title"])}
		}
		out = append(out, map[string]any{"pk": spaID(item), "url": routeURL("activity_detail", spaID(item)), "title": item["title"], "visual": vis, "tags": tags, "meta": meta, "description": spaTruncate(spaText(item["description"]), 22), "score": score})
	}
	return out, nil
}
func (s *Server) spaEventRows(ctx context.Context, r *http.Request, value any) ([]map[string]any, error) {
	items := spaRows(value)
	if s.DB != nil && len(items) > 0 {
		ids := []int64{}
		for _, item := range items {
			ids = append(ids, spaID(item))
		}
		rows, err := s.DB.Query(ctx, `SELECT `+catalog.PublicEventProjectionSQL()+` || jsonb_build_object('activity_type_obj',jsonb_build_object('name',t.name),'place_obj',CASE WHEN p.id IS NULL THEN NULL ELSE jsonb_build_object('id',p.id,'name',p.name,'display_name',`+catalog.PlaceDisplayNameSQL()+`) END) FROM events_event e LEFT JOIN places_place p ON p.id=e.place_id LEFT JOIN taxonomy_activitytype t ON t.id=e.activity_type_id WHERE `+catalog.PublicEventsSQL()+` AND e.id=ANY($1)`, ids)
		if err != nil {
			return nil, err
		}
		models := map[int64]map[string]any{}
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return nil, err
			}
			var item map[string]any
			if err := json.Unmarshal(raw, &item); err != nil {
				rows.Close()
				return nil, err
			}
			models[spaID(item)] = item
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		filtered := []map[string]any{}
		for _, item := range items {
			if model, ok := models[spaID(item)]; ok {
				filtered = append(filtered, model)
			}
		}
		items = filtered
	}
	out := []map[string]any{}
	for _, e := range items {
		typ := spaMap(spaFallback(e["activity_type_obj"], e["activity_type"]))
		p := spaMap(spaFallback(e["place_obj"], e["place"]))
		var place any
		if spaID(p) > 0 {
			place = map[string]any{"name": p["name"], "url": s.spaPlacePath(p)}
		}
		out = append(out, map[string]any{"pk": spaID(e), "url": fmt.Sprintf("/events/%d/%s/", spaID(e), snapshot.Slug(spaText(e["title"]), "event")), "title": e["title"], "type": spaText(typ["name"]), "when": s.spaDate(r, e["starts_at"], "D j M, H:i"), "place": place, "description": spaTruncate(spaText(e["description"]), 22)})
	}
	return out, nil
}
func (s *Server) spaPlaceRows(ctx context.Context, r *http.Request, value any) ([]map[string]any, error) {
	out := []map[string]any{}
	items := spaRows(value)
	if s.DB != nil && s.Catalog != nil && s.Catalog.PlaceVisuals != nil && len(items) > 0 {
		ids := []int64{}
		for _, item := range items {
			ids = append(ids, spaID(item))
		}
		visuals, err := s.Catalog.PlaceVisuals(ctx, s.DB, ids)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			place := spaPlace(item)
			if visual, ok := visuals[spaID(item)]; ok {
				place["visual"] = visual
			}
		}
	}
	for _, item := range items {
		p := spaPlace(item)
		distance := ""
		if m, ok := p["distance_m"].(float64); ok {
			distance = fmt.Sprintf("%.0f m away", math.RoundToEven(m))
		}
		chips := []string{}
		for _, chip := range spaRows(p["category_chips"]) {
			chips = append(chips, spaText(chip["name"]))
		}
		if len(chips) == 0 {
			raw, _ := json.Marshal(p["category_labels"])
			_ = json.Unmarshal(raw, &chips)
			if chips == nil {
				chips = []string{}
			}
		}
		visual := spaMap(p["visual"])
		var vis any
		switch visual["kind"] {
		case "place_cover_photo":
			vis = map[string]any{"kind": "photo", "url": spaText(visual["url"]), "alt": spaFallback(visual["alt"], p["name"]), "attribution": spaText(visual["attribution"]), "licenseName": spaText(visual["license_name"]), "sourcePageUrl": spaText(visual["source_page_url"])}
		case "accent":
			vis = map[string]any{"kind": "accent", "svg": spaText(visual["svg"])}
		}
		name := spaText(spaFallback(p["display_name"], p["name"]))
		if name == "" {
			name = s.Renderer.catalog.translate(language(r), "Unnamed place", 1)
		}
		out = append(out, map[string]any{"pk": spaID(p), "url": routeURL("place_detail", spaID(p)), "name": name, "street": spaText(p["address_street"]), "city": spaText(p["address_city"]), "distance": distance, "categoryChips": chips, "visual": vis, "accessMatch": spaBool(p["access_match"])})
	}
	return out, nil
}
func (s *Server) spaOrganizer(ctx context.Context, r *http.Request, a platform.Actor, value any) ([]map[string]any, error) {
	rows := spaRows(value)
	activities := []map[string]any{}
	for _, row := range rows {
		activity := spaMap(row["activity"])
		if len(activity) == 0 {
			activity = row
		}
		activities = append(activities, activity)
	}
	models, err := s.HydrateActivities(ctx, a, activities)
	if err != nil {
		return nil, err
	}
	byID := map[int64]map[string]any{}
	for _, item := range models {
		byID[spaID(item)] = item
	}
	out := []map[string]any{}
	tr := func(text string) string { return s.Renderer.catalog.translate(language(r), text, 1) }
	for _, row := range rows {
		item := spaMap(row["activity"])
		if len(item) == 0 {
			item = row
		}
		model, ok := byID[spaID(item)]
		if !ok {
			continue
		}
		detail, edit := routeURL("activity_detail", spaID(model)), routeURL("activity_edit", spaID(model))
		badges := []map[string]any{}
		add := func(label string, url any, tone string) {
			badges = append(badges, map[string]any{"label": label, "url": url, "tone": tone})
		}
		if n := spaInt(row["pending_joins"]); n > 0 {
			add(strings.ReplaceAll(tr("%(n)s waiting to join"), "%(n)s", strconv.Itoa(n)), detail, "info")
		}
		if spaBool(row["needs_supervisor"]) {
			add(tr("Needs a supervising guardian"), detail, "danger")
		}
		if n := spaInt(spaMap(row["quorum"])["remaining_needed"]); n > 0 {
			add(strings.ReplaceAll(tr("Needs %(n)s more to go"), "%(n)s", strconv.Itoa(n)), detail, "info")
		}
		if spaBool(row["missing_meeting_point"]) {
			add(tr("Add a meeting point"), edit, "info")
		}
		readiness := spaMap(row["readiness"])
		if spaBool(readiness["missing_what_to_bring"]) {
			add(tr("Add what to bring"), edit, "info")
		}
		if spaBool(readiness["near_capacity"]) {
			add(tr("Full"), nil, "info")
		}
		place := spaMap(model["place_obj"])
		if spaBool(row["venue_flag"]) && spaID(place) > 0 {
			add(tr("Check this venue's hours"), routeURL("place_detail", spaID(place)), "danger")
		}
		support := ""
		if n := spaInt(row["support_companions"]); n > 0 {
			label := "%(n)s members are bringing a support person."
			if n == 1 {
				label = "%(n)s member is bringing a support person."
			}
			support = strings.ReplaceAll(tr(label), "%(n)s", strconv.Itoa(n))
		}
		out = append(out, map[string]any{"pk": spaID(model), "url": detail, "title": model["title"], "type": spaMap(model["activity_type_obj"])["name"], "when": s.spaDate(r, model["starts_at"], "D d M, H:i"), "place": spaFallback(place["display_name"], place["name"]), "badges": badges, "allClear": len(badges) == 0, "supportNote": support})
	}
	return out, nil
}
func (s *Server) spaProfile(ctx context.Context, r *http.Request, a platform.Actor, legacy map[string]any, data map[string]any, tabs []map[string]any) error {
	tr := func(text string) string { return s.Renderer.catalog.translate(language(r), text, 1) }
	name := a.DisplayName
	if name == "" {
		name = a.Username
	}
	data["name"] = name
	data["username"] = a.Username
	data["ageBand"] = tr(map[string]string{"unknown": "Unknown", "under_16": "Under 16", "16_17": "16-17", "adult": "Adult (18+)"}[a.AgeBand])
	data["identityVerified"] = a.IdentityVerified
	data["canParticipate"] = spaBool(legacy["can_participate"])
	data["avatarUrl"] = spaText(legacy["avatar_url"])
	data["journeyAvatar"] = spaText(legacy["journey_avatar"])
	styles := []map[string]any{}
	for _, style := range spaRows(legacy["avatar_styles"]) {
		styles = append(styles, map[string]any{"generation": style["generation"], "name": style["name"], "previewUri": style["uri"], "current": spaBool(style["current"])})
	}
	data["avatarStyles"] = styles
	var progression any
	if p := spaMap(legacy["progression"]); len(p) > 0 {
		progression = map[string]any{"count": spaInt(p["count"]), "level": spaInt(p["level"]), "maxLevel": spaInt(p["max_level"])}
	}
	data["progression"] = progression
	var provenance any
	if p := spaMap(legacy["provenance"]); spaBool(p["has_row"]) {
		provenance = map[string]any{"isCurrent": spaBool(p["is_current"]), "bandDisplay": spaText(p["band_display"]), "provider": spaText(p["provider"]), "method": spaText(p["method"]), "verifiedAt": s.spaDate(r, p["verified_at"], "d M Y"), "expiresAt": s.spaDate(r, p["expires_at"], "d M Y"), "status": spaText(p["status"]), "expiresSoon": spaBool(p["expires_soon"]), "daysLeft": p["days_left"]}
	}
	data["provenance"] = provenance
	interests := legacy["interests"]
	if interests == nil {
		interests = []any{}
	}
	data["interests"] = interests
	connections := spaRows(legacy["connections"])
	total := len(connections)
	shown := connections[:min(8, total)]
	data["connections"] = spaUsers(shown)
	data["connectionsTotal"] = total
	pending := len(spaRows(legacy["pending_in"]))
	data["pendingIncomingCount"] = pending
	blocked := []map[string]any{}
	for _, b := range spaRows(legacy["blocked"]) {
		blocked = append(blocked, map[string]any{"pk": spaID(b), "name": spaName(b)})
	}
	data["blocked"] = blocked
	data["tabs"] = tabs
	ui := spaMap(data["ui"])
	msg := "%(counter)s pending connection requests"
	if pending == 1 {
		msg = "%(counter)s pending connection request"
	}
	ui["pendingRequests"] = strings.ReplaceAll(tr(msg), "%(counter)s", strconv.Itoa(pending))
	ui["seeAllConnections"] = strings.ReplaceAll(tr("See all %(total)s connections →"), "%(total)s", strconv.Itoa(total))
	return nil
}
func (s *Server) spaSavedSearchRows(ctx context.Context, r *http.Request, actor platform.Actor, value any) ([]map[string]any, error) {
	out := []map[string]any{}
	items := spaRows(value)
	if s.DB != nil && len(items) > 0 {
		ids := []int64{}
		for _, item := range items {
			ids = append(ids, spaID(item))
		}
		rows, err := s.DB.Query(ctx, `SELECT jsonb_build_object('id',ss.id,'activity_type',CASE WHEN t.id IS NULL THEN NULL ELSE jsonb_build_object('name',t.name) END,'category',CASE WHEN c.id IS NULL THEN NULL ELSE jsonb_build_object('name',c.name) END,'area',CASE WHEN ar.id IS NULL THEN NULL ELSE jsonb_build_object('name',ar.name) END,'cost_band',ss.cost_band,'coarse_window',ss.coarse_window,'beginners',ss.beginners) FROM saved_searches_savedsearch ss LEFT JOIN taxonomy_activitytype t ON t.id=ss.activity_type_id LEFT JOIN taxonomy_activitycategory c ON c.id=ss.category_id LEFT JOIN communities_area ar ON ar.id=ss.area_id WHERE ss.user_id=$1 AND ss.id=ANY($2) ORDER BY ss.created_at DESC,ss.id DESC`, actor.ID, ids)
		if err != nil {
			return nil, err
		}
		items = []map[string]any{}
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return nil, err
			}
			var item map[string]any
			if err := json.Unmarshal(raw, &item); err != nil {
				rows.Close()
				return nil, err
			}
			items = append(items, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	tr := func(text string) string { return s.Renderer.catalog.translate(language(r), text, 1) }
	for _, item := range items {
		typ, category, area := spaMap(item["activity_type"]), spaMap(item["category"]), spaMap(item["area"])
		what := spaText(typ["name"])
		if what == "" {
			what = spaText(category["name"])
		}
		extras := []string{}
		if name := spaText(area["name"]); name != "" {
			extras = append(extras, name)
		}
		labels := map[string]string{"free": "Free", "low": "Low cost", "paid": "Paid", "unspecified": "Not specified", "weekday_daytime": "Weekday daytime", "weekday_evening": "Weekday evening", "weekend_daytime": "Weekend daytime", "weekend_evening": "Weekend evening"}
		for _, key := range []string{"cost_band", "coarse_window"} {
			if v := spaText(item[key]); v != "" {
				extras = append(extras, tr(labels[v]))
			}
		}
		if spaBool(item["beginners"]) {
			extras = append(extras, tr("beginners welcome"))
		}
		out = append(out, map[string]any{"pk": spaID(item), "what": what, "extras": extras})
	}
	return out, nil
}
