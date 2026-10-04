package accounts

import (
	"context"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNativeDemoIdentityPolicyProductionRefusalAndExistingAccountPreservation(t *testing.T) {
	db := testdb.New(t, *accountTestDSN, func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	ctx := context.Background()
	s := New(db, nil, "synthetic-development-fixture-secret", Config{})
	if _, _, err := s.EnsureDemoAccount(ctx, "fixture-adult", "Fixture adult", "adult", true); err == nil {
		t.Fatal("production demo account accepted")
	}
	existing := testdb.Actor(t, db, "real-existing-fixture", "adult")
	s.Config.DemoEnabled = true
	if _, _, err := s.EnsureDemoAccount(ctx, existing.Username, "Changed name", "under_16", false); err == nil {
		t.Fatal("existing account modified")
	}
	var band, display string
	var staff bool
	if err := db.QueryRow(ctx, `SELECT age_band,display_name,is_staff FROM accounts_user WHERE id=$1`, existing.ID).Scan(&band, &display, &staff); err != nil || band != "adult" || display != existing.DisplayName || staff {
		t.Fatal("account preservation failed", err)
	}
	adult, created, err := s.EnsureDemoAccount(ctx, "fixture-development-adult", "Development adult", "adult", false)
	if err != nil || !created {
		t.Fatal(created, err)
	}
	again, created, err := s.EnsureDemoAccount(ctx, adult.Username, "Ignored replacement", "adult", false)
	if err != nil || created || again.ID != adult.ID || again.DisplayName != adult.DisplayName {
		t.Fatal("idempotent fixture mutation", err)
	}
	child, _, err := s.EnsureDemoAccount(ctx, "fixture-development-child", "Development child", "under_16", false)
	if err != nil {
		t.Fatal(err)
	}
	if err = platform.Participate(ctx, db, child); err == nil {
		t.Fatal("child admitted without consent")
	}
	if err = s.DemoLinkAndConsent(ctx, adult, child); err != nil {
		t.Fatal(err)
	}
	if err = platform.Participate(ctx, db, child); err != nil {
		t.Fatal("fixture consent missing", err)
	}
	if _, err = db.Exec(ctx, `UPDATE accounts_guardianrelationship SET status='revoked' WHERE guardian_id=$1 AND ward_id=$2`, adult.ID, child.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.DemoLinkAndConsent(ctx, adult, child); err == nil {
		t.Fatal("revoked relationship reactivated")
	}
	s.Config.DemoEnabled = false
	if err = s.DemoLinkAndConsent(ctx, adult, child); err == nil {
		t.Fatal("production consent accepted")
	}
}
