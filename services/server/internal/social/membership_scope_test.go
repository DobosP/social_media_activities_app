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

// Membership rows carry live presence (arrived/transit/departing), so the
// read surface is co-member scoped rather than cohort-wide (owner decision
// 2026-10-05; a deliberate departure from Django's MembershipViewSet).
func TestPostgresMembershipReadsAreCoMemberScoped(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	owner := fixtureUser(t, s, "scope-owner", "adult")
	member := fixtureUser(t, s, "scope-member", "adult")
	requester := fixtureUser(t, s, "scope-requester", "adult")
	outsider := fixtureUser(t, s, "scope-outsider", "adult")
	child := fixtureUser(t, s, "scope-child", "child")
	id := fixtureActivity(t, s, owner, nil)
	elsewhere := fixtureActivity(t, s, outsider, nil)
	mid, err := s.Join(ctx, member, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Vote(ctx, owner, mid, true, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE social_membership SET arrived_at=now(),transit_status='on_my_way' WHERE id=$1`, mid); err != nil {
		t.Fatal(err)
	}
	rid, err := s.Join(ctx, requester, id)
	if err != nil {
		t.Fatal(err)
	}
	var ownerRow, outsiderRow int64
	if err := s.DB.QueryRow(ctx, `SELECT (SELECT id FROM social_membership WHERE activity_id=$1 AND user_id=$2),(SELECT id FROM social_membership WHERE activity_id=$3 AND user_id=$4)`, id, owner.ID, elsewhere, outsider.ID).Scan(&ownerRow, &outsiderRow); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	call := func(a Actor, method, path, body string) (int, map[string]any) {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, platform.WithActor(httptest.NewRequest(method, path, strings.NewReader(body)), a))
		out := map[string]any{}
		if rec.Code == 200 {
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
		}
		return rec.Code, out
	}
	listed := func(a Actor) map[int64]map[string]any {
		t.Helper()
		code, body := call(a, "GET", "/api/v1/social/memberships/?limit=200", "")
		if code != 200 {
			t.Fatalf("%s list status=%d", a.Username, code)
		}
		rows := map[int64]map[string]any{}
		for _, raw := range body["results"].([]any) {
			row := raw.(map[string]any)
			rows[int64(row["id"].(float64))] = row
		}
		if int(body["count"].(float64)) != len(rows) {
			t.Fatalf("%s count=%v disagrees with visible rows=%d", a.Username, body["count"], len(rows))
		}
		return rows
	}
	detail := func(a Actor, membership int64) (int, map[string]any) {
		t.Helper()
		return call(a, "GET", fmt.Sprintf("/api/v1/social/memberships/%d/", membership), "")
	}

	// A same-cohort non-member sees only their own rows, never activity X's.
	rows := listed(outsider)
	for rowID, row := range rows {
		if int64(row["activity"].(float64)) == id {
			t.Fatalf("non-member listed another activity's membership %d: %v", rowID, row)
		}
	}
	if _, ok := rows[outsiderRow]; !ok || len(rows) != 1 {
		t.Fatalf("non-member own rows=%v", rows)
	}
	for _, path := range []string{"/api/v1/social/memberships/%d/", "/api/social/memberships/%d/"} {
		for _, target := range []int64{mid, rid, ownerRow} {
			if code, body := call(outsider, "GET", fmt.Sprintf(path, target), ""); code != 404 {
				t.Fatalf("non-member read membership %d status=%d body=%v", target, code, body)
			}
		}
	}

	// A co-member sees the activity's rows, presence included.
	rows = listed(member)
	for _, want := range []int64{mid, ownerRow, rid} {
		if _, ok := rows[want]; !ok {
			t.Fatalf("co-member missing row %d: %v", want, rows)
		}
	}
	if code, body := detail(member, mid); code != 200 || body["arrived_at"] == nil || body["transit_status"] != "on_my_way" {
		t.Fatalf("co-member presence read status=%d body=%v", code, body)
	}

	// The requester sees their own pending row and nothing else of X.
	rows = listed(requester)
	if row, ok := rows[rid]; !ok || len(rows) != 1 || row["state"] != "requested" {
		t.Fatalf("requester rows=%v", rows)
	}
	if code, body := detail(requester, rid); code != 200 || body["state"] != "requested" {
		t.Fatalf("requester own row status=%d body=%v", code, body)
	}
	if code, _ := detail(requester, mid); code != 404 {
		t.Fatalf("pending requester read a member's presence status=%d", code)
	}

	// The organizer sees and votes on the pending request; voting still answers
	// with the row for each eligible voter.
	if row, ok := listed(owner)[rid]; !ok || row["state"] != "requested" {
		t.Fatalf("organizer cannot see pending request: %v", row)
	}
	vote := fmt.Sprintf("/api/v1/social/memberships/%d/vote/", rid)
	if code, body := call(owner, "POST", vote, `{"approve":true}`); code != 200 || body["state"] != "requested" {
		t.Fatalf("organizer vote status=%d body=%v", code, body)
	}
	if code, body := call(member, "POST", vote, `{"approve":true}`); code != 200 || body["state"] != "member" {
		t.Fatalf("co-member vote status=%d body=%v", code, body)
	}

	// Co-members never see each other's rows across a block, either way; their
	// own rows stay visible (owner decision 2026-10-05).
	if _, err := s.DB.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, member.ID, requester.ID); err != nil {
		t.Fatal(err)
	}
	if rows := listed(member); rows[rid] != nil || rows[mid] == nil {
		t.Fatalf("blocker saw the blocked co-member's row: %v", rows)
	}
	if rows := listed(requester); rows[mid] != nil || rows[rid] == nil {
		t.Fatalf("blocked co-member saw the blocker's row: %v", rows)
	}
	if code, _ := detail(requester, mid); code != 404 {
		t.Fatalf("blocked co-member read the blocker's presence status=%d", code)
	}
	if _, err := s.DB.Exec(ctx, `DELETE FROM safety_block WHERE blocker_id=$1 AND blocked_id=$2`, member.ID, requester.ID); err != nil {
		t.Fatal(err)
	}

	// Cross-cohort stays invisible.
	if rows := listed(child); len(rows) != 0 {
		t.Fatalf("cross-cohort rows=%v", rows)
	}
	if code, _ := detail(child, mid); code != 404 {
		t.Fatalf("cross-cohort membership read status=%d", code)
	}
}
