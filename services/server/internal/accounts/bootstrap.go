package accounts

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

var bootstrapUsername = regexp.MustCompile(`^[\pL\pN_.@+\-]{3,150}$`)

// BootstrapAdministrator is an explicit operator-only account creation policy.
// It is never registered on HTTP, never called at startup and cannot promote an
// existing account. Platform administration grants no identity or cohort proof.
func (s *Service) BootstrapAdministrator(ctx context.Context, username, password string) (platform.Actor, error) {
	if !bootstrapUsername.MatchString(username) || strings.TrimSpace(username) != username {
		return platform.Actor{}, errors.New("username is invalid")
	}
	if strings.ContainsAny(password, "\x00\r\n") {
		return platform.Actor{}, errors.New("password is invalid")
	}
	hash, err := authcore.HashPassword(ctx, password)
	if err != nil {
		return platform.Actor{}, errors.New("password is invalid or hashing unavailable")
	}
	if s == nil || s.DB == nil {
		return platform.Actor{}, errors.New("administrator bootstrap unavailable")
	}
	var actor platform.Actor
	err = platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		// Prevent two operator bootstraps from shadowing each other through casing;
		// ordinary account uniqueness remains protected by its database constraint.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "administrator-bootstrap:"+strings.ToLower(username)); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_user WHERE lower(username)=lower($1))`, username).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return authcore.ErrConflict
		}
		created, err := insertPending(ctx, tx, authcore.User{Username: username, Name: ""}, hash, "password")
		if err != nil {
			return err
		}
		id, err := strconv.ParseInt(created.ID, 10, 64)
		if err != nil {
			return err
		}
		// The ID was created inside this transaction. No conflict UPDATE, user lookup
		// or age/consent edit can redirect this privilege change to an existing user.
		if _, err = tx.Exec(ctx, `UPDATE accounts_user SET is_active=true,is_staff=true,is_superuser=true,role='admin' WHERE id=$1 AND age_band='unknown' AND cohort='unassigned' AND NOT is_identity_verified`, id); err != nil {
			return err
		}
		actor, err = s.actor(ctx, tx, id)
		if err != nil {
			return err
		}
		if !actor.IsStaff || !actor.IsSuperuser || actor.Role != "admin" || actor.IdentityVerified || actor.AgeBand != "unknown" || actor.Cohort != "unassigned" {
			return errors.New("administrator bootstrap policy failed")
		}
		return platform.RecordAudit(ctx, tx, platform.Actor{}, "accounts.administrator_bootstrapped", "accounts.user:"+created.ID, map[string]any{"is_staff": true, "is_superuser": true, "role": "admin", "identity_verified": false, "cohort": "unassigned", "age_assurance_granted": false})
	})
	if errors.Is(err, authcore.ErrConflict) {
		return platform.Actor{}, authcore.ErrConflict
	}
	if err != nil {
		return platform.Actor{}, errors.New("administrator bootstrap unavailable")
	}
	return actor, nil
}
