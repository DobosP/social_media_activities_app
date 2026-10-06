package accounts

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

// TestExportPostsHelperQueryCountEquality ports the frozen
// apps/accounts/tests/test_export.py::test_thread_posts_helper_query_count_is_flat
// assertion: one author-deleted post, then three more, must use exactly the same
// number of queries. The larger checkpoint strengthens that finite regression
// witness. Measure only the helper, with no allowance for an extra query and no
// magic total tied to its current SQL shape.
func TestExportPostsHelperQueryCountEquality(t *testing.T) {
	s := accountFixture(t)
	ctx := context.Background()
	owner := accountUser(t, s, "export-query-equality-owner", "adult", "adult")
	peer := accountUser(t, s, "export-query-equality-peer", "adult", "adult")
	activity, thread := casePort2Activity(t, s, owner, "Synthetic export query activity")
	accountPost(t, s, peer.ID, thread, "synthetic peer sentinel", true, true)
	s.Config.ExportPostCap = 32 // Every measured dataset fits, so the cap cannot mask growth.

	// Keep fixture writes on the original pool. Every helper connection uses the
	// traced pool; setup and result decoding are outside its measurement window.
	fixture := *s
	db, trace := testdb.TracedPool(t, s.DB)
	if err := db.Ping(ctx); err != nil {
		t.Fatal("export query fixture pool unavailable")
	}
	s.DB = db
	bodies := []string{}
	var baseline int64
	for _, size := range []int{1, 4, 16} {
		for len(bodies) < size {
			body := fmt.Sprintf("synthetic withdrawn post %02d", len(bodies))
			accountPost(t, &fixture, owner.ID, thread, body, true, true)
			bodies = append(bodies, body)
		}
		if !t.Run(fmt.Sprintf("author_deleted_posts_%d", size), func(t *testing.T) {
			trace.Reset()
			posts, err := s.exportPosts(ctx, owner.ID, true)
			queries := trace.Count()
			if err != nil {
				t.Fatal("export helper failed", err)
			}
			if queries == 0 {
				t.Fatal("export helper produced no traced queries")
			}
			if size == 1 {
				baseline = queries
			} else if queries != baseline {
				t.Fatalf("export helper query count changed: posts=1 queries=%d; posts=%d queries=%d", baseline, size, queries)
			}

			items, ok := posts["items"].([]json.RawMessage)
			if !ok || len(items) != size || posts["total"] != size || posts["truncated"] != false {
				t.Fatalf("export helper did not return the complete author-deleted dataset at size %d", size)
			}
			seen := map[string]bool{}
			for _, raw := range items {
				var row struct {
					Body        string `json:"body"`
					Status      string `json:"status"`
					ThreadKind  string `json:"thread_kind"`
					ThreadID    int64  `json:"thread_id"`
					ThreadTitle string `json:"thread_title"`
				}
				if err := json.Unmarshal(raw, &row); err != nil {
					t.Fatal("export helper returned invalid post JSON", err)
				}
				if row.Status != "deleted_by_you" || row.ThreadKind != "activity" || row.ThreadID != activity || row.ThreadTitle != "Synthetic export query activity" || seen[row.Body] {
					t.Fatal("export helper lost withdrawn-post status, thread context or unique authorship")
				}
				seen[row.Body] = true
			}
			for _, body := range bodies {
				if !seen[body] {
					t.Fatal("export helper omitted an author's synthetic withdrawn body")
				}
			}
			t.Logf("author-deleted posts=%d helper queries=%d", size, queries)
		}) {
			return
		}
	}
}
