package catalog

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// CityKey compares spelling variants without rewriting captured city facts.
// It is deliberately exact after Romanian diacritic folding, not fuzzy matching.
func CityKey(city string) string {
	key := strings.NewReplacer("ă", "a", "â", "a", "î", "i", "ș", "s", "ş", "s", "ț", "t", "ţ", "t").Replace(strings.ToLower(norm.NFC.String(strings.TrimSpace(city))))
	if key == "bucharest" {
		return "bucuresti"
	}
	return key
}

// cityKeySQL accepts trusted SQL expressions only; user values stay parameters.
func cityKeySQL(expression string) string {
	// Same Unicode whitespace set as strings.TrimSpace, including NBSP.
	const whitespace = " \t\n\r\v\f\u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000"
	key := "translate(lower(normalize(btrim(" + expression + ", '" + whitespace + "'), NFC)), 'ăâîșşțţ', 'aaisstt')"
	return "(CASE " + key + " WHEN 'bucharest' THEN 'bucuresti' ELSE " + key + " END)"
}

// CityEqualSQL accepts trusted SQL expressions only; user values stay parameters.
func CityEqualSQL(left, right string) string {
	return cityKeySQL(left) + "=" + cityKeySQL(right)
}
