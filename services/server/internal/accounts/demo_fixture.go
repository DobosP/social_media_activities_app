package accounts

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

// EnsureDemoAccount is reachable only from an explicitly enabled local
// development command. Existing real accounts are never reverified, promoted,
// renamed or assigned a cohort. Existing marked fixtures are returned unchanged.
func (s *Service) EnsureDemoAccount(ctx context.Context, username, display, band string, staff bool) (platform.Actor, bool, error) {
	if !s.Config.DemoEnabled {
		return platform.Actor{}, false, platform.ErrForbidden
	}
	if username == "" || utf8.RuneCountInString(username) > 150 || utf8.RuneCountInString(display) > 120 || strings.ContainsAny(username, "\r\n\x00") {
		return platform.Actor{}, false, platform.ErrInvalid
	}
	cohort := map[string]string{"adult": "adult", "16_17": "teen", "under_16": "child"}[band]
	if cohort == "" || staff && band != "adult" {
		return platform.Actor{}, false, platform.ErrInvalid
	}
	var a platform.Actor
	created := false
	err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "development-fixture-account:"+username); err != nil {
			return err
		}
		var id int64
		err := tx.QueryRow(ctx, `SELECT id FROM accounts_user WHERE username=$1 FOR UPDATE`, username).Scan(&id)
		if err == nil {
			a, err = s.actor(ctx, tx, id)
			if err != nil {
				return err
			}
			var fixture bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_ageassurance WHERE user_id=$1 AND provider='dev' AND raw@>'{"demo_fixture":true}'::jsonb)`, id).Scan(&fixture); err != nil {
				return err
			}
			if !fixture || a.AgeBand != band || a.Cohort != cohort || a.IsStaff != staff {
				return platform.ErrForbidden
			}
			return nil
		}
		if err != pgx.ErrNoRows {
			return err
		}
		password := "demo12345"
		if username == "admin" {
			password = "admin12345"
		}
		if username == "ana.demo" || username == "dan.demo" || username == "staff.demo" {
			password = "parola-demo-1"
		}
		if username == "demo_organizer" {
			password = "Testpass!123"
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		salt := rand.Text()
		key, err := pbkdf2.Key(sha256.New, password, []byte(salt), 600000, 32)
		encoded := ""
		if err == nil {
			encoded = fmt.Sprintf("pbkdf2_sha256$600000$%s$%s", salt, base64.StdEncoding.EncodeToString(key))
		}
		if err != nil {
			return err
		}
		role := "user"
		if staff {
			role = "admin"
		}
		if err = tx.QueryRow(ctx, `INSERT INTO accounts_user(password,last_login,is_superuser,public_id,username,display_name,age_band,cohort,is_identity_verified,identity_verified_at,role,is_active,is_staff,date_joined) VALUES($1,NULL,$2,gen_random_uuid(),$3,$4,$5,$6,true,now(),$7,true,$2,now()) RETURNING id`, encoded, staff, username, display, band, cohort, role).Scan(&id); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO accounts_ageassurance(user_id,provider,method,age_band,verified_at,expires_at,raw,reverify_notice) VALUES($1,'dev','development-fixture',$2,now(),NULL,'{"demo_fixture":true}'::jsonb,'')`, id, band); err != nil {
			return err
		}
		a, err = s.actor(ctx, tx, id)
		if err != nil {
			return err
		}
		if err = RefreshAvatarFingerprint(ctx, tx, a); err != nil {
			return err
		}
		created = true
		return platform.RecordAudit(ctx, tx, a, "demo.account_created", fmt.Sprintf("accounts.user:%d", id), map[string]bool{"fixture": true})
	})
	return a, created, err
}
func demoFixtureUser(ctx context.Context, q platform.Querier, id int64) error {
	var fixture bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_ageassurance WHERE user_id=$1 AND provider='dev' AND raw@>'{"demo_fixture":true}'::jsonb)`, id).Scan(&fixture); err != nil {
		return err
	}
	if !fixture {
		return platform.ErrForbidden
	}
	return nil
}

// DemoLinkAndConsent seeds a transparent fixture guardian with a real consent
// record. It cannot relink or reactivate a preexisting/revoked relationship.
func (s *Service) DemoLinkAndConsent(ctx context.Context, guardian, ward platform.Actor) error {
	if !s.Config.DemoEnabled {
		return platform.ErrForbidden
	}
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		for _, id := range []int64{min(guardian.ID, ward.ID), max(guardian.ID, ward.ID)} {
			if err := demoFixtureUser(ctx, tx, id); err != nil {
				return err
			}
			var locked int64
			if err := tx.QueryRow(ctx, `SELECT id FROM accounts_user WHERE id=$1 FOR UPDATE`, id).Scan(&locked); err != nil {
				return err
			}
		}
		g, err := s.actor(ctx, tx, guardian.ID)
		if err != nil {
			return err
		}
		w, err := s.actor(ctx, tx, ward.ID)
		if err != nil {
			return err
		}
		if g.ID == w.ID || g.Cohort != "adult" || w.Cohort != "child" || w.AgeBand != "under_16" || !w.IsActive || !w.IdentityVerified {
			return platform.ErrForbidden
		}
		if err = platform.Participate(ctx, tx, g); err != nil {
			return err
		}
		blocked, err := platform.Blocked(ctx, tx, g.ID, w.ID)
		if err != nil {
			return err
		}
		if blocked {
			return platform.ErrForbidden
		}
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_guardianrelationship WHERE guardian_id=$1 AND ward_id=$2)`, g.ID, w.ID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return platform.ErrInvalid
		}
		var consent int64
		expires := s.Config.Now().Add(s.Config.ConsentValidity)
		if err = tx.QueryRow(ctx, `INSERT INTO accounts_parentalconsent(minor_id,guardian_identifier,status,scope,granted_at,expires_at,revoked_at,renewal_notice,created_at,updated_at) VALUES($1,$2,'active','',now(),$3,NULL,'',now(),now()) RETURNING id`, w.ID, g.PublicID, expires).Scan(&consent); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,relationship,status,consent_id,created_at,updated_at) VALUES($1,$2,'parent','active',$3,now(),now())`, g.ID, w.ID, consent); err != nil {
			return err
		}
		return platform.RecordAudit(ctx, tx, g, "demo.guardian_consent_created", fmt.Sprintf("accounts.user:%d", w.ID), map[string]bool{"fixture": true})
	})
}
