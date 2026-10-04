package commands

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

// Bootstrap receives credentials only through the host's secret stdin path.
// The account policy callback creates a fresh unverified operator atomically;
// this adapter cannot overwrite roles, ages, consent or an existing account.
func (s *Service) bootstrapAdministrator(ctx context.Context, input map[string]json.RawMessage) (any, error) {
	for key := range input {
		if key != "username" && key != "password" {
			return nil, platform.ErrInvalid
		}
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := options(input, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Username) == "" || in.Password == "" {
		return nil, platform.ErrInvalid
	}
	if s.Config.BootstrapAdministrator == nil {
		return nil, missingDependency("native administrator bootstrap")
	}
	actor, err := s.Config.BootstrapAdministrator(ctx, in.Username, in.Password)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": actor.ID, "status": "created"}, nil
}
