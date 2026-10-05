package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
)

func TestOperatorCase3ROEDUConsistentCompleteSnapshotAcrossPages(t *testing.T) {
	a, b := retirementPackPage(t), retirementPackPage(t)
	items := a["items"].([]any)
	a["items"], b["items"] = []any{items[0]}, []any{items[1]}
	a["pagination"].(map[string]any)["next_cursor"] = "snapshot-bound-c1"
	got, err := retirementReadPages(t, []map[string]any{a, b})
	if err != nil || !got.Complete || got.Snapshot != "sha256-"+strings.Repeat("7", 64) || got.Release != got.Snapshot || got.Pack != SocialPack || len(got.Items) != 2 || got.Items[0]["id"] != "venue-1" || got.Items[1]["id"] != "event-1" {
		t.Fatal("consistent promoted multi-page read lost item order or complete identity")
	}
}

func TestOperatorCase3ROEDUConsumerPolicyMutationsFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		venue  bool
		mutate func(map[string]any)
	}{
		{"stale_policy", false, func(i map[string]any) { i["policy_attestation"].(map[string]any)["ruleset_version"] = 5 }},
		{"lane_mismatch", false, func(i map[string]any) { i["acquisition_lane"] = "web_http" }},
		{"copyright_prose", false, func(i map[string]any) { i["description"] = "copyrighted prose" }},
		{"long_id", false, func(i map[string]any) { i["id"] = strings.Repeat("x", 129) }},
		{"infinite_location", true, func(i map[string]any) { i["location"].(map[string]any)["lat"] = math.Inf(1) }},
		{"long_address", true, func(i map[string]any) { i["address"].(map[string]any)["street"] = strings.Repeat("x", 256) }},
		{"overflow_money", false, func(i map[string]any) { i["price_min"] = json.Number("1" + strings.Repeat("0", 400)) }},
		{"object_obligation", false, func(i map[string]any) {
			i["policy_attestation"].(map[string]any)["obligations"] = []any{map[string]any{}}
		}},
		{"whitespace_url", false, func(i map[string]any) { i["ticket_url"] = "https://tickets.example.test/a b" }},
		{"missing_venue", false, func(i map[string]any) {
			delete(i, "venue_id")
			delete(i, "place_id")
			i["facets"].(map[string]any)["venue_id"] = nil
			i["facets"].(map[string]any)["place_id"] = nil
		}},
		{"object_access", false, func(i map[string]any) { i["access_type"] = map[string]any{} }},
		{"array_lane", false, func(i map[string]any) { i["acquisition_lane"] = []any{} }},
		{"object_availability", false, func(i map[string]any) { i["availability"] = map[string]any{} }},
		{"object_status", false, func(i map[string]any) {
			i["status"] = map[string]any{}
			i["lifecycle_status"] = map[string]any{}
			i["facets"].(map[string]any)["status"] = map[string]any{}
			i["facets"].(map[string]any)["lifecycle_status"] = map[string]any{}
			i["tags"] = []any{"event:concert", "lifecycle:{}"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items := retirementPackPage(t)["items"].([]any)
			item := items[1].(map[string]any)
			if tc.venue {
				item = items[0].(map[string]any)
			}
			if !ValidatePackItem(item) {
				t.Fatal("valid promoted fixture rejected")
			}
			tc.mutate(item)
			if ValidatePackItem(item) {
				t.Fatal("malformed consumer-bound/policy field accepted")
			}
		})
	}
	// The source's stale tombstone case must reject fields removed by the
	// tombstone protocol, even when the extra field is valid on a live event.
	item := retirementPackPage(t)["items"].([]any)[1].(map[string]any)
	item["kind"], item["status"], item["lifecycle_status"], item["cancelled"], item["tombstone"], item["is_tombstone"] = "event_tombstone", "removed", "removed", false, true, true
	item["facets"].(map[string]any)["status"], item["facets"].(map[string]any)["lifecycle_status"] = "removed", "removed"
	item["tags"] = []any{"event:concert", "lifecycle:removed"}
	item["title"] = ""
	for _, key := range []string{"start_datetime", "starts_at", "end_datetime", "ends_at", "timezone", "recurrence", "ticket_url", "price_min", "price_max", "currency", "is_free", "availability"} {
		delete(item, key)
	}
	if !ValidatePackItem(item) {
		t.Fatal("valid tombstone rejected")
	}
	item["timezone"] = "Europe/Bucharest"
	if ValidatePackItem(item) {
		t.Fatal("stale tombstone timezone accepted")
	}
}

func TestOperatorCase3ROEDULocalWithholdingAndCityMismatchCannotReconcile(t *testing.T) {
	for _, name := range []string{"invalid_policy_among_valid", "wrong_city", "missing_city"} {
		t.Run(name, func(t *testing.T) {
			page := retirementPackPage(t)
			items := page["items"].([]any)
			if name == "invalid_policy_among_valid" {
				cloneRaw, _ := json.Marshal(items[1])
				var invalid map[string]any
				_ = json.Unmarshal(cloneRaw, &invalid)
				invalid["id"] = "invalid"
				invalid["policy_attestation"].(map[string]any)["ruleset_version"] = 5
				page["items"] = append(items, invalid)
			} else {
				venue := items[0].(map[string]any)
				page["items"] = []any{venue}
				if name == "wrong_city" {
					venue["facets"].(map[string]any)["city"], venue["address"].(map[string]any)["city"] = "București", "București"
				} else {
					venue["facets"].(map[string]any)["city"], venue["address"].(map[string]any)["city"] = nil, ""
				}
			}
			got, err := retirementReadPages(t, []map[string]any{page})
			if err != nil || got.Complete {
				t.Fatal("local withholding or city mismatch permitted full reconciliation")
			}
			if name == "invalid_policy_among_valid" {
				if len(got.Items) != 2 || got.Items[0]["id"] != "venue-1" || got.Items[1]["id"] != "event-1" {
					t.Fatal("valid records changed when one policy-invalid item was dropped")
				}
			} else if len(got.Items) != 0 {
				t.Fatal("city filter mismatches reached consumer records")
			}
		})
	}
}

func TestPostgresOperatorCase3SyncRefusalIsolationAndNoSideEffectsOnOwnFailure(t *testing.T) {
	for _, name := range []string{"typed_refusal", "transport_failure", "invalid_envelope", "apply_failure"} {
		t.Run(name, func(t *testing.T) {
			r := jobFixture(t)
			ctx := context.Background()
			page := retirementPackPage(t)
			status := 200
			switch name {
			case "typed_refusal":
				page["items"] = []any{}
				page["withheld"] = 1
				page["errors"] = []any{"schema not ready"}
			case "transport_failure":
				status = 503
			case "invalid_envelope":
				delete(page, "snapshot_id")
			case "apply_failure":
				if _, err := r.DB.Exec(ctx, `CREATE FUNCTION operator_case3_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic consumer failure'; END $$; CREATE TRIGGER operator_case3_fail BEFORE INSERT ON places_place FOR EACH ROW EXECUTE FUNCTION operator_case3_failure()`); err != nil {
					t.Fatal(err)
				}
			}
			raw, err := json.Marshal(page)
			if err != nil {
				t.Fatal(err)
			}
			requests, covers := 0, 0
			r.Config.RoeduSyncEnabled = true
			r.Config.Roedu = &RoeduClient{BaseURL: "https://synthetic.invalid", APIKey: "synthetic-case-only", HTTP: &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(raw)), Header: http.Header{}}, nil
			})}}
			r.Config.ResolvePlaceCovers = func(context.Context, map[string]json.RawMessage) (any, error) { covers++; return 0, nil }
			result, err := r.SyncRoedu(ctx)
			if requests != 1 {
				t.Fatal("sync made unbounded/multiple producer requests")
			}
			if name == "typed_refusal" {
				if err != nil || covers != 1 || result["refused"] != true || result["refreshed"] != false {
					t.Fatal("typed external refusal did not preserve shared tick and cover recovery")
				}
				var n int
				if err := r.DB.QueryRow(ctx, `SELECT count(*) FROM safety_auditlog WHERE event='ingestion.roedu_refused'`).Scan(&n); err != nil || n != 1 {
					t.Fatal("refused source was silently treated as empty")
				}
			} else {
				if err == nil || errors.Is(err, ErrProductRefused) || covers != 0 {
					t.Fatal("consumer/transport contract failure ran cover side effects or was swallowed")
				}
			}
			var places, events int
			if err := r.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM places_place),(SELECT count(*) FROM events_event)`).Scan(&places, &events); err != nil || places != 0 || events != 0 {
				t.Fatal("failed/refused sync left ingestion writes")
			}
		})
	}
}
