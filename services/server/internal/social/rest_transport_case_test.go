package social

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

// Frozen source: apps/social/tests/test_api.py, SHA-256
// fc35dea7bf6fc36444cb7aa4fcb52a274677bc0c78c328882dcd6ebdb759842f.
// These cases exercise the registered REST adapters, not their HTML callers.
func restTransportCall(t *testing.T, s *Service, a Actor, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := platform.WithActor(httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw))), a)
	r.Header.Set("Content-Type", "application/json")
	mux := http.NewServeMux()
	s.Register(mux)
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, r)
	return out
}

// Source declaration: test_activity_description_too_long_rejected, line126.
func TestRESTSourceActivityDescriptionFieldError(t *testing.T) {
	for _, prefix := range []string{"/api/social", "/api/v1/social"} {
		t.Run(prefix, func(t *testing.T) {
			s, _ := testStore(t)
			ctx := context.Background()
			owner := fixtureUser(t, s, "rest-description-owner", "adult")
			activity := fixtureActivity(t, s, owner, nil)
			var place, typ, before int64
			if err := s.DB.QueryRow(ctx, `SELECT place_id,activity_type_id,(SELECT count(*) FROM social_activity) FROM social_activity WHERE id=$1`, activity).Scan(&place, &typ, &before); err != nil {
				t.Fatal(err)
			}
			out := restTransportCall(t, s, owner, prefix+"/activities/", map[string]any{"place": place, "activity_type": typ, "title": "Valid title", "description": strings.Repeat("d", 2001), "starts_at": time.Now().Add(time.Hour).Format(time.RFC3339)})
			if out.Code != http.StatusBadRequest {
				t.Fatal("overlong description status", out.Code)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(out.Body.Bytes(), &fields); err != nil {
				t.Fatal(err)
			}
			if _, ok := fields["description"]; !ok {
				t.Fatal("description validation lost its REST field key", out.Body.String())
			}
			var messages []string
			if err := json.Unmarshal(fields["description"], &messages); err != nil || len(messages) == 0 {
				t.Fatal("description REST field error is not a nonempty string array", err)
			}
			var after int64
			if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM social_activity`).Scan(&after); err != nil || after != before {
				t.Fatal("rejected description wrote an activity", before, after, err)
			}
		})
	}
}

// Source declaration: test_post_body_too_long_rejected, line103. The positive
// exact4000 boundary distinguishes the fixed source ceiling from a smaller cap.
func TestRESTSourcePostBodyFieldErrorAndExactBoundary(t *testing.T) {
	for _, prefix := range []string{"/api/social", "/api/v1/social"} {
		t.Run(prefix, func(t *testing.T) {
			s, _ := testStore(t)
			ctx := context.Background()
			owner := fixtureUser(t, s, "rest-post-body-owner", "adult")
			activity := fixtureActivity(t, s, owner, nil)
			path := fmt.Sprintf("%s/activities/%d/posts/", prefix, activity)
			out := restTransportCall(t, s, owner, path, map[string]any{"body": strings.Repeat("x", 4001)})
			if out.Code != http.StatusBadRequest {
				t.Fatal("overlong post body status", out.Code)
			}
			var count int
			if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM social_post p JOIN social_thread t ON t.id=p.thread_id WHERE t.activity_id=$1`, activity).Scan(&count); err != nil || count != 0 {
				t.Fatal("overlong post body persisted", count, err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(out.Body.Bytes(), &fields); err != nil {
				t.Fatal(err)
			}
			if _, ok := fields["body"]; !ok {
				t.Fatal("post validation lost its REST field key", out.Body.String())
			}
			var messages []string
			if err := json.Unmarshal(fields["body"], &messages); err != nil || len(messages) == 0 {
				t.Fatal("body REST field error is not a nonempty string array", err)
			}
			accepted := restTransportCall(t, s, owner, path, map[string]any{"body": strings.Repeat("y", 4000)})
			if accepted.Code != http.StatusCreated {
				t.Fatal("exact source body limit rejected", accepted.Code)
			}
			var stored string
			if err := s.DB.QueryRow(ctx, `SELECT p.body FROM social_post p JOIN social_thread t ON t.id=p.thread_id WHERE t.activity_id=$1`, activity).Scan(&stored); err != nil || stored != strings.Repeat("y", 4000) {
				t.Fatal("exact source body limit not stored intact", len(stored), err)
			}
		})
	}
}

// Source declaration: test_transit_invalid_status_is_forbidden, line406.
func TestRESTSourceTransitInvalidStatusForbiddenNoMutation(t *testing.T) {
	for _, prefix := range []string{"/api/social", "/api/v1/social"} {
		t.Run(prefix, func(t *testing.T) {
			s, _ := testStore(t)
			ctx := context.Background()
			owner := fixtureUser(t, s, "rest-transit-owner", "adult")
			activity := fixtureActivity(t, s, owner, nil)
			// An otherwise valid arrival window isolates the invalid-status refusal.
			if _, err := s.DB.Exec(ctx, `UPDATE social_activity SET starts_at=$2 WHERE id=$1`, activity, time.Now().Add(5*time.Minute)); err != nil {
				t.Fatal(err)
			}
			path := fmt.Sprintf("%s/activities/%d/transit/", prefix, activity)
			out := restTransportCall(t, s, owner, path, map[string]any{"status": "teleporting"})
			var state string
			if err := s.DB.QueryRow(ctx, `SELECT transit_status FROM social_membership WHERE activity_id=$1 AND user_id=$2`, activity, owner.ID).Scan(&state); err != nil || state != "none" {
				t.Fatal("invalid transit changed presence", state, err)
			}
			if out.Code != http.StatusForbidden {
				t.Fatal("invalid transit lost its REST forbidden status", out.Code)
			}
			valid := restTransportCall(t, s, owner, path, map[string]any{"status": "on_my_way"})
			if valid.Code != http.StatusOK {
				t.Fatal("valid transit control failed", valid.Code)
			}
		})
	}
}
