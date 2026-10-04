package app

import (
	"math"
	"net"
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
	if actorID > 0 {
		decision, err = a.rates.store.Actor(r.Context(), actorID, scope, policy)
	} else {
		decision, err = a.rates.store.Peer(r.Context(), a.rates.secret, peer, scope, policy)
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
