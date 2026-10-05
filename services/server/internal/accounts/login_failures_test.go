package accounts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/jackc/pgx/v5"
)

type loginTestClock struct{ nanos atomic.Int64 }

func (c *loginTestClock) Now() time.Time          { return time.Unix(0, c.nanos.Load()).UTC() }
func (c *loginTestClock) Advance(d time.Duration) { c.nanos.Add(int64(d)) }

func loginCounterFixture(t *testing.T, limit int) (*Service, *loginTestClock) {
	t.Helper()
	s := accountFixture(t)
	clock := &loginTestClock{}
	clock.nanos.Store(time.Now().UTC().Truncate(time.Microsecond).UnixNano())
	s.Config.Now = clock.Now
	s.Config.LoginFailureLimit = limit
	s.Config.LoginFailureWindow = 15 * time.Minute
	return s, clock
}

func loginSnapshot(t *testing.T, s *Service, name, peer string) (int, int, *time.Time, string) {
	t.Helper()
	key, err := s.loginFailureKey(name, peer)
	if err != nil {
		t.Fatal(err)
	}
	var failures, pending int
	var expiry *time.Time
	var epoch string
	err = s.DB.QueryRow(context.Background(), `SELECT failures,failure_until,epoch,(SELECT count(*) FROM accounts_go_login_reservation r WHERE r.key_hash=b.key_hash AND r.expires_at>$2) FROM accounts_go_login_failure b WHERE key_hash=$1`, key, s.Config.Now()).Scan(&failures, &expiry, &epoch, &pending)
	if errors.Is(err, pgx.ErrNoRows) {
		// Successful, aborted and infrastructure-failed attempts leave no row.
		return 0, 0, nil, ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return failures, pending, expiry, epoch
}

func TestLoginFailuresFixedFirstFailureWindowPairNormalizationAndReplicaReset(t *testing.T) {
	s, clock := loginCounterFixture(t, 3)
	replica := New(s.DB, s.Auth, string(s.Secret), s.Config)
	ctx := context.Background()
	first := clock.Now()
	for index, peer := range []string{"192.0.2.9:1000", "192.0.2.9:2000", "[::ffff:192.0.2.9]:3000"} {
		name := "Source-LOGIN"
		if index == 1 {
			name = "source-login"
		}
		service := s
		if index == 1 {
			service = replica
		}
		if valid, err := service.LoginFailures(ctx, name, peer, func(context.Context) (bool, error) { return false, nil }); err != nil || valid {
			t.Fatal("known invalid attempt was not recorded", err)
		}
		failures, pending, expiry, _ := loginSnapshot(t, s, "source-login", "192.0.2.9:4000")
		if failures != index+1 || pending != 0 || expiry == nil || !expiry.Equal(first.Add(15*time.Minute)) {
			t.Fatal("failure window extended, pair split, or slot leaked")
		}
		clock.Advance(time.Second)
	}
	called := false
	if _, err := replica.LoginFailures(ctx, "SOURCE-LOGIN", "192.0.2.9:9000", func(context.Context) (bool, error) { called = true; return true, nil }); !errors.Is(err, ErrLoginFailureLimit) || called {
		t.Fatal("locked pair reached credential verification", err)
	}
	if valid, err := s.LoginFailures(ctx, "other-login", "192.0.2.9:9000", func(context.Context) (bool, error) { return true, nil }); err != nil || !valid {
		t.Fatal("failure counter incorrectly covered all usernames", err)
	}
	if valid, err := s.LoginFailures(ctx, "source-login", "192.0.2.10:9000", func(context.Context) (bool, error) { return true, nil }); err != nil || !valid {
		t.Fatal("failure counter incorrectly covered all peers", err)
	}
	clock.Advance(15 * time.Minute)
	if valid, err := replica.LoginFailures(ctx, "source-login", "192.0.2.9:1", func(context.Context) (bool, error) { return true, nil }); err != nil || !valid {
		t.Fatal("expired fixed window remained locked", err)
	}
}

func TestLoginFailuresSuccessCountsOnlyFailuresAndCancellationFinalizes(t *testing.T) {
	s, _ := loginCounterFixture(t, 10)
	ctx := context.Background()
	for i := 0; i < 13; i++ {
		if valid, err := s.LoginFailures(ctx, "successful", "198.51.100.1:1", func(context.Context) (bool, error) { return true, nil }); err != nil || !valid {
			t.Fatal("successes spent failure quota", err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := s.LoginFailures(ctx, "successful", "198.51.100.1:1", func(context.Context) (bool, error) { return false, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if valid, err := s.LoginFailures(ctx, "successful", "198.51.100.1:1", func(context.Context) (bool, error) { return true, nil }); err != nil || !valid {
		t.Fatal(err)
	}
	failed, pending, expiry, _ := loginSnapshot(t, s, "successful", "198.51.100.1:1")
	if failed != 0 || pending != 0 || expiry != nil {
		t.Fatal("success failed to reset failure window")
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	if _, err := s.LoginFailures(cancelCtx, "cancelled-proof", "198.51.100.1:1", func(context.Context) (bool, error) { cancel(); return false, nil }); err != nil {
		t.Fatal("request cancellation suppressed known failure", err)
	}
	failed, pending, _, _ = loginSnapshot(t, s, "cancelled-proof", "198.51.100.1:1")
	if failed != 1 || pending != 0 {
		t.Fatal("cancelled caller stranded slot or lost failure")
	}
	infra := errors.New("synthetic verifier unavailable")
	if _, err := s.LoginFailures(ctx, "infrastructure", "198.51.100.1:1", func(context.Context) (bool, error) { return false, infra }); !errors.Is(err, infra) {
		t.Fatal(err)
	}
	failed, pending, expiry, _ = loginSnapshot(t, s, "infrastructure", "198.51.100.1:1")
	if failed != 0 || pending != 0 || expiry != nil {
		t.Fatal("infrastructure error counted as failed credentials")
	}
}

func TestLoginFailuresOldEpochCannotClearOrCountFreshFailures(t *testing.T) {
	s, _ := loginCounterFixture(t, 5)
	ctx := context.Background()
	name, peer := "epoch-proof", "203.0.113.1:1"
	if _, err := s.LoginFailures(ctx, name, peer, func(context.Context) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
	oldSuccess, err := s.reserveLogin(ctx, name, peer)
	if err != nil {
		t.Fatal(err)
	}
	oldFailure, err := s.reserveLogin(ctx, name, peer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoginFailures(ctx, name, peer, func(context.Context) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	failed, pending, _, newEpoch := loginSnapshot(t, s, name, peer)
	if failed != 0 || pending != 2 || newEpoch == oldSuccess.epoch {
		t.Fatal("success dropped old live slots or did not fence old epoch")
	}
	if _, err := s.LoginFailures(ctx, name, peer, func(context.Context) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
	_, _, until, _ := loginSnapshot(t, s, name, peer)
	if err := s.finishLogin(ctx, oldSuccess, true, false); err != nil {
		t.Fatal(err)
	}
	if err := s.finishLogin(ctx, oldFailure, false, true); err != nil {
		t.Fatal(err)
	}
	failed, pending, expiry, epoch := loginSnapshot(t, s, name, peer)
	if failed != 1 || pending != 0 || epoch != newEpoch || expiry == nil || !expiry.Equal(*until) {
		t.Fatal("old completion cleared or incremented fresh epoch")
	}
}

func TestLoginFailuresConcurrentReplicaReservationsBoundCredentialBurst(t *testing.T) {
	s, _ := loginCounterFixture(t, 10)
	replica := New(s.DB, s.Auth, string(s.Secret), s.Config)
	gate := make(chan struct{})
	started := make(chan struct{}, 20)
	results := make(chan error, 20)
	var wg sync.WaitGroup
	var release sync.Once
	defer func() { release.Do(func() { close(gate) }); wg.Wait() }()
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			service := s
			if index%2 == 1 {
				service = replica
			}
			_, err := service.LoginFailures(context.Background(), "concurrent-login", "192.0.2.22:1", func(context.Context) (bool, error) { started <- struct{}{}; <-gate; return false, nil })
			results <- err
		}(i)
	}
	for i := 0; i < 10; i++ {
		select {
		case <-started:
		case <-time.After(10 * time.Second):
			t.Fatal("expected credential reservation did not start")
		}
	}
	for i := 0; i < 10; i++ {
		select {
		case err := <-results:
			if !errors.Is(err, ErrLoginFailureLimit) {
				t.Fatal("extra reservation bypassed durable capacity", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("excess reservations failed to close")
		}
	}
	failed, pending, _, _ := loginSnapshot(t, s, "concurrent-login", "192.0.2.22:2")
	if failed != 0 || pending != 10 {
		t.Fatal("pending credential slots did not bound burst")
	}
	release.Do(func() { close(gate) })
	wg.Wait()
	for i := 0; i < 10; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	failed, pending, _, _ = loginSnapshot(t, s, "concurrent-login", "192.0.2.22:2")
	if failed != 10 || pending != 0 {
		t.Fatal("concurrent failed completions lost count or leaked slots")
	}
}

func TestLoginFailuresExpiredSlotAndFinalizationDatabaseErrorFailClosed(t *testing.T) {
	s, clock := loginCounterFixture(t, 2)
	ctx := context.Background()
	r, err := s.reserveLogin(ctx, "expired-slot", "203.0.113.2:1")
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(loginReservationLease + time.Second)
	if err := s.finishLogin(ctx, r, true, false); err == nil {
		t.Fatal("expired reservation granted completion")
	}
	if _, err := s.LoginFailures(ctx, "expired-slot", "203.0.113.2:1", func(context.Context) (bool, error) { return false, nil }); err != nil {
		t.Fatal("expired slot prevented safe new admission", err)
	}
	if _, err := s.DB.Exec(ctx, `ALTER TABLE accounts_go_login_failure ADD CONSTRAINT fixture_login_finalize_failure CHECK(failures=0) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	r, err = s.reserveLogin(ctx, "finalization-failure", "203.0.113.2:1")
	if err != nil {
		t.Fatal(err)
	}
	if valid, err := s.runLoginReservation(ctx, r, func(context.Context) (bool, error) { return false, nil }); err == nil || valid {
		t.Fatal("failed counter commit claimed credential outcome")
	}
	failed, pending, _, _ := loginSnapshot(t, s, "finalization-failure", "203.0.113.2:1")
	if failed != 0 || pending != 1 {
		t.Fatal("failed transaction did not retain unconfirmed slot")
	}
	if _, err := s.DB.Exec(ctx, `ALTER TABLE accounts_go_login_failure DROP CONSTRAINT fixture_login_finalize_failure`); err != nil {
		t.Fatal(err)
	}
}

func TestLoginFailureReservedContextIsExactPrivateAndDurable(t *testing.T) {
	s, _ := loginCounterFixture(t, 10)
	ctx := context.Background()
	if _, err := s.LoginFailures(ctx, "public-helper", "192.0.2.44:1", func(callback context.Context) (bool, error) {
		if callback.Value(reservedLoginContext{}) != nil {
			t.Fatal("public credential helper granted authcore bypass")
		}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	r, err := s.reserveLogin(ctx, "reserved", "192.0.2.44:1")
	if err != nil {
		t.Fatal(err)
	}
	reserved := context.WithValue(ctx, reservedLoginContext{}, r)
	if allowed, err := s.Store.AllowAuthAttempt(reserved, r.corePeer, s.Config.Now()); err != nil || !allowed {
		t.Fatal("real private reservation did not permit pinned login", err)
	}
	if allowed, err := s.Store.AllowAuthAttempt(reserved, r.corePeer, s.Config.Now()); err == nil || allowed {
		t.Fatal("reserved context was reusable")
	}
	if err := s.finishLogin(ctx, r, false, false); err != nil {
		t.Fatal(err)
	}
	fake := &loginReservation{db: s.DB, now: s.Config.Now, key: r.key, epoch: r.epoch, token: r.token, corePeer: r.corePeer}
	if allowed, err := s.Store.AllowAuthAttempt(context.WithValue(ctx, reservedLoginContext{}, fake), fake.corePeer, s.Config.Now()); err == nil || allowed {
		t.Fatal("missing durable slot granted private bypass")
	}
	var rows int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM accounts_go_auth_attempt`).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("private reserved login charged unrelated authcore limiter", err)
	}
}

func loginHTTPFixture(t *testing.T, s *Service) (string, *http.Cookie) {
	t.Helper()
	a := accountUser(t, s, "source-http-login", "adult", "adult")
	const password = "Synthetic-login-test-password-9236"
	hash, err := authcore.HashPassword(context.Background(), password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(context.Background(), `UPDATE accounts_user SET password=$2 WHERE id=$1`, a.ID, hash); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "https://app.example/login/", nil)
	s.Auth.LoginPage("Synthetic test", "/").ServeHTTP(w, r)
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "csrftoken" {
			return password, cookie
		}
	}
	t.Fatal("pinned authcore did not supply fixture CSRF")
	return "", nil
}

func loginHTTP(s *Service, cookie *http.Cookie, browser bool, name, password, peer string) *httptest.ResponseRecorder {
	path, body, kind := "/api/auth/login", fmt.Sprintf(`{"username":%q,"password":%q}`, name, password), "application/json"
	if browser {
		path = "/login/"
		body = url.Values{"username": {name}, "password": {password}, "csrfmiddlewaretoken": {cookie.Value}, "next": {"/profile/"}}.Encode()
		kind = "application/x-www-form-urlencoded"
	}
	r := httptest.NewRequest("POST", "https://app.example"+path, strings.NewReader(body))
	r.RemoteAddr = peer
	r.Header.Set("Content-Type", kind)
	r.Header.Set("Origin", "https://app.example")
	r.Header.Set("X-CSRFToken", cookie.Value)
	r.Header.Set("X-Forwarded-For", "198.51.100.254")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.LoginPOST(w, r, browser)
	return w
}

func TestLoginPOSTBrowserAndAPIShareFailuresAndPreserveSourceResponse(t *testing.T) {
	s, _ := loginCounterFixture(t, 2)
	_, cookie := loginHTTPFixture(t, s)
	if out := loginHTTP(s, cookie, false, "SOURCE-HTTP-LOGIN", "wrong-synthetic", "192.0.2.55:1"); out.Code != 401 {
		t.Fatal("API invalid credential status changed", out.Code)
	}
	if out := loginHTTP(s, cookie, true, "source-http-login", "wrong-synthetic", "192.0.2.55:2"); out.Code != 200 || !strings.Contains(out.Body.String(), "Please enter a correct username and password") || strings.Contains(out.Body.String(), "wrong-synthetic") {
		t.Fatal("browser invalid form status/privacy changed", out.Code)
	}
	if out := loginHTTP(s, cookie, false, "source-http-login", "wrong-synthetic", "192.0.2.55:3"); out.Code != 429 {
		t.Fatal("shared failure pair did not lock API", out.Code)
	}
	if out := loginHTTP(s, cookie, true, "source-http-login", "wrong-synthetic", "[::ffff:192.0.2.55]:4"); out.Code != 200 || !strings.Contains(out.Body.String(), LoginTooManyFailures) {
		t.Fatal("source lockout form/message missing", out.Code)
	}
	failed, pending, _, _ := loginSnapshot(t, s, "source-http-login", "192.0.2.55:5")
	if failed != 2 || pending != 0 {
		t.Fatal("lockout counted itself or leaked slot")
	}
	if out := loginHTTP(s, cookie, false, "source-http-login", "wrong-synthetic", "192.0.2.56:1"); out.Code != 401 {
		t.Fatal("untrusted XFF merged actual peers", out.Code)
	}
}

func TestLoginPOSTSuccessfulSessionsResetFailuresAndAvoidAllAttemptLimit(t *testing.T) {
	s, _ := loginCounterFixture(t, 10)
	password, cookie := loginHTTPFixture(t, s)
	for i := 0; i < 2; i++ {
		if out := loginHTTP(s, cookie, false, "source-http-login", "wrong-synthetic", "192.0.2.66:1"); out.Code != 401 {
			t.Fatal(out.Code)
		}
	}
	for i := 0; i < 13; i++ {
		browser := i%2 == 1
		out := loginHTTP(s, cookie, browser, "source-http-login", password, "192.0.2.66:1")
		want := 200
		if browser {
			want = 302
		}
		if out.Code != want {
			t.Fatal("successful logins consumed failed-attempt quota", out.Code)
		}
		var session bool
		for _, value := range out.Result().Cookies() {
			if value.Name == "sessionid" && value.HttpOnly && value.Secure {
				session = true
			}
		}
		if !session {
			t.Fatal("pinned successful login session cookies missing")
		}
		if browser && out.Header().Get("Location") != "/profile/" {
			t.Fatal("source successful form did not redirect")
		}
	}
	failed, pending, expiry, _ := loginSnapshot(t, s, "source-http-login", "192.0.2.66:1")
	if failed != 0 || pending != 0 || expiry != nil {
		t.Fatal("successful login did not clear source failure counter")
	}
	// Only the per-prefix auth.login total remains: two failures and thirteen
	// successes, never the pinned library's legacy per-host key.
	var keys, attempts int
	if err := s.DB.QueryRow(context.Background(), `SELECT count(*),coalesce(sum(count) FILTER(WHERE key_hash=$1),0) FROM accounts_go_auth_attempt`, s.Store.peerKey(AuthScopeLogin, "192.0.2.66")).Scan(&keys, &attempts); err != nil || keys != 1 || attempts != 15 {
		t.Fatal("wrapped logins charged other than the per-prefix login total", keys, attempts, err)
	}
}
