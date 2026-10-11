package main

import (
	"reflect"
	"strings"
	"testing"
)

// ADR-0057: the consumer's local-POC mode gate. Every condition must hold;
// unset is production and changes nothing; set-but-failing refuses startup.
func TestLocalPOCGateRequiresDevelopmentLoopbackAndExactFlag(t *testing.T) {
	dev := fixtureOptions()
	dev.Dev = true
	pocEnv := map[string]string{"ROEDU_LOCAL_POC": "1", "ROEDU_API_URL": "http://127.0.0.1:8077", "ROEDU_API_KEY": "synthetic-read-only-key"}
	with := func(extra map[string]string) environment {
		values := map[string]string{}
		for key, value := range pocEnv {
			values[key] = value
		}
		for key, value := range extra {
			values[key] = value
		}
		return fixtureEnv(values)
	}
	config, err := configuration(with(nil), dev)
	if err != nil || config.Jobs.Roedu == nil || !config.Jobs.Roedu.LocalPOC || !config.Jobs.Roedu.LocalPOCActive() || !config.Web.LocalPOC {
		t.Fatal("gated POC mode not enabled", err)
	}
	for _, url := range []string{"http://localhost:8077", "http://[::1]:8077"} {
		if config, err = configuration(with(map[string]string{"ROEDU_API_URL": url}), dev); err != nil || !config.Jobs.Roedu.LocalPOC {
			t.Fatal("loopback producer refused", url, err)
		}
	}
	// DJANGO_DEBUG is the existing development mode too.
	if config, err = configuration(with(map[string]string{"DJANGO_DEBUG": "true"}), fixtureOptions()); err != nil || !config.Jobs.Roedu.LocalPOC {
		t.Fatal("DJANGO_DEBUG development refused", err)
	}

	// Production (flag unset) is unchanged even in development with a loopback producer.
	delete(pocEnv, "ROEDU_LOCAL_POC")
	if config, err = configuration(with(nil), dev); err != nil || config.Jobs.Roedu.LocalPOC || config.Web.LocalPOC || config.Jobs.RoeduCities != nil {
		t.Fatal("POC mode without the flag", err)
	}
	pocEnv["ROEDU_LOCAL_POC"] = "1"

	container := dev
	container.DevContainer = true
	refusals := map[string]struct {
		env     map[string]string
		options cliOptions
	}{
		"not development":         {nil, fixtureOptions()},
		"dev container":           {nil, container},
		"non-loopback producer":   {map[string]string{"ROEDU_API_URL": "https://data.fixture.test"}, dev},
		"loopback-looking domain": {map[string]string{"ROEDU_API_URL": "https://127.0.0.1.fixture.test"}, dev},
		"no producer":             {map[string]string{"ROEDU_API_URL": ""}, dev},
		"react UI (no marker)":    {map[string]string{"SOCIAL_REACT_UI": "true"}, dev},
		"flag true":               {map[string]string{"ROEDU_LOCAL_POC": "true"}, dev},
		"flag yes":                {map[string]string{"ROEDU_LOCAL_POC": "yes"}, dev},
		"flag 0":                  {map[string]string{"ROEDU_LOCAL_POC": "0"}, dev},
	}
	for name, tc := range refusals {
		if _, err := configuration(with(tc.env), tc.options); err == nil || !strings.Contains(err.Error(), "ROEDU_LOCAL_POC") {
			t.Fatal("POC gate accepted:", name, err)
		}
	}
	// Present but empty is set, not unset.
	present := func(name string) bool { return name == "ROEDU_LOCAL_POC" }
	if _, err := configuration(with(map[string]string{"ROEDU_LOCAL_POC": ""}), dev, present); err == nil {
		t.Fatal("present-empty ROEDU_LOCAL_POC accepted")
	}
}

func TestLocalPOCSyncCitiesOnlyUnderTheGate(t *testing.T) {
	dev := fixtureOptions()
	dev.Dev = true
	env := map[string]string{"ROEDU_LOCAL_POC": "1", "ROEDU_API_URL": "http://127.0.0.1:8077", "ROEDU_API_KEY": "synthetic-read-only-key", "ROEDU_SYNC_CITIES": "Cluj, București,Pitesti"}
	config, err := configuration(fixtureEnv(env), dev)
	if err != nil || !reflect.DeepEqual(config.Jobs.RoeduCities, []string{"Cluj", "București", "Pitesti"}) {
		t.Fatal("POC city list", config.Jobs.RoeduCities, err)
	}
	for name, cities := range map[string]string{"duplicate": "Cluj,cluj", "empty entry": "Cluj,,Pitesti", "control": "Cluj\nPitesti"} {
		env["ROEDU_SYNC_CITIES"] = cities
		if _, err := configuration(fixtureEnv(env), dev); err == nil || !strings.Contains(err.Error(), "ROEDU_SYNC_CITIES") {
			t.Fatal("invalid city list accepted:", name, err)
		}
	}
	delete(env, "ROEDU_LOCAL_POC")
	env["ROEDU_SYNC_CITIES"] = "Cluj"
	if _, err := configuration(fixtureEnv(env), dev); err == nil || !strings.Contains(err.Error(), "ROEDU_SYNC_CITIES") {
		t.Fatal("city list accepted outside the POC gate", err)
	}
}
