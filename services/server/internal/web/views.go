package web

import (
	"encoding/json"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
	"net/http"
	"strconv"
)

func (s *Server) get(r *http.Request, path string) (any, error) {
	value, _, err := s.call(r, "GET", path, nil)
	return value, err
}
func (s *Server) view(r *http.Request, a platform.Actor, name string) (pongo2.Context, string, error) {
	if data, template, handled, err := s.PublicView(r, a, name); handled {
		return data, template, err
	}
	if data, template, handled, err := s.AccountView(r, a, name); handled {
		return data, template, err
	}
	if data, template, handled, err := s.SocialView(r, a, name); handled {
		return data, template, err
	}
	data := pongo2.Context{}
	template := "web/" + name + ".html"
	switch name {
	case "messages":
		return s.messagingView(r, a)
	case "donate", "transparency", "campaigns", "partners", "my_donations":
		return s.paymentView(r, a, name)
	case "privacy", "terms", "display_preferences", "account_delete", "open_data":
		return data, template, nil
	case "home":
		if a.ID == 0 {
			return data, "web/landing.html", nil
		}
		return s.home(r, a)
	case "interests":
		data, err := s.interestPage(r, a)
		return data, "web/interests.html", err
	case "topic_preferences":
		data, err := s.topicPage(r, a)
		return data, "web/topic_preferences.html", err
	case "groups":
		httpRedirect := pongo2.Context{"redirect": "/communities/"}
		return httpRedirect, "web/communities.html", nil
	case "activity_list":
		value, err := s.get(r, "/api/social/activities/?"+r.URL.RawQuery)
		data["activities"] = results(value)
		data["query"] = r.URL.Query().Get("q")
		return data, "web/activities.html", err
	case "organize":
		value, err := s.Social.OrganizerConsole(r.Context(), a)
		for key, item := range value {
			data[key] = item
		}
		return data, template, err
	case "series_list":
		value, err := s.get(r, "/api/social/series/")
		data["series"] = results(value)
		return data, template, err
	case "series_detail":
		value, err := s.get(r, "/api/social/series/"+r.PathValue("pk")+"/")
		data["series"] = object(value)
		return data, template, err
	case "communities":
		value, err := s.get(r, "/api/communities/communities/")
		if err != nil {
			return nil, "", err
		}
		groups, err := s.get(r, "/api/social/groups/")
		data["page"] = results(value)
		data["groups_page"] = results(groups)
		data["can_create"] = s.Social.AllowsGroupCreation(a)
		return data, template, err
	case "community_graph":
		value, err := s.Social.CommunityGraph(r.Context(), a)
		data["graph"] = value
		return data, "web/communities_graph.html", err
	case "community_detail":
		value, err := s.get(r, "/api/communities/communities/"+r.PathValue("slug")+"/")
		if err != nil {
			return nil, "", err
		}
		activities, err := s.get(r, "/api/communities/communities/"+r.PathValue("slug")+"/activities/")
		data["community"] = object(value)
		data["activities"] = results(activities)
		return data, template, err
	case "places_list", "places_map":
		value, err := s.get(r, "/api/places/?"+r.URL.RawQuery)
		data["places"] = results(value)
		data["features"] = value
		data["q"] = r.URL.Query().Get("q")
		return data, map[bool]string{true: "web/places.html", false: "web/places_list.html"}[name == "places_map"], err
	case "place_detail", "place_detail_slug":
		value, err := s.get(r, "/api/places/"+r.PathValue("pk")+"/")
		data["place"] = object(value)
		data["place_label"] = object(value)["name"]
		return data, "web/place_detail.html", err
	case "events_list":
		value, err := s.get(r, "/api/events/?"+r.URL.RawQuery)
		data["events"] = results(value)
		data["q"] = r.URL.Query().Get("q")
		return data, "web/events.html", err
	case "event_detail", "event_detail_slug":
		value, err := s.get(r, "/api/events/"+r.PathValue("pk")+"/")
		data["event"] = object(value)
		return data, "web/event_detail.html", err
	case "profile", "you", "settings", "my_privacy":
		value, err := s.Accounts.Self(r.Context(), s.DB, a)
		if err != nil {
			return nil, "", err
		}
		data["profile"] = value
		for key, item := range value {
			data[key] = item
		}
		if name == "you" {
			template = "web/you.html"
		}
		return data, template, nil
	case "notifications":
		data, err := s.notificationPage(r)
		return data, "web/notifications.html", err
	case "access_preferences", "notification_preferences":
		value, err := s.get(r, "/api/accounts/me/settings/")
		data["pref"] = object(value)["access"]
		data["muted_kinds"] = object(value)["muted_kinds"]
		return data, template, err
	case "connections":
		value, err := s.Social.Connections(r.Context(), a)
		if err != nil {
			return nil, "", err
		}
		pending, err := s.Social.PendingConnections(r.Context(), a)
		data["connections"] = value
		data["incoming"] = pending["incoming"]
		data["outgoing"] = pending["outgoing"]
		return data, template, err
	case "person", "person_card":
		value, err := s.Social.Profile(r.Context(), a, r.PathValue("public_id"))
		data["person"] = value
		if err == nil {
			err = s.populatePersonContext(r, value, data, name == "person")
		}
		if name == "person_card" {
			template = "web/_person_card.html"
		}
		return data, template, err
	case "activity_log":
		value, err := s.Safety.ActivityLog(r.Context(), a.ID)
		data["items"] = value
		return data, template, err
	case "safety_record":
		value, err := s.Accounts.SafetyRecord(r.Context(), a.ID)
		for key, item := range value {
			data[key] = item
		}
		return data, template, err
	case "wards":
		value, err := s.get(r, "/api/accounts/wards/")
		data["wards"] = results(value)
		return data, template, err
	case "my_guardians":
		value, err := s.get(r, "/api/accounts/guardian-links/")
		data["guardians"] = results(value)
		return data, "web/guardianship.html", err
	case "gauges":
		value, err := s.get(r, "/api/social/gauges/")
		data["items"] = results(value)
		return data, template, err
	case "gauge_detail":
		value, err := s.get(r, "/api/social/gauges/"+r.PathValue("pk")+"/")
		data["item"] = object(value)
		return data, template, err
	case "places_pending":
		value, err := s.get(r, "/api/social/place-proposals/")
		data["proposals"] = results(value)
		return data, template, err
	}
	return nil, "", platform.ErrNotFound
}
func (s *Server) home(r *http.Request, a platform.Actor) (pongo2.Context, string, error) {
	value, err := s.get(r, "/api/discovery/feed/?"+r.URL.RawQuery)
	if err != nil {
		return nil, "", err
	}
	data := pongo2.Context{}
	for key, item := range object(value) {
		data[key] = item
	}
	activities, err := s.get(r, "/api/social/activities/?limit=20")
	data["upcoming"] = results(activities)
	return data, "web/home.html", err
}
func rawObject(value json.RawMessage) map[string]any {
	var object map[string]any
	_ = json.Unmarshal(value, &object)
	return object
}
func stringID(value any) string {
	switch v := value.(type) {
	case int64:
		return strconv.FormatInt(v, 10)
	case json.Number:
		return string(v)
	case string:
		return v
	}
	return ""
}
