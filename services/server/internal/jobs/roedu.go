package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const SocialPack = "roedu:social_media_activities_app:events_places:v1"
const PolicyHash = "07f27d3c9a5e5898ba7cfac686c645713114dd9c13d72ecc054570d368daf58d"

var ErrProductRefused = errors.New("RO-EDU producer refused all items")

var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var releasePattern = regexp.MustCompile(`^sha256-[0-9a-f]{64}$`)
var categoryPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var dnsPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

type PackRead struct {
	Items                                    []map[string]any
	Pack, Snapshot, Release, Generated, Mode string
	Complete                                 bool
}
type RoeduClient struct {
	BaseURL, APIKey string
	HTTP            *http.Client
}

func text(value any, maxLen int, empty bool) bool {
	raw, ok := value.(string)
	if !ok || raw != strings.TrimSpace(raw) || utf8.RuneCountInString(raw) > maxLen || !empty && raw == "" {
		return false
	}
	for _, r := range raw {
		if r < 32 {
			return false
		}
	}
	return true
}
func optionalText(value any, maximum int) bool { return value == nil || text(value, maximum, false) }
func number(value any) (float64, bool) {
	var n float64
	var err error
	switch v := value.(type) {
	case json.Number:
		n, err = v.Float64()
	case float64:
		n = v
	default:
		return 0, false
	}
	return n, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
}
func equalsNumber(v any, wanted float64) bool { n, ok := number(v); return ok && n == wanted }
func exactKeys(value map[string]any, keys string) bool {
	wanted := strings.Fields(keys)
	if len(value) != len(wanted) {
		return false
	}
	for _, key := range wanted {
		if _, ok := value[key]; !ok {
			return false
		}
	}
	return true
}
func aware(value any) (time.Time, bool) {
	raw, ok := value.(string)
	if !ok || !text(raw, 80, false) || len(raw) < 20 || raw[10] != 'T' {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	return parsed, err == nil
}
func stable(value any) bool {
	if !text(value, 128, false) {
		return false
	}
	for _, r := range value.(string) {
		if unicode.IsSpace(r) || r < 32 {
			return false
		}
	}
	return true
}
func publicURL(value any) bool {
	if !text(value, 500, false) {
		return false
	}
	raw := value.(string)
	for _, r := range raw {
		if unicode.IsSpace(r) {
			return false
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Fragment != "" || parsed.Hostname() == "" || strings.HasSuffix(parsed.Hostname(), ".") {
		return false
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 0 || value > 65535 {
			return false
		}
	}
	if parsed.RawQuery != "" && !regexp.MustCompile(`^__query_sha256__=[0-9a-f]{64}$`).MatchString(parsed.RawQuery) {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	for _, suffix := range []string{".localhost", ".local", ".internal", ".home", ".lan", ".corp", ".private", ".home.arpa"} {
		if strings.HasSuffix(host, suffix) {
			return false
		}
	}
	if ip := net.ParseIP(host); ip != nil {
		return publicIP(ip)
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 || len(host) > 253 {
		return false
	}
	numeric := true
	for _, label := range labels {
		if !dnsPattern.MatchString(label) {
			return false
		}
		if !regexp.MustCompile(`^(?:0x[0-9a-f]+|[0-9]+)$`).MatchString(label) {
			numeric = false
		}
	}
	return !numeric
}

const commonFields = "id kind title tags facets source provenance license attribution access_type legal_basis gdpr_relevant privacy_classification privacy_revision policy_decision_id redistributable confidence content_id capture_id capture_schema_version acquisition_lane acquisition_evidence_sha256 policy_attestation first_seen last_seen updated_at"

func validateCommon(item map[string]any) bool {
	laneCheck, validLane := item["acquisition_lane"].(string)
	if !validLane || (laneCheck != "web_http" && laneCheck != "sanctioned_api" && laneCheck != "bulk_download" && laneCheck != "derived_member") {
		return false
	}
	if !stable(item["id"]) || !text(item["source"], 255, false) || !text(item["license"], 120, false) || !text(item["attribution"], 255, false) || !text(item["legal_basis"], 500, false) || item["gdpr_relevant"] != false || item["redistributable"] != true || item["privacy_classification"] != "no_personal_data" {
		return false
	}
	if item["access_type"] != "public_document" && item["access_type"] != "open_license" && item["access_type"] != "public_domain" {
		return false
	}
	for _, key := range []string{"privacy_revision", "content_id", "capture_id", "acquisition_evidence_sha256", "policy_decision_id"} {
		raw, ok := item[key].(string)
		if !ok || !hashPattern.MatchString(raw) {
			return false
		}
	}
	confidence, ok := number(item["confidence"])
	if !ok || confidence < 0 || confidence > 1 {
		return false
	}
	provenance, ok := item["provenance"].(map[string]any)
	if !ok || len(provenance) > 0 {
		return false
	}
	tags, ok := item["tags"].([]any)
	if !ok {
		return false
	}
	seen := map[string]bool{}
	for _, tag := range tags {
		if !text(tag, 128, false) || seen[tag.(string)] {
			return false
		}
		seen[tag.(string)] = true
	}
	first, f := aware(item["first_seen"])
	last, l := aware(item["last_seen"])
	updated, u := aware(item["updated_at"])
	if !f || !l || !u || last.Before(first) || updated.Before(first) {
		return false
	}
	attestation, ok := item["policy_attestation"].(map[string]any)
	if !ok || !exactKeys(attestation, "decision_id evidence_digest clearance_digest subject_sha256 schema_version ruleset_version ruleset_hash action effect reasons obligations capture_id capture_schema_version acquisition_lane acquisition_evidence_sha256") {
		return false
	}
	for _, key := range []string{"decision_id", "evidence_digest", "clearance_digest"} {
		value, ok := attestation[key].(string)
		if !ok || !hashPattern.MatchString(value) {
			return false
		}
	}
	if !equalsNumber(attestation["schema_version"], 4) || !equalsNumber(attestation["ruleset_version"], 6) || attestation["ruleset_hash"] != PolicyHash || attestation["subject_sha256"] != item["content_id"] || attestation["decision_id"] != item["policy_decision_id"] || !equalsNumber(item["capture_schema_version"], 3) || !equalsNumber(attestation["capture_schema_version"], 3) || attestation["capture_id"] != item["capture_id"] || attestation["acquisition_evidence_sha256"] != item["acquisition_evidence_sha256"] || attestation["acquisition_lane"] != item["acquisition_lane"] || attestation["action"] != "publish_source" || attestation["effect"] != "allow" {
		return false
	}
	lane := item["acquisition_lane"]
	if lane != "web_http" && lane != "sanctioned_api" && lane != "bulk_download" && lane != "derived_member" {
		return false
	}
	reasons, ok := attestation["reasons"].([]any)
	if !ok || len(reasons) != 1 || reasons[0] != "explicitly_cleared" {
		return false
	}
	obligations, ok := attestation["obligations"].([]any)
	if !ok {
		return false
	}
	for _, obligation := range obligations {
		if obligation != "attribution" && obligation != "share_alike" && obligation != "noncommercial" && obligation != "verbatim_only" {
			return false
		}
	}
	return true
}
func location(value any) bool {
	loc, ok := value.(map[string]any)
	if !ok || !exactKeys(loc, "lat lon") {
		return false
	}
	lat, l := number(loc["lat"])
	lon, o := number(loc["lon"])
	return l && o && lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180
}
func ValidatePackItem(item map[string]any) bool {
	if !validateCommon(item) {
		return false
	}
	kind, _ := item["kind"].(string)
	category, ok := item["category"].(string)
	if !ok || !categoryPattern.MatchString(category) {
		return false
	}
	facets, ok := item["facets"].(map[string]any)
	if !ok || !optionalText(facets["city"], 128) || !optionalText(facets["county"], 128) {
		return false
	}
	if kind == "venue" {
		if !exactKeys(item, commonFields+" location address category website") || !text(item["title"], 255, false) || !location(item["location"]) || !publicURL(item["website"]) || !exactKeys(facets, "city county category venue_category place_category") || facets["category"] != category || facets["venue_category"] != category || facets["place_category"] != category {
			return false
		}
		address, ok := item["address"].(map[string]any)
		if !ok || !exactKeys(address, "street city county country") || !text(address["street"], 255, true) || !text(address["city"], 128, true) || !text(address["county"], 128, true) || !regexp.MustCompile(`^[A-Z]{2}$`).MatchString(fmt.Sprint(address["country"])) || address["city"] != stringValue(facets, "city") || address["county"] != stringValue(facets, "county") {
			return false
		}
		tags := item["tags"].([]any)
		return len(tags) == 1 && tags[0] == "venue:"+category
	}
	if kind != "event" && kind != "event_tombstone" {
		return false
	}
	required := commonFields + " category status lifecycle_status cancelled tombstone is_tombstone"
	allowed := required + " venue_id place_id"
	if kind == "event" {
		required += " start_datetime starts_at end_datetime ends_at timezone currency is_free venue_id place_id"
		allowed = required + " recurrence location ticket_url price_min price_max availability"
	}
	requiredKeys := strings.Fields(required)
	allowedKeys := map[string]bool{}
	for _, key := range strings.Fields(allowed) {
		allowedKeys[key] = true
	}
	for _, key := range requiredKeys {
		if _, ok := item[key]; !ok {
			return false
		}
	}
	for key := range item {
		if !allowedKeys[key] {
			return false
		}
	}
	status, ok := item["status"].(string)
	if !ok {
		return false
	}
	if !exactKeys(facets, "city county category venue_id place_id status lifecycle_status") || facets["category"] != category || facets["status"] != status || facets["lifecycle_status"] != status || item["lifecycle_status"] != status {
		return false
	}
	venue, present := item["venue_id"]
	_, placePresent := item["place_id"]
	if present != placePresent {
		return false
	}
	if venue != nil && !stable(venue) {
		return false
	}
	if present && item["place_id"] != venue {
		return false
	}
	if facets["venue_id"] != venue || facets["place_id"] != venue {
		return false
	}
	tags := item["tags"].([]any)
	if len(tags) != 2 || tags[0] != "event:"+category || tags[1] != "lifecycle:"+status {
		return false
	}
	if kind == "event_tombstone" {
		return item["title"] == "" && status == "removed" && item["cancelled"] == false && item["tombstone"] == true && item["is_tombstone"] == true
	}
	if !text(item["title"], 255, false) || !strings.Contains("|scheduled|rescheduled|postponed|cancelled|sold_out|moved_online|expired|", "|"+status+"|") || item["cancelled"] != (status == "cancelled") || item["tombstone"] != false || item["is_tombstone"] != false {
		return false
	}
	start, s := aware(item["start_datetime"])
	if !text(item["end_datetime"], 80, true) {
		return false
	}
	if !s || item["starts_at"] != item["start_datetime"] || item["ends_at"] != item["end_datetime"] {
		return false
	}
	if item["end_datetime"] != "" {
		end, e := aware(item["end_datetime"])
		if !e || end.Before(start) {
			return false
		}
	}
	zone, ok := item["timezone"].(string)
	if !ok || strings.TrimSpace(zone) != zone {
		return false
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return false
	}
	if !regexp.MustCompile(`^[A-Z]{3}$`).MatchString(fmt.Sprint(item["currency"])) {
		return false
	}
	if _, ok := item["is_free"].(bool); !ok {
		return false
	}
	var minimum, maximum *float64
	for _, key := range []string{"price_min", "price_max"} {
		value, exists := item[key]
		if !exists || value == nil {
			continue
		}
		n, ok := number(value)
		if !ok || n < 0 || n > 9999999999.99 {
			return false
		}
		if key == "price_min" {
			minimum = &n
		} else {
			maximum = &n
		}
	}
	if minimum != nil && maximum != nil && *maximum < *minimum {
		return false
	}
	if value, ok := item["recurrence"]; ok && !text(value, 1000, false) {
		return false
	}
	if value, ok := item["location"]; ok && !location(value) {
		return false
	}
	if value, ok := item["ticket_url"]; ok && !publicURL(value) {
		return false
	}
	if value, ok := item["availability"]; ok && (value != "available" && value != "limited" && value != "sold_out" && value != "unknown") {
		return false
	}
	return true
}

func (c *RoeduClient) Read(ctx context.Context, city string) (PackRead, error) {
	result := PackRead{Items: []map[string]any{}}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client = &copyClient
	cursor := ""
	seenCursor := map[string]bool{}
	seenItem := map[string]bool{}
	clean := true
	withheldTotal := int64(0)
	refusalConditions := uint8(0)
	producerDirty := false
	finishedPaging := false
	for pageIndex := 0; pageIndex < 10000; pageIndex++ {
		endpoint := strings.TrimRight(c.BaseURL, "/") + "/v1/app-packs/social_media_activities_app/" + url.PathEscape(SocialPack)
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return result, errors.New("invalid RO-EDU endpoint")
		}
		params := parsed.Query()
		params.Set("layer", "redistributable")
		params.Set("limit", "200")
		if city != "" {
			params.Set("city", city)
		}
		if cursor != "" {
			params.Set("cursor", cursor)
		}
		parsed.RawQuery = params.Encode()
		fetchCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		req, err := http.NewRequestWithContext(fetchCtx, "GET", parsed.String(), nil)
		if err != nil {
			cancel()
			return result, errors.New("invalid RO-EDU request")
		}
		req.Header.Set("X-API-Key", c.APIKey)
		req.Header.Set("Accept", "application/json")
		response, err := client.Do(req)
		if err != nil {
			cancel()
			return result, errors.New("RO-EDU unavailable")
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
		_ = response.Body.Close()
		cancel()
		if readErr != nil || len(raw) > 8<<20 || response.StatusCode != 200 {
			return result, errors.New("RO-EDU unavailable or oversized")
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var page map[string]any
		if decoder.Decode(&page) != nil || decoder.Decode(new(any)) != io.EOF || !exactKeys(page, "pack_id app layer schema_version snapshot_id release_id snapshot_generated_at snapshot_mode snapshot_complete items pagination withheld errors") {
			return result, errors.New("invalid RO-EDU page envelope")
		}
		snapshot, _ := page["snapshot_id"].(string)
		generated, _ := page["snapshot_generated_at"].(string)
		mode, _ := page["snapshot_mode"].(string)
		_, dateOK := aware(generated)
		complete, completeOK := page["snapshot_complete"].(bool)
		if page["pack_id"] != SocialPack || page["app"] != "social_media_activities_app" || page["layer"] != "redistributable" || !equalsNumber(page["schema_version"], 1) || !releasePattern.MatchString(snapshot) || page["release_id"] != snapshot || !dateOK || !completeOK || (mode != "full" && mode != "partial") || complete != (mode == "full") {
			return result, errors.New("RO-EDU product identity or completeness invalid")
		}
		if result.Snapshot != "" && (result.Snapshot != snapshot || result.Generated != generated || result.Mode != mode) {
			return result, errors.New("RO-EDU snapshot drift")
		}
		result.Pack = SocialPack
		result.Snapshot = snapshot
		result.Release = snapshot
		result.Generated = generated
		result.Mode = mode
		withheldRaw, ok := page["withheld"].(json.Number)
		withheldInt, integerErr := withheldRaw.Int64()
		if !ok || integerErr != nil || withheldInt < 0 || withheldInt > 1000000 {
			return result, errors.New("RO-EDU result envelope invalid")
		}
		withheld := int(withheldInt)
		producerErrors, ok := page["errors"].([]any)
		if !ok {
			return result, errors.New("RO-EDU errors invalid")
		}
		for _, problem := range producerErrors {
			if !text(problem, 4096, false) {
				return result, errors.New("invalid producer error")
			}
			refusalConditions |= publicRefusalCondition(problem.(string))
		}
		producerDirty = producerDirty || len(producerErrors) > 0
		withheldTotal += withheldInt
		clean = clean && withheld == 0 && len(producerErrors) == 0
		items, ok := page["items"].([]any)
		if !ok {
			return result, errors.New("RO-EDU items invalid")
		}
		for _, rawItem := range items {
			item, ok := rawItem.(map[string]any)
			if !ok || !ValidatePackItem(item) {
				clean = false
				continue
			}
			facets := item["facets"].(map[string]any)
			if city != "" && !strings.EqualFold(fmt.Sprint(facets["city"]), city) {
				clean = false
				continue
			}
			id := item["id"].(string)
			if seenItem[id] {
				clean = false
				continue
			}
			seenItem[id] = true
			result.Items = append(result.Items, item)
			if len(result.Items) > 1000000 {
				return result, errors.New("RO-EDU record bound exceeded")
			}
		}
		pagination, ok := page["pagination"].(map[string]any)
		if !ok || !exactKeys(pagination, "next_cursor") {
			return result, errors.New("invalid RO-EDU pagination")
		}
		if pagination["next_cursor"] == nil {
			result.Complete = complete && clean
			finishedPaging = true
			break
		}
		next, ok := pagination["next_cursor"].(string)
		if !ok || !text(next, 4096, false) || seenCursor[next] {
			return result, errors.New("RO-EDU cursor repeated or invalid")
		}
		seenCursor[next] = true
		cursor = next
	}
	if !finishedPaging {
		return result, errors.New("RO-EDU page bound exceeded")
	}
	venues := map[string]bool{}
	for _, item := range result.Items {
		if item["kind"] == "venue" {
			venues[item["id"].(string)] = true
		}
	}
	related := []map[string]any{}
	for _, item := range result.Items {
		if item["kind"] == "event" && item["venue_id"] != nil && !venues[fmt.Sprint(item["venue_id"])] {
			result.Complete = false
			continue
		}
		related = append(related, item)
	}
	result.Items = related
	if len(related) == 0 && (withheldTotal > 0 || producerDirty) {
		return result, productRefusal(withheldTotal, refusalConditions)
	}
	return result, nil
}
