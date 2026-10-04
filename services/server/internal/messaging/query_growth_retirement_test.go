package messaging

import (
	"context"
	"fmt"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestRetirementPostgresMessagingHistoryAndGuardianQueryGrowth(t *testing.T) {
	s := fixture(t)
	db, trace := testdb.TracedPool(t, s.DB)
	s.DB = db
	ctx := context.Background()
	viewer := fixtureUser(t, s, "growth-message-viewer", "adult")
	guardian := fixtureUser(t, s, "growth-message-guardian", "adult")
	conversation := func(owner platform.Actor, title, cohort string) int64 {
		var id int64
		if err := db.QueryRow(ctx, `INSERT INTO messaging_conversation(kind,title,cohort,disappearing_seconds,creator_id,created_at,updated_at) VALUES('group',$1,$2,0,$3,now(),now()) RETURNING id`, title, cohort, owner.ID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO messaging_participant(conversation_id,user_id,state,role,invited_by_id,created_at,joined_at,last_read_at) VALUES($1,$2,'active','admin',NULL,now(),now(),NULL)`, id, owner.ID); err != nil {
			t.Fatal(err)
		}
		return id
	}
	history := conversation(viewer, "Query history", "adult")
	seed := func(begin, end int) {
		for i := begin; i < end; i++ {
			sender := fixtureUser(t, s, fmt.Sprintf("growth-message-sender-%02d", i), "adult")
			if _, err := db.Exec(ctx, `INSERT INTO messaging_participant(conversation_id,user_id,state,role,invited_by_id,created_at,joined_at,last_read_at) VALUES($1,$2,'active','member',$3,now(),now(),NULL)`, history, sender.ID, viewer.ID); err != nil {
				t.Fatal(err)
			}
			// Committed synthetic ciphertext rows exercise the real read projection;
			// write/recipient completeness is independently covered by domain tests.
			var message int64
			if err := db.QueryRow(ctx, `INSERT INTO messaging_message(conversation_id,sender_id,algorithm,ciphertext,iv,created_at) VALUES($1,$2,'AES-GCM-256','synthetic-ciphertext','synthetic-iv',now()+$3*interval '1 millisecond') RETURNING id`, history, sender.ID, i).Scan(&message); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, `INSERT INTO messaging_messagekey(message_id,recipient_id,ephemeral_public_jwk,wrapped_key,wrap_iv,created_at) VALUES($1,$2,'{"kty":"EC","x":"synthetic-public"}','synthetic-wrapped-key','synthetic-wrap-iv',now())`, message, viewer.ID); err != nil {
				t.Fatal(err)
			}
			conversation(viewer, fmt.Sprintf("Query conversation %02d", i), "adult")
			ward := fixtureUser(t, s, fmt.Sprintf("growth-observed-ward-%02d", i), "child")
			conversation(ward, fmt.Sprintf("Query child conversation %02d", i), "child")
			if _, err := db.Exec(ctx, `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,relationship,status,consent_id,created_at,updated_at) VALUES($1,$2,'parent','active',NULL,now(),now())`, guardian.ID, ward.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	checks := []struct {
		name  string
		read  func() error
		small int64
	}{
		{name: "history_distinct_senders", read: func() error {
			v, e := s.Messages(ctx, viewer, history, 50, 0, 0)
			if e == nil && len(v) < 4 {
				t.Fatal("message fixture empty")
			}
			return e
		}},
		{name: "conversations", read: func() error {
			v, _, e := s.Conversations(ctx, viewer, "", 100, 0, false)
			if e == nil && len(v) < 4 {
				t.Fatal("conversation fixture empty")
			}
			return e
		}},
		{name: "guardian_distinct_wards", read: func() error {
			v, _, e := s.Conversations(ctx, guardian, "", 100, 0, true)
			if e == nil && len(v) < 4 {
				t.Fatal("guardian fixture empty")
			}
			return e
		}},
	}
	seed(0, 4)
	for i := range checks {
		trace.Reset()
		if err := checks[i].read(); err != nil {
			t.Fatal(checks[i].name, err)
		}
		checks[i].small = trace.Count()
	}
	seed(4, 28)
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			trace.Reset()
			if err := c.read(); err != nil {
				t.Fatal(err)
			}
			large := trace.Count()
			t.Logf("4 -> 28 rows: queries %d -> %d", c.small, large)
			if c.small == 0 || large > c.small+1 {
				t.Fatalf("per-record query growth: small=%d large=%d", c.small, large)
			}
		})
	}
}
