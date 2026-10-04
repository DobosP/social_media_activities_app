package catalog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

func (s *Service) EventReliability(ctx context.Context, id int64) (any, error) {
	var count int
	err := s.DB.QueryRow(ctx, `SELECT count(*) FROM events_eventreport WHERE event_id=$1 AND created_at>=now()-interval '14 days'`, id).Scan(&count)
	if count >= 3 {
		return "unverified", err
	}
	return nil, err
}
func (s *Service) ReportEvent(ctx context.Context, a platform.Actor, id int64, kind string) (bool, error) {
	if !map[string]bool{"cancelled": true, "moved": true, "wrong_time": true}[kind] {
		return false, platform.ErrInvalid
	}
	created := false
	err := s.rateTransaction(ctx, a, "event_report", 10, time.Hour, func(tx pgx.Tx, reserve func() error) error {
		if err := platform.Participate(ctx, tx, a); err != nil {
			return err
		}
		var event int64
		if err := tx.QueryRow(ctx, `SELECT e.id FROM events_event e LEFT JOIN places_place p ON p.id=e.place_id WHERE e.id=$1 AND `+publicEventSQL+` AND e.starts_at>=now() AND e.lifecycle_status IN('scheduled','rescheduled','sold_out') FOR UPDATE OF e`, id).Scan(&event); err != nil {
			return err
		}
		if err := reserve(); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM events_eventreport WHERE event_id=$1 AND reporter_id=$2 AND created_at>=now()-interval '14 days')`, id, a.ID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil
		}
		if _, err := tx.Exec(ctx, `INSERT INTO events_eventreport(event_id,reporter_id,kind,created_at) VALUES($1,$2,$3,now())`, id, a.ID, kind); err != nil {
			return err
		}
		created = true
		return nil
	})
	if errors.Is(err, budgets.ErrDenied) {
		return false, nil
	}
	return created, err
}
func (s *Service) ClearEventReports(ctx context.Context, a platform.Actor, id int64) (int64, error) {
	if !a.IsActive || !a.IsStaff {
		return 0, platform.ErrForbidden
	}
	var count int64
	err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		var event int64
		if err := tx.QueryRow(ctx, `SELECT id FROM events_event WHERE id=$1 FOR UPDATE`, id).Scan(&event); err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `DELETE FROM events_eventreport WHERE event_id=$1`, id)
		if err != nil {
			return err
		}
		count = result.RowsAffected()
		return platform.RecordAudit(ctx, tx, a, "event.reports_cleared", fmt.Sprintf("events.event:%d", id), nil)
	})
	return count, err
}
