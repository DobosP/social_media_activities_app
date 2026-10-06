package social

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

// Frozen source apps/social/tests/test_api.py, SHA-256
// fc35dea7bf6fc36444cb7aa4fcb52a274677bc0c78c328882dcd6ebdb759842f:
// test_mine_membership_list_is_bounded:229,
// test_v1_mine_membership_list_is_cursor_paginated:242,
// test_v1_mine_membership_list_query_count_is_constant:256.
// Existing production is expected to satisfy these registered-handler assertions.
func TestRESTSourceOwnMembershipListBoundsCursorAndQueryCeiling(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		path      string
		title     string
		cap       int
		rows      int
		want      int
		cursor    bool
		queryScan bool
	}{
		{"legacy_bound", "/api/social/activities/mine/", "A", 3, 7, 3, false, false},
		{"v1_cursor", "/api/v1/social/activities/mine/?limit=2", "Mine ", 4, 7, 2, true, false},
		{"v1_query_ceiling", "/api/v1/social/activities/mine/?limit=10", "Mine q", 20, 12, 10, false, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s, _ := testStore(t)
			ctx := context.Background()
			owner := fixtureUser(t, s, "rest-own-memberships-owner", "adult")
			policy := DefaultPolicyConfig()
			policy.MembershipListLimit = scenario.cap
			if err := s.ConfigurePolicy(policy); err != nil {
				t.Fatal(err)
			}
			s.Cursor = platform.CursorCodec{Key: []byte("synthetic-own-memberships-cursor-key32")}
			start := time.Now()
			first := fixtureActivity(t, s, owner, nil)
			var place, typ int64
			if err := s.DB.QueryRow(ctx, `UPDATE social_activity SET title=$2,starts_at=$3 WHERE id=$1 RETURNING place_id,activity_type_id`, first, scenario.title+"0", start).Scan(&place, &typ); err != nil {
				t.Fatal(err)
			}
			for i := 1; i < scenario.rows; i++ {
				if _, err := s.CreateActivity(ctx, owner, ActivityInput{Place: place, ActivityType: typ, Title: fmt.Sprintf("%s%d", scenario.title, i), StartsAt: start}); err != nil {
					t.Fatal(err)
				}
			}
			var rows, owners int
			if err := s.DB.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE role='owner' AND state='member') FROM social_membership WHERE user_id=$1`, owner.ID).Scan(&rows, &owners); err != nil || rows != scenario.rows || owners != scenario.rows {
				t.Fatal("own membership seed is not the exact owner-member fixture", rows, owners, err)
			}
			var trace *testdb.QueryCounter
			if scenario.queryScan {
				s.DB, trace = testdb.TracedPool(t, s.DB)
			}
			mux := http.NewServeMux()
			s.Register(mux)
			call := func() *httptest.ResponseRecorder {
				out := httptest.NewRecorder()
				mux.ServeHTTP(out, platform.WithActor(httptest.NewRequest(http.MethodGet, scenario.path, nil), owner))
				return out
			}
			if trace != nil {
				// Fixture/schema setup and pool warmup are outside the first
				// registered API request; the tracer stores counts only.
				if err := s.DB.Ping(ctx); err != nil {
					t.Fatal(err)
				}
				trace.Reset()
			}
			out := call()
			if trace != nil {
				queries := trace.Count()
				t.Logf("actual registered own membership API queries=%d (ceiling4)", queries)
				if queries < 1 || queries > 4 {
					t.Errorf("own membership API traced queries=%d, want1..4", queries)
				}
			}
			if out.Code != http.StatusOK {
				t.Errorf("own membership REST status=%d, want200", out.Code)
			}
			if scenario.name == "legacy_bound" {
				var result []json.RawMessage
				if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
					t.Fatal("legacy membership response is not a raw JSON array", err)
				}
				if len(result) != scenario.want {
					t.Errorf("legacy own membership array length=%d, want%d", len(result), scenario.want)
				}
				return
			}
			var result struct {
				Limit      int               `json:"limit"`
				Results    []json.RawMessage `json:"results"`
				NextCursor string            `json:"next_cursor"`
			}
			if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Results) != scenario.want {
				t.Errorf("v1 own membership results length=%d, want%d", len(result.Results), scenario.want)
			}
			if scenario.cursor && (result.Limit != 2 || result.NextCursor == "") {
				t.Errorf("v1 own membership pagination limit=%d cursor-present=%t, want2/true", result.Limit, result.NextCursor != "")
			}
		})
	}
}
