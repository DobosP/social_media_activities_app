package accounts

import (
	"context"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"sort"
	"strconv"
	"strings"
	"time"
)

type GuardrailInput struct {
	SupervisedOnly            bool
	Earliest, Latest, MaxOpen *int
	Weekdays, Categories      []string
}

func (s *Service) AllowAction(ctx context.Context, a platform.Actor, key string, limit int, window time.Duration) (bool, error) {
	return s.allowAction(ctx, a.ID, key, limit, window)
}
func (s *Service) SetGuardianGuardrail(ctx context.Context, a platform.Actor, wardID int64, in GuardrailInput) error {
	for _, hour := range []*int{in.Earliest, in.Latest} {
		if hour != nil && (*hour < 0 || *hour > 23) {
			return platform.ErrInvalid
		}
	}
	if in.MaxOpen != nil && (*in.MaxOpen < 1 || *in.MaxOpen > 50) {
		return platform.ErrInvalid
	}
	days := map[int]bool{}
	for _, raw := range in.Weekdays {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 7 {
			return platform.ErrInvalid
		}
		days[value] = true
	}
	weekdays := ""
	for value := 1; value <= 7; value++ {
		if days[value] {
			weekdays += strconv.Itoa(value)
		}
	}
	cats := []string{}
	seen := map[string]bool{}
	for _, raw := range in.Categories {
		raw = strings.TrimSpace(raw)
		if raw != "" && !seen[raw] {
			seen[raw] = true
			cats = append(cats, raw)
		}
	}
	if len(cats) > 100 {
		return platform.ErrInvalid
	}
	sort.Strings(cats)
	allowed, err := s.allowAction(ctx, a.ID, "guardian_guardrail", 30, time.Hour)
	if err != nil {
		return err
	}
	if !allowed {
		return platform.ErrForbidden
	}
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		fresh, err := s.actor(ctx, tx, a.ID)
		if err != nil {
			return err
		}
		if !fresh.IsActive {
			return platform.ErrForbidden
		}
		var relationship int64
		if err = tx.QueryRow(ctx, `SELECT id FROM accounts_guardianrelationship WHERE guardian_id=$1 AND ward_id=$2 AND status='active' FOR UPDATE`, fresh.ID, wardID).Scan(&relationship); err != nil {
			return platform.ErrForbidden
		}
		ward, err := s.actor(ctx, tx, wardID)
		if err != nil {
			return err
		}
		if ward.Cohort != "child" {
			return platform.ErrInvalid
		}
		if len(cats) > 0 {
			var count int
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM taxonomy_activitycategory WHERE slug=ANY($1)`, cats).Scan(&count); err != nil {
				return err
			}
			if count != len(cats) {
				return platform.ErrInvalid
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO accounts_guardianguardrail(relationship_id,supervised_only,latest_start_hour,max_open_joins,allowed_weekdays,earliest_start_hour,allowed_categories,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,now(),now()) ON CONFLICT(relationship_id) DO UPDATE SET supervised_only=EXCLUDED.supervised_only,latest_start_hour=EXCLUDED.latest_start_hour,max_open_joins=EXCLUDED.max_open_joins,allowed_weekdays=EXCLUDED.allowed_weekdays,earliest_start_hour=EXCLUDED.earliest_start_hour,allowed_categories=EXCLUDED.allowed_categories,updated_at=now()`, relationship, in.SupervisedOnly, in.Latest, in.MaxOpen, weekdays, in.Earliest, cats); err != nil {
			return err
		}
		return platform.RecordAudit(ctx, tx, fresh, "guardian.guardrail_set", "accounts.user:"+strconv.FormatInt(wardID, 10), map[string]any{"supervised_only": in.SupervisedOnly, "latest_start_hour": in.Latest, "max_open_joins": in.MaxOpen, "allowed_weekdays": weekdays, "earliest_start_hour": in.Earliest, "allowed_categories": cats})
	})
}
