package budgets

import (
	"hash/maphash"
	"sync"
	"time"
)

const (
	prefilterSlots  = 1 << 16
	prefilterShards = 64
)

// Prefilter repeats recent database denials without another round trip. It is
// a fixed, direct-mapped table: a colliding key overwrites the slot, so it is
// bounded and never refuses on its own. It caches denials only; allowed
// requests always reach PostgreSQL, which stays the authority, and a replica
// can only repeat a denial the database already made until its Retry-After
// passes. Keys are hashed in memory with a per-process seed and never logged.
type Prefilter struct {
	seed   maphash.Seed
	shards [prefilterShards]prefilterShard
}

type prefilterShard struct {
	mu    sync.Mutex
	slots [prefilterSlots / prefilterShards]prefilterSlot
}

type prefilterSlot struct {
	keyHash     uint64
	deniedUntil time.Time
}

func NewPrefilter() *Prefilter { return &Prefilter{seed: maphash.MakeSeed()} }

// slot returns the key's hash and its direct-mapped slot index.
func (p *Prefilter) slot(scope, key string) (uint64, uint64) {
	h := maphash.String(p.seed, scope+"\x00"+key)
	return h, h % prefilterSlots
}

func (p *Prefilter) locate(scope, key string) (*prefilterShard, *prefilterSlot, uint64) {
	h, i := p.slot(scope, key)
	shard := &p.shards[i%prefilterShards]
	return shard, &shard.slots[i/prefilterShards], h
}

// Denied reports a live cached denial and its remaining Retry-After.
func (p *Prefilter) Denied(scope, key string, now time.Time) (time.Duration, bool) {
	if p == nil {
		return 0, false
	}
	shard, slot, h := p.locate(scope, key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	if slot.keyHash != h || !now.Before(slot.deniedUntil) {
		return 0, false
	}
	return slot.deniedUntil.Sub(now), true
}

// Deny records a database denial until its Retry-After passes.
func (p *Prefilter) Deny(scope, key string, now time.Time, retry time.Duration) {
	if p == nil || retry <= 0 {
		return
	}
	shard, slot, h := p.locate(scope, key)
	shard.mu.Lock()
	slot.keyHash, slot.deniedUntil = h, now.Add(retry)
	shard.mu.Unlock()
}
