package chat

import (
	"encoding/json"
	"sync"
	"time"
)

// typingWindow is the process-wide coalescing window per (room, actor). It
// matches the per-connection typing interval and the browser's 2.5 s cadence.
const typingWindow = 2 * time.Second

type frameDecision int

const (
	frameAccept frameDecision = iota
	frameDrop
	frameAbuse
)

// frameLimiter meters one socket's inbound frames before any authorization or
// domain work, so a dropped frame costs no database round trip. Every accepted
// frame spends a token from a bucket refilled once per FrameInterval up to
// FrameBurst; typing is also limited to one per TypingInterval. Excess typing is
// dropped silently and spends no token, so a typing flood cannot starve the
// sender's own messages. An ordinary frame on an empty bucket is abuse. The
// read loop owns the limiter; it is not safe for concurrent use.
type frameLimiter struct {
	now                   func() time.Time
	interval, typingEvery time.Duration
	burst, tokens         float64
	last, lastTyping      time.Time
}

func newFrameLimiter(c Config, now func() time.Time) *frameLimiter {
	return &frameLimiter{now: now, interval: c.FrameInterval, typingEvery: c.TypingInterval, burst: float64(c.FrameBurst), tokens: float64(c.FrameBurst), last: now()}
}
func (l *frameLimiter) allow(typing bool) frameDecision {
	now := l.now()
	if typing && !l.lastTyping.IsZero() && now.Sub(l.lastTyping) < l.typingEvery {
		return frameDrop
	}
	if now.After(l.last) {
		refill := float64(now.Sub(l.last)) / float64(l.interval)
		l.tokens = min(l.burst, l.tokens+refill)
		l.last = now
	}
	if l.tokens < 1 {
		if typing {
			return frameDrop
		}
		return frameAbuse
	}
	l.tokens--
	if typing {
		l.lastTyping = now
	}
	return frameAccept
}

// typingFrame classifies a frame without trusting it. It decodes with the same
// encoding/json field rules as the adapters; anything that is not a typing
// object is metered as an ordinary frame, the stricter path.
func typingFrame(raw []byte) bool {
	var head struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(raw, &head) == nil && head.Type == "typing"
}

type typingKey struct{ room, actor int64 }

// typingCoalescer suppresses a typing publish for a (room, actor) pair already
// attempted within the window anywhere in this process, which also covers one
// actor's several sockets. It holds at most limit entries: expired entries are
// evicted first, then the oldest, so a new actor is never refused for space.
type typingCoalescer struct {
	mu     sync.Mutex
	now    func() time.Time
	window time.Duration
	limit  int
	seen   map[typingKey]time.Time
}

func newTypingCoalescer(window time.Duration, limit int) *typingCoalescer {
	return &typingCoalescer{now: time.Now, window: window, limit: limit, seen: map[typingKey]time.Time{}}
}
func (c *typingCoalescer) allow(room, actor int64) bool {
	if c == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	key := typingKey{room: room, actor: actor}
	at, ok := c.seen[key]
	if ok && now.Sub(at) < c.window {
		return false
	}
	if !ok && len(c.seen) >= c.limit {
		var oldest typingKey
		var oldestAt time.Time
		found := false
		for k, seenAt := range c.seen {
			if now.Sub(seenAt) >= c.window {
				delete(c.seen, k)
			} else if !found || seenAt.Before(oldestAt) {
				oldest, oldestAt, found = k, seenAt, true
			}
		}
		if found && len(c.seen) >= c.limit {
			delete(c.seen, oldest)
		}
	}
	c.seen[key] = now
	return true
}
