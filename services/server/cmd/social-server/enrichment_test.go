package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/commands"
)

func jsonResponse(body string, status int) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": []string{"application/json"}}}
}
func TestGoogleNativeEnrichmentStoresOnlyDurableSourceFields(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: responseTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Host != "places.googleapis.com" || request.Header.Get("X-Goog-Api-Key") != "synthetic-provider-key" {
			t.Fatal("provider credential or destination mismatch")
		}
		if calls == 1 {
			if request.Method != http.MethodPost || request.Header.Get("X-Goog-FieldMask") != "places.id" {
				t.Fatal("search request mismatch")
			}
			var body map[string]any
			if json.NewDecoder(request.Body).Decode(&body) != nil || body["textQuery"] != "Fixture Park Cluj-Napoca" || body["maxResultCount"] != float64(1) {
				t.Fatal("search payload mismatch")
			}
			return jsonResponse(`{"places":[{"id":"ChIJ_fixture_id"}]}`, 200), nil
		}
		if request.Method != http.MethodGet || request.URL.Path != "/v1/places/ChIJ_fixture_id" || !strings.Contains(request.Header.Get("X-Goog-FieldMask"), "currentOpeningHours") {
			t.Fatal("details request mismatch")
		}
		return jsonResponse(`{"id":"ChIJ_fixture_id","googleMapsUri":"https://maps.google.com/?cid=1","currentOpeningHours":{"openNow":true},"regularOpeningHours":{"weekdayDescriptions":["Always"]},"websiteUri":"https://venue.fixture.test","internationalPhoneNumber":"+40 123 456 789","rating":4.2,"userRatingCount":50,"primaryType":"park"}`, 200), nil
	})}
	result, err := googleEnricher("synthetic-provider-key", client)(context.Background(), commands.EnrichPlace{ID: 1, Name: "Fixture Park", City: "Cluj-Napoca", Lat: 46.7, Lon: 23.6})
	if err != nil || !result.Resolved || calls != 2 || result.Website != "https://venue.fixture.test" || result.Phone != "+40 123 456 789" {
		t.Fatalf("unexpected enrichment: %v", err)
	}
	raw, err := json.Marshal(result.Tags)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "openNow") || strings.Contains(string(raw), "regularOpeningHours") || strings.Contains(string(raw), "weekdayDescriptions") || len(result.Tags) != 1 {
		t.Fatal("transient Google status persisted")
	}
	metadata := result.Tags["google"].(map[string]any)
	if metadata["place_id"] != "ChIJ_fixture_id" || metadata["primary_type"] != "park" || len(metadata) != 5 {
		t.Fatal("durable source metadata missing")
	}
}
func TestGoogleNativeProviderRejectsMalformedAndPrivateMetadata(t *testing.T) {
	for _, body := range []string{`{"places":[{"id":"../private-key-value"}]}`, `{"places":[{}]}`, `{"places":["invalid"]}`, `[]`, `{"places":[]} {}`, strings.Repeat(" ", (5<<20)+1)} {
		client := &http.Client{Transport: responseTransport(func(*http.Request) (*http.Response, error) { return jsonResponse(body, 200), nil })}
		_, err := googleEnricher("synthetic-provider-key", client)(context.Background(), commands.EnrichPlace{Name: "Fixture", Lat: 46, Lon: 23})
		if err == nil {
			t.Fatal("malformed Google provider accepted")
		}
		if strings.Contains(err.Error(), "private-key-value") {
			t.Fatal("provider error leaked input")
		}
	}
	client := &http.Client{Transport: responseTransport(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodPost {
			return jsonResponse(`{"places":[{"id":"fixture"}]}`, 200), nil
		}
		return jsonResponse(`{"websiteUri":"http://127.0.0.1/private"}`, 200), nil
	})}
	if _, err := googleEnricher("synthetic-provider-key", client)(context.Background(), commands.EnrichPlace{Name: "Fixture", Lat: 46, Lon: 23}); err == nil {
		t.Fatal("private contact URL accepted")
	}
}
func TestWikidataNativeProviderBatchesDeduplicatesAndBackfillsMissingWebsite(t *testing.T) {
	places := []commands.EnrichPlace{}
	for i := 1; i <= 52; i++ {
		places = append(places, commands.EnrichPlace{ID: int64(i), Tags: map[string]any{"wikidata": "Q" + strconv.Itoa(i)}})
	}
	places = append(places, commands.EnrichPlace{ID: 101, Tags: map[string]any{"wikidata": "Q1"}}, commands.EnrichPlace{ID: 102, Website: "https://present.fixture.test", Tags: map[string]any{"wikidata": "Qexisting"}}, commands.EnrichPlace{ID: 103, Tags: map[string]any{"wikidata": "Q1 } SELECT ?private"}})
	calls := 0
	client := &http.Client{Transport: responseTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		query := request.URL.Query().Get("query")
		if request.Header.Get("User-Agent") != "native-fixture" || request.Header.Get("Accept") != "application/sparql-results+json" || strings.Contains(query, "private") {
			t.Fatal("SPARQL request invalid")
		}
		if calls == 1 {
			if strings.Contains(query, "wd:Q51 ") || strings.Count(query, "wd:") != 50 {
				t.Fatal("SPARQL batch not bounded/deduplicated")
			}
			return jsonResponse(`{"results":{"bindings":[{"item":{"value":"http://www.wikidata.org/entity/Q1"},"website":{"value":"https://venue.fixture.test/first"}},{"item":{"value":"http://www.wikidata.org/entity/Q1"},"website":{"value":"https://venue.fixture.test/ignored"}},{"item":{"value":"http://www.wikidata.org/entity/Q999"},"website":{"value":"https://venue.fixture.test/foreign"}}]}}`, 200), nil
		}
		if strings.Count(query, "wd:") != 2 {
			t.Fatal("final SPARQL batch not bounded")
		}
		return jsonResponse(`{"results":{"bindings":[{"item":{"value":"http://www.wikidata.org/entity/Q52"},"website":{"value":"https://venue.fixture.test/last"}}]}}`, 200), nil
	})}
	result, err := wikidataEnricher("https://query.wikidata.org/sparql", "native-fixture", client)(context.Background(), places)
	if err != nil || calls != 2 || len(result) != 3 {
		t.Fatalf("unexpected Wikidata enrichment: %v", err)
	}
	if result[1].Website != "https://venue.fixture.test/first" || result[101].Website != result[1].Website || result[52].Tags["wikidata_enriched"] != true {
		t.Fatal("first website/marker not preserved")
	}
	if _, ok := result[102]; ok {
		t.Fatal("existing website overwritten")
	}
}
func TestConfiguredEnrichmentRemainsOptIn(t *testing.T) {
	cfg, err := configuration(fixtureEnv(nil), fixtureOptions())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Commands.GoogleEnrich != nil || cfg.Commands.WikidataEnrich == nil {
		t.Fatal("provider opt-in changed")
	}
	_, err = configuration(fixtureEnv(map[string]string{"GOOGLE_PLACES_ENABLED": "true"}), fixtureOptions())
	if err == nil || !strings.Contains(err.Error(), "GOOGLE_PLACES_API_KEY") {
		t.Fatal("paid provider lacks explicit credential gate")
	}
	cfg, err = configuration(fixtureEnv(map[string]string{"GOOGLE_PLACES_ENABLED": "true", "GOOGLE_PLACES_API_KEY": "synthetic-fixture-only-key", "WIKIDATA_SPARQL_URL": "https://query.fixture.test/sparql"}), fixtureOptions())
	if err != nil || cfg.Commands.GoogleEnrich == nil {
		t.Fatal("configured native enrichers missing")
	}
}
