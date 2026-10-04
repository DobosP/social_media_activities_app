package social

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

func publicPlace(ctx context.Context, q platform.Querier, a Actor, id int64, ownPending bool) error {
	ok, err := scalar(ctx, q, `SELECT EXISTS(SELECT 1 FROM places_place p WHERE p.id=$1 AND (`+catalog.PolicyFromContext(ctx).PlaceSQL()+` OR ($2 AND EXISTS(SELECT 1 FROM social_userplaceproposal pp WHERE pp.place_id=p.id AND pp.status='pending' AND pp.proposer_id=$3))))`, id, ownPending && a.Cohort == "adult", a.ID)
	return errorIfFalse(ok, err)
}
func (s *Service) CreateActivity(ctx context.Context, a Actor, in ActivityInput) (int64, error) {
	if err := in.validate(); err != nil {
		return 0, err
	}
	var id int64
	err := s.transaction(ctx, a, func(tx pgx.Tx) error { return s.createActivity(ctx, tx, a, in, nil, &id) })
	return id, err
}
func (s *Service) createActivity(ctx context.Context, tx pgx.Tx, a Actor, in ActivityInput, seriesID *int64, id *int64) error {
	if in.Supervised {
		if a.Cohort != "child" {
			return platform.ErrInvalid
		}
		in.GuardianAccompanied = true
	}
	if in.GuardianAccompanied && a.Cohort != "child" {
		return platform.ErrInvalid
	}
	if err := childCreateGate(ctx, tx, a, in); err != nil {
		return err
	}
	if err := publicPlace(ctx, tx, a, in.Place, seriesID == nil); err != nil {
		return err
	}
	ok, err := scalar(ctx, tx, `SELECT EXISTS(SELECT 1 FROM taxonomy_activitytype WHERE id=$1 AND is_active)`, in.ActivityType)
	if err := errorIfFalse(ok, err); err != nil {
		return err
	}
	threshold := 2.0 / 3.0
	if in.JoinThreshold != nil {
		threshold = *in.JoinThreshold
	}
	err = tx.QueryRow(ctx, `INSERT INTO social_activity(owner_id,place_id,activity_type_id,title,description,starts_at,ends_at,cohort,join_threshold,capacity,min_to_go,guardian_accompanied,supervised,meeting_point,what_to_bring,organizer_note,first_time_note,cost_band,cost_amount,cost_note,difficulty,accessibility_notes,beginners_welcome,status,is_hidden,is_publicly_listed,owner_can_override,go_confirmed_at,series_id,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$21,$22,$12,$13,$14,$15,$16,$23::text::numeric,$24,$17,$18,$19,'open',false,false,true,NULL,$20,now(),now()) RETURNING id`, a.ID, in.Place, in.ActivityType, in.Title, in.Description, in.StartsAt, in.EndsAt, a.Cohort, threshold, in.Capacity, in.MinToGo, in.MeetingPoint, in.WhatToBring, in.OrganizerNote, in.FirstTimeNote, in.CostBand, in.Difficulty, in.AccessibilityNotes, in.BeginnersWelcome, seriesID, in.GuardianAccompanied, in.Supervised, in.CostAmount, in.CostNote).Scan(id)
	if err != nil {
		return err
	}
	extra, err := secondaryTypes(ctx, tx, a, in.ActivityType, in.SecondaryTypes)
	if err != nil {
		return err
	}
	if err := replaceSecondary(ctx, tx, *id, extra); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO social_membership(activity_id,user_id,role,state,attendance_intent,transit_status,brings_support_person,created_at,updated_at,decided_at) VALUES($1,$2,'owner','member','unknown','none',false,now(),now(),now())`, *id, a.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO social_thread(activity_id,group_id,created_at) VALUES($1,NULL,now())`, *id); err != nil {
		return err
	}
	return s.audit(ctx, tx, a, "activity.created", "activity", *id, nil)
}

func (s *Service) UpdateActivity(ctx context.Context, a Actor, id int64, changes map[string]json.RawMessage) error {
	return s.transaction(ctx, a, func(tx pgx.Tx) error {
		v, err := activity(ctx, tx, a, id, true)
		if err != nil {
			return err
		}
		ok, err := organizer(ctx, tx, a, v)
		if err := errorIfFalse(ok, err); err != nil {
			return err
		}
		if v.Status != "open" || !v.StartsAt.After(s.Now()) {
			return platform.ErrInvalid
		}
		values := map[string]any{}
		secondaryChanged := false
		var extras []int64
		newStart, newEnd, newCap, newMin := v.StartsAt, v.EndsAt, v.Capacity, v.MinToGo
		for field, raw := range changes {
			switch field {
			case "secondary_types":
				var ids []int64
				if json.Unmarshal(raw, &ids) != nil {
					return platform.ErrInvalid
				}
				extras, err = secondaryTypes(ctx, tx, a, v.TypeID, ids)
				if err != nil {
					return err
				}
				secondaryChanged = true
			case "cost_note":
				value, err := text(raw, 120, false)
				if err != nil {
					return err
				}
				values[field] = value
			case "cost_amount":
				if string(raw) == "null" {
					values[field] = nil
				} else {
					var amount Decimal
					if json.Unmarshal(raw, &amount) != nil {
						return platform.ErrInvalid
					}
					values[field] = string(amount)
				}
			case "title", "description", "meeting_point", "what_to_bring", "organizer_note", "first_time_note", "accessibility_notes":
				cap := 500
				if field == "title" {
					cap = 200
				}
				if field == "description" {
					cap = 2000
				}
				value, err := text(raw, cap, field == "title")
				if err != nil {
					return err
				}
				values[field] = value
			case "cost_band", "difficulty":
				value, err := text(raw, 16, true)
				if err != nil {
					return err
				}
				choices := "|unspecified|free|low|paid|"
				if field == "difficulty" {
					choices = "|unspecified|easy|moderate|challenging|"
				}
				if !strings.Contains(choices, "|"+value+"|") {
					return platform.ErrInvalid
				}
				values[field] = value
			case "starts_at":
				value, err := dateValue(raw)
				if err != nil {
					return err
				}
				values[field] = value
				newStart = value
			case "ends_at":
				value, err := optionalDate(raw)
				if err != nil {
					return err
				}
				values[field] = value
				newEnd = value
			case "capacity", "min_to_go":
				value, err := nullableInt(raw)
				if err != nil {
					return err
				}
				values[field] = value
				if field == "capacity" {
					newCap = value
				} else {
					newMin = value
				}
			case "beginners_welcome":
				value, err := boolValue(raw)
				if err != nil {
					return err
				}
				values[field] = value
			default:
				return platform.ErrInvalid // Cohort, owner, place, type and listing cannot move through PATCH.
			}
		}
		if newEnd != nil && newEnd.Before(newStart) || newCap != nil && newMin != nil && *newMin > *newCap {
			return platform.ErrInvalid
		}
		if newCap != nil {
			taken, err := participantCount(ctx, tx, id)
			if err != nil {
				return err
			}
			if *newCap < taken {
				return platform.ErrInvalid
			}
		}
		var amount *string
		var band string
		if err := tx.QueryRow(ctx, `SELECT cost_amount::text,cost_band FROM social_activity WHERE id=$1`, id).Scan(&amount, &band); err != nil {
			return err
		}
		if raw, ok := values["cost_amount"]; ok {
			amount = nil
			if raw != nil {
				value := raw.(string)
				amount = &value
			}
		}
		if raw, ok := values["cost_band"]; ok {
			band = raw.(string)
		}
		if amount != nil && band != "low" && band != "paid" {
			return platform.ErrInvalid
		}
		if secondaryChanged {
			if err := replaceSecondary(ctx, tx, id, extras); err != nil {
				return err
			}
		}
		if len(values) == 0 {
			if secondaryChanged {
				return s.audit(ctx, tx, a, "activity.updated", "activity", id, nil)
			}
			return nil
		}
		keys := []string{}
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		args := []any{id}
		sets := []string{"updated_at=now()"}
		for _, key := range keys {
			args = append(args, values[key])
			expression := key + "=$" + strconv.Itoa(len(args))
			if key == "cost_amount" {
				expression += "::text::numeric"
			}
			sets = append(sets, expression)
		}
		if _, err := tx.Exec(ctx, `UPDATE social_activity SET `+strings.Join(sets, ",")+` WHERE id=$1`, args...); err != nil {
			return err
		}
		if _, present := values["starts_at"]; present && !newStart.Equal(v.StartsAt) {
			if _, err := tx.Exec(ctx, `DELETE FROM notifications_notification WHERE kind='event_reminder' AND url=$1`, fmt.Sprintf("/api/social/activities/%d/", id)); err != nil {
				return err
			}
			if err := s.fanout(ctx, tx, a, v, "activity_updated", "An activity you joined changed", fmt.Sprintf("“%s” now starts %s.", v.Title, newStart.Format("2006-01-02 15:04"))); err != nil {
				return err
			}
		}
		if err := s.confirmQuorum(ctx, tx, a, v); err != nil {
			return err
		}
		return s.audit(ctx, tx, a, "activity.updated", "activity", id, nil)
	})
}
func (s *Service) CancelActivity(ctx context.Context, a Actor, id int64, reason string) error {
	return s.privacyTransaction(ctx, a, func(tx pgx.Tx) error {
		v, err := activity(ctx, tx, a, id, true)
		if err != nil {
			return err
		}
		ok, err := organizer(ctx, tx, a, v)
		if err := errorIfFalse(ok, err); err != nil {
			return err
		}
		if v.Status != "open" {
			return platform.ErrInvalid
		}
		reason = strings.TrimSpace(reason)
		runes := []rune(reason)
		if len(runes) > 200 {
			reason = string(runes[:200])
		}
		if _, err = tx.Exec(ctx, `UPDATE social_activity SET status='cancelled',updated_at=now() WHERE id=$1`, id); err != nil {
			return err
		}
		if err = s.fanout(ctx, tx, a, v, "activity_cancelled", "An activity was cancelled", fmt.Sprintf("“%s” was cancelled by the organiser. %s", v.Title, reason)); err != nil {
			return err
		}
		return s.audit(ctx, tx, a, "activity.cancelled", "activity", id, map[string]string{"reason": reason})
	})
}
func (s *Service) SetActivityListing(ctx context.Context, a Actor, id int64, listed bool) error {
	return s.transaction(ctx, a, func(tx pgx.Tx) error {
		v, err := activity(ctx, tx, a, id, true)
		if err != nil {
			return err
		}
		if v.OwnerID != a.ID || v.Cohort != "adult" {
			return platform.ErrForbidden
		}
		if _, err = tx.Exec(ctx, `UPDATE social_activity SET is_publicly_listed=$2 WHERE id=$1`, id, listed); err != nil {
			return err
		}
		return s.audit(ctx, tx, a, "activity.public_listing", "activity", id, map[string]bool{"listed": listed})
	})
}

func (s *Service) Join(ctx context.Context, a Actor, id int64) (int64, error) {
	var membershipID int64
	err := s.transaction(ctx, a, func(tx pgx.Tx) error {
		v, err := activity(ctx, tx, a, id, true)
		if err != nil {
			return err
		}
		if v.Status != "open" {
			return platform.ErrForbidden
		}
		if err := childJoinGate(ctx, tx, a, v); err != nil {
			return err
		}
		n, err := participantCount(ctx, tx, id)
		if err != nil {
			return err
		}
		if v.Capacity != nil && n >= *v.Capacity {
			return platform.ErrForbidden
		}
		var existingState string
		err = tx.QueryRow(ctx, `SELECT state FROM social_membership WHERE activity_id=$1 AND user_id=$2`, id, a.ID).Scan(&existingState)
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
		if err == nil && existingState != "removed" {
			return platform.ErrForbidden
		}
		err = tx.QueryRow(ctx, `INSERT INTO social_membership(activity_id,user_id,role,state,attendance_intent,transit_status,brings_support_person,created_at,updated_at,decided_at) VALUES($1,$2,'member','requested','unknown','none',false,now(),now(),NULL) ON CONFLICT(activity_id,user_id) DO UPDATE SET role='member',state='requested',attendance_intent='unknown',transit_status='none',arrived_at=NULL,departing_at=NULL,met_confirmed_at=NULL,brings_support_person=false,decided_at=NULL,updated_at=now() RETURNING id`, id, a.ID).Scan(&membershipID)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM social_joinvote WHERE membership_id=$1`, membershipID); err != nil {
			return err
		}
		if err = s.notify(ctx, tx, v.OwnerID, "join_requested", "New join request", fmt.Sprintf("%s asked to join “%s”.", a.DisplayName, v.Title), fmt.Sprintf("/api/social/activities/%d/", id)); err != nil {
			return err
		}
		return s.audit(ctx, tx, a, "activity.join_requested", "membership", membershipID, nil)
	})
	return membershipID, err
}
func (s *Service) Leave(ctx context.Context, a Actor, id int64) (int64, error) {
	var mid int64
	err := s.privacyTransaction(ctx, a, func(tx pgx.Tx) error {
		if _, err := activity(ctx, tx, a, id, true); err != nil {
			return err
		}
		var role, state string
		if err := tx.QueryRow(ctx, `SELECT id,role,state FROM social_membership WHERE activity_id=$1 AND user_id=$2 FOR UPDATE`, id, a.ID).Scan(&mid, &role, &state); err != nil {
			return err
		}
		if state == "removed" || role == "owner" {
			return platform.ErrInvalid
		}
		if _, err := tx.Exec(ctx, `UPDATE social_membership SET state='removed',attendance_intent='unknown',met_confirmed_at=NULL,transit_status='none',departing_at=NULL,arrived_at=NULL,brings_support_person=false,updated_at=now() WHERE id=$1`, mid); err != nil {
			return err
		}
		return s.audit(ctx, tx, a, "activity.left", "membership", mid, nil)
	})
	return mid, err
}

func actorByID(ctx context.Context, q platform.Querier, id int64) (Actor, error) {
	var a Actor
	err := q.QueryRow(ctx, `SELECT id,public_id::text,username,display_name,age_band,cohort,role,is_identity_verified,is_active,is_staff,is_superuser FROM accounts_user WHERE id=$1`, id).Scan(&a.ID, &a.PublicID, &a.Username, &a.DisplayName, &a.AgeBand, &a.Cohort, &a.Role, &a.IdentityVerified, &a.IsActive, &a.IsStaff, &a.IsSuperuser)
	return a, err
}
func (s *Service) Vote(ctx context.Context, a Actor, mid int64, approve bool, override bool) error {
	return s.transaction(ctx, a, func(tx pgx.Tx) error {
		var activityID int64
		if err := tx.QueryRow(ctx, `SELECT activity_id FROM social_membership WHERE id=$1`, mid).Scan(&activityID); err != nil {
			return err
		}
		v, err := activity(ctx, tx, a, activityID, true)
		if err != nil {
			return err
		}
		var targetID int64
		var state string
		if err := tx.QueryRow(ctx, `SELECT user_id,state FROM social_membership WHERE id=$1 FOR UPDATE`, mid).Scan(&targetID, &state); err != nil {
			return err
		}
		if state != "requested" || v.Status != "open" {
			return platform.ErrInvalid
		}
		if override {
			ok, err := organizer(ctx, tx, a, v)
			if err := errorIfFalse(ok && v.Override, err); err != nil {
				return err
			}
		} else {
			if targetID == a.ID {
				return platform.ErrInvalid
			}
			ok, err := scalar(ctx, tx, `SELECT EXISTS(SELECT 1 FROM social_membership WHERE activity_id=$1 AND user_id=$2 AND state='member' AND role<>'guardian')`, v.ID, a.ID)
			if err := errorIfFalse(ok, err); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO social_joinvote(membership_id,voter_id,approve,created_at) VALUES($1,$2,$3,now()) ON CONFLICT(membership_id,voter_id) DO UPDATE SET approve=EXCLUDED.approve`, mid, a.ID, approve); err != nil {
				return err
			}
		}
		var approvals int
		if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM social_joinvote WHERE membership_id=$1 AND approve`, mid).Scan(&approvals); err != nil {
			return err
		}
		members, err := participantCount(ctx, tx, v.ID)
		if err != nil {
			return err
		}
		if override || members > 0 && float64(approvals)/float64(members) >= v.Threshold {
			supervised, err := supervisionSatisfied(ctx, tx, v)
			if err != nil {
				return err
			}
			if !supervised {
				if override {
					return platform.ErrInvalid
				}
				return s.audit(ctx, tx, a, "activity.join_vote", "membership", mid, map[string]bool{"approve": approve})
			}
			target, err := actorByID(ctx, tx, targetID)
			if err != nil {
				return err
			}
			if target.Cohort != v.Cohort {
				return platform.ErrForbidden
			}
			if err := platform.Participate(ctx, tx, target); err != nil {
				return err
			}
			blocked, err := platform.Blocked(ctx, tx, targetID, v.OwnerID)
			if err := errorIfFalse(!blocked, err); err != nil {
				return err
			}
			if v.Capacity != nil && members >= *v.Capacity {
				return platform.ErrInvalid
			}
			if _, err = tx.Exec(ctx, `UPDATE social_membership SET state='member',decided_at=now(),updated_at=now(),welcomed_at=CASE WHEN welcomed_at IS NULL AND NOT EXISTS(SELECT 1 FROM social_membership other WHERE other.user_id=$2 AND other.state='member' AND other.id<>$1) THEN now() ELSE welcomed_at END WHERE id=$1`, mid, targetID); err != nil {
				return err
			}
			if err = s.notify(ctx, tx, targetID, "join_approved", "You're in!", fmt.Sprintf("You were admitted to “%s”.", v.Title), fmt.Sprintf("/api/social/activities/%d/", v.ID)); err != nil {
				return err
			}
		}
		return s.audit(ctx, tx, a, "activity.join_decided", "membership", mid, map[string]bool{"approve": approve, "override": override})
	})
}

func (s *Service) ChangeOrganizer(ctx context.Context, a Actor, id, target int64, action string) (int64, error) {
	var mid int64
	err := s.transaction(ctx, a, func(tx pgx.Tx) error {
		v, err := activity(ctx, tx, a, id, true)
		if err != nil {
			return err
		}
		if v.OwnerID != a.ID || v.Cohort != "adult" || target == a.ID {
			return platform.ErrForbidden
		}
		targetActor, err := actorByID(ctx, tx, target)
		if err != nil {
			return err
		}
		if targetActor.Cohort != v.Cohort {
			return platform.ErrForbidden
		}
		if err = platform.Participate(ctx, tx, targetActor); err != nil {
			return err
		}
		var role string
		if err = tx.QueryRow(ctx, `SELECT id,role FROM social_membership WHERE activity_id=$1 AND user_id=$2 AND state='member' AND role<>'guardian' FOR UPDATE`, id, target).Scan(&mid, &role); err != nil {
			return err
		}
		if action == "grant_organizer" && role == "co_organizer" {
			return nil
		}
		next := "co_organizer"
		event := "activity.co_organizer_granted"
		if action == "revoke_organizer" {
			if role != "co_organizer" {
				return platform.ErrInvalid
			}
			next = "member"
			event = "activity.co_organizer_revoked"
		} else if action == "transfer" {
			next = "owner"
			event = "activity.ownership_transferred"
			if _, err = tx.Exec(ctx, `UPDATE social_activity SET owner_id=$2 WHERE id=$1`, id, target); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `UPDATE social_membership SET role='member' WHERE activity_id=$1 AND user_id=$2 AND role='owner' AND state='member'`, id, a.ID); err != nil {
				return err
			}
		} else if action != "grant_organizer" {
			return platform.ErrInvalid
		}
		if _, err = tx.Exec(ctx, `UPDATE social_membership SET role=$2 WHERE id=$1`, mid, next); err != nil {
			return err
		}
		if err = s.notify(ctx, tx, target, "organizer_role", "Organizer role changed", v.Title, fmt.Sprintf("/activities/%d/", id)); err != nil {
			return err
		}
		return s.audit(ctx, tx, a, event, "activity", id, map[string]int64{"member_ref": target})
	})
	return mid, err
}

func (s *Service) RSVP(ctx context.Context, a Actor, id int64, intent string) error {
	if intent != "unknown" && intent != "going" && intent != "not_going" {
		return platform.ErrInvalid
	}
	return s.transaction(ctx, a, func(tx pgx.Tx) error {
		v, err := activity(ctx, tx, a, id, true)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE social_membership SET attendance_intent=$3,updated_at=now() WHERE activity_id=$1 AND user_id=$2 AND state='member'`, id, a.ID, intent)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return platform.ErrForbidden
		}
		return s.confirmQuorum(ctx, tx, a, v)
	})
}
func (s *Service) confirmQuorum(ctx context.Context, tx pgx.Tx, a Actor, v activityState) error {
	var latched bool
	err := tx.QueryRow(ctx, `UPDATE social_activity a SET go_confirmed_at=now(),updated_at=now() WHERE a.id=$1 AND a.status='open' AND a.go_confirmed_at IS NULL AND a.min_to_go IS NOT NULL AND (SELECT COUNT(*) FROM social_membership m WHERE m.activity_id=a.id AND m.state='member' AND m.role<>'guardian' AND m.attendance_intent='going')>=a.min_to_go RETURNING true`, v.ID).Scan(&latched)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	body := fmt.Sprintf("“%s” has enough people going.", v.Title)
	ownerSeated, e := scalar(ctx, tx, `SELECT EXISTS(SELECT 1 FROM social_membership WHERE activity_id=$1 AND user_id=$2 AND state='member')`, v.ID, v.OwnerID)
	if e != nil {
		return e
	}
	if ownerSeated {
		if err := s.notify(ctx, tx, v.OwnerID, "meetup_confirmed", "A meetup is on", body, fmt.Sprintf("/api/social/activities/%d/", v.ID)); err != nil {
			return err
		}
	}
	return s.fanout(ctx, tx, Actor{ID: v.OwnerID}, v, "meetup_confirmed", "A meetup is on", body)
}
func (s *Service) Attendance(ctx context.Context, a Actor, id int64) (json.RawMessage, error) {
	v, err := activity(ctx, s.DB, a, id, false)
	if err != nil {
		return nil, err
	}
	ok, err := scalar(ctx, s.DB, `SELECT EXISTS(SELECT 1 FROM social_membership WHERE activity_id=$1 AND user_id=$2 AND state='member')`, id, a.ID)
	if err := errorIfFalse(ok, err); err != nil {
		return nil, err
	}
	return object(ctx, s.DB, `SELECT jsonb_build_object('going',COUNT(*) FILTER(WHERE attendance_intent='going'),'total',COUNT(*),'min_to_go',$2::int,'met_minimum',CASE WHEN $2::int IS NULL THEN NULL ELSE COUNT(*) FILTER(WHERE attendance_intent='going') >=$2 END,'remaining_needed',CASE WHEN $2::int IS NULL THEN NULL ELSE GREATEST(0,$2-COUNT(*) FILTER(WHERE attendance_intent='going')) END) FROM social_membership WHERE activity_id=$1 AND state='member' AND role<>'guardian'`, v.ID, func() *int {
		if v.Status != "open" {
			return nil
		}
		return v.MinToGo
	}())
}

func (s *Service) Presence(ctx context.Context, a Actor, id int64, kind, value string) (int64, error) {
	var mid int64
	err := s.transaction(ctx, a, func(tx pgx.Tx) error {
		v, err := activity(ctx, tx, a, id, true)
		if err != nil {
			return err
		}
		now := s.Now()
		if v.Status != "open" {
			return platform.ErrInvalid
		}
		if kind == "departing" && a.Cohort != "child" || !s.presenceWindow(v, now, kind == "departing") {
			return platform.ErrInvalid
		}

		var role, transit string
		var arrived, departed *time.Time
		if err := tx.QueryRow(ctx, `SELECT id,role,arrived_at,departing_at,transit_status FROM social_membership WHERE activity_id=$1 AND user_id=$2 AND state='member' FOR UPDATE`, id, a.ID).Scan(&mid, &role, &arrived, &departed, &transit); err != nil {
			return err
		}
		title, body, url := "Someone arrived", fmt.Sprintf("%s is at “%s”.", a.DisplayName, v.Title), fmt.Sprintf("/api/social/activities/%d/", id)
		switch kind {
		case "arrived":
			if arrived != nil {
				return nil
			}
			_, err = tx.Exec(ctx, `UPDATE social_membership SET arrived_at=now(),updated_at=now() WHERE id=$1`, mid)
		case "transit":
			if value != "on_my_way" && value != "running_late" {
				return platform.ErrInvalid
			}
			if transit == "running_late" || transit == value {
				return nil
			}
			if value == "on_my_way" {
				title = "Someone is on the way"
				body = fmt.Sprintf("%s is on the way to “%s”.", a.DisplayName, v.Title)
			} else {
				title = "Someone is running late"
				body = fmt.Sprintf("%s is running about 10 minutes late for “%s”.", a.DisplayName, v.Title)
			}
			_, err = tx.Exec(ctx, `UPDATE social_membership SET transit_status=$2,updated_at=now() WHERE id=$1`, mid, value)
		case "departing":
			if departed != nil {
				return nil
			}
			title = "Someone is heading home"
			body = fmt.Sprintf("%s is heading home from “%s”.", a.DisplayName, v.Title)
			url = "/wards/"
			_, err = tx.Exec(ctx, `UPDATE social_membership SET departing_at=now(),updated_at=now() WHERE id=$1`, mid)
		default:
			return platform.ErrInvalid
		}
		if err != nil {
			return err
		}
		if kind != "departing" {
			if err := s.fanout(ctx, tx, a, v, "arrival", title, body); err != nil {
				return err
			}
		}
		if a.Cohort == "child" {
			rows, err := tx.Query(ctx, `SELECT rel.guardian_id FROM accounts_guardianrelationship rel WHERE rel.ward_id=$1 AND rel.status='active' AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$1 AND b.blocked_id=rel.guardian_id) OR (b.blocker_id=rel.guardian_id AND b.blocked_id=$1)) AND ($3 OR NOT EXISTS(SELECT 1 FROM social_membership m WHERE m.activity_id=$2 AND m.user_id=rel.guardian_id AND m.state='member'))`, a.ID, id, kind == "departing")
			if err != nil {
				return err
			}
			ids := []int64{}
			for rows.Next() {
				var uid int64
				if err := rows.Scan(&uid); err != nil {
					rows.Close()
					return err
				}
				ids = append(ids, uid)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			for _, uid := range ids {
				if err := s.notify(ctx, tx, uid, "arrival", title, body, url); err != nil {
					return err
				}
			}
		}
		data := map[string]any{}
		if kind == "transit" {
			data["reason"] = value
		}
		return s.audit(ctx, tx, a, "activity."+kind, "activity", id, data)
	})
	return mid, err
}
