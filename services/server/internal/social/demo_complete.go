package social

import (
	"context"
	"fmt"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

// Development fixture helpers require an explicit caller gate and independently
// verify seeded identity/venue provenance. No production route invokes them.
func (s *Service) CompleteDemoActivity(ctx context.Context, actor Actor, id int64, development bool) error {
	if !development {
		return platform.ErrForbidden
	}
	return s.transaction(ctx, actor, func(tx pgx.Tx) error {
		v, err := activity(ctx, tx, actor, id, true)
		if err != nil {
			return err
		}
		var fixture bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_ageassurance WHERE user_id=$1 AND provider='dev' AND raw@>'{"demo_fixture":true}'::jsonb) AND EXISTS(SELECT 1 FROM places_place WHERE id=$2 AND raw_tags->>'demo_seed'='seed_demo_data')`, actor.ID, v.PlaceID).Scan(&fixture); err != nil {
			return err
		}
		if !fixture || v.OwnerID != actor.ID || v.Status != "open" || v.StartsAt.After(s.Now()) || v.EndsAt != nil && v.EndsAt.After(s.Now()) {
			return platform.ErrForbidden
		}
		if _, err = tx.Exec(ctx, `UPDATE social_activity SET status='completed',updated_at=now() WHERE id=$1`, id); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "demo.activity_completed", "activity", id, nil)
	})
}
func (s *Service) ApproveDemoVenue(ctx context.Context, staff Actor, id int64, development bool) error {
	if !development || !staff.IsActive || !staff.IsStaff {
		return platform.ErrForbidden
	}
	return s.transaction(ctx, staff, func(tx pgx.Tx) error {
		var fixture bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_ageassurance aa JOIN accounts_user u ON u.id=aa.user_id WHERE u.id=$1 AND u.is_active AND u.is_staff AND aa.provider='dev' AND aa.raw@>'{"demo_fixture":true}'::jsonb) AND EXISTS(SELECT 1 FROM places_place WHERE id=$2 AND raw_tags->>'demo_seed'='seed_demo_data')`, staff.ID, id).Scan(&fixture); err != nil {
			return err
		}
		if !fixture {
			return platform.ErrForbidden
		}
		var locked int64
		if err := tx.QueryRow(ctx, `SELECT id FROM places_place WHERE id=$1 FOR UPDATE`, id).Scan(&locked); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO places_approvedchildvenue(place_id,approved_by_id,note,created_at) VALUES($1,$2,'Development fixture venue only',now()) ON CONFLICT(place_id) DO NOTHING`, id, staff.ID); err != nil {
			return err
		}
		return s.audit(ctx, tx, staff, "demo.child_venue_approved", "place", id, map[string]string{"scope": fmt.Sprintf("development-fixture:%d", id)})
	})
}
