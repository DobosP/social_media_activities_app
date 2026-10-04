package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/admin"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/recommendations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/jackc/pgx/v5"
)

func (s *Service) seedWorld(ctx context.Context, input map[string]json.RawMessage) (result any, err error) {
	stage := "accounts"
	defer func() {
		if err != nil {
			err = fmt.Errorf("development fixture %s: %w", stage, err)
		}
	}()
	if err := s.demoGate(); err != nil {
		return nil, err
	}
	if len(input) > 0 {
		return nil, platform.ErrInvalid
	}
	if s.Config.SeedAccount == nil || s.Config.SeedConsent == nil || s.Config.SeedComplete == nil || s.Config.SeedApproveVenue == nil || s.Runner.Config.Social == nil || s.Config.Recommendations == nil || s.Config.Messaging == nil {
		return nil, missingDependency("guarded development seed policy services")
	}
	users := map[string]platform.Actor{}
	staff, _, err := s.demoAccount(ctx, "admin", "Admin", "adult", true)
	if err != nil {
		return nil, err
	}
	users["admin"] = staff
	for _, cohort := range []struct{ key, band string }{{"ADULTS", "adult"}, {"TEENS", "16_17"}} {
		var people [][]string
		if err = demoPart("seed_demo_data", cohort.key, &people); err != nil {
			return nil, err
		}
		for _, person := range people {
			user, _, err := s.demoAccount(ctx, person[0], person[1], cohort.band, false)
			if err != nil {
				return nil, err
			}
			users[person[0]] = user
		}
	}
	var children [][]string
	if err = demoPart("seed_demo_data", "CHILDREN", &children); err != nil {
		return nil, err
	}
	for _, person := range children {
		child, created, err := s.demoAccount(ctx, person[0], person[1], "under_16", false)
		if err != nil {
			return nil, err
		}
		users[person[0]] = child
		if created {
			if err = s.Config.SeedConsent(ctx, users[person[2]], child); err != nil {
				return nil, err
			}
		}
	}
	stage = "interests"
	var interests map[string][]string
	if err = demoPart("seed_demo_data", "INTERESTS", &interests); err != nil {
		return nil, err
	}
	for name, slugs := range interests {
		if _, err = s.Config.Recommendations.SetInterests(ctx, users[name], slugs); err != nil {
			return nil, err
		}
	}
	stage = "places"
	var venues [][]any
	if err = demoPart("seed_demo_data", "PLACES", &venues); err != nil {
		return nil, err
	}
	places := []int64{}
	for _, venue := range venues {
		place, err := s.demoPlace(ctx, staff, venue[0].(string), venue[1].(float64), venue[2].(float64), true, "seed_demo_data")
		if err != nil {
			return nil, err
		}
		places = append(places, place)
	}
	stage = "activities"
	scenarios := []map[string]any{}
	for _, key := range []string{"ADULT_ACTS", "TEEN_ACTS", "CHILD_ACTS"} {
		var rows []map[string]any
		if err = demoPart("seed_demo_data", key, &rows); err != nil {
			return nil, err
		}
		scenarios = append(scenarios, rows...)
	}
	created, softErrors := 0, 0
	for _, spec := range scenarios {
		title := demoText(spec["title"])
		var exists bool
		if err = s.Runner.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM social_activity WHERE title=$1)`, title).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			continue
		}
		typ, typName, err := s.demoType(ctx, demoText(spec["slug"]))
		if err == pgx.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		owner := users[demoText(spec["owner"])]
		place := places[demoInt(spec["place"], 0)]
		if owner.Cohort == "child" {
			if err = s.Config.SeedApproveVenue(ctx, staff, place); err != nil {
				return nil, err
			}
		}
		now := s.Runner.Config.Now()
		start := demoHour(now.AddDate(0, 0, demoInt(spec["days"], 0)), demoInt(spec["hour"], 10))
		if demoBool(spec["soon"]) {
			start = now.Add(time.Hour)
		}
		end := start.Add(time.Duration(demoInt(spec["dur"], 2)) * time.Hour)
		body := social.ActivityInput{Place: place, ActivityType: typ, Title: title, Description: typName + " at " + demoText(venues[demoInt(spec["place"], 0)][0]) + ". All levels welcome!", StartsAt: start, EndsAt: &end, GuardianAccompanied: demoBool(spec["guardian"]), BeginnersWelcome: demoBool(spec["beginners"])}
		if description := demoText(spec["description"]); description != "" {
			body.Description = description
		}
		if capacity := demoInt(spec["capacity"], 0); capacity > 0 {
			body.Capacity = &capacity
		}
		if enrich, ok := spec["enrich"].(map[string]any); ok {
			raw, _ := json.Marshal(enrich)
			if err = json.Unmarshal(raw, &body); err != nil {
				return nil, err
			}
		}
		activity, err := s.Runner.Config.Social.CreateActivity(ctx, owner, body)
		if err != nil {
			return nil, err
		}
		created++
		for _, name := range demoStrings(spec["members"]) {
			if err = s.demoAdmit(ctx, owner, users[name], activity); err != nil {
				return nil, fmt.Errorf("development member admission: %w", err)
			}
		}
		for _, name := range demoStrings(spec["requesters"]) {
			if _, err = s.Runner.Config.Social.Join(ctx, users[name], activity); err != nil {
				softErrors++
			}
		}
		raw, _ := json.Marshal(spec["posts"])
		var posts [][]string
		_ = json.Unmarshal(raw, &posts)
		for _, post := range posts {
			if _, err = s.Runner.Config.Social.WritePost(ctx, users[post[0]], "activity", activity, social.PostInput{Body: post[1]}, false); err != nil {
				softErrors++
			}
		}
		if announcement := demoText(spec["announce"]); announcement != "" {
			if _, err = s.Runner.Config.Social.WritePost(ctx, owner, "activity", activity, social.PostInput{Body: announcement}, true); err != nil {
				softErrors++
			}
		}
		for _, name := range demoStrings(spec["rsvp_going"]) {
			if err = s.Runner.Config.Social.RSVP(ctx, users[name], activity, "going"); err != nil {
				softErrors++
			}
		}
		for _, name := range demoStrings(spec["arrivals"]) {
			if _, err = s.Runner.Config.Social.Presence(ctx, users[name], activity, "arrived", ""); err != nil {
				softErrors++
			}
		}
		if demoBool(spec["complete"]) {
			if err = s.Config.SeedComplete(ctx, owner, activity); err != nil {
				return nil, err
			}
			for _, name := range demoStrings(spec["met"]) {
				if err = s.Runner.Config.Social.MetConfirmed(ctx, users[name], activity, true); err != nil {
					softErrors++
				}
			}
		}
	}
	stage = "series"
	var seriesExists bool
	if err = s.Runner.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM social_activityseries WHERE title='Weekly sunrise yoga')`).Scan(&seriesExists); err != nil {
		return nil, err
	}
	if !seriesExists {
		typ, _, err := s.demoType(ctx, "yoga")
		if err != nil {
			return nil, err
		}
		start := demoHour(s.Runner.Config.Now().AddDate(0, 0, 2), 7)
		end := start.Add(time.Hour)
		capacity := 20
		if _, err = s.Runner.Config.Social.CreateSeries(ctx, users["ana"], social.SeriesInput{ActivityInput: social.ActivityInput{Place: places[9], ActivityType: typ, Title: "Weekly sunrise yoga", Description: "Gentle sunrise flow every week. Beginners welcome.", EndsAt: &end, Capacity: &capacity, BeginnersWelcome: true, MeetingPoint: "Yoga Space Cluj, studio A", WhatToBring: "Mat & water", CostBand: "free", Difficulty: "easy"}, Cadence: "weekly", FirstStartsAt: start}); err != nil {
			return nil, err
		}
		if _, err = s.Runner.Config.Social.SpawnDueSeries(ctx, s.Runner.Config.Now()); err != nil {
			return nil, err
		}
	}
	stage = "events"
	var events [][]any
	if err = demoPart("seed_demo_data", "EVENTS", &events); err != nil {
		return nil, err
	}
	for _, e := range events {
		title, slug := e[0].(string), e[1].(string)
		typ, _, err := s.demoType(ctx, slug)
		if err != nil {
			return nil, err
		}
		place := places[int(e[3].(float64))]
		if _, err = s.ensureDemoEvent(ctx, "manual", "", title, title+" in Cluj-Napoca.", s.Runner.Config.Now().AddDate(0, 0, int(e[2].(float64))).Add(19*time.Hour), nil, &place, &typ, "https://visitclujnapoca.ro/events"); err != nil {
			return nil, err
		}
	}
	stage = "giving"
	if err = s.seedGiving(ctx, staff, users, places); err != nil {
		return nil, err
	}
	var savedExists bool
	if err = s.Runner.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saved_searches_savedsearch WHERE user_id=$1)`, users["alex"].ID).Scan(&savedExists); err != nil {
		return nil, err
	}
	if !savedExists {
		for _, search := range []struct {
			slug      string
			beginners bool
		}{{"yoga", false}, {"book_club", true}} {
			typ, _, err := s.demoType(ctx, search.slug)
			if err == pgx.ErrNoRows {
				continue
			}
			if err != nil {
				return nil, err
			}
			if _, err = s.Config.Recommendations.CreateSavedSearch(ctx, users["alex"], recommendations.SavedSearchInput{ActivityType: &typ, City: "Cluj-Napoca", Beginners: search.beginners}); err != nil {
				softErrors++
			}
		}
	}
	softErrors += s.seedRelationships(ctx, users)
	if _, err = s.seedBooking(ctx, nil); err != nil {
		softErrors++
	}
	communities, err := s.Runner.Config.Social.GenerateCommunities(ctx, s.Runner.Config.Now())
	if err != nil {
		return nil, err
	}
	return map[string]any{"activities_created": created, "users": len(users), "venues": len(places), "communities": communities, "soft_errors": softErrors}, nil
}
func (s *Service) seedGiving(ctx context.Context, staff platform.Actor, users map[string]platform.Actor, places []int64) error {
	ops := admin.New(s.Runner.DB, nil, s.Runner.Config.Social, nil, nil)
	var campaign int64
	err := s.Runner.DB.QueryRow(ctx, `SELECT id FROM donations_campaign WHERE slug='youth-sports-kit'`).Scan(&campaign)
	if err == pgx.ErrNoRows {
		campaign, err = ops.Save(ctx, staff, "donations.campaign", 0, demoFields(map[string]any{"slug": "youth-sports-kit", "title": "Youth sports kit fund", "description": "Boots, balls and bibs so cost is never the reason a kid sits out.", "goal_cents": 500000, "currency": "EUR", "is_active": true}))
	}
	if err != nil {
		return err
	}
	for i, item := range []struct {
		cents    int
		donor    string
		campaign bool
	}{{5000, "alex", false}, {10000, "", false}, {25000, "maria", true}, {7500, "", false}, {15000, "", true}, {30000, "", true}} {
		var donor, camp *int64
		if item.donor != "" {
			id := users[item.donor].ID
			donor = &id
		}
		if item.campaign {
			camp = &campaign
		}
		if _, err = s.Runner.DB.Exec(ctx, `INSERT INTO donations_donation(donor_id,amount_cents,currency,campaign_id,provider,status,external_ref,completed_at,created_at,recurring) SELECT $1,$2,'EUR',$3,'demo','completed',$4::text,now(),now(),false WHERE NOT EXISTS(SELECT 1 FROM donations_donation WHERE external_ref=$4::text)`, donor, item.cents, camp, fmt.Sprintf("demo-%d", i)); err != nil {
			return err
		}
	}
	for _, item := range []struct {
		category string
		cents    int
	}{{"Infrastructure & hosting", 8000}, {"Community events", 5500}, {"Safety & moderation", 12000}, {"Accessibility improvements", 3500}} {
		var exists bool
		if err = s.Runner.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM donations_spendentry WHERE category=$1 AND period='2026 Q2')`, item.category).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			if _, err = ops.Save(ctx, staff, "donations.spendentry", 0, demoFields(map[string]any{"category": item.category, "amount_cents": item.cents, "currency": "EUR", "period": "2026 Q2", "note": "Q2 spend on " + item.category + "."})); err != nil {
				return err
			}
		}
	}
	for _, p := range []struct {
		name, kind, blurb string
		place             int
		website           string
	}{{"Cluj-Napoca Public Library", "library", "Free reading rooms and community spaces in the heart of the city.", 4, "https://bjc.ro"}, {"Sport Park Cluj", "civic", "Public sports grounds supporting youth and adult activities all year round.", 3, "https://visitclujnapoca.ro"}, {"Cluj Art Museum", "cultural", "Romanian cultural heritage through exhibitions, talks and family workshops.", 14, "https://visitclujnapoca.ro"}} {
		var exists bool
		if err = s.Runner.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM places_partner WHERE name=$1)`, p.name).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			if _, err = ops.Save(ctx, staff, "places.partner", 0, demoFields(map[string]any{"name": p.name, "kind": p.kind, "blurb": p.blurb, "place_id": places[p.place], "website": p.website, "is_verified": true, "is_active": true})); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Service) seedRelationships(ctx context.Context, users map[string]platform.Actor) int {
	failures := 0
	for _, set := range []struct {
		names []string
		cap   int
	}{{[]string{"alex", "maria", "dan", "elena", "george", "ana", "radu", "ioana"}, 8}, {[]string{"tina", "mihai", "sofia", "vlad", "bianca", "andrei"}, 6}, {[]string{"kevin", "luca", "sara", "david", "ema", "tudor"}, 6}} {
		made := 0
		for i := 0; i < len(set.names); i++ {
			for j := i + 1; j < len(set.names) && made < set.cap; j++ {
				a, b := users[set.names[i]], users[set.names[j]]
				id, err := s.Runner.Config.Social.RequestConnection(ctx, a, b.PublicID)
				if err != nil {
					continue
				}
				if err = s.Runner.Config.Social.RespondConnection(ctx, b, id, "accept"); err != nil {
					failures++
				} else {
					made++
				}
			}
		}
	}
	for _, pair := range [][2]string{{"alex", "maria"}, {"alex", "dan"}} {
		id, err := s.Config.Messaging.Start(ctx, users[pair[0]], "direct", []string{pair[1]}, "")
		if err == nil {
			err = s.Config.Messaging.Transition(ctx, users[pair[1]], id, "accept")
		}
		if err != nil {
			failures++
		}
	}
	id, err := s.Config.Messaging.Start(ctx, users["alex"], "group", []string{"maria", "george"}, "Saturday crew")
	if err == nil {
		for _, name := range []string{"maria", "george"} {
			if err = s.Config.Messaging.Transition(ctx, users[name], id, "accept"); err != nil {
				failures++
			}
		}
	} else {
		failures++
	}
	return failures
}
