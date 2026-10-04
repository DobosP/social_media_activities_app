package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (r *Runner) SyncRoedu(ctx context.Context) (map[string]any, error) {
	if !r.Config.RoeduSyncEnabled || r.Config.Roedu == nil || r.Config.Roedu.APIKey == "" {
		return map[string]any{"disabled": true}, nil
	}
	city := r.Config.RoeduCity
	if city == "" {
		city = "Cluj-Napoca"
	}
	pack, err := r.Config.Roedu.Read(ctx, city)
	if err != nil {
		var coverErr error
		if r.Config.ResolvePlaceCovers != nil {
			_, coverErr = r.Config.ResolvePlaceCovers(ctx, nil)
		}
		if errors.Is(err, ErrProductRefused) {
			if auditErr := platform.Transaction(ctx, r.DB, func(tx pgx.Tx) error {
				return platform.RecordAudit(ctx, tx, platform.Actor{}, "ingestion.roedu_refused", "", map[string]any{"pack": SocialPack, "city": city})
			}); auditErr != nil {
				return nil, auditErr
			}
			return map[string]any{"refused": true, "refreshed": false}, coverErr
		}
		return nil, err
	}
	summary, err := r.ApplyRoedu(ctx, pack, city)
	if r.Config.ResolvePlaceCovers != nil {
		_, coverErr := r.Config.ResolvePlaceCovers(ctx, nil)
		if err == nil {
			err = coverErr
		}
	}
	return summary, err
}
func stringValue(m map[string]any, key string) string  { value, _ := m[key].(string); return value }
func numberValue(m map[string]any, key string) float64 { value, _ := number(m[key]); return value }
func dateValue(m map[string]any, key string) any {
	value, ok := aware(m[key])
	if !ok {
		return nil
	}
	return value
}
func money(m map[string]any, key string) any {
	value := m[key]
	if value == nil {
		return nil
	}
	return fmt.Sprint(value)
}

// ApplyRoedu materializes facts only. Child-venue approvals stay operator-owned;
// full absence reconciliation requires an unbounded clean promoted snapshot.
type RoeduApplyOptions struct {
	MinConfidence                 float64
	AllowSnapshotRollback, DryRun bool
}

func (r *Runner) ApplyRoedu(ctx context.Context, pack PackRead, city string) (map[string]any, error) {
	return r.ApplyRoeduWithOptions(ctx, pack, city, RoeduApplyOptions{MinConfidence: 1})
}
func (r *Runner) ApplyRoeduWithOptions(ctx context.Context, pack PackRead, city string, options RoeduApplyOptions) (map[string]any, error) {
	if options.MinConfidence < 0 || options.MinConfidence > 1 || math.IsNaN(options.MinConfidence) || math.IsInf(options.MinConfidence, 0) {
		return nil, platform.ErrInvalid
	}
	if options.DryRun {
		generated, dateOK := aware(pack.Generated)
		if pack.Pack != SocialPack || !releasePattern.MatchString(pack.Release) || pack.Release != pack.Snapshot || !dateOK || (pack.Mode != "full" && pack.Mode != "partial") || (pack.Complete && pack.Mode != "full") {
			return nil, errors.New("invalid promoted product identity")
		}
		if pack.Complete && pack.Mode == "full" && !options.AllowSnapshotRollback {
			var previous string
			var previousGenerated time.Time
			err := r.DB.QueryRow(ctx, `SELECT snapshot_id,snapshot_generated_at FROM events_roedueventsyncstate WHERE pack_id=$1 AND city=$2`, pack.Pack, city).Scan(&previous, &previousGenerated)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
			if err == nil && (generated.Before(previousGenerated) || generated.Equal(previousGenerated) && previous != pack.Snapshot) {
				return nil, errors.New("RO-EDU snapshot rollback requires explicit approval")
			}
		}
		venues, events, tombstones, held := 0, 0, 0, 0
		for _, item := range pack.Items {
			if !ValidatePackItem(item) {
				return nil, errors.New("invalid RO-EDU item reached storage")
			}
			switch item["kind"] {
			case "venue":
				venues++
			case "event_tombstone":
				tombstones++
			case "event":
				events++
				if numberValue(item, "confidence") < options.MinConfidence {
					held++
				}
			}
		}
		return map[string]any{"venues": venues, "events": events, "tombstones": tombstones, "held": held, "complete": pack.Complete, "dry_run": true}, nil
	}

	venues, events, tombstones := 0, 0, 0
	err := platform.Transaction(ctx, r.DB, func(tx pgx.Tx) error {
		generated, dateOK := aware(pack.Generated)
		if pack.Pack != SocialPack || !releasePattern.MatchString(pack.Release) || pack.Release != pack.Snapshot || !dateOK || (pack.Mode != "full" && pack.Mode != "partial") || (pack.Complete && pack.Mode != "full") {
			return errors.New("invalid promoted product identity")
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "roedu:"+SocialPack+":"+city); err != nil {
			return err
		}
		if pack.Complete && pack.Mode == "full" {
			var previous string
			var previousGenerated time.Time
			err := tx.QueryRow(ctx, `SELECT snapshot_id,snapshot_generated_at FROM events_roedueventsyncstate WHERE pack_id=$1 AND city=$2 FOR UPDATE`, pack.Pack, city).Scan(&previous, &previousGenerated)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err == nil && !options.AllowSnapshotRollback && (generated.Before(previousGenerated) || generated.Equal(previousGenerated) && previous != pack.Snapshot) {
				return errors.New("RO-EDU snapshot rollback requires explicit approval")
			}
		}
		venueIDs := map[string]int64{}
		classifier, err := loadClassifier(ctx, tx)
		if err != nil {
			return err
		}
		for _, item := range pack.Items {
			if !ValidatePackItem(item) {
				return errors.New("invalid RO-EDU item reached storage")
			}
			if item["kind"] != "venue" {
				continue
			}
			external := stringValue(item, "id")
			loc := item["location"].(map[string]any)
			address := item["address"].(map[string]any)
			tagMap := RoeduVenueTags(item)
			tagMap["roedu_app_pack"] = pack.Pack
			tagMap["roedu_release_id"] = pack.Release
			tagMap["roedu_snapshot_id"] = pack.Snapshot
			tags, _ := json.Marshal(tagMap)
			var id int64
			duplicate, err := findRoeduDuplicate(ctx, tx, item)
			if err != nil {
				return err
			}
			if duplicate > 0 {
				metadata := map[string]any{}
				for key, value := range tagMap {
					if strings.HasPrefix(key, "roedu") {
						metadata[key] = value
					}
				}
				metadataJSON, _ := json.Marshal(metadata)
				entry, _ := json.Marshal([]map[string]string{{"source": "roedu", "external_id": external}})
				_, err = tx.Exec(ctx, `UPDATE places_place SET raw_tags=raw_tags||$2::jsonb||jsonb_build_object('merged_sources',CASE WHEN COALESCE(raw_tags->'merged_sources','[]'::jsonb)@>$3::jsonb THEN COALESCE(raw_tags->'merged_sources','[]'::jsonb) ELSE COALESCE(raw_tags->'merged_sources','[]'::jsonb)||$3::jsonb END),last_seen_at=now(),website=CASE WHEN website='' THEN $4 ELSE website END,attribution=CASE WHEN attribution='' THEN $5 ELSE attribution END,license_name=CASE WHEN license_name='' THEN $6 ELSE license_name END WHERE id=$1`, duplicate, metadataJSON, entry, stringValue(item, "website"), stringValue(item, "attribution"), stringValue(item, "license"))
				if err != nil {
					return err
				}
				if err = mapRoeduVenue(ctx, tx, duplicate, tagMap); err != nil {
					return err
				}
				venueIDs[external] = duplicate
				venues++
				continue
			}
			err = tx.QueryRow(ctx, `SELECT id FROM places_place WHERE source='roedu' AND external_id=$1 FOR UPDATE`, external).Scan(&id)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE places_place SET name=$2,location=ST_SetSRID(ST_MakePoint($3,$4),4326),raw_tags=raw_tags||$5::jsonb,address_street=$6,address_city=$7,address_country=$8,website=$9,last_seen_at=now(),attribution=$10,license_name=$11,provenance_url='' WHERE id=$1`, id, stringValue(item, "title"), numberValue(loc, "lon"), numberValue(loc, "lat"), tags, stringValue(address, "street"), stringValue(address, "city"), stringValue(address, "country"), stringValue(item, "website"), stringValue(item, "attribution"), stringValue(item, "license"))
			} else {
				err = tx.QueryRow(ctx, `INSERT INTO places_place(name,location,address_street,address_housenumber,address_city,address_postcode,address_country,opening_hours_raw,opening_hours,source,osm_type,osm_id,external_id,raw_tags,first_seen_at,last_seen_at,phone,website,attribution,license_name,provenance_url) VALUES($1,ST_SetSRID(ST_MakePoint($2,$3),4326),$4,'',$5,'',$6,'','{}','roedu','',NULL,$7,$8,now(),now(),'',$9,$10,$11,'') RETURNING id`, stringValue(item, "title"), numberValue(loc, "lon"), numberValue(loc, "lat"), stringValue(address, "street"), stringValue(address, "city"), stringValue(address, "country"), external, tags, stringValue(item, "website"), stringValue(item, "attribution"), stringValue(item, "license")).Scan(&id)
			}
			if err != nil {
				return err
			}
			if err = mapRoeduVenue(ctx, tx, id, tagMap); err != nil {
				return err
			}
			venueIDs[external] = id
			venues++
		}
		seen := []string{}
		for _, item := range pack.Items {
			kind := stringValue(item, "kind")
			if kind != "event" && kind != "event_tombstone" {
				continue
			}
			external := stringValue(item, "id")
			seen = append(seen, external)
			if kind == "event_tombstone" {
				tag, err := tx.Exec(ctx, `UPDATE events_event SET lifecycle_status='removed',is_tombstone=true,source_pack_id=$2,source_snapshot_id=$3,source_release_id=$3,source_snapshot_generated_at=$4,source_updated_at=$5,updated_at=now() WHERE source='roedu' AND external_id=$1 AND (source_updated_at IS NULL OR source_updated_at<=$5)`, external, pack.Pack, pack.Snapshot, dateString(pack.Generated), dateValue(item, "updated_at"))
				if err != nil {
					return err
				}
				tombstones += int(tag.RowsAffected())
				continue
			}
			venue := stringValue(item, "venue_id")
			place, ok := venueIDs[venue]
			if !ok {
				return errors.New("event references unavailable local venue")
			}
			start, ok := aware(item["starts_at"])
			if !ok {
				return errors.New("event date invalid")
			}
			var end any
			if item["ends_at"] != "" {
				end = dateValue(item, "ends_at")
			}
			category := stringValue(item, "category")
			typeID := classifier.classify(category, stringValue(item, "title"))
			facets := item["facets"].(map[string]any)
			var id int64
			err := tx.QueryRow(ctx, `SELECT id FROM events_event WHERE source='roedu' AND external_id=$1 FOR UPDATE`, external).Scan(&id)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			args := []any{external, place, typeID, stringValue(item, "title"), start, end, safeURL(stringValue(item, "ticket_url")), stringValue(item, "attribution"), stringValue(item, "license"), numberValue(item, "confidence") < options.MinConfidence, stringValue(item, "status"), category, stringValue(facets, "city"), numberValue(item, "confidence"), dateValue(item, "first_seen"), dateValue(item, "last_seen"), pack.Pack, pack.Release, dateString(pack.Generated), pack.Snapshot, dateValue(item, "updated_at"), venue, stringValue(item, "availability"), stringValue(item, "currency"), item["is_free"], money(item, "price_max"), money(item, "price_min"), stringValue(item, "recurrence"), stringValue(item, "timezone")}
			if err == nil {
				args = append(args, id)
				_, err = tx.Exec(ctx, `UPDATE events_event SET external_id=$1::text,place_id=$2,activity_type_id=COALESCE($3,activity_type_id),title=$4,description='',starts_at=$5,ends_at=$6,url=$7,attribution=$8,license_name=$9,is_import_held=$10,lifecycle_status=$11,is_tombstone=false,source_category=$12,source_city=$13,source_confidence=$14,source_first_seen_at=$15,source_last_seen_at=$16,source_pack_id=$17,source_release_id=$18,source_snapshot_generated_at=$19,source_snapshot_id=$20,source_updated_at=$21,source_venue_id=$22,source_availability=$23,source_currency=$24,source_is_free=$25,source_price_max=$26,source_price_min=$27,source_recurrence=$28,source_timezone=$29,updated_at=now() WHERE id=$30 AND (source_updated_at IS NULL OR source_updated_at<=$21)`, args...)
			} else {
				_, err = tx.Exec(ctx, `INSERT INTO events_event(external_id,place_id,activity_type_id,title,description,starts_at,ends_at,url,attribution,license_name,provenance_url,is_import_held,lifecycle_status,is_tombstone,source_category,source_city,source_confidence,source_first_seen_at,source_last_seen_at,source_pack_id,source_release_id,source_snapshot_generated_at,source_snapshot_id,source_updated_at,source_venue_id,source_availability,source_currency,source_is_free,source_price_max,source_price_min,source_recurrence,source_timezone,source,created_at,updated_at) VALUES($1,$2,$3,$4,'',$5,$6,$7,$8,$9,'',$10,$11,false,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,'roedu',now(),now())`, args...)
			}
			if err != nil {
				return err
			}
			events++
		}
		if pack.Complete && pack.Mode == "full" {
			if _, err := tx.Exec(ctx, `UPDATE events_event SET lifecycle_status='removed',is_tombstone=true,source_snapshot_id=$4,source_release_id=$4,source_snapshot_generated_at=$5,updated_at=now() WHERE source='roedu' AND source_pack_id=$1 AND lower(source_city)=lower($2) AND NOT(external_id=ANY($3)) AND (source_snapshot_generated_at IS NULL OR source_snapshot_generated_at<=$5)`, pack.Pack, city, seen, pack.Snapshot, dateString(pack.Generated)); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO events_roedueventsyncstate(pack_id,city,snapshot_id,release_id,snapshot_generated_at,completed_at) VALUES($1,$2,$3,$3,$4,now()) ON CONFLICT(pack_id,city) DO UPDATE SET snapshot_id=EXCLUDED.snapshot_id,release_id=EXCLUDED.release_id,snapshot_generated_at=EXCLUDED.snapshot_generated_at,completed_at=now()`, pack.Pack, city, pack.Snapshot, dateString(pack.Generated)); err != nil {
				return err
			}
		}
		return platform.RecordAudit(ctx, tx, platform.Actor{}, "ingestion.roedu_synced", "", map[string]any{"venues": venues, "events": events, "tombstones": tombstones, "complete": pack.Complete, "pack": pack.Pack, "release": pack.Release, "min_confidence": options.MinConfidence, "rollback_allowed": options.AllowSnapshotRollback})
	})
	return map[string]any{"venues": venues, "events": events, "tombstones": tombstones, "complete": pack.Complete}, err
}
func dateString(raw string) time.Time { value, _ := time.Parse(time.RFC3339Nano, raw); return value }

func (r *Runner) IndexNow(ctx context.Context) (map[string]any, error) {
	if !r.Config.IndexNowEnabled || r.Config.IndexNowKey == "" || r.Config.SiteBaseURL == "" {
		return map[string]any{"disabled": true}, nil
	}
	window, cap := r.Config.IndexNowWindowHours, r.Config.IndexNowMaxURLs
	if window == 0 {
		window = 26
	}
	if cap == 0 {
		cap = 1000
	}
	if window < 1 || window > 24*365 || cap < 1 || cap > 1000 {
		return nil, errors.New("invalid IndexNow job bounds")
	}
	base := strings.TrimRight(r.Config.SiteBaseURL, "/")
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("invalid public site URL")
	}
	rows, err := r.DB.Query(ctx, `SELECT p.id,COALESCE((SELECT NULLIF(c.proposed_value,'') FROM places_placecorrection c WHERE c.place_id=p.id AND c.field='name' AND c.status='published' ORDER BY COALESCE(c.published_at,c.created_at) DESC,c.id DESC LIMIT 1),NULLIF(p.name,''),'Unnamed place') FROM places_place p WHERE p.last_seen_at>=$1 AND `+catalog.PublicPlaceSQL+` ORDER BY p.last_seen_at DESC,p.id LIMIT $2`, r.Config.Now().Add(-time.Duration(window)*time.Hour), cap)
	if err != nil {
		return nil, err
	}
	urls := []string{}
	for rows.Next() {
		var id int64
		var name string
		if err = rows.Scan(&id, &name); err != nil {
			rows.Close()
			return nil, err
		}
		urls = append(urls, base+"/places/"+strconv.FormatInt(id, 10)+"/"+slug(name, "place")+"/")
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = r.DB.Query(ctx, `SELECT e.id,e.title FROM events_event e LEFT JOIN places_place p ON p.id=e.place_id WHERE e.updated_at>=$1 AND e.starts_at>=$2 AND NOT e.is_import_held AND NOT e.is_tombstone AND e.lifecycle_status IN ('scheduled','rescheduled','sold_out') AND (p.id IS NULL OR (`+catalog.PublicPlaceSQL+`)) ORDER BY e.updated_at DESC,e.id LIMIT $3`, r.Config.Now().Add(-time.Duration(window)*time.Hour), r.Config.Now(), cap)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var title string
		if err = rows.Scan(&id, &title); err != nil {
			rows.Close()
			return nil, err
		}
		urls = append(urls, base+"/events/"+strconv.FormatInt(id, 10)+"/"+slug(title, "event")+"/")
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(urls) == 0 {
		return map[string]any{"submitted": 0}, nil
	}
	payload, _ := json.Marshal(map[string]any{"host": parsed.Host, "key": r.Config.IndexNowKey, "keyLocation": base + "/indexnow.txt", "urlList": urls})
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(fetchCtx, "POST", "https://api.indexnow.org/indexnow", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	client := r.Config.IndexNowHTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client = &copyClient
	response, err := client.Do(request)
	if err != nil {
		return map[string]any{"submitted": 0, "failed": len(urls)}, nil
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return map[string]any{"submitted": 0, "failed": len(urls)}, nil
	}
	return map[string]any{"submitted": len(urls)}, nil
}
