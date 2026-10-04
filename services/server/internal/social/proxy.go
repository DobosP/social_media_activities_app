package social

import (
	"context"
	"encoding/json"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func (s *Service) actingAs(ctx context.Context, caller Actor, publicID string) (Actor, error) {
	if publicID == "" {
		return caller, nil
	}
	target, err := targetByPublicID(ctx, s.DB, publicID)
	if err != nil {
		return Actor{}, platform.ErrInvalid
	}
	yes, err := scalar(ctx, s.DB, `SELECT EXISTS(SELECT 1 FROM accounts_guardianrelationship WHERE guardian_id=$1 AND ward_id=$2 AND status='active')`, caller.ID, target.ID)
	if err := errorIfFalse(yes, err); err != nil {
		return Actor{}, err
	}
	return target, nil
}
func (s *Service) bodyActor(ctx context.Context, a Actor, body map[string]json.RawMessage) (Actor, error) {
	if raw, ok := body["on_behalf_of"]; ok {
		publicID, err := text(raw, 36, true)
		if err != nil {
			return Actor{}, err
		}
		delete(body, "on_behalf_of")
		return s.actingAs(ctx, a, publicID)
	}
	return a, nil
}
