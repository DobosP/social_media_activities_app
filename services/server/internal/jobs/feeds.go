package jobs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type RawEvent struct {
	Title, Description, URL, ExternalID, Attribution, License, Provenance string
	Starts                                                                time.Time
	Ends                                                                  *time.Time
}

func icalDate(raw string) (time.Time, error) {
	for _, layout := range []string{"20060102T150405Z", "20060102T150405", "20060102"} {
		value, err := time.Parse(layout, strings.TrimSpace(raw))
		if err == nil {
			return value.UTC(), nil
		}
	}
	return time.Time{}, errors.New("invalid calendar date")
}
func positive(raw string, fallback int) int {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return fallback
	}
	return value
}
func addMonths(start time.Time, n int) time.Time {
	base := time.Date(start.Year(), start.Month()+time.Month(n), 1, start.Hour(), start.Minute(), start.Second(), start.Nanosecond(), start.Location())
	last := time.Date(base.Year(), base.Month()+1, 0, 0, 0, 0, 0, start.Location()).Day()
	return time.Date(base.Year(), base.Month(), min(start.Day(), last), start.Hour(), start.Minute(), start.Second(), start.Nanosecond(), start.Location())
}
func ExpandRule(start time.Time, rule string, now time.Time) []time.Time {
	parts := map[string]string{}
	for _, token := range strings.Split(rule, ";") {
		kv := strings.SplitN(token, "=", 2)
		if len(kv) == 2 {
			parts[strings.ToUpper(strings.TrimSpace(kv[0]))] = strings.TrimSpace(kv[1])
		}
	}
	freq := strings.ToUpper(parts["FREQ"])
	if freq != "DAILY" && freq != "WEEKLY" && freq != "MONTHLY" || freq == "MONTHLY" && parts["BYDAY"] != "" {
		return []time.Time{start}
	}
	interval := min(positive(parts["INTERVAL"], 1), 100000)
	count := positive(parts["COUNT"], 0)
	until, _ := icalDate(parts["UNTIL"])
	floor, horizon := now.Add(-24*time.Hour), now.Add(90*24*time.Hour)
	out := []time.Time{}
	seen := 0
	record := func(value time.Time) bool {
		seen++
		if count > 0 && seen > count || !until.IsZero() && value.After(until) || value.After(horizon) {
			return false
		}
		if !value.Before(floor) {
			out = append(out, value)
		}
		return len(out) < 120
	}
	if freq == "WEEKLY" && parts["BYDAY"] != "" {
		weekdays := map[string]int{"MO": 0, "TU": 1, "WE": 2, "TH": 3, "FR": 4, "SA": 5, "SU": 6}
		set := map[int]bool{}
		for _, day := range strings.Split(parts["BYDAY"], ",") {
			if value, ok := weekdays[strings.ToUpper(strings.TrimSpace(day))]; ok {
				set[value] = true
			}
		}
		if len(set) == 0 {
			set[(int(start.Weekday())+6)%7] = true
		}
		days := []int{}
		for day := range set {
			days = append(days, day)
		}
		sort.Ints(days)
		week0 := start.AddDate(0, 0, -(int(start.Weekday())+6)%7)
		for week := 0; week < 4000; week++ {
			base := week0.AddDate(0, 0, week*interval*7)
			if base.After(horizon) {
				break
			}
			for _, day := range days {
				value := base.AddDate(0, 0, day)
				if value.Before(start) {
					continue
				}
				if !record(value) {
					return out
				}
			}
		}
		return out
	}
	if freq == "MONTHLY" {
		for i := 0; i < 4000; i++ {
			if !record(addMonths(start, i*interval)) {
				break
			}
		}
		return out
	}
	step := 24 * time.Hour * time.Duration(interval)
	if freq == "WEEKLY" {
		step *= 7
	}
	value := start
	if value.Before(floor) {
		skipped := int(floor.Sub(value) / step)
		if skipped > 0 {
			seen += skipped
			value = value.Add(step * time.Duration(skipped))
			if count > 0 && seen >= count {
				return out
			}
		}
	}
	for i := 0; i < 4000; i++ {
		if !record(value) {
			break
		}
		value = value.Add(step)
	}
	return out
}

func ParseICS(raw string, now time.Time) ([]RawEvent, error) {
	if len(raw) > 5<<20 {
		return nil, errors.New("calendar body exceeds limit")
	}
	unfolded := []string{}
	scanner := bufio.NewScanner(strings.NewReader(strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n")))
	scanner.Buffer(make([]byte, 4096), 5<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') && len(unfolded) > 0 {
			unfolded[len(unfolded)-1] += line[1:]
		} else {
			unfolded = append(unfolded, line)
		}
	}
	if scanner.Err() != nil {
		return nil, errors.New("calendar line exceeds limit")
	}
	out := []RawEvent{}
	var current *RawEvent
	rule := ""
	for _, line := range unfolded {
		if line == "BEGIN:VEVENT" {
			current = &RawEvent{Title: "(untitled)"}
			rule = ""
			continue
		}
		if line == "END:VEVENT" {
			if current != nil && !current.Starts.IsZero() {
				dates := []time.Time{current.Starts}
				if rule != "" {
					dates = ExpandRule(current.Starts, rule, now)
				}
				for _, date := range dates {
					event := *current
					if current.Ends != nil {
						end := date.Add(current.Ends.Sub(current.Starts))
						event.Ends = &end
					}
					event.Starts = date
					if rule != "" && event.ExternalID != "" {
						event.ExternalID = clampText(event.ExternalID, 150) + ":" + date.Format("20060102")
					}
					out = append(out, event)
					if len(out) >= 2000 {
						return out, nil
					}
				}
			}
			current = nil
			continue
		}
		if current == nil {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToUpper(strings.SplitN(parts[0], ";", 2)[0])
		value := strings.TrimSpace(parts[1])
		switch key {
		case "SUMMARY":
			current.Title = clampText(value, 255)
		case "DESCRIPTION":
			current.Description = value
		case "URL":
			current.URL = clampText(value, 500)
		case "UID":
			current.ExternalID = clampText(value, 200)
		case "RRULE":
			rule = value
		case "DTSTART":
			current.Starts, _ = icalDate(value)
		case "DTEND":
			end, err := icalDate(value)
			if err == nil {
				current.Ends = &end
			}
		}
	}
	return out, nil
}

func safeURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" {
		return ""
	}
	return parsed.String()
}
func publicIP(ip net.IP) bool {
	if ip == nil || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || !ip.IsGlobalUnicast() {
		return false
	}
	// Go's IsGlobalUnicast deliberately includes documentation and reserved
	// ranges. External ingestion must reject those as Python ipaddress.is_global
	// does, including shared carrier NAT and IPv4-mapped IPv6 addresses.
	for _, block := range nonPublicNetworks {
		if block.Contains(ip) {
			return false
		}
	}
	return true
}

var nonPublicNetworks = func() []*net.IPNet {
	blocks := []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "::/96", "64:ff9b:1::/48", "100::/64", "2001::/23", "2001:db8::/32", "2002::/16", "3fff::/20", "fc00::/7", "fe80::/10"}
	out := make([]*net.IPNet, 0, len(blocks))
	for _, block := range blocks {
		_, network, _ := net.ParseCIDR(block)
		out = append(out, network)
	}
	return out
}()

// FetchFeed pins checked DNS addresses for the connection and rejects redirects,
// preventing a registered calendar URL from reaching internal hosts on rebinding.
func FetchFeed(ctx context.Context, raw string) ([]byte, error) {
	parsed, err := url.Parse(raw)
	if err != nil || safeURL(raw) == "" {
		return nil, errors.New("unsafe feed URL")
	}
	host := parsed.Hostname()
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("feed DNS unavailable")
	}
	for _, address := range addresses {
		if !publicIP(address.IP) {
			return nil, errors.New("unsafe feed address")
		}
	}
	port := parsed.Port()
	if port == "" {
		port = "443"
		if parsed.Scheme == "http" {
			port = "80"
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(addresses[0].IP.String(), port))
	}
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer transport.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return nil, errors.New("invalid feed URL")
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, errors.New("feed unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, errors.New("feed unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (5<<20)+1))
	if err != nil || len(body) > 5<<20 {
		return nil, errors.New("feed response exceeds limit")
	}
	return body, nil
}

func (r *Runner) SyncFeeds(ctx context.Context) (map[string]int, error) {
	classifier, err := loadClassifier(ctx, r.DB)
	if err != nil {
		return nil, err
	}
	rows, err := r.DB.Query(ctx, `SELECT id,url,place_id,activity_type_id FROM events_eventfeed WHERE is_active ORDER BY id`)
	if err != nil {
		return nil, err
	}
	type feed struct {
		id          int64
		url         string
		place, kind *int64
	}
	feeds := []feed{}
	for rows.Next() {
		var f feed
		if err = rows.Scan(&f.id, &f.url, &f.place, &f.kind); err != nil {
			rows.Close()
			return nil, err
		}
		feeds = append(feeds, f)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	count, failed := 0, 0
	fetch := r.Config.FetchFeed
	if fetch == nil {
		fetch = FetchFeed
	}
	for _, feed := range feeds {
		body, err := fetch(ctx, feed.url)
		events := []RawEvent{}
		if err == nil {
			events, err = ParseICS(string(body), r.Config.Now())
		}
		processed := 0
		if err == nil {
			err = platform.Transaction(ctx, r.DB, func(tx pgx.Tx) error {
				for _, event := range events {
					effective := event.Starts
					if event.Ends != nil {
						effective = *event.Ends
					}
					if effective.Before(r.Config.Now()) {
						continue
					}
					if event.ExternalID != "" {
						event.ExternalID = fmt.Sprintf("feed%d:%s", feed.id, event.ExternalID)
					}
					kind := feed.kind
					if kind == nil {
						kind = classifier.classify("", event.Title+" "+event.Description)
					}
					if err := upsertICS(ctx, tx, event, feed.place, kind); err != nil {
						return err
					}
					processed++
				}
				return nil
			})
		}
		status := fmt.Sprintf("ok: %d event(s)", processed)
		if err != nil {
			failed++
			status = "error: feed unavailable or invalid"
		} else {
			count += processed
		}
		if _, err = r.DB.Exec(ctx, `UPDATE events_eventfeed SET last_synced_at=$2,last_status=$3 WHERE id=$1`, feed.id, r.Config.Now(), status); err != nil {
			return nil, err
		}
	}
	return map[string]int{"events": count, "failed_feeds": failed}, nil
}
func upsertICS(ctx context.Context, tx pgx.Tx, event RawEvent, place, kind *int64) error {
	identity := "ical:" + event.ExternalID
	if event.ExternalID == "" {
		var placeID int64
		if place != nil {
			placeID = *place
		}
		identity = fmt.Sprintf("ical:%d:%s:%s", placeID, event.Title, event.Starts.Format(time.RFC3339Nano))
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, identity); err != nil {
		return err
	}
	var id int64
	var err error
	if event.ExternalID != "" {
		err = tx.QueryRow(ctx, `SELECT id FROM events_event WHERE source='ical' AND external_id=$1 FOR UPDATE`, event.ExternalID).Scan(&id)
	} else {
		err = tx.QueryRow(ctx, `SELECT id FROM events_event WHERE source='ical' AND place_id IS NOT DISTINCT FROM $1 AND title=$2 AND starts_at=$3 FOR UPDATE`, place, event.Title, event.Starts).Scan(&id)
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE events_event SET place_id=$2,activity_type_id=COALESCE($3,activity_type_id),title=$4,description=$5,starts_at=$6,ends_at=$7,url=$8,attribution=$9,license_name=$10,provenance_url=$11,is_import_held=false,updated_at=now() WHERE id=$1`, id, place, kind, event.Title, event.Description, event.Starts, event.Ends, safeURL(event.URL), event.Attribution, event.License, safeURL(event.Provenance))
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO events_event(title,description,starts_at,ends_at,url,source,external_id,created_at,updated_at,activity_type_id,place_id,attribution,license_name,provenance_url,is_import_held,is_tombstone,lifecycle_status,source_category,source_city,source_confidence,source_first_seen_at,source_last_seen_at,source_pack_id,source_release_id,source_snapshot_generated_at,source_snapshot_id,source_updated_at,source_venue_id,source_availability,source_currency,source_is_free,source_price_max,source_price_min,source_recurrence,source_timezone) VALUES($1,$2,$3,$4,$5,'ical',$6,now(),now(),$7,$8,$9,$10,$11,false,false,'scheduled','','',NULL,NULL,NULL,'','',NULL,'',NULL,'','','',NULL,NULL,NULL,'','')`, event.Title, event.Description, event.Starts, event.Ends, safeURL(event.URL), clampText(event.ExternalID, 200), kind, place, event.Attribution, event.License, safeURL(event.Provenance))
	return err
}
