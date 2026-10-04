package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"
)

func (s *Service) Wards(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r)
	if !ok {
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT DISTINCT u.id FROM accounts_user u JOIN accounts_guardianrelationship g ON g.ward_id=u.id WHERE g.guardian_id=$1 AND g.status='active' ORDER BY u.id`, a.ID)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			platform.Fail(w, err)
			return
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		platform.Fail(w, err)
		return
	}
	out := []map[string]any{}
	for _, id := range ids {
		ward, err := s.actor(r.Context(), s.DB, id)
		if err != nil {
			platform.Fail(w, err)
			return
		}
		payload, err := s.wardPayload(r.Context(), s.DB, ward)
		if err != nil {
			platform.Fail(w, err)
			return
		}
		out = append(out, payload)
	}
	platform.JSON(w, 200, out)
}
func (s *Service) WardDetail(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r)
	if !ok {
		return
	}
	ward, err := s.ward(r.Context(), s.DB, a, r.PathValue("public_id"))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if r.Method == http.MethodDelete {
		if err = s.Erase(r.Context(), a, ward); err != nil {
			platform.Fail(w, err)
			return
		}
		platform.JSON(w, 204, nil)
		return
	}
	if r.Method == http.MethodPatch {
		var body struct {
			Display *string `json:"display_name"`
		}
		if platform.Decode(w, r, &body) != nil || body.Display == nil || utf8.RuneCountInString(*body.Display) > 120 {
			platform.Fail(w, platform.ErrInvalid)
			return
		}
		err = platform.Transaction(r.Context(), s.DB, func(tx pgx.Tx) error {
			yes, err := s.isGuardian(r.Context(), tx, a.ID, ward.ID)
			if err != nil {
				return err
			}
			if !yes {
				return platform.ErrForbidden
			}
			_, err = tx.Exec(r.Context(), `UPDATE accounts_user SET display_name=$2 WHERE id=$1`, ward.ID, *body.Display)
			if err != nil {
				return err
			}
			return platform.RecordAudit(r.Context(), tx, a, "guardian.ward_updated", "accounts.user:"+strconv.FormatInt(ward.ID, 10), nil)
		})
		if err != nil {
			platform.Fail(w, err)
			return
		}
		ward.DisplayName = *body.Display
	}
	payload, err := s.wardPayload(r.Context(), s.DB, ward)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, payload)
}

func (s *Service) GuardianLinks(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		out, err := jsonObjects(r.Context(), s.DB, `SELECT jsonb_build_object('token',i.token,'guardian',g.display_name,'guardian_public_id',g.public_id,'ward',w.display_name,'ward_public_id',w.public_id,'relationship',i.relationship,'status',i.status,'created_at',i.created_at,'expires_at',i.expires_at) FROM accounts_guardianlinkinvite i JOIN accounts_user g ON g.id=i.guardian_id JOIN accounts_user w ON w.id=i.ward_id WHERE i.ward_id=$1 AND i.status='pending' AND i.expires_at>$2 ORDER BY i.id`, a.ID, s.Config.Now())
		if err != nil {
			platform.Fail(w, err)
			return
		}
		platform.JSON(w, 200, out)
		return
	}
	if !s.Config.AllowMinorOnboarding {
		platform.Error(w, 400, "Minor onboarding is disabled on this deployment.")
		return
	}
	allowed, err := s.allowAction(r.Context(), a.ID, "guardian_invite", 20, time.Hour)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if !allowed {
		platform.Error(w, 429, "Too many invites; try again later.")
		return
	}
	var body struct {
		Ward         string `json:"ward"`
		Relationship string `json:"relationship"`
	}
	if platform.Decode(w, r, &body) != nil {
		platform.Fail(w, platform.ErrInvalid)
		return
	}
	if body.Relationship == "" {
		body.Relationship = "parent"
	}
	if utf8.RuneCountInString(body.Relationship) > 32 {
		platform.Fail(w, platform.ErrInvalid)
		return
	}
	ward, err := s.byPublicID(r.Context(), s.DB, body.Ward)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	var id int64
	err = platform.Transaction(r.Context(), s.DB, func(tx pgx.Tx) error {
		if a.ID == ward.ID || a.Cohort != "adult" || !a.IdentityVerified || !ward.IsActive || (ward.Cohort != "child" && ward.Cohort != "teen") {
			return platform.ErrInvalid
		}
		if err := platform.Participate(r.Context(), tx, a); err != nil {
			return err
		}
		blocked, err := platform.Blocked(r.Context(), tx, a.ID, ward.ID)
		if err != nil {
			return err
		}
		if blocked {
			return platform.ErrForbidden
		}
		yes, err := s.isGuardian(r.Context(), tx, a.ID, ward.ID)
		if err != nil {
			return err
		}
		if yes {
			return platform.ErrInvalid
		}
		if _, err = tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "guardian:"+strconv.FormatInt(a.ID, 10)+":"+strconv.FormatInt(ward.ID, 10)); err != nil {
			return err
		}
		err = tx.QueryRow(r.Context(), `SELECT id FROM accounts_guardianlinkinvite WHERE guardian_id=$1 AND ward_id=$2 AND status='pending' FOR UPDATE`, a.ID, ward.ID).Scan(&id)
		token := randomState()
		expires := s.Config.Now().Add(s.Config.GuardianInviteTTL)
		if err == nil {
			_, err = tx.Exec(r.Context(), `UPDATE accounts_guardianlinkinvite SET relationship=$2,token=$3,expires_at=$4 WHERE id=$1`, id, body.Relationship, token, expires)
		} else if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(r.Context(), `INSERT INTO accounts_guardianlinkinvite(guardian_id,ward_id,relationship,token,status,created_at,expires_at,responded_at) VALUES($1,$2,$3,$4,'pending',now(),$5,NULL) RETURNING id`, a.ID, ward.ID, body.Relationship, token, expires).Scan(&id)
		}
		if err != nil {
			return err
		}
		return platform.RecordAudit(r.Context(), tx, a, "guardian.link_invited", "accounts.user:"+strconv.FormatInt(ward.ID, 10), nil)
	})
	if err != nil {
		platform.Fail(w, err)
		return
	}
	result, err := s.invitePayload(r.Context(), id)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 201, result)
}

func (s *Service) invitePayload(ctx context.Context, id int64) (json.RawMessage, error) {
	return jsonObject(ctx, s.DB, `SELECT jsonb_build_object('token',i.token,'guardian',g.display_name,'guardian_public_id',g.public_id,'ward',w.display_name,'ward_public_id',w.public_id,'relationship',i.relationship,'status',i.status,'created_at',i.created_at,'expires_at',i.expires_at) FROM accounts_guardianlinkinvite i JOIN accounts_user g ON g.id=i.guardian_id JOIN accounts_user w ON w.id=i.ward_id WHERE i.id=$1`, id)
}

func (s *Service) AcceptGuardian(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r)
	if !ok {
		return
	}
	if !s.Config.AllowMinorOnboarding {
		platform.Error(w, 400, "Minor onboarding is disabled on this deployment.")
		return
	}
	expired := false
	err := platform.Transaction(r.Context(), s.DB, func(tx pgx.Tx) error {
		var id, guardian int64
		var relationship string
		var expires time.Time
		err := tx.QueryRow(r.Context(), `SELECT id,guardian_id,relationship,expires_at FROM accounts_guardianlinkinvite WHERE token=$1 AND status='pending' AND ward_id=$2 FOR UPDATE`, r.PathValue("token"), a.ID).Scan(&id, &guardian, &relationship, &expires)
		if err != nil {
			return err
		}
		if !expires.After(s.Config.Now()) {
			expired = true
			_, err = tx.Exec(r.Context(), `UPDATE accounts_guardianlinkinvite SET status='expired',responded_at=now() WHERE id=$1`, id)
			return err
		}
		g, err := s.actor(r.Context(), tx, guardian)
		if err != nil {
			return err
		}
		if g.Cohort != "adult" || (a.Cohort != "child" && a.Cohort != "teen") {
			return platform.ErrInvalid
		}
		if err := platform.Participate(r.Context(), tx, g); err != nil {
			return err
		}
		blocked, err := platform.Blocked(r.Context(), tx, g.ID, a.ID)
		if err != nil {
			return err
		}
		if blocked {
			return platform.ErrForbidden
		}
		if _, err = tx.Exec(r.Context(), `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,relationship,status,consent_id,created_at,updated_at) VALUES($1,$2,$3,'active',NULL,now(),now()) ON CONFLICT(guardian_id,ward_id) DO UPDATE SET relationship=EXCLUDED.relationship,status='active',consent_id=NULL,updated_at=now()`, g.ID, a.ID, relationship); err != nil {
			return err
		}
		if _, err = tx.Exec(r.Context(), `UPDATE accounts_guardianlinkinvite SET status='accepted',responded_at=now() WHERE id=$1`, id); err != nil {
			return err
		}
		return platform.RecordAudit(r.Context(), tx, a, "guardian.link_accepted", "accounts.user:"+strconv.FormatInt(g.ID, 10), nil)
	})
	if expired {
		platform.Error(w, 400, "This invite has expired.")
		return
	}
	if err != nil {
		platform.Fail(w, err)
		return
	}
	payload, err := s.Self(r.Context(), s.DB, a)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, payload)
}
func (s *Service) DeclineGuardian(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r)
	if !ok {
		return
	}
	err := platform.Transaction(r.Context(), s.DB, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `UPDATE accounts_guardianlinkinvite SET status='declined',responded_at=now() WHERE token=$1 AND ward_id=$2 AND status='pending'`, r.PathValue("token"), a.ID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return platform.ErrInvalid
		}
		return platform.RecordAudit(r.Context(), tx, a, "guardian.link_declined", "accounts.user:"+strconv.FormatInt(a.ID, 10), nil)
	})
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 204, nil)
}

func (s *Service) WardConsent(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r)
	if !ok {
		return
	}
	ward, err := s.ward(r.Context(), s.DB, a, r.PathValue("public_id"))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	err = platform.Transaction(r.Context(), s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `SELECT id FROM accounts_user WHERE id=$1 FOR UPDATE`, ward.ID); err != nil {
			return err
		}
		yes, err := s.isGuardian(r.Context(), tx, a.ID, ward.ID)
		if err != nil {
			return err
		}
		if !yes {
			return platform.ErrForbidden
		}
		if r.Method == http.MethodDelete {
			if _, err = tx.Exec(r.Context(), `UPDATE accounts_parentalconsent SET status='revoked',revoked_at=now(),updated_at=now() WHERE minor_id=$1 AND status='active'`, ward.ID); err != nil {
				return err
			}
			return evictParticipation(r.Context(), tx, ward, "consent_revoked")
		}
		if !s.Config.AllowMinorOnboarding || ward.AgeBand != "under_16" || a.Cohort != "adult" {
			return platform.ErrInvalid
		}
		if err := platform.Participate(r.Context(), tx, a); err != nil {
			return err
		}
		var id int64
		err = tx.QueryRow(r.Context(), `SELECT id FROM accounts_parentalconsent WHERE minor_id=$1 AND guardian_identifier=$2 ORDER BY id LIMIT 1 FOR UPDATE`, ward.ID, a.PublicID).Scan(&id)
		expires := s.Config.Now().Add(s.Config.ConsentValidity)
		if err == nil {
			_, err = tx.Exec(r.Context(), `UPDATE accounts_parentalconsent SET status='active',scope='',granted_at=now(),expires_at=$2,revoked_at=NULL,renewal_notice='',updated_at=now() WHERE id=$1`, id, expires)
		} else if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(r.Context(), `INSERT INTO accounts_parentalconsent(minor_id,guardian_identifier,status,scope,granted_at,expires_at,revoked_at,renewal_notice,created_at,updated_at) VALUES($1,$2,'active','',now(),$3,NULL,'',now(),now()) RETURNING id`, ward.ID, a.PublicID, expires).Scan(&id)
		}
		if err != nil {
			return err
		}
		if _, err = tx.Exec(r.Context(), `UPDATE accounts_guardianrelationship SET consent_id=$3,updated_at=now() WHERE guardian_id=$1 AND ward_id=$2 AND status='active'`, a.ID, ward.ID, id); err != nil {
			return err
		}
		return platform.RecordAudit(r.Context(), tx, a, "guardian.consent_granted", "accounts.user:"+strconv.FormatInt(ward.ID, 10), nil)
	})
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if r.Method == http.MethodDelete {
		platform.JSON(w, 204, nil)
		return
	}
	payload, err := s.wardPayload(r.Context(), s.DB, ward)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 201, payload)
}

func evictParticipation(ctx context.Context, tx pgx.Tx, a platform.Actor, reason string) error {
	tag, err := tx.Exec(ctx, `UPDATE messaging_participant SET state='removed' WHERE user_id=$1 AND state IN ('active','invited')`, a.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		if err = platform.RecordAudit(ctx, tx, a, "messaging.participation_revoked", "", map[string]any{"count": tag.RowsAffected(), "reason": reason}); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE messaging_participant p SET state='removed' WHERE p.role='guardian' AND p.state='active' AND NOT EXISTS(SELECT 1 FROM accounts_guardianrelationship g JOIN accounts_user w ON w.id=g.ward_id JOIN messaging_participant wp ON wp.user_id=w.id AND wp.conversation_id=p.conversation_id AND wp.state='active' WHERE g.guardian_id=p.user_id AND g.status='active' AND w.cohort='child')`); err != nil {
		return err
	}
	tag, err = tx.Exec(ctx, `UPDATE social_groupmembership SET state='removed' WHERE user_id=$1 AND state='member'`, a.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return platform.RecordAudit(ctx, tx, a, "group.participation_revoked", "", map[string]any{"count": tag.RowsAffected(), "reason": reason})
	}
	return nil
}
