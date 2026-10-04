// Package export is the native producer of the reviewed agent snapshot contract.
package export

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/text/unicode/norm"
)

const SchemaVersion = 2
const EventsCap = 10000
const PlacesCap = 50000
const ActivitiesCap = 2000

type Summary struct {
	Events, Places, Activities, Categories, ActivityTypes int
	Truncated                                             bool
}
type Service struct {
	DB   *pgxpool.Pool
	Site string
	Now  func() time.Time
	mu   sync.Mutex
}

func New(db *pgxpool.Pool, site string) *Service {
	return &Service{DB: db, Site: strings.TrimRight(site, "/"), Now: time.Now}
}

var separators = regexp.MustCompile(`[\s-]+`)

func Slug(text, fallback string) string {
	var b strings.Builder
	for _, r := range norm.NFKD.String(text) {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsSpace(r) || r == '_' || r == '-') {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	slug := strings.Trim(separators.ReplaceAllString(b.String(), "-"), "-_")
	if slug == "" {
		return fallback
	}
	return slug
}
func records(ctx context.Context, q platform.Querier, sql string, args ...any) ([]map[string]any, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var record map[string]any
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}
func isoZ(value any) any {
	if value == nil {
		return nil
	}
	text, ok := value.(string)
	if !ok {
		return value
	}
	date, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return value
	}
	if date.Nanosecond() == 0 {
		return date.UTC().Format("2006-01-02T15:04:05Z")
	}
	return date.UTC().Format("2006-01-02T15:04:05.000000Z")
}
func path(kind string, r map[string]any) {
	r["path"] = fmt.Sprintf("/%s/%d/%s/", kind, int64(r["id"].(float64)), Slug(r["name"].(string), "place"))
}
func writeJSON(directory, name string, payload any) (string, error) {
	tmp, err := os.CreateTemp(directory, "."+name+"-*.tmp")
	if err != nil {
		return "", err
	}
	temp := tmp.Name()
	defer os.Remove(temp)
	digest := sha256.New()
	enc := json.NewEncoder(io.MultiWriter(tmp, digest))
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(temp, 0644); err != nil {
		return "", err
	}
	if err := os.Rename(temp, filepath.Join(directory, name)); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// Snapshot queries one repeatable-read generation and writes manifest last. Its
// digest pins each exact published file, so readers reject interrupted generations.
func (s *Service) Snapshot(ctx context.Context, directory string) (Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out Summary
	if directory == "" || s.DB == nil {
		return out, platform.ErrInvalid
	}
	if err := os.MkdirAll(directory, 0755); err != nil {
		return out, err
	}
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout='30s'`); err != nil {
		return out, err
	}
	eventWhere := catalog.PolicyFromContext(ctx).EventSQL() + ` AND e.starts_at>=now() AND e.lifecycle_status IN('scheduled','rescheduled','sold_out')`
	eventJoin := ` FROM events_event e LEFT JOIN places_place p ON p.id=e.place_id LEFT JOIN taxonomy_activitytype t ON t.id=e.activity_type_id `
	eventProjection := catalog.PublicEventProjectionSQL() + ` || jsonb_build_object('place_id',e.place_id,'place_summary',CASE WHEN p.id IS NULL THEN NULL ELSE jsonb_build_object('id',p.id,'name',` + catalog.PlaceDisplayNameSQL() + `,'city',p.address_city,'lat',ST_Y(p.location::geometry),'lon',ST_X(p.location::geometry)) END)`
	events, err := records(ctx, tx, `SELECT `+eventProjection+eventJoin+` WHERE `+eventWhere+` ORDER BY e.starts_at,e.id LIMIT $1`, EventsCap+1)
	if err != nil {
		return out, err
	}
	if len(events) > EventsCap {
		out.Truncated = true
		events = events[:EventsCap]
	}
	for _, e := range events {
		e["starts_at"] = isoZ(e["starts_at"])
		e["ends_at"] = isoZ(e["ends_at"])
		e["path"] = fmt.Sprintf("/events/%d/%s/", int64(e["id"].(float64)), Slug(e["title"].(string), "event"))
		delete(e, "place")
		delete(e, "place_name")
	}
	places, err := records(ctx, tx, `SELECT `+catalog.PlaceExportProjectionSQL()+` FROM places_place p WHERE `+catalog.PolicyFromContext(ctx).PlaceSQL()+` ORDER BY p.id LIMIT $1`, PlacesCap+1)
	if err != nil {
		return out, err
	}
	if len(places) > PlacesCap {
		out.Truncated = true
		places = places[:PlacesCap]
	}
	for _, p := range places {
		path("places", p)
	}
	activities, err := records(ctx, tx, `SELECT jsonb_build_object('id',a.id,'title',a.title,'cohort',a.cohort,'starts_at',a.starts_at,'status',a.status,'activity_type',t.slug,'place_id',a.place_id) FROM social_activity a JOIN accounts_user u ON u.id=a.owner_id LEFT JOIN taxonomy_activitytype t ON t.id=a.activity_type_id WHERE a.cohort='adult' AND a.is_publicly_listed AND a.status='open' AND NOT a.is_hidden AND a.starts_at>=now() AND u.is_active ORDER BY a.starts_at,a.id LIMIT $1`, ActivitiesCap+1)
	if err != nil {
		return out, err
	}
	if len(activities) > ActivitiesCap {
		out.Truncated = true
		activities = activities[:ActivitiesCap]
	}
	for _, a := range activities {
		a["starts_at"] = isoZ(a["starts_at"])
	}
	categories, err := records(ctx, tx, `SELECT jsonb_build_object('slug',c.slug,'name',c.name,'parent',p.slug) FROM taxonomy_activitycategory c LEFT JOIN taxonomy_activitycategory p ON p.id=c.parent_id ORDER BY c.slug`)
	if err != nil {
		return out, err
	}
	types, err := records(ctx, tx, `SELECT jsonb_build_object('slug',t.slug,'name',t.name,'category',c.slug,'parent',p.slug,'family_friendly',t.family_friendly,'wellness',t.wellness) FROM taxonomy_activitytype t LEFT JOIN taxonomy_activitycategory c ON c.id=t.category_id LEFT JOIN taxonomy_activitytype p ON p.id=t.parent_id WHERE t.is_active ORDER BY t.slug`)
	if err != nil {
		return out, err
	}
	if err := tx.Commit(ctx); err != nil {
		return out, err
	}
	now := s.Now().UTC()
	generated := now.Format("2006-01-02T15:04:05.000000Z")
	datasets := map[string]any{}
	for _, data := range []struct {
		name    string
		records []map[string]any
	}{{"events", events}, {"places", places}, {"activities", activities}} {
		payload := map[string]any{"schema_version": SchemaVersion, "generated_at": generated, "count": len(data.records), "records": data.records}
		hash, err := writeJSON(directory, data.name+".json", payload)
		if err != nil {
			return out, err
		}
		datasets[data.name] = map[string]any{"file": data.name + ".json", "count": len(data.records), "sha256": hash}
	}
	hash, err := writeJSON(directory, "taxonomy.json", map[string]any{"schema_version": SchemaVersion, "generated_at": generated, "categories": categories, "activity_types": types})
	if err != nil {
		return out, err
	}
	datasets["taxonomy"] = map[string]any{"file": "taxonomy.json", "count": len(categories) + len(types), "sha256": hash}
	licenses := []map[string]string{}
	seen := map[string]bool{}
	for _, data := range [][]map[string]any{places, events} {
		for _, record := range data {
			name, _ := record["license_name"].(string)
			attribution, _ := record["attribution"].(string)
			name = strings.TrimSpace(name)
			attribution = strings.TrimSpace(attribution)
			key := name + "\x00" + attribution
			if name != "" && !seen[key] {
				seen[key] = true
				licenses = append(licenses, map[string]string{"license_name": name, "attribution": attribution})
			}
		}
	}
	_, err = writeJSON(directory, "manifest.json", map[string]any{"schema_version": SchemaVersion, "generated_at": generated, "site": s.Site, "datasets": datasets, "licenses": licenses, "truncated": out.Truncated})
	out.Events = len(events)
	out.Places = len(places)
	out.Activities = len(activities)
	out.Categories = len(categories)
	out.ActivityTypes = len(types)
	return out, err
}
