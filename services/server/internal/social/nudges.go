package social

import (
	"context"
	"fmt"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

type nudgeRow struct {
	Recipient, Activity int64
	Title               string
}

func (s *Service) notifyOnce(ctx context.Context, row nudgeRow, kind, title, body, url string) (bool, error) {
	var sent bool
	err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,702344))`, fmt.Sprintf("%d:%s:%s", row.Recipient, kind, url)); err != nil {
			return err
		}
		yes, err := scalar(ctx, tx, `SELECT EXISTS(SELECT 1 FROM notifications_notification WHERE recipient_id=$1 AND kind=$2 AND url=$3)`, row.Recipient, kind, url)
		if err != nil || yes {
			return err
		}
		if s.Notify == nil {
			return platform.ErrForbidden
		}
		sent, err = s.Notify(ctx, tx, row.Recipient, kind, preview(title, 200), body, url)
		return err
	})
	return sent, err
}
func nudgeRows(ctx context.Context, q platform.Querier, sql string, args ...any) ([]nudgeRow, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []nudgeRow{}
	for rows.Next() {
		var row nudgeRow
		if err := rows.Scan(&row.Recipient, &row.Activity, &row.Title); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
func (s *Service) NudgeOrganizers(ctx context.Context, now time.Time) (int, error) {
	rows, err := nudgeRows(ctx, s.DB, `SELECT who.user_id,a.id,a.title FROM social_activity a CROSS JOIN LATERAL(SELECT a.owner_id user_id UNION SELECT m.user_id FROM social_membership m WHERE m.activity_id=a.id AND m.state='member' AND m.role='co_organizer') who WHERE a.status='open' AND NOT a.is_hidden AND a.starts_at BETWEEN $1 AND $1::timestamptz+interval '48 hours' AND btrim(a.meeting_point)='' ORDER BY a.id,who.user_id`, now)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, row := range rows {
		delivered, err := s.notifyOnce(ctx, row, "organizer_prep", "“"+row.Title+"” still has no meeting point", "It starts soon and members won't know where to gather. Add a meeting point so everyone can find the group.", fmt.Sprintf("/activities/%d/", row.Activity))
		if err != nil {
			return sent, err
		}
		if delivered {
			sent++
		}
	}
	return sent, nil
}
func (s *Service) NudgeRSVP(ctx context.Context, now time.Time) (int, error) {
	rows, err := nudgeRows(ctx, s.DB, `SELECT m.user_id,a.id,a.title FROM social_activity a JOIN social_membership m ON m.activity_id=a.id AND m.state='member' AND m.role<>'guardian' AND m.attendance_intent='unknown' WHERE a.status='open' AND NOT a.is_hidden AND a.starts_at BETWEEN $1::timestamptz-interval '3 hours' AND $1::timestamptz+interval '2 hours' ORDER BY a.id,m.user_id`, now)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, row := range rows {
		delivered, err := s.notifyOnce(ctx, row, "rsvp_nudge", "Still coming to “"+row.Title+"”?", "A quick yes or no helps everyone plan — you can update it any time.", fmt.Sprintf("/activities/%d/", row.Activity))
		if err != nil {
			return sent, err
		}
		if delivered {
			sent++
		}
	}
	return sent, nil
}
func (s *Service) NudgeSupervisors(ctx context.Context, now time.Time) (int, error) {
	rows, err := nudgeRows(ctx, s.DB, `SELECT rel.guardian_id,a.id,a.title FROM social_activity a JOIN accounts_guardianrelationship rel ON rel.ward_id=a.owner_id AND rel.status='active' WHERE a.status='open' AND NOT a.is_hidden AND a.supervised AND a.cohort='child' AND a.starts_at BETWEEN $1 AND $1::timestamptz+interval '48 hours' AND NOT EXISTS(SELECT 1 FROM social_membership sup JOIN accounts_guardianrelationship link ON link.guardian_id=sup.user_id AND link.ward_id=a.owner_id AND link.status='active' JOIN accounts_user guardian ON guardian.id=sup.user_id WHERE sup.activity_id=a.id AND sup.state='member' AND sup.role='guardian' AND guardian.is_active AND guardian.is_identity_verified AND guardian.cohort='adult' AND COALESCE((SELECT proof.expires_at IS NULL OR proof.expires_at>now() FROM accounts_ageassurance proof WHERE proof.user_id=guardian.id ORDER BY proof.verified_at DESC,proof.id DESC LIMIT 1),true)) AND EXISTS(SELECT 1 FROM social_membership request WHERE request.activity_id=a.id AND request.state='requested' AND (SELECT COUNT(*)::double precision FROM social_joinvote vote WHERE vote.membership_id=request.id AND vote.approve)/NULLIF((SELECT COUNT(*)::double precision FROM social_membership peer WHERE peer.activity_id=a.id AND peer.state='member' AND peer.role<>'guardian'),0)>=a.join_threshold) AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=a.owner_id AND b.blocked_id=rel.guardian_id) OR (b.blocker_id=rel.guardian_id AND b.blocked_id=a.owner_id)) ORDER BY a.id,rel.guardian_id`, now)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, row := range rows {
		delivered, err := s.notifyOnce(ctx, row, "supervisor_needed", "“"+row.Title+"” needs an adult to supervise", "A meetup your child is organising is ready to go but needs you (or another guardian) to join as its supervisor. Open your guardian page.", fmt.Sprintf("/wards/?supervisor_needed=%d", row.Activity))
		if err != nil {
			return sent, err
		}
		if delivered {
			sent++
		}
	}
	return sent, nil
}
