package accounts

import (
	"context"
	"errors"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"time"
)

type SweepSummary struct {
	Nudged, Paused, NewlyExpired int
	Failed                       int
}

func (s *Service) activeGuardians(ctx context.Context, q platform.Querier, ward int64) ([]int64, error) {
	rows, err := q.Query(ctx, `SELECT guardian_id FROM accounts_guardianrelationship WHERE ward_id=$1 AND status='active' ORDER BY guardian_id`, ward)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Service) RunReverifySweep(ctx context.Context, now time.Time, reminderDays, cap int) (SweepSummary, error) {
	var summary SweepSummary
	if cap < 1 {
		cap = 1000
	}
	if reminderDays < 1 {
		reminderDays = 14
	}
	cutoff := now.Add(time.Duration(reminderDays) * 24 * time.Hour)
	rows, err := s.DB.Query(ctx, `SELECT u.id,a.id FROM accounts_user u JOIN LATERAL(SELECT id,expires_at,reverify_notice FROM accounts_ageassurance WHERE user_id=u.id ORDER BY verified_at DESC,id DESC LIMIT 1)a ON true WHERE u.cohort IN ('child','teen') AND u.is_identity_verified AND a.expires_at IS NOT NULL AND ((a.expires_at<=$1 AND a.reverify_notice<>'expired') OR (a.expires_at>$1 AND a.expires_at<=$2 AND a.reverify_notice='')) ORDER BY u.id`, now, cutoff)
	if err != nil {
		return summary, err
	}
	type candidate struct{ user, proof int64 }
	candidates := []candidate{}
	for rows.Next() {
		var value candidate
		if err = rows.Scan(&value.user, &value.proof); err != nil {
			rows.Close()
			return summary, err
		}
		candidates = append(candidates, value)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return summary, err
	}
	for _, candidate := range candidates {
		kind := ""
		err = platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
			var expires *time.Time
			var notice string
			var proof int64
			err := tx.QueryRow(ctx, `SELECT id,expires_at,reverify_notice FROM accounts_ageassurance WHERE user_id=$1 ORDER BY verified_at DESC,id DESC LIMIT 1 FOR UPDATE`, candidate.user).Scan(&proof, &expires, &notice)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			if proof != candidate.proof || expires == nil {
				return nil
			}
			a, err := s.actor(ctx, tx, candidate.user)
			if err != nil {
				return err
			}
			if a.Cohort != "child" && a.Cohort != "teen" {
				return nil
			}
			if !expires.After(now) {
				if notice == "expired" {
					return nil
				}
				kind = "new_expiry"
				if summary.Paused >= cap {
					return nil
				}
				if err = evictParticipation(ctx, tx, a, "assurance_expired"); err != nil {
					return err
				}
				if _, err = tx.Exec(ctx, `UPDATE accounts_ageassurance SET reverify_notice='expired' WHERE id=$1`, proof); err != nil {
					return err
				}
				if _, err = platform.Notify(ctx, tx, a.ID, "system", "Your age verification has expired", "Re-verify your age to keep joining and chatting.", "/verify-age/"); err != nil {
					return err
				}
				kind = "paused"
				return nil
			}
			if expires.After(cutoff) || notice != "" {
				return nil
			}
			guardians, err := s.activeGuardians(ctx, tx, a.ID)
			if err != nil {
				return err
			}
			if _, err = platform.Notify(ctx, tx, a.ID, "system", "Your age verification is expiring soon", "Re-verify your age soon to keep joining and chatting.", "/verify-age/"); err != nil {
				return err
			}
			for _, id := range guardians {
				if _, err = platform.Notify(ctx, tx, id, "system", "Your ward's age verification is expiring soon", "Your ward needs to re-verify their age soon to keep participating.", "/verify-age/"); err != nil {
					return err
				}
			}
			_, err = tx.Exec(ctx, `UPDATE accounts_ageassurance SET reverify_notice='soon' WHERE id=$1`, proof)
			kind = "nudged"
			return err
		})
		if err != nil {
			summary.Failed++
			if ctx.Err() != nil {
				return summary, ctx.Err()
			}
			continue
		}
		switch kind {
		case "nudged":
			summary.Nudged++
		case "paused":
			summary.Paused++
			summary.NewlyExpired++
		case "new_expiry":
			summary.NewlyExpired++
		}
	}
	err = platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if summary.NewlyExpired > cap {
			if err := platform.RecordAudit(ctx, tx, platform.Actor{}, "accounts.reverify_mass_expiry_guard", "", map[string]int{"newly_expired": summary.NewlyExpired, "cap": cap}); err != nil {
				return err
			}
		}
		return platform.RecordAudit(ctx, tx, platform.Actor{}, "accounts.reverify_swept", "", map[string]int{"nudged": summary.Nudged, "paused": summary.Paused, "newly_expired": summary.NewlyExpired})
	})
	return summary, err
}

func (s *Service) RunConsentRenewalSweep(ctx context.Context, now time.Time, reminderDays, cap int) (SweepSummary, error) {
	var summary SweepSummary
	if cap < 1 {
		cap = 1000
	}
	if reminderDays < 1 {
		reminderDays = 14
	}
	cutoff := now.Add(time.Duration(reminderDays) * 24 * time.Hour)
	rows, err := s.DB.Query(ctx, `SELECT DISTINCT u.id FROM accounts_user u JOIN accounts_parentalconsent c ON c.minor_id=u.id WHERE u.cohort='child' AND u.age_band='under_16' AND c.status='active' AND c.expires_at IS NOT NULL AND c.expires_at<=$1 ORDER BY u.id`, cutoff)
	if err != nil {
		return summary, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
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
		kind := ""
		nudged := 0
		err = platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT id FROM accounts_user WHERE id=$1 FOR UPDATE`, id); err != nil {
				return err
			}
			a, err := s.actor(ctx, tx, id)
			if err != nil {
				return err
			}
			if a.Cohort != "child" || a.AgeBand != "under_16" {
				return nil
			}
			rows, err := tx.Query(ctx, `SELECT id,expires_at,renewal_notice FROM accounts_parentalconsent WHERE minor_id=$1 AND status='active' FOR UPDATE`, id)
			if err != nil {
				return err
			}
			type consent struct {
				id      int64
				expires *time.Time
				notice  string
			}
			active := []consent{}
			valid := []consent{}
			for rows.Next() {
				var c consent
				if err = rows.Scan(&c.id, &c.expires, &c.notice); err != nil {
					rows.Close()
					return err
				}
				active = append(active, c)
				if c.expires == nil || c.expires.After(now) {
					valid = append(valid, c)
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			if len(active) == 0 {
				return nil
			}
			guardians, err := s.activeGuardians(ctx, tx, id)
			if err != nil {
				return err
			}
			if len(valid) == 0 {
				kind = "new_expiry"
				if summary.Paused >= cap {
					return nil
				}
				if err = evictParticipation(ctx, tx, a, "consent_lapsed"); err != nil {
					return err
				}
				if _, err = tx.Exec(ctx, `UPDATE accounts_parentalconsent SET status='expired',updated_at=now() WHERE minor_id=$1 AND status='active'`, id); err != nil {
					return err
				}
				if _, err = platform.Notify(ctx, tx, id, "system", "Your parental consent has expired", "Ask a parent or guardian to renew their consent so you can take part again.", "/guardianship/"); err != nil {
					return err
				}
				for _, guardian := range guardians {
					if _, err = platform.Notify(ctx, tx, guardian, "system", "Your ward's parental consent has expired", "Renew your consent so they can keep joining and chatting.", "/wards/"); err != nil {
						return err
					}
				}
				kind = "paused"
				return nil
			}
			for _, c := range valid {
				if c.expires == nil || c.expires.After(cutoff) || c.notice != "" {
					continue
				}
				for _, guardian := range guardians {
					if _, err = platform.Notify(ctx, tx, guardian, "system", "Your ward's parental consent is expiring soon", "Renew your consent soon so they can keep joining and chatting.", "/wards/"); err != nil {
						return err
					}
				}
				if _, err = tx.Exec(ctx, `UPDATE accounts_parentalconsent SET renewal_notice='soon',updated_at=now() WHERE id=$1`, c.id); err != nil {
					return err
				}
				nudged++
			}
			kind = "nudged"
			return nil
		})
		if err != nil {
			summary.Failed++
			if ctx.Err() != nil {
				return summary, ctx.Err()
			}
			continue
		}
		switch kind {
		case "nudged":
			summary.Nudged += nudged
		case "paused":
			summary.Paused++
			summary.NewlyExpired++
		case "new_expiry":
			summary.NewlyExpired++
		}
	}
	err = platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if summary.NewlyExpired > cap {
			if err := platform.RecordAudit(ctx, tx, platform.Actor{}, "accounts.consent_mass_lapse_guard", "", map[string]int{"newly_lapsed": summary.NewlyExpired, "cap": cap}); err != nil {
				return err
			}
		}
		return platform.RecordAudit(ctx, tx, platform.Actor{}, "accounts.consent_swept", "", map[string]int{"nudged": summary.Nudged, "paused": summary.Paused, "newly_lapsed": summary.NewlyExpired})
	})
	return summary, err
}
