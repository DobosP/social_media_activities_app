package jobs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"golang.org/x/text/unicode/norm"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

func slug(value, fallback string) string {
	value = strings.ToLower(norm.NFKD.String(value))
	var b strings.Builder
	for _, r := range value {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if r < 128 && ((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_') {
			b.WriteRune(r)
		} else if unicode.IsSpace(r) || r == '-' {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(regexp.MustCompile(`-+`).ReplaceAllString(b.String(), "-"), "-_")
	if out == "" {
		out = fallback
	}
	return clampText(out, 80)
}
func queryMaps(ctx context.Context, r *Runner, sql string, args ...any) ([]map[string]any, error) {
	rows, err := r.DB.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var value map[string]any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&value) != nil {
			return nil, fmt.Errorf("invalid public snapshot projection")
		}
		out = append(out, value)
	}
	return out, rows.Err()
}
func atomicJSON(dir, name string, payload any) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	raw = append(raw, '\n')
	temp, err := os.CreateTemp(dir, "."+name+"-*")
	if err != nil {
		return "", err
	}
	path := temp.Name()
	defer os.Remove(path)
	if err = temp.Chmod(0644); err == nil {
		_, err = temp.Write(raw)
	}
	if err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	if err = os.Rename(path, filepath.Join(dir, name)); err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (r *Runner) ExportAgentSnapshot(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
	dir := r.Config.AgentSnapshotDir
	if dir == "" {
		return map[string]bool{"disabled": true}, nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	generated := r.Config.Now().UTC().Format("2006-01-02T15:04:05.000000Z")
	nameSQL := `COALESCE(NULLIF((SELECT proposed_value FROM places_placecorrection WHERE place_id=p.id AND field='name' AND status='published' ORDER BY COALESCE(published_at,created_at) DESC,id DESC LIMIT 1),''),p.name)`
	addressSQL := `COALESCE(NULLIF((SELECT proposed_value FROM places_placecorrection WHERE place_id=p.id AND field='address' AND status='published' ORDER BY COALESCE(published_at,created_at) DESC,id DESC LIMIT 1),''),concat_ws(', ',NULLIF(btrim(p.address_street||' '||p.address_housenumber),''),NULLIF(p.address_city,'')))`
	hoursSQL := `COALESCE(NULLIF((SELECT proposed_value FROM places_placecorrection WHERE place_id=p.id AND field='hours' AND status='published' ORDER BY COALESCE(published_at,created_at) DESC,id DESC LIMIT 1),''),p.opening_hours_raw)`
	places, err := queryMaps(ctx, r, `SELECT jsonb_build_object('id',p.id,'name',`+nameSQL+`,'lat',ST_Y(p.location::geometry),'lon',ST_X(p.location::geometry),'address',`+addressSQL+`,'city',p.address_city,'postcode',p.address_postcode,'country',p.address_country,'website',p.website,'phone',p.phone,'opening_hours_text',`+hoursSQL+`,'activity_types',COALESCE((SELECT jsonb_agg(t.slug ORDER BY pa.id) FROM places_placeactivity pa JOIN taxonomy_activitytype t ON t.id=pa.activity_id WHERE pa.place_id=p.id AND NOT pa.is_disputed),'[]'::jsonb),'attribution',p.attribution,'license_name',p.license_name,'provenance_url',p.provenance_url) FROM places_place p WHERE `+catalog.PublicPlaceSQL+` ORDER BY p.id LIMIT 50001`)
	if err != nil {
		return nil, err
	}
	events, err := queryMaps(ctx, r, `SELECT jsonb_build_object('id',e.id,'title',e.title,'description',e.description,'starts_at',to_char(e.starts_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),'ends_at',CASE WHEN e.ends_at IS NULL THEN NULL ELSE to_char(e.ends_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') END,'url',e.url,'source',e.source,'attribution',e.attribution,'license_name',e.license_name,'provenance_url',e.provenance_url,'attribution_credit',CASE WHEN e.license_name='' AND e.attribution='' AND e.provenance_url='' THEN NULL ELSE jsonb_build_object('license_name',e.license_name,'attribution',e.attribution,'provenance_url',e.provenance_url) END,'activity',t.slug,'source_category',e.source_category,'lifecycle_status',e.lifecycle_status,'source_confidence',e.source_confidence,'source_recurrence',e.source_recurrence,'source_timezone',e.source_timezone,'source_price_min',e.source_price_min::text,'source_price_max',e.source_price_max::text,'source_currency',e.source_currency,'source_is_free',e.source_is_free,'source_availability',e.source_availability,'place_id',e.place_id,'place_summary',CASE WHEN p.id IS NULL THEN NULL ELSE jsonb_build_object('id',p.id,'name',`+nameSQL+`,'city',p.address_city,'lat',ST_Y(p.location::geometry),'lon',ST_X(p.location::geometry)) END) FROM events_event e LEFT JOIN places_place p ON p.id=e.place_id LEFT JOIN taxonomy_activitytype t ON t.id=e.activity_type_id WHERE e.starts_at>=$1 AND NOT e.is_import_held AND NOT e.is_tombstone AND e.lifecycle_status IN ('scheduled','rescheduled','sold_out') AND (p.id IS NULL OR (`+catalog.PublicPlaceSQL+`)) ORDER BY e.starts_at,e.id LIMIT 10001`, r.Config.Now())
	if err != nil {
		return nil, err
	}
	activities, err := queryMaps(ctx, r, `SELECT jsonb_build_object('id',a.id,'title',a.title,'cohort',a.cohort,'starts_at',to_char(a.starts_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),'status',a.status,'activity_type',t.slug,'place_id',a.place_id) FROM social_activity a JOIN accounts_user u ON u.id=a.owner_id LEFT JOIN taxonomy_activitytype t ON t.id=a.activity_type_id WHERE a.cohort='adult' AND a.is_publicly_listed AND a.status='open' AND NOT a.is_hidden AND a.starts_at>=$1 AND u.is_active ORDER BY a.starts_at,a.id LIMIT 2001`, r.Config.Now())
	if err != nil {
		return nil, err
	}
	truncated := len(places) > 50000 || len(events) > 10000 || len(activities) > 2000
	if len(places) > 50000 {
		places = places[:50000]
	}
	if len(events) > 10000 {
		events = events[:10000]
	}
	if len(activities) > 2000 {
		activities = activities[:2000]
	}
	for _, place := range places {
		place["path"] = fmt.Sprintf("/places/%s/%s/", fmt.Sprint(place["id"]), slug(fmt.Sprint(place["name"]), "place"))
	}
	for _, event := range events {
		event["path"] = fmt.Sprintf("/events/%s/%s/", fmt.Sprint(event["id"]), slug(fmt.Sprint(event["title"]), "event"))
	}
	categories, err := queryMaps(ctx, r, `SELECT jsonb_build_object('slug',c.slug,'name',c.name,'parent',parent.slug) FROM taxonomy_activitycategory c LEFT JOIN taxonomy_activitycategory parent ON parent.id=c.parent_id ORDER BY c.slug`)
	if err != nil {
		return nil, err
	}
	types, err := queryMaps(ctx, r, `SELECT jsonb_build_object('slug',t.slug,'name',t.name,'category',c.slug,'parent',parent.slug,'family_friendly',t.family_friendly,'wellness',t.wellness) FROM taxonomy_activitytype t JOIN taxonomy_activitycategory c ON c.id=t.category_id LEFT JOIN taxonomy_activitytype parent ON parent.id=t.parent_id WHERE t.is_active ORDER BY t.slug`)
	if err != nil {
		return nil, err
	}
	datasets := map[string]any{}
	for _, data := range []struct {
		Name string
		Rows []map[string]any
	}{{"places", places}, {"events", events}, {"activities", activities}} {
		file := data.Name + ".json"
		digest, err := atomicJSON(dir, file, map[string]any{"schema_version": 2, "generated_at": generated, "count": len(data.Rows), "records": data.Rows})
		if err != nil {
			return nil, err
		}
		datasets[data.Name] = map[string]any{"file": file, "count": len(data.Rows), "sha256": digest}
	}
	taxDigest, err := atomicJSON(dir, "taxonomy.json", map[string]any{"schema_version": 2, "generated_at": generated, "categories": categories, "activity_types": types})
	if err != nil {
		return nil, err
	}
	datasets["taxonomy"] = map[string]any{"file": "taxonomy.json", "count": len(categories) + len(types), "sha256": taxDigest}
	licenses := []map[string]string{}
	seen := map[string]bool{}
	for _, records := range [][]map[string]any{places, events} {
		for _, record := range records {
			name := strings.TrimSpace(fmt.Sprint(record["license_name"]))
			credit := strings.TrimSpace(fmt.Sprint(record["attribution"]))
			if name == "" || seen[name+"\x00"+credit] {
				continue
			}
			seen[name+"\x00"+credit] = true
			licenses = append(licenses, map[string]string{"license_name": name, "attribution": credit})
		}
	}
	_, err = atomicJSON(dir, "manifest.json", map[string]any{"schema_version": 2, "generated_at": generated, "site": r.Config.SiteBaseURL, "datasets": datasets, "licenses": licenses, "truncated": truncated})
	return map[string]any{"places": len(places), "events": len(events), "activities": len(activities), "categories": len(categories), "activity_types": len(types), "truncated": truncated}, err
}
