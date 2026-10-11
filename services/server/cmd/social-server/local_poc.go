package main

import (
	"net/url"
	"strings"

	"github.com/DobosP/social_media_activities_app/services/server/internal/jobs"
)

// localPOCGate is the consumer half of the local-POC mode gate (ADR-0057,
// cross-repo contract v1 §4): ROEDU_LOCAL_POC exactly "1", the existing
// development mode (--dev / DJANGO_DEBUG) without --dev-container, the
// server-rendered UI (the POC marker lives in its templates), and a loopback
// ROEDU_API_URL. Unset is production and changes nothing. Set but any other
// condition failing refuses startup rather than running a silent half-POC.
func localPOCGate(d *decoder, devContainer, development, reactUI bool, apiURL string) bool {
	if !d.isSet("ROEDU_LOCAL_POC") {
		return false
	}
	parsed, err := url.Parse(apiURL)
	if d.get("ROEDU_LOCAL_POC") != "1" || !development || devContainer || reactUI || apiURL == "" || err != nil || !safeHTTPS(apiURL, true) || !jobs.LoopbackHost(parsed.Hostname()) {
		d.invalid("ROEDU_LOCAL_POC")
		return false
	}
	return true
}

// roeduSyncCities parses ROEDU_SYNC_CITIES: a comma list of exact producer
// city facets, accepted only under the POC gate (production keeps the single
// ROEDU_SYNC_CITY job byte-identical).
func roeduSyncCities(d *decoder, raw string, localPOC bool) []string {
	if !localPOC {
		d.invalid("ROEDU_SYNC_CITIES")
		return nil
	}
	cities := []string{}
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		city := strings.TrimSpace(part)
		folded := strings.ToLower(city)
		if city == "" || len(city) > 160 || strings.ContainsAny(city, "\x00\r\n") || seen[folded] || len(cities) >= 200 {
			d.invalid("ROEDU_SYNC_CITIES")
			return nil
		}
		seen[folded] = true
		cities = append(cities, city)
	}
	return cities
}
