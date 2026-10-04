package safety

import (
	"context"
	"errors"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type actionState struct {
	ID, ContentType, TargetID  int64
	App, Model, Action, Reason string
	Expires, Lifted            *time.Time
}

func (s *Service) action(ctx context.Context, q platform.Querier, id int64, lock bool) (actionState, error) {
	var a actionState
	sql := `SELECT m.id,m.target_type_id,m.target_id,c.app_label,c.model,m.action,m.reason,m.expires_at,m.lifted_at FROM safety_moderationaction m JOIN django_content_type c ON c.id=m.target_type_id WHERE m.id=$1`
	if lock {
		sql += ` FOR UPDATE OF m`
	}
	err := q.QueryRow(ctx, sql, id).Scan(&a.ID, &a.ContentType, &a.TargetID, &a.App, &a.Model, &a.Action, &a.Reason, &a.Expires, &a.Lifted)
	return a, err
}
func (s *Service) FileAppeal(ctx context.Context, a platform.Actor, actionID int64, statement string) (int64, error) {
	statement = strings.TrimSpace(statement)
	if statement == "" || utf8.RuneCountInString(statement) > 2000 {
		return 0, platform.ErrInvalid
	}
	action, err := s.action(ctx, s.DB, actionID, false)
	if err != nil {
		return 0, err
	}
	target, err := s.ResolveTarget(ctx, s.DB, action.App, action.Model, action.TargetID)
	if err != nil {
		return 0, err
	}
	if target.Affected != a.ID {
		return 0, platform.ErrNotFound
	}
	allowed, err := s.allow(ctx, a, "appeal", 5, 24*time.Hour)
	if err != nil {
		return 0, err
	}
	if !allowed {
		return 0, ErrRate
	}
	var id int64
	err = platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT id FROM safety_moderationaction WHERE id=$1 FOR UPDATE`, actionID); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM safety_moderationappeal WHERE action_id=$1)`, actionID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return ErrConflict
		}
		if err := tx.QueryRow(ctx, `INSERT INTO safety_moderationappeal(statement,status,decision_notes,decided_at,created_at,action_id,appellant_id,decided_by_id) VALUES($1,'pending','',NULL,now(),$2,$3,NULL) RETURNING id`, statement, actionID, a.ID).Scan(&id); err != nil {
			return err
		}
		return platform.RecordAudit(ctx, tx, a, "moderation.appeal_filed", "safety.moderationaction:"+strconv.FormatInt(actionID, 10), nil)
	})
	return id, err
}

type Reversal struct{ Reactivated, LeftHiddenAuthorDeleted bool }

func (s *Service) reverse(ctx context.Context, tx pgx.Tx, a actionState) (Reversal, error) {
	var result Reversal
	target, err := s.ResolveTarget(ctx, tx, a.App, a.Model, a.TargetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if a.Model == "user" && a.App == "accounts" && (a.Action == "suspend" || a.Action == "timed_ban" || a.Action == "ban") {
		var active bool
		if err = tx.QueryRow(ctx, `SELECT is_active FROM accounts_user WHERE id=$1 FOR UPDATE`, a.TargetID).Scan(&active); err != nil {
			return result, err
		}
		var otherBan, otherTimed bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM safety_moderationaction WHERE target_type_id=$1 AND target_id=$2 AND id<>$3 AND lifted_at IS NULL AND action='ban'),EXISTS(SELECT 1 FROM safety_moderationaction WHERE target_type_id=$1 AND target_id=$2 AND id<>$3 AND lifted_at IS NULL AND action IN ('suspend','timed_ban') AND (expires_at IS NULL OR expires_at>$4))`, a.ContentType, a.TargetID, a.ID, s.Config.Now()).Scan(&otherBan, &otherTimed); err != nil {
			return result, err
		}
		if _, err = tx.Exec(ctx, `UPDATE safety_moderationaction SET lifted_at=$2 WHERE id=$1 AND lifted_at IS NULL`, a.ID, s.Config.Now()); err != nil {
			return result, err
		}
		if !otherBan && !otherTimed && !active {
			if _, err = tx.Exec(ctx, `UPDATE accounts_user SET is_active=true WHERE id=$1`, a.TargetID); err != nil {
				return result, err
			}
			result.Reactivated = true
		}
		if a.Action == "ban" && !otherBan {
			tag, err := tx.Exec(ctx, `DELETE FROM accounts_bannedidentity WHERE holder_hash IN (SELECT holder_hash FROM accounts_identitybinding WHERE user_id=$1)`, a.TargetID)
			if err != nil {
				return result, err
			}
			if tag.RowsAffected() > 0 {
				if err = platform.RecordAudit(ctx, tx, platform.Actor{ID: target.Affected}, "identity.ban_released", "accounts.user:"+strconv.FormatInt(target.Affected, 10), nil); err != nil {
					return result, err
				}
			}
		}
	} else if a.Action == "remove" && a.App == "social" && (a.Model == "post" || a.Model == "activity" || a.Model == "group") {
		var hidden, deleted bool
		column := "false"
		if a.Model == "post" {
			column = "is_author_deleted"
		}
		if err = tx.QueryRow(ctx, "SELECT is_hidden,"+column+" FROM "+pgx.Identifier{"social_" + a.Model}.Sanitize()+" WHERE id=$1 FOR UPDATE", a.TargetID).Scan(&hidden, &deleted); err != nil {
			return result, err
		}
		var other bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM safety_moderationaction WHERE target_type_id=$1 AND target_id=$2 AND id<>$3 AND action='remove' AND lifted_at IS NULL)`, a.ContentType, a.TargetID, a.ID).Scan(&other); err != nil {
			return result, err
		}
		if _, err = tx.Exec(ctx, `UPDATE safety_moderationaction SET lifted_at=$2 WHERE id=$1 AND lifted_at IS NULL`, a.ID, s.Config.Now()); err != nil {
			return result, err
		}
		if !other && hidden {
			if deleted {
				result.LeftHiddenAuthorDeleted = true
				if err = platform.RecordAudit(ctx, tx, platform.Actor{}, "moderation.reversal_left_hidden", a.App+"."+a.Model+":"+strconv.FormatInt(a.TargetID, 10), map[string]any{"action_id": a.ID, "reason": "author_deleted"}); err != nil {
					return result, err
				}
			} else {
				if _, err = tx.Exec(ctx, "UPDATE "+pgx.Identifier{"social_" + a.Model}.Sanitize()+" SET is_hidden=false WHERE id=$1", a.TargetID); err != nil {
					return result, err
				}
			}
		}
	}
	return result, nil
}

func (s *Service) ResolveAppeal(ctx context.Context, actor platform.Actor, id int64, grant bool, notes string) (Reversal, error) {
	if !actor.Moderator() {
		return Reversal{}, platform.ErrForbidden
	}
	var outcome Reversal
	err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		var status string
		var actionID int64
		if err := tx.QueryRow(ctx, `SELECT status,action_id FROM safety_moderationappeal WHERE id=$1 FOR UPDATE`, id).Scan(&status, &actionID); err != nil {
			return err
		}
		if status != "pending" {
			return ErrConflict
		}
		action, err := s.action(ctx, tx, actionID, true)
		if err != nil {
			return err
		}
		target, err := s.ResolveTarget(ctx, tx, action.App, action.Model, action.TargetID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		newStatus := "upheld"
		if grant {
			newStatus = "overturned"
			outcome, err = s.reverse(ctx, tx, action)
			if err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE safety_moderationappeal SET status=$2,decided_by_id=$3,decided_at=now(),decision_notes=$4 WHERE id=$1`, id, newStatus, actor.ID, notes); err != nil {
			return err
		}
		if err = platform.RecordAudit(ctx, tx, actor, "moderation.appeal_resolved", "safety.moderationaction:"+strconv.FormatInt(actionID, 10), map[string]bool{"granted": grant}); err != nil {
			return err
		}
		title, body := "Your appeal was reviewed", "We reviewed your contest of a moderation decision and the decision stands. Thank you for letting us take another look."
		if grant {
			title = "Your appeal succeeded"
			body = "We reviewed your contest of a moderation decision and reversed it. Any restriction from that decision has been removed."
			if outcome.LeftHiddenAuthorDeleted {
				body = "We reviewed your contest of a moderation decision and reversed it. Because you had deleted that message yourself, the message stays deleted — reversing our decision doesn't undo your own deletion."
			}
		}
		notify(ctx, tx, target.Affected, "moderation", title, body, "")
		return guardians(ctx, tx, target.Affected)
	})
	return outcome, err
}

func (s *Service) LiftSuspensions(ctx context.Context) (int, error) {
	rows, err := s.DB.Query(ctx, `SELECT id FROM safety_moderationaction WHERE action IN ('suspend','timed_ban') AND expires_at<=$1 AND lifted_at IS NULL ORDER BY id`, s.Config.Now())
	if err != nil {
		return 0, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, id := range ids {
		reactivated := int64(0)
		err = platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
			var locked int64
			err := tx.QueryRow(ctx, `SELECT id FROM safety_moderationaction WHERE id=$1 AND lifted_at IS NULL FOR UPDATE SKIP LOCKED`, id).Scan(&locked)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			action, err := s.action(ctx, tx, id, false)
			if err != nil {
				return err
			}
			if action.App == "accounts" && action.Model == "user" {
				var active bool
				err = tx.QueryRow(ctx, `SELECT is_active FROM accounts_user WHERE id=$1 FOR UPDATE`, action.TargetID).Scan(&active)
				if errors.Is(err, pgx.ErrNoRows) {
					_, err = tx.Exec(ctx, `UPDATE safety_moderationaction SET lifted_at=$2 WHERE id=$1`, id, s.Config.Now())
					return err
				}
				if err != nil {
					return err
				}
				var blocked bool
				if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM safety_moderationaction WHERE target_type_id=$1 AND target_id=$2 AND lifted_at IS NULL AND (action='ban' OR (action IN ('suspend','timed_ban') AND (expires_at IS NULL OR expires_at>$3))))`, action.ContentType, action.TargetID, s.Config.Now()).Scan(&blocked); err != nil {
					return err
				}
				if !blocked && !active {
					if _, err = tx.Exec(ctx, `UPDATE accounts_user SET is_active=true WHERE id=$1`, action.TargetID); err != nil {
						return err
					}
					if err = platform.RecordAudit(ctx, tx, platform.Actor{}, "moderation.suspension_lifted", "accounts.user:"+strconv.FormatInt(action.TargetID, 10), nil); err != nil {
						return err
					}
					reactivated = action.TargetID
				}
			}
			_, err = tx.Exec(ctx, `UPDATE safety_moderationaction SET lifted_at=$2 WHERE id=$1`, id, s.Config.Now())
			return err
		})
		if err != nil {
			return count, err
		}
		if reactivated > 0 {
			count++
			_ = platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
				notify(ctx, tx, reactivated, "moderation", "Your suspension has ended", "Your account is active again and you can take part in activities. Thanks for your patience.", "")
				return nil
			})
		}
	}
	return count, nil
}
