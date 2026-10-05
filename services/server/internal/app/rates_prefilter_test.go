package app

import (
	"context"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
)

func TestThrottledPeerIsAnsweredFromPrefilterWithoutDatabase(t *testing.T) {
	admissions := map[string]int{}
	a := &App{rates: requestRates{prefilter: budgets.NewPrefilter(), admitPeer: func(_ context.Context, peer, scope string, _ budgets.Policy) (budgets.Decision, error) {
		admissions[scope+" "+peer]++
		// The synthetic database admits each key once, then denies for 30 seconds.
		if admissions[scope+" "+peer] > 1 {
			return budgets.Decision{RetryAfter: 30 * time.Second}, nil
		}
		return budgets.Decision{Allowed: true}, nil
	}}}
	request := func(method, path, remote string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.RemoteAddr = remote
		w := httptest.NewRecorder()
		a.admitAPI(w, r)
		return w
	}
	if w := request("GET", "/api/places/", "192.0.2.7:1000"); w.Code != 200 {
		t.Fatal("first request", w.Code)
	}
	if w := request("GET", "/api/places/", "192.0.2.7:1001"); w.Code != 429 || w.Header().Get("Retry-After") != "30" {
		t.Fatal("database denial", w.Code, w.Header().Get("Retry-After"))
	}
	for i := 0; i < 5; i++ {
		w := request("GET", "/api/v1/places/", "192.0.2.7:"+strconv.Itoa(2000+i))
		retry, _ := strconv.Atoi(w.Header().Get("Retry-After"))
		if w.Code != 429 || retry < 1 || retry > 30 {
			t.Fatal("cached denial", i, w.Code, retry)
		}
	}
	if got := admissions["api.anonymous 192.0.2.7"]; got != 2 {
		t.Fatal("throttled peer reached the database again", got)
	}
	// Other peers and the token scope still reach the database, which stays
	// the only authority that admits.
	if w := request("GET", "/api/places/", "192.0.2.8:1000"); w.Code != 200 || admissions["api.anonymous 192.0.2.8"] != 1 {
		t.Fatal("allowed peer did not reach the database", w.Code)
	}
	if w := request("POST", "/api/auth/token/", "192.0.2.7:3000"); w.Code != 200 || admissions["api.token 192.0.2.7"] != 1 {
		t.Fatal("token scope shared the anonymous denial", w.Code)
	}
}
