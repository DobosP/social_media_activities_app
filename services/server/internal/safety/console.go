package safety

import (
	"encoding/json"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"html/template"
	"net/http"
	"strconv"
)

var consolePage = template.Must(template.New("moderation").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Moderation</title><main><h1>Moderation</h1>{{if .Message}}<p role="status">{{.Message}}</p>{{end}}<h2>Reports</h2><pre>{{.Reports}}</pre><h2>Resolve a report</h2><form method="post"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><input type="hidden" name="operation" value="report"><label>Report ID<input name="id" type="number" min="1" required></label><label>Decision<select name="decision"><option value="dismiss">Dismiss</option><option value="warn">Warn</option><option value="remove">Remove content</option><option value="suspend">Suspend account</option><option value="timed_ban">Timed ban</option><option value="ban">Lifetime ban</option></select></label><label>Duration in days for timed restrictions<input name="days" type="number" min="1" max="36500"></label><label>Private notes<textarea name="notes" maxlength="2000"></textarea></label><button>Resolve report</button></form><h2>Appeals</h2><pre>{{.Appeals}}</pre><form method="post"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><input type="hidden" name="operation" value="appeal"><label>Appeal ID<input name="id" type="number" min="1" required></label><label>Outcome<select name="decision"><option value="uphold">Uphold</option><option value="overturn">Overturn</option></select></label><label>Private notes<textarea name="notes" maxlength="2000"></textarea></label><button>Resolve appeal</button></form><h2>Soft concerns</h2><pre>{{.Concerns}}</pre><p>Sensor items protect people and cannot be escalated into allegations.</p><form method="post"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><input type="hidden" name="operation" value="concern"><label>Review ID<input name="id" type="number" min="1" required></label><label>Decision<select name="decision"><option value="review">Reviewed</option><option value="dismiss">Dismiss</option><option value="escalate">Escalate the post to reports</option><option value="send_note">Send a human teen note</option></select></label><label>Optional human teen note<textarea name="notes" maxlength="2000"></textarea></label><button>Handle concern</button></form></main></html>`))

func (s *Service) Console(w http.ResponseWriter, r *http.Request) {
	a, ok := moderator(w, r)
	if !ok {
		return
	}
	if s.Config.Accounts == nil || s.Config.Accounts.Auth == nil {
		platform.Error(w, 503, "Moderator console unavailable.")
		return
	}
	csrf := s.Config.Accounts.Auth.EnsureCSRF(w, r)
	message := ""
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		if r.ParseForm() != nil {
			platform.Error(w, 400, "Invalid form.")
			return
		}
		r.Header.Set("X-CSRFToken", r.PostForm.Get("csrf_token"))
		if s.Config.Accounts.Auth.CheckCSRF(r) != nil {
			platform.Error(w, 403, "CSRF verification failed.")
			return
		}
		id, err := strconv.ParseInt(r.PostForm.Get("id"), 10, 64)
		if err != nil || id < 1 {
			platform.Error(w, 400, "Invalid record ID.")
			return
		}
		decision, notes := r.PostForm.Get("decision"), r.PostForm.Get("notes")
		switch r.PostForm.Get("operation") {
		case "appeal":
			if decision != "uphold" && decision != "overturn" {
				err = platform.ErrInvalid
			} else {
				_, err = s.ResolveAppeal(r.Context(), a, id, decision == "overturn", notes)
			}
		case "concern":
			_, err = s.ResolveConcern(r.Context(), a, id, decision, notes)
		case "report":
			if decision == "dismiss" {
				err = platform.Transaction(r.Context(), s.DB, func(tx pgx.Tx) error {
					var reporter int64
					if err := tx.QueryRow(r.Context(), `SELECT COALESCE(reporter_id,0) FROM safety_report WHERE id=$1 FOR UPDATE`, id).Scan(&reporter); err != nil {
						return err
					}
					if _, err := tx.Exec(r.Context(), `UPDATE safety_report SET status='dismissed',handled_by_id=$2,handled_at=now(),resolution=$3 WHERE id=$1`, id, a.ID, notes); err != nil {
						return err
					}
					if err := platform.RecordAudit(r.Context(), tx, a, "report.dismissed", "safety.report:"+strconv.FormatInt(id, 10), nil); err != nil {
						return err
					}
					notify(r.Context(), tx, reporter, "system", "Your report was reviewed", "Thanks for your report. Our moderation team reviewed it and found no action was needed.", "")
					return guardians(r.Context(), tx, reporter)
				})
			} else {
				var app, model, reason string
				var targetID int64
				err = s.DB.QueryRow(r.Context(), `SELECT c.app_label,c.model,r.target_id,r.reason FROM safety_report r JOIN django_content_type c ON c.id=r.target_type_id WHERE r.id=$1`, id).Scan(&app, &model, &targetID, &reason)
				if err == nil {
					target, e := s.ResolveTarget(r.Context(), s.DB, app, model, targetID)
					err = e
					if err == nil {
						days, _ := strconv.Atoi(r.PostForm.Get("days"))
						_, err = s.TakeAction(r.Context(), a, target, ActionInput{Decision: decision, Reason: reason, Notes: notes, SuspendDays: days}, id)
					}
				}
			}
		default:
			err = platform.ErrInvalid
		}
		if err != nil {
			message = "This action could not be applied. Check its scope, current status and required duration."
		} else {
			message = "The action was recorded."
		}
	}
	reports, err := s.ReportQueue(r.Context(), a, "")
	if err != nil {
		fail(w, err)
		return
	}
	appeals, err := s.AppealQueue(r.Context(), a, "")
	if err != nil {
		fail(w, err)
		return
	}
	concerns, err := objects(r.Context(), s.DB, `SELECT jsonb_build_object('id',id,'kind',kind,'status',status,'post',post_id,'payload',payload,'created_at',created_at) FROM safety_concernreview WHERE status='open' ORDER BY created_at,id LIMIT 200`)
	if err != nil {
		fail(w, err)
		return
	}
	reportJSON, _ := json.MarshalIndent(reports, "", "  ")
	appealJSON, _ := json.MarshalIndent(appeals, "", "  ")
	concernJSON, _ := json.MarshalIndent(concerns, "", "  ")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = consolePage.Execute(w, map[string]any{"CSRF": csrf, "Message": message, "Reports": string(reportJSON), "Appeals": string(appealJSON), "Concerns": string(concernJSON)})
}
