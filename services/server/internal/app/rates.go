package app

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

type requestRates struct {
	store  *budgets.Store
	secret []byte

	// prefilter repeats database denials for anonymous/token peers; nil disables it.
	prefilter *budgets.Prefilter
	// admitPeer replaces the shared store only in tests that count admissions.
	admitPeer func(ctx context.Context, peer, scope string, policy budgets.Policy) (budgets.Decision, error)
}

func (rates requestRates) peer(ctx context.Context, peer, scope string, policy budgets.Policy) (budgets.Decision, error) {
	if rates.admitPeer != nil {
		return rates.admitPeer(ctx, peer, scope, policy)
	}
	return rates.store.Peer(ctx, rates.secret, peer, scope, policy)
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
	// Anonymous keys are per IPv4 address or IPv6 /64 (ADR-0037).
	peer := platform.PeerKey(r.RemoteAddr)
	scope, limit := "api.anonymous", a.Config.ThrottleAnonymous
	var actorID int64
	if r.Method == http.MethodPost && path == "auth/token/" {
		scope, limit = "api.token", a.Config.ThrottleToken
	} else if actor, ok := platform.ActorFrom(r); ok {
		scope, limit, actorID = "api.user", a.Config.ThrottleUser, actor.ID
	}
	if limit <= 0 { // safe defaults also cover manually constructed test apps.
		limit = 60
		if scope == "api.user" {
			limit = 240
		}
		if scope == "api.token" {
			limit = 10
		}
	}
	policy := budgets.Policy{Limit: limit, Window: time.Minute}
	var decision budgets.Decision
	var err error
	if actorID > 0 {
		decision, err = a.rates.store.Actor(r.Context(), actorID, scope, policy)
	} else if wait, denied := a.rates.prefilter.Denied(scope, peer, time.Now()); denied {
		// A denial the database already made; allowed requests always reach it.
		decision = budgets.Decision{RetryAfter: wait}
	} else {
		decision, err = a.rates.peer(r.Context(), peer, scope, policy)
		if err == nil && !decision.Allowed {
			a.rates.prefilter.Deny(scope, peer, time.Now(), decision.RetryAfter)
		}
	}
	if err != nil {
		platform.Error(w, http.StatusServiceUnavailable, "Request admission unavailable.")
		return false
	}
	if !decision.Allowed {
		wait := max(time.Second, decision.RetryAfter)
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
		platform.Error(w, http.StatusTooManyRequests, "Request was throttled.")
		return false
	}
	return true
}
