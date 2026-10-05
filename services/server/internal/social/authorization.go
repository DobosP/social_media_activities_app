package social

import (
	"context"
	"errors"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

func authorizedResult(err error) (bool, error) {
	if err == nil {
		return true, nil
	}
	if errors.Is(err, platform.ErrForbidden) || errors.Is(err, platform.ErrInvalid) || errors.Is(err, platform.ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return false, err
}
func threadFromID(ctx context.Context, q platform.Querier, a Actor, threadID int64) (threadState, error) {
	var activityID, groupID int64
	if err := q.QueryRow(ctx, `SELECT COALESCE(activity_id,0),COALESCE(group_id,0) FROM social_thread WHERE id=$1`, threadID).Scan(&activityID, &groupID); err != nil {
		return threadState{}, err
	}
	kind, id := "activity", activityID
	if groupID > 0 {
		kind, id = "group", groupID
	}
	if id < 1 || activityID > 0 && groupID > 0 {
		return threadState{}, platform.ErrNotFound
	}
	return threadOwner(ctx, q, a, kind, id, false)
}

// These adapters are shared with media and live transport, so no endpoint can
// broaden membership, cohort, consent or mutual-block authorization.
func (s *Service) CanReadThread(ctx context.Context, q platform.Querier, a Actor, threadID int64) (bool, error) {
	v, err := threadFromID(ctx, q, a, threadID)
	if err != nil {
		return authorizedResult(err)
	}
	return authorizedResult(threadGate(ctx, q, a, v, false))
}
func (s *Service) CanWriteThread(ctx context.Context, q platform.Querier, a Actor, threadID int64) (bool, error) {
	v, err := threadFromID(ctx, q, a, threadID)
	if err != nil {
		return authorizedResult(err)
	}
	return authorizedResult(threadGate(ctx, q, a, v, true))
}
func (s *Service) CanSeeActivity(ctx context.Context, q platform.Querier, a Actor, id int64) (bool, error) {
	_, err := activity(ctx, q, a, id, false)
	return authorizedResult(err)
}

// ActivityCoverVisibilitySQL is the canonical ordinary-viewer clause for a
// batched media projection. Both aliases must be freshly selected DB rows:
// activity a, viewer v. Staff and anonymous publication policy belong to media.
func (s *Service) ActivityCoverVisibilitySQL() string {
	return `v.is_active AND v.cohort<>'' AND v.cohort<>'unassigned' AND a.cohort=v.cohort AND NOT a.is_hidden AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=v.id AND b.blocked_id=a.owner_id) OR (b.blocker_id=a.owner_id AND b.blocked_id=v.id))`
}

// CanSeeUser is the user-report target gate: Profile's indistinguishable vetoes
// (inactive or unassigned either side, cross-cohort, self, blocked either way)
// for a freshly reloaded viewer. It admits nothing beyond the minimal card.
func (s *Service) CanSeeUser(ctx context.Context, q platform.Querier, a Actor, id int64) (bool, error) {
	if a.ID < 1 {
		return false, nil
	}
	fresh, err := actorByID(ctx, q, a.ID)
	if err != nil {
		return authorizedResult(err)
	}
	b, err := actorByID(ctx, q, id)
	if err != nil {
		return authorizedResult(err)
	}
	if !pairVisible(fresh, b) {
		return false, nil
	}
	blocked, err := platform.Blocked(ctx, q, fresh.ID, b.ID)
	return !blocked && err == nil, err
}
func (s *Service) CanViewProfilePhoto(ctx context.Context, q platform.Querier, a Actor, ownerID int64) (bool, error) {
	if a.ID == ownerID && a.IsActive {
		return true, nil
	}
	b, err := actorByID(ctx, q, ownerID)
	if err != nil {
		return authorizedResult(err)
	}
	if !pairVisible(a, b) {
		return false, nil
	}
	blocked, err := platform.Blocked(ctx, q, a.ID, b.ID)
	return !blocked, err
}

// CanJoin projects the same capacity, existing-seat and child guardrail gates as
// Join. It only reads; a true affordance never reserves a seat or weakens the
// locked recheck performed when the user submits their request.
func (s *Service) CanJoin(ctx context.Context, a Actor, id int64) (bool, error) {
	if a.ID < 1 {
		return false, nil
	}
	fresh, err := actorByID(ctx, s.DB, a.ID)
	if err != nil {
		return authorizedResult(err)
	}
	if err := platform.Participate(ctx, s.DB, fresh); err != nil {
		return authorizedResult(err)
	}
	v, err := activity(ctx, s.DB, fresh, id, false)
	if err != nil {
		return authorizedResult(err)
	}
	if v.Status != "open" {
		return false, nil
	}
	if v.Capacity != nil {
		n, err := participantCount(ctx, s.DB, id)
		if err != nil {
			return false, err
		}
		if n >= *v.Capacity {
			return false, nil
		}
	}
	var existing bool
	if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM social_membership WHERE activity_id=$1 AND user_id=$2 AND state<>'removed')`, id, fresh.ID).Scan(&existing); err != nil {
		return false, err
	}
	if existing {
		return false, nil
	}
	return authorizedResult(childJoinGate(ctx, s.DB, fresh, v))
}

// SupervisorPresent is an internal UI helper; callers first authorize the
// activity through CanSeeActivity. Presence always requires a current eligible
// active guardian of the owner, even when supervision was optional.
func (s *Service) SupervisorPresent(ctx context.Context, id int64) (bool, error) {
	var owner int64
	if err := s.DB.QueryRow(ctx, `SELECT owner_id FROM social_activity WHERE id=$1`, id).Scan(&owner); err != nil {
		return false, err
	}
	return supervisionSatisfied(ctx, s.DB, activityState{ID: id, OwnerID: owner, Supervised: true})
}
