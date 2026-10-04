package jobs

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

type eventKeyword struct {
	Term   string
	TypeID int64
}
type eventClassifier struct {
	Keywords []eventKeyword
	IDs      map[string]int64
}

func loadClassifier(ctx context.Context, q platform.Querier) (eventClassifier, error) {
	classifier := eventClassifier{IDs: map[string]int64{}}
	rows, err := q.Query(ctx, `SELECT id,slug,name,aliases FROM taxonomy_activitytype WHERE is_active ORDER BY id`)
	if err != nil {
		return classifier, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var slug, name string
		var raw []byte
		if err = rows.Scan(&id, &slug, &name, &raw); err != nil {
			return classifier, err
		}
		classifier.IDs[slug] = id
		var aliases []string
		_ = json.Unmarshal(raw, &aliases)
		terms := append([]string{strings.ReplaceAll(slug, "_", " "), name}, aliases...)
		seen := map[string]bool{}
		for _, term := range terms {
			term = strings.TrimSpace(strings.ToLower(term))
			if utf8.RuneCountInString(term) >= 3 && !seen[term] {
				seen[term] = true
				classifier.Keywords = append(classifier.Keywords, eventKeyword{term, id})
			}
		}
	}
	sort.SliceStable(classifier.Keywords, func(i, j int) bool {
		return utf8.RuneCountInString(classifier.Keywords[i].Term) > utf8.RuneCountInString(classifier.Keywords[j].Term)
	})
	return classifier, rows.Err()
}
func leadingWordContains(haystack, needle string) bool {
	for offset := 0; offset < len(haystack); {
		index := strings.Index(haystack[offset:], needle)
		if index < 0 {
			return false
		}
		index += offset
		if index == 0 {
			return true
		}
		before, _ := utf8.DecodeLastRuneInString(haystack[:index])
		if !unicode.IsLetter(before) && !unicode.IsNumber(before) && before != '_' {
			return true
		}
		offset = index + len(needle)
	}
	return false
}

var roeduCategoryType = map[string]string{"theatre": "theatre_show", "concert": "concert", "exhibition": "museum_visit", "film": "open_air_cinema", "festival": "festival", "conference": "community_event", "workshop": "workshop", "literature": "reading", "dance": "dance_social", "opera": "theatre_show", "family": "community_event"}

func (c eventClassifier) classify(category, title string) *int64 {
	if slug := roeduCategoryType[strings.ToLower(strings.TrimSpace(category))]; slug != "" {
		if id := c.IDs[slug]; id > 0 {
			return &id
		}
	}
	haystack := strings.ToLower(title)
	for _, keyword := range c.Keywords {
		if leadingWordContains(haystack, keyword.Term) {
			id := keyword.TypeID
			return &id
		}
	}
	return nil
}

// ClassifyActivity is the manual iCalendar/native operator seam. It shares the
// same declared taxonomy names, slugs, aliases and leading-word matching used by
// scheduled/canonical imports; no behavioral inference is introduced.
func ClassifyActivity(ctx context.Context, q platform.Querier, text string) (*int64, error) {
	classifier, err := loadClassifier(ctx, q)
	if err != nil {
		return nil, err
	}
	return classifier.classify("", text), nil
}
