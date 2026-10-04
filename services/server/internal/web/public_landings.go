package web

import (
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

func (s *Server) publicLandingCombos(r *http.Request) ([]map[string]any, error) {
	return socialRows(r.Context(), s.DB, `SELECT jsonb_build_object('area',jsonb_build_object('id',ar.id,'slug',ar.slug,'name',ar.name,'city',ar.city),'activity',jsonb_build_object('id',t.id,'slug',t.slug,'name',t.name)) FROM communities_area ar CROSS JOIN taxonomy_activitytype t WHERE ar.is_active AND t.is_active AND (EXISTS(SELECT 1 FROM places_place p JOIN places_placeactivity pa ON pa.place_id=p.id WHERE lower(p.address_city)=lower(ar.city) AND pa.activity_id=t.id AND NOT pa.is_disputed AND `+catalog.PolicyFromContext(r.Context()).PlaceSQL()+`) OR EXISTS(SELECT 1 FROM events_event e JOIN places_place p ON p.id=e.place_id WHERE e.activity_type_id=t.id AND lower(p.address_city)=lower(ar.city) AND `+catalog.PolicyFromContext(r.Context()).EventSQL()+` AND `+publicUpcomingSQL+`)) ORDER BY ar.slug,t.slug`)
}
func (s *Server) publicLanding(r *http.Request, a platform.Actor, name string) (pongo2.Context, string, error) {
	combos, err := s.publicLandingCombos(r)
	if err != nil {
		return nil, "", err
	}
	data := pongo2.Context{"user": socialActor(a)}
	grouped := []any{}
	areas := map[string]map[string]any{}
	types := map[string][]map[string]any{}
	for _, combo := range combos {
		area := spaMap(combo["area"])
		slug := spaText(area["slug"])
		areas[slug] = area
		types[slug] = append(types[slug], spaMap(combo["activity"]))
	}
	keys := []string{}
	for key := range areas {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return spaText(areas[keys[i]]["name"]) < spaText(areas[keys[j]]["name"]) })
	for _, key := range keys {
		grouped = append(grouped, []any{areas[key], types[key]})
	}
	if name == "things_to_do_index" {
		data["grouped"] = grouped
		return data, "web/landing_index.html", nil
	}
	area := areas[r.PathValue("area_slug")]
	if area == nil {
		return nil, "", platform.ErrNotFound
	}
	data["area"] = area
	if name == "things_to_do_city" {
		data["activities"] = types[spaText(area["slug"])]
		return data, "web/landing_city.html", nil
	}
	var typ map[string]any
	for _, t := range types[spaText(area["slug"])] {
		if t["slug"] == r.PathValue("activity_slug") {
			typ = t
			break
		}
	}
	if typ == nil {
		return nil, "", platform.ErrNotFound
	}
	data["activity_type"] = typ
	request := r.Clone(r.Context())
	copyURL := *r.URL
	request.URL = &copyURL
	q := url.Values{"activity": []string{spaText(typ["slug"])}}
	request.URL.RawQuery = q.Encode()
	places, _, err := s.publicPlaces(request, a, spaText(area["city"]), spaText(typ["slug"]), 10000)
	if err != nil {
		return nil, "", err
	}
	events, err := s.publicEvents(request, spaText(area["city"]), 0, 50)
	if err != nil {
		return nil, "", err
	}
	if len(places) == 0 && len(events) == 0 {
		return nil, "", platform.ErrNotFound
	}
	data["places"], data["events"] = places, events
	if len(events) > 0 {
		data["structured_data"] = publicLD(s.publicItemList(r, events, true))
	}
	data["breadcrumb_data"] = publicLD(s.publicBreadcrumb(r, []map[string]any{{"name": "Home", "url": "/"}, {"name": "Things to do", "url": routeURL("things_to_do_index")}, {"name": area["name"], "url": routeURL("things_to_do_city", area["slug"])}, {"name": typ["name"], "url": routeURL("things_to_do", area["slug"], typ["slug"])}}))
	return data, "web/landing_detail.html", nil
}
func publicLandingPath(area, typ map[string]any) string {
	return "/things-to-do/" + strings.TrimSpace(spaText(area["slug"])) + "/" + strings.TrimSpace(spaText(typ["slug"])) + "/"
}
