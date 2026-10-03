package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// Config holds the runtime configuration, sourced entirely from environment
// variables with the defaults documented in README.md.
type Config struct {
	Addr           string
	SnapshotDir    string
	ReloadInterval int // seconds
	RatePerMin     int
	RateBurst      int
	TrustProxy     bool
	MaxLimit       int
}

// LoadConfig reads configuration from the environment, applying defaults for
// anything unset. It returns an error if a set value fails to parse.
func LoadConfig() (Config, error) {
	cfg := Config{
		Addr:           envOr("AGENT_API_ADDR", ":8090"),
		SnapshotDir:    envOr("AGENT_SNAPSHOT_DIR", "/data/agent_snapshot"),
		ReloadInterval: 30,
		RatePerMin:     300,
		RateBurst:      60,
		TrustProxy:     envOr("AGENT_API_TRUST_PROXY", "") == "1",
		MaxLimit:       200,
	}

	var err error
	if cfg.ReloadInterval, err = envInt("AGENT_API_RELOAD_SECONDS", cfg.ReloadInterval); err != nil {
		return cfg, err
	}
	if cfg.RatePerMin, err = envInt("AGENT_API_RATE_PER_MIN", cfg.RatePerMin); err != nil {
		return cfg, err
	}
	if cfg.RateBurst, err = envInt("AGENT_API_RATE_BURST", cfg.RateBurst); err != nil {
		return cfg, err
	}
	if cfg.MaxLimit, err = envInt("AGENT_API_MAX_LIMIT", cfg.MaxLimit); err != nil {
		return cfg, err
	}
	for _, field := range []struct {
		name       string
		value, max int
	}{
		{"AGENT_API_RELOAD_SECONDS", cfg.ReloadInterval, 3600},
		{"AGENT_API_RATE_PER_MIN", cfg.RatePerMin, 1000000},
		{"AGENT_API_RATE_BURST", cfg.RateBurst, 10000},
		{"AGENT_API_MAX_LIMIT", cfg.MaxLimit, 1000},
	} {
		if field.value < 1 || field.value > field.max {
			return cfg, fmt.Errorf("%s must be between 1 and %d", field.name, field.max)
		}
	}
	if v := envOr("AGENT_API_TRUST_PROXY", ""); v != "" && v != "0" && v != "1" {
		return cfg, fmt.Errorf("AGENT_API_TRUST_PROXY must be 0 or 1")
	}
	_, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return cfg, fmt.Errorf("AGENT_API_ADDR must be a host:port listen address")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 0 || portNumber > 65535 {
		return cfg, fmt.Errorf("AGENT_API_ADDR must contain a valid numeric port")
	}
	if strings.IndexFunc(cfg.SnapshotDir, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
		return cfg, fmt.Errorf("AGENT_SNAPSHOT_DIR must not contain control characters")
	}

	return cfg, nil
}

func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		// strconv errors include the supplied value, which may accidentally be
		// a credential. Startup diagnostics name the field without echoing it.
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	return n, nil
}
