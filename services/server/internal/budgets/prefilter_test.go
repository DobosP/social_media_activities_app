package budgets

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestPrefilterServesDenialUntilExpiry(t *testing.T) {
	p := NewPrefilter()
	now := time.Unix(1_800_000_000, 0)
	if _, denied := p.Denied("api.anonymous", "192.0.2.1", now); denied {
		t.Fatal("empty prefilter refused")
	}
	p.Deny("api.anonymous", "192.0.2.1", now, 30*time.Second)
	if wait, denied := p.Denied("api.anonymous", "192.0.2.1", now.Add(10*time.Second)); !denied || wait != 20*time.Second {
		t.Fatal("live denial not served", wait, denied)
	}
	if _, denied := p.Denied("api.token", "192.0.2.1", now); denied {
		t.Fatal("denial crossed scopes")
	}
	if _, denied := p.Denied("api.anonymous", "192.0.2.1", now.Add(30*time.Second)); denied {
		t.Fatal("expired denial still served")
	}
	p.Deny("api.anonymous", "192.0.2.2", now, 0)
	if _, denied := p.Denied("api.anonymous", "192.0.2.2", now); denied {
		t.Fatal("non-denial cached")
	}
	var missing *Prefilter
	missing.Deny("api.anonymous", "192.0.2.1", now, time.Minute)
	if _, denied := missing.Denied("api.anonymous", "192.0.2.1", now); denied {
		t.Fatal("nil prefilter refused")
	}
}

func TestPrefilterCollisionOverwritesWithoutRefusing(t *testing.T) {
	p := NewPrefilter()
	now := time.Unix(1_800_000_000, 0)
	first := "198.51.100.0"
	firstHash, firstSlot := p.slot("api.anonymous", first)
	second := ""
	for i := 1; second == "" && i < 1<<24; i++ {
		candidate := "198.51.100." + strconv.Itoa(i)
		if hash, slot := p.slot("api.anonymous", candidate); slot == firstSlot && hash != firstHash {
			second = candidate
		}
	}
	if second == "" {
		t.Fatal("no colliding synthetic key found")
	}
	p.Deny("api.anonymous", first, now, time.Minute)
	if _, denied := p.Denied("api.anonymous", second, now); denied {
		t.Fatal("colliding key refused on its own")
	}
	p.Deny("api.anonymous", second, now, time.Minute)
	if _, denied := p.Denied("api.anonymous", first, now); denied {
		t.Fatal("overwritten denial still served")
	}
	if _, denied := p.Denied("api.anonymous", second, now); !denied {
		t.Fatal("overwriting denial not served")
	}
}

func TestPrefilterConcurrentUse(t *testing.T) {
	p := NewPrefilter()
	now := time.Now()
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				key := "192.0.2." + strconv.Itoa((offset+i)%500)
				if i%3 == 0 {
					p.Deny("api.anonymous", key, now, time.Minute)
				} else {
					p.Denied("api.anonymous", key, now)
				}
			}
		}(g)
	}
	wg.Wait()
	if _, denied := p.Denied("api.anonymous", "192.0.2.0", now.Add(time.Minute)); denied {
		t.Fatal("expired concurrent denial still served")
	}
}
