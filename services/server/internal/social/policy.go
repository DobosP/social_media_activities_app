package social

import (
	"fmt"
	"time"
)

// PolicyConfig exposes operational policy without making cohort/safety floors optional.
// Configure once before serving; a Service is shared by concurrent handlers/jobs.
type PolicyConfig struct {
	ChatMaxLength, ThreadPostLimit, MembershipListLimit, CommunityActivitiesPageSize int
	SeriesSpawnLeadDays, SeriesSpawnBatch, InterestLifetimeDays, InterestThreshold   int
	ArrivalWindowBeforeHours, ArrivalWindowAfterHours, DepartureWindowAfterHours     int
	ArrivalRetentionHours, PlaceProposalDedupRadiusM                                 int
	UserGroupCohorts, SupportCompanionCohorts                                        map[string]bool
}

func DefaultPolicyConfig() PolicyConfig {
	return PolicyConfig{ChatMaxLength: 4000, ThreadPostLimit: 100, MembershipListLimit: 100, CommunityActivitiesPageSize: 100,
		SeriesSpawnLeadDays: 14, SeriesSpawnBatch: 500, InterestLifetimeDays: 14, InterestThreshold: 3,
		ArrivalWindowBeforeHours: 2, ArrivalWindowAfterHours: 3, DepartureWindowAfterHours: 3,
		ArrivalRetentionHours: 6, PlaceProposalDedupRadiusM: 60,
		UserGroupCohorts: map[string]bool{"adult": true}, SupportCompanionCohorts: map[string]bool{"adult": true}}
}
func adultOnlyCohorts(cohorts map[string]bool) bool {
	for cohort, enabled := range cohorts {
		if enabled && cohort != "adult" {
			return false
		}
	}
	return true
}
func (c PolicyConfig) Validate() error {
	for _, v := range []struct {
		name        string
		n, min, max int
	}{
		{"CHAT_MAX_LENGTH", c.ChatMaxLength, 1, 4000},
		{"SOCIAL_THREAD_POST_LIMIT", c.ThreadPostLimit, 1, 1000}, {"SOCIAL_MEMBERSHIP_LIST_LIMIT", c.MembershipListLimit, 1, 1000},
		{"COMMUNITY_ACTIVITIES_PAGE_SIZE", c.CommunityActivitiesPageSize, 1, 1000},
		{"SERIES_SPAWN_LEAD_DAYS", c.SeriesSpawnLeadDays, 1, 90}, {"SERIES_SPAWN_BATCH", c.SeriesSpawnBatch, 1, 1000},
		{"INTEREST_LIFETIME_DAYS", c.InterestLifetimeDays, 1, 90}, {"INTEREST_THRESHOLD", c.InterestThreshold, 3, 1000},
		{"ARRIVAL_WINDOW_BEFORE_HOURS", c.ArrivalWindowBeforeHours, 0, 24}, {"ARRIVAL_WINDOW_AFTER_HOURS", c.ArrivalWindowAfterHours, 0, 6},
		{"DEPARTURE_WINDOW_AFTER_HOURS", c.DepartureWindowAfterHours, 0, 6}, {"ARRIVAL_RETENTION_HOURS", c.ArrivalRetentionHours, 1, 6},
		{"PLACE_PROPOSAL_DEDUP_RADIUS_M", c.PlaceProposalDedupRadiusM, 1, 1000}} {
		if v.n < v.min || v.n > v.max {
			return fmt.Errorf("%s is outside native bounds", v.name)
		}
	}
	if c.ArrivalWindowAfterHours > c.ArrivalRetentionHours || c.DepartureWindowAfterHours > c.ArrivalRetentionHours {
		return fmt.Errorf("ARRIVAL_RETENTION_HOURS must cover presence windows")
	}
	if !adultOnlyCohorts(c.UserGroupCohorts) {
		return fmt.Errorf("GROUPS_USER_CREATION_COHORTS permits only adult or empty")
	}
	if !adultOnlyCohorts(c.SupportCompanionCohorts) {
		return fmt.Errorf("SUPPORT_COMPANION_COHORTS permits only adult or empty")
	}
	return nil
}
func (s *Service) ConfigurePolicy(c PolicyConfig) error {
	if err := c.Validate(); err != nil {
		return err
	}
	// Do not retain caller-owned mutable maps.
	c.UserGroupCohorts = map[string]bool{"adult": c.UserGroupCohorts["adult"]}
	c.SupportCompanionCohorts = map[string]bool{"adult": c.SupportCompanionCohorts["adult"]}
	s.Policy = c
	return nil
}
func (s *Service) PresenceRetention() time.Duration {
	return time.Duration(s.Policy.ArrivalRetentionHours) * time.Hour
}
func (s *Service) presenceWindow(v activityState, now time.Time, departing bool) bool {
	if departing {
		end := v.StartsAt.Add(3 * time.Hour)
		if v.EndsAt != nil {
			end = *v.EndsAt
		}
		return !now.Before(v.StartsAt) && !now.After(end.Add(time.Duration(s.Policy.DepartureWindowAfterHours)*time.Hour))
	}
	return !now.Before(v.StartsAt.Add(-time.Duration(s.Policy.ArrivalWindowBeforeHours)*time.Hour)) && !now.After(v.StartsAt.Add(time.Duration(s.Policy.ArrivalWindowAfterHours)*time.Hour))
}
