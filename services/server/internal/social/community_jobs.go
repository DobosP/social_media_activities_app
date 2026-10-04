package social

import (
	"context"
	"fmt"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

type CommunityConfig struct{ MinActivities, AnonymousFloor, MinDays, LookbackDays int }

func DefaultCommunityConfig() CommunityConfig { return CommunityConfig{3, 5, 2, 180} }

type CommunitySummary struct {
	Published   int   `json:"published"`
	Deactivated int64 `json:"deactivated"`
}
type coordinate struct {
	City, Cohort, Tier      string
	Category                int64
	Type                    *int64
	Activities, Days, Peers int
}

func (s *Service) GenerateCommunities(ctx context.Context, now time.Time) (CommunitySummary, error) {
	var out CommunitySummary
	if s.DB == nil || s.Audit == nil {
		return out, platform.ErrForbidden
	}
	c := s.CommunityPolicy
	if c.MinActivities < 1 || c.AnonymousFloor < 1 || c.MinDays < 1 || c.LookbackDays < 1 {
		return out, platform.ErrInvalid
	}
	err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(683475951216)`); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `WITH base AS(SELECT a.id,a.cohort,a.activity_type_id,t.category_id,p.address_city city,(a.starts_at AT TIME ZONE 'Europe/Bucharest')::date AS local_day FROM social_activity a JOIN places_place p ON p.id=a.place_id JOIN taxonomy_activitytype t ON t.id=a.activity_type_id WHERE a.starts_at>=$1 AND NOT a.is_hidden AND a.status IN ('open','completed') AND p.address_city<>'' AND a.cohort IN ('adult','teen','child')),peers AS(SELECT b.*,CASE WHEN u.id IS NOT NULL THEN m.user_id ELSE NULL END user_id FROM base b LEFT JOIN social_membership m ON m.activity_id=b.id AND m.state='member' AND m.role<>'guardian' LEFT JOIN accounts_user u ON u.id=m.user_id AND u.cohort=b.cohort) SELECT city,cohort,category_id,activity_type_id,'type',COUNT(DISTINCT id),COUNT(DISTINCT local_day),COUNT(DISTINCT user_id) FROM peers GROUP BY city,cohort,category_id,activity_type_id UNION ALL SELECT city,cohort,category_id,NULL::bigint,'category',COUNT(DISTINCT id),COUNT(DISTINCT local_day),COUNT(DISTINCT user_id) FROM peers GROUP BY city,cohort,category_id ORDER BY 1,2,5,3,4`, now.AddDate(0, 0, -c.LookbackDays))
		if err != nil {
			return err
		}
		coords := []coordinate{}
		for rows.Next() {
			var co coordinate
			if err := rows.Scan(&co.City, &co.Cohort, &co.Category, &co.Type, &co.Tier, &co.Activities, &co.Days, &co.Peers); err != nil {
				rows.Close()
				return err
			}
			coords = append(coords, co)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		keep := []int64{}
		for _, co := range coords {
			if co.Activities < c.MinActivities || co.Days < c.MinDays || co.Peers < c.AnonymousFloor {
				continue
			}
			areaID, err := ensureArea(ctx, tx, co.City)
			if err != nil {
				return err
			}
			var areaName, areaSlug, label, slug string
			if err := tx.QueryRow(ctx, `SELECT name,slug FROM communities_area WHERE id=$1`, areaID).Scan(&areaName, &areaSlug); err != nil {
				return err
			}
			if co.Type != nil {
				if err := tx.QueryRow(ctx, `SELECT name,slug FROM taxonomy_activitytype WHERE id=$1`, *co.Type).Scan(&label, &slug); err != nil {
					return err
				}
				slug = areaSlug + "-t-" + slug + "-" + co.Cohort
			} else {
				if err := tx.QueryRow(ctx, `SELECT name,slug FROM taxonomy_activitycategory WHERE id=$1`, co.Category).Scan(&label, &slug); err != nil {
					return err
				}
				slug = areaSlug + "-c-" + slug + "-" + co.Cohort
			}
			slug = citySlug(slug)
			name := areaName + " " + label
			if len([]rune(name)) > 160 {
				return platform.ErrInvalid
			}
			var id int64
			err = tx.QueryRow(ctx, `SELECT id FROM communities_community WHERE cohort=$1 AND area_id=$2 AND (($3::bigint IS NOT NULL AND activity_type_id=$3) OR ($3::bigint IS NULL AND activity_type_id IS NULL AND category_id=$4))`, co.Cohort, areaID, co.Type, co.Category).Scan(&id)
			if err == pgx.ErrNoRows {
				base := slug
				if base == "" {
					base = "x"
				}
				if len(base) > 140 {
					base = base[:140]
				}
				unique := ""
				for n := 0; n < 10000; n++ {
					candidate := base
					if n > 0 {
						suffix := fmt.Sprintf("-%d", n+1)
						candidate = base[:min(len(base), 140-len(suffix))] + suffix
					}
					exists, err := scalar(ctx, tx, `SELECT EXISTS(SELECT 1 FROM communities_community WHERE slug=$1)`, candidate)
					if err != nil {
						return err
					}
					if !exists {
						unique = candidate
						break
					}
				}
				if unique == "" {
					return platform.ErrInvalid
				}
				err = tx.QueryRow(ctx, `INSERT INTO communities_community(cohort,area_id,category_id,activity_type_id,tier,slug,name,is_published,last_evaluated_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,true,$8,$8) RETURNING id`, co.Cohort, areaID, co.Category, co.Type, co.Tier, unique, name, now).Scan(&id)
				if err != nil {
					return err
				}
			} else if err != nil {
				return err
			} else {
				if _, err := tx.Exec(ctx, `UPDATE communities_community SET name=$2,tier=$3,category_id=$4,is_published=true,last_evaluated_at=$5 WHERE id=$1`, id, name, co.Tier, co.Category, now); err != nil {
					return err
				}
			}
			keep = append(keep, id)
			out.Published++
		}
		tag, err := tx.Exec(ctx, `UPDATE communities_community SET is_published=false,last_evaluated_at=$1 WHERE is_published AND NOT(id=ANY($2))`, now, keep)
		if err != nil {
			return err
		}
		out.Deactivated = tag.RowsAffected()
		return s.Audit(ctx, tx, Actor{}, "community.generated", "", map[string]any{"published": out.Published, "deactivated": out.Deactivated})
	})
	return out, err
}
