package social

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

var windows = map[string]string{"weekday_daytime": "Weekday daytime", "weekday_evening": "Weekday evening", "weekend_daytime": "Weekend daytime", "weekend_evening": "Weekend evening"}

const gaugeColumns = `jsonb_build_object('id',g.id,'proposer',u.display_name,'place',p.name,'activity_type',t.slug,'cohort',g.cohort,'coarse_window',CASE g.coarse_window WHEN 'weekday_daytime' THEN 'Weekday daytime' WHEN 'weekday_evening' THEN 'Weekday evening' WHEN 'weekend_daytime' THEN 'Weekend daytime' WHEN 'weekend_evening' THEN 'Weekend evening' END,'ready',(SELECT COUNT(*) FROM social_activityinterest_interested_users i WHERE i.activityinterest_id=g.id)>=3,'remaining',GREATEST(0,3-(SELECT COUNT(*) FROM social_activityinterest_interested_users i WHERE i.activityinterest_id=g.id)),'expires_at',g.expires_at,'created_at',g.created_at)`
const gaugeJoin = ` FROM social_activityinterest g JOIN accounts_user u ON u.id=g.proposer_id JOIN places_place p ON p.id=g.place_id JOIN taxonomy_activitytype t ON t.id=g.activity_type_id `
const gaugeVisible = `g.cohort=$2 AND g.converted_activity_id IS NULL AND g.expires_at>now() AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$1 AND b.blocked_id=g.proposer_id) OR (b.blocker_id=g.proposer_id AND b.blocked_id=$1))`

func (s *Service) Gauge(ctx context.Context, a Actor, id int64) (json.RawMessage, error) {
	if !assigned(a) {
		return nil, platform.ErrNotFound
	}
	return object(ctx, s.DB, `SELECT `+gaugeColumns+gaugeJoin+` WHERE g.id=$3 AND `+gaugeVisible, a.ID, a.Cohort, id)
}
func (s *Service) listGauges(ctx context.Context, a Actor, limit, offset int) ([]json.RawMessage, error) {
	if !assigned(a) {
		return []json.RawMessage{}, nil
	}
	return objects(ctx, s.DB, `SELECT `+gaugeColumns+gaugeJoin+` WHERE `+gaugeVisible+` ORDER BY g.expires_at,g.id LIMIT $3 OFFSET $4`, a.ID, a.Cohort, limit, offset)
}

type GaugeInput struct {
	Place        int64  `json:"place"`
	ActivityType int64  `json:"activity_type"`
	CoarseWindow string `json:"coarse_window"`
}

func (s *Service) ProposeGauge(ctx context.Context, a Actor, in GaugeInput) (int64, error) {
	if _, ok := windows[in.CoarseWindow]; !ok || in.Place < 1 || in.ActivityType < 1 {
		return 0, platform.ErrInvalid
	}
	var id int64
	err := s.transaction(ctx, a, func(tx pgx.Tx) error {
		if err := publicPlace(ctx, tx, a, in.Place, false); err != nil {
			return err
		}
		if err := childCreateGate(ctx, tx, a, ActivityInput{Place: in.Place, ActivityType: in.ActivityType}); err != nil {
			return err
		}
		yes, err := scalar(ctx, tx, `SELECT EXISTS(SELECT 1 FROM taxonomy_activitytype WHERE id=$1 AND is_active)`, in.ActivityType)
		if err := errorIfFalse(yes, err); err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `INSERT INTO social_activityinterest(proposer_id,place_id,activity_type_id,cohort,coarse_window,converted_activity_id,expires_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,NULL,now()+interval '14 days',now(),now()) RETURNING id`, a.ID, in.Place, in.ActivityType, a.Cohort, in.CoarseWindow).Scan(&id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO social_activityinterest_interested_users(activityinterest_id,user_id) VALUES($1,$2)`, id, a.ID); err != nil {
			return err
		}
		return s.audit(ctx, tx, a, "interest.proposed", "activityinterest", id, nil)
	})
	return id, err
}
func (s *Service) MarkGauge(ctx context.Context, a Actor, id int64, interested bool) error {
	work := func(tx pgx.Tx) error {
		var cohort string
		var proposer int64
		var expired bool
		if err := tx.QueryRow(ctx, `SELECT cohort,proposer_id,converted_activity_id IS NOT NULL OR expires_at<=now() FROM social_activityinterest WHERE id=$1 FOR UPDATE`, id).Scan(&cohort, &proposer, &expired); err != nil {
			return err
		}
		if a.Cohort != cohort {
			return platform.ErrNotFound
		}
		if interested {
			if expired {
				return platform.ErrInvalid
			}
			blocked, err := platform.Blocked(ctx, tx, a.ID, proposer)
			if err := errorIfFalse(!blocked, err); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO social_activityinterest_interested_users(activityinterest_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, a.ID); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(ctx, `DELETE FROM social_activityinterest_interested_users WHERE activityinterest_id=$1 AND user_id=$2`, id, a.ID); err != nil {
				return err
			}
		}
		return nil
	}
	if interested {
		return s.transaction(ctx, a, work)
	}
	return s.privacyTransaction(ctx, a, work)
}

type GaugeConversion struct {
	Title       string     `json:"title"`
	StartsAt    time.Time  `json:"starts_at"`
	EndsAt      *time.Time `json:"ends_at"`
	Description string     `json:"description"`
}

func (s *Service) ConvertGauge(ctx context.Context, a Actor, id int64, in GaugeConversion) (int64, error) {
	var activityID int64
	err := s.transaction(ctx, a, func(tx pgx.Tx) error {
		var proposer, place, typ int64
		var cohort string
		var unavailable bool
		if err := tx.QueryRow(ctx, `SELECT proposer_id,place_id,activity_type_id,cohort,converted_activity_id IS NOT NULL OR expires_at<=now() FROM social_activityinterest WHERE id=$1 FOR UPDATE`, id).Scan(&proposer, &place, &typ, &cohort, &unavailable); err != nil {
			return err
		}
		if proposer != a.ID || cohort != a.Cohort {
			return platform.ErrForbidden
		}
		if unavailable {
			return platform.ErrInvalid
		}
		input := ActivityInput{Place: place, ActivityType: typ, Title: in.Title, StartsAt: in.StartsAt, EndsAt: in.EndsAt, Description: in.Description}
		if err := input.validate(); err != nil {
			return err
		}
		if err := s.createActivity(ctx, tx, a, input, nil, &activityID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE social_activityinterest SET converted_activity_id=$2,updated_at=now() WHERE id=$1`, id, activityID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT i.user_id FROM social_activityinterest_interested_users i JOIN accounts_user u ON u.id=i.user_id WHERE i.activityinterest_id=$1 AND i.user_id<>$2 AND u.cohort=$3 AND u.is_active AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$2 AND b.blocked_id=i.user_id) OR (b.blocker_id=i.user_id AND b.blocked_id=$2))`, id, a.ID, cohort)
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
			target, err := actorByID(ctx, tx, uid)
			if err != nil {
				return err
			}
			if err := platform.Participate(ctx, tx, target); err == platform.ErrForbidden {
				continue
			} else if err != nil {
				return err
			}
			if err := s.notify(ctx, tx, uid, "interest_converted", "A meetup you were interested in is on", in.Title+" is now a real meetup — join if you can come.", fmt.Sprintf("/api/social/activities/%d/", activityID)); err != nil {
				return err
			}
		}
		return s.audit(ctx, tx, a, "interest.converted", "activity", activityID, nil)
	})
	return activityID, err
}
func (s *Service) gaugesList(w http.ResponseWriter, r *http.Request, a Actor) {
	v, err := s.cursorRows(r, 200, func(ctx context.Context, limit, offset int) ([]json.RawMessage, error) {
		return s.listGauges(ctx, a, limit, offset)
	})
	response(w, v, err, 200)
}
func (s *Service) gaugeDetail(w http.ResponseWriter, r *http.Request, a Actor) {
	id, err := idFrom(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	v, err := s.Gauge(r.Context(), a, id)
	response(w, v, err, 200)
}
func (s *Service) gaugeCreate(w http.ResponseWriter, r *http.Request, a Actor) {
	var in GaugeInput
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	id, err := s.ProposeGauge(r.Context(), a, in)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	v, err := s.Gauge(r.Context(), a, id)
	response(w, v, err, 201)
}
func (s *Service) gaugeAction(w http.ResponseWriter, r *http.Request, a Actor) {
	id, err := idFrom(r)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if actionOf(r) == "convert" {
		var in GaugeConversion
		if err := platform.Decode(w, r, &in); err != nil {
			platform.Fail(w, err)
			return
		}
		aid, err := s.ConvertGauge(r.Context(), a, id, in)
		if err != nil {
			platform.Fail(w, err)
			return
		}
		v, err := s.Activity(r.Context(), a, aid)
		response(w, v, err, 201)
		return
	}
	if err := s.MarkGauge(r.Context(), a, id, actionOf(r) == "interested"); err != nil {
		platform.Fail(w, err)
		return
	}
	v, err := s.Gauge(r.Context(), a, id)
	response(w, v, err, 200)
}
