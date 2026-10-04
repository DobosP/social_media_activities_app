package web

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var threadTokens = regexp.MustCompile("`([^`\n]+)`|\\*\\*(\\S(?:[^\n]*?\\S)?)\\*\\*|\\*(\\S(?:[^*\n]*?\\S)?)\\*|_(\\S(?:[^_\n]*?\\S)?)_|@([\\p{L}\\p{N}_.-]{1,150})|(https?://[^\\s<]+)")
var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&#x27;")
var italicStar = regexp.MustCompile(`^\*(\S(?:[^*\n]*?\S)?)\*$`)
var italicUnderscore = regexp.MustCompile(`^_(\S(?:[^_\n]*?\S)?)_$`)

func escape(raw string) string { return htmlEscaper.Replace(raw) }
func word(r rune) bool         { return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' }

// BodyMarkup shares exactly one escape-first implementation between native
// live chat and the ordinary HTML thread. A minor's URLs stay plain text.
func BodyMarkup(body string, roster map[string]bool, allowLinks bool) string {
	var output strings.Builder
	last, search := 0, 0
	for search < len(body) {
		indices := threadTokens.FindStringSubmatchIndex(body[search:])
		if indices == nil {
			break
		}
		for i := range indices {
			if indices[i] >= 0 {
				indices[i] += search
			}
		}
		start, end := indices[0], indices[1]
		kind := 0
		for i := 1; i < 7; i++ {
			if indices[2*i] >= 0 {
				kind = i
				break
			}
		}
		before, after := rune(0), rune(0)
		if start > 0 {
			before, _ = utf8.DecodeLastRuneInString(body[:start])
		}
		if end < len(body) {
			after, _ = utf8.DecodeRuneInString(body[end:])
		}
		// RE2 has no lookaround. If an emphasis candidate ends before a word or
		// another delimiter, try later delimiters before applying the boundary gate.
		// This preserves the legacy regex's backtracking for cases such as __x__.
		if kind == 3 || kind == 4 {
			delimiter := byte('*')
			pattern := italicStar
			if kind == 4 {
				delimiter = '_'
				pattern = italicUnderscore
			}
			invalid := func(r rune) bool { return word(r) || (kind == 3 && r == '*') }
			if invalid(after) && !invalid(before) {
				for candidate := end; candidate < len(body) && body[candidate] != '\n'; candidate++ {
					if body[candidate] != delimiter {
						continue
					}
					next := rune(0)
					if candidate+1 < len(body) {
						next, _ = utf8.DecodeRuneInString(body[candidate+1:])
					}
					if invalid(next) {
						continue
					}
					if match := pattern.FindStringSubmatchIndex(body[start : candidate+1]); match != nil {
						end = candidate + 1
						indices[1] = end
						indices[kind*2] = start + match[2]
						indices[kind*2+1] = start + match[3]
						after = next
						break
					}
				}
			}
		}
		if (kind == 3 && (word(before) || before == '*' || word(after) || after == '*')) || (kind == 4 && (word(before) || word(after))) || (kind == 5 && (word(before) || before == '@')) {
			search = start + 1
			continue
		}
		output.WriteString(escape(body[last:start]))
		inner := body[indices[kind*2]:indices[kind*2+1]]
		switch kind {
		case 1:
			output.WriteString("<code>" + escape(inner) + "</code>")
		case 2:
			output.WriteString("<strong>" + escape(inner) + "</strong>")
		case 3, 4:
			output.WriteString("<em>" + escape(inner) + "</em>")
		case 5:
			if roster[strings.ToLower(inner)] {
				output.WriteString(`<span class="mention">@` + escape(inner) + `</span>`)
			} else {
				output.WriteString(escape(body[start:end]))
			}
		case 6:
			raw := strings.TrimRight(inner, ".,;:!?")
			trail := inner[len(raw):]
			if allowLinks && raw != "" {
				output.WriteString(`<a href="` + escape(raw) + `" rel="noopener noreferrer nofollow" target="_blank">` + escape(raw) + `</a>` + escape(trail))
			} else {
				output.WriteString(escape(inner))
			}
		}
		last = end
		search = end
	}
	output.WriteString(escape(body[last:]))
	rendered := strings.ReplaceAll(output.String(), "\r\n", "\n")
	rendered = strings.ReplaceAll(rendered, "\r", "\n")
	return strings.ReplaceAll(rendered, "\n", "<br>")
}
