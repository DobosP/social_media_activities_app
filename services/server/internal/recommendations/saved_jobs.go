package recommendations

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/jackc/pgx/v5"
)

type MatchSummary struct{ Notified, Scanned, Skipped int }
type searchRecord struct {
	ID, User                   int64
	Type, Category, Area       *int64
	Cohort, City, Cost, Window string
	Beginners                  bool
}
type candidate struct {
	ID                                 int64
	Title, TypeName, PlaceName, Window string
	Starts                             time.Time
}

func coarseSQL(window string) string {
	if window == "" {
		return "true"
	}
	weekday := `EXTRACT(ISODOW FROM a.starts_at AT TIME ZONE 'Europe/Bucharest')`
	hour := `EXTRACT(HOUR FROM a.starts_at AT TIME ZONE 'Europe/Bucharest')`
	day := weekday + `>=6`
	if strings.HasPrefix(window, "weekday") {
		day = weekday + `<=5`
	}
	if strings.HasSuffix(window, "daytime") {
		return `(` + day + ` AND ` + hour + `<18)`
	}
	return `(` + day + ` AND ` + hour + `>=18)`
}
func loadSaver(ctx context.Context, q platform.Querier, id int64) (platform.Actor, error) {
	var a platform.Actor
	err := q.QueryRow(ctx, `SELECT id,public_id::text,username,display_name,age_band,cohort,role,is_identity_verified,is_active,is_staff,is_superuser FROM accounts_user WHERE id=$1`, id).Scan(&a.ID, &a.PublicID, &a.Username, &a.DisplayName, &a.AgeBand, &a.Cohort, &a.Role, &a.IdentityVerified, &a.IsActive, &a.IsStaff, &a.IsSuperuser)
	return a, err
}
func matchCandidates(ctx context.Context, tx pgx.Tx, a platform.Actor, ss searchRecord, gauge bool, limit int) ([]candidate, error) {
	args := []any{a.ID, a.Cohort, ss.Type, ss.Category, ss.City, ss.Beginners, ss.Cost, ss.Window, limit}
	where := social.ActivityVisibilitySQL() + ` AND a.status='open' AND a.starts_at>=now() AND a.owner_id<>$1 AND ($5::text='' OR lower(p.address_city)=lower($5)) AND (NOT $6 OR a.beginners_welcome) AND ($7::text='' OR a.cost_band=$7) AND $8::text IS NOT NULL AND ` + coarseSQL(ss.Window) + ` AND (($3::bigint IS NOT NULL AND (a.activity_type_id=$3 OR EXISTS(SELECT 1 FROM social_activity_secondary_types st WHERE st.activity_id=a.id AND st.activitytype_id=$3))) OR ($4::bigint IS NOT NULL AND (t.category_id=$4 OR EXISTS(SELECT 1 FROM social_activity_secondary_types st JOIN taxonomy_activitytype sec ON sec.id=st.activitytype_id WHERE st.activity_id=a.id AND sec.category_id=$4)))) AND NOT EXISTS(SELECT 1 FROM saved_searches_savedsearchmatch ledger WHERE ledger.user_id=$1 AND ledger.activity_id=a.id)`
	sql := `SELECT a.id,a.title,t.name,p.name,a.starts_at,'' FROM social_activity a JOIN places_place p ON p.id=a.place_id JOIN taxonomy_activitytype t ON t.id=a.activity_type_id WHERE ` + where + ` ORDER BY a.starts_at,a.id LIMIT $9`
	if gauge {
		if ss.Beginners || ss.Cost != "" {
			return []candidate{}, nil
		}
		where = `g.cohort=$2 AND g.converted_activity_id IS NULL AND g.expires_at>now() AND g.proposer_id<>$1 AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$1 AND b.blocked_id=g.proposer_id) OR (b.blocker_id=g.proposer_id AND b.blocked_id=$1)) AND NOT EXISTS(SELECT 1 FROM social_activityinterest_interested_users own WHERE own.activityinterest_id=g.id AND own.user_id=$1) AND ($5::text='' OR lower(p.address_city)=lower($5)) AND NOT $6 AND $7::text='' AND ($8::text='' OR g.coarse_window=$8) AND (($3::bigint IS NOT NULL AND g.activity_type_id=$3) OR ($4::bigint IS NOT NULL AND t.category_id=$4)) AND NOT EXISTS(SELECT 1 FROM saved_searches_savedsearchgaugematch ledger WHERE ledger.user_id=$1 AND ledger.interest_id=g.id)`
		sql = `SELECT g.id,''::text,t.name,p.name,g.expires_at,g.coarse_window FROM social_activityinterest g JOIN places_place p ON p.id=g.place_id JOIN taxonomy_activitytype t ON t.id=g.activity_type_id WHERE ` + where + ` ORDER BY g.expires_at,g.id LIMIT $9`
	}
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []candidate{}
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.ID, &c.Title, &c.TypeName, &c.PlaceName, &c.Starts, &c.Window); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MatchSavedSearches uses a durable user/object ledger before notifications. Muting
// still consumes the ledger, so delete/recreate and later unmute never replay it.
func (s *Service) MatchSavedSearches(ctx context.Context, now time.Time) (MatchSummary, error) {
	var out MatchSummary
	rows, err := s.DB.Query(ctx, `SELECT ss.id,ss.user_id,ss.activity_type_id,ss.category_id,ss.area_id,ss.cohort,coalesce(ar.city,''),ss.beginners,ss.cost_band,ss.coarse_window FROM saved_searches_savedsearch ss LEFT JOIN communities_area ar ON ar.id=ss.area_id ORDER BY ss.id`)
	if err != nil {
		return out, err
	}
	searches := []searchRecord{}
	for rows.Next() {
		var ss searchRecord
		if err := rows.Scan(&ss.ID, &ss.User, &ss.Type, &ss.Category, &ss.Area, &ss.Cohort, &ss.City, &ss.Beginners, &ss.Cost, &ss.Window); err != nil {
			rows.Close()
			return out, err
		}
		searches = append(searches, ss)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	location, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		return out, err
	}
	for _, ss := range searches {
		if out.Scanned >= 1000 {
			break
		}
		var scanned, notified int
		err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
			var existing int64
			if err := tx.QueryRow(ctx, `SELECT id FROM saved_searches_savedsearch WHERE id=$1 FOR UPDATE`, ss.ID).Scan(&existing); err != nil {
				if err == pgx.ErrNoRows {
					return nil
				}
				return err
			}
			var locked int64
			if err := tx.QueryRow(ctx, `SELECT id FROM accounts_user WHERE id=$1 FOR UPDATE`, ss.User).Scan(&locked); err != nil {
				return err
			}
			a, err := loadSaver(ctx, tx, ss.User)
			if err != nil {
				return err
			}
			if a.Cohort != ss.Cohort || a.Cohort == "unassigned" || a.Cohort == "" {
				return nil
			}
			if platform.Participate(ctx, tx, a) != nil {
				return nil
			}
			var consumed int
			if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM saved_searches_savedsearchmatch WHERE user_id=$1 AND created_at>$2)+(SELECT count(*) FROM saved_searches_savedsearchgaugematch WHERE user_id=$1 AND created_at>$2)`, a.ID, now.Add(-24*time.Hour)).Scan(&consumed); err != nil {
				return err
			}
			remaining := min(50-consumed, 1000-out.Scanned)
			if remaining <= 0 {
				return nil
			}
			for _, gauge := range []bool{false, true} {
				candidates, err := matchCandidates(ctx, tx, a, ss, gauge, remaining)
				if err != nil {
					return err
				}
				for _, c := range candidates {
					table, column, kind, url, title, body := "saved_searches_savedsearchmatch", "activity_id", "activity_match", fmt.Sprintf("/activities/%d/", c.ID), fmt.Sprintf("New %s: \"%s\"", c.TypeName, c.Title), "Starts "+c.Starts.In(location).Format("Mon 02 Jan, 15:04")+"."
					if gauge {
						table, column, kind, url, title = "saved_searches_savedsearchgaugematch", "interest_id", "gauge_match", fmt.Sprintf("/gauges/%d/", c.ID), "New interest gauge: "+c.TypeName
						labels := map[string]string{"weekday_daytime": "Weekday daytime", "weekday_evening": "Weekday evening", "weekend_daytime": "Weekend daytime", "weekend_evening": "Weekend evening"}
						body = "Someone's gauging interest at " + c.PlaceName + " (" + labels[c.Window] + "). Add yours to help it start."
					}
					result, err := tx.Exec(ctx, `INSERT INTO `+table+`(user_id,`+column+`,created_at) VALUES($1,$2,$3) ON CONFLICT(user_id,`+column+`) DO NOTHING`, a.ID, c.ID, now)
					if err != nil {
						return err
					}
					if result.RowsAffected() == 0 {
						continue
					}
					scanned++
					remaining--
					delivered, err := platform.Notify(ctx, tx, a.ID, kind, title, body, url)
					if err != nil {
						return err
					}
					if delivered {
						notified++
					}
					if remaining == 0 {
						break
					}
				}
				if remaining == 0 {
					break
				}
			}
			return nil
		})
		if err != nil {
			out.Skipped++
			continue
		}
		out.Scanned += scanned
		out.Notified += notified
	}
	err = platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		return platform.RecordAudit(ctx, tx, platform.Actor{}, "saved_search.swept", "", map[string]int{"notified": out.Notified, "scanned": out.Scanned, "skipped": out.Skipped})
	})
	return out, err
}
