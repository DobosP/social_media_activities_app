package safety

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func (s *Service) Register(mux *http.ServeMux) {
	registerExact(mux, "GET /moderation/", s.Console)
	registerExact(mux, "POST /moderation/", s.Console)
	registerExact(mux, "GET /account/restricted/", s.Restricted)
	registerExact(mux, "POST /account/restricted/", s.Restricted)
	for _, prefix := range []string{"/api/safety", "/api/v1/safety"} {
		registerExact(mux, "POST "+prefix+"/reports/", s.ReportHTTP)
		registerExact(mux, "POST "+prefix+"/blocks/", s.BlockHTTP)
		registerExact(mux, "DELETE "+prefix+"/blocks/", s.BlockHTTP)
		registerExact(mux, "GET "+prefix+"/appeals/", s.AppealHTTP)
		registerExact(mux, "POST "+prefix+"/appeals/", s.AppealHTTP)
		registerExact(mux, "GET "+prefix+"/moderation/reports/", s.ReportQueueHTTP)
		registerExact(mux, "POST "+prefix+"/moderation/reports/{id}/resolve/", s.ResolveReportHTTP)
		registerExact(mux, "GET "+prefix+"/moderation/appeals/", s.AppealQueueHTTP)
		registerExact(mux, "POST "+prefix+"/moderation/appeals/{id}/resolve/", s.ResolveAppealHTTP)
		registerExact(mux, "POST "+prefix+"/moderation/referrals/", s.ReferralHTTP)
		registerExact(mux, "GET "+prefix+"/moderation/referrals/{id}/proof/", s.ReferralProofHTTP)
		registerExact(mux, "GET "+prefix+"/me/record/", s.RecordHTTP)
		registerExact(mux, "GET "+prefix+"/moderation/concerns/", s.ConcernQueueHTTP)
		registerExact(mux, "POST "+prefix+"/moderation/concerns/{id}/resolve/", s.ResolveConcernHTTP)
	}
}
func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrConflict):
		platform.Error(w, 400, "This decision has already been contested or resolved.")
	case errors.Is(err, ErrRate):
		platform.Error(w, 429, "Too many requests; try again later.")
	default:
		platform.Fail(w, err)
	}
}
func moderator(w http.ResponseWriter, r *http.Request) (platform.Actor, bool) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return a, false
	}
	if !a.Moderator() {
		platform.Error(w, 403, "Moderator access required.")
		return a, false
	}
	return a, true
}
func requestID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, platform.ErrNotFound
	}
	return id, nil
}
func (s *Service) RecordHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	if s.Config.Accounts == nil {
		platform.Fail(w, errors.New("self safety record unavailable"))
		return
	}
	payload, err := s.Config.Accounts.SafetyRecord(r.Context(), a.ID)
	if err != nil {
		fail(w, err)
		return
	}
	platform.JSON(w, 200, payload)
}

func (s *Service) ReportHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	var body struct {
		Type   string `json:"target_type"`
		ID     int64  `json:"target_id"`
		Reason string `json:"reason"`
		Detail string `json:"detail"`
	}
	if platform.Decode(w, r, &body) != nil || reasons[body.Reason] == "" {
		fail(w, platform.ErrInvalid)
		return
	}
	// Eligibility precedes the debit, so refused probes never spend the budget.
	target, err := s.ReportTarget(r.Context(), a, body.Type, body.ID)
	if err != nil {
		fail(w, platform.ErrNotFound)
		return
	}
	allowed, err := s.allow(r.Context(), a, "report", 20, time.Hour)
	if err != nil {
		fail(w, err)
		return
	}
	if !allowed {
		fail(w, ErrRate)
		return
	}
	id, err := s.FileReport(r.Context(), a, target, body.Reason, body.Detail)
	if err != nil {
		fail(w, err)
		return
	}
	payload, err := s.Report(r.Context(), id, false)
	if err != nil {
		fail(w, err)
		return
	}
	platform.JSON(w, 201, payload)
}
func (s *Service) BlockHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	var body struct {
		UserID int64 `json:"user_id"`
	}
	if platform.Decode(w, r, &body) != nil || body.UserID < 1 || body.UserID == a.ID {
		fail(w, platform.ErrInvalid)
		return
	}
	// Block and unblock share one budget, charged before the target lookup so
	// the endpoint cannot be used as a cheap account-ID existence oracle.
	allowed, err := s.allow(r.Context(), a, "block", 30, time.Hour)
	if err != nil {
		fail(w, err)
		return
	}
	if !allowed {
		fail(w, ErrRate)
		return
	}
	target, err := s.ResolveTarget(r.Context(), s.DB, "accounts", "user", body.UserID)
	if err != nil {
		fail(w, err)
		return
	}
	err = platform.Transaction(r.Context(), s.DB, func(tx pgx.Tx) error {
		var changed bool
		var event string
		if r.Method == http.MethodDelete {
			tag, err := tx.Exec(r.Context(), `DELETE FROM safety_block WHERE blocker_id=$1 AND blocked_id=$2`, a.ID, body.UserID)
			if err != nil {
				return err
			}
			changed = tag.RowsAffected() > 0
			event = "user.unblocked"
		} else {
			tag, err := tx.Exec(r.Context(), `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now()) ON CONFLICT(blocker_id,blocked_id) DO NOTHING`, a.ID, body.UserID)
			if err != nil {
				return err
			}
			changed = tag.RowsAffected() > 0
			event = "user.blocked"
		}
		if changed {
			return platform.RecordAudit(r.Context(), tx, a, event, "accounts.user:"+strconv.FormatInt(target.ID, 10), nil)
		}
		return nil
	})
	if err != nil {
		fail(w, err)
		return
	}
	platform.JSON(w, 204, nil)
}
func (s *Service) ReportQueueHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := moderator(w, r)
	if !ok {
		return
	}
	payload, err := s.ReportQueue(r.Context(), a, r.URL.Query().Get("status"))
	if err != nil {
		fail(w, err)
		return
	}
	platform.JSON(w, 200, payload)
}

func (s *Service) ResolveReportHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := moderator(w, r)
	if !ok {
		return
	}
	id, err := requestID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var body ActionInput
	if platform.Decode(w, r, &body) != nil {
		fail(w, platform.ErrInvalid)
		return
	}
	if body.Decision == "dismiss" {
		err = platform.Transaction(r.Context(), s.DB, func(tx pgx.Tx) error {
			var reporter int64
			if err := tx.QueryRow(r.Context(), `SELECT COALESCE(reporter_id,0) FROM safety_report WHERE id=$1 FOR UPDATE`, id).Scan(&reporter); err != nil {
				return err
			}
			if _, err := tx.Exec(r.Context(), `UPDATE safety_report SET status='dismissed',handled_by_id=$2,handled_at=now(),resolution=$3 WHERE id=$1`, id, a.ID, body.Notes); err != nil {
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
				if body.Reason == "" {
					body.Reason = reason
				}
				_, err = s.TakeAction(r.Context(), a, target, body, id)
			}
		}
	}
	if err != nil {
		fail(w, err)
		return
	}
	payload, err := s.Report(r.Context(), id, true)
	if err != nil {
		fail(w, err)
		return
	}
	platform.JSON(w, 200, payload)
}
func (s *Service) AppealHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		payload, err := s.Appeals(r.Context(), a.ID)
		if err != nil {
			fail(w, err)
			return
		}
		platform.JSON(w, 200, payload)
		return
	}
	var body struct {
		Action    int64  `json:"action_id"`
		Statement string `json:"statement"`
	}
	if platform.Decode(w, r, &body) != nil {
		fail(w, platform.ErrInvalid)
		return
	}
	if _, err := s.FileAppeal(r.Context(), a, body.Action, body.Statement); err != nil {
		fail(w, err)
		return
	}
	platform.JSON(w, 201, map[string]string{"status": "pending", "detail": "Your appeal was received."})
}
func (s *Service) AppealQueueHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := moderator(w, r)
	if !ok {
		return
	}
	payload, err := s.AppealQueue(r.Context(), a, r.URL.Query().Get("status"))
	if err != nil {
		fail(w, err)
		return
	}
	platform.JSON(w, 200, payload)
}
func (s *Service) ResolveAppealHTTP(w http.ResponseWriter, r *http.Request) {
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
		Grant *bool  `json:"grant"`
		Notes string `json:"notes"`
	}
	if platform.Decode(w, r, &body) != nil || body.Grant == nil {
		fail(w, platform.ErrInvalid)
		return
	}
	if _, err = s.ResolveAppeal(r.Context(), a, id, *body.Grant, body.Notes); err != nil {
		fail(w, err)
		return
	}
	payload, err := s.Appeal(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	platform.JSON(w, 200, payload)
}

type ReferralInput struct {
	Subject   string `json:"subject"`
	Reason    string `json:"reason"`
	Authority string `json:"authority"`
	Reference string `json:"reference"`
	ReportID  int64  `json:"report_id"`
	Notes     string `json:"notes"`
}

func (s *Service) ReferralHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := moderator(w, r)
	if !ok {
		return
	}
	var body ReferralInput
	if platform.Decode(w, r, &body) != nil || reasons[body.Reason] == "" || authorities[body.Authority] == "" || utf8.RuneCountInString(body.Reference) > 128 {
		fail(w, platform.ErrInvalid)
		return
	}
	var id int64
	err := platform.Transaction(r.Context(), s.DB, func(tx pgx.Tx) error {
		var user int64
		if err := tx.QueryRow(r.Context(), `SELECT id FROM accounts_user WHERE public_id=$1::uuid`, body.Subject).Scan(&user); err != nil {
			return err
		}
		var report any
		if body.ReportID > 0 {
			var found bool
			if err := tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM safety_report WHERE id=$1)`, body.ReportID).Scan(&found); err != nil {
				return err
			}
			if found {
				report = body.ReportID
			}
		}
		if err := tx.QueryRow(r.Context(), `INSERT INTO safety_authorityreferral(subject_ref,reason,authority,reference,audit_anchor_hash,notes,created_at,report_id,referred_by_id) VALUES($1,$2,$3,$4,'',$5,now(),$6,$7) RETURNING id`, body.Subject, body.Reason, body.Authority, body.Reference, body.Notes, report, a.ID).Scan(&id); err != nil {
			return err
		}
		if err := platform.RecordAudit(r.Context(), tx, a, "authority.referral", "accounts.user:"+strconv.FormatInt(user, 10), map[string]string{"reason": body.Reason, "authority": body.Authority}); err != nil {
			return err
		}
		_, err := tx.Exec(r.Context(), `UPDATE safety_authorityreferral SET audit_anchor_hash=(SELECT hash FROM safety_auditlog ORDER BY id DESC LIMIT 1) WHERE id=$1`, id)
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	payload, err := s.ReferralProof(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	platform.JSON(w, 201, payload)
}
func (s *Service) ReferralProofHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := moderator(w, r)
	if !ok {
		return
	}
	id, err := requestID(r)
	if err != nil {
		fail(w, err)
		return
	}
	err = platform.Transaction(r.Context(), s.DB, func(tx pgx.Tx) error {
		return platform.RecordAudit(r.Context(), tx, a, "authority.referral_proof_viewed", "safety.authorityreferral:"+strconv.FormatInt(id, 10), nil)
	})
	if err != nil {
		fail(w, err)
		return
	}
	payload, err := s.ReferralProof(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	platform.JSON(w, 200, payload)
}
func (s *Service) ReferralProof(ctx context.Context, id int64) (map[string]any, error) {
	var subject, reason, authority, reference, anchor string
	var created time.Time
	err := s.DB.QueryRow(ctx, `SELECT subject_ref::text,reason,authority,reference,audit_anchor_hash,created_at FROM safety_authorityreferral WHERE id=$1`, id).Scan(&subject, &reason, &authority, &reference, &anchor, &created)
	if err != nil {
		return nil, err
	}
	valid, _, err := s.VerifyAuditChain(ctx, nil)
	if err != nil {
		return nil, err
	}
	label := reasons[reason]
	if label == "" {
		label = reason
	}
	return map[string]any{"subject_ref": subject, "reason_label": label, "authority": authorities[authority], "reference": reference, "created_at": created, "anchor_hash": anchor, "chain_valid": valid}, nil
}

func (s *Service) RestrictionStatement(ctx context.Context, user int64) (json.RawMessage, error) {
	return object(ctx, s.DB, `SELECT jsonb_build_object('action_id',m.id,'action_label',CASE m.action WHEN 'suspend' THEN 'Suspend account' WHEN 'timed_ban' THEN 'Timed ban' WHEN 'ban' THEN 'Ban account' END,'reason_label',CASE m.reason WHEN 'grooming' THEN 'Grooming / predatory contact' WHEN 'harassment' THEN 'Harassment / bullying' WHEN 'csam' THEN 'Child sexual abuse material' WHEN 'spam' THEN 'Spam' WHEN 'off_platform' THEN 'Unsafe off-platform / meetup risk' WHEN 'other' THEN 'Other' ELSE m.reason END,'created_at',m.created_at,'is_lifetime',m.action='ban','lifts_at',CASE WHEN m.action='ban' THEN NULL ELSE m.expires_at END,'appeal_status',ap.status,'appeal_status_label',CASE ap.status WHEN 'pending' THEN 'Pending review' WHEN 'upheld' THEN 'Upheld (decision stands)' WHEN 'overturned' THEN 'Overturned (decision reversed)' END,'can_appeal',ap.id IS NULL) FROM safety_moderationaction m JOIN django_content_type c ON c.id=m.target_type_id LEFT JOIN safety_moderationappeal ap ON ap.action_id=m.id WHERE c.app_label='accounts' AND c.model='user' AND m.target_id=$1 AND m.action IN ('suspend','timed_ban','ban') AND m.lifted_at IS NULL AND (m.action='ban' OR m.expires_at IS NULL OR m.expires_at>$2) ORDER BY m.created_at DESC,m.id DESC LIMIT 1`, user, s.Config.Now())
}

func registerExact(mux *http.ServeMux, pattern string, handler http.HandlerFunc) {
	if strings.HasSuffix(pattern, "/") {
		pattern += "{$}"
	}
	mux.HandleFunc(pattern, handler)
}
