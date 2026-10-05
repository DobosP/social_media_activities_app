package messaging

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

func (s *Service) Register(mux *http.ServeMux) {
	for _, base := range []string{"/api/messaging/", "/api/v1/messaging/"} {
		mux.HandleFunc(exactRoute("GET "+base+"keys/"), s.ownKey)
		mux.HandleFunc(exactRoute("POST "+base+"keys/"), s.ownKey)
		mux.HandleFunc(exactRoute("GET "+base+"keys/{username}/"), s.userKey)
		mux.HandleFunc(exactRoute("POST "+base+"verify/"), s.verify)
		mux.HandleFunc(exactRoute("GET "+base+"conversations/"), s.conversations)
		mux.HandleFunc(exactRoute("POST "+base+"conversations/"), s.conversations)
		for _, action := range []string{"accept", "decline", "leave"} {
			mux.HandleFunc(exactRoute("POST "+base+"conversations/{id}/"+action+"/"), s.transitionHTTP)
		}
		mux.HandleFunc(exactRoute("POST "+base+"conversations/{id}/participants/"), s.participantsHTTP)
		mux.HandleFunc(exactRoute("DELETE "+base+"conversations/{id}/participants/"), s.participantsHTTP)
		mux.HandleFunc(exactRoute("POST "+base+"conversations/{id}/disappearing/"), s.disappearingHTTP)
		mux.HandleFunc(exactRoute("GET "+base+"conversations/{id}/keys/"), s.participantKeys)
		mux.HandleFunc(exactRoute("GET "+base+"guardian/conversations/"), s.guardianList)
		mux.HandleFunc(exactRoute("POST "+base+"conversations/{id}/guardian/"), s.guardianHTTP)
		mux.HandleFunc(exactRoute("GET "+base+"conversations/{id}/messages/"), s.messagesHTTP)
		mux.HandleFunc(exactRoute("POST "+base+"conversations/{id}/messages/"), s.messagesHTTP)
		mux.HandleFunc(exactRoute("POST "+base+"conversations/{id}/messages/{message_id}/report/"), s.reportHTTP)
	}
}
func decodeRequest(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(value) != nil || d.Decode(new(any)) != io.EOF {
		return platform.ErrInvalid
	}
	return nil
}
func fail(w http.ResponseWriter, e error, permission int) {
	if errors.Is(e, platform.ErrForbidden) {
		platform.Error(w, permission, "Messaging participation is not currently permitted.")
		return
	}
	platform.Fail(w, e)
}
func (s *Service) ownKey(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	status := 200
	var data map[string]any
	var e error
	if r.Method == "POST" {
		var body struct {
			PublicJWK map[string]any `json:"public_jwk"`
			Algorithm string         `json:"algorithm"`
			Backup    any            `json:"wrapped_private_jwk"`
		}
		if e = decodeRequest(w, r, &body); e == nil {
			data, e = s.RegisterKey(r.Context(), a, body.PublicJWK, body.Algorithm, body.Backup)
		}
		status = 201
	} else {
		data, e = s.OwnKey(r.Context(), a)
	}
	if errors.Is(e, pgx.ErrNoRows) {
		platform.Error(w, 404, "No key registered.")
		return
	}
	if e != nil {
		fail(w, e, 400)
		return
	}
	platform.JSON(w, status, data)
}
func (s *Service) userKey(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	data, e := s.ContactKey(r.Context(), a, r.PathValue("username"))
	if e != nil {
		fail(w, e, 404)
		return
	}
	platform.JSON(w, 200, data)
}
func (s *Service) verify(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	var body struct {
		Username    string `json:"username"`
		Fingerprint string `json:"fingerprint"`
	}
	e := decodeRequest(w, r, &body)
	var data map[string]any
	if e == nil {
		data, e = s.VerifyKey(r.Context(), a, body.Username, body.Fingerprint)
	}
	if e != nil {
		fail(w, e, 400)
		return
	}
	platform.JSON(w, 200, data)
}
func (s *Service) conversations(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	if r.Method == "POST" {
		var body struct {
			Kind      string          `json:"kind"`
			Username  string          `json:"username"`
			Usernames json.RawMessage `json:"usernames"`
			Title     string          `json:"title"`
		}
		e := decodeRequest(w, r, &body)
		var names []string
		if e == nil && len(body.Usernames) > 0 && string(body.Usernames) != "null" {
			if json.Unmarshal(body.Usernames, &names) != nil {
				var name string
				if json.Unmarshal(body.Usernames, &name) != nil {
					e = platform.ErrInvalid
				} else {
					names = []string{name}
				}
			}
		}
		if len(names) == 0 && body.Username != "" {
			names = []string{body.Username}
		}
		var id int64
		if e == nil {
			id, e = s.Start(r.Context(), a, body.Kind, names, body.Title)
			if errors.Is(e, pgx.ErrNoRows) {
				e = platform.ErrInvalid
			}
		}
		if e != nil {
			fail(w, e, 400)
			return
		}
		data, e := s.Conversation(r.Context(), a, id)
		if e != nil {
			fail(w, e, 400)
			return
		}
		platform.JSON(w, 201, data)
		return
	}
	s.listConversations(w, r, a, false)
}
func (s *Service) listConversations(w http.ResponseWriter, r *http.Request, a platform.Actor, guardian bool) {
	versioned := strings.HasPrefix(r.URL.Path, "/api/v1/")
	cap := s.ConversationLimit
	if cap < 1 || cap > 100 {
		cap = 100
	}
	limit, offset := cap, 0
	if versioned {
		limit = platform.ParseLimit(r.URL.Query().Get("limit"), min(50, cap), cap)
		offset = s.Cursor.Decode(r.URL.Query().Get("cursor"))
	}
	data, next, e := s.Conversations(r.Context(), a, r.URL.Query().Get("q"), limit, offset, guardian)
	if e != nil {
		fail(w, e, 403)
		return
	}
	if versioned {
		cursor := ""
		if next {
			cursor = s.Cursor.Encode(offset + limit)
		}
		platform.JSON(w, 200, map[string]any{"next_cursor": cursor, "limit": limit, "results": data})
	} else {
		platform.JSON(w, 200, data)
	}
}
func (s *Service) transitionHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	id, e := reqID(r)
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if e == nil {
		e = s.Transition(r.Context(), a, id, parts[len(parts)-1])
	}
	if e != nil {
		fail(w, e, 400)
		return
	}
	data, e := s.Conversation(r.Context(), a, id)
	if e != nil {
		fail(w, e, 400)
		return
	}
	platform.JSON(w, 200, data)
}
func (s *Service) messagesHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	id, e := reqID(r)
	if e != nil || id <= 0 {
		fail(w, platform.ErrInvalid, 400)
		return
	}
	if r.Method == "POST" {
		var body MessageInput
		e = decodeRequest(w, r, &body)
		var message int64
		if e == nil {
			message, e = s.Post(r.Context(), a, id, body)
		}
		if e != nil {
			fail(w, e, 400)
			return
		}
		data, e := s.Message(r.Context(), a, message, false)
		if e != nil {
			fail(w, e, 400)
			return
		}
		platform.JSON(w, 201, data)
		return
	}
	maxLimit := s.MessagePageLimit
	if maxLimit < 1 || maxLimit > 50 {
		maxLimit = 50
	}
	limit := platform.ParseLimit(r.URL.Query().Get("limit"), maxLimit, maxLimit)
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	versioned := strings.HasPrefix(r.URL.Path, "/api/v1/")
	before := int64(0)
	fetch := limit
	if versioned {
		before, _ = strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
		fetch++
	}
	data, e := s.Messages(r.Context(), a, id, fetch, after, before)
	if e != nil {
		fail(w, e, 403)
		return
	}
	if versioned {
		cursor := ""
		if len(data) > limit && before <= 0 && after > 0 {
			// Forward pages ascend: keep the oldest unseen messages. The client
			// continues with after=<last id>, so nothing pending is skipped.
			data = data[:limit]
		} else if len(data) > limit {
			data = data[1:]
			if len(data) > 0 {
				cursor = strconv.FormatInt(data[0].(map[string]any)["id"].(int64), 10)
			}
		}
		platform.JSON(w, 200, map[string]any{"next_cursor": cursor, "limit": limit, "results": data})
	} else {
		platform.JSON(w, 200, data)
	}
}

func exactRoute(pattern string) string { return strings.TrimSuffix(pattern, "/") + "/{$}" }
