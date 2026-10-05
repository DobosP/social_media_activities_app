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

func TestOperatorCase4WikidataExactQIDAndNoCandidateTransportCases(t *testing.T) {
	for _, tc := range []struct {
		tags    map[string]any
		website string
		calls   int
	}{{map[string]any{"wikidata": "Q42"}, "", 1}, {map[string]any{"wikidata": "notaqid"}, "", 0}, {map[string]any{}, "", 0}, {map[string]any{"wikidata": "Q42"}, "https://already.example/", 0}} {
		calls := 0
		client := &http.Client{Transport: responseTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if !strings.Contains(r.URL.Query().Get("query"), "wd:Q42 ") {
				t.Fatal("source QID was not retained in exact provider query")
			}
			return jsonResponse(`{"results":{"bindings":[{"item":{"value":"http://www.wikidata.org/entity/Q42"},"website":{"value":"https://example.org/"}}]}}`, 200), nil
		})}
		result, err := wikidataEnricher("https://query.wikidata.org/sparql", "synthetic-case4", client)(context.Background(), []commands.EnrichPlace{{ID: 42, Website: tc.website, Tags: tc.tags}})
		if err != nil || calls != tc.calls || len(result) != tc.calls {
			t.Fatal("QID validation/existing-website/no-QID provider call contract changed", calls, tc.calls, err)
		}
		if tc.calls == 1 && result[42].Tags["wikidata"] != "Q42" {
			t.Fatal("validated QID was not preserved in native enrichment result")
		}
	}
}

func TestPostgresOperatorCase4WikidataCommandPersistsExactBackfillAndMarkers(t *testing.T) {
	for _, qid := range []string{"Q42", "Q7"} {
		t.Run(qid, func(t *testing.T) {
			db := testdb.New(t, *configurationDSN, nil)
			ctx := context.Background()
			place := testdb.Place(t, db, "Central Library", "osm")
			tags, _ := json.Marshal(map[string]any{"wikidata": qid})
			if _, err := db.Exec(ctx, `UPDATE places_place SET raw_tags=$2,website='' WHERE id=$1`, place, tags); err != nil {
				t.Fatal(err)
			}
			website := "https://example.org/"
			if qid == "Q7" {
				website = "https://lib.example/"
			}
			calls := 0
			client := &http.Client{Transport: responseTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if !strings.Contains(r.URL.Query().Get("query"), "wd:"+qid+" ") {
					t.Fatal("command did not use actual entity provider")
				}
				raw, _ := json.Marshal(map[string]any{"results": map[string]any{"bindings": []any{map[string]any{"item": map[string]string{"value": "http://www.wikidata.org/entity/" + qid}, "website": map[string]string{"value": website}}}}})
				return jsonResponse(string(raw), 200), nil
			})}
			r := jobs.New(db, jobs.DefaultConfig())
			if err := commands.Install(r, commands.Config{WikidataEnrich: wikidataEnricher("https://query.wikidata.org/sparql", "synthetic-case4", client)}); err != nil {
				t.Fatal(err)
			}
			result, err := r.Run(ctx, "enrich_places", rawOptions(`{"wikidata":true}`))
			if err != nil || result.(map[string]int)["wikidata_enriched"] != 1 || calls != 1 {
				t.Fatal("actual enrich_places --wikidata path did not report one backfill", err)
			}
			var stored, retainedQID string
			var checked bool
			if err := db.QueryRow(ctx, `SELECT website,raw_tags->>'wikidata',raw_tags->'wikidata_enriched'='true'::jsonb FROM places_place WHERE id=$1`, place).Scan(&stored, &retainedQID, &checked); err != nil || stored != website || retainedQID != qid || !checked {
				t.Fatal("source website/QID/enriched marker not persisted exactly", err)
			}
		})
	}
}
