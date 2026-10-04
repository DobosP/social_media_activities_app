package social

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestRetirementSeriesTemplateOwnerOnlyRosterNoBackfillAndLifecycle(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	owner := fixtureUser(t, s, "retirement-series-owner", "adult")
	other := fixtureUser(t, s, "retirement-series-outsider", "adult")
	base := fixtureActivity(t, s, owner, nil)
	var place, typ int64
	if err := s.DB.QueryRow(ctx, `SELECT place_id,activity_type_id FROM social_activity WHERE id=$1`, base).Scan(&place, &typ); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	s.Now = func() time.Time { return now }
	first := now.Add(24 * time.Hour)
	location, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		t.Fatal(err)
	}
	end := first.Add(90 * time.Minute)
	amount := Decimal("12.50")
	series, err := s.CreateSeries(ctx, owner, SeriesInput{ActivityInput: ActivityInput{Place: place, ActivityType: typ, Title: "Synthetic recurring meetup", Description: "Facts-only template", StartsAt: first, EndsAt: &end, MeetingPoint: "North gate", WhatToBring: "Water", OrganizerNote: "Template note", CostBand: "paid", CostAmount: &amount, CostNote: "Room hire", Capacity: p(5), BeginnersWelcome: true}, FirstStartsAt: first, Cadence: "weekly"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSeriesNote(ctx, owner, series, "One instance only"); err != nil {
		t.Fatal(err)
	}
	before, err := s.SpawnDueSeries(ctx, now)
	if err != nil || before.Spawned != 1 {
		t.Fatal("first in-window occurrence did not spawn", before, err)
	}
	var activity int64
	var starts, ends, cursor time.Time
	var title, meeting, cost, note, cohort, band, description, bring string
	var copiedOwner, copiedPlace, copiedType int64
	var beginners bool
	if err := s.DB.QueryRow(ctx, `SELECT id,starts_at,ends_at,title,meeting_point,cost_amount::text,organizer_note,owner_id,place_id,activity_type_id,beginners_welcome,cohort,cost_band,description,what_to_bring FROM social_activity WHERE series_id=$1`, series).Scan(&activity, &starts, &ends, &title, &meeting, &cost, &note, &copiedOwner, &copiedPlace, &copiedType, &beginners, &cohort, &band, &description, &bring); err != nil {
		t.Fatal(err)
	}
	if !starts.Equal(first) || !ends.Equal(end) || title != "Synthetic recurring meetup" || meeting != "North gate" || cost != "12.50" || note != "Template note\n\nOne instance only" || copiedOwner != owner.ID || copiedPlace != place || copiedType != typ || !beginners || cohort != owner.Cohort || band != "paid" || description != "Facts-only template" || bring != "Water" {
		t.Fatal("series occurrence did not copy exact template/duration/concrete cost and one-off note")
	}
	var seats, owners, notices int
	if err := s.DB.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE role='owner' AND user_id=$2) FROM social_membership WHERE activity_id=$1 AND state='member'`, activity, owner.ID).Scan(&seats, &owners); err != nil || seats != 1 || owners != 1 {
		t.Fatal("series spawn copied a prior roster rather than only owner seat")
	}
	var nextNote string
	if err := s.DB.QueryRow(ctx, `SELECT next_starts_at,next_instance_note FROM social_activityseries WHERE id=$1`, series).Scan(&cursor, &nextNote); err != nil || !cursor.Equal(first.In(location).AddDate(0, 0, 7)) || nextNote != "" {
		t.Fatal("spawn did not advance cursor once and consume one-off note")
	}
	for _, tick := range []time.Time{now, now.Add(6 * time.Hour)} {
		if result, err := s.SpawnDueSeries(ctx, tick); err != nil || result.Spawned != 0 {
			t.Fatal("same tick or existing upcoming occurrence was duplicated", result, err)
		}
	}
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM notifications_notification`).Scan(&notices); err != nil || notices != 0 {
		t.Fatal("series spawn manufactured membership/participation notifications")
	}
	for _, action := range []string{"pause", "end"} {
		if err := s.TransitionSeries(ctx, other, series, action); !errors.Is(err, platform.ErrForbidden) {
			t.Fatal("outsider changed series lifecycle", err)
		}
	}
	if err := s.TransitionSeries(ctx, owner, series, "pause"); err != nil {
		t.Fatal(err)
	}
	if result, err := s.SpawnDueSeries(ctx, now.AddDate(0, 0, 30)); err != nil || result.Spawned != 0 {
		t.Fatal("paused series spawned")
	}
	if err := s.TransitionSeries(ctx, owner, series, "resume"); err != nil {
		t.Fatal(err)
	}
	late := now.AddDate(0, 0, 60)
	if result, err := s.SpawnDueSeries(ctx, late); err != nil || result.Spawned != 1 {
		t.Fatal("resumed stale cursor failed to skip past occurrences", result, err)
	}
	var latest time.Time
	if err := s.DB.QueryRow(ctx, `SELECT max(starts_at),count(*) FROM social_activity WHERE series_id=$1`, series).Scan(&latest, &seats); err != nil || latest.Before(late) || seats != 2 {
		t.Fatal("resume backfilled historical occurrences")
	}
	if err := s.TransitionSeries(ctx, owner, series, "end"); err != nil {
		t.Fatal(err)
	}
	if result, err := s.SpawnDueSeries(ctx, late.AddDate(0, 0, 7)); err != nil || result.Spawned != 0 {
		t.Fatal("ended series spawned")
	}
}

func TestRetirementSeriesCohortDriftPausesWithAudit(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	owner := fixtureUser(t, s, "retirement-series-drift", "adult")
	base := fixtureActivity(t, s, owner, nil)
	var place, typ int64
	if err := s.DB.QueryRow(ctx, `SELECT place_id,activity_type_id FROM social_activity WHERE id=$1`, base).Scan(&place, &typ); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	series, err := s.CreateSeries(ctx, owner, SeriesInput{ActivityInput: ActivityInput{Place: place, ActivityType: typ, Title: "Synthetic cohort drift"}, FirstStartsAt: now.Add(time.Hour), Cadence: "weekly"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET cohort='teen',age_band='16_17' WHERE id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	result, err := s.SpawnDueSeries(ctx, now)
	if err != nil || result.Paused != 1 || result.Spawned != 0 {
		t.Fatal("cohort-drifted owner kept spawning into old cohort", result, err)
	}
	var paused bool
	var audits int
	if err := s.DB.QueryRow(ctx, `SELECT status='paused',(SELECT count(*) FROM safety_auditlog WHERE event='series.paused' AND data->>'reason'='owner_cohort_drift') FROM social_activityseries WHERE id=$1`, series).Scan(&paused, &audits); err != nil || !paused || audits != 1 {
		t.Fatal("cohort drift pause lacked truthful audit")
	}
}
