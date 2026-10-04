package social

import (
	"context"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

type PresencePurge struct {
	ArrivalTransit int64 `json:"arrival_transit"`
	Departure      int64 `json:"departure"`
}

func (s *Service) AutoComplete(ctx context.Context, now time.Time, grace time.Duration) (int64, error) {
	if grace < 0 {
		return 0, platform.ErrInvalid
	}
	var n int64
	err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE social_activity SET status='completed',updated_at=$1 WHERE status='open' AND COALESCE(ends_at,starts_at)<$2`, now, now.Add(-grace))
		if err != nil {
			return err
		}
		n = tag.RowsAffected()
		if s.Audit == nil {
			return platform.ErrForbidden
		}
		return s.Audit(ctx, tx, Actor{}, "activity.auto_completed", "", map[string]any{"completed": n})
	})
	return n, err
}
func (s *Service) ExpirePresence(ctx context.Context, now time.Time, retention time.Duration) (PresencePurge, error) {
	var out PresencePurge
	if retention < 0 {
		return out, platform.ErrInvalid
	}
	err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE social_membership m SET arrived_at=NULL,transit_status='none',updated_at=$1 FROM social_activity a WHERE a.id=m.activity_id AND a.starts_at<$2 AND (m.arrived_at IS NOT NULL OR m.transit_status<>'none')`, now, now.Add(-retention))
		if err != nil {
			return err
		}
		out.ArrivalTransit = tag.RowsAffected()
		tag, err = tx.Exec(ctx, `UPDATE social_membership m SET departing_at=NULL,updated_at=$1 FROM social_activity a WHERE a.id=m.activity_id AND COALESCE(a.ends_at,a.starts_at)<$2 AND m.departing_at IS NOT NULL`, now, now.Add(-retention))
		if err != nil {
			return err
		}
		out.Departure = tag.RowsAffected()
		return nil
	})
	return out, err
}
func (s *Service) ExpireGauges(ctx context.Context, now time.Time) (int64, error) {
	var total int64
	for batch := 0; batch < 100; batch++ {
		var deleted int64
		err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT id FROM social_activityinterest WHERE expires_at<$1 ORDER BY id LIMIT 1000 FOR UPDATE SKIP LOCKED`, now)
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
			if len(ids) == 0 {
				return nil
			}
			if _, err := tx.Exec(ctx, `DELETE FROM social_activityinterest_interested_users WHERE activityinterest_id=ANY($1)`, ids); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM saved_searches_savedsearchgaugematch WHERE interest_id=ANY($1)`, ids); err != nil {
				return err
			}
			tag, err := tx.Exec(ctx, `DELETE FROM social_activityinterest WHERE id=ANY($1)`, ids)
			if err != nil {
				return err
			}
			deleted = tag.RowsAffected()
			return nil
		})
		if err != nil {
			return total, err
		}
		total += deleted
		if deleted == 0 {
			break
		}
	}
	return total, nil
}
