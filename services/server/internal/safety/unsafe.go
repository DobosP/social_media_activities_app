package safety

import (
	"context"
	"errors"
	"fmt"
	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"time"
)

const UnsafeSentinel = "Filed via the one-tap safe-exit “I feel unsafe” button."

type UnsafeResult struct {
	ReportID         int64 `json:"report_id"`
	GuardiansAlerted int   `json:"guardians_alerted"`
	Repeat           bool  `json:"repeat"`
}

// UnsafeReport sends server-composed guardian copy only. A child's report text
// never becomes an adult contact channel, and repeated taps do not storm alerts.
func (s *Service) UnsafeReport(ctx context.Context, a platform.Actor, activityID int64) (UnsafeResult, error) {
	var result UnsafeResult
	policy, policyErr := budgets.Resolve(s.RatePolicies, "unsafe_report", budgets.Policy{Limit: 12, Window: time.Hour})
	if policyErr != nil {
		return result, policyErr
	}
	if s.DB == nil || a.ID < 1 {
		return result, errors.New("safety budget actor unavailable")
	}
	if s.Config.CanSeeActivity == nil {
		return result, platform.ErrNotFound
	}
	visible, err := s.Config.CanSeeActivity(ctx, s.DB, a, activityID)
	if err != nil {
		return result, err
	}
	if !visible {
		return result, platform.ErrNotFound
	}
	target, err := s.ResolveTarget(ctx, s.DB, "social", "activity", activityID)
	if err != nil {
		return result, err
	}
	if err := s.pruneExpiredActionBudgets(ctx, s.Config.Now()); err != nil {
		return result, err
	}
	err = platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		var reporter int64
		if err := tx.QueryRow(ctx, `SELECT id FROM accounts_user WHERE id=$1 FOR KEY SHARE`, a.ID).Scan(&reporter); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT id FROM social_activity WHERE id=$1 FOR UPDATE`, activityID); err != nil {
			return err
		}
		var status string
		var created time.Time
		err := tx.QueryRow(ctx, `SELECT id,status,created_at FROM safety_report WHERE reporter_id=$1 AND target_type_id=$2 AND target_id=$3 AND reason='off_platform' AND detail=$4 ORDER BY created_at DESC,id DESC LIMIT 1`, a.ID, target.ContentType, target.ID, UnsafeSentinel).Scan(&result.ReportID, &status, &created)
		if err == nil && (status == "open" || status == "reviewing" || created.After(s.Config.Now().Add(-s.Config.UnsafeReportCooldown))) {
			result.Repeat = true
			return nil
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var attempts int
		now := s.Config.Now()
		if err = tx.QueryRow(ctx, `INSERT INTO safety_go_actionbudget(user_id,action,count,until)
			VALUES($1,'unsafe_report',1,$2) ON CONFLICT(user_id,action) DO UPDATE SET
			count=CASE WHEN safety_go_actionbudget.until<=$4 THEN 1 ELSE safety_go_actionbudget.count+1 END,
			until=CASE WHEN safety_go_actionbudget.until<=$4 THEN $2 ELSE safety_go_actionbudget.until END
			WHERE safety_go_actionbudget.until<=$4 OR safety_go_actionbudget.count<$3 RETURNING count`,
			a.ID, now.Add(policy.Window), policy.Limit, now).Scan(&attempts); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrRate
			}
			return err
		}
		if attempts > policy.Limit {
			return ErrRate
		}
		result.ReportID, err = s.fileReport(ctx, tx, a, target, "off_platform", UnsafeSentinel)
		if err != nil {
			return err
		}
		if a.Cohort != "child" {
			return nil
		}
		rows, err := tx.Query(ctx, `SELECT DISTINCT g.guardian_id FROM accounts_guardianrelationship g WHERE g.ward_id=$1 AND g.status='active' AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=g.guardian_id AND b.blocked_id=$1) OR (b.blocker_id=$1 AND b.blocked_id=g.guardian_id))`, a.ID)
		if err != nil {
			return err
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		name := a.DisplayName
		if name == "" {
			name = a.Username
		}
		body := fmt.Sprintf("%s used the 'I feel unsafe' button during a meetup. Please check in with them now. You can see their upcoming meetups on your guardian page.", name)
		for _, id := range ids {
			if notify(ctx, tx, id, "system", "Safety alert: a child you look after asked for help", body, "/wards/") {
				result.GuardiansAlerted++
			}
		}
		return nil
	})
	return result, err
}
