package social

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func pageLink(r *http.Request, limit, offset int) string {
	copy := *r.URL
	params := copy.Query()
	params.Set("limit", strconv.Itoa(limit))
	params.Set("offset", strconv.Itoa(offset))
	copy.RawQuery = params.Encode()
	if copy.Scheme == "" {
		copy.Scheme = "http"
		if r.TLS != nil {
			copy.Scheme = "https"
		}
	}
	if copy.Host == "" {
		copy.Host = r.Host
	}
	return copy.String()
}
func limitPage(r *http.Request, count int64, rows []json.RawMessage, limit, offset int) map[string]any {
	var next, previous any
	if int64(offset+limit) < count {
		next = pageLink(r, limit, offset+limit)
	}
	if offset > 0 {
		previous = pageLink(r, limit, max(0, offset-limit))
	}
	return map[string]any{"count": count, "next": next, "previous": previous, "results": rows}
}

func (s *Service) cursorRows(r *http.Request, maxLimit int, load func(context.Context, int, int) ([]json.RawMessage, error)) (any, error) {
	limit := platform.ParseLimit(r.URL.Query().Get("limit"), 50, maxLimit)
	if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
		return load(r.Context(), maxLimit, 0)
	}
	offset := min(s.Cursor.Decode(r.URL.Query().Get("cursor")), 100000)
	rows, err := load(r.Context(), limit+1, offset)
	if err != nil {
		return nil, err
	}
	next := ""
	if len(rows) > limit {
		if len(s.Cursor.Key) < 32 {
			return nil, platform.ErrForbidden
		}
		rows = rows[:limit]
		next = s.Cursor.Encode(offset + limit)
	}
	return map[string]any{"next_cursor": next, "limit": limit, "results": rows}, nil
}

func secure(fn func(http.ResponseWriter, *http.Request, Actor)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, ok := platform.RequireActor(w, r)
		if !ok {
			return
		}
		ctx, cancel := platform.Timeout(r)
		defer cancel()
		fn(w, r.WithContext(ctx), a)
	}
}
func bodyMap(w http.ResponseWriter, r *http.Request) (map[string]json.RawMessage, error) {
	var body map[string]json.RawMessage
	err := platform.Decode(w, r, &body)
	if err == nil && body == nil {
		err = platform.ErrInvalid
	}
	return body, err
}
func page(r *http.Request, maxLimit int) (int, int) {
	limit := 50
	offset := 0
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil {
		limit = max(1, min(n, maxLimit))
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && n >= 0 {
		offset = min(n, 100000)
	}
	return min(limit, maxLimit), offset
}
func registerRoute(mux *http.ServeMux, pattern string, handler http.HandlerFunc) {
	if strings.HasSuffix(pattern, "/") {
		pattern += "{$}"
	}
	mux.HandleFunc(pattern, handler)
}
func (s *Service) Register(mux *http.ServeMux) {
	for _, prefix := range []string{"/api/v1/", "/api/"} {
		base := prefix + "social/"
		registerRoute(mux, "GET "+base+"activities/", secure(s.activitiesList))
		registerRoute(mux, "POST "+base+"activities/", secure(s.activityCreate))
		registerRoute(mux, "GET "+base+"activities/{id}/", secure(s.activityDetail))
		registerRoute(mux, "PATCH "+base+"activities/{id}/", secure(s.activityPatch))
		registerRoute(mux, "GET "+base+"activities/mine/", secure(s.mine))
		for _, action := range []string{"cancel", "set_public_listing", "rsvp", "arrived", "transit", "departing", "join", "leave", "guardians", "grant_organizer", "revoke_organizer", "transfer", "supervision", "met_confirmed", "support_companion", "move"} {
			registerRoute(mux, "POST "+base+"activities/{id}/"+action+"/", secure(s.activityAction))
		}
		registerRoute(mux, "GET "+base+"activities/{id}/posts/", secure(s.postsList))
		registerRoute(mux, "POST "+base+"activities/{id}/posts/", secure(s.postsCreate))
		registerRoute(mux, "POST "+base+"activities/{id}/announce/", secure(s.postsCreate))
		registerRoute(mux, "GET "+base+"memberships/", secure(s.membershipsList))
		registerRoute(mux, "GET "+base+"memberships/{id}/", secure(s.membershipDetail))
		registerRoute(mux, "POST "+base+"memberships/{id}/vote/", secure(s.membershipDecision))
		registerRoute(mux, "POST "+base+"memberships/{id}/admit/", secure(s.membershipDecision))
		registerRoute(mux, "GET "+base+"groups/", secure(s.groupsList))
		registerRoute(mux, "POST "+base+"groups/", secure(s.groupCreate))
		registerRoute(mux, "GET "+base+"groups/{id}/", secure(s.groupDetail))
		for _, action := range []string{"join", "leave", "set_public_listing", "archive", "ask"} {
			registerRoute(mux, "POST "+base+"groups/{id}/"+action+"/", secure(s.groupAction))
		}
		registerRoute(mux, "GET "+base+"groups/{id}/roster/", secure(s.groupRoster))
		registerRoute(mux, "GET "+base+"groups/{id}/activities/", secure(s.groupActivities))
		registerRoute(mux, "GET "+base+"groups/{id}/posts/", secure(s.postsList))
		registerRoute(mux, "POST "+base+"groups/{id}/posts/", secure(s.postsCreate))
		registerRoute(mux, "POST "+base+"groups/{id}/announce/", secure(s.postsCreate))
		registerRoute(mux, "GET "+base+"place-proposals/", secure(s.proposalsList))
		registerRoute(mux, "POST "+base+"place-proposals/", secure(s.proposalCreate))
		registerRoute(mux, "POST "+base+"place-proposals/{id}/confirm/", secure(s.proposalConfirm))
		registerRoute(mux, "GET "+base+"gauges/", secure(s.gaugesList))
		registerRoute(mux, "POST "+base+"gauges/", secure(s.gaugeCreate))
		registerRoute(mux, "GET "+base+"gauges/{id}/", secure(s.gaugeDetail))
		for _, action := range []string{"interested", "uninterested", "convert"} {
			registerRoute(mux, "POST "+base+"gauges/{id}/"+action+"/", secure(s.gaugeAction))
		}
		registerRoute(mux, "GET "+base+"organizer-console/", secure(s.console))
		registerRoute(mux, "GET "+base+"series/", secure(s.seriesList))
		registerRoute(mux, "POST "+base+"series/", secure(s.seriesCreate))
		registerRoute(mux, "GET "+base+"series/{id}/", secure(s.seriesDetail))
		for _, action := range []string{"pause", "resume", "end", "next_note"} {
			registerRoute(mux, "POST "+base+"series/{id}/"+action+"/", secure(s.seriesAction))
		}
		registerRoute(mux, "PATCH "+base+"posts/{id}/", secure(s.postAction))
		registerRoute(mux, "DELETE "+base+"posts/{id}/", secure(s.postAction))
		for _, action := range []string{"reaction", "dissent", "concern"} {
			registerRoute(mux, "POST "+base+"posts/{id}/"+action+"/", secure(s.postAction))
		}
		cb := prefix + "connections/"
		registerRoute(mux, "GET "+cb+"connections/", secure(s.connectionsList))
		registerRoute(mux, "GET "+cb+"connections/pending/", secure(s.connectionsPending))
		registerRoute(mux, "GET "+cb+"connections/search/", secure(s.connectionsSearch))
		registerRoute(mux, "POST "+cb+"connections/request_to/", secure(s.connectionRequest))
		registerRoute(mux, "POST "+cb+"connections/remove/", secure(s.connectionRemove))
		for _, action := range []string{"accept", "decline", "withdraw"} {
			registerRoute(mux, "POST "+cb+"connections/{id}/"+action+"/", secure(s.connectionAction))
		}
		registerRoute(mux, "GET "+cb+"people/{public_id}/", secure(s.profile))
		registerRoute(mux, "GET "+prefix+"communities/communities/", secure(s.communitiesList))
		registerRoute(mux, "GET "+prefix+"communities/communities/graph/", secure(s.communityGraph))
		registerRoute(mux, "GET "+prefix+"communities/communities/{slug}/", secure(s.communityDetail))
		registerRoute(mux, "GET "+prefix+"communities/communities/{slug}/activities/", secure(s.communityActivities))
	}
}
func actionOf(r *http.Request) string {
	path := strings.TrimSuffix(r.URL.Path, "/")
	return path[strings.LastIndex(path, "/")+1:]
}
func (s *Service) activitiesList(w http.ResponseWriter, r *http.Request, a Actor) {
	limit, offset := page(r, 200)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) < 2 {
		q = ""
	}
	rows, err := s.listActivities(r.Context(), a, q, limit, offset)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	var count int64
	err = s.DB.QueryRow(r.Context(), matchingTypes+`SELECT COUNT(*) FROM social_activity a JOIN places_place p ON p.id=a.place_id JOIN taxonomy_activitytype t ON t.id=a.activity_type_id WHERE a.cohort=$2 AND NOT a.is_hidden AND `+blockOwner+` AND `+activitySearch, a.ID, a.Cohort, escapeLike(q)).Scan(&count)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	response(w, limitPage(r, count, rows, limit, offset), nil, 200)
}
func (s *Service) activityDetail(w http.ResponseWriter, r *http.Request, a Actor) {
	id, err := idFrom(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	v, err := s.Activity(r.Context(), a, id)
	response(w, v, err, 200)
}
func (s *Service) activityCreate(w http.ResponseWriter, r *http.Request, a Actor) {
	var in ActivityInput
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	var err error
	a, err = s.actingAs(r.Context(), a, in.OnBehalfOf)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	id, err := s.CreateActivity(r.Context(), a, in)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	v, err := s.Activity(r.Context(), a, id)
	response(w, v, err, 201)
}
func (s *Service) activityPatch(w http.ResponseWriter, r *http.Request, a Actor) {
	id, err := idFrom(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	body, err := bodyMap(w, r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	a, err = s.bodyActor(r.Context(), a, body)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	err = s.UpdateActivity(r.Context(), a, id, body)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	v, err := s.Activity(r.Context(), a, id)
	response(w, v, err, 200)
}
func listing(body map[string]json.RawMessage) (bool, error) {
	raw, ok := body["listed"]
	legacy, old := body["is_publicly_listed"]
	if ok && old || !ok && !old {
		return false, platform.ErrInvalid
	}
	if old {
		raw = legacy
	}
	return boolValue(raw)
}
func (s *Service) activityAction(w http.ResponseWriter, r *http.Request, a Actor) {
	id, err := idFrom(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	body := map[string]json.RawMessage{}
	if r.ContentLength != 0 {
		body, err = bodyMap(w, r)
		if err != nil {
			platform.Fail(w, err)
			return
		}
	}
	action := actionOf(r)
	if action == "join" || action == "leave" || action == "cancel" || action == "rsvp" || action == "guardians" {
		a, err = s.bodyActor(r.Context(), a, body)
		if err != nil {
			platform.Fail(w, err)
			return
		}
	}
	var mid int64
	switch action {
	case "join":
		mid, err = s.Join(r.Context(), a, id)
	case "leave":
		mid, err = s.Leave(r.Context(), a, id)
	case "cancel":
		reason := ""
		if raw, ok := body["reason"]; ok {
			reason, err = text(raw, 200, false)
		}
		if err == nil {
			err = s.CancelActivity(r.Context(), a, id, reason)
		}
	case "set_public_listing":
		var listed bool
		listed, err = listing(body)
		if err == nil {
			err = s.SetActivityListing(r.Context(), a, id, listed)
		}
	case "rsvp":
		var intent string
		intent, err = text(body["intent"], 16, true)
		if err == nil {
			err = s.RSVP(r.Context(), a, id, intent)
		}
		if err == nil {
			v, e := s.Attendance(r.Context(), a, id)
			response(w, v, e, 200)
			return
		}
	case "arrived", "transit", "departing":
		value := ""
		if action == "transit" {
			value, err = text(body["status"], 16, true)
		}
		if err == nil {
			mid, err = s.Presence(r.Context(), a, id, action, value)
		}
	case "guardians":
		var target int64
		target, err = intID(body["user_id"])
		if err == nil {
			mid, err = s.AddGuardian(r.Context(), a, id, target)
		}
	case "grant_organizer", "revoke_organizer", "transfer":
		var target int64
		target, err = intID(body["user_id"])
		if err == nil {
			mid, err = s.ChangeOrganizer(r.Context(), a, id, target, action)
		}
	case "supervision":
		var supervised bool
		supervised, err = boolValue(body["supervised"])
		if err == nil {
			err = s.SetSupervision(r.Context(), a, id, supervised)
		}
	case "met_confirmed":
		var value bool
		value = true
		if raw, ok := body["confirmed"]; ok {
			value, err = boolValue(raw)
		}
		if err == nil {
			err = s.MetConfirmed(r.Context(), a, id, value)
		}
	case "support_companion":
		var value bool
		value, err = boolValue(body["brings"])
		if err == nil {
			err = s.SupportCompanion(r.Context(), a, id, value)
		}
	case "move":
		var target int64
		target, err = intID(body["place"])
		if err == nil {
			err = s.MoveActivity(r.Context(), a, id, target)
		}
	default:
		err = platform.ErrNotFound
	}
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if mid > 0 && action != "transfer" {
		v, e := s.Membership(r.Context(), a, mid)
		status := 200
		if action == "join" || action == "guardians" {
			status = 201
		}
		response(w, v, e, status)
		return
	}
	v, e := s.Activity(r.Context(), a, id)
	response(w, v, e, 200)
}
func (s *Service) membershipsList(w http.ResponseWriter, r *http.Request, a Actor) {
	limit, offset := page(r, 200)
	rows, err := objects(r.Context(), s.DB, `SELECT `+membershipColumns+` FROM social_membership m JOIN accounts_user u ON u.id=m.user_id JOIN social_activity a ON a.id=m.activity_id WHERE a.cohort=$2 AND NOT a.is_hidden AND `+blockOwner+` ORDER BY m.id LIMIT $3 OFFSET $4`, a.ID, a.Cohort, limit, offset)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	var count int64
	if err := s.DB.QueryRow(r.Context(), `SELECT COUNT(*) FROM social_membership m JOIN social_activity a ON a.id=m.activity_id WHERE a.cohort=$2 AND NOT a.is_hidden AND `+blockOwner, a.ID, a.Cohort).Scan(&count); err != nil {
		platform.Fail(w, err)
		return
	}
	response(w, limitPage(r, count, rows, limit, offset), nil, 200)
}
func (s *Service) membershipDetail(w http.ResponseWriter, r *http.Request, a Actor) {
	id, err := idFrom(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	v, err := s.Membership(r.Context(), a, id)
	response(w, v, err, 200)
}
func (s *Service) membershipDecision(w http.ResponseWriter, r *http.Request, a Actor) {
	id, err := idFrom(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	approve, override := true, actionOf(r) == "admit"
	if !override {
		body, err := bodyMap(w, r)
		if err != nil {
			platform.Fail(w, err)
			return
		}
		approve, err = boolValue(body["approve"])
		if err != nil {
			platform.Fail(w, err)
			return
		}
	}
	if err := s.Vote(r.Context(), a, id, approve, override); err != nil {
		platform.Fail(w, err)
		return
	}
	v, err := s.Membership(r.Context(), a, id)
	response(w, v, err, 200)
}
func (s *Service) mine(w http.ResponseWriter, r *http.Request, a Actor) {
	var err error
	a, err = s.actingAs(r.Context(), a, r.URL.Query().Get("on_behalf_of"))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	value, err := s.cursorRows(r, 100, func(ctx context.Context, limit, offset int) ([]json.RawMessage, error) {
		return objects(ctx, s.DB, `SELECT `+membershipColumns+` FROM social_membership m JOIN accounts_user u ON u.id=m.user_id WHERE m.user_id=$1 AND m.state<>'removed' ORDER BY m.created_at DESC,m.id DESC LIMIT $2 OFFSET $3`, a.ID, limit, offset)
	})
	response(w, value, err, 200)
}
func postKind(r *http.Request) string {
	if strings.Contains(r.URL.Path, "/groups/") {
		return "group"
	}
	return "activity"
}
func (s *Service) postsList(w http.ResponseWriter, r *http.Request, a Actor) {
	id, err := idFrom(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	a, err = s.actingAs(r.Context(), a, r.URL.Query().Get("on_behalf_of"))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	limit, _ := page(r, 100)
	before, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	rows, cursor, err := s.Posts(r.Context(), a, postKind(r), id, before, limit)
	if strings.HasPrefix(r.URL.Path, "/api/v1/") {
		response(w, map[string]any{"next_cursor": cursor, "limit": limit, "results": rows}, err, 200)
	} else {
		response(w, rows, err, 200)
	}
}
func (s *Service) postsCreate(w http.ResponseWriter, r *http.Request, a Actor) {
	id, err := idFrom(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	var in PostInput
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	if in.OnBehalfOf != "" {
		ward, e := s.actingAs(r.Context(), a, in.OnBehalfOf)
		if e != nil {
			platform.Fail(w, e)
			return
		}
		if _, e = activity(r.Context(), s.DB, ward, id, false); e != nil {
			platform.Fail(w, e)
			return
		}
		if ward.ID != a.ID {
			platform.Fail(w, platform.ErrForbidden)
			return
		}
	} // A message remains the authenticated author's own utterance.
	pid, err := s.WritePost(r.Context(), a, postKind(r), id, in, actionOf(r) == "announce")
	if err != nil {
		platform.Fail(w, err)
		return
	}
	v, err := s.Post(r.Context(), a, pid)
	response(w, v, err, 201)
}
func (s *Service) postAction(w http.ResponseWriter, r *http.Request, a Actor) {
	id, err := idFrom(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if r.Method == "DELETE" {
		_, err := s.DeletePost(r.Context(), a, id)
		response(w, nil, err, 204)
		return
	}
	body, err := bodyMap(w, r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if r.Method == "PATCH" {
		bodyValue, err := text(body["body"], 4000, true)
		if err == nil {
			err = s.EditPost(r.Context(), a, id, bodyValue)
		}
		if err != nil {
			platform.Fail(w, err)
			return
		}
		v, err := s.Post(r.Context(), a, id)
		response(w, v, err, 200)
		return
	}
	kind := actionOf(r)
	facet := ""
	if kind == "reaction" {
		facet, err = text(body["emoji"], 32, true)
	}
	if err != nil {
		platform.Fail(w, err)
		return
	}
	added, err := s.ToggleSentiment(r.Context(), a, id, kind, facet)
	response(w, map[string]bool{"added": added}, err, 200)
}
func (s *Service) groupsList(w http.ResponseWriter, r *http.Request, a Actor) {
	value, err := s.cursorRows(r, 200, func(ctx context.Context, limit, offset int) ([]json.RawMessage, error) {
		return s.listGroups(ctx, a, limit, offset)
	})
	response(w, value, err, 200)
}
func (s *Service) groupDetail(w http.ResponseWriter, r *http.Request, a Actor) {
	id, err := idFrom(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	v, err := s.Group(r.Context(), a, id)
	response(w, v, err, 200)
}
func (s *Service) groupCreate(w http.ResponseWriter, r *http.Request, a Actor) {
	var in GroupInput
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	id, err := s.CreateGroup(r.Context(), a, in)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	v, err := s.Group(r.Context(), a, id)
	response(w, v, err, 201)
}
func (s *Service) groupAction(w http.ResponseWriter, r *http.Request, a Actor) {
	id, err := idFrom(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	switch actionOf(r) {
	case "join":
		err = s.JoinGroup(r.Context(), a, id)
	case "leave":
		err = s.LeaveGroup(r.Context(), a, id)
		if err == nil {
			platform.JSON(w, 200, map[string]bool{"left": true})
			return
		}
	case "archive":
		err = s.ArchiveGroup(r.Context(), a, id)
	case "set_public_listing":
		var body map[string]json.RawMessage
		body, err = bodyMap(w, r)
		if err == nil {
			var listed bool
			listed, err = listing(body)
			if err == nil {
				err = s.SetGroupListing(r.Context(), a, id, listed)
			}
		}
	case "ask":
		body, e := bodyMap(w, r)
		if e != nil {
			platform.Fail(w, e)
			return
		}
		prompt, e := text(body["prompt"], 40, true)
		if e != nil {
			platform.Fail(w, e)
			return
		}
		sent, e := s.AskGroup(r.Context(), a, id, prompt)
		response(w, map[string]bool{"sent": sent}, e, 200)
		return
	}
	if err != nil {
		platform.Fail(w, err)
		return
	}
	v, err := s.Group(r.Context(), a, id)
	response(w, v, err, 200)
}
func (s *Service) groupRoster(w http.ResponseWriter, r *http.Request, a Actor) {
	id, err := idFrom(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	members, err := s.GroupRoster(r.Context(), a, id)
	response(w, map[string]any{"members": members}, err, 200)
}
func (s *Service) groupActivities(w http.ResponseWriter, r *http.Request, a Actor) {
	id, err := idFrom(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	v, err := group(r.Context(), s.DB, a, id, false, false)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	rows, err := s.coordinateActivities(r.Context(), a, v.City, v.TypeID, v.CategoryID)
	response(w, rows, err, 200)
}
func (s *Service) seriesList(w http.ResponseWriter, r *http.Request, a Actor) {
	value, err := s.cursorRows(r, 200, func(ctx context.Context, limit, offset int) ([]json.RawMessage, error) {
		return s.listSeries(ctx, a, limit, offset)
	})
	response(w, value, err, 200)
}
func (s *Service) seriesDetail(w http.ResponseWriter, r *http.Request, a Actor) {
	id, err := idFrom(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	v, err := s.Series(r.Context(), a, id)
	response(w, v, err, 200)
}
func (s *Service) seriesCreate(w http.ResponseWriter, r *http.Request, a Actor) {
	var in SeriesInput
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	id, err := s.CreateSeries(r.Context(), a, in)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	v, err := s.Series(r.Context(), a, id)
	response(w, v, err, 201)
}
func (s *Service) seriesAction(w http.ResponseWriter, r *http.Request, a Actor) {
	id, err := idFrom(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if actionOf(r) == "next_note" {
		body, e := bodyMap(w, r)
		if e != nil {
			platform.Fail(w, e)
			return
		}
		note, e := text(body["note"], 500, false)
		if e == nil {
			err = s.SetSeriesNote(r.Context(), a, id, note)
		} else {
			err = e
		}
	} else {
		err = s.TransitionSeries(r.Context(), a, id, actionOf(r))
	}
	if err != nil {
		platform.Fail(w, err)
		return
	}
	v, err := s.Series(r.Context(), a, id)
	response(w, v, err, 200)
}
func (s *Service) connectionsList(w http.ResponseWriter, r *http.Request, a Actor) {
	v, err := s.Connections(r.Context(), a)
	response(w, v, err, 200)
}
func (s *Service) connectionsPending(w http.ResponseWriter, r *http.Request, a Actor) {
	v, err := s.PendingConnections(r.Context(), a)
	response(w, v, err, 200)
}
func (s *Service) connectionsSearch(w http.ResponseWriter, r *http.Request, a Actor) {
	v, err := s.SearchConnections(r.Context(), a, r.URL.Query().Get("q"))
	response(w, v, err, 200)
}
func publicID(body map[string]json.RawMessage) (string, error) {
	return text(body["public_id"], 36, true)
}
func (s *Service) connectionRequest(w http.ResponseWriter, r *http.Request, a Actor) {
	body, err := bodyMap(w, r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	pid, err := publicID(body)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	id, err := s.RequestConnection(r.Context(), a, pid)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	v, err := s.Connection(r.Context(), a, id)
	response(w, v, err, 201)
}
func (s *Service) connectionAction(w http.ResponseWriter, r *http.Request, a Actor) {
	id, err := idFrom(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if err := s.RespondConnection(r.Context(), a, id, actionOf(r)); err != nil {
		platform.Fail(w, err)
		return
	}
	v, err := s.Connection(r.Context(), a, id)
	response(w, v, err, 200)
}
func (s *Service) connectionRemove(w http.ResponseWriter, r *http.Request, a Actor) {
	body, err := bodyMap(w, r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	pid, err := publicID(body)
	if err == nil {
		err = s.RemoveConnection(r.Context(), a, pid)
	}
	response(w, nil, err, 204)
}
func (s *Service) profile(w http.ResponseWriter, r *http.Request, a Actor) {
	v, err := s.Profile(r.Context(), a, r.PathValue("public_id"))
	response(w, v, err, 200)
}
