package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func casePortMessageCount(t *testing.T, s *Service, conversation int64, want int) {
	t.Helper()
	var count int
	if err := s.DB.QueryRow(context.Background(), `SELECT count(*) FROM messaging_message WHERE conversation_id=$1`, conversation).Scan(&count); err != nil || count != want {
		t.Fatal("refused write changed ciphertext rows", err)
	}
}

func TestCasePortMessagingContactGateBothDirections(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	a := fixtureUser(t, s, "case-contact-a", "adult")
	b := fixtureUser(t, s, "case-contact-b", "adult")
	child := fixtureUser(t, s, "case-contact-child", "child")
	teen := fixtureUser(t, s, "case-contact-teen", "teen")
	unknown := fixtureUser(t, s, "case-contact-unassigned", "unassigned")
	for _, scenario := range []struct {
		name    string
		a, b    platform.Actor
		allowed bool
	}{
		{"same-cohort", a, b, true},
		{"adult-child", a, child, false},
		{"child-adult", child, a, false},
		{"teen-adult", teen, a, false},
		{"teen-child", teen, child, false},
		{"unassigned-adult", unknown, a, false},
		{"adult-unassigned", a, unknown, false},
		{"self", a, a, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if err := pair(ctx, s.DB, scenario.a, scenario.b); scenario.allowed && err != nil || !scenario.allowed && !errors.Is(err, platform.ErrForbidden) {
				t.Fatal("current pair eligibility mismatch", err)
			}
		})
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	for _, pairActors := range [][2]platform.Actor{{a, b}, {b, a}} {
		if err := pair(ctx, s.DB, pairActors[0], pairActors[1]); !errors.Is(err, platform.ErrForbidden) {
			t.Fatal("one-direction block left reverse messaging authority", err)
		}
	}
}

func TestCasePortMessagingKeyValidationRotationAndOpaqueBackup(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	a := fixtureUser(t, s, "case-key-a", "adult")
	for _, scenario := range []struct {
		name string
		key  map[string]any
	}{
		{"private-material", map[string]any{"kty": "EC", "x": "synthetic-public", "d": "synthetic-private-sentinel"}},
		{"missing-public-coordinate", map[string]any{"kty": "EC"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if _, err := s.RegisterKey(ctx, a, scenario.key, "", nil); !errors.Is(err, platform.ErrInvalid) {
				t.Fatal("invalid public-key material registered", err)
			}
			var count int
			if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM messaging_publickey WHERE user_id=$1`, a.ID).Scan(&count); err != nil || count != 0 {
				t.Fatal("invalid registration persisted key material", err)
			}
		})
	}
	backup := map[string]any{"ct": "opaque", "iv": "x"}
	if _, err := s.RegisterKey(ctx, a, jwk("first"), "", backup); err != nil {
		t.Fatal(err)
	}
	own, err := s.OwnKey(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	var storedBackup map[string]any
	if err := json.Unmarshal(own["wrapped_private_jwk"].(json.RawMessage), &storedBackup); err != nil || !reflect.DeepEqual(storedBackup, backup) {
		t.Fatal("own opaque backup was transformed or exposed incorrectly", err)
	}
	second := jwk("rotated")
	if _, err := s.RegisterKey(ctx, a, second, "", nil); err != nil {
		t.Fatal(err)
	}
	var active, total int
	var current json.RawMessage
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FILTER(WHERE active),count(*) FROM messaging_publickey WHERE user_id=$1`, a.ID).Scan(&active, &total); err != nil || active != 1 || total != 2 {
		t.Fatal("rotation did not keep exactly one of two keys active", err)
	}
	if err := s.DB.QueryRow(ctx, `SELECT public_jwk FROM messaging_publickey WHERE user_id=$1 AND active`, a.ID).Scan(&current); err != nil {
		t.Fatal(err)
	}
	var currentJWK map[string]any
	if err := json.Unmarshal(current, &currentJWK); err != nil || currentJWK["x"] != second["x"] {
		t.Fatal("rotated key was not current", err)
	}
}

func TestCasePortMessagingConsentlessMinorRefusesKeysAndContact(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	a := fixtureUser(t, s, "case-no-consent-a", "child")
	b := fixtureUser(t, s, "case-consented-b", "child")
	if _, err := s.DB.Exec(ctx, `DELETE FROM accounts_parentalconsent WHERE minor_id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterKey(ctx, a, jwk("child"), "", nil); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("consentless minor registered a key", err)
	}
	for _, pairActors := range [][2]platform.Actor{{a, b}, {b, a}} {
		if err := pair(ctx, s.DB, pairActors[0], pairActors[1]); !errors.Is(err, platform.ErrForbidden) {
			t.Fatal("consentless minor retained contact authority", err)
		}
	}
	if _, err := s.Start(ctx, a, "direct", []string{b.Username}, ""); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("consentless minor created a channel", err)
	}
	var keys, channels int
	if err := s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM messaging_publickey WHERE user_id=$1),(SELECT count(*) FROM messaging_conversation WHERE creator_id=$1)`, a.ID).Scan(&keys, &channels); err != nil || keys != 0 || channels != 0 {
		t.Fatal("denied minor admission left authority rows", err)
	}
}

func TestCasePortMessagingRevokedConsentCutsSendAndRead(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	a := fixtureUser(t, s, "case-revoke-a", "child")
	b := fixtureUser(t, s, "case-revoke-b", "child")
	id, err := s.Start(ctx, a, "direct", []string{b.Username}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, b, id, "accept"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Post(ctx, a, id, packet(a, b)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_parentalconsent SET status='revoked' WHERE minor_id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Post(ctx, a, id, packet(a, b)); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("captured consented actor wrote after revocation", err)
	}
	if _, err := s.Messages(ctx, a, id, 50, 0, 0); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("captured consented actor read after revocation", err)
	}
	casePortMessageCount(t, s, id, 1)
}

func TestCasePortMessagingStaleCohortCannotAcceptOrSend(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	for _, action := range []string{"accept", "send"} {
		t.Run(action, func(t *testing.T) {
			a := fixtureUser(t, s, "case-stale-a-"+action, "adult")
			b := fixtureUser(t, s, "case-stale-b-"+action, "adult")
			id, err := s.Start(ctx, a, "direct", []string{b.Username}, "")
			if err != nil {
				t.Fatal(err)
			}
			changed := b
			if action == "send" {
				if err := s.Transition(ctx, b, id, "accept"); err != nil {
					t.Fatal(err)
				}
				changed = a
			}
			if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET age_band='under_16',cohort='child' WHERE id=$1`, changed.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_parentalconsent(minor_id,guardian_identifier,status,scope,granted_at,expires_at,revoked_at,renewal_notice,created_at,updated_at) VALUES($1,'synthetic-case-guardian','active','',now(),now()+interval '1 year',NULL,'',now(),now())`, changed.ID); err != nil {
				t.Fatal(err)
			}
			if action == "accept" {
				if err := s.Transition(ctx, b, id, "accept"); !errors.Is(err, platform.ErrInvalid) {
					t.Fatal("captured adult accepted child into adult channel", err)
				}
				var state string
				if err := s.DB.QueryRow(ctx, `SELECT state FROM messaging_participant WHERE conversation_id=$1 AND user_id=$2`, id, b.ID).Scan(&state); err != nil || state != "invited" {
					t.Fatal("refused acceptance mutated membership", err)
				}
			} else if _, err := s.Post(ctx, a, id, packet(a, b)); !errors.Is(err, platform.ErrForbidden) {
				t.Fatal("captured adult sent after current child correction", err)
			}
			casePortMessageCount(t, s, id, 0)
		})
	}
}

func TestCasePortGuardianRevocationPrunesActualObserver(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	ward := fixtureUser(t, s, "case-observer-ward", "child")
	peer := fixtureUser(t, s, "case-observer-peer", "child")
	guardian := fixtureUser(t, s, "case-observer-guardian", "adult")
	if _, err := s.RegisterKey(ctx, guardian, jwk("guardian"), "", nil); err != nil {
		t.Fatal(err)
	}
	id, err := s.Start(ctx, ward, "group", []string{peer.Username}, "synthetic observed group")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, peer, id, "accept"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,status,relationship,consent_id,created_at,updated_at) VALUES($1,$2,'active','parent',NULL,now(),now())`, guardian.ID, ward.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AddGuardian(ctx, guardian, id); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.CanView(ctx, s.DB, guardian, id); err != nil || !ok {
		t.Fatal("linked guardian could not observe", err)
	}
	accountService := accounts.New(s.DB, nil, "synthetic", accounts.Config{})
	if err := accountService.RevokeGuardian(ctx, guardian, ward.ID, s); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := s.DB.QueryRow(ctx, `SELECT state FROM messaging_participant WHERE conversation_id=$1 AND user_id=$2`, id, guardian.ID).Scan(&state); err != nil || state != "removed" {
		t.Fatal("revoked observer row survived", err)
	}
	if ok, err := s.CanView(ctx, s.DB, guardian, id); err != nil || ok {
		t.Fatal("revoked guardianship retained observer authority", err)
	}
}

func TestCasePortCiphertextAndExactRecipientStorage(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	a := fixtureUser(t, s, "case-storage-a", "adult")
	b := fixtureUser(t, s, "case-storage-b", "adult")
	id, err := s.Start(ctx, a, "direct", []string{b.Username}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, b, id, "accept"); err != nil {
		t.Fatal(err)
	}
	message, err := s.Post(ctx, a, id, packet(a, b))
	if err != nil {
		t.Fatal(err)
	}
	var ciphertext string
	var recipients []int64
	if err := s.DB.QueryRow(ctx, `SELECT ciphertext FROM messaging_message WHERE id=$1`, message).Scan(&ciphertext); err != nil || ciphertext != "Y2lwaGVy" {
		t.Fatal("opaque ciphertext was transformed", err)
	}
	if err := s.DB.QueryRow(ctx, `SELECT array_agg(recipient_id ORDER BY recipient_id) FROM messaging_messagekey WHERE message_id=$1`, message).Scan(&recipients); err != nil || !reflect.DeepEqual(recipients, []int64{min(a.ID, b.ID), max(a.ID, b.ID)}) {
		t.Fatal("wrapped recipient set differed from active pair", fmt.Sprint(err))
	}
}
