package main

import (
	"fmt"
	"os"
	"strconv"
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
		return 0, fmt.Errorf("invalid %s=%q: %w", key, v, err)
	}
	return n, nil
}
