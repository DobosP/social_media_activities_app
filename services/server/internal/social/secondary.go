package social

import (
	"context"
	"encoding/json"
	"regexp"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

type Decimal string

var decimalSyntax = regexp.MustCompile(`^(?:0|[1-9][0-9]{0,4})(?:\.[0-9]{1,2})?$`)

func validDecimal(value string) bool { return decimalSyntax.MatchString(value) }
func (d *Decimal) UnmarshalJSON(raw []byte) error {
	var value string
	if len(raw) > 0 && raw[0] == '"' {
		if json.Unmarshal(raw, &value) != nil {
			return platform.ErrInvalid
		}
	} else {
		value = string(raw)
	}
	if !validDecimal(value) {
		return platform.ErrInvalid
	}
	*d = Decimal(value)
	return nil
}
func secondaryTypes(ctx context.Context, q platform.Querier, a Actor, primary int64, values []int64) ([]int64, error) {
	out := []int64{}
	seen := map[int64]bool{primary: true}
	var rail EffectiveGuardrail
	var err error
	if a.Cohort == "child" {
		rail, err = effectiveRail(ctx, q, a.ID)
		if err != nil {
			return nil, err
		}
	}
	for _, id := range values {
		if id <= 0 {
			return nil, platform.ErrInvalid
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
		if len(out) > 2 {
			return nil, platform.ErrInvalid
		}
		yes, err := scalar(ctx, q, `SELECT EXISTS(SELECT 1 FROM taxonomy_activitytype WHERE id=$1 AND is_active)`, id)
		if err := errorIfFalse(yes, err); err != nil {
			return nil, err
		}
		if a.Cohort == "child" {
			if err := categoryAllowed(ctx, q, rail, id); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}
func replaceSecondary(ctx context.Context, tx pgx.Tx, id int64, values []int64) error {
	if _, err := tx.Exec(ctx, `DELETE FROM social_activity_secondary_types WHERE activity_id=$1`, id); err != nil {
		return err
	}
	for _, extra := range values {
		if _, err := tx.Exec(ctx, `INSERT INTO social_activity_secondary_types(activity_id,activitytype_id) VALUES($1,$2)`, id, extra); err != nil {
			return err
		}
	}
	return nil
}
