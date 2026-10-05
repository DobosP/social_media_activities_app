package accounts_test

import (
	"context"
	"encoding/json"
	"flag"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/safety"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func correctionAccounts(t *testing.T) *accounts.Service {
	t.Helper()
	// Read only the explicit disposable fixture flag registered by this package's
	// internal tests. No environment or credential source participates.
	dsn := flag.Lookup("accounts-test-dsn")
	if dsn == nil {
		t.Fatal("accounts fixture flag unavailable")
	}
	db := testdb.New(t, dsn.Value.String(), func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	if err := accounts.NewStore(db).Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := accounts.New(db, nil, "synthetic", accounts.Config{})
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCoverageCorrectionSelfErasureUUIDOnlyAuditAndValidChain(t *testing.T) {
	s := correctionAccounts(t)
	ctx := context.Background()
	a := testdb.Actor(t, s.DB, "correction-erased-user", "adult")
	if err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error { return platform.RecordAudit(ctx, tx, a, "synthetic.before_erasure", "", nil) }); err != nil {
		t.Fatal(err)
	}
	chain := safety.New(s.DB, safety.Config{})
	if valid, _, err := chain.VerifyAuditChain(ctx, nil); err != nil || !valid {
		t.Fatal("initial canonical chain invalid", err)
	}
	if err := s.Erase(ctx, a, a); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_user WHERE id=$1)`, a.ID).Scan(&exists); err != nil || exists {
		t.Fatal("self-erased user survived", err)
	}
	var raw json.RawMessage
	var actor *int64
	var actorRef int64
	if err := s.DB.QueryRow(ctx, `SELECT data,actor_id,actor_ref FROM safety_auditlog WHERE event='account.erased'`).Scan(&raw, &actor, &actorRef); err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if data["erased_public_id"] != a.PublicID {
		t.Fatal("erasure audit lost erased subject UUID")
	}
	if _, exists := data["erased_username"]; exists || strings.Contains(string(raw), a.Username) {
		t.Fatal("permanent erasure audit retained username")
	}
	if actor != nil || actorRef != a.ID {
		t.Fatal("audit FK anonymization changed immutable chain actor")
	}
	if valid, _, err := chain.VerifyAuditChain(ctx, nil); err != nil || !valid {
		t.Fatal("self erasure broke canonical audit chain", err)
	}
}

func correctionActivity(t *testing.T, s *accounts.Service, owner platform.Actor) (*social.Service, int64) {
	t.Helper()
	ctx := context.Background()
	native := social.New(s.DB, platform.RecordAudit)
	var kind int64
	if err := s.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	place := testdb.Place(t, s.DB, "Correction synthetic hall", "osm")
	activity, err := native.CreateActivity(ctx, owner, social.ActivityInput{Place: place, ActivityType: kind, Title: "Correction private game", StartsAt: time.Now().Add(24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	return native, activity
}

func correctionPostRows(t *testing.T, s *accounts.Service, owner platform.Actor) (map[string]any, []map[string]any, []byte) {
	t.Helper()
	payload, err := s.Export(context.Background(), owner, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	posts := payload["thread_posts"].(map[string]any)
	var rows []map[string]any
	for _, rawRow := range posts["items"].([]json.RawMessage) {
		var row map[string]any
		if err := json.Unmarshal(rawRow, &row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	return posts, rows, raw
}

func TestCoverageCorrectionExportOwnReplyAnnouncementAndStrictProjection(t *testing.T) {
	s := correctionAccounts(t)
	ctx := context.Background()
	owner := testdb.Actor(t, s.DB, "correction-export-owner", "adult")
	peer := testdb.Actor(t, s.DB, "correction-export-peer", "adult")
	native, activity := correctionActivity(t, s, owner)
	member, err := native.Join(ctx, peer, activity)
	if err != nil {
		t.Fatal(err)
	}
	if err := native.Vote(ctx, owner, member, true, true); err != nil {
		t.Fatal(err)
	}
	if _, err := native.WritePost(ctx, owner, "activity", activity, social.PostInput{Body: "my plan: bring snacks"}, false); err != nil {
		t.Fatal(err)
	}
	peerPost, err := native.WritePost(ctx, peer, "activity", activity, social.PostInput{Body: "another members private words"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := native.WritePost(ctx, owner, "activity", activity, social.PostInput{Body: "replying to you", ReplyTo: &peerPost}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := native.WritePost(ctx, owner, "activity", activity, social.PostInput{Body: "owner announcement here"}, true); err != nil {
		t.Fatal(err)
	}
	posts, rows, raw := correctionPostRows(t, s, owner)
	if len(rows) != 3 || posts["total"] != 3 || posts["truncated"] != false {
		t.Fatal("own-word export totals/truncation mismatch")
	}
	if strings.Contains(string(raw), "another members private words") {
		t.Fatal("reply target's words leaked into portability export")
	}
	wantFields := []string{"thread_kind", "thread_id", "thread_title", "body", "status", "is_announcement", "edited", "had_attachment", "created_at"}
	sort.Strings(wantFields)
	bodies := map[string]bool{}
	for _, row := range rows {
		fields := make([]string, 0, len(row))
		for key := range row {
			fields = append(fields, key)
		}
		sort.Strings(fields)
		if !reflect.DeepEqual(fields, wantFields) {
			t.Fatal("own-word export escaped strict nine-field allowlist")
		}
		if row["thread_kind"] != "activity" || row["thread_id"] != float64(activity) || row["status"] != "visible" {
			t.Fatal("own-word thread identity/status projection drift")
		}
		bodies[row["body"].(string)] = true
		if row["body"] == "owner announcement here" && row["is_announcement"] != true {
			t.Fatal("announcement was downgraded to ordinary post")
		}
	}
	for _, body := range []string{"my plan: bring snacks", "replying to you", "owner announcement here"} {
		if !bodies[body] {
			t.Fatal("own post/reply/announcement missing")
		}
	}
}

func TestCoverageCorrectionExportNewestCapPreservesChronologicalBodyOrder(t *testing.T) {
	s := correctionAccounts(t)
	s.Config.ExportPostCap = 3
	ctx := context.Background()
	owner := testdb.Actor(t, s.DB, "correction-newest-owner", "adult")
	native, activity := correctionActivity(t, s, owner)
	for _, body := range []string{"post 0", "post 1", "post 2", "post 3", "post 4"} {
		if _, err := native.WritePost(ctx, owner, "activity", activity, social.PostInput{Body: body}, false); err != nil {
			t.Fatal(err)
		}
	}
	posts, rows, _ := correctionPostRows(t, s, owner)
	var bodies []string
	for _, row := range rows {
		bodies = append(bodies, row["body"].(string))
	}
	if !reflect.DeepEqual(bodies, []string{"post 2", "post 3", "post 4"}) || posts["total"] != 5 || posts["truncated"] != true {
		t.Fatal("bounded export failed exact newest-kept chronological sequence")
	}
}
