package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// A PostgreSQL bigint is an exact public resource identity, including during
// the read-time hours/visual overlay. Assert the wire through typed int64 fields
// so the fixture cannot repeat the float conversion it is intended to detect.
func TestCasePortPostgresPlaceWireKeepsExactBigintIdentity(t *testing.T) {
	s := fixture(t, true)
	ctx := context.Background()
	place := placeFixture(t, s, "Large identity venue", "osm", 23.6, 46.77)
	const large int64 = 9007199254740993
	if _, err := s.DB.Exec(ctx, `UPDATE places_place SET id=$2 WHERE id=$1`, place, large); err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) }
	for _, path := range []string{"/api/places/", fmt.Sprintf("/api/places/%d/", large)} {
		t.Run(path, func(t *testing.T) {
			out := request(t, s, path, nil)
			if out.Code != 200 {
				t.Fatal("public venue unavailable", out.Code)
			}
			type feature struct {
				ID         int64 `json:"id"`
				Properties struct {
					Name    string `json:"name"`
					OpenNow bool   `json:"open_now"`
				} `json:"properties"`
			}
			var row feature
			if path == "/api/places/" {
				var page struct {
					Features []feature `json:"features"`
				}
				if err := json.Unmarshal(out.Body.Bytes(), &page); err != nil || len(page.Features) != 1 {
					t.Fatal("public fixture cardinality", err)
				}
				row = page.Features[0]
			} else if err := json.Unmarshal(out.Body.Bytes(), &row); err != nil {
				t.Fatal(err)
			}
			if row.ID != large || row.Properties.Name != "Large identity venue" || !row.Properties.OpenNow {
				t.Fatalf("read-time overlay changed exact identity: got=%d want=%d", row.ID, large)
			}
		})
	}
}
