package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/DobosP/social_media_activities_app/services/server/internal/web"
)

func TestNativeLogoutNeverThrottledAndSignupCapIsPerPrefix(t *testing.T) {
	db := testdb.New(t, *appDSN, nil)
	a, err := New(context.Background(), db, integrationConfig(t), true)
	if err != nil {
		t.Fatal(err)
	}
	csrf := strings.Repeat("a", 52)
	post := func(path, body, remote string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", "https://app.example"+path, strings.NewReader(body))
		r.RemoteAddr = remote
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://app.example")
		r.Header.Set("X-CSRFToken", csrf)
		r.AddCookie(&http.Cookie{Name: "csrftoken", Value: csrf})
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	for i := 0; i < 12; i++ {
		if w := post("/api/auth/logout", "", "192.0.2.120:1"); w.Code != 200 || !strings.Contains(w.Body.String(), `{"authenticated":false}`) {
			t.Fatal("API logout was throttled", i, w.Code)
		}
	}
	a.Accounts.RatePolicies = map[string]budgets.Policy{"auth.signup": {Limit: 2, Window: time.Hour}}
	for i := 0; i < 2; i++ {
		if w := post("/api/auth/signup", fmt.Sprintf(`{"username":"capped-signup-%d","password":"synthetic-password-only"}`, i), fmt.Sprintf("192.0.2.121:%d", 1000+i)); w.Code != 201 {
			t.Fatal("signup inside its per-prefix cap", i, w.Code, w.Body.String())
		}
	}
	if w := post("/api/auth/signup", `{"username":"capped-signup-x","password":"synthetic-password-only"}`, "192.0.2.121:2000"); w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal("signup per-prefix cap missing", w.Code)
	}
	if w := post("/api/auth/signup", `{"username":"capped-signup-b","password":"synthetic-password-only"}`, "192.0.2.122:1"); w.Code != 201 {
		t.Fatal("one prefix's signups throttled another prefix", w.Code, w.Body.String())
	}
}

func TestNativeCredentialSubtreeVariantsReachNoVerdict(t *testing.T) {
	db := testdb.New(t, *appDSN, nil)
	ctx := context.Background()
	a, err := New(ctx, db, integrationConfig(t), true)
	if err != nil {
		t.Fatal(err)
	}
	const password = "synthetic-subtree-password-only"
	hash, err := authcore.HashPassword(ctx, password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.CreatePasswordUser(ctx, authcore.User{Username: "subtree-login-user"}, hash); err != nil {
		t.Fatal(err)
	}
	var usersBefore int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM accounts_user`).Scan(&usersBefore); err != nil {
		t.Fatal(err)
	}
	csrf := strings.Repeat("a", 52)
	for _, path := range []string{"/login/x", "/login/x/y", "/register/x", "/register/x/y"} {
		body := url.Values{"username": {"subtree-login-user"}, "password": {password}, "display_name": {"Subtree"}, "csrfmiddlewaretoken": {csrf}}.Encode()
		r := httptest.NewRequest("POST", "https://app.example"+path, strings.NewReader(body))
		r.RemoteAddr = "192.0.2.150:1"
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", "https://app.example")
		r.Header.Set("X-CSRFToken", csrf)
		r.AddCookie(&http.Cookie{Name: "csrftoken", Value: csrf})
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatal("credential subtree variant reached a handler", path, w.Code)
		}
		for _, cookie := range w.Result().Cookies() {
			if cookie.Name == "sessionid" {
				t.Fatal("credential subtree variant set a session", path)
			}
		}
	}
	for _, table := range []string{"accounts_go_auth_attempt", "accounts_go_login_failure", "accounts_go_login_reservation", "accounts_go_session"} {
		var rows int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&rows); err != nil || rows != 0 {
			t.Fatal("credential subtree variant left authentication state", table, rows, err)
		}
	}
	var users int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM accounts_user`).Scan(&users); err != nil || users != usersBefore {
		t.Fatal("register subtree variant created an account", users, err)
	}
}

type attemptRecorder struct {
	authcore.Store
	calls int
}

// Refusing keeps every pinned handler away from the embedded nil Store.
func (s *attemptRecorder) AllowAuthAttempt(context.Context, string, time.Time) (bool, error) {
	s.calls++
	return false, nil
}

func TestNativeAuthMarkerCoversEveryPinnedAttemptRoute(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "authcore", "service.go"))
	if err != nil {
		t.Fatal(err)
	}
	routes := regexp.MustCompile(`mux\.HandleFunc\("([A-Z]+) "\+prefix\+"([^"]+)"`).FindAllStringSubmatch(string(source), -1)
	if len(routes) == 0 {
		t.Fatal("pinned authentication route table not found")
	}
	store := &attemptRecorder{}
	auth, err := authcore.New(authcore.Config{PublicURL: "https://app.example", FacebookClientID: "synthetic-client", FacebookClientSecret: "synthetic-secret", FacebookAPIVersion: "v19.0"}, store)
	if err != nil {
		t.Fatal(err)
	}
	// The assembled mux's credential surface: the pinned library's routes and
	// the HTML wrappers that call into it (the web router's zero value needs no
	// renderer or database for these probes).
	mux := http.NewServeMux()
	auth.Register(mux, "/api/auth")
	(&web.Server{Auth: auth}).Register(mux)
	type probe struct{ method, path string }
	probes := []probe{}
	for _, route := range routes {
		path := "/api/auth" + strings.ReplaceAll(route[2], "{provider}", "facebook")
		probes = append(probes, probe{route[1], path}, probe{route[1], path + "/"}, probe{route[1], path + "/x"})
	}
	for _, path := range []string{"/login/", "/register/", "/logout/", "/login", "/register", "/login/x", "/register/x", "/login/x/y", "/register/x/y", "/logout/x"} {
		probes = append(probes, probe{http.MethodPost, path})
	}
	csrf := strings.Repeat("c", 52)
	charging := 0
	for _, p := range probes {
		r := httptest.NewRequest(p.method, "https://app.example"+p.path, strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://app.example")
		r.Header.Set("X-CSRFToken", csrf)
		r.AddCookie(&http.Cookie{Name: "csrftoken", Value: csrf})
		before := store.calls
		mux.ServeHTTP(httptest.NewRecorder(), r)
		if store.calls == before {
			continue
		}
		charging++
		if _, _, marked := authAttemptScope(p.method, p.path); !marked && !loginIntercept(p.method, p.path) {
			t.Fatalf("%s %s charges the pinned attempt store without a peer marker or the login intercept", p.method, p.path)
		}
	}
	// Four pinned routes plus the exact /login/ and /register/ wrappers.
	if charging != 6 {
		t.Fatal("attempt-charging route set changed; review the marker table", charging)
	}
	if scope, exempt, ok := authAttemptScope(http.MethodPost, "/register/"); !ok || exempt || scope != accounts.AuthScopeSignup {
		t.Fatal("HTML signup wrapper lost its signup scope")
	}
	if _, exempt, ok := authAttemptScope(http.MethodPost, "/api/auth/logout"); !ok || !exempt {
		t.Fatal("API logout lost its exemption")
	}
}
