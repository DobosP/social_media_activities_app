package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/commands"
)

func fetchExternalJSON(ctx context.Context, request *http.Request, maximum int64, timeout time.Duration, injected *http.Client) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request = request.WithContext(ctx)
	var client http.Client
	if injected != nil {
		client = *injected
	} else {
		u := request.URL
		if !safeHTTPS(u.String(), false) {
			return nil, errors.New("external provider URL is invalid")
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
		if err != nil || len(addresses) == 0 {
			return nil, errors.New("external provider unavailable")
		}
		for _, address := range addresses {
			if !publicIngestionIP(address.IP) {
				return nil, errors.New("external provider address is invalid")
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
		client.Transport = transport
	}
	client.Timeout = timeout
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("external provider unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, errors.New("external provider unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil || int64(len(raw)) > maximum {
		return nil, errors.New("external provider response exceeds bound")
	}
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&result) != nil || result == nil || decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("external provider response is invalid")
	}
	return result, nil
}

func googleEnricher(apiKey string, injected *http.Client) func(context.Context, commands.EnrichPlace) (commands.EnrichResult, error) {
	return func(ctx context.Context, place commands.EnrichPlace) (commands.EnrichResult, error) {
		result := commands.EnrichResult{}
		if apiKey == "" {
			return result, errors.New("GOOGLE_PLACES_API_KEY is required")
		}
		if math.IsNaN(place.Lat) || math.IsNaN(place.Lon) || math.IsInf(place.Lat, 0) || math.IsInf(place.Lon, 0) || math.Abs(place.Lat) > 90 || math.Abs(place.Lon) > 180 {
			return result, errors.New("invalid place coordinates")
		}
		query := strings.TrimSpace(strings.TrimSpace(place.Name) + " " + strings.TrimSpace(place.City))
		if query == "" {
			return result, nil
		}
		payload, _ := json.Marshal(map[string]any{"textQuery": query, "maxResultCount": 1, "locationBias": map[string]any{"circle": map[string]any{"center": map[string]float64{"latitude": place.Lat, "longitude": place.Lon}, "radius": 200.0}}})
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://places.googleapis.com/v1/places:searchText", bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json")
		request.Header.Set("X-Goog-Api-Key", apiKey)
		request.Header.Set("X-Goog-FieldMask", "places.id")
		search, err := fetchExternalJSON(ctx, request, 5<<20, 15*time.Second, injected)
		if err != nil {
			return result, err
		}
		places, _ := search["places"].([]any)
		if len(places) == 0 {
			return result, nil
		}
		first, ok := places[0].(map[string]any)
		if !ok {
			return result, errors.New("Google place response is invalid")
		}
		id := stringField(first, "id")
		if !regexp.MustCompile(`^[A-Za-z0-9_-]{1,255}$`).MatchString(id) {
			return result, errors.New("Google place response is invalid")
		}
		request, _ = http.NewRequestWithContext(ctx, http.MethodGet, "https://places.googleapis.com/v1/places/"+id, nil)
		request.Header.Set("Accept", "application/json")
		request.Header.Set("X-Goog-Api-Key", apiKey)
		request.Header.Set("X-Goog-FieldMask", "id,googleMapsUri,currentOpeningHours,regularOpeningHours,websiteUri,internationalPhoneNumber,rating,userRatingCount,primaryType")
		details, err := fetchExternalJSON(ctx, request, 5<<20, 15*time.Second, injected)
		if err != nil {
			return result, err
		}
		reference := stringField(details, "id")
		if reference == "" {
			reference = id
		}
		if reference != id {
			return result, errors.New("Google place identity drift")
		}
		metadata := map[string]any{"place_id": reference}
		if maps := stringField(details, "googleMapsUri"); maps != "" {
			if !safePublicContactURL(maps) || len(maps) > 2048 {
				return result, errors.New("Google place metadata is invalid")
			}
			metadata["maps_uri"] = maps
		}
		if rating, ok := details["rating"]; ok && rating != nil {
			n, valid := number(rating)
			if !valid || n < 0 || n > 5 {
				return result, errors.New("Google rating metadata is invalid")
			}
			if n != 0 {
				metadata["rating"] = rating
			}
		}
		if count, ok := details["userRatingCount"]; ok && count != nil {
			n, valid := number(count)
			if !valid || n < 0 || n > 1000000000 || math.Trunc(n) != n {
				return result, errors.New("Google rating metadata is invalid")
			}
			if n != 0 {
				metadata["rating_count"] = count
			}
		}
		if kind := stringField(details, "primaryType"); kind != "" {
			if len(kind) > 128 || strings.ContainsAny(kind, "\r\n\x00") {
				return result, errors.New("Google type metadata is invalid")
			}
			metadata["primary_type"] = kind
		}
		website := stringField(details, "websiteUri")
		if website != "" && (!safePublicContactURL(website) || len(website) > 500) {
			return result, errors.New("Google website metadata is invalid")
		}
		phone := stringField(details, "internationalPhoneNumber")
		if len(phone) > 50 || strings.ContainsAny(phone, "\r\n\x00") {
			return result, errors.New("Google phone metadata is invalid")
		}
		// Keep literal live status available to the caller, outside durable tags.
		var openNow *bool
		if current, ok := details["currentOpeningHours"].(map[string]any); ok {
			if value, ok := current["openNow"].(bool); ok {
				openNow = &value
			}
		}
		return commands.EnrichResult{Resolved: true, Tags: map[string]any{"google": metadata}, Website: website, Phone: phone, OpenNow: openNow}, nil
	}
}

func wikidataEnricher(endpoint, agent string, injected *http.Client) func(context.Context, []commands.EnrichPlace) (map[int64]commands.EnrichResult, error) {
	return func(ctx context.Context, places []commands.EnrichPlace) (map[int64]commands.EnrichResult, error) {
		byQID := map[string][]commands.EnrichPlace{}
		qids := []string{}
		for _, place := range places {
			if place.Website != "" {
				continue
			}
			qid := stringField(place.Tags, "wikidata")
			if !regexp.MustCompile(`^Q[0-9]{1,20}$`).MatchString(qid) {
				continue
			}
			if len(byQID[qid]) == 0 {
				qids = append(qids, qid)
			}
			byQID[qid] = append(byQID[qid], place)
		}
		out := map[int64]commands.EnrichResult{}
		for start := 0; start < len(qids); start += 50 {
			chunk := qids[start:min(start+50, len(qids))]
			values := []string{}
			for _, qid := range chunk {
				values = append(values, "wd:"+qid)
			}
			query := "SELECT ?item ?website WHERE { VALUES ?item { " + strings.Join(values, " ") + " } OPTIONAL { ?item wdt:P856 ?website. } }"
			u, err := url.Parse(endpoint)
			if err != nil || !safeHTTPS(endpoint, false) {
				return nil, errors.New("WIKIDATA_SPARQL_URL is invalid")
			}
			params := u.Query()
			params.Set("query", query)
			params.Set("format", "json")
			u.RawQuery = params.Encode()
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
			request.Header.Set("User-Agent", agent)
			request.Header.Set("Accept", "application/sparql-results+json")
			response, err := fetchExternalJSON(ctx, request, 10<<20, 30*time.Second, injected)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				continue
			} // Source batches are best-effort.
			results, _ := response["results"].(map[string]any)
			bindings, _ := results["bindings"].([]any)
			if len(bindings) > 100000 {
				return nil, errors.New("Wikidata response exceeds bound")
			}
			allowed := map[string]bool{}
			for _, qid := range chunk {
				allowed[qid] = true
			}
			seen := map[string]bool{}
			for _, row := range bindings {
				binding, ok := row.(map[string]any)
				if !ok {
					continue
				}
				item, _ := binding["item"].(map[string]any)
				websiteRow, _ := binding["website"].(map[string]any)
				itemURI := stringField(item, "value")
				pos := strings.LastIndex(itemURI, "/")
				if pos < 0 {
					continue
				}
				qid := itemURI[pos+1:]
				website := stringField(websiteRow, "value")
				if !allowed[qid] || seen[qid] || website == "" || !safePublicContactURL(website) {
					continue
				}
				if len(website) > 500 {
					website = website[:500]
					if !safePublicContactURL(website) {
						continue
					}
				}
				seen[qid] = true
				for _, place := range byQID[qid] {
					out[place.ID] = commands.EnrichResult{Resolved: true, Website: website, Tags: map[string]any{"wikidata": qid, "wikidata_enriched": true}}
				}
			}
		}
		return out, nil
	}
}
func safePublicContactURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") || strings.ContainsAny(raw, "\r\n\x00\\") || strings.TrimSpace(raw) != raw {
		return false
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		if !publicIngestionIP(ip) {
			return false
		}
	}
	if loopbackHost(u.Hostname()) || strings.EqualFold(u.Hostname(), "localhost") || strings.HasSuffix(strings.ToLower(u.Hostname()), ".local") {
		return false
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return true
}
