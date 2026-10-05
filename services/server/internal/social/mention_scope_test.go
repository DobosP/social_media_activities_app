package social

import (
	"context"
	"testing"
)

// GO-PRIV-05: @mentions resolve on activity threads only. A standing group
// thread never turns a name into a ping, so its member set stays unenumerable.
func TestPostgresGroupThreadPingResolvesNoMentions(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	owner := fixtureUser(t, s, "go-mention-owner", "adult")
	peer := fixtureUser(t, s, "go-mention-peer", "adult")
	mentions := func() int {
		t.Helper()
		var n int
		if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM notifications_notification WHERE recipient_id=$1 AND kind='mention'`, peer.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	var typeID int64
	if err := s.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typeID); err != nil {
		t.Fatal(err)
	}
	s.AllowUserGroups = true
	gid, err := s.CreateGroup(ctx, owner, GroupInput{City: "Cluj-Napoca", ActivityType: &typeID, Title: "Mention scope group"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.JoinGroup(ctx, peer, gid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WritePost(ctx, owner, "group", gid, PostInput{Body: "See you there @go-mention-peer", Ping: true}, false); err != nil {
		t.Fatal(err)
	}
	if n := mentions(); n != 0 {
		t.Fatalf("group thread ping resolved %d mention notification(s)", n)
	}
	id := fixtureActivity(t, s, owner, p(3))
	mid, err := s.Join(ctx, peer, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Vote(ctx, owner, mid, true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WritePost(ctx, owner, "activity", id, PostInput{Body: "See you there @go-mention-peer", Ping: true}, false); err != nil {
		t.Fatal(err)
	}
	if n := mentions(); n != 1 {
		t.Fatalf("activity thread ping produced %d mention notification(s), want 1", n)
	}
}
