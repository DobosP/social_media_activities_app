package messaging

import (
	"context"
	"fmt"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

// Frozen source: apps/messaging/tests/test_api.py at ce3d0e3ee9f180ce2e95140300b12582db9895d6.
// Source SHA-256: 262aec1e28054306e78980422d459ce828594f302effee7d8f33dba4ca50310a.

// Original case: apps/messaging/tests/test_api.py::test_v1_conversation_list_query_count_is_constant, line195.
func TestMessagingSourceCaseV1ConversationListQueryCeilingFive(t *testing.T) {
	s := fixture(t)
	db, trace := testdb.TracedPool(t, s.DB)
	s.DB = db
	s.ConversationLimit = 20
	ctx := context.Background()
	a := fixtureUser(t, s, "source-list-query-owner", "adult")
	for i := 0; i < 8; i++ {
		partner := fixtureUser(t, s, fmt.Sprintf("source-list-query-partner-%d", i), "adult")
		if _, err := s.Start(ctx, a, "direct", []string{partner.Username}, ""); err != nil {
			t.Fatal(err)
		}
	}

	trace.Reset()
	response := call(s, a, "GET", "/api/v1/messaging/conversations/?limit=6", nil)
	queries := trace.Count()
	t.Logf("original V1 list: 8 conversations, limit6, actualqueries=%d, sourceceiling=5", queries)
	if queries > 5 {
		t.Fatalf("original V1 conversation list query ceiling violated: %d>5", queries)
	}
	if response.Code != 200 {
		t.Fatalf("V1 conversation list status=%d, want200", response.Code)
	}
	rows, ok := object(t, response)["results"].([]any)
	if !ok || len(rows) != 6 {
		t.Fatalf("V1 conversation list results=%d, want6", len(rows))
	}
}

// Original case: apps/messaging/tests/test_api.py::test_v1_message_history_query_count_is_constant, line257.
func TestMessagingSourceCaseV1MessageHistoryQueryCeilingSeven(t *testing.T) {
	s := fixture(t)
	db, trace := testdb.TracedPool(t, s.DB)
	s.DB = db
	s.MessagePageLimit = 20
	ctx := context.Background()
	a := fixtureUser(t, s, "source-history-query-sender", "adult")
	b := fixtureUser(t, s, "source-history-query-recipient", "adult")
	conversation, err := s.Start(ctx, a, "direct", []string{b.Username}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, b, conversation, "accept"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		input := packet(a, b)
		input.Ciphertext = fmt.Sprintf("Y2lwaGVyq%d", i)
		if _, err := s.Post(ctx, a, conversation, input); err != nil {
			t.Fatal(err)
		}
	}

	trace.Reset()
	response := call(s, b, "GET", fmt.Sprintf("/api/v1/messaging/conversations/%d/messages/?limit=10", conversation), nil)
	queries := trace.Count()
	t.Logf("original V1 history: 12 complete packets, limit10, actualqueries=%d, sourceceiling=7", queries)
	if queries > 7 {
		t.Fatalf("original V1 message history query ceiling violated: %d>7", queries)
	}
	if response.Code != 200 {
		t.Fatalf("V1 message history status=%d, want200", response.Code)
	}
	rows, ok := object(t, response)["results"].([]any)
	if !ok || len(rows) != 10 {
		t.Fatalf("V1 message history results=%d, want10", len(rows))
	}
}
