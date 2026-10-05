package messaging

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

// GO-PRIV-08: a v1 forward (after=) page holding more than limit unseen
// messages keeps the oldest ones, so polling with after=<last id> skips none.
func TestPostgresV1AfterHistoryKeepsOldestPendingMessage(t *testing.T) {
	s := fixture(t)
	a := fixtureUser(t, s, "after-history-owner", "adult")
	b := fixtureUser(t, s, "after-history-peer", "adult")
	id, err := s.Start(context.Background(), a, "direct", []string{b.Username}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(context.Background(), b, id, "accept"); err != nil {
		t.Fatal(err)
	}
	var sent []int64
	for i := 0; i < 9; i++ {
		in := packet(a, b)
		in.Ciphertext = fmt.Sprintf("QWZ0ZXI%d", i)
		message, err := s.Post(context.Background(), a, id, in)
		if err != nil {
			t.Fatal(err)
		}
		sent = append(sent, message)
	}
	path := fmt.Sprintf("/api/v1/messaging/conversations/%d/messages/", id)
	page := func(viewer platform.Actor, after int64) []string {
		t.Helper()
		w := call(s, viewer, "GET", fmt.Sprintf("%s?after=%d&limit=3", path, after), nil)
		if w.Code != 200 {
			t.Fatal("forward history status", w.Code)
		}
		var out []string
		for _, row := range object(t, w)["results"].([]any) {
			out = append(out, row.(map[string]any)["ciphertext"].(string))
		}
		return out
	}
	if got := page(b, sent[0]); !reflect.DeepEqual(got, []string{"QWZ0ZXI1", "QWZ0ZXI2", "QWZ0ZXI3"}) {
		t.Fatalf("forward page dropped the oldest pending message: %v", got)
	}
	if got := page(b, sent[3]); !reflect.DeepEqual(got, []string{"QWZ0ZXI4", "QWZ0ZXI5", "QWZ0ZXI6"}) {
		t.Fatalf("forward continuation skipped a message: %v", got)
	}
	if got := page(b, sent[6]); !reflect.DeepEqual(got, []string{"QWZ0ZXI7", "QWZ0ZXI8"}) {
		t.Fatalf("final forward page: %v", got)
	}
}
