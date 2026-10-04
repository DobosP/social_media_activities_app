package notifications

import (
	"context"
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

func TestRetirementPostgresOrganizerPrepNudgeMatrix(t *testing.T) {
	for _, c := range []struct {
		name, point, status string
		hours               int
		muted, coorganizer  bool
		want                int
	}{
		{"blank_once", "", "open", 1, false, false, 1},
		{"owner_and_coorganizer_only", "", "open", 1, false, true, 2},
		{"meeting_point_set", "Main gate", "open", 1, false, false, 0},
		{"whitespace_blank", "   ", "open", 1, false, false, 1},
		{"outside_window", "", "open", 49, false, false, 0},
		{"cancelled", "", "cancelled", 1, false, false, 0},
		{"muted", "", "open", 1, true, false, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			db := testdb.New(t, *domainDSN, func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
			owner := testdb.Actor(t, db, "prep-retirement-owner", "adult")
			peer := testdb.Actor(t, db, "prep-retirement-peer", "adult")
			member := testdb.Actor(t, db, "prep-retirement-member", "adult")
			place := testdb.Place(t, db, "Prep fixture hall", "osm")
			var typ int64
			if err := db.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
				t.Fatal(err)
			}
			soc := social.New(db, platform.RecordAudit)
			now := time.Now()
			activity, err := soc.CreateActivity(ctx, owner, social.ActivityInput{Place: place, ActivityType: typ, Title: "Prep fixture", StartsAt: now.Add(time.Duration(c.hours) * time.Hour), MeetingPoint: c.point})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, `UPDATE social_activity SET status=$2 WHERE id=$1`, activity, c.status); err != nil {
				t.Fatal(err)
			}
			for _, p := range []struct {
				id   int64
				role string
			}{{peer.ID, "co_organizer"}, {member.ID, "member"}} {
				if !c.coorganizer && p.id == peer.ID {
					continue
				}
				if _, err := db.Exec(ctx, `INSERT INTO social_membership(activity_id,user_id,role,state,created_at,updated_at,decided_at,attendance_intent,transit_status,brings_support_person) VALUES($1,$2,$3,'member',now(),now(),now(),'unknown','none',false)`, activity, p.id, p.role); err != nil {
					t.Fatal(err)
				}
			}
			if c.muted {
				if _, err := db.Exec(ctx, `INSERT INTO notifications_notificationpreference(user_id,muted_kinds) VALUES($1,ARRAY['organizer_prep'])`, owner.ID); err != nil {
					t.Fatal(err)
				}
			}
			sent, err := soc.NudgeOrganizers(ctx, now)
			if err != nil || sent != c.want {
				t.Fatalf("prep recipients=%d want=%d err=%v", sent, c.want, err)
			}
			replayed, err := soc.NudgeOrganizers(ctx, now)
			if err != nil || replayed != 0 {
				t.Fatal("prep replay produced another notice", err)
			}
			var memberNotices int
			if err := db.QueryRow(ctx, `SELECT count(*) FROM notifications_notification WHERE recipient_id=$1 AND kind='organizer_prep'`, member.ID).Scan(&memberNotices); err != nil || memberNotices != 0 {
				t.Fatal("regular member received organizer preparation notice", err)
			}
			for _, recipient := range []struct {
				id   int64
				want int
			}{{owner.ID, min(c.want, 1)}, {peer.ID, max(c.want-1, 0)}} {
				var count int
				if err := db.QueryRow(ctx, `SELECT count(*) FROM notifications_notification WHERE recipient_id=$1 AND kind='organizer_prep'`, recipient.id).Scan(&count); err != nil || count != recipient.want {
					t.Fatal("prep notice recipient distribution changed", err, count, recipient.want)
				}
			}
			rows, err := db.Query(ctx, `SELECT url FROM notifications_notification WHERE kind='organizer_prep'`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var url string
				if err := rows.Scan(&url); err != nil {
					t.Fatal(err)
				}
				if url != fmt.Sprintf("/activities/%d/", activity) || strings.Contains(url, "/api/") {
					t.Fatal("nudge lost web detail link")
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
