package catalog

import (
	"strconv"
	"strings"
	"time"
)

type Schedule map[string][][2]int

var Days = []string{"mo", "tu", "we", "th", "fr", "sa", "su"}

func minutes(raw string) (int, bool) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 2 || len(parts[0]) < 1 || len(parts[0]) > 2 || len(parts[1]) != 2 {
		return 0, false
	}
	h, e1 := strconv.Atoi(parts[0])
	m, e2 := strconv.Atoi(parts[1])
	if e1 != nil || e2 != nil {
		return 0, false
	}
	if h == 24 && m == 0 {
		return 1440, true
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}
func dayIndex(s string) int {
	for i, day := range Days {
		if day == s {
			return i
		}
	}
	return -1
}
func expandDays(raw string) ([]int, bool) {
	out := []int{}
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, false
		}
		if strings.Contains(p, "-") {
			bounds := strings.SplitN(p, "-", 2)
			start, end := dayIndex(bounds[0]), dayIndex(bounds[1])
			if start < 0 || end < 0 {
				return nil, false
			}
			for i := start; ; i = (i + 1) % 7 {
				out = append(out, i)
				if i == end {
					break
				}
			}
		} else {
			index := dayIndex(p)
			if index < 0 {
				return nil, false
			}
			out = append(out, index)
		}
	}
	return out, true
}
func ParseOpeningHours(raw string) Schedule {
	text := strings.ToLower(strings.TrimSpace(raw))
	if text == "" {
		return nil
	}
	schedule := Schedule{}
	for _, day := range Days {
		schedule[day] = [][2]int{}
	}
	if text == "24/7" || text == "24/7 open" || text == "mo-su 00:00-24:00" {
		for _, day := range Days {
			schedule[day] = [][2]int{{0, 1440}}
		}
		return schedule
	}
	parsed := false
	for _, rule := range strings.Split(text, ";") {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			continue
		}
		parts := strings.Fields(rule)
		if len(parts) < 2 {
			return nil
		}
		days, ok := expandDays(parts[0])
		if !ok || len(days) == 0 {
			return nil
		}
		if len(parts) == 2 && (parts[1] == "off" || parts[1] == "closed") {
			parsed = true
			continue
		}
		for _, spec := range parts[1:] {
			spec = strings.TrimRight(spec, ",")
			for _, window := range strings.Split(spec, ",") {
				bounds := strings.SplitN(window, "-", 2)
				if len(bounds) != 2 {
					return nil
				}
				start, ok := minutes(bounds[0])
				if !ok {
					return nil
				}
				end, ok := minutes(bounds[1])
				if !ok {
					return nil
				}
				for _, day := range days {
					if end > start {
						schedule[Days[day]] = append(schedule[Days[day]], [2]int{start, end})
					} else if end < start {
						schedule[Days[day]] = append(schedule[Days[day]], [2]int{start, 1440})
						schedule[Days[(day+1)%7]] = append(schedule[Days[(day+1)%7]], [2]int{0, end})
					}
				}
				parsed = true
			}
		}
	}
	if !parsed {
		return nil
	}
	return schedule
}
func OpenAt(schedule Schedule, when time.Time) *bool {
	if len(schedule) == 0 {
		return nil
	}
	day := (int(when.Weekday()) + 6) % 7
	minute := when.Hour()*60 + when.Minute()
	open := false
	for _, window := range schedule[Days[day]] {
		if window[0] <= minute && minute < window[1] {
			open = true
			break
		}
	}
	return &open
}
