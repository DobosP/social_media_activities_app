package budgets_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
)

func TestPolicyAndMissingStateFailClosed(t *testing.T) {
	for _, p := range []budgets.Policy{{}, {Limit: 10001, Window: time.Minute}, {Limit: 1, Window: time.Millisecond}, {Limit: 1, Window: 25 * time.Hour}} {
		if p.Valid() == nil {
			t.Fatal("invalid policy accepted", p)
		}
	}
	p, err := budgets.Resolve(nil, "action", budgets.Policy{Limit: 3, Window: time.Hour})
	if err != nil || p.Limit != 3 {
		t.Fatal(p, err)
	}
	if _, err = budgets.Resolve(map[string]budgets.Policy{"action": {}}, "action", p); err == nil {
		t.Fatal("explicit empty policy used default")
	}
	var store *budgets.Store
	if d, err := store.Actor(context.Background(), 1, "test.actor", p); err == nil || d.Allowed {
		t.Fatal("missing store failed open")
	}
	if d, err := store.Peer(context.Background(), []byte("short"), "192.0.2.1", "test.peer", p); err == nil || d.Allowed {
		t.Fatal("missing peer key failed open")
	}
}

func TestReservationIsBoundedAndPreservesFailure(t *testing.T) {
	for _, deny := range []bool{false, true} {
		passes, debits := 0, 0
		failure := errors.New("mutation failed")
		err := budgets.Reserve(func(reserve func() error) error {
			passes++
			if err := reserve(); err != nil {
				return err
			}
			return failure
		}, func() (budgets.Decision, error) { debits++; return budgets.Decision{Allowed: !deny}, nil })
		wantPass := 2
		if deny {
			wantPass = 1
			if !errors.Is(err, budgets.ErrDenied) {
				t.Fatal(err)
			}
		} else if !errors.Is(err, failure) {
			t.Fatal(err)
		}
		if passes != wantPass || debits != 1 {
			t.Fatal("replay or debit was unbounded", passes, debits)
		}
	}
	debits := 0
	if err := budgets.Reserve(func(func() error) error { return nil }, func() (budgets.Decision, error) { debits++; return budgets.Decision{}, nil }); err != nil || debits != 0 {
		t.Fatal("idempotent path consumed a debit")
	}
}
