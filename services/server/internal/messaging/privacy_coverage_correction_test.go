package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestCoverageCorrectionActualMessageReadsUseRecipientSpecificKeys(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	a := fixtureUser(t, s, "correction-key-sender", "adult")
	b := fixtureUser(t, s, "correction-key-peer", "adult")
	id, err := s.Start(ctx, a, "direct", []string{b.Username}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, b, id, "accept"); err != nil {
		t.Fatal(err)
	}
	in := packet(a, b)
	in.RecipientKeys[0].WrappedKey = "synthetic-wrapped-for-sender-only"
	in.RecipientKeys[0].WrapIV = "synthetic-wrap-iv-sender"
	in.RecipientKeys[1].WrappedKey = "synthetic-wrapped-for-peer-only"
	in.RecipientKeys[1].WrapIV = "synthetic-wrap-iv-peer"
	message, err := s.Post(ctx, a, id, in)
	if err != nil {
		t.Fatal(err)
	}
	for index, viewer := range []platform.Actor{a, b} {
		t.Run(viewer.Username, func(t *testing.T) {
			want, other := in.RecipientKeys[index], in.RecipientKeys[1-index]
			rows, err := s.Messages(ctx, viewer, id, 50, 0, 0)
			if err != nil || len(rows) != 1 {
				t.Fatal("own wrapped-key history unavailable", err)
			}
			row := rows[0].(map[string]any)
			key := row["key"].(map[string]any)
			if key["wrapped_key"] != want.WrappedKey || key["wrap_iv"] != want.WrapIV {
				t.Fatal("history selected another recipient's key")
			}
			if _, exists := row["keys"]; exists {
				t.Fatal("private REST history exposed broadcast key set")
			}
			one, err := s.Message(ctx, viewer, message, false)
			if err != nil || one["key"].(map[string]any)["wrapped_key"] != want.WrappedKey {
				t.Fatal("single-message read selected another recipient's key", err)
			}
			for _, base := range []string{"/api/messaging/", "/api/v1/messaging/"} {
				out := call(s, viewer, "GET", fmt.Sprintf("%sconversations/%d/messages/", base, id), nil)
				if out.Code != 200 || strings.Contains(out.Body.String(), other.WrappedKey) || !strings.Contains(out.Body.String(), want.WrappedKey) {
					t.Fatal("actual history transport leaked/replaced recipient key", out.Code)
				}
				var decoded any
				if err := json.Unmarshal(out.Body.Bytes(), &decoded); err != nil {
					t.Fatal(err)
				}
				var wireRows []any
				if strings.Contains(base, "/v1/") {
					wireRows = decoded.(map[string]any)["results"].([]any)
				} else {
					wireRows = decoded.([]any)
				}
				if len(wireRows) != 1 || wireRows[0].(map[string]any)["key"].(map[string]any)["wrapped_key"] != want.WrappedKey {
					t.Fatal("wire own-key identity mismatch")
				}
			}
		})
	}
}

func TestCoverageCorrectionReportPersistsHarassmentExcerptWithoutAuditContent(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	a := fixtureUser(t, s, "correction-report-sender", "adult")
	b := fixtureUser(t, s, "correction-report-member", "adult")
	id, err := s.Start(ctx, a, "direct", []string{b.Username}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, b, id, "accept"); err != nil {
		t.Fatal(err)
	}
	in := packet(a, b)
	message, err := s.Post(ctx, a, id, in)
	if err != nil {
		t.Fatal(err)
	}
	const excerpt = "synthetic client disclosed harassment evidence"
	report, err := s.Report(ctx, b, id, message, "harassment", "Synthetic reporting note", excerpt)
	if err != nil {
		t.Fatal(err)
	}
	var reason, detail string
	var reporter int64
	if err := s.DB.QueryRow(ctx, `SELECT reason,detail,reporter_id FROM safety_report WHERE id=$1`, report).Scan(&reason, &detail, &reporter); err != nil || reason != "harassment" || reporter != b.ID || !strings.Contains(detail, excerpt) {
		t.Fatal("report reason/excerpt/actor was transformed", err)
	}
	var leaked bool
	if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM safety_auditlog WHERE position($1 in data::text)>0 OR position($2 in data::text)>0 OR position($3 in data::text)>0)`, excerpt, in.Ciphertext, in.RecipientKeys[0].WrappedKey).Scan(&leaked); err != nil || leaked {
		t.Fatal("encrypted/decrypted content entered durable audit", err)
	}
}
