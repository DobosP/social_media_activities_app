package main

import (
	"strings"
	"testing"
)

func clearTestConfigEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"AGENT_API_ADDR", "AGENT_SNAPSHOT_DIR", "AGENT_API_RELOAD_SECONDS",
		"AGENT_API_RATE_PER_MIN", "AGENT_API_RATE_BURST", "AGENT_API_TRUST_PROXY", "AGENT_API_MAX_LIMIT",
	} {
		t.Setenv(name, "")
	}
}

func TestConfigRejectsInvalidValuesWithoutDisclosure(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"AGENT_API_RELOAD_SECONDS", "private-sentinel"},
		{"AGENT_API_RATE_PER_MIN", "0"},
		{"AGENT_API_RATE_PER_MIN", "1000001"},
		{"AGENT_API_RATE_BURST", "-1"},
		{"AGENT_API_RATE_BURST", "10001"},
		{"AGENT_API_MAX_LIMIT", "1001"},
		{"AGENT_API_RELOAD_SECONDS", "3601"},
		{"AGENT_API_TRUST_PROXY", "private-sentinel"},
		{"AGENT_API_ADDR", "private-sentinel"},
		{"AGENT_API_ADDR", ":private-sentinel"},
		{"AGENT_API_ADDR", ":65536"},
		{"AGENT_SNAPSHOT_DIR", "/data/private-sentinel\n"},
	} {
		t.Run(tc.name+"/"+tc.value, func(t *testing.T) {
			clearTestConfigEnv(t)
			t.Setenv(tc.name, tc.value)
			_, err := LoadConfig()
			if err == nil {
				t.Fatal("expected invalid configuration to fail startup")
			}
			if !strings.Contains(err.Error(), tc.name) || strings.Contains(err.Error(), "private-sentinel") {
				t.Fatalf("diagnostic must identify only the field: %s", err)
			}
		})
	}
}

func TestConfigDefaultsAndExplicitProxyMode(t *testing.T) {
	clearTestConfigEnv(t)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RatePerMin != 300 || cfg.RateBurst != 60 || cfg.MaxLimit != 200 || cfg.ReloadInterval != 30 || cfg.TrustProxy {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	t.Setenv("AGENT_API_TRUST_PROXY", "1")
	t.Setenv("AGENT_API_ADDR", "[::1]:0")
	cfg, err = LoadConfig()
	if err != nil || !cfg.TrustProxy {
		t.Fatalf("explicit proxy mode should remain supported: %v", err)
	}
}
