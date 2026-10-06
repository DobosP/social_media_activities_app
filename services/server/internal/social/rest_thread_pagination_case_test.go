package social

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

// Frozen source apps/social/tests/test_api.py, SHA-256
// fc35dea7bf6fc36444cb7aa4fcb52a274677bc0c78c328882dcd6ebdb759842f:
// test_thread_posts_list_is_bounded:143,
// test_v1_thread_posts_are_cursor_paginated:158,
// test_v1_thread_posts_query_count_is_constant:181.
// Existing production is expected to satisfy these registered-handler assertions.
func TestRESTSourceThreadPostBoundsCursorAndQueryCeiling(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		prefix    string
		query     string
		cap       int
		posts     int
		cursor    bool
		queryScan bool
	}{
		{"legacy_bound", "/api", "", 5, 12, false, false},
		{"v1_cursor", "/api/v1", "?limit=3", 5, 12, true, false},
		{"v1_query_ceiling", "/api/v1", "?limit=10", 20, 15, false, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s, _ := testStore(t)
			ctx := context.Background()
			owner := fixtureUser(t, s, "rest-thread-pagination-owner", "adult")
			activity := fixtureActivity(t, s, owner, nil)
			if _, err := s.DB.Exec(ctx, `UPDATE social_activity SET title='Hike',starts_at=$2 WHERE id=$1`, activity, time.Now()); err != nil {
				t.Fatal(err)
			}
			policy := DefaultPolicyConfig()
			policy.ThreadPostLimit = scenario.cap
			if err := s.ConfigurePolicy(policy); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < scenario.posts; i++ {
				if _, err := s.WritePost(ctx, owner, "activity", activity, PostInput{Body: fmt.Sprintf("post %d", i)}, false); err != nil {
					t.Fatal(err)
				}
			}
			var count int
			var authoredVisible bool
			if err := s.DB.QueryRow(ctx, `SELECT count(*),bool_and(p.author_id=$2 AND NOT p.is_hidden) FROM social_post p JOIN social_thread t ON t.id=p.thread_id WHERE t.activity_id=$1`, activity, owner.ID).Scan(&count, &authoredVisible); err != nil || count != scenario.posts || !authoredVisible {
				t.Fatal("thread seed is not the exact visible owner-post fixture", count, authoredVisible, err)
			}
			var trace *testdb.QueryCounter
			if scenario.queryScan {
				s.DB, trace = testdb.TracedPool(t, s.DB)
			}
			mux := http.NewServeMux()
			s.Register(mux)
			path := fmt.Sprintf("%s/social/activities/%d/posts/", scenario.prefix, activity)
			call := func(query string) *httptest.ResponseRecorder {
				out := httptest.NewRecorder()
				mux.ServeHTTP(out, platform.WithActor(httptest.NewRequest(http.MethodGet, path+query, nil), owner))
				return out
			}
			if trace != nil {
				// Seed/schema/pool setup is outside the first endpoint GET. The
				// tracer records counts only; there is no endpoint warm request.
				if err := s.DB.Ping(ctx); err != nil {
					t.Fatal(err)
				}
				trace.Reset()
			}
			out := call(scenario.query)
			if trace != nil {
				queries := trace.Count()
				t.Logf("actual first registered thread-post API queries=%d (ceiling8)", queries)
				if queries < 1 || queries > 8 {
					t.Errorf("thread-post API traced queries=%d, want1..8", queries)
				}
			}
			if out.Code != http.StatusOK {
				t.Errorf("thread-post REST status=%d, want200", out.Code)
			}
			postBodies := func(raw json.RawMessage) []string {
				t.Helper()
				var rows []struct {
					Body string `json:"body"`
				}
				if err := json.Unmarshal(raw, &rows); err != nil {
					t.Fatal("thread results are not a JSON array", err)
				}
				bodies := make([]string, len(rows))
				for i, row := range rows {
					bodies[i] = row.Body
				}
				return bodies
			}
			if scenario.name == "legacy_bound" {
				bodies := postBodies(out.Body.Bytes())
				if !slices.Equal(bodies, []string{"post 7", "post 8", "post 9", "post 10", "post 11"}) {
					t.Errorf("legacy newest5 oldest-first bodies=%v, want post7..11", bodies)
				}
				return
			}
			var envelope map[string]json.RawMessage
			if err := json.Unmarshal(out.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			bodies := postBodies(envelope["results"])
			if !scenario.cursor {
				if len(bodies) != 10 {
					t.Errorf("v1 thread-post results length=%d, want10", len(bodies))
				}
				return
			}
			if len(envelope) != 3 || envelope["next_cursor"] == nil || envelope["limit"] == nil || envelope["results"] == nil {
				t.Error("v1 thread-post envelope is not exactly next_cursor/limit/results")
			}
			var limit int
			var cursor string
			if err := json.Unmarshal(envelope["limit"], &limit); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(envelope["next_cursor"], &cursor); err != nil {
				t.Fatal(err)
			}
			if limit != 3 || cursor == "" {
				t.Errorf("v1 thread pagination limit=%d cursor-present=%t, want3/true", limit, cursor != "")
			}
			if !slices.Equal(bodies, []string{"post 9", "post 10", "post 11"}) {
				t.Errorf("v1 first-page bodies=%v, want post9..11", bodies)
			}
			older := call("?limit=3&cursor=" + url.QueryEscape(cursor))
			if older.Code != http.StatusOK {
				t.Errorf("v1 returned-cursor request status=%d, want200", older.Code)
			}
			var olderEnvelope map[string]json.RawMessage
			if err := json.Unmarshal(older.Body.Bytes(), &olderEnvelope); err != nil {
				t.Fatal(err)
			}
			olderBodies := postBodies(olderEnvelope["results"])
			if !slices.Equal(olderBodies, []string{"post 6", "post 7", "post 8"}) {
				t.Errorf("v1 returned-cursor older-page bodies=%v, want post6..8", olderBodies)
			}
		})
	}
}
