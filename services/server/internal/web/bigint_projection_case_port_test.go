package web

import (
	"context"
	"fmt"
	"testing"
)

func TestCasePortWebPlaceIdentityKeepsBigintInPageAndClaimLinks(t *testing.T) {
	s, actor, place, _, mux := webCasePortFixture(t)
	const large int64 = 9007199254740993
	if _, err := s.DB.Exec(context.Background(), `UPDATE places_place SET id=$2 WHERE id=$1`, place, large); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/places/%d/", large)
	body := webCasePortHTML(t, mux, actor, path)
	webCasePortContains(t, body, fmt.Sprintf(`href="/places/%d/claim/"`, large))
	r := socialLegacyRequest("GET", path, actor, large, nil)
	data, _, handled, err := s.PublicView(r, actor, "place_detail")
	if err != nil || !handled || spaID(spaMap(data["place"])) != large {
		t.Fatal("HTML projection rounded original int64 place", err)
	}
}

func TestCasePortWebSQLProjectionKeepsNestedIDsAndNumericTemplateTypes(t *testing.T) {
	s, _, _, _, _ := webCasePortFixture(t)
	const large int64 = 9007199254740993
	rows, err := socialRows(context.Background(), s.DB, `SELECT jsonb_build_object('id',$1::bigint,'count',2,'ratio',0.5,'nested',jsonb_build_object('id',$1::bigint),'items',jsonb_build_array(jsonb_build_object('id',$1::bigint)))`, large)
	if err != nil || len(rows) != 1 {
		t.Fatal("SQL numeric fixture", err)
	}
	row := rows[0]
	if spaID(row) != large || spaID(spaMap(row["nested"])) != large || spaID(spaMap(row["items"].([]any)[0])) != large {
		t.Fatal("nested SQL identity lost precision")
	}
	if count, ok := row["count"].(int); !ok || count != 2 {
		t.Fatal("template integer type changed", row["count"])
	}
	if ratio, ok := row["ratio"].(float64); !ok || ratio != 0.5 {
		t.Fatal("template decimal type changed", row["ratio"])
	}
}
