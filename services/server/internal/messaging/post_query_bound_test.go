package messaging

import (
	"context"
	"fmt"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

// Post validates only the sender and batches recipient keys, so its statement
// count must not depend on membership. The tracer keeps counts only.
func TestPostgresPostQueriesAreMembershipIndependent(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	sender := fixtureUser(t, s, "post-bound-sender", "adult")
	group := func(name string, size int) (int64, MessageInput) {
		t.Helper()
		var id int64
		if err := s.DB.QueryRow(ctx, `INSERT INTO messaging_conversation(kind,title,cohort,disappearing_seconds,creator_id,created_at,updated_at) VALUES('group',$1,'adult',0,$2,now(),now()) RETURNING id`, name, sender.ID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.Exec(ctx, `INSERT INTO messaging_participant(conversation_id,user_id,state,role,invited_by_id,created_at,joined_at,last_read_at) VALUES($1,$2,'active','admin',NULL,now(),now(),NULL)`, id, sender.ID); err != nil {
			t.Fatal(err)
		}
		members := []platform.Actor{sender}
		for i := 1; i < size; i++ {
			m := fixtureUser(t, s, fmt.Sprintf("%s-member-%03d", name, i), "adult")
			if _, err := s.DB.Exec(ctx, `INSERT INTO messaging_participant(conversation_id,user_id,state,role,invited_by_id,created_at,joined_at,last_read_at) VALUES($1,$2,'active','member',$3,now(),now(),NULL)`, id, m.ID, sender.ID); err != nil {
				t.Fatal(err)
			}
			members = append(members, m)
		}
		return id, packet(members...)
	}
	small, smallInput := group("post-bound-3", 3)
	large, largeInput := group("post-bound-256", 256)
	db, trace := testdb.TracedPool(t, s.DB)
	s.DB = db
	send := func(id int64, in MessageInput) int64 {
		t.Helper()
		trace.Reset()
		if _, err := s.Post(ctx, sender, id, in); err != nil {
			t.Fatal("bounded send failed", err)
		}
		return trace.Count()
	}
	n3 := send(small, smallInput)
	n256 := send(large, largeInput)
	t.Logf("post queries 3 -> 256 members: %d -> %d (ceiling 28)", n3, n256)
	if n256 != n3 || n256 > 28 {
		t.Fatalf("send query count depends on membership or exceeds the ceiling: n3=%d n256=%d", n3, n256)
	}
	var keys int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM messaging_messagekey k JOIN messaging_message m ON m.id=k.message_id WHERE m.conversation_id=$1`, large).Scan(&keys); err != nil || keys != 256 {
		t.Fatal("batched recipient keys", keys, err)
	}
}
