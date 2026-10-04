package admin

import (
	"context"
	"fmt"
	"strings"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

func (s *Service) ReviewEvent(ctx context.Context, a platform.Actor, id int64, release bool, reason string) error {
	if err := s.Gate(ctx, a); err != nil {
		return err
	}
	if strings.TrimSpace(reason) == "" || len([]rune(reason)) > 2000 {
		return platform.ErrInvalid
	}
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if err := s.gateTx(ctx, tx, a); err != nil {
			return err
		}
		var source, status, pack, license, provenance string
		var tombstone bool
		var confidence *float64
		var place *int64
		if err := tx.QueryRow(ctx, `SELECT source,lifecycle_status,source_pack_id,license_name,provenance_url,is_tombstone,source_confidence,place_id FROM events_event WHERE id=$1 FOR UPDATE`, id).Scan(&source, &status, &pack, &license, &provenance, &tombstone, &confidence, &place); err != nil {
			return err
		}
		if release {
			if tombstone || status != "scheduled" && status != "rescheduled" && status != "sold_out" {
				return platform.ErrForbidden
			}
			if source == "roedu" && (pack != "roedu:social_media_activities_app:events_places:v1" || confidence == nil || *confidence < 1.0 || license == "") {
				return platform.ErrForbidden
			}
			if place != nil {
				var public bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM places_place p WHERE p.id=$1 AND `+catalog.PolicyFromContext(ctx).PlaceSQL()+`)`, *place).Scan(&public); err != nil {
					return err
				}
				if !public {
					return platform.ErrForbidden
				}
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE events_event SET is_import_held=$2,updated_at=now() WHERE id=$1`, id, !release); err != nil {
			return err
		}
		event := "events.import_held"
		if release {
			event = "events.import_reviewed"
		}
		return platform.RecordAudit(ctx, tx, a, event, fmt.Sprintf("events.event:%d", id), map[string]string{"reason": reason})
	})
}
