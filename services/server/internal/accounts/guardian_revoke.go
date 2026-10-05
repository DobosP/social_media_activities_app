package accounts

import (
	"context"
	"errors"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"strconv"
)

type GuardianMessaging interface {
	PruneObservers(context.Context, pgx.Tx, int64) error
	RemoveUser(context.Context, pgx.Tx, int64, string) error
}

// RevokeGuardian removes only the actor's active link and corresponding grant.
// A co-guardian's current consent and observer authorization stay independent.
func (s *Service) RevokeGuardian(ctx context.Context, a platform.Actor, wardID int64, crypto GuardianMessaging) error {
	if crypto == nil {
		return platform.ErrInvalid
	}
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		fresh, err := s.actor(ctx, tx, a.ID)
		if err != nil {
			return err
		}
		if !fresh.IsActive {
			return platform.ErrForbidden
		}
		if _, err = tx.Exec(ctx, `SELECT id FROM accounts_user WHERE id=$1 FOR UPDATE`, wardID); err != nil {
			return err
		}
		var relationship int64
		if err = tx.QueryRow(ctx, `SELECT id FROM accounts_guardianrelationship WHERE guardian_id=$1 AND ward_id=$2 AND status='active' FOR UPDATE`, fresh.ID, wardID).Scan(&relationship); err != nil {
			return platform.ErrForbidden
		}
		ward, err := s.actor(ctx, tx, wardID)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE accounts_guardianrelationship SET status='revoked',updated_at=now() WHERE id=$1`, relationship); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE accounts_parentalconsent SET status='revoked',revoked_at=now(),updated_at=now() WHERE minor_id=$1 AND guardian_identifier=$2 AND status='active'`, wardID, fresh.PublicID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT DISTINCT conversation_id FROM messaging_participant WHERE user_id IN($1,$2) AND state='active' ORDER BY conversation_id`, fresh.ID, wardID)
		if err != nil {
			return err
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err = crypto.PruneObservers(ctx, tx, id); err != nil {
				return err
			}
		}
		can := platform.Participate(ctx, tx, ward)
		if can != nil {
			if !errors.Is(can, platform.ErrForbidden) {
				return can
			}
			if err = crypto.RemoveUser(ctx, tx, ward.ID, "guardian_revoked"); err != nil {
				return err
			}
			if err = evictParticipation(ctx, tx, ward, "guardian_revoked"); err != nil {
				return err
			}
		}
		return platform.RecordAudit(ctx, tx, fresh, "guardian.revoked", "accounts.user:"+strconv.FormatInt(wardID, 10), nil)
	})
}

// revokeAdultWard ends every guardian link and parental consent held over a user
// whose age re-verification placed them in the adult cohort (ADR-0045). Each link
// is audited with the ward as actor. Observer seats are pruned by the cohort-change
// eviction that follows in the same AgeVerify transaction.
func revokeAdultWard(ctx context.Context, tx pgx.Tx, ward platform.Actor) error {
	// Same user-before-relationship lock order as RevokeGuardian and Erase. NO KEY
	// UPDATE is the lock the cohort UPDATE takes anyway and still serializes with
	// their FOR UPDATE, without blocking unrelated FK inserts on this account.
	if _, err := tx.Exec(ctx, `SELECT id FROM accounts_user WHERE id=$1 FOR NO KEY UPDATE`, ward.ID); err != nil {
		return err
	}
	// Consents before relationships, the order a guardian's own Erase uses, so a
	// concurrent erase and re-verification cannot deadlock on the pair's rows.
	if _, err := tx.Exec(ctx, `UPDATE accounts_parentalconsent SET status='revoked',revoked_at=now(),updated_at=now() WHERE minor_id=$1 AND status='active'`, ward.ID); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `UPDATE accounts_guardianrelationship SET status='revoked',updated_at=now() WHERE ward_id=$1 AND status='active' RETURNING guardian_id`, ward.ID)
	if err != nil {
		return err
	}
	guardians := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		guardians = append(guardians, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range guardians {
		if err = platform.RecordAudit(ctx, tx, ward, "guardian.revoked", "accounts.user:"+strconv.FormatInt(id, 10), map[string]string{"reason": "ward_adult"}); err != nil {
			return err
		}
	}
	return nil
}
