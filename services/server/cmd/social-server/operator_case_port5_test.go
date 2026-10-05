package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/commands"
	"github.com/DobosP/social_media_activities_app/services/server/internal/jobs"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func operatorCase5GoogleClient(t *testing.T, details map[string]any) *http.Client {
	t.Helper()
	return &http.Client{Transport: responseTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("X-Goog-Api-Key") != "synthetic-case5-key" {
			t.Fatal("provider fixture credential changed")
		}
		if r.Method == "POST" {
			raw, _ := json.Marshal(map[string]any{"places": []any{map[string]any{"id": details["id"]}}})
			return jsonResponse(string(raw), 200), nil
		}
		raw, err := json.Marshal(details)
		if err != nil {
			t.Fatal(err)
		}
		return jsonResponse(string(raw), 200), nil
	})}
}

func TestOperatorCase5GoogleTransientLiteralBooleanAndNoMetadataPersistence(t *testing.T) {
	for _, value := range []any{true, false, nil, "true", 1, map[string]any{}} {
		result, err := googleEnricher("synthetic-case5-key", operatorCase5GoogleClient(t, map[string]any{"id": "g9", "currentOpeningHours": map[string]any{"openNow": value}, "googleMapsUri": "https://maps.google/?cid=9"}))(context.Background(), commands.EnrichPlace{Name: "City Sports Hall", Lat: 46.77, Lon: 23.59})
		if err != nil || !result.Resolved {
			t.Fatal("valid durable provider fixture failed", err)
		}
		if want, ok := value.(bool); ok {
			if result.OpenNow == nil || *result.OpenNow != want {
				t.Fatal("literal provider live boolean not returned exactly")
			}
		} else if result.OpenNow != nil {
			t.Fatal("null/malformed openNow was coerced into live status")
		}
		raw, _ := json.Marshal(result.Tags)
		if strings.Contains(string(raw), "openNow") || strings.Contains(string(raw), "open_now") || strings.Contains(string(raw), "currentOpeningHours") {
			t.Fatal("transient status entered durable tags")
		}
	}
}

func TestOperatorCase5GoogleDisabledAndMissingKeyCannotCallProvider(t *testing.T) {
	cfg, err := configuration(fixtureEnv(nil), fixtureOptions())
	if err != nil || cfg.Commands.GoogleEnrich != nil {
		t.Fatal("Google enrichment is not closed by default", err)
	}
	calls := 0
	client := &http.Client{Transport: responseTransport(func(*http.Request) (*http.Response, error) {
		calls++
		t.Fatal("missing-key provider transport reached")
		return nil, nil
	})}
	if _, err := googleEnricher("", client)(context.Background(), commands.EnrichPlace{Name: "Fixture", Lat: 46.77, Lon: 23.59}); err == nil || calls != 0 {
		t.Fatal("missing key activated provider")
	}
	if _, err := configuration(fixtureEnv(map[string]string{"GOOGLE_PLACES_ENABLED": "true"}), fixtureOptions()); err == nil || !strings.Contains(err.Error(), "GOOGLE_PLACES_API_KEY") {
		t.Fatal("explicit enabled/missing-key startup guard changed")
	}
}

func TestPostgresOperatorCase5GoogleActualSQLDurableFieldsAndContactBackfill(t *testing.T) {
	for _, name := range []string{"transient_true", "transient_false", "backfill", "existing_website"} {
		t.Run(name, func(t *testing.T) {
			db := testdb.New(t, *configurationDSN, nil)
			ctx := context.Background()
			place := testdb.Place(t, db, "City Sports Hall", "osm")
			if name == "existing_website" {
				if _, err := db.Exec(ctx, `UPDATE places_place SET website='https://original.example.ro' WHERE id=$1`, place); err != nil {
					t.Fatal(err)
				}
			}
			providerID, mapsURI := "g9", "https://maps.google/?cid=9"
			if strings.HasPrefix(name, "transient") {
				providerID, mapsURI = "g1", "https://maps.google/?cid=1"
			}
			details := map[string]any{"id": providerID, "googleMapsUri": mapsURI, "currentOpeningHours": map[string]any{"openNow": name != "transient_false"}, "websiteUri": "https://sportshall.example.ro", "internationalPhoneNumber": "+40 264 111111", "rating": 4.6, "userRatingCount": 120, "primaryType": "sports_complex"}
			provider := googleEnricher("synthetic-case5-key", operatorCase5GoogleClient(t, details))
			seen := false
			cfg := commands.Config{GoogleEnrich: func(ctx context.Context, p commands.EnrichPlace) (commands.EnrichResult, error) {
				out, err := provider(ctx, p)
				if out.OpenNow == nil || *out.OpenNow != (name != "transient_false") {
					t.Fatal("actual callback lost transient provider boolean")
				}
				seen = true
				return out, err
			}}
			r := jobs.New(db, jobs.DefaultConfig())
			if err := commands.Install(r, cfg); err != nil {
				t.Fatal(err)
			}
			result, err := r.Run(ctx, "enrich_places", rawOptions(`{"google":true}`))
			if err != nil || result.(map[string]int)["google_enriched"] != 1 || !seen {
				t.Fatal("actual native command failed enabled enrichment", err)
			}
			var website, phone string
			var raw []byte
			if err := db.QueryRow(ctx, `SELECT website,phone,raw_tags FROM places_place WHERE id=$1`, place).Scan(&website, &phone, &raw); err != nil {
				t.Fatal(err)
			}
			wanted := "https://sportshall.example.ro"
			if name == "existing_website" {
				wanted = "https://original.example.ro"
			}
			if website != wanted || phone != "+40 264 111111" {
				t.Fatal("durable contact backfill overwrote existing website or lost missing contact")
			}
			var tags map[string]any
			if json.Unmarshal(raw, &tags) != nil {
				t.Fatal("invalid overlay JSON")
			}
			g, ok := tags["google"].(map[string]any)
			if !ok || g["place_id"] != providerID || g["maps_uri"] != mapsURI || g["rating"] != 4.6 || g["rating_count"] != float64(120) || g["primary_type"] != "sports_complex" {
				t.Fatal("exact source durable metadata lost")
			}
			for _, key := range []string{"openNow", "open_now", "currentOpeningHours", "regularOpeningHours", "weekdayDescriptions"} {
				if strings.Contains(string(raw), key) {
					t.Fatal("actual SQL overlay persisted transient provider status", key)
				}
			}
		})
	}
}
