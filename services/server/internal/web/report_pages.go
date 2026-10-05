package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
	"github.com/jackc/pgx/v5"
)

func reportSubject(r *http.Request) (string, int64, error) {
	value := func(key string) string {
		if query := r.URL.Query().Get(key); query != "" {
			return query
		}
		return r.PostForm.Get(key)
	}
	id, err := strconv.ParseInt(value("id"), 10, 64)
	if err != nil || id < 1 {
		return "", 0, platform.ErrNotFound
	}
	return value("type"), id, nil
}

func (s *Server) reportPage(r *http.Request, a platform.Actor) (pongo2.Context, error) {
	if s.Safety == nil {
		return nil, errors.New("report service unavailable")
	}
	model, id, err := reportSubject(r)
	if err != nil {
		return nil, err
	}
	target, err := s.Safety.ReportTarget(r.Context(), a, model, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, platform.ErrInvalid) {
			return nil, platform.ErrNotFound
		}
		return nil, err
	}
	// ReportTarget owns the label: never a username for a non-staff reporter.
	label := target.Label
	form, err := s.form(r, a, "ReportForm", nil)
	if err != nil {
		return nil, err
	}
	// ReportForm's exported field metadata contains its type but no Django
	// choice tuples. Supply the report API's fixed reason vocabulary here.
	var selectHTML strings.Builder
	selectHTML.WriteString(`<select id="id_reason" name="reason" required>`)
	for _, choice := range []option{{"grooming", "Grooming / predatory contact"}, {"harassment", "Harassment / bullying"}, {"csam", "Child sexual abuse material"}, {"spam", "Spam"}, {"off_platform", "Unsafe off-platform / meetup risk"}, {"other", "Other"}} {
		selected := ""
		if r.PostForm.Get("reason") == choice.Value {
			selected = " selected"
		}
		fmt.Fprintf(&selectHTML, `<option value="%s"%s>%s</option>`, escape(choice.Value), selected, escape(choice.Label))
	}
	selectHTML.WriteString(`</select>`)
	object(form["reason"])["widget"] = pongo2.AsSafeValue(selectHTML.String())
	var paragraphs strings.Builder
	for _, field := range form["fields"].([]any) {
		row := object(field)
		fmt.Fprintf(&paragraphs, "<p>%s%s</p>", row["label_tag"], row["widget"])
	}
	form["as_p"] = pongo2.AsSafeValue(paragraphs.String())
	data := pongo2.Context{"form": form, "target_type": model, "target_id": id, "target_label": label}
	if err := s.socialNav(r.Context(), a, data); err != nil {
		return nil, err
	}
	return data, nil
}

func (s *Server) reportAction(w http.ResponseWriter, r *http.Request, a platform.Actor) {
	data, err := s.reportPage(r, a)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	_, response, err := s.call(r, "POST", "/api/safety/reports/", map[string]any{"target_type": data["target_type"], "target_id": data["target_id"], "reason": r.PostForm.Get("reason"), "detail": strings.TrimSpace(r.PostForm.Get("detail"))})
	if err != nil {
		if response != nil && response.Code == http.StatusBadRequest {
			form := object(data["form"])
			form["as_p"] = pongoErrorPrefix("Choose a valid report reason.", form["as_p"])
			data["csrf"] = s.Auth.EnsureCSRF(w, r)
			if err := s.Renderer.Render(w, r, "web/report.html", data); err != nil {
				platform.Error(w, 500, "Page unavailable.")
			}
			return
		}
		if response != nil {
			platform.Error(w, response.Code, "Report could not be submitted.")
		} else {
			platform.Fail(w, err)
		}
		return
	}
	to := "/"
	if data["target_type"] == "activity" {
		to = routeURL("activity_detail", data["target_id"])
	}
	http.Redirect(w, r, to, http.StatusFound)
}
