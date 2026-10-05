package catalog

import "strings"

// PlainPlaceBrief uses already loaded facts. A nil venue slice is the list
// projection and never initiates a crowd-fact query or exposes vote counts.
func PlainPlaceBrief(name, address string, tags map[string]any, venue []map[string]any) [][]any {
	brief := [][]any{{"Place", name}}
	if address = strings.TrimSpace(address); address != "" {
		brief = append(brief, []any{"Where", "It is at " + address + "."})
	}
	facts := AccessibilityFacts(tags)
	for _, fact := range [][2]string{{"step_free", "Step-free access"}, {"accessible_toilet", "Accessible toilet"}, {"changing_table", "Baby changing table"}, {"tactile_paving", "Tactile paving"}, {"hearing_loop", "Hearing loop"}, {"automatic_door", "Automatic door"}} {
		brief = append(brief, []any{fact[1], plainFactState(facts[fact[0]])})
	}
	for _, row := range venue {
		state, _ := row["state"].(string)
		brief = append(brief, []any{row["label"], plainFactState(state)})
	}
	return brief
}

func plainFactState(state string) string {
	if word := map[string]string{"true": "yes", "false": "no", "limited": "limited"}[state]; word != "" {
		return word
	}
	return "not recorded"
}
