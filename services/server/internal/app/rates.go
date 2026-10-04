package app

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

// Keep DRF's sliding-minute admission while bounding both identities and total
// request history. Saturation refuses new work until an existing window expires.
type requestRates struct {
	mu      sync.Mutex
	history map[string][]time.Time
	entries int
	sweep   time.Time
}

func (l *requestRates) allow(key string, limit int, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.history == nil {
		l.history = map[string][]time.Time{}
	}
	cutoff := now.Add(-time.Minute)
	if !l.sweep.After(now) {
		for id, events := range l.history {
			if len(events) == 0 || !events[len(events)-1].After(cutoff) {
				l.entries -= len(events)
				delete(l.history, id)
			}
		}
		l.sweep = now.Add(10 * time.Second)
	}
	events := l.history[key]
	n := 0
	for n < len(events) && !events[n].After(cutoff) {
		n++
	}
	l.entries -= n
	if n > 0 {
		events = append(events[:0], events[n:]...)
	}
	if len(events) == 0 {
		delete(l.history, key)
	} else {
		l.history[key] = events
	}
	if len(events) >= limit {
		l.history[key] = events
		return max(time.Second, events[0].Add(time.Minute).Sub(now))
	}
	if len(events) == 0 && len(l.history) >= 10000 || l.entries >= 1000000 {
		return time.Minute
	}
	l.history[key] = append(events, now)
	l.entries++
	return 0
}

func (a *App) admitAPI(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		return true
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	path = strings.TrimPrefix(path, "v1/")
	if path == "health" || path == "health/" || path == "ready" || path == "ready/" || path == "ops/csp-report/" {
		return true
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	key, limit := "anonymous:"+peer, a.Config.ThrottleAnonymous
	if r.Method == http.MethodPost && path == "auth/token/" {
		key, limit = "token:"+peer, a.Config.ThrottleToken
	} else if actor, ok := platform.ActorFrom(r); ok {
		key, limit = "user:"+strconv.FormatInt(actor.ID, 10), a.Config.ThrottleUser
	}
	if limit <= 0 { // safe defaults also cover manually constructed test apps.
		limit = 60
		if strings.HasPrefix(key, "user:") {
			limit = 240
		}
		if strings.HasPrefix(key, "token:") {
			limit = 10
		}
	}
	if wait := a.rates.allow(key, limit, time.Now()); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
		platform.Error(w, http.StatusTooManyRequests, "Request was throttled.")
		return false
	}
	return true
}
