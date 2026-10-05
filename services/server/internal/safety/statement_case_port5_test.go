package safety

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Unlike LIKE-only fixtures, every port here retains the real foreign keys.
func privacy5Fixture(t *testing.T) (*Service, *social.Service) {
	t.Helper()
	ctx := context.Background()
	db := testdb.New(t, *safetyTestDSN, func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	acc := accounts.New(db, nil, "synthetic-privacy5-public-binding", accounts.Config{})
	if err := acc.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	soc := social.New(db, platform.RecordAudit)
	s := New(db, Config{Accounts: acc, CanSeeActivity: soc.CanSeeActivity, CanReadThread: soc.CanReadThread})
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s, soc
}

func privacy5Activity(t *testing.T, s *Service, soc *social.Service, owner platform.Actor) int64 {
	t.Helper()
	ctx := context.Background()
	place := testdb.Place(t, s.DB, "Privacy5 synthetic public library", "osm")
	var category, typ int64
	if err := s.DB.QueryRow(ctx, `INSERT INTO taxonomy_activitycategory(slug,name,description,created_at,updated_at) VALUES($1,'Synthetic activity','',now(),now()) RETURNING id`, fmt.Sprintf("privacy5-%d", place)).Scan(&category); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(ctx, `INSERT INTO taxonomy_activitytype(slug,name,aliases,is_active,created_at,updated_at,category_id,family_friendly,wellness) VALUES($1,'Synthetic peer activity','[]',true,now(),now(),$2,true,false) RETURNING id`, fmt.Sprintf("privacy5-type-%d", place), category).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	id, err := soc.CreateActivity(ctx, owner, social.ActivityInput{Title: "Synthetic private activity", Place: place, ActivityType: typ, StartsAt: time.Now().Add(24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func privacy5Target(t *testing.T, s *Service, model string, id int64) Target {
	t.Helper()
	app := "social"
	if model == "user" {
		app = "accounts"
	}
	target, err := s.ResolveTarget(context.Background(), s.DB, app, model, id)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func privacy5Action(t *testing.T, s *Service, mod platform.Actor, model string, id int64, in ActionInput) int64 {
	t.Helper()
	action, err := s.TakeAction(context.Background(), mod, privacy5Target(t, s, model, id), in, 0)
	if err != nil {
		t.Fatal(err)
	}
	return action
}

func privacy5Post(t *testing.T, soc *social.Service, owner platform.Actor, activity int64, body string) int64 {
	t.Helper()
	id, err := soc.WritePost(context.Background(), owner, "activity", activity, social.PostInput{Body: body}, false)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func privacy5Object(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var row map[string]any
	if err := json.Unmarshal(raw, &row); err != nil {
		t.Fatal(err)
	}
	return row
}

func TestPrivacyCasePort5StatementsNotifyExactAffectedSubject(t *testing.T) {
	for _, tc := range []struct{ name, model, decision, reason, label string }{
		{"ban", "user", "ban", "grooming", "Ban account"}, {"suspend", "user", "suspend", "harassment", "Suspend account"}, {"warn", "user", "warn", "other", "Warn"}, {"remove_post", "post", "remove", "spam", "Remove content"}, {"remove_activity", "activity", "remove", "off_platform", "Remove content"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, soc := privacy5Fixture(t)
			ctx := context.Background()
			mod := user(t, s, "privacy5-private-moderator", true)
			subject := user(t, s, "privacy5-affected", false)
			id := subject.ID
			if tc.model != "user" {
				id = privacy5Activity(t, s, soc, subject)
				if tc.model == "post" {
					id = privacy5Post(t, soc, subject, id, "Synthetic private words")
				}
			}
			privacy5Action(t, s, mod, tc.model, id, ActionInput{Decision: tc.decision, Reason: tc.reason, Notes: "private source-case moderator note"})
			var notices int
			var body, url string
			if err := s.DB.QueryRow(ctx, `SELECT count(*),COALESCE(max(body),''),COALESCE(max(url),'') FROM notifications_notification WHERE recipient_id=$1 AND kind='moderation'`, subject.ID).Scan(&notices, &body, &url); err != nil {
				t.Fatal(err)
			}
			if notices != 1 || !strings.Contains(body, tc.label) || !strings.Contains(body, reasons[tc.reason]) || !strings.Contains(strings.ToLower(body), "contest") || url != "" {
				t.Fatalf("statement-of-reasons count/label/reason/remedy/url failed: count=%d", notices)
			}
			if strings.Contains(body, mod.Username) || strings.Contains(body, "private source-case moderator note") {
				t.Fatal("statement exposed private moderator/note")
			}
			var all int
			if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM notifications_notification`).Scan(&all); err != nil || all != 1 {
				t.Fatal("no-report action manufactured reporter notice", all, err)
			}
		})
	}
}

func TestPrivacyCasePort5RestrictionStatementSelfScopeDatesAndNoDecision(t *testing.T) {
	s, _ := privacy5Fixture(t)
	ctx := context.Background()
	mod := user(t, s, "privacy5-statement-secret-moderator", true)
	subject := user(t, s, "privacy5-suspended", false)
	other := user(t, s, "privacy5-active-other", false)
	action := privacy5Action(t, s, mod, "user", subject.ID, ActionInput{Decision: "suspend", Reason: "harassment", SuspendDays: 3, Notes: "private restriction statement note"})
	raw, err := s.RestrictionStatement(ctx, subject.ID)
	if err != nil {
		t.Fatal(err)
	}
	row := privacy5Object(t, raw)
	var expires time.Time
	if err := s.DB.QueryRow(ctx, `SELECT expires_at FROM safety_moderationaction WHERE id=$1`, action).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	lift, err := time.Parse(time.RFC3339Nano, row["lifts_at"].(string))
	if err != nil || !lift.Equal(expires) || row["action_id"] != float64(action) || row["is_lifetime"] != false || row["can_appeal"] != true {
		t.Fatal("owned suspension statement/date/remedy mismatch", err)
	}
	if strings.Contains(string(raw), mod.Username) || strings.Contains(string(raw), "private restriction statement note") {
		t.Fatal("restricted statement leaked moderator/note")
	}
	if _, err := s.RestrictionStatement(ctx, other.ID); err != pgx.ErrNoRows {
		t.Fatal("active account got another subject decision", err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_active=false WHERE id=$1`, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestrictionStatement(ctx, other.ID); err != pgx.ErrNoRows {
		t.Fatal("self-deactivated account got false moderation detail", err)
	}
	privacy5Action(t, s, mod, "user", subject.ID, ActionInput{Decision: "ban", Reason: "grooming"})
	raw, err = s.RestrictionStatement(ctx, subject.ID)
	if err != nil {
		t.Fatal(err)
	}
	row = privacy5Object(t, raw)
	if row["is_lifetime"] != true || row["lifts_at"] != nil {
		t.Fatal("lifetime restriction advertised lift date")
	}
}
