package accounts

import (
	"context"
	"errors"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"strconv"
	"strings"
)

func (s *Service) Erase(ctx context.Context, actor, target platform.Actor) error {
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		// Lock the account before changing guardianships, memberships or sessions.
		// The guardian check follows the lock so a concurrent adult re-verification
		// (which revokes the link under the same row lock) cannot be raced.
		if _, err := tx.Exec(ctx, `SELECT id FROM accounts_user WHERE id=$1 FOR UPDATE`, target.ID); err != nil {
			return err
		}
		if actor.ID != target.ID {
			yes, err := s.isGuardian(ctx, tx, actor.ID, target.ID)
			if err != nil {
				return err
			}
			if !yes {
				return platform.ErrForbidden
			}
		}
		rows, err := tx.Query(ctx, `SELECT ward_id FROM accounts_guardianrelationship WHERE guardian_id=$1 AND status='active'`, target.ID)
		if err != nil {
			return err
		}
		wards := []int64{}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			wards = append(wards, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE accounts_parentalconsent SET status='revoked',revoked_at=now(),updated_at=now() WHERE guardian_identifier=$1 AND status='active'`, target.PublicID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE accounts_guardianrelationship SET status='revoked',updated_at=now() WHERE guardian_id=$1`, target.ID); err != nil {
			return err
		}
		for _, id := range wards {
			ward, err := s.actor(ctx, tx, id)
			if err != nil {
				return err
			}
			can := platform.Participate(ctx, tx, ward)
			if can != nil {
				if !errors.Is(can, platform.ErrForbidden) {
					return can
				}
				if err = evictParticipation(ctx, tx, ward, "guardian_revoked"); err != nil {
					return err
				}
			}
		}
		if err = evictParticipation(ctx, tx, target, "account_erased"); err != nil {
			return err
		}
		// Authored ciphertext is erased rather than merely anonymizing its sender.
		messageRows, err := tx.Query(ctx, `SELECT id FROM messaging_message WHERE sender_id=$1`, target.ID)
		if err != nil {
			return err
		}
		messages := []int64{}
		for messageRows.Next() {
			var id int64
			if err = messageRows.Scan(&id); err != nil {
				messageRows.Close()
				return err
			}
			messages = append(messages, id)
		}
		err = messageRows.Err()
		messageRows.Close()
		if err != nil {
			return err
		}
		if err = deleteRows(ctx, tx, "messaging_message", messages, map[string]bool{}); err != nil {
			return err
		}
		groups, err := jsonObjects(ctx, tx, `SELECT jsonb_build_object('id',id,'cohort',cohort,'is_hidden',is_hidden,'status',status) FROM social_group WHERE owner_id=$1`, target.ID)
		if err != nil {
			return err
		}
		for _, raw := range groups {
			var g struct {
				ID       int64
				Cohort   string
				IsHidden bool `json:"is_hidden"`
				Status   string
			}
			if err = jsonDecode(raw, &g); err != nil {
				return err
			}
			if err = platform.RecordAudit(ctx, tx, actor, "group.owner_erased", "social.group:"+strconv.FormatInt(g.ID, 10), map[string]any{"cohort": g.Cohort, "is_hidden": g.IsHidden, "status": g.Status}); err != nil {
				return err
			}
		}
		if err = platform.RecordAudit(ctx, tx, actor, "account.erased", "", map[string]string{"erased_public_id": target.PublicID}); err != nil {
			return err
		}
		// Execute reviewed ORM deletion policies natively; ordinary deletes invoke
		// media outbox triggers. Audit actor_ref, chain fields and ban ledgers remain.
		return deleteRows(ctx, tx, "accounts_user", []int64{target.ID}, map[string]bool{})
	})
}

type relation struct{ Schema, Table, Column, Delete string }

func deleteRows(ctx context.Context, tx pgx.Tx, table string, ids []int64, seen map[string]bool) error {
	if len(ids) == 0 {
		return nil
	}
	if seen[table] {
		return errors.New("unexpected cascading relation cycle")
	}
	seen[table] = true
	defer delete(seen, table)
	rows, err := tx.Query(ctx, `SELECT n.nspname,t.relname,a.attname,c.confdeltype::text FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid JOIN pg_namespace n ON n.oid=t.relnamespace JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=c.conkey[1] WHERE c.contype='f' AND c.confrelid=to_regclass($1) AND array_length(c.conkey,1)=1 ORDER BY t.relname,a.attname`, table)
	if err != nil {
		return err
	}
	refs := []relation{}
	for rows.Next() {
		var ref relation
		if err = rows.Scan(&ref.Schema, &ref.Table, &ref.Column, &ref.Delete); err != nil {
			rows.Close()
			return err
		}
		refs = append(refs, ref)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, ref := range refs {
		// Go-native FK actions are enforced by PostgreSQL itself.
		if ref.Delete == "c" || ref.Delete == "n" {
			continue
		}
		policy := deletionPolicy[ref.Table+"."+ref.Column]
		if policy == "" {
			return errors.New("unreviewed account erasure relation")
		}
		quoted := pgx.Identifier{ref.Schema, ref.Table}.Sanitize()
		var currentSchema string
		if err = tx.QueryRow(ctx, `SELECT current_schema()`).Scan(&currentSchema); err != nil {
			return err
		}
		if ref.Schema != currentSchema {
			return errors.New("unreviewed cross-schema erasure relation")
		}
		column := pgx.Identifier{ref.Column}.Sanitize()
		switch policy {
		case "SET_NULL":
			if _, err = tx.Exec(ctx, "UPDATE "+quoted+" SET "+column+"=NULL WHERE "+column+"=ANY($1)", ids); err != nil {
				return err
			}
		case "CASCADE":
			if ref.Table == "authtoken_token" {
				if _, err = tx.Exec(ctx, "DELETE FROM "+quoted+" WHERE "+column+"=ANY($1)", ids); err != nil {
					return err
				}
				continue
			}
			key, err := primaryColumn(ctx, tx, ref.Table)
			if err != nil {
				return err
			}
			children, err := tx.Query(ctx, "SELECT "+pgx.Identifier{key}.Sanitize()+" FROM "+quoted+" WHERE "+column+"=ANY($1)", ids)
			if err != nil {
				return err
			}
			childIDs := []int64{}
			for children.Next() {
				var id int64
				if err = children.Scan(&id); err != nil {
					children.Close()
					return err
				}
				childIDs = append(childIDs, id)
			}
			err = children.Err()
			children.Close()
			if err != nil {
				return err
			}
			if err = deleteRows(ctx, tx, ref.Table, childIDs, seen); err != nil {
				return err
			}
		case "PROTECT":
			var present bool
			if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+quoted+" WHERE "+column+"=ANY($1))", ids).Scan(&present); err != nil {
				return err
			}
			if present {
				return platform.ErrForbidden
			}
		default:
			return errors.New("unsupported account erasure policy")
		}
	}
	// table is selected from our root or PostgreSQL metadata, never HTTP input.
	if strings.ContainsAny(table, ".\x00") {
		return platform.ErrInvalid
	}
	key, err := primaryColumn(ctx, tx, table)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "DELETE FROM "+pgx.Identifier{table}.Sanitize()+" WHERE "+pgx.Identifier{key}.Sanitize()+"=ANY($1)", ids)
	return err
}

func primaryColumn(ctx context.Context, tx pgx.Tx, table string) (string, error) {
	var name string
	err := tx.QueryRow(ctx, `SELECT a.attname FROM pg_constraint c JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=c.conkey[1] WHERE c.contype='p' AND c.conrelid=to_regclass($1) AND array_length(c.conkey,1)=1`, table).Scan(&name)
	return name, err
}
