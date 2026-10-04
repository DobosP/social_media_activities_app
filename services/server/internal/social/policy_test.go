package social

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestPolicyPresenceBoundsAndImmutableCohorts(t *testing.T) {
	s := New(nil, nil)
	cfg := DefaultPolicyConfig()
	cfg.ArrivalWindowBeforeHours = 1
	cfg.ArrivalWindowAfterHours = 1
	cfg.DepartureWindowAfterHours = 1
	cfg.ArrivalRetentionHours = 2
	if err := s.ConfigurePolicy(cfg); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	v := activityState{StartsAt: start}
	for _, tc := range []struct {
		offset  time.Duration
		allowed bool
	}{{-2 * time.Hour, false}, {-time.Hour, true}, {time.Hour, true}, {2 * time.Hour, false}} {
		if s.presenceWindow(v, start.Add(tc.offset), false) != tc.allowed {
			t.Fatal("wrong arrival policy", tc)
		}
	}
	if s.PresenceRetention() != 2*time.Hour {
		t.Fatal("wrong retention")
	}
	cfg.UserGroupCohorts["child"] = true
	if s.ConfigurePolicy(cfg) == nil {
		t.Fatal("minor self-created groups allowed")
	}
	cfg = DefaultPolicyConfig()
	cfg.SupportCompanionCohorts["teen"] = true
	if s.ConfigurePolicy(cfg) == nil {
		t.Fatal("minor support companion allowed")
	}
	cfg = DefaultPolicyConfig()
	cfg.ArrivalRetentionHours = 7
	if s.ConfigurePolicy(cfg) == nil {
		t.Fatal("presence privacy ceiling weakened")
	}
	cfg = DefaultPolicyConfig()
	cfg.ArrivalRetentionHours = 1
	if s.ConfigurePolicy(cfg) == nil {
		t.Fatal("presence window outlives retention")
	}
	cfg = DefaultPolicyConfig()
	cfg.InterestThreshold = 2
	if s.ConfigurePolicy(cfg) == nil {
		t.Fatal("interest quorum floor weakened")
	}
}

func TestPostgresNondefaultSocialPoliciesHaveBehavior(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	owner := fixtureUser(t, s, "policy-owner", "adult")
	id := fixtureActivity(t, s, owner, nil)
	cfg := DefaultPolicyConfig()
	cfg.ThreadPostLimit = 2
	cfg.ChatMaxLength = 8
	cfg.InterestLifetimeDays = 2
	cfg.InterestThreshold = 5
	cfg.SeriesSpawnLeadDays = 1
	cfg.SeriesSpawnBatch = 1
	if err := s.ConfigurePolicy(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WritePost(ctx, owner, "activity", id, PostInput{Body: strings.Repeat("a", 9)}, false); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("configured post cap ignored", err)
	}
	for _, body := range []string{"one", "two", "three"} {
		if _, err := s.WritePost(ctx, owner, "activity", id, PostInput{Body: body}, false); err != nil {
			t.Fatal(err)
		}
	}
	rows, cursor, err := s.Posts(ctx, owner, "activity", id, 0, 99)
	if err != nil || len(rows) != 2 || cursor == "" {
		t.Fatal("configured thread page cap", len(rows), cursor, err)
	}
	var place, typ int64
	if err = s.DB.QueryRow(ctx, `SELECT place_id,activity_type_id FROM social_activity WHERE id=$1`, id).Scan(&place, &typ); err != nil {
		t.Fatal(err)
	}
	gauge, err := s.ProposeGauge(ctx, owner, GaugeInput{Place: place, ActivityType: typ, CoarseWindow: "weekend_daytime"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.Gauge(ctx, owner, gauge)
	if err != nil {
		t.Fatal(err)
	}
	body := decodeObject(t, raw)
	if body["ready"] != false || body["remaining"] != float64(4) {
		t.Fatal("configured gauge quorum ignored", body)
	}
	var days float64
	if err = s.DB.QueryRow(ctx, `SELECT extract(epoch FROM expires_at-created_at)/86400 FROM social_activityinterest WHERE id=$1`, gauge).Scan(&days); err != nil || days != 2 {
		t.Fatal("gauge lifetime", days, err)
	}
	now := time.Now()
	for _, offset := range []time.Duration{12 * time.Hour, 18 * time.Hour, 48 * time.Hour} {
		_, err = s.CreateSeries(ctx, owner, SeriesInput{ActivityInput: ActivityInput{Place: place, ActivityType: typ, Title: "Configured series"}, Cadence: "weekly", FirstStartsAt: now.Add(offset)})
		if err != nil {
			t.Fatal(err)
		}
	}
	result, err := s.SpawnDueSeries(ctx, now)
	if err != nil || result.Spawned != 1 {
		t.Fatal("series lead/batch ignored", result, err)
	}
	var farSpawned int
	if err = s.DB.QueryRow(ctx, `SELECT count(*) FROM social_activity WHERE series_id IS NOT NULL AND starts_at>$1`, now.Add(24*time.Hour)).Scan(&farSpawned); err != nil || farSpawned != 0 {
		t.Fatal("series lead ignored", farSpawned, err)
	}
}
