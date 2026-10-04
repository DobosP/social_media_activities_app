package web

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
)

func TestGroupCreationPolicyMatchesBrowserAndDomain(t *testing.T) {
	s, actor, _, typ := socialLegacyFixture(t)
	s.Social.AllowUserGroups = true
	for _, enabled := range []bool{false, true} {
		policy := social.DefaultPolicyConfig()
		policy.UserGroupCohorts = map[string]bool{"adult": enabled}
		if err := s.Social.ConfigurePolicy(policy); err != nil {
			t.Fatal(err)
		}
		html, legacy := socialLegacyHTML(t, s, actor, "communities", 0, "")
		if legacy["can_create"] != enabled || strings.Contains(html, "/groups/new/") != enabled {
			t.Fatal("browser offered the wrong configured capability", enabled)
		}
		r := socialLegacyRequest("GET", "/communities/", actor, 0, nil)
		payload, _, _, _, err := s.BuildSPA(context.Background(), r, actor, "communities", legacy)
		if err != nil || spaMap(payload["data"])["canCreate"] != enabled {
			t.Fatal("SPA capability differs from domain policy", enabled, err)
		}
		_, err = s.Social.CreateGroup(context.Background(), actor, social.GroupInput{Title: "Policy checked group", City: "Synthetic Policy City", ActivityType: &typ})
		if enabled && err != nil || !enabled && !errors.Is(err, platform.ErrForbidden) {
			t.Fatal("domain and presentation permission differ", enabled, err)
		}
	}
}
