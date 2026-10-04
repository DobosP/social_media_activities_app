package recommendations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

func retirementRecommendations(t *testing.T) (*Service, platform.Actor, platform.Actor, int64, int64, int64) {
	t.Helper()
	db := testdb.New(t, *recommendationDSN, func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	cat := catalog.New(db)
	soc := social.New(db, platform.RecordAudit)
	s := New(db, cat, soc)
	viewer := testdb.Actor(t, db, "retirement-saver", "adult")
	owner := testdb.Actor(t, db, "retirement-owner", "adult")
	place := testdb.Place(t, db, "Retirement match venue", "osm")
	var typ, category int64
	if err := db.QueryRow(context.Background(), `SELECT id,category_id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ, &category); err != nil {
		t.Fatal(err)
	}
	return s, viewer, owner, place, typ, category
}

func TestRetirementPostgresSavedSearchValidationAndDuplicateDimensions(t *testing.T) {
	s, viewer, other, _, typ, category := retirementRecommendations(t)
	ctx := context.Background()
	for _, c := range []struct {
		name  string
		input SavedSearchInput
	}{
		{"no_filter", SavedSearchInput{}}, {"two_filters", SavedSearchInput{ActivityType: &typ, Category: &category}},
		{"invalid_window", SavedSearchInput{ActivityType: &typ, CoarseWindow: "whenever"}},
		{"invalid_cost", SavedSearchInput{ActivityType: &typ, CostBand: "expensive"}},
		{"oversized_city", SavedSearchInput{ActivityType: &typ, City: strings.Repeat("x", 129)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := s.CreateSavedSearch(ctx, viewer, c.input); !errors.Is(err, platform.ErrInvalid) {
				t.Fatal("invalid saved search accepted", err)
			}
		})
	}
	first, err := s.CreateSavedSearch(ctx, viewer, SavedSearchInput{ActivityType: &typ, City: "  Cluj-Napoca  ", CoarseWindow: "weekday_evening"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateSavedSearch(ctx, viewer, SavedSearchInput{ActivityType: &typ, City: "Cluj-Napoca", CoarseWindow: "weekday_evening"}); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("normalized duplicate accepted", err)
	}
	second, err := s.CreateSavedSearch(ctx, viewer, SavedSearchInput{ActivityType: &typ, City: "Cluj-Napoca", CoarseWindow: "weekend_daytime"})
	if err != nil || second == first {
		t.Fatal("distinct coarse window collapsed", err)
	}
	if _, err = s.SavedSearch(ctx, other, first); err == nil {
		t.Fatal("other subject read a saved search")
	}
	if err = s.DeleteSavedSearch(ctx, other, first); err == nil {
		t.Fatal("other subject deleted a saved search")
	}
	rows, err := s.SavedSearches(ctx, viewer)
	if err != nil || len(rows) != 2 {
		t.Fatal("own searches lost", err)
	}
	for _, raw := range rows {
		var row map[string]any
		if err := json.Unmarshal(raw, &row); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"lat", "lon", "latitude", "longitude", "location", "coordinate", "point", "geom"} {
			if _, ok := row[name]; ok {
				t.Fatal("saved search exposed precise coordinates", name)
			}
		}
		wantWindow := "weekday_evening"
		if int64(row["id"].(float64)) == second {
			wantWindow = "weekend_daytime"
		}
		if row["coarse_window"] != wantWindow {
			t.Fatal("saved coarse window changed")
		}
	}
	if err := s.DeleteSavedSearch(ctx, viewer, first); err != nil {
		t.Fatal("owner deletion failed", err)
	}
	if _, err := s.SavedSearch(ctx, viewer, first); err == nil {
		t.Fatal("deleted saved search remains readable")
	}
}

func TestRetirementPostgresSavedSearchActivityFilterMatrix(t *testing.T) {
	for _, name := range []string{"matching_once", "hidden", "cancelled", "completed", "blocked_owner", "other_city", "beginners", "cost_band", "category_match", "own_activity", "muted_ledger_no_replay"} {
		t.Run(name, func(t *testing.T) {
			s, viewer, owner, place, typ, category := retirementRecommendations(t)
			ctx := context.Background()
			input := SavedSearchInput{ActivityType: &typ}
			if name == "other_city" {
				input.City = "Different city"
			}
			if name == "beginners" {
				input.Beginners = true
			}
			if name == "cost_band" {
				input.CostBand = "free"
			}
			if name == "category_match" {
				input.ActivityType = nil
				input.Category = &category
			}
			if _, err := s.CreateSavedSearch(ctx, viewer, input); err != nil {
				t.Fatal(err)
			}
			creator := owner
			if name == "own_activity" {
				creator = viewer
			}
			activity, err := s.Social.CreateActivity(ctx, creator, social.ActivityInput{Place: place, ActivityType: typ, Title: "Retirement match", StartsAt: time.Now().Add(24 * time.Hour), CostBand: "paid"})
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "hidden":
				_, err = s.DB.Exec(ctx, `UPDATE social_activity SET is_hidden=true WHERE id=$1`, activity)
			case "cancelled", "completed":
				_, err = s.DB.Exec(ctx, `UPDATE social_activity SET status=$2 WHERE id=$1`, activity, name)
			case "blocked_owner":
				_, err = s.DB.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, viewer.ID, owner.ID)
			case "muted_ledger_no_replay":
				_, err = s.DB.Exec(ctx, `INSERT INTO notifications_notificationpreference(user_id,muted_kinds) VALUES($1,ARRAY['activity_match'])`, viewer.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.MatchSavedSearches(ctx, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if name == "matching_once" || name == "category_match" {
				want = 1
			}
			if result.Notified != want {
				t.Fatalf("matched notices=%d want=%d", result.Notified, want)
			}
			if name == "muted_ledger_no_replay" {
				if _, err = s.DB.Exec(ctx, `UPDATE notifications_notificationpreference SET muted_kinds='{}' WHERE user_id=$1`, viewer.ID); err != nil {
					t.Fatal(err)
				}
			}
			replayed, err := s.MatchSavedSearches(ctx, time.Now())
			if err != nil || replayed.Notified != 0 {
				t.Fatal("saved match replayed across tick/unmute", err)
			}
			if _, err := s.Social.RequestConnection(ctx, viewer, owner.PublicID); err == nil {
				t.Fatal("saved match manufactured a shared contact context")
			}
			var ledger int
			if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM saved_searches_savedsearchmatch WHERE user_id=$1 AND activity_id=$2`, viewer.ID, activity).Scan(&ledger); err != nil {
				t.Fatal(err)
			}
			if name == "muted_ledger_no_replay" && ledger != 1 {
				t.Fatal("muted delivery failed to consume the durable match ledger")
			}
			var notices int
			if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM notifications_notification WHERE recipient_id=$1 AND kind='activity_match'`, viewer.ID).Scan(&notices); err != nil || notices != want {
				t.Fatal("match counter did not describe actual notice rows", err)
			}
			if want == 1 {
				var url string
				if err := s.DB.QueryRow(ctx, `SELECT url FROM notifications_notification WHERE recipient_id=$1 AND kind='activity_match'`, viewer.ID).Scan(&url); err != nil || url != "/activities/"+fmt.Sprint(activity)+"/" || ledger != 1 {
					t.Fatal("actual once-only notice lost its activity URL or ledger", err)
				}
			}
		})
	}
}
