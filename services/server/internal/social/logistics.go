package social

import (
	"context"
	"fmt"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

func (s *Service) MetConfirmed(ctx context.Context, a Actor, id int64, confirmed bool) error {
	return s.transaction(ctx, a, func(tx pgx.Tx) error {
		v, err := activity(ctx, tx, a, id, true)
		if err != nil {
			return err
		}
		if v.Status != "completed" {
			return platform.ErrInvalid
		}
		tag, err := tx.Exec(ctx, `UPDATE social_membership SET met_confirmed_at=CASE WHEN $3 THEN COALESCE(met_confirmed_at,now()) ELSE NULL END,updated_at=now() WHERE activity_id=$1 AND user_id=$2 AND state='member'`, id, a.ID, confirmed)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return platform.ErrForbidden
		}
		return nil
	})
}
func (s *Service) SupportCompanion(ctx context.Context, a Actor, id int64, brings bool) error {
	return s.transaction(ctx, a, func(tx pgx.Tx) error {
		if a.Cohort != "adult" {
			return platform.ErrForbidden
		}
		if _, err := activity(ctx, tx, a, id, true); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE social_membership SET brings_support_person=$3,updated_at=now() WHERE activity_id=$1 AND user_id=$2 AND state='member' AND role<>'guardian'`, id, a.ID, brings)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return platform.ErrForbidden
		}
		return nil
	})
}
func (s *Service) MoveActivity(ctx context.Context, a Actor, id, placeID int64) error {
	return s.transaction(ctx, a, func(tx pgx.Tx) error {
		v, err := activity(ctx, tx, a, id, true)
		if err != nil {
			return err
		}
		yes, err := organizer(ctx, tx, a, v)
		if err := errorIfFalse(yes, err); err != nil {
			return err
		}
		if v.Status != "open" || !v.StartsAt.After(s.Now()) {
			return platform.ErrInvalid
		}
		if v.PlaceID == placeID {
			return nil
		}
		if err := publicPlace(ctx, tx, a, placeID, v.Cohort == "adult" && !v.GuardianAccompanied); err != nil {
			return err
		}
		if v.Cohort == "child" {
			if err := childVenue(ctx, tx, placeID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE social_activity SET place_id=$2,updated_at=now() WHERE id=$1`, id, placeID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM notifications_notification WHERE kind='event_reminder' AND url=$1`, fmt.Sprintf("/api/social/activities/%d/", id)); err != nil {
			return err
		}
		if err := s.fanout(ctx, tx, a, v, "activity_updated", "An activity you joined changed", "The meetup venue changed."); err != nil {
			return err
		}
		return s.audit(ctx, tx, a, "activity.moved", "activity", id, nil)
	})
}

var GroupPrompts = map[string]string{"next_meetup": "When is the next meetup?", "where": "Where exactly do we meet?", "what_to_bring": "What should I bring?", "how_it_works": "I'm new here — how does this group work?", "more_info": "Could you post more about what's coming up?"}

func (s *Service) AskGroup(ctx context.Context, a Actor, id int64, prompt string) (bool, error) {
	message, ok := GroupPrompts[prompt]
	if !ok {
		return false, platform.ErrInvalid
	}
	var sent bool
	err := s.transaction(ctx, a, func(tx pgx.Tx) error {
		v, err := group(ctx, tx, a, id, true, false)
		if err != nil {
			return err
		}
		if v.Cohort != "child" && v.Cohort != "teen" {
			return platform.ErrForbidden
		}
		var role string
		if err := tx.QueryRow(ctx, `SELECT role FROM social_groupmembership WHERE group_id=$1 AND user_id=$2 AND state='member'`, id, a.ID).Scan(&role); err != nil {
			return err
		}
		if role != "member" {
			return platform.ErrForbidden
		}
		var curated, staff bool
		if err := tx.QueryRow(ctx, `SELECT g.is_staff_curated,u.is_staff FROM social_group g JOIN accounts_user u ON u.id=g.owner_id WHERE g.id=$1`, id).Scan(&curated, &staff); err != nil {
			return err
		}
		if !curated || !staff || !s.allow(a.ID, "group_question", 6, time.Hour) {
			return platform.ErrForbidden
		}
		if err := s.audit(ctx, tx, a, "group.question_asked", "group", id, map[string]string{"prompt": prompt}); err != nil {
			return err
		}
		if s.Notify == nil {
			return platform.ErrForbidden
		}
		sent, err = s.Notify(ctx, tx, v.OwnerID, "group_question", "New question in "+v.Title, "A member asks: "+message, fmt.Sprintf("/groups/%d/", id))
		return err
	})
	return sent, err
}
