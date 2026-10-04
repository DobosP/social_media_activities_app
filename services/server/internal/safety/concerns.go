package safety

import (
	"context"
	"encoding/json"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

const TeenRelayTemplate = "Hi — a few people felt this post might be a bit off-topic for this group. That happens to everyone; nothing's wrong. Just a gentle heads-up in case you'd like to tweak it. You're doing great being here."

func (s *Service) ConcernQueueHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := moderator(w, r)
	if !ok {
		return
	}
	rows, err := objects(r.Context(), s.DB, `SELECT jsonb_build_object('id',id,'kind',kind,'post',post_id,'subject_user',subject_user_id,'payload',payload,'status',status,'handled_by',handled_by_id,'handled_at',handled_at,'created_at',created_at) FROM safety_concernreview WHERE ($1='' OR status=$1) ORDER BY created_at,id LIMIT 200`, r.URL.Query().Get("status"))
	if err != nil {
		fail(w, err)
		return
	}
	err = platform.Transaction(r.Context(), s.DB, func(tx pgx.Tx) error {
		return platform.RecordAudit(r.Context(), tx, a, "concern.queue_viewed", "", map[string]int{"count": len(rows)})
	})
	if err != nil {
		fail(w, err)
		return
	}
	platform.JSON(w, 200, rows)
}
func (s *Service) ResolveConcernHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := moderator(w, r)
	if !ok {
		return
	}
	id, err := requestID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var body struct {
		Action string `json:"action"`
		Note   string `json:"note"`
	}
	if platform.Decode(w, r, &body) != nil {
		fail(w, platform.ErrInvalid)
		return
	}
	delivered, err := s.ResolveConcern(r.Context(), a, id, body.Action, body.Note)
	if err != nil {
		fail(w, err)
		return
	}
	platform.JSON(w, 200, map[string]any{"ok": true, "note_delivered": delivered})
}

// ResolveConcern is an explicit human action. Sensor items remain informational;
// escalation always targets a post, never the protected subject of a sensor.
func (s *Service) ResolveConcern(ctx context.Context, a platform.Actor, id int64, action, note string) (bool, error) {
	if !a.Moderator() {
		return false, platform.ErrForbidden
	}
	if utf8.RuneCountInString(note) > 2000 {
		return false, platform.ErrInvalid
	}
	delivered := false
	err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		var kind, status string
		var post, subject *int64
		if err := tx.QueryRow(ctx, `SELECT kind,status,post_id,subject_user_id FROM safety_concernreview WHERE id=$1 FOR UPDATE`, id).Scan(&kind, &status, &post, &subject); err != nil {
			return err
		}
		if status != "open" {
			return ErrConflict
		}
		event := ""
		switch action {
		case "review":
			status = "reviewed"
			event = "concern.reviewed"
		case "dismiss":
			status = "dismissed"
			event = "concern.dismissed"
		case "escalate":
			if kind != "concern_escalated" && kind != "teen_concern" || post == nil {
				return platform.ErrInvalid
			}
			target, err := s.ResolveTarget(ctx, tx, "social", "post", *post)
			if err != nil {
				return err
			}
			label := "Concern escalated (k2, adult)"
			if kind == "teen_concern" {
				label = "Teen concern (human relay)"
			}
			if _, err = s.fileReport(ctx, tx, a, target, "other", "Escalated from concern review #"+strconv.FormatInt(id, 10)+" ("+label+"). Formative concern signal reviewed by a moderator and routed to the Report queue."); err != nil {
				return err
			}
			status = "escalated"
			event = "concern.escalated"
		case "send_note":
			if kind != "teen_concern" || subject == nil {
				return platform.ErrInvalid
			}
			text := strings.TrimSpace(note)
			if text == "" {
				text = TeenRelayTemplate
			}
			url := ""
			if post != nil {
				var activity, group *int64
				err := tx.QueryRow(ctx, `SELECT t.activity_id,t.group_id FROM social_post p JOIN social_thread t ON t.id=p.thread_id WHERE p.id=$1`, *post).Scan(&activity, &group)
				if err != nil {
					return err
				}
				if group != nil {
					url = "/groups/" + strconv.FormatInt(*group, 10) + "/"
				} else if activity != nil {
					url = "/activities/" + strconv.FormatInt(*activity, 10) + "/"
				}
			}
			delivered = notify(ctx, tx, *subject, "formative_note", "A quiet note about one of your posts", text, url)
			status = "reviewed"
			event = "concern.note_muted"
			if delivered {
				event = "concern.note_relayed"
			}
		default:
			return platform.ErrInvalid
		}
		if _, err := tx.Exec(ctx, `UPDATE safety_concernreview SET status=$2,handled_by_id=$3,handled_at=now() WHERE id=$1`, id, status, a.ID); err != nil {
			return err
		}
		return platform.RecordAudit(ctx, tx, a, event, "safety.concernreview:"+strconv.FormatInt(id, 10), nil)
	})
	return delivered, err
}

// RawConcern is staff-only through the owning route; no flagger identities exist
// in its DTO and no incident facts enter any public progression/profile surface.
func (s *Service) RawConcern(ctx context.Context, id int64) (json.RawMessage, error) {
	return object(ctx, s.DB, `SELECT jsonb_build_object('id',id,'kind',kind,'status',status,'payload',payload) FROM safety_concernreview WHERE id=$1`, id)
}
