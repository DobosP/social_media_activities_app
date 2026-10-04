package messaging

import (
	"context"
	"errors"
	"fmt"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

func (s *Service) guardianEligible(ctx context.Context, q platform.Querier, a platform.Actor, id int64) (bool, error) {
	fresh, e := actor(ctx, q, a.ID)
	if errors.Is(e, pgx.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	a = fresh
	if !a.IsActive || a.Cohort != "adult" {
		return false, nil
	}
	if e = platform.Participate(ctx, q, a); e != nil {
		if errors.Is(e, platform.ErrForbidden) {
			return false, nil
		}
		return false, e
	}
	rows, e := q.Query(ctx, `SELECT u.id FROM messaging_participant p JOIN accounts_guardianrelationship g ON g.ward_id=p.user_id JOIN accounts_user u ON u.id=p.user_id JOIN messaging_conversation c ON c.id=p.conversation_id WHERE p.conversation_id=$1 AND c.cohort='child' AND p.state='active' AND p.role!='guardian' AND u.cohort='child' AND u.is_active AND g.guardian_id=$2 AND g.status='active' LIMIT 256`, id, a.ID)
	if e != nil {
		return false, e
	}
	var ids []int64
	for rows.Next() {
		var userID int64
		if e = rows.Scan(&userID); e != nil {
			rows.Close()
			return false, e
		}
		ids = append(ids, userID)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return false, e
	}
	for _, userID := range ids {
		ward, e := actor(ctx, q, userID)
		if e != nil {
			return false, e
		}
		if e = platform.Participate(ctx, q, ward); e == nil {
			return true, nil
		} else if !errors.Is(e, platform.ErrForbidden) {
			return false, e
		}
	}
	return false, nil
}
func (s *Service) Transition(ctx context.Context, a platform.Actor, id int64, action string) error {
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		fresh, e := actor(ctx, tx, a.ID)
		if e != nil {
			return e
		}
		a = fresh
		var state, role, cohort string
		if e = tx.QueryRow(ctx, `SELECT p.state,p.role,c.cohort FROM messaging_participant p JOIN messaging_conversation c ON c.id=p.conversation_id WHERE p.user_id=$1 AND c.id=$2 FOR UPDATE OF p,c`, a.ID, id).Scan(&state, &role, &cohort); e != nil {
			return e
		}
		next := ""
		switch action {
		case "accept":
			if state != "invited" || a.Cohort == "unassigned" || a.Cohort != cohort {
				return platform.ErrInvalid
			}
			if e = platform.Participate(ctx, tx, a); e != nil {
				return e
			}
			next = "active"
		case "decline":
			if state != "invited" {
				return platform.ErrInvalid
			}
			next = "declined"
		case "leave":
			if state != "active" && state != "invited" {
				return platform.ErrInvalid
			}
			next = "left"
		default:
			return platform.ErrInvalid
		}
		if _, e = tx.Exec(ctx, `UPDATE messaging_participant SET state=$1::varchar,joined_at=CASE WHEN $1::varchar='active' THEN now() ELSE joined_at END WHERE conversation_id=$2 AND user_id=$3`, next, id, a.ID); e != nil {
			return e
		}
		if e = s.PruneObservers(ctx, tx, id); e != nil {
			return e
		}
		event := map[string]string{"accept": "messaging.invite_accepted", "decline": "messaging.invite_declined", "leave": "messaging.left"}[action]
		return platform.RecordAudit(ctx, tx, a, event, "", map[string]any{"conversation_id": id})
	})
}
func (s *Service) AddParticipant(ctx context.Context, a platform.Actor, id int64, username string) error {
	return s.changeParticipant(ctx, a, id, username, false)
}
func (s *Service) RemoveParticipant(ctx context.Context, a platform.Actor, id int64, username string) error {
	return s.changeParticipant(ctx, a, id, username, true)
}
func (s *Service) changeParticipant(ctx context.Context, a platform.Actor, id int64, username string, remove bool) error {
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		fresh, e := actor(ctx, tx, a.ID)
		if e != nil {
			return e
		}
		a = fresh
		if e = s.canWrite(ctx, tx, a, id); e != nil {
			return e
		}
		var kind, cohort, role string
		if e = tx.QueryRow(ctx, `SELECT c.kind,c.cohort,p.role FROM messaging_conversation c JOIN messaging_participant p ON p.conversation_id=c.id WHERE c.id=$1 AND p.user_id=$2 AND p.state='active' FOR UPDATE OF c`, id, a.ID).Scan(&kind, &cohort, &role); e != nil {
			return e
		}
		if role != "admin" {
			return platform.ErrInvalid
		}
		b, e := target(ctx, tx, username)
		if e != nil {
			return e
		}
		if b.ID == a.ID {
			return platform.ErrInvalid
		}
		var state, targetRole string
		e = tx.QueryRow(ctx, `SELECT state,role FROM messaging_participant WHERE conversation_id=$1 AND user_id=$2 FOR UPDATE`, id, b.ID).Scan(&state, &targetRole)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if remove {
			if e != nil || state != "active" && state != "invited" || targetRole == "guardian" {
				return platform.ErrInvalid
			}
			if _, e = tx.Exec(ctx, `UPDATE messaging_participant SET state='removed' WHERE conversation_id=$1 AND user_id=$2`, id, b.ID); e != nil {
				return e
			}
			if e = s.PruneObservers(ctx, tx, id); e != nil {
				return e
			}
			return platform.RecordAudit(ctx, tx, a, "messaging.participant_removed", fmt.Sprintf("accounts.user:%d", b.ID), map[string]any{"conversation_id": id})
		}
		if kind != "group" || b.Cohort != cohort {
			return platform.ErrInvalid
		}
		if e = pair(ctx, tx, a, b); e != nil {
			return e
		}
		if state == "active" || state == "invited" {
			return nil
		}
		var count int
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM messaging_participant WHERE conversation_id=$1 AND state IN('active','invited')`, id).Scan(&count); e != nil {
			return e
		}
		if count >= s.maxMembers() {
			return platform.ErrInvalid
		}
		if _, e = tx.Exec(ctx, `INSERT INTO messaging_participant(conversation_id,user_id,state,role,invited_by_id,created_at,joined_at,last_read_at) VALUES($1,$2,'invited','member',$3,now(),NULL,NULL) ON CONFLICT(conversation_id,user_id) DO UPDATE SET state='invited',invited_by_id=excluded.invited_by_id`, id, b.ID, a.ID); e != nil {
			return e
		}
		return platform.RecordAudit(ctx, tx, a, "messaging.participant_added", fmt.Sprintf("accounts.user:%d", b.ID), map[string]any{"conversation_id": id})
	})
}
func (s *Service) SetDisappearing(ctx context.Context, a platform.Actor, id int64, seconds int) error {
	if !map[int]bool{0: true, 300: true, 3600: true, 86400: true, 604800: true, 2592000: true}[seconds] {
		return platform.ErrInvalid
	}
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if e := s.canWrite(ctx, tx, a, id); e != nil {
			return e
		}
		var kind, role string
		if e := tx.QueryRow(ctx, `SELECT c.kind,p.role FROM messaging_conversation c JOIN messaging_participant p ON p.conversation_id=c.id WHERE c.id=$1 AND p.user_id=$2 AND p.state='active' FOR UPDATE OF c`, id, a.ID).Scan(&kind, &role); e != nil {
			return e
		}
		if kind == "group" && role != "admin" {
			return platform.ErrInvalid
		}
		if _, e := tx.Exec(ctx, `UPDATE messaging_conversation SET disappearing_seconds=$1,updated_at=now() WHERE id=$2`, seconds, id); e != nil {
			return e
		}
		return platform.RecordAudit(ctx, tx, a, "messaging.disappearing_set", "", map[string]any{"conversation_id": id, "seconds": seconds})
	})
}
func (s *Service) AddGuardian(ctx context.Context, a platform.Actor, id int64) error {
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `SELECT id FROM messaging_conversation WHERE id=$1 FOR UPDATE`, id); e != nil {
			return e
		}
		eligible, e := s.guardianEligible(ctx, tx, a, id)
		if e != nil {
			return e
		}
		if !eligible {
			return platform.ErrInvalid
		}
		var key bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM messaging_publickey WHERE user_id=$1 AND active)`, a.ID).Scan(&key); e != nil {
			return e
		}
		if !key {
			return platform.ErrInvalid
		}
		var state string
		e = tx.QueryRow(ctx, `SELECT state FROM messaging_participant WHERE conversation_id=$1 AND user_id=$2`, id, a.ID).Scan(&state)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if state == "active" {
			return nil
		}
		var count int
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM messaging_participant WHERE conversation_id=$1 AND state IN('active','invited')`, id).Scan(&count); e != nil {
			return e
		}
		if count >= s.maxMembers() {
			return platform.ErrInvalid
		}
		if _, e = tx.Exec(ctx, `INSERT INTO messaging_participant(conversation_id,user_id,state,role,invited_by_id,created_at,joined_at,last_read_at) VALUES($1,$2,'active','guardian',NULL,now(),now(),NULL) ON CONFLICT(conversation_id,user_id) DO UPDATE SET state='active',role='guardian',joined_at=now()`, id, a.ID); e != nil {
			return e
		}
		return platform.RecordAudit(ctx, tx, a, "messaging.guardian_observing", "", map[string]any{"conversation_id": id})
	})
}
