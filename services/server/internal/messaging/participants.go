package messaging

import (
	"net/http"
	"strconv"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func (s *Service) participantsHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	id, e := reqID(r)
	var body struct {
		Username string `json:"username"`
	}
	if e == nil {
		if r.ContentLength != 0 {
			e = decodeRequest(w, r, &body)
		}
		if body.Username == "" {
			body.Username = r.URL.Query().Get("username")
		}
	}
	if e == nil {
		if r.Method == "POST" {
			e = s.AddParticipant(r.Context(), a, id, body.Username)
		} else {
			e = s.RemoveParticipant(r.Context(), a, id, body.Username)
		}
	}
	if e != nil {
		fail(w, e, 400)
		return
	}
	if r.Method == "DELETE" {
		platform.JSON(w, 204, nil)
		return
	}
	data, e := s.Conversation(r.Context(), a, id)
	if e != nil {
		fail(w, e, 400)
		return
	}
	platform.JSON(w, 201, data)
}
func (s *Service) participantKeys(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	id, e := reqID(r)
	var data []any
	if e == nil {
		data, e = s.ParticipantKeys(r.Context(), a, id)
	}
	if e != nil {
		fail(w, e, 403)
		return
	}
	platform.JSON(w, 200, data)
}
func (s *Service) disappearingHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	id, e := reqID(r)
	var body struct {
		Seconds int `json:"seconds"`
	}
	if e == nil {
		e = decodeRequest(w, r, &body)
	}
	if e == nil {
		e = s.SetDisappearing(r.Context(), a, id, body.Seconds)
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
func (s *Service) guardianList(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	s.listConversations(w, r, a, true)
}
func (s *Service) guardianHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	id, e := reqID(r)
	if e == nil {
		e = s.AddGuardian(r.Context(), a, id)
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
}
func (s *Service) reportHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	id, e := reqID(r)
	message, parseErr := strconv.ParseInt(r.PathValue("message_id"), 10, 64)
	if e != nil || parseErr != nil || message <= 0 {
		fail(w, platform.ErrInvalid, 400)
		return
	}
	var body struct {
		Reason  string `json:"reason"`
		Detail  string `json:"detail"`
		Excerpt string `json:"decrypted_excerpt"`
	}
	e = decodeRequest(w, r, &body)
	if e == nil {
		_, e = s.Report(r.Context(), a, id, message, body.Reason, body.Detail, body.Excerpt)
	}
	if e != nil {
		fail(w, e, 400)
		return
	}
	platform.JSON(w, 201, map[string]any{"detail": "Report submitted."})
}
