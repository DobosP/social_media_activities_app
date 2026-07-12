package main

import (
	"compress/gzip"
	"encoding/json"
	"log"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// --- CORS -------------------------------------------------------------

func setCORSHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "*")
	h.Set("Access-Control-Max-Age", "86400")
}

// methodAndCORS enforces GET/HEAD/OPTIONS only and sets CORS headers on
// every response. Unregistered methods get a 405 with the standard error
// envelope; OPTIONS gets a bare 204 (CORS preflight).
func methodAndCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		switch r.Method {
		case http.MethodOptions:
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet, http.MethodHead:
			next.ServeHTTP(w, r)
		default:
			w.Header().Set("Allow", "GET, HEAD, OPTIONS")
			writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		}
	})
}

// --- logging ------------------------------------------------------------

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (sw *statusWriter) WriteHeader(status int) {
	if sw.wroteHeader {
		return
	}
	sw.status = status
	sw.wroteHeader = true
	sw.ResponseWriter.WriteHeader(status)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	if !sw.wroteHeader {
		sw.WriteHeader(http.StatusOK)
	}
	return sw.ResponseWriter.Write(b)
}

// loggingMiddleware logs method, path, status, duration and a truncated
// query string only. It never logs client identity (IP/UA/headers), per the
// platform's privacy invariants.
func loggingMiddleware(logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		dur := time.Since(start)

		q := r.URL.RawQuery
		const maxQueryLog = 200
		if len(q) > maxQueryLog {
			q = q[:maxQueryLog] + "...(truncated)"
		}
		logger.Printf("method=%s path=%s status=%d duration_ms=%d query=%q",
			r.Method, r.URL.Path, sw.status, dur.Milliseconds(), q)
	})
}

// --- rate limiting --------------------------------------------------------

type tokenBucket struct {
	tokens float64
	last   time.Time
}

// RateLimiter is a per-client token bucket limiter. Buckets are created
// lazily and swept periodically to bound memory use.
type RateLimiter struct {
	mu         sync.Mutex
	buckets    map[string]*tokenBucket
	ratePerMin float64
	burst      float64
	idleTTL    time.Duration
	lastSweep  time.Time
	now        func() time.Time // overridable for tests
}

func NewRateLimiter(ratePerMin, burst int) *RateLimiter {
	return &RateLimiter{
		buckets:    make(map[string]*tokenBucket),
		ratePerMin: float64(ratePerMin),
		burst:      float64(burst),
		idleTTL:    10 * time.Minute,
		now:        time.Now,
	}
}

// Allow reports whether the client identified by key may proceed, consuming
// a token if so.
func (rl *RateLimiter) Allow(key string) bool {
	now := rl.now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.buckets[key]
	if !ok {
		b = &tokenBucket{tokens: rl.burst, last: now}
		rl.buckets[key] = b
	} else {
		elapsed := now.Sub(b.last).Seconds()
		if elapsed > 0 {
			b.tokens = math.Min(rl.burst, b.tokens+elapsed*(rl.ratePerMin/60.0))
			b.last = now
		}
	}

	rl.sweepLocked(now)

	if b.tokens >= 1 {
		b.tokens -= 1
		return true
	}
	return false
}

// sweepLocked evicts buckets idle for longer than idleTTL. Caller must hold
// rl.mu. Runs at most once per minute to keep Allow() cheap.
func (rl *RateLimiter) sweepLocked(now time.Time) {
	if !rl.lastSweep.IsZero() && now.Sub(rl.lastSweep) < time.Minute {
		return
	}
	rl.lastSweep = now
	for k, b := range rl.buckets {
		if now.Sub(b.last) > rl.idleTTL {
			delete(rl.buckets, k)
		}
	}
}

// clientKey derives a per-client identity for rate limiting only (never
// logged). When trustProxy is set, the last hop of X-Forwarded-For is used
// (the hop closest to this server, i.e. the one Caddy appended); otherwise
// the TCP peer address is used.
func clientKey(r *http.Request, trustProxy bool) string {
	if trustProxy {
		xff := r.Header.Get("X-Forwarded-For")
		if xff != "" {
			parts := strings.Split(xff, ",")
			last := strings.TrimSpace(parts[len(parts)-1])
			if last != "" {
				return last
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// rateLimitMiddleware rejects requests once a client exhausts its token
// bucket. /agent/v1/healthz is exempt so uptime monitors are never throttled.
func rateLimitMiddleware(rl *RateLimiter, trustProxy bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/agent/v1/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		key := clientKey(r, trustProxy)
		if !rl.Allow(key) {
			w.Header().Set("Retry-After", "60")
			writeError(w, r, http.StatusTooManyRequests, "rate_limited", "rate limit exceeded, retry later")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- response helpers (caching, gzip, errors) ------------------------------

// shouldGzip reports whether the response body should be gzip-compressed.
func shouldGzip(r *http.Request, bodyLen int) bool {
	if bodyLen < 1024 {
		return false
	}
	return acceptsGzip(r.Header.Get("Accept-Encoding"))
}

// acceptsGzip implements RFC 9110 §12.5.3 content-negotiation for the gzip
// coding only: it parses each comma-separated Accept-Encoding member,
// matches the coding name (case-insensitive, "gzip" or "x-gzip") ignoring
// any parameters other than q, and returns true only if a matching coding's
// q-value is greater than zero. An absent or empty header means no
// preference was expressed, so gzip is not applied (matches prior
// behavior). A q=0 entry is an explicit refusal for that coding per the RFC.
func acceptsGzip(acceptEncoding string) bool {
	if acceptEncoding == "" {
		return false
	}
	// Track the q-value seen for each relevant coding independently so a
	// refusal of one (e.g. "gzip;q=0") never masks an accept of the other
	// (e.g. "x-gzip;q=0.5") when both appear in the same header.
	q := map[string]float64{"gzip": -1, "x-gzip": -1}
	for _, member := range strings.Split(acceptEncoding, ",") {
		member = strings.TrimSpace(member)
		if member == "" {
			continue
		}
		coding := member
		qValue := 1.0
		if semi := strings.Index(member, ";"); semi >= 0 {
			coding = strings.TrimSpace(member[:semi])
			for _, param := range strings.Split(member[semi+1:], ";") {
				param = strings.TrimSpace(param)
				name, value, ok := strings.Cut(param, "=")
				if !ok || strings.TrimSpace(name) != "q" {
					continue
				}
				if parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
					qValue = parsed
				}
			}
		}
		coding = strings.ToLower(coding)
		if _, relevant := q[coding]; relevant {
			q[coding] = qValue
		}
	}
	return q["gzip"] > 0 || q["x-gzip"] > 0
}

// writeBody writes status, headers (Cache-Control, ETag, CORS, Vary) and the
// given body, transparently gzip-encoding when appropriate. HEAD requests
// get headers only.
func writeBody(w http.ResponseWriter, r *http.Request, status int, contentType, cacheControl, etag string, body []byte) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Cache-Control", cacheControl)
	if etag != "" {
		h.Set("ETag", etag)
	}
	h.Set("Vary", "Accept-Encoding")
	setCORSHeaders(w)

	if r.Method == http.MethodHead {
		w.WriteHeader(status)
		return
	}

	if shouldGzip(r, len(body)) {
		h.Set("Content-Encoding", "gzip")
		w.WriteHeader(status)
		gz := gzip.NewWriter(w)
		_, _ = gz.Write(body)
		_ = gz.Close()
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeJSON marshals payload and writes it via writeBody.
func writeJSON(w http.ResponseWriter, r *http.Request, status int, cacheControl, etag string, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "failed to encode response")
		return
	}
	writeBody(w, r, status, "application/json; charset=utf-8", cacheControl, etag, body)
}

// writeRawJSON writes an already-encoded JSON document verbatim.
func writeRawJSON(w http.ResponseWriter, r *http.Request, status int, cacheControl, etag string, raw json.RawMessage) {
	writeBody(w, r, status, "application/json; charset=utf-8", cacheControl, etag, raw)
}

// write304 short-circuits a conditional GET/HEAD with an empty body.
func write304(w http.ResponseWriter, r *http.Request, cacheControl, etag string) {
	h := w.Header()
	h.Set("Cache-Control", cacheControl)
	h.Set("ETag", etag)
	h.Set("Vary", "Accept-Encoding")
	setCORSHeaders(w)
	w.WriteHeader(http.StatusNotModified)
}

// ifNoneMatchHit reports whether the request's If-None-Match header matches
// etag (simple strong comparison; also accepts "*").
func ifNoneMatchHit(r *http.Request, etag string) bool {
	inm := r.Header.Get("If-None-Match")
	if inm == "" {
		return false
	}
	if inm == "*" {
		return true
	}
	for _, candidate := range strings.Split(inm, ",") {
		if strings.TrimSpace(candidate) == etag {
			return true
		}
	}
	return false
}

type errorEnvelope struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeError writes the standard {"error":{"code","message"}} envelope. It
// still sets CORS headers so error responses remain usable cross-origin.
func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	body, _ := json.Marshal(errorEnvelope{Error: errorDetail{Code: code, Message: message}})
	setCORSHeaders(w)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r != nil && r.Method == http.MethodHead {
		w.WriteHeader(status)
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
