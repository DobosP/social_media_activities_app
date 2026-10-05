package web

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestWebCasePortCalendarWireGrammar(t *testing.T) {
	start := time.Date(2030, 2, 1, 18, 30, 0, 0, time.UTC)
	calendar := func(title string) string {
		return socialCalendar([]map[string]any{{"id": int64(12), "title": title, "starts_at": start, "ends_at": nil, "place": map[string]any{"display_name": "Court"}}}, "fixture.local", start)
	}
	t.Run("omits_unset_end_time", func(t *testing.T) {
		body := calendar("No end set")
		if !strings.Contains(body, "DTSTART:") || strings.Contains(body, "DTEND:") {
			t.Fatal("unset end must remain absent")
		}
	})
	t.Run("summary_escapes_comma_and_semicolon", func(t *testing.T) {
		body := calendar("Chess, boards; all welcome")
		if !strings.Contains(body, `SUMMARY:Chess\, boards\; all welcome`) || strings.Contains(body, "Chess, boards") {
			t.Fatal("summary lost RFC5545 escaping")
		}
	})
	t.Run("crlf_header", func(t *testing.T) {
		body := calendar("CRLF check")
		if !strings.Contains(body, "\r\n") || !strings.HasPrefix(body, "BEGIN:VCALENDAR\r\n") {
			t.Fatal("calendar header lost CRLF")
		}
	})
	t.Run("folds_to_75_octets_without_losing_title", func(t *testing.T) {
		for _, title := range []string{"A very long meetup title that comfortably exceeds the seventy five octet folding limit", strings.Repeat("Întâlnire ", 20)} {
			body := calendar(title)
			for _, line := range strings.Split(body, "\r\n") {
				if len(line) > 75 || !utf8.ValidString(line) {
					t.Fatal("calendar line exceeded75octets or split a Unicode rune")
				}
			}
			if !strings.Contains(strings.ReplaceAll(body, "\r\n ", ""), "SUMMARY:"+title) {
				t.Fatal("unfolding did not preserve complete title")
			}
		}
	})
}
