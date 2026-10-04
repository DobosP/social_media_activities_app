package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/commands"
	"github.com/DobosP/social_media_activities_app/services/server/internal/jobs"
)

type roeduPlaces struct{ client *jobs.RoeduClient }

func (s roeduPlaces) Fetch(ctx context.Context, o commands.PlaceOptions, emit func(commands.RawPlace) error) error {
	if s.client == nil || s.client.APIKey == "" {
		return errors.New("ROEDU_API_KEY is required")
	}
	if o.BBox != "" {
		return errors.New("native RO-EDU places supports canonical city scope")
	}
	pack, err := s.client.Read(ctx, o.City)
	if err != nil {
		return err
	}
	emitted := 0
	for _, item := range pack.Items {
		if item["kind"] != "venue" {
			continue
		}
		if o.Limit > 0 && emitted >= o.Limit {
			break
		}
		loc, _ := item["location"].(map[string]any)
		address, _ := item["address"].(map[string]any)
		lon, lonOK := number(loc["lon"])
		lat, latOK := number(loc["lat"])
		if !lonOK || !latOK {
			return errors.New("invalid canonical RO-EDU venue")
		}
		tags := jobs.RoeduVenueTags(item)
		tags["roedu_app_pack"], tags["roedu_release_id"], tags["roedu_snapshot_id"] = pack.Pack, pack.Release, pack.Snapshot
		raw := commands.RawPlace{Source: "roedu", ExternalID: stringField(item, "id"), Name: stringField(item, "title"), Lon: lon, Lat: lat, Tags: tags, Address: map[string]string{"street": stringField(address, "street"), "city": stringField(address, "city"), "county": stringField(address, "county"), "country": stringField(address, "country")}, Website: stringField(item, "website"), Attribution: stringField(item, "source"), License: stringField(item, "license")}
		if raw.Address["country"] == "" {
			raw.Address["country"] = "RO"
		}
		if err = emit(raw); err != nil {
			return err
		}
		emitted++
	}
	return nil
}
func stringField(m map[string]any, key string) string { v, _ := m[key].(string); return v }
func number(value any) (float64, bool) {
	switch v := value.(type) {
	case json.Number:
		n, e := v.Float64()
		return n, e == nil
	case float64:
		return v, true
	}
	return 0, false
}

// Optional ingestion is reached only through an explicit one-shot command. DNS
// addresses are all checked and a checked address is pinned for this request.
func overpassFetch(agent string) func(context.Context, string, string) ([]byte, error) {
	return func(ctx context.Context, endpoint, query string) ([]byte, error) {
		u, e := url.Parse(endpoint)
		if e != nil || !safeHTTPS(endpoint, false) || u.RawQuery != "" {
			return nil, errors.New("OVERPASS_URL is invalid")
		}
		ctx, cancel := context.WithTimeout(ctx, 190*time.Second)
		defer cancel()
		addresses, e := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
		if e != nil || len(addresses) == 0 {
			return nil, errors.New("Overpass unavailable")
		}
		for _, address := range addresses {
			if !publicIngestionIP(address.IP) {
				return nil, errors.New("Overpass address is invalid")
			}
		}
		port := u.Port()
		if port == "" {
			port = "443"
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(addresses[0].IP.String(), port))
		}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 190 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		request, e := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(url.Values{"data": {query}}.Encode()))
		if e != nil {
			return nil, errors.New("Overpass request is invalid")
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("User-Agent", agent)
		request.Header.Set("Accept", "application/json")
		response, e := client.Do(request)
		if e != nil {
			return nil, errors.New("Overpass unavailable")
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return nil, errors.New("Overpass unavailable")
		}
		raw, e := io.ReadAll(io.LimitReader(response.Body, (64<<20)+1))
		if e != nil || len(raw) > 64<<20 {
			return nil, errors.New("Overpass response exceeds bound or is unavailable")
		}
		return raw, nil
	}
}
func publicIngestionIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	for _, block := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "::/96", "64:ff9b:1::/48", "100::/64", "2001::/23", "2001:db8::/32", "2002::/16", "3fff::/20", "fc00::/7", "fe80::/10"} {
		_, network, _ := net.ParseCIDR(block)
		if network.Contains(ip) {
			return false
		}
	}
	return true
}
