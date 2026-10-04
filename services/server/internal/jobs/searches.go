package jobs

import (
	"context"
	"errors"
	"fmt"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"time"
)

type MatchSummary struct{ Notified, Scanned, Skipped int }
type savedSearch struct {
	ID, User             int64
	Cohort               string
	Type, Category, Area *int64
	Beginners            bool
	Cost, Window         string
}

const searchArea = `($4::bigint IS NULL OR EXISTS(SELECT 1 FROM communities_area ar WHERE ar.id=$4 AND ((ar.derive_method='city' AND lower(p.address_city)=lower(ar.city)) OR false)))`

func (r *Runner) MatchSavedSearches(ctx context.Context) (MatchSummary, error) {
	var summary MatchSummary
	rows, err := r.DB.Query(ctx, `SELECT id,user_id,cohort,activity_type_id,category_id,area_id,beginners,cost_band,coarse_window FROM saved_searches_savedsearch ORDER BY id`)
	if err != nil {
		return summary, err
	}
	searches := []savedSearch{}
	for rows.Next() {
		var search savedSearch
		if err = rows.Scan(&search.ID, &search.User, &search.Cohort, &search.Type, &search.Category, &search.Area, &search.Beginners, &search.Cost, &search.Window); err != nil {
			rows.Close()
			return summary, err
		}
		searches = append(searches, search)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return summary, err
	}
	loc, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		return summary, err
	}
	for _, search := range searches {
		if summary.Scanned >= r.Config.SavedSearchMatchBatch {
			break
		}
		beforeScanned, beforeNotified := summary.Scanned, summary.Notified
		err = platform.Transaction(ctx, r.DB, func(tx pgx.Tx) error {
			var a platform.Actor
			err := tx.QueryRow(ctx, `SELECT id,cohort,age_band,is_identity_verified,is_active FROM accounts_user WHERE id=$1`, search.User).Scan(&a.ID, &a.Cohort, &a.AgeBand, &a.IdentityVerified, &a.IsActive)
			if err != nil {
				return err
			}
			if a.Cohort == "unassigned" || a.Cohort != search.Cohort {
				return nil
			}
			if err = platform.Participate(ctx, tx, a); errors.Is(err, platform.ErrForbidden) {
				return nil
			} else if err != nil {
				return err
			}
			rows, err := tx.Query(ctx, `SELECT a.id,a.title,t.name,a.starts_at FROM social_activity a JOIN taxonomy_activitytype t ON t.id=a.activity_type_id JOIN places_place p ON p.id=a.place_id WHERE a.cohort=$1 AND NOT a.is_hidden AND a.status='open' AND a.starts_at>=$2 AND a.owner_id<>$3 AND `+searchArea+` AND (($5::bigint IS NOT NULL AND (a.activity_type_id=$5 OR EXISTS(SELECT 1 FROM social_activity_secondary_types st WHERE st.activity_id=a.id AND st.activitytype_id=$5))) OR ($6::bigint IS NOT NULL AND (t.category_id=$6 OR EXISTS(SELECT 1 FROM social_activity_secondary_types st JOIN taxonomy_activitytype t2 ON t2.id=st.activitytype_id WHERE st.activity_id=a.id AND t2.category_id=$6)))) AND (NOT $7::boolean OR a.beginners_welcome) AND ($8='' OR a.cost_band=$8) AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$3 AND b.blocked_id=a.owner_id) OR (b.blocker_id=a.owner_id AND b.blocked_id=$3)) AND NOT EXISTS(SELECT 1 FROM saved_searches_savedsearchmatch m WHERE m.user_id=$3 AND m.activity_id=a.id) ORDER BY a.starts_at,a.id LIMIT $9`, search.Cohort, r.Config.Now(), a.ID, search.Area, search.Type, search.Category, search.Beginners, search.Cost, r.Config.SavedSearchMatchBatch-summary.Scanned)
			if err != nil {
				return err
			}
			type match struct {
				id          int64
				title, kind string
				start       time.Time
			}
			matches := []match{}
			for rows.Next() {
				var m match
				if err = rows.Scan(&m.id, &m.title, &m.kind, &m.start); err != nil {
					rows.Close()
					return err
				}
				matches = append(matches, m)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			for _, m := range matches {
				if !scheduleFits(m.start.In(loc), search.Window) {
					continue
				}
				if summary.Scanned >= r.Config.SavedSearchMatchBatch {
					break
				}
				allowed, err := r.searchBudget(ctx, tx, a.ID)
				if err != nil {
					return err
				}
				if !allowed {
					break
				}
				tag, err := tx.Exec(ctx, `INSERT INTO saved_searches_savedsearchmatch(user_id,activity_id,created_at) VALUES($1,$2,now()) ON CONFLICT(user_id,activity_id) DO NOTHING`, a.ID, m.id)
				if err != nil {
					return err
				}
				if tag.RowsAffected() == 0 {
					continue
				}
				summary.Scanned++
				sent, err := platform.Notify(ctx, tx, a.ID, "activity_match", fmt.Sprintf("New %s: \"%s\"", m.kind, m.title), "Starts "+m.start.In(loc).Format("Mon 02 Jan, 15:04")+".", fmt.Sprintf("/activities/%d/", m.id))
				if err != nil {
					return err
				}
				if sent {
					summary.Notified++
				}
			}
			if search.Beginners || search.Cost != "" {
				return nil
			}
			rows, err = tx.Query(ctx, `SELECT g.id,t.name,p.name,g.coarse_window FROM social_activityinterest g JOIN taxonomy_activitytype t ON t.id=g.activity_type_id JOIN places_place p ON p.id=g.place_id WHERE g.cohort=$1 AND g.converted_activity_id IS NULL AND g.expires_at>$2 AND g.proposer_id<>$3 AND `+searchArea+` AND (($5::bigint IS NOT NULL AND g.activity_type_id=$5) OR ($6::bigint IS NOT NULL AND t.category_id=$6)) AND ($7='' OR g.coarse_window=$7) AND NOT EXISTS(SELECT 1 FROM social_activityinterest_interested_users i WHERE i.activityinterest_id=g.id AND i.user_id=$3) AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$3 AND b.blocked_id=g.proposer_id) OR (b.blocker_id=g.proposer_id AND b.blocked_id=$3)) AND NOT EXISTS(SELECT 1 FROM saved_searches_savedsearchgaugematch m WHERE m.user_id=$3 AND m.interest_id=g.id) ORDER BY g.expires_at,g.id LIMIT $8`, search.Cohort, r.Config.Now(), a.ID, search.Area, search.Type, search.Category, search.Window, r.Config.SavedSearchMatchBatch-summary.Scanned)
			if err != nil {
				return err
			}
			type gauge struct {
				id                  int64
				kind, place, window string
			}
			gauges := []gauge{}
			for rows.Next() {
				var g gauge
				if err = rows.Scan(&g.id, &g.kind, &g.place, &g.window); err != nil {
					rows.Close()
					return err
				}
				gauges = append(gauges, g)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			for _, g := range gauges {
				if summary.Scanned >= r.Config.SavedSearchMatchBatch {
					break
				}
				allowed, err := r.searchBudget(ctx, tx, a.ID)
				if err != nil {
					return err
				}
				if !allowed {
					break
				}
				tag, err := tx.Exec(ctx, `INSERT INTO saved_searches_savedsearchgaugematch(user_id,interest_id,created_at) VALUES($1,$2,now()) ON CONFLICT(user_id,interest_id) DO NOTHING`, a.ID, g.id)
				if err != nil {
					return err
				}
				if tag.RowsAffected() == 0 {
					continue
				}
				summary.Scanned++
				labels := map[string]string{"weekday_daytime": "Weekday daytime", "weekday_evening": "Weekday evening", "weekend_daytime": "Weekend daytime", "weekend_evening": "Weekend evening"}
				sent, err := platform.Notify(ctx, tx, a.ID, "gauge_match", "New interest gauge: "+g.kind, fmt.Sprintf("Someone's gauging interest at %s (%s). Add yours to help it start.", g.place, labels[g.window]), fmt.Sprintf("/gauges/%d/", g.id))
				if err != nil {
					return err
				}
				if sent {
					summary.Notified++
				}
			}
			return nil
		})
		if err != nil {
			summary.Scanned, summary.Notified = beforeScanned, beforeNotified
			summary.Skipped++
			if ctx.Err() != nil {
				return summary, ctx.Err()
			}
		}
	}
	err = platform.Transaction(ctx, r.DB, func(tx pgx.Tx) error {
		return platform.RecordAudit(ctx, tx, platform.Actor{}, "saved_search.swept", "", map[string]int{"notified": summary.Notified, "scanned": summary.Scanned, "skipped": summary.Skipped})
	})
	return summary, err
}
func scheduleFits(t time.Time, window string) bool {
	if window == "" {
		return true
	}
	weekday := t.Weekday() != time.Saturday && t.Weekday() != time.Sunday
	daytime := t.Hour() < 18
	switch window {
	case "weekday_daytime":
		return weekday && daytime
	case "weekday_evening":
		return weekday && !daytime
	case "weekend_daytime":
		return !weekday && daytime
	case "weekend_evening":
		return !weekday && !daytime
	}
	return false
}
func (r *Runner) searchBudget(ctx context.Context, tx pgx.Tx, user int64) (bool, error) {
	if _, err := tx.Exec(ctx, `DELETE FROM accounts_go_action_budget WHERE until<=$1`, r.Config.Now()); err != nil {
		return false, err
	}
	var count int
	err := tx.QueryRow(ctx, `INSERT INTO accounts_go_action_budget(user_id,action,count,until) VALUES($1,'saved_search_match',1,$2) ON CONFLICT(user_id,action) DO UPDATE SET count=accounts_go_action_budget.count+1 RETURNING count`, user, r.Config.Now().Add(r.Config.SavedSearchNotifyWindow)).Scan(&count)
	return count <= r.Config.SavedSearchNotifyLimit, err
}
