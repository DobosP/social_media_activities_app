package social

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode/utf8"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

type SeriesInput struct {
	ActivityInput
	Cadence       string    `json:"cadence"`
	FirstStartsAt time.Time `json:"first_starts_at"`
}

const seriesColumns = `jsonb_build_object('id',sr.id,'title',sr.title,'description',sr.description,'meeting_point',sr.meeting_point,'what_to_bring',sr.what_to_bring,'organizer_note',sr.organizer_note,'cost_band',sr.cost_band,'difficulty',sr.difficulty,'accessibility_notes',sr.accessibility_notes,'beginners_welcome',sr.beginners_welcome,'owner',u.display_name,'place',sr.place_id,'activity_type',t.slug,'cohort',sr.cohort,'cadence',sr.cadence,'status',sr.status,'next_starts_at',sr.next_starts_at,'join_threshold',sr.join_threshold,'capacity',sr.capacity,'min_to_go',sr.min_to_go,'guardian_accompanied',sr.guardian_accompanied,'supervised',sr.supervised,'created_at',sr.created_at)`
const seriesJoin = ` FROM social_activityseries sr JOIN accounts_user u ON u.id=sr.owner_id JOIN taxonomy_activitytype t ON t.id=sr.activity_type_id `

func (s *Service) Series(ctx context.Context, a Actor, id int64) (json.RawMessage, error) {
	return object(ctx, s.DB, `SELECT `+seriesColumns+seriesJoin+` WHERE sr.id=$1 AND (sr.owner_id=$2 OR $3)`, id, a.ID, a.IsStaff)
}
func (s *Service) listSeries(ctx context.Context, a Actor, limit, offset int) ([]json.RawMessage, error) {
	return objects(ctx, s.DB, `SELECT `+seriesColumns+seriesJoin+` WHERE sr.owner_id=$1 OR $2 ORDER BY sr.id LIMIT $3 OFFSET $4`, a.ID, a.IsStaff, limit, offset)
}
func (s *Service) CreateSeries(ctx context.Context, a Actor, in SeriesInput) (int64, error) {
	if in.FirstStartsAt.IsZero() {
		return 0, platform.ErrInvalid
	}
	in.StartsAt = in.FirstStartsAt
	if err := in.validate(); err != nil {
		return 0, err
	}
	if in.Cadence != "weekly" && in.Cadence != "biweekly" && in.Cadence != "monthly" {
		return 0, platform.ErrInvalid
	}
	var id int64
	err := s.transaction(ctx, a, func(tx pgx.Tx) error {
		if in.Supervised {
			if a.Cohort != "child" {
				return platform.ErrInvalid
			}
			in.GuardianAccompanied = true
		}
		if in.GuardianAccompanied && a.Cohort != "child" {
			return platform.ErrInvalid
		}
		if err := childCreateGate(ctx, tx, a, in.ActivityInput); err != nil {
			return err
		}
		if err := publicPlace(ctx, tx, a, in.Place, false); err != nil {
			return err
		}
		ok, err := scalar(ctx, tx, `SELECT EXISTS(SELECT 1 FROM taxonomy_activitytype WHERE id=$1 AND is_active)`, in.ActivityType)
		if err := errorIfFalse(ok, err); err != nil {
			return err
		}
		threshold := 2.0 / 3
		if in.JoinThreshold != nil {
			threshold = *in.JoinThreshold
		}
		var duration *int
		if in.EndsAt != nil && in.EndsAt.After(in.StartsAt) {
			minutes := int(in.EndsAt.Sub(in.StartsAt) / time.Minute)
			duration = &minutes
		}
		location, err := time.LoadLocation("Europe/Bucharest")
		if err != nil {
			return err
		}
		anchor := in.StartsAt.In(location).Day()
		err = tx.QueryRow(ctx, `INSERT INTO social_activityseries(owner_id,place_id,activity_type_id,cohort,title,description,meeting_point,what_to_bring,organizer_note,accessibility_notes,next_instance_note,cost_band,cost_amount,cost_note,difficulty,capacity,min_to_go,join_threshold,guardian_accompanied,supervised,beginners_welcome,cadence,next_starts_at,anchor_day,duration_minutes,status,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'',$11,$23::text::numeric,$24,$12,$13,$14,$15,$21,$22,$16,$17,$18,$19,$20,'active',now(),now()) RETURNING id`, a.ID, in.Place, in.ActivityType, a.Cohort, in.Title, in.Description, in.MeetingPoint, in.WhatToBring, in.OrganizerNote, in.AccessibilityNotes, in.CostBand, in.Difficulty, in.Capacity, in.MinToGo, threshold, in.BeginnersWelcome, in.Cadence, in.StartsAt, anchor, duration, in.GuardianAccompanied, in.Supervised, in.CostAmount, in.CostNote).Scan(&id)
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, a, "series.created", "activityseries", id, nil)
	})
	return id, err
}
func (s *Service) TransitionSeries(ctx context.Context, a Actor, id int64, action string) error {
	return s.privacyTransaction(ctx, a, func(tx pgx.Tx) error {
		var owner int64
		var state string
		if err := tx.QueryRow(ctx, `SELECT owner_id,status FROM social_activityseries WHERE id=$1 FOR UPDATE`, id).Scan(&owner, &state); err != nil {
			return err
		}
		if owner != a.ID {
			return platform.ErrForbidden
		}
		next := ""
		event := ""
		switch action {
		case "pause":
			if state != "active" {
				return platform.ErrInvalid
			}
			next, event = "paused", "series.paused"
		case "resume":
			if state != "paused" {
				return platform.ErrInvalid
			}
			if err := platform.Participate(ctx, tx, a); err != nil {
				return err
			}
			next, event = "active", "series.resumed"
		case "end":
			if state == "ended" {
				return platform.ErrInvalid
			}
			next, event = "ended", "series.ended"
		default:
			return platform.ErrInvalid
		}
		if _, err := tx.Exec(ctx, `UPDATE social_activityseries SET status=$2,updated_at=now() WHERE id=$1`, id, next); err != nil {
			return err
		}
		return s.audit(ctx, tx, a, event, "activityseries", id, nil)
	})
}
func (s *Service) SetSeriesNote(ctx context.Context, a Actor, id int64, note string) error {
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > 500 {
		note = string([]rune(note)[:500])
	}
	return s.privacyTransaction(ctx, a, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE social_activityseries SET next_instance_note=$3,updated_at=now() WHERE id=$1 AND owner_id=$2 AND status<>'ended'`, id, a.ID, note)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return platform.ErrForbidden
		}
		return s.audit(ctx, tx, a, "series.next_note_set", "activityseries", id, nil)
	})
}

// AdvanceSlot uses the launch city's wall-clock and an immutable monthly anchor,
// preserving 18:00 through DST and restoring March31 after February28.
func AdvanceSlot(value time.Time, cadence string, anchor int, location *time.Location) (time.Time, error) {
	local := value.In(location)
	year, month, day := local.Date()
	switch cadence {
	case "weekly":
		day += 7
	case "biweekly":
		day += 14
	case "monthly":
		month++
		if month > 12 {
			year++
			month = 1
		}
		last := time.Date(year, month+1, 0, 0, 0, 0, 0, location).Day()
		if anchor < 1 {
			anchor = day
		}
		day = min(anchor, last)
	default:
		return time.Time{}, platform.ErrInvalid
	}
	return time.Date(year, month, day, local.Hour(), local.Minute(), local.Second(), local.Nanosecond(), location), nil
}

type SpawnSummary struct {
	Spawned int `json:"spawned"`
	Skipped int `json:"skipped"`
	Paused  int `json:"paused"`
}

func (s *Service) SpawnDueSeries(ctx context.Context, now time.Time) (SpawnSummary, error) {
	var summary SpawnSummary
	if s.Audit == nil || s.DB == nil {
		return summary, platform.ErrForbidden
	}
	location, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		return summary, err
	}
	rows, err := s.DB.Query(ctx, `SELECT id FROM social_activityseries WHERE status='active' AND next_starts_at<=$1 ORDER BY id LIMIT 500`, now.AddDate(0, 0, 14))
	if err != nil {
		return summary, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return summary, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return summary, err
	}
	for _, id := range ids {
		outcome := "skipped"
		err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
			var ownerID int64
			var cohort, cadence, note string
			var anchor int
			var duration *int
			var raw []byte
			err := tx.QueryRow(ctx, `SELECT owner_id,cohort,cadence,anchor_day,duration_minutes,next_instance_note,jsonb_build_object('place',place_id,'activity_type',activity_type_id,'title',title,'description',description,'starts_at',next_starts_at,'join_threshold',join_threshold,'capacity',capacity,'min_to_go',min_to_go,'guardian_accompanied',guardian_accompanied,'supervised',supervised,'meeting_point',meeting_point,'what_to_bring',what_to_bring,'organizer_note',organizer_note,'cost_band',cost_band,'cost_amount',cost_amount::text,'cost_note',cost_note,'difficulty',difficulty,'accessibility_notes',accessibility_notes,'beginners_welcome',beginners_welcome) FROM social_activityseries WHERE id=$1 AND status='active' FOR UPDATE SKIP LOCKED`, id).Scan(&ownerID, &cohort, &cadence, &anchor, &duration, &note, &raw)
			if err == pgx.ErrNoRows {
				outcome = "none"
				return nil
			}
			if err != nil {
				return err
			}
			owner, err := actorByID(ctx, tx, ownerID)
			if err != nil {
				return err
			}
			if owner.Cohort != cohort {
				if _, err := tx.Exec(ctx, `UPDATE social_activityseries SET status='paused',updated_at=now() WHERE id=$1`, id); err != nil {
					return err
				}
				outcome = "paused"
				return s.audit(ctx, tx, owner, "series.paused", "activityseries", id, map[string]string{"reason": "owner_cohort_drift"})
			}
			if err := platform.Participate(ctx, tx, owner); err != nil {
				return err
			}
			var in ActivityInput
			if json.Unmarshal(raw, &in) != nil {
				return platform.ErrInvalid
			}
			upcoming, err := scalar(ctx, tx, `SELECT EXISTS(SELECT 1 FROM social_activity WHERE series_id=$1 AND starts_at>=$2)`, id, now)
			if err != nil {
				return err
			}
			if upcoming {
				outcome = "none"
				return nil
			}
			for n := 0; in.StartsAt.Before(now) && n < 240; n++ {
				in.StartsAt, err = AdvanceSlot(in.StartsAt, cadence, anchor, location)
				if err != nil {
					return err
				}
			}
			if in.StartsAt.Before(now) || in.StartsAt.After(now.AddDate(0, 0, 14)) {
				_, err = tx.Exec(ctx, `UPDATE social_activityseries SET next_starts_at=$2,updated_at=now() WHERE id=$1`, id, in.StartsAt)
				return err
			}
			exists, err := scalar(ctx, tx, `SELECT EXISTS(SELECT 1 FROM social_activity WHERE series_id=$1 AND starts_at=$2)`, id, in.StartsAt)
			if err != nil {
				return err
			}
			next, err := AdvanceSlot(in.StartsAt, cadence, anchor, location)
			if err != nil {
				return err
			}
			if exists {
				_, err = tx.Exec(ctx, `UPDATE social_activityseries SET next_starts_at=$2,updated_at=now() WHERE id=$1`, id, next)
				outcome = "none"
				return err
			}
			if duration != nil && *duration > 0 {
				end := in.StartsAt.Add(time.Duration(*duration) * time.Minute)
				in.EndsAt = &end
			}
			if note != "" {
				if in.OrganizerNote != "" {
					in.OrganizerNote += "\n\n"
				}
				in.OrganizerNote += note
			}
			var activityID int64
			if err := s.createActivity(ctx, tx, owner, in, &id, &activityID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE social_activityseries SET next_starts_at=$2,next_instance_note='',updated_at=now() WHERE id=$1`, id, next); err != nil {
				return err
			}
			outcome = "spawned"
			return s.audit(ctx, tx, owner, "series.spawned", "activity", activityID, map[string]int64{"series_id": id})
		})
		if err != nil {
			summary.Skipped++
		} else {
			switch outcome {
			case "spawned":
				summary.Spawned++
			case "paused":
				summary.Paused++
			case "skipped":
				summary.Skipped++
			}
		}
	}
	return summary, nil
}
