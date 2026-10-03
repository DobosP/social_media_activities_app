// Command agentapi serves this platform's public open-data snapshot
// (events, places, activities, taxonomy) to AI agents over a small,
// read-only, stdlib-only HTTP API. It is deliberately DB-free: a separate
// Django job writes gate-filtered public JSON files to a snapshot
// directory, and this process loads them into memory and serves queries
// against them. See README.md for the on-disk contract and deployment
// notes.
package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// buildHandler assembles the full middleware chain: logging (outermost) ->
// method/CORS/security headers -> request budgets -> rate limiting -> routes. Factored out of
// main() so tests can exercise the exact same chain via httptest.
func buildHandler(app *App, rl *RateLimiter, cfg Config, logger *log.Logger) http.Handler {
	var handler http.Handler = app.routes()
	handler = rateLimitMiddleware(rl, cfg.TrustProxy, handler)
	handler = requestBoundsMiddleware(handler)
	handler = methodAndCORS(handler)
	handler = loggingMiddleware(logger, handler)
	return handler
}

func main() {
	healthcheck := len(os.Args) == 2 && os.Args[1] == "--healthcheck"
	if len(os.Args) != 1 && !healthcheck {
		log.Fatal("agentapi: unsupported command arguments")
	}
	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("agentapi: config error: %v", err)
	}
	if healthcheck {
		if !checkHealth(cfg.Addr) {
			log.Fatal("agentapi: health check failed")
		}
		return
	}

	logger := log.New(os.Stdout, "", log.LstdFlags|log.Lmicroseconds)

	loader := NewLoader(cfg.SnapshotDir, logger)
	if _, err := loader.CheckReload(); err != nil {
		logger.Print("agentapi: initial snapshot load failed (will retry)")
	}

	app := NewApp(cfg, loader)
	rl := NewRateLimiter(cfg.RatePerMin, cfg.RateBurst)
	handler := buildHandler(app, rl, cfg, logger)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 16,
		ErrorLog:          log.New(serverErrorWriter{logger: logger}, "", 0),
	}

	stopReload := make(chan struct{})
	go loader.StartAutoReload(time.Duration(cfg.ReloadInterval)*time.Second, stopReload)

	logger.Printf("agentapi: listening, reload every %ds", cfg.ReloadInterval)

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- srv.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		if err != nil && err != http.ErrServerClosed {
			logger.Fatal("agentapi: server failed")
		}
	case sig := <-sigCh:
		logger.Printf("agentapi: received %s, shutting down (30s drain)", sig)
		close(stopReload)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			logger.Print("agentapi: graceful shutdown failed")
		}
	}
}

// serverErrorWriter suppresses the stdlib server's raw diagnostic text:
// panic logs contain peer IPs and may include application data in stacks.
type serverErrorWriter struct{ logger *log.Logger }

func (w serverErrorWriter) Write(p []byte) (int, error) {
	w.logger.Print("agentapi: HTTP server error")
	return len(p), nil
}

// checkHealth uses only the configured numeric port and a loopback address.
// It does not honor HTTP_PROXY, follow redirects or print response content.
func checkHealth(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	loopback := "127.0.0.1"
	if ip, err := netip.ParseAddr(host); err == nil && ip.Is6() && !ip.Is4In6() {
		loopback = "::1"
	}
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
		ResponseHeaderTimeout:  2 * time.Second,
		MaxResponseHeaderBytes: 8192,
		DisableKeepAlives:      true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   3 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Get("http://" + net.JoinHostPort(loopback, port) + "/agent/v1/healthz")
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusOK
}
