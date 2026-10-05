package messaging

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestCasePort3CiphertextByteCapsAndExactRecipientBoundaryPersistOnlyValid(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	a := fixtureUser(t, s, "case3-byte-sender", "adult")
	b := fixtureUser(t, s, "case3-byte-recipient", "adult")
	id := casePort3ActiveDirect(t, s, a, b)
	if err := s.ConfigurePolicy(Policy{MaxCiphertextBytes: 16, MaxGroupMembers: 2}); err != nil {
		t.Fatal(err)
	}
	in := packet(a, b)
	in.Ciphertext = strings.Repeat("A", 17)
	if _, err := s.Post(ctx, a, id, in); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("oversized17 byte ciphertext admitted", err)
	}
	var messages, keys int
	if err := s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM messaging_message),(SELECT count(*) FROM messaging_messagekey)`).Scan(&messages, &keys); err != nil || messages != 0 || keys != 0 {
		t.Fatal("rejected oversized packet left rows", err)
	}
	in.Ciphertext = strings.Repeat("A", 16)
	message, err := s.Post(ctx, a, id, in)
	if err != nil {
		t.Fatal("ciphertext at16 byte cap refused", err)
	}
	var exists bool
	if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM messaging_message WHERE id=$1),(SELECT count(*) FROM messaging_messagekey WHERE message_id=$1)`, message).Scan(&exists, &keys); err != nil || !exists || keys != 2 {
		t.Fatal("exact member/ciphertext boundary failed storage contract", err)
	}
	if err := s.ConfigurePolicy(Policy{MaxCiphertextBytes: 8, MaxGroupMembers: 2}); err != nil {
		t.Fatal(err)
	}
	in.Ciphertext = strings.Repeat("é", 5)
	if _, err := s.Post(ctx, a, id, in); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("UTF8 byte cap counted characters", err)
	}
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM messaging_message`).Scan(&messages); err != nil || messages != 1 {
		t.Fatal("multibyte rejected packet altered stored count", err)
	}
}

func TestCasePort3MissingWrappedKeyRefusesRealWriteWithoutRows(t *testing.T) {
	s := fixture(t)
	a := fixtureUser(t, s, "case3-field-sender", "adult")
	b := fixtureUser(t, s, "case3-field-peer", "adult")
	id := casePort3ActiveDirect(t, s, a, b)
	in := packet(a, b)
	in.RecipientKeys[0].WrappedKey = ""
	if _, err := s.Post(context.Background(), a, id, in); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("missing wrapped-key field admitted", err)
	}
	var messages, keys int
	if err := s.DB.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM messaging_message),(SELECT count(*) FROM messaging_messagekey)`).Scan(&messages, &keys); err != nil || messages != 0 || keys != 0 {
		t.Fatal("missing wrapped-key packet left data rows", err)
	}
}
