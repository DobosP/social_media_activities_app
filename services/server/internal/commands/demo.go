package commands

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/jackc/pgx/v5"
)

// Source-owned fixture names and scenarios, extracted offline from the three
// existing seed commands. Passwords/auth material are intentionally absent.
//
//go:embed demo-manifest.json
var demoManifestJSON []byte
var demoManifest = func() map[string]map[string]json.RawMessage {
	var data map[string]map[string]json.RawMessage
	if json.Unmarshal(demoManifestJSON, &data) != nil {
		panic("invalid native fixture manifest")
	}
	return data
}()

func demoPart(name, key string, out any) error { return json.Unmarshal(demoManifest[name][key], out) }
func (s *Service) demoAccount(ctx context.Context, name, display, band string, staff bool) (platform.Actor, bool, error) {
	if err := s.demoGate(); err != nil {
		return platform.Actor{}, false, err
	}
	if s.Config.SeedAccount == nil {
		return platform.Actor{}, false, missingDependency("development-only identity assurance")
	}
	return s.Config.SeedAccount(ctx, name, display, band, staff)
}
func (s *Service) demoPlace(ctx context.Context, actor platform.Actor, name string, lon, lat float64, userSource bool, seed string) (int64, error) {
	if err := s.demoGate(); err != nil {
		return 0, err
	}
	var id int64
	err := s.Runner.DB.QueryRow(ctx, `SELECT id FROM places_place WHERE name=$1 ORDER BY id LIMIT 1`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != pgx.ErrNoRows {
		return 0, err
	}
	source, website := "osm", ""
	if userSource {
		source, website = "user", "https://visitclujnapoca.ro"
	}
	tags, _ := json.Marshal(map[string]string{"demo_seed": seed})
	err = platform.Transaction(ctx, s.Runner.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "demo-place:"+name); err != nil {
			return err
		}
		if err = tx.QueryRow(ctx, `SELECT id FROM places_place WHERE name=$1 ORDER BY id LIMIT 1`, name).Scan(&id); err == nil {
			return nil
		}
		if err != pgx.ErrNoRows {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO places_place(name,source,osm_type,osm_id,external_id,location,raw_tags,address_street,address_housenumber,address_city,address_postcode,address_country,opening_hours_raw,opening_hours,phone,website,first_seen_at,last_seen_at,attribution,license_name,provenance_url) VALUES($1,$2,'',NULL,'',ST_SetSRID(ST_MakePoint($3,$4),4326),$5,'','','Cluj-Napoca','','RO','',NULL,'',$6,now(),now(),'Development fixture','','') RETURNING id`, name, source, lon, lat, tags, website).Scan(&id); err != nil {
			return err
		}
		if userSource {
			if _, err := tx.Exec(ctx, `INSERT INTO social_userplaceproposal(place_id,proposer_id,status,required_confirmations,published_at,created_at) VALUES($1,$2,'pending',3,NULL,now())`, id, actor.ID); err != nil {
				return err
			}
		}
		return platform.RecordAudit(ctx, tx, actor, "demo.place_created", fmt.Sprintf("places.place:%d", id), map[string]string{"seed": seed})
	})
	if err != nil {
		return 0, err
	}
	if userSource {
		var proposal int64
		if err = s.Runner.DB.QueryRow(ctx, `SELECT id FROM social_userplaceproposal WHERE place_id=$1`, id).Scan(&proposal); err != nil {
			return 0, err
		}
		if !actor.IsStaff {
			return 0, platform.ErrForbidden
		}
		if err = s.Runner.Config.Social.StaffProposal(ctx, actor, proposal, true, "Development fixture publication"); err != nil {
			return 0, err
		}
	}
	return id, nil
}
func (s *Service) demoType(ctx context.Context, slug string) (int64, string, error) {
	var id int64
	var name string
	err := s.Runner.DB.QueryRow(ctx, `SELECT id,name FROM taxonomy_activitytype WHERE slug=$1 AND is_active`, slug).Scan(&id, &name)
	return id, name, err
}
func (s *Service) demoAdmit(ctx context.Context, owner, user platform.Actor, activity int64) error {
	var state string
	err := s.Runner.DB.QueryRow(ctx, `SELECT state FROM social_membership WHERE activity_id=$1 AND user_id=$2`, activity, user.ID).Scan(&state)
	if err == nil && state == "member" {
		return nil
	}
	if err != nil && err != pgx.ErrNoRows {
		return err
	}
	mid, err := s.Runner.Config.Social.Join(ctx, user, activity)
	if err != nil {
		return err
	}
	return s.Runner.Config.Social.Vote(ctx, owner, mid, true, true)
}
func (s *Service) seedDemoUsers(ctx context.Context, input map[string]json.RawMessage) (any, error) {
	if err := s.demoGate(); err != nil {
		return nil, err
	}
	var in struct{ Force bool }
	if err := options(input, &in); err != nil {
		return nil, err
	}
	if s.Runner.Config.Social == nil {
		return nil, missingDependency("social service")
	}
	users := []platform.Actor{}
	created := 0
	for _, person := range []struct {
		name, display string
		staff         bool
	}{{"ana.demo", "Ana Demo", false}, {"dan.demo", "Dan Demo", false}, {"staff.demo", "Staff Demo", true}} {
		a, newUser, err := s.demoAccount(ctx, person.name, person.display, "adult", person.staff)
		if err != nil {
			return nil, err
		}
		users = append(users, a)
		if newUser {
			created++
		}
	}
	var exists bool
	if err := s.Runner.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM social_activity WHERE owner_id=$1 AND title LIKE '[DEMO]%')`, users[0].ID).Scan(&exists); err != nil {
		return nil, err
	}
	made := false
	if !exists {
		var place, typ int64
		var typName string
		err := s.Runner.DB.QueryRow(ctx, `SELECT p.id FROM places_place p WHERE `+catalog.PolicyFromContext(ctx).PlaceSQL()+` AND EXISTS(SELECT 1 FROM places_placeactivity pa WHERE pa.place_id=p.id) ORDER BY p.id LIMIT 1`).Scan(&place)
		if err != nil && err != pgx.ErrNoRows {
			return nil, err
		}
		if err == nil {
			if err = s.Runner.DB.QueryRow(ctx, `SELECT id,name FROM taxonomy_activitytype WHERE is_active ORDER BY slug LIMIT 1`).Scan(&typ, &typName); err != nil {
				return nil, err
			}
			now := s.Runner.Config.Now()
			start := demoHour(now.AddDate(0, 0, 2), 18)
			activity, err := s.Runner.Config.Social.CreateActivity(ctx, users[0], social.ActivityInput{Place: place, ActivityType: typ, Title: "[DEMO] " + typName + " cu Ana", Description: "Meetup demo pentru testarea vederilor autentificate.", StartsAt: start, BeginnersWelcome: true})
			if err != nil {
				return nil, err
			}
			if err = s.demoAdmit(ctx, users[0], users[1], activity); err != nil {
				return nil, err
			}
			made = true
		}
	}
	return map[string]any{"users_created": created, "activity_created": made}, nil
}
func demoHour(t time.Time, hour int) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), hour, 0, 0, 0, t.Location())
}

func (s *Service) seedBrowse(ctx context.Context, input map[string]json.RawMessage) (any, error) {
	if err := s.demoGate(); err != nil {
		return nil, err
	}
	if len(input) > 0 {
		return nil, platform.ErrInvalid
	}
	if s.Runner.Config.Social == nil || s.Config.Recommendations == nil {
		return nil, missingDependency("social recommendations")
	}
	organizer, _, err := s.demoAccount(ctx, "demo_organizer", "Demo Organizer", "adult", false)
	if err != nil {
		return nil, err
	}
	var venues [][]any
	var activities [][]any
	if err = demoPart("seed_browse_demo", "_VENUES", &venues); err != nil {
		return nil, err
	}
	if err = demoPart("seed_browse_demo", "_ACTIVITIES", &activities); err != nil {
		return nil, err
	}
	places := []int64{}
	for _, venue := range venues {
		id, err := s.demoPlace(ctx, organizer, venue[0].(string), venue[1].(float64), venue[2].(float64), false, "seed_browse_demo")
		if err != nil {
			return nil, err
		}
		places = append(places, id)
	}
	created, skipped := 0, 0
	for _, row := range activities {
		typ, _, err := s.demoType(ctx, row[0].(string))
		if err == pgx.ErrNoRows {
			skipped++
			continue
		}
		if err != nil {
			return nil, err
		}
		var exists bool
		if err = s.Runner.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM social_activity WHERE title=$1)`, row[1]).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			continue
		}
		start := demoHour(s.Runner.Config.Now().AddDate(0, 0, int(row[3].(float64))), int(row[4].(float64)))
		_, err = s.Runner.Config.Social.CreateActivity(ctx, organizer, social.ActivityInput{Place: places[int(row[8].(float64))], ActivityType: typ, Title: row[1].(string), Description: row[2].(string), StartsAt: start, CostBand: row[5].(string), Difficulty: row[6].(string), BeginnersWelcome: row[7].(bool)})
		if err != nil {
			return map[string]int{"created": created}, err
		}
		created++
	}
	var tester platform.Actor
	err = s.Runner.DB.QueryRow(ctx, `SELECT id,public_id::text,username,display_name,cohort,age_band,is_identity_verified,is_active,is_staff FROM accounts_user WHERE username='tester'`).Scan(&tester.ID, &tester.PublicID, &tester.Username, &tester.DisplayName, &tester.Cohort, &tester.AgeBand, &tester.IdentityVerified, &tester.IsActive, &tester.IsStaff)
	if err != nil && err != pgx.ErrNoRows {
		return nil, err
	}
	if err == nil {
		if _, err = s.Config.Recommendations.SetTopics(ctx, tester, []string{"sport", "outdoor"}); err != nil {
			return nil, err
		}
	}
	return map[string]int{"created": created, "skipped_type": skipped, "venues": len(places)}, nil
}
func demoText(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
func demoBool(v any) bool { b, _ := v.(bool); return b }
func demoInt(v any, defaultValue int) int {
	n, ok := v.(float64)
	if !ok {
		return defaultValue
	}
	return int(n)
}
func demoStrings(v any) []string {
	raw, _ := json.Marshal(v)
	var values []string
	_ = json.Unmarshal(raw, &values)
	return values
}
func demoFields(input map[string]any) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range input {
		out[k], _ = json.Marshal(v)
	}
	return out
}
func (s *Service) ensureDemoEvent(ctx context.Context, source, externalID, title, description string, start time.Time, end *time.Time, place, typ *int64, url string) (bool, error) {
	if err := s.demoGate(); err != nil {
		return false, err
	}
	created := false
	err := platform.Transaction(ctx, s.Runner.DB, func(tx pgx.Tx) error {
		key := externalID
		if key == "" {
			key = title
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "demo-event:"+source+":"+key); err != nil {
			return err
		}
		var id int64
		err := tx.QueryRow(ctx, `SELECT id FROM events_event WHERE source=$1 AND (($2<>'' AND external_id=$2) OR ($2='' AND title=$3)) ORDER BY id LIMIT 1 FOR UPDATE`, source, externalID, title).Scan(&id)
		if err == nil {
			if externalID != "" {
				_, err = tx.Exec(ctx, `UPDATE events_event SET title=$2,description=$3,starts_at=$4,ends_at=$5,place_id=$6,activity_type_id=$7,url=$8,updated_at=now() WHERE id=$1`, id, title, description, start, end, place, typ, external(url))
			}
			return err
		}
		if err != pgx.ErrNoRows {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO events_event(title,description,starts_at,ends_at,url,source,external_id,created_at,updated_at,activity_type_id,place_id,attribution,license_name,provenance_url,is_import_held,is_tombstone,lifecycle_status,source_category,source_city,source_confidence,source_first_seen_at,source_last_seen_at,source_pack_id,source_release_id,source_snapshot_generated_at,source_snapshot_id,source_updated_at,source_venue_id,source_availability,source_currency,source_is_free,source_price_max,source_price_min,source_recurrence,source_timezone) VALUES($1,$2,$3,$4,$5,$6,$7,now(),now(),$8,$9,'Demo data (dev only)','','',false,false,'scheduled','','',NULL,NULL,NULL,'','',NULL,'',NULL,'','','',NULL,NULL,NULL,'','')`, title, description, start, end, external(url), source, externalID, typ, place)
		created = err == nil
		return err
	})
	return created, err
}
func (s *Service) generateDemoEvents(ctx context.Context, input map[string]json.RawMessage) (any, error) {
	if err := s.demoGate(); err != nil {
		return nil, err
	}
	var in struct {
		Synthesize int
		Force      bool
		DryRun     bool `json:"dry_run"`
	}
	if err := options(input, &in); err != nil {
		return nil, err
	}
	if in.Synthesize < 0 || in.Synthesize > 10000 {
		return nil, platform.ErrInvalid
	}
	now := s.Runner.Config.Now()
	rows, err := s.Runner.DB.Query(ctx, `SELECT id,starts_at,ends_at FROM events_event WHERE starts_at<$1 AND source<>'demo' ORDER BY id`, now)
	if err != nil {
		return nil, err
	}
	type event struct {
		id    int64
		start time.Time
		end   *time.Time
	}
	events := []event{}
	for rows.Next() {
		var e event
		if err = rows.Scan(&e.id, &e.start, &e.end); err != nil {
			rows.Close()
			return nil, err
		}
		events = append(events, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, e := range events {
		weeks := int(now.Sub(e.start).Hours()/24/7) + 1
		delta := time.Duration(weeks) * 7 * 24 * time.Hour
		start := e.start.Add(delta)
		if start.Before(now) {
			delta += 7 * 24 * time.Hour
			start = e.start.Add(delta)
		}
		var end *time.Time
		if e.end != nil {
			v := e.end.Add(delta)
			end = &v
		}
		if !in.DryRun {
			if _, err = s.Runner.DB.Exec(ctx, `UPDATE events_event SET starts_at=$2,ends_at=$3,updated_at=now() WHERE id=$1`, e.id, start, end); err != nil {
				return nil, err
			}
		}
	}
	type named struct {
		id   int64
		name string
	}
	places, types := []named{}, []named{}
	load := func(query string, out *[]named) error {
		rows, err := s.Runner.DB.Query(ctx, query)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item named
			if err = rows.Scan(&item.id, &item.name); err != nil {
				return err
			}
			*out = append(*out, item)
		}
		return rows.Err()
	}
	if in.Synthesize > 0 {
		if err = load(`SELECT p.id,p.name FROM places_place p WHERE `+catalog.PolicyFromContext(ctx).PlaceSQL()+` ORDER BY p.id LIMIT 25`, &places); err != nil {
			return nil, err
		}
		if len(places) == 0 {
			return nil, fmt.Errorf("%w: no public fixture places", platform.ErrInvalid)
		}
		if err = load(`SELECT id,name FROM taxonomy_activitytype WHERE is_active ORDER BY slug`, &types); err != nil {
			return nil, err
		}
		if len(types) == 0 {
			return nil, platform.ErrInvalid
		}
	}
	synthesized := 0
	for n := 0; n < in.Synthesize; n++ {
		p := places[n%len(places)]
		label := "Comunitate"
		var typ *int64
		if n%3 != 2 {
			t := types[n%len(types)]
			typ = &t.id
			label = t.name
		}
		start := demoHour(now.AddDate(0, 0, 1+(n*2)%21), 10+(n*3)%9)
		end := start.Add(2 * time.Hour)
		if n%4 == 3 {
			end = start.Add(48 * time.Hour)
		}
		if in.DryRun {
			synthesized++
			continue
		}
		created, err := s.ensureDemoEvent(ctx, "demo", fmt.Sprintf("demo:%d", n), "[DEMO] "+label+" la "+p.name, "", start, &end, &p.id, typ, "")
		if err != nil {
			return nil, err
		}
		if created {
			synthesized++
		}
	}
	return map[string]int{"rescheduled": len(events), "synthesized": synthesized}, nil
}
