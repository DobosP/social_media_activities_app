package social

import (
	"context"
	"fmt"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

type Guardrail struct {
	Supervised            bool
	Latest, Earliest, Cap *int
	Weekdays              string
	Categories            []string
}
type EffectiveGuardrail struct {
	Supervised            bool
	Latest, Earliest, Cap *int
	Weekdays              map[int]bool
	Categories            map[string]bool
}

func minOptional(a, b *int) *int {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if *b < *a {
		return b
	}
	return a
}
func maxOptional(a, b *int) *int {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if *b > *a {
		return b
	}
	return a
}
func combineGuardrails(rails []Guardrail) (EffectiveGuardrail, error) {
	var out EffectiveGuardrail
	for _, r := range rails {
		out.Supervised = out.Supervised || r.Supervised
		out.Latest = minOptional(out.Latest, r.Latest)
		out.Earliest = maxOptional(out.Earliest, r.Earliest)
		out.Cap = minOptional(out.Cap, r.Cap)
		if r.Weekdays != "" {
			days := map[int]bool{}
			for _, c := range r.Weekdays {
				if c < '1' || c > '7' {
					return out, platform.ErrForbidden
				}
				days[int(c-'0')] = true
			}
			if out.Weekdays == nil {
				out.Weekdays = days
			} else {
				for day := range out.Weekdays {
					if !days[day] {
						delete(out.Weekdays, day)
					}
				}
			}
		}
		if len(r.Categories) > 0 {
			categories := map[string]bool{}
			for _, c := range r.Categories {
				if c == "" {
					return out, platform.ErrForbidden
				}
				categories[c] = true
			}
			if out.Categories == nil {
				out.Categories = categories
			} else {
				for category := range out.Categories {
					if !categories[category] {
						delete(out.Categories, category)
					}
				}
			}
		}
	}
	return out, nil
}
func effectiveRail(ctx context.Context, q platform.Querier, ward int64) (EffectiveGuardrail, error) {
	rows, err := q.Query(ctx, `SELECT rail.supervised_only,rail.latest_start_hour,rail.earliest_start_hour,rail.max_open_joins,rail.allowed_weekdays,rail.allowed_categories FROM accounts_guardianguardrail rail JOIN accounts_guardianrelationship rel ON rel.id=rail.relationship_id WHERE rel.ward_id=$1 AND rel.status='active'`, ward)
	if err != nil {
		return EffectiveGuardrail{}, err
	}
	rails := []Guardrail{}
	for rows.Next() {
		var r Guardrail
		if err := rows.Scan(&r.Supervised, &r.Latest, &r.Earliest, &r.Cap, &r.Weekdays, &r.Categories); err != nil {
			rows.Close()
			return EffectiveGuardrail{}, err
		}
		rails = append(rails, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return EffectiveGuardrail{}, err
	}
	return combineGuardrails(rails)
}
func categoryAllowed(ctx context.Context, q platform.Querier, rail EffectiveGuardrail, typeID int64) error {
	if rail.Categories == nil {
		return nil
	}
	if len(rail.Categories) == 0 {
		return platform.ErrForbidden
	}
	rows, err := q.Query(ctx, `WITH RECURSIVE ancestry AS (SELECT c.id,c.parent_id,c.slug,ARRAY[c.id] AS path FROM taxonomy_activitycategory c JOIN taxonomy_activitytype t ON t.category_id=c.id WHERE t.id=$1 UNION ALL SELECT c.id,c.parent_id,c.slug,a.path||c.id FROM taxonomy_activitycategory c JOIN ancestry a ON a.parent_id=c.id WHERE NOT c.id=ANY(a.path) AND cardinality(a.path)<16) SELECT slug FROM ancestry`, typeID)
	if err != nil {
		return err
	}
	defer rows.Close()
	allowed := false
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return err
		}
		allowed = allowed || rail.Categories[slug]
	}
	return errorIfFalse(allowed, rows.Err())
}
func childVenue(ctx context.Context, q platform.Querier, id int64) error {
	yes, err := scalar(ctx, q, `SELECT EXISTS(SELECT 1 FROM places_place p WHERE p.id=$1 AND (EXISTS(SELECT 1 FROM places_approvedchildvenue WHERE place_id=p.id) OR (p.source IN ('osm','user') AND jsonb_typeof(p.raw_tags)='object' AND EXISTS(SELECT 1 FROM places_childvenueclass cv WHERE cv.is_active AND jsonb_typeof(cv.osm_match)='object' AND cv.osm_match<>'{}'::jsonb AND NOT EXISTS(SELECT 1 FROM jsonb_each(cv.osm_match) criterion WHERE p.raw_tags->criterion.key IS DISTINCT FROM criterion.value))) OR (p.source='overture' AND jsonb_typeof(p.raw_tags)='object' AND EXISTS(SELECT 1 FROM places_childvenueclass cv WHERE cv.is_active AND ((jsonb_typeof(cv.overture_categories)='array' AND cv.overture_categories ? (p.raw_tags->>'overture:category')) OR EXISTS(SELECT 1 FROM jsonb_array_elements_text(CASE WHEN jsonb_typeof(p.raw_tags->'overture:alternate')='array' THEN p.raw_tags->'overture:alternate' ELSE '[]'::jsonb END) category WHERE jsonb_typeof(cv.overture_categories)='array' AND cv.overture_categories ? category))))))`, id)
	return errorIfFalse(yes, err)
}
func childCreateGate(ctx context.Context, q platform.Querier, a Actor, in ActivityInput) error {
	if a.Cohort != "child" {
		return nil
	}
	if err := childVenue(ctx, q, in.Place); err != nil {
		return err
	}
	rail, err := effectiveRail(ctx, q, a.ID)
	if err != nil {
		return err
	}
	return categoryAllowed(ctx, q, rail, in.ActivityType)
}
func childJoinGate(ctx context.Context, q platform.Querier, a Actor, v activityState) error {
	if a.Cohort != "child" {
		return nil
	}
	if err := childVenue(ctx, q, v.PlaceID); err != nil {
		return err
	}
	rail, err := effectiveRail(ctx, q, a.ID)
	if err != nil {
		return err
	}
	location, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		return err
	}
	local := v.StartsAt.In(location)
	weekday := int(local.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	if rail.Supervised && !v.GuardianAccompanied || rail.Latest != nil && local.Hour() > *rail.Latest || rail.Earliest != nil && local.Hour() < *rail.Earliest || rail.Weekdays != nil && !rail.Weekdays[weekday] {
		return platform.ErrForbidden
	}
	if err := categoryAllowed(ctx, q, rail, v.TypeID); err != nil {
		return err
	}
	if rail.Cap != nil {
		var n int
		if err := q.QueryRow(ctx, `SELECT COUNT(*) FROM social_membership m JOIN social_activity a ON a.id=m.activity_id WHERE m.user_id=$1 AND a.status='open' AND m.state<>'removed'`, a.ID).Scan(&n); err != nil {
			return err
		}
		if n >= *rail.Cap {
			return platform.ErrForbidden
		}
	}
	return nil
}
func supervisionSatisfied(ctx context.Context, q platform.Querier, v activityState) (bool, error) {
	if !v.Supervised {
		return true, nil
	}
	rows, err := q.Query(ctx, `SELECT m.user_id FROM social_membership m JOIN accounts_guardianrelationship rel ON rel.guardian_id=m.user_id AND rel.ward_id=$1 AND rel.status='active' WHERE m.activity_id=$2 AND m.role='guardian' AND m.state='member'`, v.OwnerID, v.ID)
	if err != nil {
		return false, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return false, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		actor, err := actorByID(ctx, q, id)
		if err != nil {
			return false, err
		}
		if actor.Cohort != "adult" {
			continue
		}
		if err := platform.Participate(ctx, q, actor); err == nil {
			return true, nil
		} else if err != platform.ErrForbidden {
			return false, err
		}
	}
	return false, nil
}
func (s *Service) AddGuardian(ctx context.Context, a Actor, id, targetID int64) (int64, error) {
	var mid int64
	err := s.transaction(ctx, a, func(tx pgx.Tx) error {
		v, err := activity(ctx, tx, a, id, true)
		if err != nil {
			return err
		}
		if v.OwnerID != a.ID || v.Cohort != "child" || !v.GuardianAccompanied {
			return platform.ErrForbidden
		}
		target, err := actorByID(ctx, tx, targetID)
		if err != nil {
			return err
		}
		if target.Cohort != "adult" {
			return platform.ErrForbidden
		}
		if err := platform.Participate(ctx, tx, target); err != nil {
			return err
		}
		yes, err := scalar(ctx, tx, `SELECT EXISTS(SELECT 1 FROM accounts_guardianrelationship WHERE guardian_id=$1 AND ward_id=$2 AND status='active')`, targetID, a.ID)
		if err := errorIfFalse(yes, err); err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `INSERT INTO social_membership(activity_id,user_id,role,state,attendance_intent,transit_status,brings_support_person,created_at,updated_at,decided_at) VALUES($1,$2,'guardian','member','unknown','none',false,now(),now(),now()) ON CONFLICT(activity_id,user_id) DO UPDATE SET role='guardian',state='member',decided_at=now(),updated_at=now() RETURNING id`, id, targetID).Scan(&mid)
		if err != nil {
			return err
		}
		if err := s.settlePending(ctx, tx, a, v); err != nil {
			return err
		}
		return s.audit(ctx, tx, a, "activity.guardian_added", "activity", id, nil)
	})
	return mid, err
}
func (s *Service) SetSupervision(ctx context.Context, a Actor, id int64, supervised bool) error {
	return s.transaction(ctx, a, func(tx pgx.Tx) error {
		v, err := activity(ctx, tx, a, id, true)
		if err != nil {
			return err
		}
		if v.OwnerID != a.ID || v.Status != "open" || supervised && v.Cohort != "child" {
			return platform.ErrForbidden
		}
		if _, err := tx.Exec(ctx, `UPDATE social_activity SET supervised=$2,guardian_accompanied=CASE WHEN $2 THEN true ELSE guardian_accompanied END,updated_at=now() WHERE id=$1`, id, supervised); err != nil {
			return err
		}
		v.Supervised = supervised
		if err := s.settlePending(ctx, tx, a, v); err != nil {
			return err
		}
		return s.audit(ctx, tx, a, "activity.supervision_set", "activity", id, map[string]bool{"supervised": supervised})
	})
}
func (s *Service) settlePending(ctx context.Context, tx pgx.Tx, a Actor, v activityState) error {
	yes, err := supervisionSatisfied(ctx, tx, v)
	if err != nil || !yes {
		return err
	}
	members, err := participantCount(ctx, tx, v.ID)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT m.id,m.user_id FROM social_membership m WHERE m.activity_id=$1 AND m.state='requested' AND (SELECT COUNT(*)::double precision FROM social_joinvote vote WHERE vote.membership_id=m.id AND vote.approve)/NULLIF($2::double precision,0)>=$3 ORDER BY m.id`, v.ID, members, v.Threshold)
	if err != nil {
		return err
	}
	pending := [][2]int64{}
	for rows.Next() {
		var mid, uid int64
		if err := rows.Scan(&mid, &uid); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, [2]int64{mid, uid})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range pending {
		var approvals int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM social_joinvote WHERE membership_id=$1 AND approve`, p[0]).Scan(&approvals); err != nil {
			return err
		}
		if members == 0 || float64(approvals)/float64(members) < v.Threshold {
			continue
		}
		if v.Capacity != nil && members >= *v.Capacity {
			break
		}
		target, err := actorByID(ctx, tx, p[1])
		if err != nil {
			return err
		}
		if target.Cohort != v.Cohort {
			continue
		}
		if err := platform.Participate(ctx, tx, target); err == platform.ErrForbidden {
			continue
		} else if err != nil {
			return err
		}
		blocked, err := platform.Blocked(ctx, tx, target.ID, v.OwnerID)
		if err != nil {
			return err
		}
		if blocked {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE social_membership SET state='member',decided_at=now(),updated_at=now() WHERE id=$1`, p[0]); err != nil {
			return err
		}
		if err := s.notify(ctx, tx, p[1], "join_approved", "You're in!", fmt.Sprintf("You were admitted to “%s”.", v.Title), fmt.Sprintf("/api/social/activities/%d/", v.ID)); err != nil {
			return err
		}
		members++
	}
	return nil
}
