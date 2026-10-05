package social

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
)

func TestCoverageCorrectionStrangerCardExactFieldsAndRegisteredAvatar(t *testing.T) {
	s, _ := testStore(t)
	s.Avatar = accounts.Avatar
	a := fixtureUser(t, s, "correction-stranger-viewer", "adult")
	b := fixtureUser(t, s, "correction-stranger-target", "adult")
	card, err := s.Profile(context.Background(), a, b.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for key := range card {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	want := []string{"tier", "public_id", "display", "avatar", "minor"}
	sort.Strings(want)
	if !reflect.DeepEqual(keys, want) || card["tier"] != "stranger" || card["public_id"] != b.PublicID || card["minor"] != false {
		t.Fatal("minimal stranger projection escaped exact source cap")
	}
	avatar, ok := card["avatar"].(string)
	const prefix = "data:image/svg+xml;base64,"
	if !ok || !strings.HasPrefix(avatar, prefix) {
		t.Fatal("registered signature avatar lost base64 SVG wire shape")
	}
	svg, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(avatar, prefix))
	if err != nil || !strings.Contains(string(svg), "<svg") {
		t.Fatal("registered avatar was not encoded SVG", err)
	}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"age_band", "cohort", "progression", "met_confirmed", "attendance", "last_seen", "date_joined", "fingerprint"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("minimal card exposed forbidden field", forbidden)
		}
	}
}
