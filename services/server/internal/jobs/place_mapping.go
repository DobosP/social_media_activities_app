package jobs

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
	"golang.org/x/text/unicode/norm"
)

//go:embed place-rules.json
var placeRulesJSON []byte

type placeRule struct {
	RuleID     string            `json:"rule_id"`
	Match      map[string]string `json:"match"`
	Slug       string            `json:"slug"`
	Confidence float64           `json:"confidence"`
}

var placeRules = func() []placeRule {
	var rules []placeRule
	if json.Unmarshal(placeRulesJSON, &rules) != nil {
		panic("invalid reviewed native place rules")
	}
	return rules
}()

type PlaceMatch struct {
	Slug, RuleID string
	Confidence   float64
}

func MatchPlaceTags(tags map[string]any) []PlaceMatch {
	out := []PlaceMatch{}
	indices := map[string]int{}
	for _, rule := range placeRules {
		matched := true
		for key, value := range rule.Match {
			if tags[key] != value {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		current, ok := indices[rule.Slug]
		if !ok {
			indices[rule.Slug] = len(out)
			out = append(out, PlaceMatch{rule.Slug, rule.RuleID, rule.Confidence})
		} else if out[current].Confidence < rule.Confidence {
			out[current] = PlaceMatch{rule.Slug, rule.RuleID, rule.Confidence}
		}
	}
	return out
}
func RoeduVenueTags(item map[string]any) map[string]any {
	low := strings.ToLower(stringValue(item, "title") + " " + stringValue(item, "category"))
	tags := map[string]any{"amenity": "arts_centre"}
	groups := []struct {
		needles []string
		tags    map[string]any
	}{
		{[]string{"opera", "operă"}, map[string]any{"amenity": "theatre", "theatre:genre": "opera"}},
		{[]string{"filarmonic", "filarmonică", "concert"}, map[string]any{"amenity": "theatre", "theatre:type": "concert"}},
		{[]string{"teatru", "theater", "theatre"}, map[string]any{"amenity": "theatre"}},
		{[]string{"muzeu", "museum", "muzeul"}, map[string]any{"tourism": "museum"}},
		{[]string{"galeri", "gallery", "artă", "arta"}, map[string]any{"tourism": "gallery"}},
		{[]string{"bibliotec", "library"}, map[string]any{"amenity": "library"}},
		{[]string{"cinema", "film"}, map[string]any{"amenity": "cinema"}}}
outer:
	for _, group := range groups {
		for _, needle := range group.needles {
			if strings.Contains(low, needle) {
				tags = group.tags
				break outer
			}
		}
	}
	tags["roedu:tags"] = item["tags"]
	facets, _ := item["facets"].(map[string]any)
	for _, key := range []string{"city", "county", "category", "venue_category", "place_category"} {
		if v := stringValue(facets, key); v != "" {
			tags["roedu:"+key] = v
		}
	}
	for _, key := range []string{"source", "confidence", "privacy_revision", "access_type", "legal_basis", "policy_decision_id", "content_id", "capture_id", "acquisition_lane", "acquisition_evidence_sha256", "policy_attestation"} {
		tags["roedu:"+key] = item[key]
	}
	return tags
}
func normalizePlaceName(raw string) []rune {
	raw = strings.ToLower(norm.NFKD.String(raw))
	var b strings.Builder
	for _, r := range raw {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || unicode.IsSpace(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	return []rune(strings.Join(strings.Fields(b.String()), " "))
}

// PlaceNameSimilarity implements difflib.SequenceMatcher's matching-block ratio,
// including its popular-character heuristic. It never performs an edit-distance
// merge which could absorb a distinct neighboring venue more aggressively.
func PlaceNameSimilarity(left, right string) float64 {
	a, b := normalizePlaceName(left), normalizePlaceName(right)
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	if string(a) == string(b) {
		return 1
	}
	b2j := map[rune][]int{}
	for j, r := range b {
		b2j[r] = append(b2j[r], j)
	}
	if len(b) >= 200 {
		for r, list := range b2j {
			if len(list) > len(b)/100+1 {
				delete(b2j, r)
			}
		}
	}
	type rangePair struct{ alo, ahi, blo, bhi int }
	stack := []rangePair{{0, len(a), 0, len(b)}}
	matched := 0
	for len(stack) > 0 {
		part := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		besti, bestj, bestsize := part.alo, part.blo, 0
		previous := map[int]int{}
		for i := part.alo; i < part.ahi; i++ {
			current := map[int]int{}
			for _, j := range b2j[a[i]] {
				if j < part.blo {
					continue
				}
				if j >= part.bhi {
					break
				}
				size := previous[j-1] + 1
				current[j] = size
				if size > bestsize {
					besti, bestj, bestsize = i-size+1, j-size+1, size
				}
			}
			previous = current
		}
		for besti > part.alo && bestj > part.blo && a[besti-1] == b[bestj-1] {
			besti--
			bestj--
			bestsize++
		}
		for besti+bestsize < part.ahi && bestj+bestsize < part.bhi && a[besti+bestsize] == b[bestj+bestsize] {
			bestsize++
		}
		if bestsize == 0 {
			continue
		}
		matched += bestsize
		if part.alo < besti && part.blo < bestj {
			stack = append(stack, rangePair{part.alo, besti, part.blo, bestj})
		}
		if besti+bestsize < part.ahi && bestj+bestsize < part.bhi {
			stack = append(stack, rangePair{besti + bestsize, part.ahi, bestj + bestsize, part.bhi})
		}
	}
	return 2 * float64(matched) / float64(len(a)+len(b))
}
func findRoeduDuplicate(ctx context.Context, tx pgx.Tx, item map[string]any) (int64, error) {
	location := item["location"].(map[string]any)
	rows, err := tx.Query(ctx, `SELECT id,name FROM places_place WHERE source<>'roedu' AND ST_DWithin(location,ST_SetSRID(ST_MakePoint($1,$2),4326)::geography,75) ORDER BY ST_Distance(location,ST_SetSRID(ST_MakePoint($1,$2),4326)::geography),id LIMIT 50`, numberValue(location, "lon"), numberValue(location, "lat"))
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		if err = rows.Scan(&id, &name); err != nil {
			return 0, err
		}
		if PlaceNameSimilarity(name, stringValue(item, "title")) >= .82 {
			return id, nil
		}
	}
	return 0, rows.Err()
}
func mapRoeduVenue(ctx context.Context, tx pgx.Tx, id int64, tags map[string]any) error {
	for _, match := range MatchPlaceTags(tags) {
		var typeID int64
		err := tx.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug=$1`, match.Slug).Scan(&typeID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("native place mapping references missing taxonomy")
		}
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO places_placeactivity(place_id,activity_id,origin,confidence,source,mapping_rule,is_disputed,created_at,updated_at) VALUES($1,$2,'inferred',$3,'roedu',$4,false,now(),now()) ON CONFLICT(place_id,activity_id) DO UPDATE SET origin='inferred',confidence=EXCLUDED.confidence,source='roedu',mapping_rule=EXCLUDED.mapping_rule,updated_at=now() WHERE places_placeactivity.origin NOT IN ('confirmed','manual') AND places_placeactivity.confidence<=EXCLUDED.confidence`, id, typeID, match.Confidence, match.RuleID); err != nil {
			return err
		}
	}
	return nil
}
