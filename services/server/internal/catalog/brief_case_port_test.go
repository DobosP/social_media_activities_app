package catalog

import (
	"reflect"
	"testing"
)

func TestCasePortPlainPlaceBriefStatesAddressAndCountFreeList(t *testing.T) {
	rows := PlainPlaceBrief("Cluj Library", " Main St 1, Cluj ", map[string]any{"wheelchair": "yes", "toilets:wheelchair": "no"}, nil)
	if !reflect.DeepEqual(rows[0], []any{"Place", "Cluj Library"}) || !reflect.DeepEqual(rows[1], []any{"Where", "It is at Main St 1, Cluj."}) {
		t.Fatal("brief name/address", rows)
	}
	byLabel := map[string]any{}
	for _, row := range rows {
		byLabel[row[0].(string)] = row[1]
	}
	if byLabel["Step-free access"] != "yes" || byLabel["Accessible toilet"] != "no" || byLabel["Baby changing table"] != "not recorded" {
		t.Fatal("honest accessibility words", byLabel)
	}
	for _, label := range []string{"Drinking water", "Toilets"} {
		if _, ok := byLabel[label]; ok {
			t.Fatal("list loaded crowd facts", label)
		}
	}
	if got := PlainPlaceBrief("No address", "", nil, nil); len(got) != 7 {
		t.Fatal("absent address fabricated", got)
	}
	venue := []map[string]any{{"label": "Drinking water", "state": "true", "yes": 123, "no": 99, "user_id": 8}, {"label": "Toilets", "state": "false"}, {"label": "Shade", "state": "limited"}, {"label": "Shelter", "state": "unknown"}}
	rows = PlainPlaceBrief("Cluj Library", "", nil, venue)
	byLabel = map[string]any{}
	for _, row := range rows {
		byLabel[row[0].(string)] = row[1]
		if row[0] != "Place" && !map[any]bool{"yes": true, "no": true, "limited": true, "not recorded": true}[row[1]] {
			t.Fatal("brief count/data leakage", row)
		}
	}
	if byLabel["Drinking water"] != "yes" || byLabel["Toilets"] != "no" || byLabel["Shade"] != "limited" || byLabel["Shelter"] != "not recorded" {
		t.Fatal("detail facts omitted", byLabel)
	}
}
