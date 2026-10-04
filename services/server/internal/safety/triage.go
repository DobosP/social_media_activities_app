package safety

import (
	"golang.org/x/text/cases"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

var contactTerms = []string{"whatsapp", "telegram", "signal", "snapchat", "snap", "instagram", "insta", "discord", "messenger", "viber", "tiktok", "kik", "phone number", "your number", "my number", "call me", "text me", "dm me", "add me", "message me privately", "meet me alone", "come alone", "don't tell", "dont tell", "do not tell", "our secret", "keep it secret", "off the app", "off this app", "numar de telefon", "numarul tau", "numarul meu", "suna-ma", "suna ma", "scrie-mi", "scrie mi", "adauga-ma", "adauga ma", "vino singur", "vino singura", "ne vedem singuri", "nu spune", "secretul nostru", "pastreaza secret"}
var normalizeDiacritics = strings.NewReplacer("ă", "a", "â", "a", "î", "i", "ș", "s", "ş", "s", "ț", "t", "ţ", "t")
var phonePattern = regexp.MustCompile(`(?:\p{Nd}[ \-]?){7,}`)
var emailPattern = regexp.MustCompile(`[^\s@]+@[^\s@]+\.[^\s@]+`)

func word(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' }
func ContactHintTerms(raw string) []string {
	normalized := normalizeDiacritics.Replace(cases.Fold().String(raw))
	hits := map[string]bool{}
	for _, term := range contactTerms {
		start := 0
		for start < len(normalized) {
			at := strings.Index(normalized[start:], term)
			if at < 0 {
				break
			}
			at += start
			before := []rune(normalized[:at])
			after := []rune(normalized[at+len(term):])
			if (len(before) == 0 || !word(before[len(before)-1])) && (len(after) == 0 || !word(after[0])) {
				hits[term] = true
				break
			}
			start = at + len(term)
		}
	}
	if phonePattern.MatchString(normalized) {
		hits["phone-number"] = true
	}
	if emailPattern.MatchString(raw) {
		hits["email-address"] = true
	}
	out := []string{}
	for hit := range hits {
		out = append(out, hit)
	}
	sort.Strings(out)
	return out
}
