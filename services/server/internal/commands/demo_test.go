package commands

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/jobs"
	"github.com/DobosP/social_media_activities_app/services/server/internal/messaging"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/recommendations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

func seedFixture(t *testing.T) (*Service, *jobs.Runner) {
	t.Helper()
	ctx := context.Background()
	db := testdb.New(t, *commandsDSN, func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	account := accounts.New(db, nil, "synthetic-seed-fixture-secret", accounts.Config{DemoEnabled: true})
	soc := social.New(db, platform.RecordAudit)
	rec := recommendations.New(db, catalog.New(db), soc)
	soc.AfterActivitySave = rec.RecomputeEmbeddingTx
	soc.Avatar = accounts.Avatar
	msg := messaging.New(db, platform.CursorCodec{Key: []byte("synthetic-fixture-key-32bytes-here")})
	if err := messaging.EnsureSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	cfg := jobs.DefaultConfig()
	cfg.Social = soc
	cfg.Accounts = account
	cfg.Now = func() time.Time { return time.Date(2026, 10, 4, 6, 0, 0, 0, time.UTC) }
	r := jobs.New(db, cfg)
	root, _ := filepath.Abs("../../../..")
	scratch := t.TempDir()
	if err := os.MkdirAll(scratch, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(scratch) })
	c := Config{Recommendations: rec, DemoEnabled: true, SeedRoot: filepath.Join(root, "db"), Scratch: scratch, SeedAccount: account.EnsureDemoAccount, SeedConsent: account.DemoLinkAndConsent, Messaging: msg, SeedComplete: func(ctx context.Context, a platform.Actor, id int64) error {
		return soc.CompleteDemoActivity(ctx, a, id, true)
	}, SeedApproveVenue: func(ctx context.Context, a platform.Actor, id int64) error {
		return soc.ApproveDemoVenue(ctx, a, id, true)
	}}
	c.SeedUploadCover = func(ctx context.Context, a platform.Actor, id int64, path, alt string) error {
		if a.Cohort != "adult" || alt == "" {
			return platform.ErrForbidden
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		im, err := png.Decode(f)
		if err != nil {
			return err
		}
		if im.Bounds().Dx() != 1200 || im.Bounds().Dy() != 760 {
			return platform.ErrInvalid
		}
		var owner int64
		if err = db.QueryRow(ctx, `SELECT owner_id FROM social_activity WHERE id=$1`, id).Scan(&owner); err != nil {
			return err
		}
		if owner != a.ID {
			return platform.ErrForbidden
		}
		return nil
	}
	if err := Install(r, c); err != nil {
		t.Fatal(err)
	}
	return &Service{r, c}, r
}
func TestNativeDevelopmentCommandsRefuseProductionAndForceBypass(t *testing.T) {
	s := &Service{}
	for _, name := range []string{"seed_demo_users", "seed_demo_data", "seed_browse_demo", "generate_demo_events", "seed_mobile_card_demo", "load_roedu_seed"} {
		var err error
		switch name {
		case "seed_demo_users":
			_, err = s.seedDemoUsers(context.Background(), args(map[string]any{"force": true}))
		case "seed_demo_data":
			_, err = s.seedWorld(context.Background(), nil)
		case "seed_browse_demo":
			_, err = s.seedBrowse(context.Background(), nil)
		case "generate_demo_events":
			_, err = s.generateDemoEvents(context.Background(), args(map[string]any{"force": true}))
		case "seed_mobile_card_demo":
			_, err = s.seedMobile(context.Background(), nil)
		case "load_roedu_seed":
			_, err = s.loadSeed(context.Background(), nil)
		}
		if err == nil {
			t.Fatal(name, "production seed accepted")
		}
	}
}
func TestNativeSeedBrowseUsersEventsAndMobileScenarios(t *testing.T) {
	_, r := seedFixture(t)
	ctx := context.Background()
	out, err := r.Run(ctx, "seed_browse_demo", nil)
	if err != nil || out.(map[string]int)["created"] != 40 {
		t.Fatal(out, err)
	}
	out, err = r.Run(ctx, "seed_browse_demo", nil)
	if err != nil || out.(map[string]int)["created"] != 0 {
		t.Fatal(out, err)
	}
	var place, typ int64
	if err = r.DB.QueryRow(ctx, `SELECT id FROM places_place ORDER BY id LIMIT 1`).Scan(&place); err != nil {
		t.Fatal(err)
	}
	if err = r.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	if _, err = r.DB.Exec(ctx, `INSERT INTO places_placeactivity(place_id,activity_id,origin,confidence,source,mapping_rule,is_disputed,created_at,updated_at) VALUES($1,$2,'manual',1,'manual','fixture',false,now(),now())`, place, typ); err != nil {
		t.Fatal(err)
	}
	out, err = r.Run(ctx, "seed_demo_users", nil)
	if err != nil || !out.(map[string]any)["activity_created"].(bool) {
		t.Fatal(out, err)
	}
	out, err = r.Run(ctx, "seed_demo_users", nil)
	if err != nil || out.(map[string]any)["activity_created"].(bool) {
		t.Fatal(out, err)
	}
	out, err = r.Run(ctx, "generate_demo_events", args(map[string]any{"synthesize": 7}))
	if err != nil || out.(map[string]int)["synthesized"] != 7 {
		t.Fatal(out, err)
	}
	out, err = r.Run(ctx, "generate_demo_events", args(map[string]any{"synthesize": 7}))
	if err != nil || out.(map[string]int)["synthesized"] != 0 {
		t.Fatal(out, err)
	}
	out, err = r.Run(ctx, "seed_mobile_card_demo", nil)
	if err != nil || len(out.(map[string]any)["activities"].([]int64)) != 5 {
		t.Fatal(out, err)
	}
	out, err = r.Run(ctx, "seed_mobile_card_demo", nil)
	if err != nil || len(out.(map[string]any)["activities"].([]int64)) != 5 {
		t.Fatal(out, err)
	}
}
func TestNativeSeedWorldAllCohortsAuditedScopeAndIdempotence(t *testing.T) {
	_, r := seedFixture(t)
	ctx := context.Background()
	out, err := r.Run(ctx, "seed_demo_data", nil)
	if err != nil {
		t.Fatal(out, err)
	}
	if out.(map[string]any)["activities_created"] != 16 || out.(map[string]any)["users"] != 21 {
		t.Fatal(out)
	}
	var children, consent, approval, adultPublic int
	if err = r.DB.QueryRow(ctx, `SELECT count(*) FROM accounts_user WHERE cohort='child'`).Scan(&children); err != nil {
		t.Fatal(err)
	}
	if err = r.DB.QueryRow(ctx, `SELECT count(*) FROM accounts_parentalconsent WHERE status='active'`).Scan(&consent); err != nil {
		t.Fatal(err)
	}
	if err = r.DB.QueryRow(ctx, `SELECT count(*) FROM places_approvedchildvenue`).Scan(&approval); err != nil {
		t.Fatal(err)
	}
	if err = r.DB.QueryRow(ctx, `SELECT count(*) FROM social_activity WHERE is_publicly_listed AND cohort<>'adult'`).Scan(&adultPublic); err != nil {
		t.Fatal(err)
	}
	if children != 6 || consent != 6 || approval != 2 || adultPublic != 0 {
		t.Fatal(children, consent, approval, adultPublic)
	}
	out, err = r.Run(ctx, "seed_demo_data", nil)
	if err != nil || out.(map[string]any)["activities_created"] != 0 {
		t.Fatal(out, err)
	}
}
func TestNativeLocalDataOnlySeedCOPYAndEmptyDatasetRule(t *testing.T) {
	s, r := seedFixture(t)
	ctx := context.Background()
	out, err := r.Run(ctx, "load_roedu_seed", nil)
	if err != nil {
		t.Fatal(out, err)
	}
	counts := out.(map[string]int64)
	if counts["places_place"] != 7 || counts["places_placeactivity"] != 9 || counts["events_event"] != 39 {
		t.Fatal(counts)
	}
	out, err = r.Run(ctx, "load_roedu_seed", nil)
	if err != nil || out.(map[string]int64)["already_present"] != 1 {
		t.Fatal(out, err)
	}
	if _, err = s.loadSeed(ctx, args(map[string]any{"path": "../../outside.sql"})); err == nil {
		t.Fatal("seed path escaped root")
	}
}
