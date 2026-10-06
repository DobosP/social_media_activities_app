package social

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Frozen source apps/social/tests/test_api.py, SHA-256
// fc35dea7bf6fc36444cb7aa4fcb52a274677bc0c78c328882dcd6ebdb759842f:
// test_rsvp_returns_live_count:322, test_rsvp_non_member_forbidden:338,
// test_rsvp_invalid_intent_is_400:349. Existing production is expected to pass.
func TestRESTSourceRSVPResponseAndRefusalMatrix(t *testing.T) {
	for _, prefix := range []string{"/api/social", "/api/v1/social"} {
		for _, scenario := range []struct {
			name   string
			intent string
			status int
		}{
			{"live_count", "going", http.StatusOK},
			{"non_member", "going", http.StatusForbidden},
			{"invalid_intent", "maybe?", http.StatusBadRequest},
		} {
			t.Run(prefix+"/"+scenario.name, func(t *testing.T) {
				s, _ := testStore(t)
				ctx := context.Background()
				owner := fixtureUser(t, s, "rest-rsvp-owner", "adult")
				peer := fixtureUser(t, s, "rest-rsvp-peer", "adult")
				activity := fixtureActivity(t, s, owner, nil)
				if _, err := s.DB.Exec(ctx, `UPDATE social_activity SET title='Run',starts_at=$2 WHERE id=$1`, activity, time.Now()); err != nil {
					t.Fatal(err)
				}
				actor := peer
				wantSeats := 1
				if scenario.name == "live_count" {
					retirementSeat(t, s, activity, peer.ID, "member")
					wantSeats = 2
				} else if scenario.name == "invalid_intent" {
					actor = owner
				}
				// The outsider has the same visible adult activity as its member
				// control, so the403 cannot be a cross-cohort/not-found refusal.
				if _, err := s.Activity(ctx, actor, activity); err != nil {
					t.Fatal("RSVP actor cannot see the fixture activity", err)
				}
				var threshold *int
				if err := s.DB.QueryRow(ctx, `SELECT min_to_go FROM social_activity WHERE id=$1`, activity).Scan(&threshold); err != nil || threshold != nil {
					t.Fatal("RSVP fixture unexpectedly has a quorum threshold", threshold, err)
				}
				out := restTransportCall(t, s, actor, fmt.Sprintf("%s/activities/%d/rsvp/", prefix, activity), map[string]any{"intent": scenario.intent})
				if out.Code != scenario.status {
					t.Errorf("RSVP %s REST status=%d, want%d", scenario.name, out.Code, scenario.status)
				}
				// Check committed state independently of the status and response.
				var seats, changed int
				if err := s.DB.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE attendance_intent<>'unknown') FROM social_membership WHERE activity_id=$1 AND state='member'`, activity).Scan(&seats, &changed); err != nil {
					t.Fatal(err)
				}
				wantChanged := 0
				if scenario.name == "live_count" {
					wantChanged = 1
					var ownerIntent, peerIntent string
					if err := s.DB.QueryRow(ctx, `SELECT o.attendance_intent,p.attendance_intent FROM social_membership o JOIN social_membership p ON p.activity_id=o.activity_id WHERE o.activity_id=$1 AND o.user_id=$2 AND p.user_id=$3`, activity, owner.ID, peer.ID).Scan(&ownerIntent, &peerIntent); err != nil || ownerIntent != "unknown" || peerIntent != "going" {
						t.Errorf("RSVP did not update only the requesting member: owner=%q peer=%q err=%v", ownerIntent, peerIntent, err)
					}
				}
				if seats != wantSeats || changed != wantChanged {
					t.Errorf("RSVP %s committed seats=%d changed=%d, want%d/%d", scenario.name, seats, changed, wantSeats, wantChanged)
				}
				if scenario.name != "live_count" {
					return
				}
				var response map[string]any
				if err := json.Unmarshal(out.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if response["going"] != float64(1) || response["total"] != float64(2) {
					t.Errorf("RSVP live counts=%v/%v, want1/2", response["going"], response["total"])
				}
				minimum, present := response["min_to_go"]
				if !present {
					t.Error("RSVP response omitted the min_to_go key")
				} else if minimum != nil {
					t.Errorf("RSVP min_to_go=%v, want null", minimum)
				}
			})
		}
	}
}
