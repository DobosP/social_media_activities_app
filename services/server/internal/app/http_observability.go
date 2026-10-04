package app

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/ops"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// The forwarding protocol is meaningful only when the connection's immediate
// peer is an explicitly configured proxy. It is independent of the XFF chain.
func (a *App) secureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || r.Header.Get("X-Forwarded-Proto") != "https" {
		return false
	}
	address, err := netip.ParseAddr(peer)
	if err != nil {
		return false
	}
	for _, network := range a.proxyNetworks {
		if network.Contains(address.Unmap()) {
			return true
		}
	}
	return false
}

type observedResponse struct {
	http.ResponseWriter
	status      int
	bodyBearing bool
}

func (w *observedResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *observedResponse) WriteHeader(status int) {
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status != 0 {
		return
	}
	// net/http otherwise drains a small unread body before flushing a refusal.
	// Close rejected body-bearing HTTP/1 requests so authentication, role and
	// declared-size gates respond without requiring the denied upload to arrive.
	if status >= 400 && w.bodyBearing {
		w.Header().Set("Connection", "close")
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *observedResponse) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}
func (w *observedResponse) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *observedResponse) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, reader, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.status = http.StatusSwitchingProtocols
	}
	return conn, reader, err
}

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if a.Catalog != nil {
		r = r.WithContext(catalog.WithPolicy(r.Context(), a.Catalog.Policy))
	}
	memory := a.Config.DataUploadMemoryBytes
	if memory == 0 {
		memory = 8 << 20
	}
	r = r.WithContext(platform.WithDataUploadLimit(r.Context(), memory))
	started := time.Now()
	id := strings.TrimSpace(r.Header.Get("X-Request-ID"))
	if !requestIDPattern.MatchString(id) {
		id = rand.Text()
	}
	w.Header().Set("X-Request-ID", id)
	w.Header().Set("X-Social-Runtime", "go")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	permissions := a.Config.PermissionsPolicy
	if permissions == "" {
		permissions = DefaultPermissionsPolicy
	}
	w.Header().Set("Permissions-Policy", permissions)
	w.Header().Set("Cache-Control", "no-store")
	response := &observedResponse{ResponseWriter: w, bodyBearing: r.ContentLength != 0 || len(r.TransferEncoding) > 0}
	defer func() {
		failure := recover()
		status := response.status
		if failure != nil {
			status = http.StatusInternalServerError
			if response.status == 0 {
				platform.Error(response, status, "Request unavailable.")
			}
		} else if status == 0 {
			status = http.StatusOK
		}
		elapsed := time.Since(started)
		if a.Ops != nil {
			a.Ops.Observe(status, elapsed)
		}
		route := "unmatched"
		if a.Mux != nil {
			_, pattern := a.Mux.Handler(r)
			if pattern != "" {
				route = pattern
			}
		}
		if strings.HasPrefix(r.URL.Path, "/static/") {
			route = "/static/*"
		}
		if a.Config.ErrorReporter != nil {
			if failure != nil {
				a.Config.ErrorReporter.Capture(ops.Panic, r.Method, route)
			} else if status >= 500 {
				a.Config.ErrorReporter.Capture(ops.HTTP5xx, r.Method, route)
			}
		}
		if !a.Config.RequestLoggingEnabled || !requestLogEnabled(a.Config.LogLevel, status, failure != nil) {
			if failure != nil && response.status != http.StatusInternalServerError {
				panic(http.ErrAbortHandler)
			}
			return
		}
		writer := a.Config.LogWriter
		if writer == nil {
			writer = os.Stderr
		}
		var encoded []byte
		if a.Config.LogFormat == "plain" {
			encoded = fmt.Appendf(nil, "request method=%s route=%q status=%d duration_ms=%d request_id=%s\n", r.Method, route, status, elapsed.Milliseconds(), id)
		} else {
			encoded, _ = json.Marshal(map[string]any{"event": "request", "method": r.Method, "route": route, "status_code": status, "duration_ms": elapsed.Milliseconds(), "request_id": id})
			encoded = append(encoded, '\n')
		}
		_, _ = writer.Write(encoded)
		if failure != nil && response.status != http.StatusInternalServerError {
			panic(http.ErrAbortHandler)
		}
	}()
	if !allowedHost(r.Host, a.Config.AllowedHosts) {
		platform.Error(response, http.StatusBadRequest, "Invalid host.")
		return
	}
	if len(r.URL.EscapedPath()) > 2048 || len(r.URL.RawQuery) > 8192 {
		platform.Error(response, http.StatusRequestURITooLong, "Request too large.")
		return
	}
	secure := a.secureRequest(r)
	if secure && a.Config.HSTSSeconds > 0 {
		policy := "max-age=" + strconv.FormatInt(a.Config.HSTSSeconds, 10)
		if a.Config.HSTSIncludeSubdomains {
			policy += "; includeSubDomains"
		}
		if a.Config.HSTSPreload {
			policy += "; preload"
		}
		response.Header().Set("Strict-Transport-Security", policy)
	}
	peer, _, _ := net.SplitHostPort(r.RemoteAddr)
	address, parseErr := netip.ParseAddr(peer)
	localLiveness := r.URL.Path == "/healthz" && (r.Method == http.MethodGet || r.Method == http.MethodHead) && parseErr == nil && address.IsLoopback()
	if !secure && a.Config.SecureSSLRedirect && !localLiveness {
		origin, err := url.Parse(a.Config.PublicURL)
		if err != nil || origin.Scheme != "https" || origin.Host == "" {
			platform.Error(response, http.StatusServiceUnavailable, "Secure origin unavailable.")
			return
		}
		target := &url.URL{Scheme: "https", Host: origin.Host, Path: r.URL.Path, RawPath: r.URL.RawPath, RawQuery: r.URL.RawQuery}
		http.Redirect(response, r, target.String(), http.StatusMovedPermanently)
		return
	}
	a.serveHTTP(response, r)
}

var _ io.Writer = (*observedResponse)(nil)
var _ http.Hijacker = (*observedResponse)(nil)
var _ http.Flusher = (*observedResponse)(nil)
