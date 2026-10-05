package social

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

// The safe-exit service must also answer success through both HTTP adapters.
// Its own-row response must not widen membership or activity read visibility.
func TestPostgresActivityLeaveAPISurvivesOwnerBlocks(t *testing.T) {
	for _, prefix := range []string{"/api/social", "/api/v1/social"} {
		for _, direction := range []string{"member_blocks_owner", "owner_blocks_member"} {
			t.Run(prefix+"/"+direction, func(t *testing.T) {
				s, _ := testStore(t)
				ctx := context.Background()
				owner := fixtureUser(t, s, "exit-response-owner", "adult")
				member := fixtureUser(t, s, "exit-response-member", "adult")
				outsider := fixtureUser(t, s, "exit-response-outsider", "adult")
				activity := fixtureActivity(t, s, owner, nil)
				mid, err := s.Join(ctx, member, activity)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.Vote(ctx, owner, mid, true, true); err != nil {
					t.Fatal(err)
				}
				if _, err := s.DB.Exec(ctx, `UPDATE social_membership SET arrived_at=now(),transit_status='on_my_way',departing_at=now(),attendance_intent='going' WHERE id=$1`, mid); err != nil {
					t.Fatal(err)
				}
				blocker, blocked := member.ID, owner.ID
				if direction == "owner_blocks_member" {
					blocker, blocked = blocked, blocker
				}
				if _, err := s.DB.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, blocker, blocked); err != nil {
					t.Fatal(err)
				}
				mux := http.NewServeMux()
				s.Register(mux)
				call := func(actor Actor, method, path string) *httptest.ResponseRecorder {
					out := httptest.NewRecorder()
					mux.ServeHTTP(out, platform.WithActor(httptest.NewRequest(method, path, strings.NewReader(`{}`)), actor))
					return out
				}
				path := fmt.Sprintf("%s/activities/%d/leave/", prefix, activity)
				if out := call(outsider, "POST", path); out.Code != 404 {
					t.Fatal("non-member leave acquired another member's row", out.Code)
				}
				out := call(member, "POST", path)
				var state string
				if err := s.DB.QueryRow(ctx, `SELECT state FROM social_membership WHERE id=$1`, mid).Scan(&state); err != nil || state != "removed" {
					t.Fatal("safe exit did not commit removal", state, err)
				}
				if out.Code != 200 {
					t.Fatal("committed safe exit returned an error under an owner block", out.Code)
				}
				var row map[string]any
				if err := json.Unmarshal(out.Body.Bytes(), &row); err != nil {
					t.Fatal(err)
				}
				keys := []string{"id", "activity", "user", "role", "state", "attendance_intent", "arrived_at", "transit_status", "departing_at", "created_at", "decided_at"}
				if len(row) != len(keys) {
					t.Fatal("leave changed the membership response shape", row)
				}
				for _, key := range keys {
					if _, ok := row[key]; !ok {
						t.Fatal("leave omitted membership field", key)
					}
				}
				if row["id"] != float64(mid) || row["activity"] != float64(activity) || row["user"] != member.DisplayName || row["state"] != "removed" || row["role"] != "member" || row["attendance_intent"] != "unknown" || row["transit_status"] != "none" || row["arrived_at"] != nil || row["departing_at"] != nil {
					t.Fatal("leave did not serialize only the caller's cleared membership", row)
				}
				if out := call(member, "GET", fmt.Sprintf("%s/memberships/%d/", prefix, mid)); out.Code != 404 {
					t.Fatal("safe-exit response widened ordinary membership reads", out.Code)
				}
				if out := call(member, "GET", fmt.Sprintf("%s/activities/%d/", prefix, activity)); out.Code != 404 {
					t.Fatal("safe-exit response widened blocked activity reads", out.Code)
				}
			})
		}
	}
}
