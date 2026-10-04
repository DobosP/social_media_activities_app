package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	snapshot "github.com/DobosP/social_media_activities_app/services/server/internal/export"
	"github.com/flosch/pongo2/v6"
)

// encoding/json escapes HTML delimiters and U+2028/U+2029 before marking
// these deliberately public-only JSON-LD values safe inside a script element.
func publicLD(value any) *pongo2.Value {
	raw, err := json.Marshal(value)
	if err != nil {
		return pongo2.AsValue("")
	}
	return pongo2.AsSafeValue(string(raw))
}
func (s *Server) publicAbsolute(r *http.Request, path string) string {
	base := strings.TrimRight(s.Config.PublicURL, "/")
	if base == "" && r != nil {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}
func publicEventPath(e map[string]any) string {
	return fmt.Sprintf("/events/%d/%s/", spaID(e), snapshot.Slug(spaText(e["title"]), "event"))
}
func (s *Server) publicPlaceNode(r *http.Request, p map[string]any) map[string]any {
	node := map[string]any{"@type": "Place", "name": spaFallback(p["_display_name"], p["display_name"]), "url": s.publicAbsolute(r, publicPlacePath(p))}
	address := map[string]any{"@type": "PostalAddress"}
	for key, value := range map[string]string{"streetAddress": strings.TrimSpace(spaText(p["address_street"]) + " " + spaText(p["address_housenumber"])), "addressLocality": spaText(spaFallback(p["address_city"], p["city"])), "postalCode": spaText(spaFallback(p["address_postcode"], p["postcode"])), "addressCountry": spaText(spaFallback(p["address_country"], p["country"]))} {
		if value != "" {
			address[key] = value
		}
	}
	if len(address) > 1 {
		node["address"] = address
	} else if spaText(p["display_address"]) != "" {
		node["address"] = p["display_address"]
	}
	if p["lat"] != nil && p["lon"] != nil {
		node["geo"] = map[string]any{"@type": "GeoCoordinates", "latitude": p["lat"], "longitude": p["lon"]}
	}
	if website := publicExternal(spaText(p["website"])); website != "" {
		node["sameAs"] = website
	}
	return node
}
func (s *Server) publicEventLD(r *http.Request, e map[string]any) map[string]any {
	status := map[string]string{"cancelled": "EventCancelled", "postponed": "EventPostponed", "rescheduled": "EventRescheduled", "moved_online": "EventMovedOnline"}[spaText(e["lifecycle_status"])]
	if status == "" {
		status = "EventScheduled"
	}
	node := map[string]any{"@context": "https://schema.org", "@type": "Event", "name": e["title"], "startDate": publicDate(e["starts_at"]), "url": s.publicAbsolute(r, publicEventPath(e)), "eventAttendanceMode": "https://schema.org/OfflineEventAttendanceMode", "eventStatus": "https://schema.org/" + status}
	if spaText(e["description"]) != "" {
		node["description"] = e["description"]
	}
	if e["ends_at"] != nil {
		node["endDate"] = publicDate(e["ends_at"])
	}
	if p := spaMap(e["place"]); len(p) > 0 {
		node["location"] = s.publicPlaceNode(r, p)
	}
	url := publicExternal(spaText(e["url"]))
	if url != "" {
		node["sameAs"] = url
	}
	if e["source_is_free"] != nil {
		node["isAccessibleForFree"] = e["source_is_free"]
	}
	offer := map[string]any{"@type": "Offer"}
	if spaBool(e["source_is_free"]) {
		offer["price"] = "0"
	} else if e["source_price_min"] != nil {
		offer["price"] = e["source_price_min"]
	}
	if offer["price"] != nil && spaText(e["source_currency"]) != "" {
		offer["priceCurrency"] = e["source_currency"]
	}
	if availability := map[string]string{"available": "InStock", "limited": "LimitedAvailability", "sold_out": "SoldOut"}[spaText(e["source_availability"])]; availability != "" {
		offer["availability"] = "https://schema.org/" + availability
	}
	if url != "" && (e["source_is_free"] != nil || e["source_price_min"] != nil || e["source_price_max"] != nil || spaText(e["source_availability"]) != "") {
		offer["url"] = url
	}
	if len(offer) > 1 {
		node["offers"] = offer
	}
	return node
}
func (s *Server) publicBreadcrumb(r *http.Request, crumbs []map[string]any) map[string]any {
	items := []any{}
	for i, c := range crumbs {
		item := map[string]any{"@type": "ListItem", "position": i + 1, "name": c["name"]}
		if spaText(c["url"]) != "" {
			item["item"] = s.publicAbsolute(r, spaText(c["url"]))
		}
		items = append(items, item)
	}
	return map[string]any{"@context": "https://schema.org", "@type": "BreadcrumbList", "itemListElement": items}
}
func (s *Server) publicItemList(r *http.Request, rows []map[string]any, events bool) map[string]any {
	items := []any{}
	for i, row := range rows {
		name, path := row["name"], publicPlacePath(row)
		if events {
			name, path = row["title"], publicEventPath(row)
		}
		items = append(items, map[string]any{"@type": "ListItem", "position": i + 1, "name": name, "url": s.publicAbsolute(r, path)})
	}
	return map[string]any{"@context": "https://schema.org", "@type": "ItemList", "itemListElement": items}
}
func (s *Server) publicOrganization(r *http.Request) map[string]any {
	org := map[string]any{"@type": []string{"Organization", "NGO"}, "name": "Activities", "url": s.publicAbsolute(r, "/"), "description": "A nonprofit, text-first platform that helps people meet in person for real group activities at real places — sport, outdoors, games, reading and more. First city: Cluj-Napoca, Romania."}
	if len(s.Config.SiteSameAs) > 0 {
		org["sameAs"] = s.Config.SiteSameAs
	}
	if s.Config.SiteAreaServed != "" {
		org["areaServed"] = s.Config.SiteAreaServed
	}
	if s.Config.SiteContactEmail != "" {
		org["email"] = s.Config.SiteContactEmail
	}
	return org
}
func (s *Server) publicDataset(r *http.Request, include bool) map[string]any {
	distributions := []any{}
	for _, item := range [][3]string{{"Events feed (RSS)", "application/rss+xml", routeURL("events_feed")}, {"Events feed (Atom)", "application/atom+xml", routeURL("events_feed_atom")}, {"Events JSON API", "application/json", "/api/v1/events/"}} {
		distributions = append(distributions, map[string]any{"@type": "DataDownload", "name": item[0], "encodingFormat": item[1], "contentUrl": s.publicAbsolute(r, item[2])})
	}
	if include {
		distributions = append(distributions, map[string]any{"@type": "DataDownload", "name": "Snapshot manifest", "encodingFormat": "application/json", "contentUrl": s.publicAbsolute(r, routeURL("open_data_snapshot", "manifest.json"))})
	}
	return map[string]any{"@context": "https://schema.org", "@type": "Dataset", "name": "Activities open dataset — Cluj-Napoca venues & events", "description": "Public venues (parks, libraries, sports venues) and events for Cluj-Napoca, Romania, plus public adult opt-in activity cards — open data, updated periodically from open sources (OSM, Overture, RO-EDU) and venue calendars.", "url": s.publicAbsolute(r, "/open-data/"), "isAccessibleForFree": true, "license": s.publicAbsolute(r, "/open-data/#licensing"), "creator": s.publicOrganization(r), "spatialCoverage": map[string]any{"@type": "Place", "name": "Cluj-Napoca, Romania"}, "distribution": distributions}
}

func publicPlacePath(p map[string]any) string {
	name := spaText(p["name"])
	if value, ok := p["_display_name"]; ok {
		name = spaText(value)
	}
	return fmt.Sprintf("/places/%d/%s/", spaID(p), snapshot.Slug(name, "place"))
}
