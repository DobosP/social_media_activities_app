package admin

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

// PermissionLevel names complete, reviewed capabilities. Legacy Django groups
// and individual permissions are not native authorization inputs.
type PermissionLevel string

const (
	PermissionUser          PermissionLevel = "user"
	PermissionModerator     PermissionLevel = "moderator"
	PermissionOperator      PermissionLevel = "operator"
	PermissionAdministrator PermissionLevel = "administrator"
)

type PermissionState struct {
	Role        string `json:"role"`
	IsStaff     bool   `json:"is_staff"`
	IsSuperuser bool   `json:"is_superuser"`
}

func permissionState(level PermissionLevel) (PermissionState, bool) {
	switch level {
	case PermissionUser:
		return PermissionState{Role: "user"}, true
	case PermissionModerator:
		return PermissionState{Role: "moderator"}, true
	case PermissionOperator:
		return PermissionState{Role: "user", IsStaff: true}, true
	case PermissionAdministrator:
		return PermissionState{Role: "admin", IsStaff: true, IsSuperuser: true}, true
	default:
		return PermissionState{}, false
	}
}

func permissions(a platform.Actor) PermissionState {
	return PermissionState{a.Role, a.IsStaff, a.IsSuperuser}
}

func (p PermissionState) capabilities() uint8 {
	var bits uint8
	if p.Role == "moderator" || p.Role == "admin" || p.IsStaff || p.IsSuperuser {
		bits |= 1 // moderation
	}
	if p.IsStaff {
		bits |= 2 // operator model console
	}
	if p.Role == "admin" || p.IsSuperuser {
		bits |= 4 // native administrator domain operations
	}
	if p.IsSuperuser {
		bits |= 8 // superuser authority
	}
	return bits
}

// The unassigned case preserves the explicit native administrator bootstrap:
// that operator remains unable to participate without ordinary age assurance.
const permissionAdultPredicate = `cohort='adult' AND age_band='adult' AND is_identity_verified AND
 COALESCE((SELECT age_band='adult' AND (expires_at IS NULL OR expires_at>now())
  FROM accounts_ageassurance WHERE user_id=accounts_user.id ORDER BY verified_at DESC,id DESC LIMIT 1),false)`

const permissionManagerPredicate = `is_active AND is_staff AND is_superuser AND role='admin' AND
 ((` + permissionAdultPredicate + `) OR
  (cohort='unassigned' AND age_band='unknown' AND NOT is_identity_verified AND
   EXISTS(SELECT 1 FROM safety_auditlog WHERE event='accounts.administrator_bootstrapped'
    AND target_ref='accounts.user:'||accounts_user.id::text)))`

func permissionManager(a platform.Actor) bool {
	return a.IsActive && a.IsStaff && a.IsSuperuser && a.Role == "admin" &&
		((a.Cohort == "adult" && a.AgeBand == "adult" && a.IdentityVerified) ||
			(a.Cohort == "unassigned" && a.AgeBand == "unknown" && !a.IdentityVerified))
}

// PermissionGate is only a presentation gate. ChangePermissions reloads and
// locks authority itself; a captured request actor never authorizes a change.
func (s *Service) PermissionGate(ctx context.Context, a platform.Actor) error {
	if a.ID < 1 {
		return platform.ErrForbidden
	}
	var allowed bool
	if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_user WHERE id=$1 AND `+permissionManagerPredicate+`)`, a.ID).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return platform.ErrForbidden
	}
	return nil
}

// ChangePermissions changes only platform capability fields, never identity,
// activity/cohort membership, consent, account activity or authentication data.
// Revocations can repair ineligible legacy accounts; grants require a current
// verified adult. Every effective change forces fresh authentication.
func (s *Service) ChangePermissions(ctx context.Context, actor platform.Actor, targetID int64, level PermissionLevel, reason string) error {
	after, valid := permissionState(level)
	reason = strings.TrimSpace(reason)
	if !valid || actor.ID < 1 || targetID < 1 || reason == "" || utf8.RuneCountInString(reason) > 2000 || strings.ContainsRune(reason, '\x00') {
		return platform.ErrInvalid
	}
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		// Serialize privilege changes, including simultaneous removal of distinct
		// administrators. Row locks also order changes against sanctions/erasure.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('admin-permission-changes',0))`); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id,role,is_staff,is_superuser,is_active,age_band,cohort,is_identity_verified FROM accounts_user WHERE id=$1 OR id=$2 ORDER BY id FOR UPDATE`, actor.ID, targetID)
		if err != nil {
			return err
		}
		current := map[int64]platform.Actor{}
		for rows.Next() {
			var a platform.Actor
			if err = rows.Scan(&a.ID, &a.Role, &a.IsStaff, &a.IsSuperuser, &a.IsActive, &a.AgeBand, &a.Cohort, &a.IdentityVerified); err != nil {
				rows.Close()
				return err
			}
			current[a.ID] = a
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		fresh := current[actor.ID]
		if !permissionManager(fresh) {
			return platform.ErrForbidden
		}
		var authorized bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_user WHERE id=$1 AND `+permissionManagerPredicate+`)`, fresh.ID).Scan(&authorized); err != nil {
			return err
		}
		if !authorized {
			return platform.ErrForbidden
		}
		target, exists := current[targetID]
		if !exists {
			return platform.ErrNotFound
		}
		before := permissions(target)
		if before == after {
			return nil // no-op cannot revoke tokens or manufacture an audit event
		}
		grant := after.capabilities() & ^before.capabilities() != 0
		if grant {
			if targetID == fresh.ID || !target.IsActive || !target.IdentityVerified || target.AgeBand != "adult" || target.Cohort != "adult" {
				return platform.ErrForbidden
			}
			if err := platform.Participate(ctx, tx, target); err != nil {
				return err
			}
			var eligible bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_user WHERE id=$1 AND is_active AND `+permissionAdultPredicate+`)`, targetID).Scan(&eligible); err != nil {
				return err
			}
			if !eligible {
				return platform.ErrForbidden
			}
		}
		proposed := target
		proposed.Role, proposed.IsStaff, proposed.IsSuperuser = after.Role, after.IsStaff, after.IsSuperuser
		if permissionManager(target) && !permissionManager(proposed) {
			// Lock remaining managers so a simultaneous account sanction cannot
			// make the count stale before this permission transaction commits.
			rows, err := tx.Query(ctx, `SELECT id FROM accounts_user WHERE id<>$1 AND `+permissionManagerPredicate+` ORDER BY id FOR UPDATE`, targetID)
			if err != nil {
				return err
			}
			remaining := rows.Next()
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			if !remaining {
				return platform.ErrForbidden
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE accounts_user SET role=$2,is_staff=$3,is_superuser=$4 WHERE id=$1`, targetID, after.Role, after.IsStaff, after.IsSuperuser); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM accounts_go_session WHERE user_id=$1`, targetID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM authtoken_token WHERE user_id=$1`, targetID); err != nil {
			return err
		}
		return platform.RecordAudit(ctx, tx, fresh, "admin.permissions_changed", fmt.Sprintf("accounts.user:%d", targetID), map[string]any{"before": before, "after": after, "reason": reason, "authentication_revoked": true})
	})
}
