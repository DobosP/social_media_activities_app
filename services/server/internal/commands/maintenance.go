package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/jobs"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

func external(raw string) string {
	raw = strings.TrimSpace(raw)
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return raw
	}
	return ""
}
func (s *Service) enrichPlaces(ctx context.Context, input map[string]json.RawMessage) (any, error) {
	var in struct {
		City, Source     string
		Limit            int
		DryRun           bool `json:"dry_run"`
		Google, Wikidata bool
	}
	if err := options(input, &in); err != nil {
		return nil, err
	}
	if !in.DryRun {
		if in.Wikidata && s.Config.WikidataEnrich == nil {
			return nil, missingDependency("Wikidata enricher")
		}
	}
	limit := 1000000
	if in.Limit > 0 {
		limit = in.Limit
	}
	rows, err := s.Runner.DB.Query(ctx, `SELECT id,opening_hours_raw,opening_hours FROM places_place WHERE ($1='' OR lower(address_city)=lower($1)) AND ($2='' OR source=$2) ORDER BY id LIMIT $3`, in.City, in.Source, limit)
	if err != nil {
		return nil, err
	}
	type place struct {
		id       int64
		raw      string
		schedule []byte
	}
	places := []place{}
	for rows.Next() {
		var p place
		if err = rows.Scan(&p.id, &p.raw, &p.schedule); err != nil {
			rows.Close()
			return nil, err
		}
		places = append(places, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	counts := map[string]int{"hours_parsed": 0, "hours_unparsed": 0, "hours_updated": 0}
	google := in.Google && s.Config.GoogleEnrich != nil
	if in.Google && !google {
		counts["google_disabled"] = 1
	}
	for _, p := range places {
		if p.raw == "" {
			continue
		}
		parsed := catalog.ParseOpeningHours(p.raw)
		if parsed == nil {
			counts["hours_unparsed"]++
		} else {
			counts["hours_parsed"]++
		}
		raw, _ := json.Marshal(parsed)
		var old any
		var current any
		_ = json.Unmarshal(p.schedule, &old)
		_ = json.Unmarshal(raw, &current)
		same, _ := json.Marshal(old)
		if string(same) != string(raw) && !in.DryRun {
			if _, err = s.Runner.DB.Exec(ctx, `UPDATE places_place SET opening_hours=$2 WHERE id=$1`, p.id, raw); err != nil {
				return counts, err
			}
			counts["hours_updated"]++
		}
	}
	if !in.DryRun && (google || in.Wikidata) {
		ids := []int64{}
		for _, p := range places {
			ids = append(ids, p.id)
		}
		if err = s.enrichExternal(ctx, ids, google, in.Wikidata, counts); err != nil {
			return counts, err
		}
	}
	return counts, nil
}
func (s *Service) seedBooking(ctx context.Context, input map[string]json.RawMessage) (any, error) {
	var in struct {
		DryRun bool `json:"dry_run"`
	}
	if err := options(input, &in); err != nil {
		return nil, err
	}
	var count int64
	if in.DryRun {
		err := s.Runner.DB.QueryRow(ctx, `SELECT count(*) FROM places_place p WHERE p.website<>'' AND NOT EXISTS(SELECT 1 FROM booking_placebookinginfo b WHERE b.place_id=p.id)`).Scan(&count)
		return map[string]int64{"would_create": count}, err
	}
	err := platform.Transaction(ctx, s.Runner.DB, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO booking_placebookinginfo(place_id,provider,deep_link,instructions,provider_place_ref,created_at,updated_at) SELECT p.id,'deeplink',p.website,'Reserve via the venue''s website.','',now(),now() FROM places_place p WHERE lower(p.website)~'^https?://' AND length(p.website)<=200 AND NOT EXISTS(SELECT 1 FROM booking_placebookinginfo b WHERE b.place_id=p.id) ON CONFLICT(place_id) DO NOTHING`)
		if err == nil {
			count = tag.RowsAffected()
		}
		return err
	})
	return map[string]int64{"created": count}, err
}
func (s *Service) ingestEvents(ctx context.Context, input map[string]json.RawMessage) (any, error) {
	var in struct {
		ICSURL       string `json:"ics_url"`
		ICSFile      string `json:"ics_file"`
		Place        *int64
		ActivityType *int64 `json:"activity_type"`
	}
	if err := options(input, &in); err != nil {
		return nil, err
	}
	if in.ICSURL == "" && in.ICSFile == "" {
		return nil, fmt.Errorf("%w: provide ics_url or ics_file", platform.ErrInvalid)
	}
	if in.Place != nil {
		var id int64
		if err := s.Runner.DB.QueryRow(ctx, `SELECT id FROM places_place WHERE id=$1`, *in.Place).Scan(&id); err != nil {
			return nil, err
		}
	}
	if in.ActivityType != nil {
		if *in.ActivityType < 1 {
			return nil, platform.ErrInvalid
		}
		var id int64
		if err := s.Runner.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE id=$1`, *in.ActivityType).Scan(&id); err != nil {
			return nil, err
		}
	}
	now := s.Runner.Config.Now()
	var raw []byte
	var err error
	if in.ICSFile != "" {
		f, e := os.Open(in.ICSFile)
		if e != nil {
			return nil, e
		}
		defer f.Close()
		raw, err = io.ReadAll(io.LimitReader(f, 8<<20+1))
		if len(raw) > 8<<20 {
			return nil, platform.ErrInvalid
		}
	} else if s.Runner.Config.FetchFeed != nil {
		raw, err = s.Runner.Config.FetchFeed(ctx, in.ICSURL)
	} else {
		raw, err = jobs.FetchFeed(ctx, in.ICSURL)
	}
	if err != nil {
		return nil, err
	}
	events, err := jobs.ParseICS(string(raw), now)
	if err != nil {
		return nil, err
	}
	count := 0
	for _, event := range events {
		effective := event.Starts
		if event.Ends != nil {
			effective = *event.Ends
		}
		if effective.Before(now) {
			continue
		}
		err = platform.Transaction(ctx, s.Runner.DB, func(tx pgx.Tx) error { return importEventWithType(ctx, tx, event, in.Place, in.ActivityType) })
		if err != nil {
			return map[string]int{"imported": count}, err
		}
		count++
	}
	return map[string]int{"imported": count}, nil
}
func importEvent(ctx context.Context, tx pgx.Tx, event jobs.RawEvent, place *int64) error {
	return importEventWithType(ctx, tx, event, place, nil)
}

func importEventWithType(ctx context.Context, tx pgx.Tx, event jobs.RawEvent, place, explicitType *int64) error {
	identity := "ical:" + event.ExternalID
	if event.ExternalID == "" {
		identity = fmt.Sprintf("ical:%v:%s:%s", place, event.Title, event.Starts.Format("2006-01-02T15:04:05Z07:00"))
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, identity); err != nil {
		return err
	}
	var id int64
	err := tx.QueryRow(ctx, `SELECT id FROM events_event WHERE source='ical' AND (($1<>'' AND external_id=$1) OR ($1='' AND place_id IS NOT DISTINCT FROM $2 AND title=$3 AND starts_at=$4)) FOR UPDATE`, event.ExternalID, place, event.Title, event.Starts).Scan(&id)
	if err != nil && err != pgx.ErrNoRows {
		return err
	}
	typ := explicitType
	if typ == nil {
		var classifyErr error
		typ, classifyErr = jobs.ClassifyActivity(ctx, tx, event.Title+" "+event.Description)
		if classifyErr != nil {
			return classifyErr
		}
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE events_event SET place_id=$2,activity_type_id=$3,title=$4,description=$5,starts_at=$6,ends_at=$7,url=$8,attribution=$9,license_name=$10,provenance_url=$11,updated_at=now() WHERE id=$1`, id, place, typ, event.Title, event.Description, event.Starts, event.Ends, external(event.URL), event.Attribution, event.License, external(event.Provenance))
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO events_event(title,description,starts_at,ends_at,url,source,external_id,created_at,updated_at,activity_type_id,place_id,attribution,license_name,provenance_url,is_import_held,is_tombstone,lifecycle_status,source_category,source_city,source_confidence,source_first_seen_at,source_last_seen_at,source_pack_id,source_release_id,source_snapshot_generated_at,source_snapshot_id,source_updated_at,source_venue_id,source_availability,source_currency,source_is_free,source_price_max,source_price_min,source_recurrence,source_timezone) VALUES($1,$2,$3,$4,$5,'ical',$6,now(),now(),$7,$8,$9,$10,$11,false,false,'scheduled','','',NULL,NULL,NULL,'','',NULL,'',NULL,'','','',NULL,NULL,NULL,'','')`, event.Title, event.Description, event.Starts, event.Ends, external(event.URL), event.ExternalID, typ, place, event.Attribution, event.License, external(event.Provenance))
	return err
}
