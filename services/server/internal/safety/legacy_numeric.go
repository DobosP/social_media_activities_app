package safety

import (
	"encoding/json"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"math"
	"sort"
	"strconv"
	"strings"
)

// JSONB loses integral-float type for positive scientific exponents in legacy
// rows. A bounded digest-matching alternative preserves both integer precision
// and the original immutable hash; it never rewrites historical data.
func legacyNumericVariants(value any) []any {
	switch v := value.(type) {
	case json.Number:
		out := []any{v}
		raw := v.String()
		if !strings.ContainsAny(raw, ".eE") && len(strings.TrimPrefix(raw, "-")) >= 17 {
			number, err := strconv.ParseFloat(raw, 64)
			if err == nil && !math.IsInf(number, 0) {
				out = append(out, number)
			}
		}
		return out
	case map[string]any:
		out := []any{map[string]any{}}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			element := v[key]
			next := []any{}
			for _, existing := range out {
				for _, option := range legacyNumericVariants(element) {
					copy := map[string]any{}
					for k, v := range existing.(map[string]any) {
						copy[k] = v
					}
					copy[key] = option
					next = append(next, copy)
					if len(next) >= 64 {
						break
					}
				}
				if len(next) >= 64 {
					break
				}
			}
			out = next
		}
		return out
	case []any:
		out := []any{[]any{}}
		for _, element := range v {
			next := []any{}
			for _, existing := range out {
				for _, option := range legacyNumericVariants(element) {
					copy := append([]any{}, existing.([]any)...)
					copy = append(copy, option)
					next = append(next, copy)
					if len(next) >= 64 {
						break
					}
				}
				if len(next) >= 64 {
					break
				}
			}
			out = next
		}
		return out
	default:
		return []any{value}
	}
}

func legacyNumericHashMatches(payload map[string]any, previous, stored string) bool {
	for _, variant := range legacyNumericVariants(payload) {
		raw, err := platform.CanonicalJSON(variant)
		if err == nil && sha([]byte(previous+sha(raw))) == stored {
			return true
		}
	}
	return false
}
