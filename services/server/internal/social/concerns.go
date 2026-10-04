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

const FormativeNoteTitle = "A quiet note about one of your posts"
const FormativeNoteBody = "A few members felt one of your recent posts didn't quite fit the spirit of this group. No one has reported anything, nothing is hidden, and no names or numbers are attached — this is just a friendly heads-up so you can take another look if you'd like. If you edit the post, this note won't come back for it. You're a valued part of this group; this is about one post, not about you."

type ConcernSummary struct {
	Skipped     bool `json:"skipped"`
	Notes       int  `json:"notes"`
	Escalated   int  `json:"escalated"`
	Teen        int  `json:"teen"`
	Coordinated int  `json:"coordinated"`
	Pileon      int  `json:"pileon"`
	Failed      int  `json:"failed"`
}

func (s *Service) notifyModerators(ctx context.Context, title, body string) error {
	if s.Sentiment.Mode == "automated" {
		return nil
	}
	rows, err := s.DB.Query(ctx, `SELECT id FROM accounts_user WHERE is_active AND (is_staff OR role IN ('moderator','admin')) ORDER BY id`)
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		_ = platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error { return s.notify(ctx, tx, id, "mod_alert", title, body, "/moderation/") })
	}
	return nil
}
func (s *Service) openReview(ctx context.Context, kind string, post, subject *int64, payload map[string]any) (bool, error) {
	created := false
	err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		key := kind
		predicate := ""
		args := []any{kind}
		switch kind {
		case "concern_escalated", "teen_concern":
			if post == nil {
				return platform.ErrInvalid
			}
			key += fmt.Sprint(*post)
			args = append(args, *post)
			predicate = "post_id=$2"
		case "sensor_pileon":
			if subject == nil {
				return platform.ErrInvalid
			}
			key += fmt.Sprint(*subject)
			args = append(args, *subject)
			predicate = "subject_user_id=$2"
		case "sensor_coordinated":
			author, ok := payload["author_id"].(int64)
			if !ok {
				return platform.ErrInvalid
			}
			key += fmt.Sprint(author)
			args = append(args, fmt.Sprint(author))
			predicate = "payload->>'author_id'=$2"
		default:
			return platform.ErrInvalid
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,702342))`, key); err != nil {
			return err
		}
		exists, err := scalar(ctx, tx, `SELECT EXISTS(SELECT 1 FROM safety_concernreview WHERE kind=$1 AND status='open' AND `+predicate+`)`, args...)
		if err != nil || exists {
			return err
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		var id int64
		err = tx.QueryRow(ctx, `INSERT INTO safety_concernreview(kind,payload,status,handled_at,created_at,handled_by_id,post_id,subject_user_id) VALUES($1,$2,'open',NULL,now(),NULL,$3,$4) RETURNING id`, kind, raw, post, subject).Scan(&id)
		if err != nil {
			return err
		}
		created = true
		return s.Audit(ctx, tx, Actor{}, "concern.review_opened", fmt.Sprintf("safety.concernreview:%d", id), map[string]string{"kind": kind})
	})
	return created, err
}
func sortedSet(set map[int64]bool) []int64 {
	out := make([]int64, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
func (s *Service) pileon(ctx context.Context, now time.Time) (map[int64]bool, int, error) {
	protected := map[int64]bool{}
	rows, err := s.DB.Query(ctx, `SELECT subject_user_id FROM safety_concernreview WHERE kind='sensor_pileon' AND status='open' AND subject_user_id IS NOT NULL`)
	if err != nil {
		return nil, 0, err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, 0, err
		}
		protected[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	rows, err = s.DB.Query(ctx, `SELECT p.author_id,array_agg(DISTINCT c.post_id ORDER BY c.post_id) FROM social_postconcern c JOIN social_post p ON p.id=c.post_id WHERE c.created_at>=$1 GROUP BY p.author_id HAVING COUNT(DISTINCT c.post_id)>=3`, now.AddDate(0, 0, -7))
	if err != nil {
		return nil, 0, err
	}
	type incident struct {
		author int64
		posts  []int64
	}
	incidents := []incident{}
	for rows.Next() {
		var in incident
		if err := rows.Scan(&in.author, &in.posts); err != nil {
			rows.Close()
			return nil, 0, err
		}
		incidents = append(incidents, in)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	created := 0
	for _, in := range incidents {
		protected[in.author] = true
		fresh, err := s.openReview(ctx, "sensor_pileon", nil, &in.author, map[string]any{"post_ids": in.posts, "window_days": 7})
		if err != nil {
			return nil, created, err
		}
		if fresh {
			created++
			if err := s.notifyModerators(ctx, "A member may need protective review", "One member is drawing concern flags across several posts. Please look."); err != nil {
				return nil, created, err
			}
		}
	}
	return protected, created, nil
}

type flaggerPosts map[int64]map[int64]bool

func coordinatedPosts(users flaggerPosts) ([]int64, int) {
	heavy := []int64{}
	for id, posts := range users {
		if len(posts) >= 3 {
			heavy = append(heavy, id)
		}
	}
	sort.Slice(heavy, func(i, j int) bool { return heavy[i] < heavy[j] })
	common := map[int64]bool{}
	for i := 0; i < len(heavy); i++ {
		for j := i + 1; j < len(heavy); j++ {
			shared := []int64{}
			for pid := range users[heavy[i]] {
				if users[heavy[j]][pid] {
					shared = append(shared, pid)
				}
			}
			if len(shared) >= 3 {
				for _, pid := range shared {
					common[pid] = true
				}
			}
		}
	}
	if len(common) == 0 {
		return nil, 0
	}
	overlap := 0
	for _, posts := range users {
		covers := true
		for id := range common {
			if !posts[id] {
				covers = false
				break
			}
		}
		if covers {
			overlap++
		}
	}
	if overlap < 2 {
		return nil, 0
	}
	return sortedSet(common), overlap
}
func (s *Service) coordinated(ctx context.Context, now time.Time) (int, error) {
	rows, err := s.DB.Query(ctx, `SELECT p.author_id,c.user_id,c.post_id FROM (SELECT user_id,post_id,created_at FROM social_postconcern UNION ALL SELECT user_id,post_id,created_at FROM social_postdissent) c JOIN social_post p ON p.id=c.post_id WHERE c.created_at>=$1 ORDER BY p.author_id,c.user_id,c.post_id`, now.AddDate(0, 0, -14))
	if err != nil {
		return 0, err
	}
	authors := map[int64]flaggerPosts{}
	for rows.Next() {
		var author, user, post int64
		if err := rows.Scan(&author, &user, &post); err != nil {
			rows.Close()
			return 0, err
		}
		if authors[author] == nil {
			authors[author] = flaggerPosts{}
		}
		if authors[author][user] == nil {
			authors[author][user] = map[int64]bool{}
		}
		authors[author][user][post] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	created := 0
	for author, users := range authors {
		posts, size := coordinatedPosts(users)
		if len(posts) == 0 {
			continue
		}
		fresh, err := s.openReview(ctx, "sensor_coordinated", nil, nil, map[string]any{"author_id": author, "post_ids": posts, "window_days": 14, "flagger_set_size": size})
		if err != nil {
			return created, err
		}
		if fresh {
			created++
			if err := s.notifyModerators(ctx, "Possible coordinated flagging", "The same members are flagging one author across several posts. Review it."); err != nil {
				return created, err
			}
		}
	}
	return created, nil
}
func (s *Service) EvaluateConcerns(ctx context.Context, now time.Time) (ConcernSummary, error) {
	var out ConcernSummary
	if s.DB == nil || s.Audit == nil {
		return out, platform.ErrForbidden
	}
	if err := s.Sentiment.validate(); err != nil {
		return out, err
	}
	claimed, err := s.claimDaily(ctx, "concerns_daily", now)
	if err != nil {
		return out, err
	}
	if !claimed {
		out.Skipped = true
		return out, nil
	}
	protected, count, err := s.pileon(ctx, now)
	if err != nil {
		return out, err
	}
	out.Pileon = count
	out.Coordinated, err = s.coordinated(ctx, now)
	if err != nil {
		return out, err
	}
	rows, err := s.DB.Query(ctx, `SELECT c.user_id FROM social_postconcern c JOIN social_post p ON p.id=c.post_id WHERE c.created_at>=$1 GROUP BY c.user_id HAVING COUNT(DISTINCT p.author_id)>=5`, now.AddDate(0, 0, -s.Sentiment.CooldownDays))
	if err != nil {
		return out, err
	}
	downweighted := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		downweighted[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	noted := map[int64]bool{}
	after := int64(0)
	for {
		posts, err := postBatch(ctx, s.DB, after, true)
		if err != nil {
			return out, err
		}
		if len(posts) == 0 {
			break
		}
		audiences := map[int64]int{}
		for _, p := range posts {
			after = p.ID
			if p.HiddenOwner || p.Cohort == "child" || p.Cohort != "adult" && p.Cohort != "teen" {
				continue
			}
			flags, err := s.DB.Query(ctx, `SELECT DISTINCT user_id FROM social_postconcern WHERE post_id=$1 AND created_at>$2`, p.ID, p.Updated)
			if err != nil {
				out.Failed++
				continue
			}
			n := 0
			for flags.Next() {
				var id int64
				if err := flags.Scan(&id); err != nil {
					flags.Close()
					return out, err
				}
				if !downweighted[id] {
					n++
				}
			}
			err = flags.Err()
			flags.Close()
			if err != nil {
				out.Failed++
				continue
			}
			if p.Cohort == "teen" {
				if n >= s.Sentiment.ConcernTeenK {
					created, err := s.openReview(ctx, "teen_concern", &p.ID, &p.Author, map[string]any{"window_days": s.Sentiment.CooldownDays})
					if err != nil {
						out.Failed++
						continue
					}
					if created {
						out.Teen++
						if err := s.notifyModerators(ctx, "A teen post may need a gentle human relay", "A few members felt a teen's post might be off-topic. See the queue."); err != nil {
							return out, err
						}
					}
				}
				continue
			}
			aud, ok := audiences[p.ThreadID]
			if !ok {
				aud, err = audience(ctx, s.DB, p)
				if err != nil {
					out.Failed++
					continue
				}
				audiences[p.ThreadID] = aud
			}
			attempted, delivered, barred, err := s.adultLadder(ctx, p, n, aud, protected[p.Author], noted[p.Author], now)
			if err != nil {
				out.Failed++
				continue
			}
			if attempted {
				noted[p.Author] = true
			}
			if delivered {
				out.Notes++
			}
			recross := barred && n >= s.Sentiment.ConcernK1
			if n >= s.Sentiment.ConcernK2 || recross {
				created, err := s.openReview(ctx, "concern_escalated", &p.ID, &p.Author, map[string]any{"barred_recross": recross})
				if err != nil {
					out.Failed++
					continue
				}
				if created {
					out.Escalated++
					if err := s.notifyModerators(ctx, "A concern needs a human look", "A post drew repeated concern flags. Review it in the moderation queue."); err != nil {
						return out, err
					}
				}
			}
		}
	}
	err = platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		return s.Audit(ctx, tx, Actor{}, "concerns.evaluated", "", map[string]any{"notes": out.Notes, "escalated": out.Escalated, "teen": out.Teen, "coordinated": out.Coordinated, "pileon": out.Pileon})
	})
	return out, err
}
func (s *Service) adultLadder(ctx context.Context, p batchPost, count, audience int, protected, alreadyNoted bool, now time.Time) (attempted, delivered, barred bool, err error) {
	err = platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,702343))`, fmt.Sprint(p.Author)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO social_postconcernstate(post_id,note_sent_at,note_barred) VALUES($1,NULL,false) ON CONFLICT(post_id) DO NOTHING`, p.ID); err != nil {
			return err
		}
		var sent *time.Time
		if err := tx.QueryRow(ctx, `SELECT note_sent_at,note_barred FROM social_postconcernstate WHERE post_id=$1 FOR UPDATE`, p.ID).Scan(&sent, &barred); err != nil {
			return err
		}
		if sent != nil && p.Updated.After(*sent) && !barred {
			barred = true
			if _, err := tx.Exec(ctx, `UPDATE social_postconcernstate SET note_barred=true WHERE post_id=$1`, p.ID); err != nil {
				return err
			}
		}
		if count < s.Sentiment.ConcernK1 || audience < s.Sentiment.ConcernAudience || sent != nil || barred || protected || alreadyNoted {
			return nil
		}
		recent, err := scalar(ctx, tx, `SELECT EXISTS(SELECT 1 FROM social_postconcernstate state JOIN social_post po ON po.id=state.post_id WHERE po.author_id=$1 AND state.note_sent_at>=$2)`, p.Author, now.AddDate(0, 0, -s.Sentiment.CooldownDays))
		if err != nil || recent {
			return err
		}
		url := fmt.Sprintf("/activities/%d/", p.ActivityID)
		if p.GroupID > 0 {
			url = fmt.Sprintf("/groups/%d/", p.GroupID)
		}
		if s.Notify == nil {
			return platform.ErrForbidden
		}
		delivered, err = s.Notify(ctx, tx, p.Author, "formative_note", FormativeNoteTitle, FormativeNoteBody, url)
		if err != nil {
			return err
		}
		attempted = true
		if _, err := tx.Exec(ctx, `UPDATE social_postconcernstate SET note_sent_at=$2 WHERE post_id=$1`, p.ID, now); err != nil {
			return err
		}
		event := "concern.formative_note_sent"
		if !delivered {
			event = "concern.formative_note_muted"
		}
		return s.Audit(ctx, tx, Actor{}, event, fmt.Sprintf("social.post:%d", p.ID), nil)
	})
	return
}

type ReactionPurge struct {
	Skipped   bool  `json:"skipped"`
	Reactions int64 `json:"reactions"`
	Dissents  int64 `json:"dissents"`
	Concerns  int64 `json:"concerns"`
}

func (s *Service) PurgeSentimentRows(ctx context.Context, now time.Time) (ReactionPurge, error) {
	var out ReactionPurge
	if s.DB == nil || s.Audit == nil {
		return out, platform.ErrForbidden
	}
	if err := s.Sentiment.validate(); err != nil {
		return out, err
	}
	claimed, err := s.claimDaily(ctx, "reaction_purge_daily", now)
	if err != nil {
		return out, err
	}
	if !claimed {
		out.Skipped = true
		return out, nil
	}
	cutoff := now.AddDate(0, 0, -s.Sentiment.RetentionDays)
	for _, table := range []string{"social_postreaction", "social_postdissent", "social_postconcern"} {
		var total int64
		for {
			tag, err := s.DB.Exec(ctx, `DELETE FROM `+table+` WHERE id IN(SELECT id FROM `+table+` WHERE created_at<$1 ORDER BY id LIMIT 1000)`, cutoff)
			if err != nil {
				return out, err
			}
			total += tag.RowsAffected()
			if tag.RowsAffected() == 0 {
				break
			}
		}
		switch table {
		case "social_postreaction":
			out.Reactions = total
		case "social_postdissent":
			out.Dissents = total
		case "social_postconcern":
			out.Concerns = total
		}
	}
	err = platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		return s.Audit(ctx, tx, Actor{}, "reaction.rows_purged", "", map[string]any{"reactions": out.Reactions, "dissents": out.Dissents, "concerns": out.Concerns})
	})
	return out, err
}
