package messaging

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestPolicyTightensCiphertextAndRecipientAdmission(t *testing.T) {
	s := New(nil, platform.CursorCodec{})
	p := Policy{MaxCiphertextBytes: 8, MaxGroupMembers: 2}
	if err := s.ConfigurePolicy(p); err != nil {
		t.Fatal(err)
	}
	in := MessageInput{Ciphertext: "12345678", IV: "synthetic", RecipientKeys: make([]RecipientKey, 2)}
	if err := validateInputBounded(&in, s.maxMembers(), s.maxCiphertext()); err != nil || in.Algorithm != "AES-GCM-256" {
		t.Fatal("boundary ciphertext rejected", err)
	}
	in.Ciphertext += "9"
	if _, err := s.Post(context.Background(), platform.Actor{}, 1, in); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("oversized ciphertext reached database", err)
	}
	in.Ciphertext = "12345678"
	in.RecipientKeys = make([]RecipientKey, 3)
	if _, err := s.Post(context.Background(), platform.Actor{}, 1, in); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("oversized recipient set reached database", err)
	}
	// The byte budget counts UTF-8 bytes, preserving the source wire contract.
	in.Ciphertext = strings.Repeat("é", 5)
	in.RecipientKeys = nil
	if err := validateInputBounded(&in, s.maxMembers(), s.maxCiphertext()); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("ciphertext budget counted runes", err)
	}
}

func TestPolicyRejectsUnsafeCapsWithoutMutatingService(t *testing.T) {
	s := New(nil, platform.CursorCodec{})
	d := DefaultPolicy()
	if err := d.Validate(); err != nil || (Policy{}).WithDefaults() != d {
		t.Fatal("source defaults drifted", err)
	}
	for _, p := range []Policy{{0, 256}, {-1, 256}, {65537, 256}, {65536, 1}, {65536, 257}} {
		if s.ConfigurePolicy(p) == nil {
			t.Fatal("unsafe cap accepted", p)
		}
		if s.maxMembers() != 256 || s.maxCiphertext() != 65536 {
			t.Fatal("failed configuration mutated live caps")
		}
	}
}

func TestPostgresPolicyGroupAndCiphertextCaps(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	a := fixtureUser(t, s, "policy-sender", "adult")
	b := fixtureUser(t, s, "policy-recipient", "adult")
	c := fixtureUser(t, s, "policy-third", "adult")
	if err := s.ConfigurePolicy(Policy{MaxCiphertextBytes: 8, MaxGroupMembers: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(ctx, a, "group", []string{b.Username, c.Username}, "Synthetic bounded group"); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("configured group cap ignored at creation", err)
	}
	conversation, err := s.Start(ctx, a, "group", []string{b.Username}, "Synthetic bounded group")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddParticipant(ctx, a, conversation, c.Username); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("configured group cap ignored at invitation", err)
	}
	if err := s.Transition(ctx, b, conversation, "accept"); err != nil {
		t.Fatal(err)
	}
	in := packet(a, b)
	if _, err := s.Post(ctx, a, conversation, in); err != nil {
		t.Fatal("boundary ciphertext not stored", err)
	}
	in.Ciphertext += "9"
	if _, err := s.Post(ctx, a, conversation, in); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("configured ciphertext cap ignored", err)
	}
	var count int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM messaging_message WHERE conversation_id=$1`, conversation).Scan(&count); err != nil || count != 1 {
		t.Fatal("rejected message persisted", count, err)
	}
}
