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
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// buildHandler assembles the full middleware chain: logging (outermost) ->
// method/CORS enforcement -> rate limiting -> routes. Factored out of
// main() so tests can exercise the exact same chain via httptest.
func buildHandler(app *App, rl *RateLimiter, cfg Config, logger *log.Logger) http.Handler {
	var handler http.Handler = app.routes()
	handler = rateLimitMiddleware(rl, cfg.TrustProxy, handler)
	handler = methodAndCORS(handler)
	handler = loggingMiddleware(logger, handler)
	return handler
}

func main() {
	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("agentapi: config error: %v", err)
	}

	logger := log.New(os.Stdout, "", log.LstdFlags|log.Lmicroseconds)

	loader := NewLoader(cfg.SnapshotDir, logger)
	if _, err := loader.CheckReload(); err != nil {
		logger.Printf("agentapi: initial snapshot load failed (will retry): %v", err)
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
	}

	stopReload := make(chan struct{})
	go loader.StartAutoReload(time.Duration(cfg.ReloadInterval)*time.Second, stopReload)

	logger.Printf("agentapi: listening on %s, snapshot dir %s, reload every %ds",
		cfg.Addr, cfg.SnapshotDir, cfg.ReloadInterval)

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- srv.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		if err != nil && err != http.ErrServerClosed {
			logger.Fatalf("agentapi: server error: %v", err)
		}
	case sig := <-sigCh:
		logger.Printf("agentapi: received %s, shutting down (30s drain)", sig)
		close(stopReload)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			logger.Printf("agentapi: graceful shutdown error: %v", err)
		}
	}
}
