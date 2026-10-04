package social

import (
	"context"
	"errors"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func (s *Service) ChildVenueSafe(ctx context.Context, id int64) (bool, error) {
	return authorizedResult(childVenue(ctx, s.DB, id))
}
func (s *Service) ChildVenueRationale(ctx context.Context, q platform.Querier, id int64) (string, error) {
	allowed, err := authorizedResult(childVenue(ctx, q, id))
	if err != nil || !allowed {
		return "", err
	}
	var staff bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM places_approvedchildvenue WHERE place_id=$1)`, id).Scan(&staff); err != nil {
		return "", err
	}
	if staff {
		return "staff_verified", nil
	}
	return "rule_match", nil
}
func (s *Service) EffectiveGuardrail(ctx context.Context, wardID int64) (EffectiveGuardrail, error) {
	return effectiveRail(ctx, s.DB, wardID)
}

// SupervisionSatisfied is a projection seam. The caller must first authorize
// the activity or the active-guardian ward manifest; this never grants access.
func (s *Service) SupervisionSatisfied(ctx context.Context, q platform.Querier, id int64) (bool, error) {
	var state activityState
	if err := q.QueryRow(ctx, `SELECT id,owner_id,supervised FROM social_activity WHERE id=$1`, id).Scan(&state.ID, &state.OwnerID, &state.Supervised); err != nil {
		return false, err
	}
	return supervisionSatisfied(ctx, q, state)
}

// GuardrailPreview diagnoses current limits for an authorized guardian. The
// source's first fifty visible upcoming candidates are bounded BEFORE joined/
// full exclusions; these exclusions do not count as a guardrail rejection.
func (s *Service) GuardrailPreview(ctx context.Context, guardian Actor, wardID int64, limit int) (map[string]int, error) {
	if guardian.ID < 1 || !guardian.IsActive {
		return nil, platform.ErrForbidden
	}
	var active bool
	if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_guardianrelationship WHERE guardian_id=$1 AND ward_id=$2 AND status='active')`, guardian.ID, wardID).Scan(&active); err != nil {
		return nil, err
	}
	if !active {
		return nil, platform.ErrForbidden
	}
	ward, err := actorByID(ctx, s.DB, wardID)
	if err != nil {
		return nil, err
	}
	if ward.Cohort != "child" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 50)
	rows, err := s.DB.Query(ctx, `SELECT a.id,a.capacity,(SELECT count(*) FROM social_membership m WHERE m.activity_id=a.id AND m.state='member' AND m.role<>'guardian'),EXISTS(SELECT 1 FROM social_membership m WHERE m.activity_id=a.id AND m.user_id=$1 AND m.state<>'removed') FROM social_activity a WHERE `+ActivityVisibilitySQL()+` AND a.status='open' AND a.starts_at>=now() ORDER BY a.starts_at,a.id LIMIT $3`, ward.ID, ward.Cohort, limit)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		var capacity *int
		var peers int
		var joined bool
		if err := rows.Scan(&id, &capacity, &peers, &joined); err != nil {
			rows.Close()
			return nil, err
		}
		if !joined && (capacity == nil || peers < *capacity) {
			ids = append(ids, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := map[string]int{"eligible": 0, "total": len(ids)}
	if err := platform.Participate(ctx, s.DB, ward); err != nil {
		if errors.Is(err, platform.ErrForbidden) {
			return out, nil
		}
		return nil, err
	}
	for _, id := range ids {
		allowed, err := s.CanJoin(ctx, ward, id)
		if err != nil {
			return nil, err
		}
		if allowed {
			out["eligible"]++
		}
	}
	return out, nil
}
