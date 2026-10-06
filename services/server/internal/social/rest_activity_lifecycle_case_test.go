package social

import (
	"bytes"
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

// Frozen source apps/social/tests/test_api.py, SHA-256
// fc35dea7bf6fc36444cb7aa4fcb52a274677bc0c78c328882dcd6ebdb759842f:
// test_owner_can_edit_activity_via_patch:274,
// test_non_owner_cannot_patch_activity:290, test_owner_can_cancel_via_api:307.
// These owner-positive/nonmember-negative cases do not exclude co-organizers.
func TestRESTSourceActivityOwnerEditsNonmemberRefusalAndCancellation(t *testing.T) {
	for _, prefix := range []string{"/api/social", "/api/v1/social"} {
		for _, scenario := range []string{"owner_patch", "nonmember_patch", "owner_cancel"} {
			t.Run(prefix+"/"+scenario, func(t *testing.T) {
				s, _ := testStore(t)
				ctx := context.Background()
				owner := fixtureUser(t, s, "rest-lifecycle-owner", "adult")
				actor := owner
				activity := fixtureActivity(t, s, owner, nil)
				title, start := "Old", time.Now().Add(24*time.Hour)
				method, action := http.MethodPatch, ""
				body := map[string]any{"title": "New name"}
				wantCode, wantTitle, wantStatus := http.StatusOK, "New name", "open"
				if scenario == "nonmember_patch" {
					actor = fixtureUser(t, s, "rest-lifecycle-nonmember", "adult")
					title, body = "Keep", map[string]any{"title": "hijack"}
					wantCode, wantTitle = http.StatusForbidden, "Keep"
				} else if scenario == "owner_cancel" {
					title, start = "Run", time.Now()
					method, action = http.MethodPost, "cancel/"
					body = map[string]any{"reason": "weather"}
					wantTitle, wantStatus = "Run", "cancelled"
				}
				if _, err := s.DB.Exec(ctx, `UPDATE social_activity SET title=$2,starts_at=$3 WHERE id=$1`, activity, title, start); err != nil {
					t.Fatal(err)
				}
				if scenario == "nonmember_patch" {
					if _, err := s.Activity(ctx, actor, activity); err != nil || actor.Cohort != owner.Cohort {
						t.Fatal("nonmember cannot see the same-cohort fixture", err)
					}
					var seated bool
					if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM social_membership WHERE activity_id=$1 AND user_id=$2)`, activity, actor.ID).Scan(&seated); err != nil || seated {
						t.Fatal("negative actor is not a genuine nonmember", seated, err)
					}
				}
				var before json.RawMessage
				var auditsBefore int
				if err := s.DB.QueryRow(ctx, `SELECT to_jsonb(a),(SELECT count(*) FROM safety_auditlog) FROM social_activity a WHERE a.id=$1`, activity).Scan(&before, &auditsBefore); err != nil {
					t.Fatal(err)
				}
				raw, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				request := platform.WithActor(httptest.NewRequest(method, fmt.Sprintf("%s/activities/%d/%s", prefix, activity, action), strings.NewReader(string(raw))), actor)
				request.Header.Set("Content-Type", "application/json")
				mux := http.NewServeMux()
				s.Register(mux)
				out := httptest.NewRecorder()
				mux.ServeHTTP(out, request)
				if out.Code != wantCode {
					t.Errorf("%s REST status=%d, want%d", scenario, out.Code, wantCode)
				}
				// Read committed state independently of status and wire projection.
				var after json.RawMessage
				var storedTitle, storedStatus string
				var auditsAfter int
				if err := s.DB.QueryRow(ctx, `SELECT to_jsonb(a),a.title,a.status,(SELECT count(*) FROM safety_auditlog) FROM social_activity a WHERE a.id=$1`, activity).Scan(&after, &storedTitle, &storedStatus, &auditsAfter); err != nil {
					t.Fatal(err)
				}
				if storedTitle != wantTitle || storedStatus != wantStatus {
					t.Errorf("%s committed title/status=%q/%q, want%q/%q", scenario, storedTitle, storedStatus, wantTitle, wantStatus)
				}
				if scenario == "nonmember_patch" {
					if !bytes.Equal(before, after) || auditsBefore != auditsAfter {
						t.Errorf("nonmember PATCH refusal wrote activity or audit state: audits%d->%d", auditsBefore, auditsAfter)
					}
					return
				}
				var response map[string]any
				if err := json.Unmarshal(out.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if response["title"] != wantTitle || response["status"] != wantStatus {
					t.Errorf("%s response title/status=%v/%v, want%q/%q", scenario, response["title"], response["status"], wantTitle, wantStatus)
				}
			})
		}
	}
}
