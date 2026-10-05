package ops

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
)

type Check func(context.Context) error
type HTTPConfig struct {
	Version, MetricsToken string
	Cache, Storage        Check
}
type Service struct {
	DB                          *pgxpool.Pool
	Config                      HTTPConfig
	draining                    atomic.Bool
	requests, errors, latencyNS atomic.Uint64
	mu                          sync.Mutex
	csp                         []CSPViolation
	Budgets                     *budgets.Store
}

func NewService(db *pgxpool.Pool, config HTTPConfig) *Service {
	if config.Version == "" {
		config.Version = "unknown"
	}
	return &Service{DB: db, Config: config, Budgets: budgets.New(db)}
}
func (s *Service) Register(mux *http.ServeMux) {
	for _, prefix := range []string{"/api", "/api/v1"} {
		for _, path := range []string{"/health", "/health/"} {
			registerExact(mux, "GET "+prefix+path, s.Health)
		}
		for _, path := range []string{"/ready", "/ready/"} {
			registerExact(mux, "GET "+prefix+path, s.Ready)
		}
		registerExact(mux, "GET "+prefix+"/ops/stats/", s.Stats)
		registerExact(mux, "POST "+prefix+"/ops/csp-report/", s.CSPReport)
	}
	registerExact(mux, "GET /metrics", s.Metrics)
}
func (s *Service) MarkDraining() { s.draining.Store(true) }
func (s *Service) Observe(status int, elapsed time.Duration) {
	s.requests.Add(1)
	if status >= 500 {
		s.errors.Add(1)
	}
	s.latencyNS.Add(uint64(max(elapsed.Nanoseconds(), 0)))
}
func (s *Service) Health(w http.ResponseWriter, r *http.Request) {
	platform.JSON(w, 200, map[string]string{"status": "ok", "version": s.Config.Version})
}
func (s *Service) Ready(w http.ResponseWriter, r *http.Request) {
	if s.draining.Load() {
		platform.JSON(w, 503, map[string]any{"status": "draining", "draining": true})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	database := s.DB != nil && s.DB.Ping(ctx) == nil
	result := map[string]any{"status": "ready", "draining": false, "database": database}
	ready := database
	for _, check := range []struct {
		name string
		fn   Check
	}{{"cache", s.Config.Cache}, {"storage", s.Config.Storage}} {
		if check.fn != nil {
			ok := check.fn(ctx) == nil
			result[check.name] = ok
			ready = ready && ok
		}
	}
	status := 200
	if !ready {
		status = 503
		result["status"] = "degraded"
	}
	platform.JSON(w, status, result)
}
func (s *Service) Stats(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	if !a.IsStaff {
		platform.Error(w, 403, "Staff access required.")
		return
	}
	var users, activities, posts, bookings, donations, total int64
	err := s.DB.QueryRow(r.Context(), `SELECT (SELECT count(*) FROM accounts_user),(SELECT count(*) FROM social_activity),(SELECT count(*) FROM social_post),(SELECT count(*) FROM booking_booking),(SELECT count(*) FROM donations_donation WHERE status='completed'),(SELECT COALESCE(sum(amount_cents),0) FROM donations_donation WHERE status='completed')`).Scan(&users, &activities, &posts, &bookings, &donations, &total)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, map[string]int64{"users": users, "activities": activities, "posts": posts, "bookings": bookings, "donations_completed": donations, "donations_total_cents": total})
}
func (s *Service) Metrics(w http.ResponseWriter, r *http.Request) {
	expected := "Bearer " + s.Config.MetricsToken
	provided := r.Header.Get("Authorization")
	if s.Config.MetricsToken == "" || len(expected) != len(provided) || subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 {
		http.Error(w, "metrics unavailable", 403)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, "# HELP social_http_requests_total Total observed HTTP requests.\n# TYPE social_http_requests_total counter\nsocial_http_requests_total %d\n# HELP social_http_server_errors_total Total observed HTTP responses with status 500 or greater.\n# TYPE social_http_server_errors_total counter\nsocial_http_server_errors_total %d\n# HELP social_http_duration_seconds_sum Total observed HTTP request duration in seconds.\n# TYPE social_http_duration_seconds_sum counter\nsocial_http_duration_seconds_sum %.9f\n", s.requests.Load(), s.errors.Load(), float64(s.latencyNS.Load())/1e9)
}

type CSPViolation struct {
	Directive string `json:"directive"`
	Blocked   string `json:"blocked"`
	Document  string `json:"document"`
}

func cleanCSP(raw any, limit int) string {
	value, ok := raw.(string)
	if !ok {
		return ""
	}
	value = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, value)
	chars := []rune(value)
	if len(chars) > limit {
		value = string(chars[:limit])
	}
	return strings.TrimSpace(value)
}
func cspURI(raw any) string {
	value := cleanCSP(raw, 200)
	if value == "" {
		return "unknown"
	}
	for _, known := range []string{"self", "inline", "eval", "wasm-eval"} {
		if value == known {
			return value
		}
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "unknown"
	}
	if parsed.Scheme != "" {
		if parsed.Scheme == "http" || parsed.Scheme == "https" || parsed.Scheme == "ws" || parsed.Scheme == "wss" {
			if parsed.Host != "" {
				path := parsed.EscapedPath()
				if path == "" {
					path = "/"
				}
				return parsed.Scheme + "://" + parsed.Host + path
			}
		}
		return parsed.Scheme + ":"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}
func ParseCSP(raw []byte) ([]CSPViolation, error) {
	if len(raw) > 8<<10 {
		return nil, errors.New("CSP body exceeds limit")
	}
	var payload any
	if json.Unmarshal(raw, &payload) != nil {
		return nil, errors.New("malformed CSP report")
	}
	entries := []any{}
	switch value := payload.(type) {
	case map[string]any:
		entries = append(entries, value)
	case []any:
		entries = value
	default:
		return nil, errors.New("malformed CSP report")
	}
	out := []CSPViolation{}
	for _, value := range entries {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		for _, key := range []string{"csp-report", "body"} {
			if nested, ok := item[key].(map[string]any); ok && len(nested) > 0 {
				item = nested
				break
			}
		}
		pick := func(keys ...string) any {
			for _, key := range keys {
				if value, ok := item[key]; ok && value != "" && value != nil {
					return value
				}
			}
			return ""
		}
		directive := cleanCSP(pick("effective-directive", "violated-directive"), 80)
		fields := strings.FieldsFunc(directive, unicode.IsSpace)
		if len(fields) == 0 {
			directive = "unknown"
		} else {
			directive = fields[0]
		}
		out = append(out, CSPViolation{directive, cspURI(pick("blocked-uri", "blockedURL")), cspURI(pick("document-uri", "documentURL"))})
	}
	if len(out) == 0 {
		return nil, errors.New("empty CSP report")
	}
	return out, nil
}
func (s *Service) CSPReport(w http.ResponseWriter, r *http.Request) {
	defer func() { w.WriteHeader(204) }()
	decision, err := s.Budgets.Global(r.Context(), "ops.csp_report", budgets.Policy{Limit: 120, Window: time.Minute})
	if err != nil || !decision.Allowed {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, (8<<10)+1))
	if err != nil {
		return
	}
	violations, err := ParseCSP(raw)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.csp = append(s.csp, violations...)
	if len(s.csp) > 200 {
		s.csp = s.csp[len(s.csp)-200:]
	}
}
func (s *Service) RecentCSP() []CSPViolation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]CSPViolation{}, s.csp...)
}

func registerExact(mux *http.ServeMux, pattern string, handler http.HandlerFunc) {
	if strings.HasSuffix(pattern, "/") {
		pattern += "{$}"
	}
	mux.HandleFunc(pattern, handler)
}
