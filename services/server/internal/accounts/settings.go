package accounts

import (
	"encoding/json"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"net/http"
	"sort"
	"strconv"
)

func (s *Service) Settings(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPut {
		var body struct {
			Muted  *[]string `json:"muted_kinds"`
			Access *struct {
				StepFree bool `json:"needs_step_free"`
				Toilet   bool `json:"needs_accessible_toilet"`
				Hearing  bool `json:"needs_hearing_loop"`
				Quiet    bool `json:"prefers_quiet"`
			} `json:"access"`
		}
		if platform.Decode(w, r, &body) != nil {
			platform.Fail(w, platform.ErrInvalid)
			return
		}
		err := platform.Transaction(r.Context(), s.DB, func(tx pgx.Tx) error {
			if body.Muted != nil {
				allowed := map[string]bool{"join_requested": true, "join_approved": true, "event_reminder": true, "activity_cancelled": true, "activity_updated": true, "announcement": true, "group_announcement": true, "meetup_confirmed": true, "arrival": true, "connection_request": true, "connection_accepted": true, "mention": true, "organizer_role": true, "activity_match": true, "gauge_match": true, "group_question": true, "interest_converted": true, "rsvp_nudge": true, "organizer_prep": true, "supervisor_needed": true, "formative_note": true, "mod_alert": true}
				muted := []string{}
				seen := map[string]bool{}
				for _, kind := range *body.Muted {
					if allowed[kind] && !seen[kind] {
						muted = append(muted, kind)
						seen[kind] = true
					}
				}
				sort.Strings(muted)
				if _, err := tx.Exec(r.Context(), `INSERT INTO notifications_notificationpreference(user_id,muted_kinds) VALUES($1,$2) ON CONFLICT(user_id) DO UPDATE SET muted_kinds=EXCLUDED.muted_kinds`, a.ID, muted); err != nil {
					return err
				}
				if err := platform.RecordAudit(r.Context(), tx, a, "notification.preferences_updated", "accounts.user:"+strconv.FormatInt(a.ID, 10), map[string]any{"muted_kinds": muted}); err != nil {
					return err
				}
			}
			if body.Access != nil {
				access := body.Access
				if _, err := tx.Exec(r.Context(), `INSERT INTO places_accesspreference(user_id,needs_step_free,needs_accessible_toilet,needs_hearing_loop,prefers_quiet,created_at,updated_at) VALUES($1,$2,$3,$4,$5,now(),now()) ON CONFLICT(user_id) DO UPDATE SET needs_step_free=EXCLUDED.needs_step_free,needs_accessible_toilet=EXCLUDED.needs_accessible_toilet,needs_hearing_loop=EXCLUDED.needs_hearing_loop,prefers_quiet=EXCLUDED.prefers_quiet,updated_at=now()`, a.ID, access.StepFree, access.Toilet, access.Hearing, access.Quiet); err != nil {
					return err
				}
				return platform.RecordAudit(r.Context(), tx, a, "user.access_preference_updated", "accounts.user:"+strconv.FormatInt(a.ID, 10), nil)
			}
			return nil
		})
		if err != nil {
			platform.Fail(w, err)
			return
		}
	}
	result, err := jsonObject(r.Context(), s.DB, `SELECT jsonb_build_object('muted_kinds',COALESCE((SELECT to_jsonb(muted_kinds) FROM notifications_notificationpreference WHERE user_id=$1),'[]'::jsonb),'access',jsonb_build_object('needs_step_free',COALESCE(p.needs_step_free,false),'needs_accessible_toilet',COALESCE(p.needs_accessible_toilet,false),'needs_hearing_loop',COALESCE(p.needs_hearing_loop,false),'prefers_quiet',COALESCE(p.prefers_quiet,false))) FROM accounts_user u LEFT JOIN places_accesspreference p ON p.user_id=u.id WHERE u.id=$1`, a.ID)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	var output map[string]any
	if json.Unmarshal(result, &output) != nil {
		platform.Fail(w, platform.ErrInvalid)
		return
	}
	platform.JSON(w, 200, output)
}
