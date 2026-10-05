package social

import (
	"context"
	"errors"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func casePort2ConnectionStatus(t *testing.T, s *Service, id int64, want string) {
	t.Helper()
	var status string
	if err := s.DB.QueryRow(context.Background(), `SELECT status FROM connections_connection WHERE id=$1`, id).Scan(&status); err != nil || status != want {
		t.Fatal("connection lifecycle state mismatch", err)
	}
}

func TestCasePort2ConnectionsSharedPeerAndCohortWalls(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	a := fixtureUser(t, s, "case2-connect-a", "adult")
	b := fixtureUser(t, s, "case2-connect-b", "adult")
	c := fixtureUser(t, s, "case2-connect-child", "child")
	d := fixtureUser(t, s, "case2-connect-child-peer", "child")
	teen := fixtureUser(t, s, "case2-connect-teen", "teen")
	if err := s.canConnect(ctx, s.DB, a, b); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("strangers connected without shared activity", err)
	}
	activity := fixtureActivity(t, s, a, nil)
	for _, peer := range []Actor{b, c, teen} {
		retirementSeat(t, s, activity, peer.ID, "member")
	}
	if err := s.canConnect(ctx, s.DB, a, b); err != nil {
		t.Fatal("live peer co-members could not connect", err)
	}
	for _, scenario := range []struct {
		name string
		a, b Actor
	}{
		{"adult-child", a, c}, {"child-adult", c, a}, {"adult-teen", a, teen}, {"teen-child", teen, c}, {"self", a, a},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if err := s.canConnect(ctx, s.DB, scenario.a, scenario.b); !errors.Is(err, platform.ErrForbidden) {
				t.Fatal("cohort/self wall widened despite synthetic shared membership", err)
			}
		})
	}
	childActivity := fixtureActivity(t, s, c, nil)
	retirementSeat(t, s, childActivity, d.ID, "member")
	if !s.ConnectionCohorts["child"] {
		t.Fatal("supported child cohort was disabled")
	}
	if err := s.canConnect(ctx, s.DB, c, d); err != nil {
		t.Fatal("consented children could not connect within own cohort", err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	for _, actors := range [][2]Actor{{a, b}, {b, a}} {
		if err := s.canConnect(ctx, s.DB, actors[0], actors[1]); !errors.Is(err, platform.ErrForbidden) {
			t.Fatal("persisted block allowed connection in one direction", err)
		}
	}
}

func TestCasePort2ConnectionRequestAcceptAndSeverLifecycle(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	for _, scenario := range []string{"request-accept", "decline-withdraw-remove"} {
		t.Run(scenario, func(t *testing.T) {
			a := fixtureUser(t, s, "case2-lifecycle-a-"+scenario, "adult")
			b := fixtureUser(t, s, "case2-lifecycle-b-"+scenario, "adult")
			activity := fixtureActivity(t, s, a, nil)
			retirementSeat(t, s, activity, b.ID, "member")
			id, err := s.RequestConnection(ctx, a, b.PublicID)
			if err != nil {
				t.Fatal(err)
			}
			casePort2ConnectionStatus(t, s, id, "pending")
			for _, viewer := range []Actor{a, b} {
				connections, err := s.Connections(ctx, viewer)
				if err != nil || len(connections) != 0 {
					t.Fatal("pending handshake already connected pair", err)
				}
			}
			if scenario == "decline-withdraw-remove" {
				if err := s.RespondConnection(ctx, b, id, "decline"); err != nil {
					t.Fatal(err)
				}
				casePort2ConnectionStatus(t, s, id, "declined")
				if connections, err := s.Connections(ctx, a); err != nil || len(connections) != 0 {
					t.Fatal("decline retained connection", err)
				}
				id, err = s.RequestConnection(ctx, a, b.PublicID)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.RespondConnection(ctx, a, id, "withdraw"); err != nil {
					t.Fatal(err)
				}
				casePort2ConnectionStatus(t, s, id, "withdrawn")
				id, err = s.RequestConnection(ctx, a, b.PublicID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := s.RespondConnection(ctx, b, id, "accept"); err != nil {
				t.Fatal(err)
			}
			casePort2ConnectionStatus(t, s, id, "accepted")
			for _, viewer := range []Actor{a, b} {
				connections, err := s.Connections(ctx, viewer)
				if err != nil || len(connections) != 1 {
					t.Fatal("accepted relationship was not symmetric", err)
				}
			}
			if scenario == "request-accept" {
				var requests, accepts int
				if err := s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM notifications_notification WHERE recipient_id=$1 AND kind='connection_request'),(SELECT count(*) FROM notifications_notification WHERE recipient_id=$2 AND kind='connection_accepted')`, b.ID, a.ID).Scan(&requests, &accepts); err != nil || requests != 1 || accepts != 1 {
					t.Fatal("request/accept handshake notice missing", err)
				}
			} else {
				if err := s.RemoveConnection(ctx, b, a.PublicID); err != nil {
					t.Fatal(err)
				}
				for _, viewer := range []Actor{a, b} {
					connections, err := s.Connections(ctx, viewer)
					if err != nil || len(connections) != 0 {
						t.Fatal("either-side removal retained connection", err)
					}
				}
			}
		})
	}
}

func casePort2SearchHas(t *testing.T, s *Service, a Actor, query, publicID string) bool {
	t.Helper()
	rows, err := s.SearchConnections(context.Background(), a, query)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range rows {
		if decodeObject(t, raw)["public_id"] == publicID {
			return true
		}
	}
	return false
}

func TestCasePort2ConnectionSearchOnlyAndGuardianSeatExclusion(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	a := fixtureUser(t, s, "case2-search-anchor", "adult")
	shared := fixtureUser(t, s, "case2-shared-mate", "adult")
	stranger := fixtureUser(t, s, "case2-stranger-mate", "adult")
	blocked := fixtureUser(t, s, "case2-blocked-mate", "adult")
	activity := fixtureActivity(t, s, a, nil)
	retirementSeat(t, s, activity, shared.ID, "member")
	retirementSeat(t, s, activity, blocked.ID, "member")
	for _, query := range []string{"", "a"} {
		rows, err := s.SearchConnections(ctx, a, query)
		if err != nil || len(rows) != 0 {
			t.Fatal("connection discovery became suggestions feed", err)
		}
	}
	if !casePort2SearchHas(t, s, a, "mate", shared.PublicID) {
		t.Fatal("eligible queried co-member missing")
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, a.ID, blocked.ID); err != nil {
		t.Fatal(err)
	}
	if casePort2SearchHas(t, s, a, "mate", stranger.PublicID) || casePort2SearchHas(t, s, a, "mate", blocked.PublicID) {
		t.Fatal("search enumerated non-shared or blocked target")
	}
	id, err := s.RequestConnection(ctx, a, shared.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RespondConnection(ctx, shared, id, "accept"); err != nil {
		t.Fatal(err)
	}
	if casePort2SearchHas(t, s, a, "mate", shared.PublicID) {
		t.Fatal("already connected target remained in discovery")
	}
	g1 := fixtureUser(t, s, "case2-supervisory-guardian1", "adult")
	g2 := fixtureUser(t, s, "case2-supervisory-guardian2", "adult")
	retirementSeat(t, s, activity, g1.ID, "guardian")
	retirementSeat(t, s, activity, g2.ID, "guardian")
	var peerContext bool
	if err := s.DB.QueryRow(ctx, `SELECT `+sharedActivity, g1.ID, g2.ID).Scan(&peerContext); err != nil || peerContext {
		t.Fatal("guardian seats created peer shared context", err)
	}
	if err := s.canConnect(ctx, s.DB, g1, g2); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("co-supervisors could connect through supervision", err)
	}
	if casePort2SearchHas(t, s, a, "guardian1", g1.PublicID) {
		t.Fatal("guardian surfaced as peer co-member search result")
	}
}
