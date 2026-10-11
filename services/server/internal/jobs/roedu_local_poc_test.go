package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

// ADR-0057 / local-POC contract v1. The POC fixture is the real producer's
// publish_source DENY (privacy `unknown`) over the production fixture's own
// evidence, plus the contract admission (testdata/generate_roedu_local_poc_fixture.py).

func pocDecode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var page map[string]any
	if err := decoder.Decode(&page); err != nil {
		t.Fatal(err)
	}
	return page
}
func pocPage(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("testdata/roedu-local-poc-page.json")
	if err != nil {
		t.Fatal(err)
	}
	return pocDecode(t, raw)
}
func pocItems(t *testing.T) []map[string]any {
	t.Helper()
	items := []map[string]any{}
	for _, item := range pocPage(t)["items"].([]any) {
		items = append(items, item.(map[string]any))
	}
	return items
}

// deepCopy keeps json.Number values, so a mutated copy differs only where set.
func deepCopy(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return pocDecode(t, raw)
}
func pocClient(t *testing.T, base string, local bool, pages ...[]byte) *RoeduClient {
	t.Helper()
	calls := 0
	return &RoeduClient{BaseURL: base, APIKey: "generated-public-test-key", LocalPOC: local, HTTP: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		page := pages[min(calls, len(pages)-1)]
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(page)), Header: make(http.Header)}, nil
	})}}
}
func pocMarshal(t *testing.T, page map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestLocalPOCAdmissionKnownAnswerVectors(t *testing.T) {
	kat := map[string]any{
		"schema_version":         json.Number("1"),
		"kind":                   "owner_local_development_override",
		"not_a_legal_basis":      true,
		"underlying_decision_id": "pd-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"waived_reasons":         []any{"copyright_unknown", "personal_data_not_cleared", "privacy_personal_data"},
		"poc_root":               "/mnt/data/roedu-local-poc",
	}
	if got, ok := LocalPOCAdmissionDigest(kat); !ok || got != "974486a47fc9191208ee3b8c6dc8c8a2584303ae5e8b7570336285324c3d2681" {
		t.Fatal("KAT-1", got, ok)
	}
	kat["poc_root"] = "/mnt/data/roedu-local-poc/ă<&>"
	if got, ok := LocalPOCAdmissionDigest(kat); !ok || got != "b354282b94e0f0c1a3ff9a2a12036899a17b9ad9df22156adaa3375f028bb650" {
		t.Fatal("KAT-2 (UTF-8, no HTML escaping)", got, ok)
	}
	// Python leaves U+2028 literal and escapes controls; Go's encoder would not match.
	if canonicalString("a b\"\\\n\x01") != "\"a b\\\"\\\\\\n\\u0001\"" {
		t.Fatal("canonical string escaping drifted from the Python encoder")
	}
}

func TestLocalPOCProductionValidatorRefusesEveryPOCItem(t *testing.T) {
	for _, item := range pocItems(t) {
		if ValidatePackItem(item) {
			t.Fatal("production validator accepted a real deny", item["id"])
		}
		if !ValidateLocalPOCPackItem(item) {
			t.Fatal("verified POC item refused", item["id"])
		}
		// Honest values are carried, never the clean constants.
		if item["gdpr_relevant"] != true || item["privacy_classification"] != "unknown" || item["policy_attestation"].(map[string]any)["effect"] != "deny" {
			t.Fatal("POC fixture lost its real privacy values")
		}
	}
}

// With no admission present the POC validator is the production validator.
func TestLocalPOCValidatorEqualsProductionWithoutAdmission(t *testing.T) {
	raw, err := os.ReadFile("testdata/roedu-validator.json")
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var rows []struct {
		Item  map[string]any
		Valid bool
	}
	if err = decoder.Decode(&rows); err != nil {
		t.Fatal(err)
	}
	for i, row := range rows {
		if ValidateLocalPOCPackItem(row.Item) != row.Valid || validatePageItem(row.Item, false) != row.Valid {
			t.Fatalf("row %d diverged from the production oracle", i)
		}
	}
}

func TestLocalPOCAdmissionNegatives(t *testing.T) {
	base := pocItems(t)[1]
	if base["kind"] != "event" {
		t.Fatal("fixture order")
	}
	redigest := func(item map[string]any) {
		admission := item["local_poc_admission"].(map[string]any)
		digest, ok := LocalPOCAdmissionDigest(admission)
		if ok {
			admission["admission_digest"] = digest
		}
	}
	cases := map[string]func(item, admission, attestation map[string]any){
		"effect allow":              func(_, _, att map[string]any) { att["effect"] = "allow" },
		"action not publish_source": func(_, _, att map[string]any) { att["action"] = "analyze" },
		"reason not waived":         func(_, _, att map[string]any) { att["reasons"] = append(att["reasons"].([]any), "copyright_unknown") },
		"waived reason not denied": func(item, adm, _ map[string]any) {
			adm["waived_reasons"] = []any{"capability_denied", "copyright_unknown", "personal_data_not_cleared", "privacy_unknown"}
			redigest(item)
		},
		"non-waivable reason": func(item, adm, att map[string]any) {
			att["reasons"] = []any{"capture_id_missing", "privacy_unknown"}
			adm["waived_reasons"] = []any{"capture_id_missing", "privacy_unknown"}
			redigest(item)
		},
		"waived unsorted": func(item, adm, _ map[string]any) {
			adm["waived_reasons"] = []any{"privacy_unknown", "capability_denied", "personal_data_not_cleared"}
			redigest(item)
		},
		"waived duplicate": func(item, adm, att map[string]any) {
			att["reasons"] = []any{"privacy_unknown", "privacy_unknown"}
			adm["waived_reasons"] = []any{"privacy_unknown", "privacy_unknown"}
			redigest(item)
		},
		"digest tampered": func(_, adm, _ map[string]any) {
			adm["admission_digest"] = strings.Repeat("0", 64)
		},
		"relative poc_root":       func(item, adm, _ map[string]any) { adm["poc_root"] = "mnt/poc"; redigest(item) },
		"kind":                    func(item, adm, _ map[string]any) { adm["kind"] = "legal_basis"; redigest(item) },
		"not_a_legal_basis false": func(item, adm, _ map[string]any) { adm["not_a_legal_basis"] = false; redigest(item) },
		"schema_version 2":        func(item, adm, _ map[string]any) { adm["schema_version"] = json.Number("2"); redigest(item) },
		"schema_version string":   func(item, adm, _ map[string]any) { adm["schema_version"] = "1"; redigest(item) },
		"extra admission key":     func(item, adm, _ map[string]any) { adm["note"] = "x"; redigest(item) },
		"missing admission key":   func(item, adm, _ map[string]any) { delete(adm, "poc_root") },
		"underlying id mismatch": func(item, adm, _ map[string]any) {
			adm["underlying_decision_id"] = strings.Repeat("a", 64)
			redigest(item)
		},
		"item decision id mismatch": func(item, _, _ map[string]any) { item["policy_decision_id"] = strings.Repeat("b", 64) },
		"gdpr_relevant not bool":    func(item, _, _ map[string]any) { item["gdpr_relevant"] = json.Number("1") },
		"privacy class malformed":   func(item, _, _ map[string]any) { item["privacy_classification"] = "Personal Data" },
		"integrity: subject":        func(_, _, att map[string]any) { att["subject_sha256"] = strings.Repeat("c", 64) },
		"integrity: ruleset":        func(_, _, att map[string]any) { att["ruleset_version"] = json.Number("7") },
		"shape: description":        func(item, _, _ map[string]any) { item["description"] = "private prose" },
		"admission not object":      func(item, _, _ map[string]any) { item["local_poc_admission"] = "admitted" },
	}
	for name, mutate := range cases {
		item := deepCopy(t, base)
		admission, _ := item["local_poc_admission"].(map[string]any)
		mutate(item, admission, item["policy_attestation"].(map[string]any))
		if ValidateLocalPOCPackItem(item) {
			t.Fatal("POC validator accepted", name)
		}
		if ValidatePackItem(item) {
			t.Fatal("production validator accepted", name)
		}
	}
	// A clean allow item with an admission bolted on is not a POC item.
	raw, err := os.ReadFile("testdata/roedu-page.json")
	if err != nil {
		t.Fatal(err)
	}
	allowed := pocDecode(t, raw)["items"].([]any)[1].(map[string]any)
	allowed["local_poc_admission"] = base["local_poc_admission"]
	if ValidateLocalPOCPackItem(allowed) {
		t.Fatal("admission accepted over an allow decision")
	}
}

func TestLocalPOCReadGate(t *testing.T) {
	poc := pocMarshal(t, pocPage(t))
	read, err := pocClient(t, "http://127.0.0.1:8077", true, poc).Read(context.Background(), "Cluj-Napoca")
	if err != nil || !read.LocalPOC || read.Complete || len(read.Items) != 2 {
		t.Fatal("gated POC read", read.LocalPOC, read.Complete, len(read.Items), err)
	}
	if read.Items[0]["gdpr_relevant"] != true || read.Items[1]["local_poc_admission"] == nil {
		t.Fatal("POC read rewrote the honest item")
	}
	for name, client := range map[string]*RoeduClient{
		"production client":    pocClient(t, "http://127.0.0.1:8077", false, poc),
		"non-loopback host":    pocClient(t, "https://data.example.org", true, poc),
		"loopback-looking DNS": pocClient(t, "http://127.0.0.1.example.org", true, poc),
		"userinfo":             pocClient(t, "http://user@127.0.0.1:8077", true, poc),
	} {
		if read, err := client.Read(context.Background(), "Cluj-Napoca"); err == nil || len(read.Items) != 0 || read.LocalPOC {
			t.Fatal("POC page accepted with the gate off:", name)
		}
	}
	for name, value := range map[string]any{"false": false, "string": "true", "number": 1, "null": nil} {
		page := pocPage(t)
		page["local_poc"] = value
		if _, err := pocClient(t, "http://127.0.0.1:8077", true, pocMarshal(t, page)).Read(context.Background(), "Cluj-Napoca"); err == nil {
			t.Fatal("POC envelope accepted with local_poc", name)
		}
	}
	// Admission items on a page that does not declare local_poc are refused like
	// any other off-contract item: nothing remains, so the product is refused.
	page := pocPage(t)
	delete(page, "local_poc")
	if _, err := pocClient(t, "http://127.0.0.1:8077", true, pocMarshal(t, page)).Read(context.Background(), "Cluj-Napoca"); !errors.Is(err, ErrProductRefused) {
		t.Fatal("undeclared POC items accepted", err)
	}
	// A production page through a POC client is read exactly as production.
	production, err := os.ReadFile("testdata/roedu-page.json")
	if err != nil {
		t.Fatal(err)
	}
	read, err = pocClient(t, "http://localhost:8077", true, production).Read(context.Background(), "Cluj-Napoca")
	if err != nil || read.LocalPOC || !read.Complete || len(read.Items) != 2 {
		t.Fatal("production page through a POC client", read.LocalPOC, read.Complete, err)
	}
	// Every page of one snapshot must agree on local_poc.
	first := pocPage(t)
	first["pagination"] = map[string]any{"next_cursor": "page-2"}
	second := pocPage(t)
	delete(second, "local_poc")
	second["items"] = []any{}
	if _, err := pocClient(t, "http://[::1]:8077", true, pocMarshal(t, first), pocMarshal(t, second)).Read(context.Background(), "Cluj-Napoca"); err == nil || !strings.Contains(err.Error(), "drift") {
		t.Fatal("POC marker drift between pages accepted", err)
	}
	// A city with no items on a POC page is a product refusal (never listed by
	// ROEDU_SYNC_CITIES; the multi-city sync skips it).
	if _, err := pocClient(t, "http://127.0.0.1:8077", true, poc).Read(context.Background(), "Pitesti"); !errors.Is(err, ErrProductRefused) {
		t.Fatal("empty POC city", err)
	}
}

func TestLocalPOCApplyAndMultiCityRefusedWithoutGate(t *testing.T) {
	items := pocItems(t)
	pack := PackRead{Items: items, Pack: SocialPack, Snapshot: "sha256-" + strings.Repeat("7", 64), Release: "sha256-" + strings.Repeat("7", 64), Generated: "2026-07-12T08:00:00+00:00", Mode: "partial", LocalPOC: true}
	for name, client := range map[string]*RoeduClient{"no client": nil, "production client": {BaseURL: "http://127.0.0.1:8077"}, "remote POC client": {BaseURL: "https://data.example.org", LocalPOC: true}} {
		config := DefaultConfig()
		config.Roedu = client
		r := New(nil, config)
		for _, dry := range []bool{true, false} {
			if _, err := r.ApplyRoeduWithOptions(context.Background(), pack, "Cluj-Napoca", RoeduApplyOptions{MinConfidence: 1, DryRun: dry}); !errors.Is(err, ErrLocalPOCRefused) {
				t.Fatal("POC pack applied with the gate off:", name, dry, err)
			}
		}
	}
	config := DefaultConfig()
	config.RoeduSyncEnabled = true
	config.Roedu = &RoeduClient{BaseURL: "https://data.example.org", APIKey: "generated-public-test-key"}
	config.RoeduCities = []string{"Cluj", "București"}
	if _, err := New(nil, config).SyncRoedu(context.Background()); !errors.Is(err, ErrLocalPOCRefused) {
		t.Fatal("multi-city sync ran outside the POC gate", err)
	}
	// Dry-run under the gate validates with the POC validator; nothing is written.
	config = DefaultConfig()
	config.Roedu = &RoeduClient{BaseURL: "http://127.0.0.1:8077", LocalPOC: true}
	summary, err := New(nil, config).ApplyRoeduWithOptions(context.Background(), pack, "Cluj-Napoca", RoeduApplyOptions{MinConfidence: LocalPOCMinConfidence, DryRun: true})
	if err != nil || summary["local_poc"] != true || summary["venues"] != 1 || summary["events"] != 1 || summary["complete"] != false {
		t.Fatal("gated POC dry run", summary, err)
	}
	if options := roeduSyncOptions(pack); options.MinConfidence != 0.9 {
		t.Fatal("POC scheduled sync floor", options)
	}
	pack.LocalPOC = false
	if options := roeduSyncOptions(pack); options.MinConfidence != 1 {
		t.Fatal("production scheduled sync floor changed", options)
	}
}

func TestLocalPOCVenueTagsOnlyForPOCPacks(t *testing.T) {
	item := pocItems(t)[0]
	tags := map[string]any{}
	LocalPOCTags(tags, PackRead{}, item)
	if len(tags) != 0 {
		t.Fatal("production pack tagged", tags)
	}
	LocalPOCTags(tags, PackRead{LocalPOC: true}, item)
	if tags["roedu_local_poc"] != true || tags["roedu:gdpr_relevant"] != true || tags["roedu:privacy_classification"] != "unknown" || tags["roedu:local_poc_admission"] == nil {
		t.Fatal("POC venue evidence", tags)
	}
}
