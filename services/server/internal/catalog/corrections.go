package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

func (s *Service) ProposeCorrection(ctx context.Context, a platform.Actor, placeID int64, field, value string) (int64, error) {
	if field != "name" && field != "address" && field != "hours" {
		return 0, platform.ErrInvalid
	}
	value = strings.TrimSpace(value)
	if len([]rune(value)) > 255 {
		value = string([]rune(value)[:255])
	}
	if value == "" || field == "hours" && ParseOpeningHours(value) == nil {
		return 0, platform.ErrInvalid
	}
	var id int64
	err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if err := platform.Participate(ctx, tx, a); err != nil {
			return err
		}
		if err := s.publicVenue(ctx, tx, placeID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT id FROM places_place WHERE id=$1 FOR UPDATE`, placeID); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `INSERT INTO places_placecorrection(place_id,proposer_id,field,proposed_value,required_confirmations,status,created_at,published_at) VALUES($1,$2,$3,$4,$5,'pending',now(),NULL) RETURNING id`, placeID, a.ID, field, value, s.policy().CorrectionQuorum).Scan(&id)
		if err != nil {
			return err
		}
		return platform.RecordAudit(ctx, tx, a, "place.correction_proposed", fmt.Sprintf("places.place:%d", placeID), map[string]string{"field": field})
	})
	return id, err
}
func publishCorrection(ctx context.Context, tx pgx.Tx, id, place int64, field string) error {
	if _, err := tx.Exec(ctx, `UPDATE places_placecorrection SET status='published',published_at=now() WHERE id=$1`, id); err != nil {
		return err
	}
	if field == "hours" {
		if _, err := tx.Exec(ctx, `DELETE FROM places_opennowreport WHERE place_id=$1`, place); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) ConfirmCorrection(ctx context.Context, a platform.Actor, id int64) error {
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if err := platform.Participate(ctx, tx, a); err != nil {
			return err
		}
		var proposer, place int64
		var state, field string
		var quorum int
		if err := tx.QueryRow(ctx, `SELECT proposer_id,place_id,status,field,required_confirmations FROM places_placecorrection WHERE id=$1 FOR UPDATE`, id).Scan(&proposer, &place, &state, &field, &quorum); err != nil {
			return err
		}
		if proposer == a.ID || state != "pending" {
			return platform.ErrInvalid
		}
		if _, err := tx.Exec(ctx, `INSERT INTO places_placecorrectionconfirmation(correction_id,user_id,created_at) VALUES($1,$2,now()) ON CONFLICT(correction_id,user_id) DO NOTHING`, id, a.ID); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM places_placecorrectionconfirmation WHERE correction_id=$1`, id).Scan(&n); err != nil {
			return err
		}
		if n >= quorum {
			if err := publishCorrection(ctx, tx, id, place, field); err != nil {
				return err
			}
		}
		return platform.RecordAudit(ctx, tx, a, "place.correction_confirmed", fmt.Sprintf("places.place:%d", place), nil)
	})
}
func (s *Service) StaffCorrection(ctx context.Context, a platform.Actor, id int64, publish bool, reason string) error {
	if !a.IsActive || !a.IsStaff {
		return platform.ErrForbidden
	}
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		var place int64
		var state, field string
		if err := tx.QueryRow(ctx, `SELECT place_id,status,field FROM places_placecorrection WHERE id=$1 FOR UPDATE`, id).Scan(&place, &state, &field); err != nil {
			return err
		}
		event := "place.correction_rejected"
		if publish {
			if state != "pending" {
				return platform.ErrInvalid
			}
			if err := publishCorrection(ctx, tx, id, place, field); err != nil {
				return err
			}
			event = "place.correction_published"
		} else {
			if state == "rejected" {
				return platform.ErrInvalid
			}
			if _, err := tx.Exec(ctx, `UPDATE places_placecorrection SET status='rejected',published_at=NULL WHERE id=$1`, id); err != nil {
				return err
			}
		}
		return platform.RecordAudit(ctx, tx, a, event, fmt.Sprintf("places.place:%d", place), map[string]string{"reason": reason})
	})
}
func (s *Service) ReportVenue(ctx context.Context, a platform.Actor, place int64, closure bool) (bool, error) {
	kind, table := "open_now_report", "places_opennowreport"
	decay := s.policy().OpenNowReportDecay
	if closure {
		kind, table = "place_closure_report", "places_placeclosurereport"
		decay = s.policy().ClosureReportDecay
	}
	created := false
	err := s.rateTransaction(ctx, a, kind, 10, time.Hour, func(tx pgx.Tx, reserve func() error) error {
		if err := platform.Participate(ctx, tx, a); err != nil {
			return err
		}
		if err := reserve(); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT id FROM places_place WHERE id=$1 FOR UPDATE`, place); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+table+` WHERE place_id=$1 AND reporter_id=$2 AND created_at>=now()-$3*interval '1 second')`, place, a.ID, int64(decay/time.Second)).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil
		}
		if _, err := tx.Exec(ctx, `INSERT INTO `+table+`(place_id,reporter_id,created_at) VALUES($1,$2,now())`, place, a.ID); err != nil {
			return err
		}
		created = true
		return platform.RecordAudit(ctx, tx, a, "place."+kind, fmt.Sprintf("places.place:%d", place), nil)
	})
	if errors.Is(err, budgets.ErrDenied) {
		return false, nil
	}
	return created, err
}
func (s *Service) ClearVenueReports(ctx context.Context, a platform.Actor, place int64, closure bool) (int64, error) {
	if !a.IsActive || !a.IsStaff {
		return 0, platform.ErrForbidden
	}
	table, event := "places_opennowreport", "place.open_now_reports_cleared"
	if closure {
		table, event = "places_placeclosurereport", "place.closure_reports_cleared"
	}
	var n int64
	err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM `+table+` WHERE place_id=$1`, place)
		if err != nil {
			return err
		}
		n = tag.RowsAffected()
		return platform.RecordAudit(ctx, tx, a, event, fmt.Sprintf("places.place:%d", place), nil)
	})
	return n, err
}
func (s *Service) ReverseEdge(ctx context.Context, a platform.Actor, id int64, action string) error {
	if !a.IsActive || !a.IsStaff {
		return platform.ErrForbidden
	}
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		var place int64
		if err := tx.QueryRow(ctx, `SELECT place_id FROM places_placeactivity WHERE id=$1 FOR UPDATE`, id).Scan(&place); err != nil {
			return err
		}
		switch action {
		case "demote":
			if _, err := tx.Exec(ctx, `UPDATE places_placeactivity SET origin='inferred',is_disputed=false,updated_at=now() WHERE id=$1`, id); err != nil {
				return err
			}
		case "restore", "reset":
			if _, err := tx.Exec(ctx, `UPDATE places_placeactivity SET is_disputed=false,updated_at=now() WHERE id=$1`, id); err != nil {
				return err
			}
		default:
			return platform.ErrInvalid
		}
		query := `DELETE FROM places_activityedgevote WHERE edge_id=$1`
		if action == "restore" {
			query += ` AND vote='dispute'`
		}
		if _, err := tx.Exec(ctx, query, id); err != nil {
			return err
		}
		return platform.RecordAudit(ctx, tx, a, "place.edge_"+action, fmt.Sprintf("places.place:%d", place), nil)
	})
}
