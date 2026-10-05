package accounts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
)

func admissionRowCount(t *testing.T, s *Service, table string) int {
	t.Helper()
	var rows int
	if err := s.DB.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestLoginFailureTableFullNeverDeniesFreshPair(t *testing.T) {
	s, _ := loginCounterFixture(t, 10)
	ctx := context.Background()
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_go_login_failure(key_hash,epoch,failures,failure_until,touched_at)
 SELECT encode(sha256(i::text::bytea),'hex'),encode(sha256(i::text::bytea),'hex'),1,$1::timestamptz+interval '15 minutes',$1::timestamptz FROM generate_series(1,10000) i`, s.Config.Now()); err != nil {
		t.Fatal(err)
	}
	if valid, err := s.LoginFailures(ctx, "fresh-user", "198.51.100.2:1", func(context.Context) (bool, error) { return true, nil }); err != nil || !valid {
		t.Fatal("full failure table refused a pair with no failures", err)
	}
	if valid, err := s.LoginFailures(ctx, "fresh-failing-user", "198.51.100.2:2", func(context.Context) (bool, error) { return false, nil }); err != nil || valid {
		t.Fatal("full failure table refused to count a fresh pair's failure", err)
	}
}

func TestLoginPeerCapThrottlesSprayerNotOtherPeer(t *testing.T) {
	s, _ := loginCounterFixture(t, 10)
	s.RatePolicies = map[string]budgets.Policy{"auth.login": {Limit: 5, Window: 15 * time.Minute}}
	password, cookie := loginHTTPFixture(t, s)
	for i := 0; i < 5; i++ {
		if out := loginHTTP(s, cookie, false, fmt.Sprintf("sprayed-%d", i), "wrong-synthetic", fmt.Sprintf("192.0.2.70:%d", 1000+i)); out.Code != 401 {
			t.Fatal("spray attempt inside the peer cap", i, out.Code)
		}
	}
	out := loginHTTP(s, cookie, false, "sprayed-5", "wrong-synthetic", "192.0.2.70:2000")
	if out.Code != 429 {
		t.Fatal("per-prefix total-attempt cap missing", out.Code)
	}
	if retry, err := strconv.Atoi(out.Header().Get("Retry-After")); err != nil || retry < 1 || retry > 900 {
		t.Fatal("throttled API login lacks a bounded Retry-After", out.Header().Get("Retry-After"))
	}
	out = loginHTTP(s, cookie, true, "sprayed-6", "wrong-synthetic", "[::ffff:192.0.2.70]:2001")
	if out.Code != 200 || !strings.Contains(out.Body.String(), "Too many login attempts from this network") {
		t.Fatal("throttled browser login lacks the network message", out.Code)
	}
	if out := loginHTTP(s, cookie, false, "source-http-login", password, "192.0.2.71:1"); out.Code != 200 {
		t.Fatal("one sprayer throttled a different peer", out.Code)
	}
	for i := 0; i < 5; i++ {
		if out := loginHTTP(s, cookie, false, fmt.Sprintf("sprayed-v6-%d", i), "wrong-synthetic", fmt.Sprintf("[2001:db8:70:1::%x]:1", i+1)); out.Code != 401 {
			t.Fatal("IPv6 spray attempt inside the peer cap", i, out.Code)
		}
	}
	if out := loginHTTP(s, cookie, false, "sprayed-v6-x", "wrong-synthetic", "[2001:db8:70:1:ffff:ffff:ffff:fffe]:9"); out.Code != 429 {
		t.Fatal("rotating addresses inside one IPv6 /64 escaped the cap", out.Code)
	}
	if out := loginHTTP(s, cookie, false, "sprayed-v6-y", "wrong-synthetic", "[2001:db8:70:2::1]:1"); out.Code != 401 {
		t.Fatal("a different IPv6 /64 shared the cap", out.Code)
	}
	if failures := admissionRowCount(t, s, "accounts_go_login_failure"); failures != 11 {
		t.Fatal("refused attempts minted failure rows", failures)
	}
}

func TestLoginPOSTOversizePasswordCreatesNoRows(t *testing.T) {
	s, _ := loginCounterFixture(t, 10)
	_, cookie := loginHTTPFixture(t, s)
	oversize := strings.Repeat("p", 1025)
	if out := loginHTTP(s, cookie, false, "source-http-login", oversize, "192.0.2.80:1"); out.Code != 400 || !strings.Contains(out.Body.String(), "invalid login details") {
		t.Fatal("oversize API password response changed", out.Code)
	}
	if out := loginHTTP(s, cookie, true, "source-http-login", oversize, "192.0.2.80:2"); out.Code != 200 || !strings.Contains(out.Body.String(), "Please enter a correct username and password") || strings.Contains(out.Body.String(), oversize) {
		t.Fatal("oversize browser password response changed", out.Code)
	}
	for _, table := range []string{"accounts_go_login_failure", "accounts_go_login_reservation", "accounts_go_auth_attempt"} {
		if rows := admissionRowCount(t, s, table); rows != 0 {
			t.Fatal("hash-free rejection created admission state", table, rows)
		}
	}
}

func TestLoginPOSTSuccessLeavesNoFailureRow(t *testing.T) {
	s, _ := loginCounterFixture(t, 10)
	password, cookie := loginHTTPFixture(t, s)
	if out := loginHTTP(s, cookie, false, "source-http-login", password, "192.0.2.90:1"); out.Code != 200 {
		t.Fatal(out.Code)
	}
	if rows := admissionRowCount(t, s, "accounts_go_login_failure"); rows != 0 {
		t.Fatal("successful login left a failure row", rows)
	}
	for i := 0; i < 2; i++ {
		if out := loginHTTP(s, cookie, false, "source-http-login", "wrong-synthetic", "192.0.2.90:2"); out.Code != 401 {
			t.Fatal(out.Code)
		}
	}
	if rows := admissionRowCount(t, s, "accounts_go_login_failure"); rows != 1 {
		t.Fatal("failed pair was not recorded", rows)
	}
	if out := loginHTTP(s, cookie, true, "source-http-login", password, "192.0.2.90:3"); out.Code != 302 {
		t.Fatal(out.Code)
	}
	for _, table := range []string{"accounts_go_login_failure", "accounts_go_login_reservation"} {
		if rows := admissionRowCount(t, s, table); rows != 0 {
			t.Fatal("success after failures left pair state", table, rows)
		}
	}
}

func TestAuthAttemptTableFullStillAdmitsFreshPeer(t *testing.T) {
	s, _ := loginCounterFixture(t, 10)
	ctx := context.Background()
	now := s.Config.Now()
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_go_auth_attempt(key_hash,count,expires_at)
 SELECT encode(sha256(i::text::bytea),'hex'),1,$1::timestamptz+interval '1 minute' FROM generate_series(1,10000) i`, now); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "https://app.example/api/auth/signup", nil)
	r.RemoteAddr = "198.51.100.3:1"
	if allowed, err := s.Store.AllowAuthAttempt(s.WithPeerAdmission(r, AuthScopeSignup).Context(), hashState("198.51.100.3"), now); err != nil || !allowed {
		t.Fatal("full attempt table refused a fresh signup peer", err)
	}
	if allowed, err := s.Store.AllowAuthAttempt(ctx, hashState("198.51.100.4"), now); err != nil || !allowed {
		t.Fatal("full attempt table refused a fresh legacy key", err)
	}
}

func TestAuthAttemptScopesIPv6PrefixAndLogoutExempt(t *testing.T) {
	s, clock := loginCounterFixture(t, 10)
	s.Store.sweepDue = func() bool { return false }
	s.RatePolicies = map[string]budgets.Policy{AuthScopeSignup: {Limit: 3, Window: time.Hour}, AuthScopeOAuthStart: {Limit: 3, Window: 15 * time.Minute}}
	allow := func(r *http.Request) bool {
		t.Helper()
		allowed, err := s.Store.AllowAuthAttempt(r.Context(), hashState(r.RemoteAddr), clock.Now())
		if err != nil {
			t.Fatal(err)
		}
		return allowed
	}
	marked := func(address, scope string) *http.Request {
		r := httptest.NewRequest("POST", "https://app.example/api/auth/signup", nil)
		r.RemoteAddr = address
		return s.WithPeerAdmission(r, scope)
	}
	for i, address := range []string{"[2001:db8:60:1::1]:1", "[2001:db8:60:1::abcd]:2", "[2001:db8:60:1:ffff::1]:3"} {
		if !allow(marked(address, AuthScopeSignup)) {
			t.Fatal("signup inside its per-prefix cap refused", i)
		}
	}
	if allow(marked("[2001:db8:60:1:ffff::9]:4", AuthScopeSignup)) {
		t.Fatal("a second address in the same /64 escaped the signup cap")
	}
	if !allow(marked("[2001:db8:60:1::1]:5", AuthScopeOAuthStart)) {
		t.Fatal("signup cap leaked into the OAuth start scope")
	}
	if !allow(marked("[2001:db8:60:2::1]:1", AuthScopeSignup)) {
		t.Fatal("a different /64 shared the signup cap")
	}
	before := admissionRowCount(t, s, "accounts_go_auth_attempt")
	for i := 0; i < 50; i++ {
		r := httptest.NewRequest("POST", "https://app.example/api/auth/logout", nil)
		r.RemoteAddr = "192.0.2.99:1"
		if !allow(s.WithPeerAdmissionExempt(r)) {
			t.Fatal("exempt logout was throttled", i)
		}
	}
	if after := admissionRowCount(t, s, "accounts_go_auth_attempt"); after != before {
		t.Fatal("exempt logout wrote admission rows", before, after)
	}
	clock.Advance(time.Hour)
	if !allow(marked("[2001:db8:60:1::1]:6", AuthScopeSignup)) {
		t.Fatal("expired signup window kept refusing")
	}
	var count int
	if err := s.DB.QueryRow(context.Background(), `SELECT count FROM accounts_go_auth_attempt WHERE key_hash=$1`, s.Store.peerKey(AuthScopeSignup, "2001:db8:60:1::/64")).Scan(&count); err != nil || count != 1 {
		t.Fatal("expired window did not reset its count", count, err)
	}
}

func TestOAuthFlowsCappedPerPrefixNotGlobally(t *testing.T) {
	s, _ := loginCounterFixture(t, 10)
	ctx := context.Background()
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_go_oauth_flow(state_hash,data,expires_at)
 SELECT encode(sha256(i::text::bytea),'hex'),'{}'::jsonb,now()+interval '5 minutes' FROM generate_series(1,10000) i`); err != nil {
		t.Fatal(err)
	}
	flow := func(address string, n int) error {
		r := httptest.NewRequest("GET", "https://app.example/api/auth/oauth/facebook/start", nil)
		r.RemoteAddr = address
		marked := s.WithPeerAdmission(r, AuthScopeOAuthStart)
		return s.Store.CreateOAuthFlow(marked.Context(), hashState(fmt.Sprintf("synthetic-flow-%s-%d", address, n)), authcore.OAuthFlow{Provider: "facebook", ExpiresAt: time.Now().Add(5 * time.Minute)})
	}
	// The live-flow cap is the prefix's OAuth start limit: a class of thirty
	// starting together all get a flow; the thirty-first live flow is refused.
	for i := 0; i < authOAuthStartPolicy.Limit; i++ {
		if err := flow("198.51.100.60:1", i); err != nil {
			t.Fatal("live flow inside the OAuth start limit refused", i, err)
		}
	}
	if err := flow("198.51.100.60:2", 1000); !errors.Is(err, authcore.ErrConflict) {
		t.Fatal("live flow beyond the OAuth start limit admitted", err)
	}
	if err := flow("198.51.100.61:1", 0); err != nil {
		t.Fatal("one prefix's pending flows refused another prefix", err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_go_oauth_flow SET expires_at=now()-interval '1 second' WHERE peer_hash=$1`, s.Store.peerKey(authScopeOAuthFlow, "198.51.100.60")); err != nil {
		t.Fatal(err)
	}
	if err := flow("198.51.100.60:3", 1001); err != nil {
		t.Fatal("expired flows still counted against their prefix", err)
	}
	// An override of the start limit moves the flow cap with it.
	s.RatePolicies = map[string]budgets.Policy{AuthScopeOAuthStart: {Limit: 2, Window: 15 * time.Minute}}
	for i := 0; i < 2; i++ {
		if err := flow("198.51.100.62:1", i); err != nil {
			t.Fatal(err)
		}
	}
	if err := flow("198.51.100.62:1", 2); !errors.Is(err, authcore.ErrConflict) {
		t.Fatal("flow cap ignored the resolved OAuth start limit", err)
	}
}

func TestRequirePeerMarkerRefusesUnmarkedAttemptsAndFlows(t *testing.T) {
	s, _ := loginCounterFixture(t, 10)
	s.Store.RequirePeerMarker = true
	ctx := context.Background()
	now := s.Config.Now()
	if allowed, err := s.Store.AllowAuthAttempt(ctx, hashState("198.51.100.5"), now); err != nil || allowed {
		t.Fatal("unmarked attempt admitted despite the marker requirement", err)
	}
	if rows := admissionRowCount(t, s, "accounts_go_auth_attempt"); rows != 0 {
		t.Fatal("refused unmarked attempt wrote a row", rows)
	}
	flow := authcore.OAuthFlow{Provider: "facebook", ExpiresAt: time.Now().Add(5 * time.Minute)}
	if err := s.Store.CreateOAuthFlow(ctx, hashState("synthetic-unmarked-flow"), flow); !errors.Is(err, authcore.ErrConflict) {
		t.Fatal("unmarked OAuth flow admitted despite the marker requirement", err)
	}
	if rows := admissionRowCount(t, s, "accounts_go_oauth_flow"); rows != 0 {
		t.Fatal("refused unmarked OAuth flow wrote a row", rows)
	}
	r := httptest.NewRequest("POST", "https://app.example/api/auth/signup", nil)
	r.RemoteAddr = "198.51.100.5:1"
	if allowed, err := s.Store.AllowAuthAttempt(s.WithPeerAdmission(r, AuthScopeSignup).Context(), hashState("198.51.100.5"), now); err != nil || !allowed {
		t.Fatal("marked attempt refused", err)
	}
	if rows := admissionRowCount(t, s, "accounts_go_auth_attempt"); rows != 1 {
		t.Fatal("marked attempt did not charge its prefix", rows)
	}
	if allowed, err := s.Store.AllowAuthAttempt(s.WithPeerAdmissionExempt(r).Context(), hashState("198.51.100.5"), now); err != nil || !allowed {
		t.Fatal("exempt attempt refused", err)
	}
	reservation, err := s.reserveLogin(ctx, "reserved-marker", "198.51.100.5:1")
	if err != nil {
		t.Fatal(err)
	}
	if allowed, err := s.Store.AllowAuthAttempt(context.WithValue(ctx, reservedLoginContext{}, reservation), reservation.corePeer, now); err != nil || !allowed {
		t.Fatal("private login reservation refused", err)
	}
	if err := s.finishLogin(ctx, reservation, false, false); err != nil {
		t.Fatal(err)
	}
	if rows := admissionRowCount(t, s, "accounts_go_auth_attempt"); rows != 1 {
		t.Fatal("exempt or reserved attempt wrote a row", rows)
	}
	start := httptest.NewRequest("GET", "https://app.example/api/auth/oauth/facebook/start", nil)
	start.RemoteAddr = "198.51.100.5:2"
	if err := s.Store.CreateOAuthFlow(s.WithPeerAdmission(start, AuthScopeOAuthStart).Context(), hashState("synthetic-marked-flow"), flow); err != nil {
		t.Fatal("marked OAuth flow refused", err)
	}
}

func TestSweepAuthStateRemovesOnlyExpiredAndUnreserved(t *testing.T) {
	s, _ := loginCounterFixture(t, 10)
	s.Store.sweepDue = func() bool { return false }
	ctx := context.Background()
	now := s.Config.Now()
	key := func(n int) string { return fmt.Sprintf("%064x", n) }
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := s.DB.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	failure := func(n, failures int, until *time.Time, touched time.Time) {
		t.Helper()
		exec(`INSERT INTO accounts_go_login_failure(key_hash,epoch,failures,failure_until,touched_at) VALUES($1,$1,$2,$3,$4)`, key(n), failures, until, touched)
	}
	reservation := func(token, pair int, expires time.Time) {
		t.Helper()
		exec(`INSERT INTO accounts_go_login_reservation(token_hash,key_hash,epoch,expires_at) VALUES($1,$2,$2,$3)`, key(token), key(pair), expires)
	}
	stale, expired, live := now.Add(-2*time.Minute), now.Add(-time.Second), now.Add(10*time.Minute)
	// Removed: 1 (stale zero row), 2 (stale expired window), reservation 102.
	// Kept: 3 (stale but live reservation 101), 4 (live failure window).
	failure(1, 0, nil, stale)
	failure(2, 3, &expired, stale)
	failure(3, 0, nil, stale)
	failure(4, 2, &live, stale)
	reservation(101, 3, now.Add(time.Minute))
	reservation(102, 4, expired)
	exec(`INSERT INTO accounts_go_auth_attempt(key_hash,count,expires_at) VALUES($1,1,$2),($3,1,$4)`, key(201), expired, key(202), now.Add(time.Minute))
	exec(`INSERT INTO accounts_go_oauth_flow(state_hash,data,expires_at) VALUES($1,'{}'::jsonb,$2),($3,'{}'::jsonb,$4)`, key(301), now.Add(-time.Hour), key(302), now.Add(time.Hour))
	if err := s.SweepAuthState(ctx, 1000); err != nil {
		t.Fatal(err)
	}
	exists := func(table, column string, n int) bool {
		t.Helper()
		var found bool
		if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+table+` WHERE `+column+`=$1)`, key(n)).Scan(&found); err != nil {
			t.Fatal(err)
		}
		return found
	}
	for _, check := range []struct {
		table, column string
		n             int
		want          bool
	}{
		{"accounts_go_login_failure", "key_hash", 1, false},
		{"accounts_go_login_failure", "key_hash", 2, false},
		{"accounts_go_login_failure", "key_hash", 3, true},
		{"accounts_go_login_failure", "key_hash", 4, true},
		{"accounts_go_login_reservation", "token_hash", 101, true},
		{"accounts_go_login_reservation", "token_hash", 102, false},
		{"accounts_go_auth_attempt", "key_hash", 201, false},
		{"accounts_go_auth_attempt", "key_hash", 202, true},
		{"accounts_go_oauth_flow", "state_hash", 301, false},
		{"accounts_go_oauth_flow", "state_hash", 302, true},
	} {
		if got := exists(check.table, check.column, check.n); got != check.want {
			t.Fatal("sweep kept or removed the wrong row", check.table, check.n, got)
		}
	}
}
