package commands

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestNativeBootstrapStrictCredentialInputAndPrivateOutput(t *testing.T) {
	called := 0
	s := &Service{Config: Config{BootstrapAdministrator: func(ctx context.Context, user, password string) (platform.Actor, error) {
		called++
		return platform.Actor{ID: 7, Username: user, IsActive: true, IsStaff: true, IsSuperuser: true, Role: "admin", Cohort: "unassigned", AgeBand: "unknown"}, nil
	}}}
	for _, input := range []map[string]any{{}, {"username": "operator"}, {"username": "operator", "password": "synthetic-bootstrap-test", "age_band": "adult"}, {"username": "operator", "password": "synthetic-bootstrap-test", "is_staff": true}, {"username": "operator", "password": "synthetic-bootstrap-test", "consent": true}} {
		if _, err := s.bootstrapAdministrator(context.Background(), args(input)); err == nil {
			t.Fatal("invalid bootstrap input accepted")
		}
	}
	if called != 0 {
		t.Fatal("policy invoked before validation")
	}
	out, err := s.bootstrapAdministrator(context.Background(), args(map[string]any{"username": "operator", "password": "synthetic-bootstrap-test"}))
	if err != nil || called != 1 {
		t.Fatal(called, err)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "operator") || strings.Contains(string(raw), "password") || strings.Contains(string(raw), "synthetic") {
		t.Fatal("credential data returned")
	}
	s.Config.BootstrapAdministrator = func(context.Context, string, string) (platform.Actor, error) {
		return platform.Actor{}, errors.New("fresh account required")
	}
	if _, err = s.bootstrapAdministrator(context.Background(), args(map[string]any{"username": "operator", "password": "synthetic-bootstrap-test"})); err == nil {
		t.Fatal("policy rejection hidden")
	}
}
