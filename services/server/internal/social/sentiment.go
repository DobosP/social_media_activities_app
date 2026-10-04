package social

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

type SentimentConfig struct {
	AdultK, TeenK, DissentK, DissentAudience, DissentLatch, DissentLapse, ConcernK1, ConcernK2, ConcernTeenK, ConcernAudience, CooldownDays, RetentionDays int
	Mode                                                                                                                                                   string
}

func DefaultSentimentConfig() SentimentConfig {
	return SentimentConfig{5, 8, 6, 12, 2, 2, 2, 4, 3, 8, 14, 90, "automated+human"}
}
func (c SentimentConfig) validate() error {
	for _, n := range []int{c.AdultK, c.TeenK, c.DissentK, c.DissentAudience, c.DissentLatch, c.DissentLapse, c.ConcernK1, c.ConcernK2, c.ConcernTeenK, c.ConcernAudience, c.CooldownDays, c.RetentionDays} {
		if n < 1 || n > 100000 {
			return platform.ErrInvalid
		}
	}
	if c.Mode != "automated" && c.Mode != "automated+human" {
		return platform.ErrInvalid
	}
	return nil
}

type Footer struct {
	Pairs        [][2]string
	Permanent    []string
	Dissent      bool
	Hits, Misses int
	Week         string
}
type FooterChanges struct{ Latched, Unlatched, Permanent int }

func appreciation(footer Footer, counts map[string]int, k, audience, retention int, now time.Time) (Footer, FooterChanges) {
	var changes FooterChanges
	today := now.UTC().Format("2006-01-02")
	date, _ := time.Parse("2006-01-02", today)
	existing := map[string]string{}
	permanent := map[string]bool{}
	for _, pair := range footer.Pairs {
		existing[pair[0]] = pair[1]
	}
	for _, slug := range footer.Permanent {
		permanent[slug] = true
	}
	pairs := make([][2]string, 0)
	for _, slug := range Facets {
		since, was := existing[slug]
		count := counts[slug]
		keep := false
		if was {
			if count >= k {
				keep = true
			} else if !permanent[slug] {
				start, err := time.Parse("2006-01-02", since)
				if err != nil {
					start = date
				}
				if int(date.Sub(start)/(24*time.Hour)) >= retention {
					permanent[slug] = true
					changes.Permanent++
				} else {
					changes.Unlatched++
				}
			}
		} else if count >= k && audience >= 2*k {
			since = today
			keep = true
			changes.Latched++
		}
		if keep {
			start, err := time.Parse("2006-01-02", since)
			if err != nil {
				start = date
				since = today
			}
			if int(date.Sub(start)/(24*time.Hour)) >= retention && !permanent[slug] {
				permanent[slug] = true
				changes.Permanent++
			} else {
				pairs = append(pairs, [2]string{slug, since})
			}
		}
	}
	footer.Pairs = pairs
	footer.Permanent = []string{}
	for slug := range permanent {
		footer.Permanent = append(footer.Permanent, slug)
	}
	sort.Strings(footer.Permanent)
	return footer, changes
}

var facetLines = map[string]string{"helped_me": "People found this helpful.", "felt_welcome": "People felt welcome here.", "made_me_smile": "This made people smile.", "want_to_come": "This makes people want to come.", "got_me_thinking": "This got people thinking."}

func FooterLines(footer Footer, threadCohort, viewerCohort string, announcement bool) []string {
	lines := []string{}
	if threadCohort == "child" || viewerCohort == "child" {
		return lines
	}
	slugs := map[string]bool{}
	for _, p := range footer.Pairs {
		slugs[p[0]] = true
	}
	for _, v := range footer.Permanent {
		slugs[v] = true
	}
	for _, slug := range Facets {
		if slugs[slug] {
			lines = append(lines, facetLines[slug])
		}
		if len(lines) == 2 {
			break
		}
	}
	if footer.Dissent && threadCohort == "adult" && !announcement {
		lines = append(lines, "Some see this differently.")
	}
	return lines
}
func isoWeek(now time.Time) string {
	year, week := now.ISOWeek()
	return fmt.Sprintf("%d-W%02d", year, week)
}
func (s *Service) claimDaily(ctx context.Context, name string, now time.Time) (bool, error) {
	var claimed bool
	err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,702341))`, name); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ops_jobmarker(name,last_run_at) VALUES($1,NULL) ON CONFLICT(name) DO NOTHING`, name); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `UPDATE ops_jobmarker SET last_run_at=$2 WHERE name=$1 AND (last_run_at IS NULL OR last_run_at<=$2::timestamptz-interval '24 hours') RETURNING true`, name, now).Scan(&claimed)
		if err == pgx.ErrNoRows {
			return nil
		}
		return err
	})
	return claimed, err
}
func (s *Service) weeklyDue(ctx context.Context, name string, now time.Time) (bool, error) {
	var stamp *time.Time
	err := s.DB.QueryRow(ctx, `SELECT last_run_at FROM ops_jobmarker WHERE name=$1`, name).Scan(&stamp)
	if err == pgx.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return stamp == nil || isoWeek(*stamp) != isoWeek(now), nil
}

type batchPost struct {
	ID, Author, ThreadID, Owner, ActivityID, GroupID int64
	Cohort                                           string
	HiddenOwner, Announcement                        bool
	Updated                                          time.Time
}

func postBatch(ctx context.Context, q platform.Querier, after int64, concerns bool) ([]batchPost, error) {
	where := `EXISTS(SELECT 1 FROM social_postreaction r WHERE r.post_id=po.id) OR EXISTS(SELECT 1 FROM social_postdissent d WHERE d.post_id=po.id) OR EXISTS(SELECT 1 FROM social_postsentimentfooter f WHERE f.post_id=po.id)`
	if concerns {
		where = `EXISTS(SELECT 1 FROM social_postconcern c WHERE c.post_id=po.id)`
	}
	rows, err := q.Query(ctx, `SELECT po.id,po.author_id,po.thread_id,COALESCE(a.owner_id,g.owner_id),COALESCE(th.activity_id,0),COALESCE(th.group_id,0),COALESCE(a.cohort,g.cohort),COALESCE(a.is_hidden,g.is_hidden),po.is_announcement,po.updated_at FROM social_post po JOIN social_thread th ON th.id=po.thread_id LEFT JOIN social_activity a ON a.id=th.activity_id LEFT JOIN social_group g ON g.id=th.group_id WHERE po.id>$1 AND NOT po.is_hidden AND (`+where+`) ORDER BY po.id LIMIT 1000`, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []batchPost{}
	for rows.Next() {
		var p batchPost
		if err := rows.Scan(&p.ID, &p.Author, &p.ThreadID, &p.Owner, &p.ActivityID, &p.GroupID, &p.Cohort, &p.HiddenOwner, &p.Announcement, &p.Updated); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func audience(ctx context.Context, q platform.Querier, p batchPost) (int, error) {
	var n int
	table, column, id := "social_membership", "activity_id", p.ActivityID
	if p.GroupID > 0 {
		table, column, id = "social_groupmembership", "group_id", p.GroupID
	}
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM `+table+` m WHERE m.`+column+`=$1 AND m.state='member' AND m.role<>'guardian' AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$2 AND b.blocked_id=m.user_id) OR (b.blocker_id=m.user_id AND b.blocked_id=$2))`, id, p.Owner).Scan(&n)
	return n, err
}
func loadFooter(ctx context.Context, tx pgx.Tx, id int64) (Footer, error) {
	var f Footer
	if _, err := tx.Exec(ctx, `INSERT INTO social_postsentimentfooter(post_id,appreciation_slugs,appreciation_permanent,dissent_active,dissent_consecutive_hits,dissent_consecutive_misses,dissent_window_key,computed_at) VALUES($1,'[]','[]',false,0,0,'',now()) ON CONFLICT(post_id) DO NOTHING`, id); err != nil {
		return f, err
	}
	var pairs, permanent []byte
	err := tx.QueryRow(ctx, `SELECT appreciation_slugs,appreciation_permanent,dissent_active,dissent_consecutive_hits,dissent_consecutive_misses,dissent_window_key FROM social_postsentimentfooter WHERE post_id=$1 FOR UPDATE`, id).Scan(&pairs, &permanent, &f.Dissent, &f.Hits, &f.Misses, &f.Week)
	if err != nil {
		return f, err
	}
	if json.Unmarshal(pairs, &f.Pairs) != nil || json.Unmarshal(permanent, &f.Permanent) != nil {
		return f, platform.ErrInvalid
	}
	return f, nil
}
func saveFooter(ctx context.Context, tx pgx.Tx, id int64, f Footer, now time.Time) error {
	pairs, err := json.Marshal(f.Pairs)
	if err != nil {
		return err
	}
	permanent, err := json.Marshal(f.Permanent)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE social_postsentimentfooter SET appreciation_slugs=$2,appreciation_permanent=$3,dissent_active=$4,dissent_consecutive_hits=$5,dissent_consecutive_misses=$6,dissent_window_key=$7,computed_at=$8 WHERE post_id=$1`, id, pairs, permanent, f.Dissent, f.Hits, f.Misses, f.Week, now)
	return err
}

type SentimentSummary struct {
	Skipped   bool `json:"skipped"`
	Latched   int  `json:"latched"`
	Unlatched int  `json:"unlatched"`
	Permanent int  `json:"permanent"`
	Dissent   any  `json:"dissent"`
	Failed    int  `json:"failed"`
}

func (s *Service) RecomputeSentiment(ctx context.Context, now time.Time) (SentimentSummary, error) {
	var out SentimentSummary
	if s.DB == nil || s.Audit == nil {
		return out, platform.ErrForbidden
	}
	if err := s.Sentiment.validate(); err != nil {
		return out, err
	}
	claimed, err := s.claimDaily(ctx, "post_sentiment_daily", now)
	if err != nil {
		return out, err
	}
	if !claimed {
		out.Skipped = true
		return out, nil
	}
	weekly, err := s.weeklyDue(ctx, "post_sentiment_weekly", now)
	if err != nil {
		return out, err
	}
	dissent := map[string]any{"week": isoWeek(now), "latched": 0, "lapsed": 0}
	processed := 0
	after := int64(0)
	for {
		posts, err := postBatch(ctx, s.DB, after, false)
		if err != nil {
			return out, err
		}
		if len(posts) == 0 {
			break
		}
		audiences := map[int64]int{}
		for _, p := range posts {
			after = p.ID
			if p.HiddenOwner {
				continue
			}
			if p.Cohort == "child" || p.Cohort != "adult" && p.Cohort != "teen" {
				if _, err := s.DB.Exec(ctx, `DELETE FROM social_postsentimentfooter WHERE post_id=$1`, p.ID); err != nil {
					return out, err
				}
				continue
			}
			n, ok := audiences[p.ThreadID]
			if !ok {
				n, err = audience(ctx, s.DB, p)
				if err != nil {
					return out, err
				}
				audiences[p.ThreadID] = n
			}
			var change FooterChanges
			dl, dp := 0, 0
			err = platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
				f, err := loadFooter(ctx, tx, p.ID)
				if err != nil {
					return err
				}
				rows, err := tx.Query(ctx, `SELECT emoji,COUNT(DISTINCT user_id) FROM social_postreaction WHERE post_id=$1 GROUP BY emoji`, p.ID)
				if err != nil {
					return err
				}
				counts := map[string]int{}
				for rows.Next() {
					var slug string
					var count int
					if err := rows.Scan(&slug, &count); err != nil {
						rows.Close()
						return err
					}
					counts[slug] = count
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return err
				}
				k := s.Sentiment.AdultK
				if p.Cohort == "teen" {
					k = s.Sentiment.TeenK
				}
				f, change = appreciation(f, counts, k, n, s.Sentiment.RetentionDays, now)
				if weekly && p.Cohort == "adult" && !p.Announcement && f.Week != isoWeek(now) {
					var count int
					if err := tx.QueryRow(ctx, `SELECT COUNT(DISTINCT user_id) FROM social_postdissent WHERE post_id=$1`, p.ID).Scan(&count); err != nil {
						return err
					}
					was := f.Dissent
					if count >= s.Sentiment.DissentK && n >= s.Sentiment.DissentAudience {
						f.Hits++
						f.Misses = 0
					} else {
						f.Misses++
						f.Hits = 0
					}
					if f.Hits >= s.Sentiment.DissentLatch {
						f.Dissent = true
					} else if f.Misses >= s.Sentiment.DissentLapse {
						f.Dissent = false
					}
					if f.Dissent && !was {
						dl = 1
					}
					if was && !f.Dissent {
						dp = 1
					}
					f.Week = isoWeek(now)
				}
				return saveFooter(ctx, tx, p.ID, f, now)
			})
			if err != nil {
				out.Failed++
				continue
			}
			processed++
			out.Latched += change.Latched
			out.Unlatched += change.Unlatched
			out.Permanent += change.Permanent
			dissent["latched"] = dissent["latched"].(int) + dl
			dissent["lapsed"] = dissent["lapsed"].(int) + dp
		}
	}
	if weekly {
		if _, err := s.DB.Exec(ctx, `INSERT INTO ops_jobmarker(name,last_run_at) VALUES('post_sentiment_weekly',$1) ON CONFLICT(name) DO UPDATE SET last_run_at=EXCLUDED.last_run_at`, now); err != nil {
			return out, err
		}
		out.Dissent = dissent
	}
	err = platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		return s.Audit(ctx, tx, Actor{}, "post_sentiment.recomputed", "", map[string]any{"latched": out.Latched, "unlatched": out.Unlatched, "permanent": out.Permanent, "posts": processed})
	})
	return out, err
}
func (s *Service) SentimentFooters(ctx context.Context, a Actor, kind string, id int64, postIDs []int64) (map[int64][]string, error) {
	v, err := threadOwner(ctx, s.DB, a, kind, id, false)
	if err != nil {
		return nil, err
	}
	if err := threadGate(ctx, s.DB, a, v, false); err != nil {
		return nil, err
	}
	out := map[int64][]string{}
	for _, id := range postIDs {
		out[id] = []string{}
	}
	if a.Cohort == "child" || v.Cohort == "child" || len(postIDs) == 0 {
		return out, nil
	}
	if len(postIDs) > 1000 {
		return nil, platform.ErrInvalid
	}
	rows, err := s.DB.Query(ctx, `SELECT p.id,p.is_announcement,f.appreciation_slugs,f.appreciation_permanent,f.dissent_active FROM social_post p JOIN social_postsentimentfooter f ON f.post_id=p.id WHERE p.thread_id=$1 AND p.id=ANY($2) AND NOT p.is_hidden`, v.ThreadID, postIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var pid int64
		var announcement bool
		var pairs, permanent []byte
		var f Footer
		if err := rows.Scan(&pid, &announcement, &pairs, &permanent, &f.Dissent); err != nil {
			return nil, err
		}
		if json.Unmarshal(pairs, &f.Pairs) != nil || json.Unmarshal(permanent, &f.Permanent) != nil {
			return nil, platform.ErrInvalid
		}
		out[pid] = FooterLines(f, v.Cohort, a.Cohort, announcement)
	}
	return out, rows.Err()
}
