package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

// Reuse the producer-deny fixture and its verified local admission. The literal
// TDM access label is the only changed fact; no legal or privacy clearance is added.
func tdmPOCPage(t *testing.T) map[string]any {
	t.Helper()
	page := pocPage(t)
	for _, raw := range page["items"].([]any) {
		raw.(map[string]any)["access_type"] = "tdm_exception"
	}
	return page
}

func TestLocalPOCTDMVerifiedAccessKeepsOriginalItems(t *testing.T) {
	for _, raw := range tdmPOCPage(t)["items"].([]any) {
		item := raw.(map[string]any)
		t.Run(item["kind"].(string), func(t *testing.T) {
			before := deepCopy(t, item)
			if ValidatePackItem(item) || validatePageItem(item, false) {
				t.Fatal("public validation accepted TDM local evidence")
			}
			if !ValidateLocalPOCPackItem(item) || !validatePageItem(item, true) {
				t.Fatal("verified local TDM item was refused")
			}
			if !reflect.DeepEqual(item, before) {
				t.Fatal("validation rewrote the original access, privacy or deny evidence")
			}
		})
	}
}

func TestLocalPOCTDMWithoutAdmissionRemainsStrict(t *testing.T) {
	raw, err := os.ReadFile("testdata/roedu-page.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, rawItem := range pocDecode(t, raw)["items"].([]any) {
		item := rawItem.(map[string]any)
		if !ValidatePackItem(item) || !ValidateLocalPOCPackItem(item) {
			t.Fatal("original public fixture is invalid")
		}
		item["access_type"] = "tdm_exception"
		if ValidatePackItem(item) || ValidateLocalPOCPackItem(item) {
			t.Fatal("TDM access accepted without a verified deny admission")
		}
	}
}

func TestLocalPOCTDMAdmissionShapeAndIntegrityRefusals(t *testing.T) {
	redigest := func(item map[string]any) {
		admission, _ := item["local_poc_admission"].(map[string]any)
		if digest, ok := LocalPOCAdmissionDigest(admission); ok {
			admission["admission_digest"] = digest
		}
	}
	cases := map[string]func(map[string]any){
		"missing admission": func(item map[string]any) { delete(item, "local_poc_admission") },
		"forged admission digest": func(item map[string]any) {
			item["local_poc_admission"].(map[string]any)["admission_digest"] = strings.Repeat("0", 64)
		},
		"admission not object": func(item map[string]any) { item["local_poc_admission"] = true },
		"relative root": func(item map[string]any) {
			item["local_poc_admission"].(map[string]any)["poc_root"] = "mnt/poc"
			redigest(item)
		},
		"root wrong type": func(item map[string]any) {
			item["local_poc_admission"].(map[string]any)["poc_root"] = true
		},
		"root changed after admission": func(item map[string]any) {
			item["local_poc_admission"].(map[string]any)["poc_root"] = "/other-local-root"
		},
		"waiver claims legal basis": func(item map[string]any) {
			item["local_poc_admission"].(map[string]any)["not_a_legal_basis"] = false
			redigest(item)
		},
		"waiver reason mismatch": func(item map[string]any) {
			item["local_poc_admission"].(map[string]any)["waived_reasons"] = []any{"privacy_unknown"}
			redigest(item)
		},
		"wrong decision binding": func(item map[string]any) {
			item["local_poc_admission"].(map[string]any)["underlying_decision_id"] = strings.Repeat("a", 64)
			redigest(item)
		},
		"no real deny": func(item map[string]any) {
			item["policy_attestation"].(map[string]any)["effect"] = "allow"
		},
		"nonwaivable capture failure": func(item map[string]any) {
			item["policy_attestation"].(map[string]any)["reasons"] = []any{"capture_id_missing"}
			item["local_poc_admission"].(map[string]any)["waived_reasons"] = []any{"capture_id_missing"}
			redigest(item)
		},
		"CNP field outside factual contract":    func(item map[string]any) { item["cnp_present"] = true },
		"CNP data outside factual contract":     func(item map[string]any) { item["cnp"] = "synthetic identifier" },
		"private body outside factual contract": func(item map[string]any) { item["description"] = "synthetic private body" },
		"capture integrity mismatch": func(item map[string]any) {
			item["policy_attestation"].(map[string]any)["capture_id"] = strings.Repeat("c", 64)
		},
		"content integrity mismatch": func(item map[string]any) {
			item["policy_attestation"].(map[string]any)["subject_sha256"] = strings.Repeat("c", 64)
		},
		"acquisition integrity mismatch": func(item map[string]any) {
			item["policy_attestation"].(map[string]any)["acquisition_evidence_sha256"] = strings.Repeat("c", 64)
		},
		"policy pin mismatch": func(item map[string]any) {
			item["policy_attestation"].(map[string]any)["ruleset_version"] = json.Number("7")
		},
		"missing legal basis":      func(item map[string]any) { item["legal_basis"] = "" },
		"invalid privacy revision": func(item map[string]any) { item["privacy_revision"] = "invalid" },
		"unrecognized access":      func(item map[string]any) { item["access_type"] = "private_owner_grant" },
		"access spelling changed":  func(item map[string]any) { item["access_type"] = "TDM_EXCEPTION" },
	}
	for _, raw := range tdmPOCPage(t)["items"].([]any) {
		base := raw.(map[string]any)
		for name, mutate := range cases {
			t.Run(base["kind"].(string)+"/"+name, func(t *testing.T) {
				item := deepCopy(t, base)
				mutate(item)
				before := deepCopy(t, item)
				if ValidateLocalPOCPackItem(item) || ValidatePackItem(item) {
					t.Fatal("invalid local TDM item accepted")
				}
				if !reflect.DeepEqual(item, before) {
					t.Fatal("refusal rewrote input evidence")
				}
			})
		}
	}
}

func TestLocalPOCTDMReadAndDryRunRetainRealEvidence(t *testing.T) {
	page := tdmPOCPage(t)
	client := pocClient(t, "http://127.0.0.1:8077", true, pocMarshal(t, page))
	read, err := client.Read(context.Background(), "Cluj-Napoca")
	if err != nil || !read.LocalPOC || read.Complete || len(read.Items) != 2 {
		t.Fatal("gated local TDM read", read.LocalPOC, read.Complete, len(read.Items), err)
	}
	for i, item := range read.Items {
		if !reflect.DeepEqual(item, page["items"].([]any)[i]) {
			t.Fatal("consumer read relabelled original metadata")
		}
	}
	config := DefaultConfig()
	config.Roedu = client
	summary, err := New(nil, config).ApplyRoeduWithOptions(context.Background(), read, "Cluj-Napoca", RoeduApplyOptions{MinConfidence: LocalPOCMinConfidence, DryRun: true})
	if err != nil || summary["local_poc"] != true || summary["complete"] != false || summary["venues"] != 1 || summary["events"] != 1 || summary["tombstones"] != 0 {
		t.Fatal("local TDM dry-run", summary, err)
	}
	venue, event := read.Items[0], read.Items[1]
	tags := RoeduVenueTags(venue)
	LocalPOCTags(tags, read, venue)
	evidence := LocalPOCEventEvidence(event)
	if tags["roedu:access_type"] != "tdm_exception" || tags["roedu:gdpr_relevant"] != true || tags["roedu:privacy_classification"] != "unknown" || !reflect.DeepEqual(tags["roedu:policy_attestation"], venue["policy_attestation"]) || !reflect.DeepEqual(tags["roedu:local_poc_admission"], venue["local_poc_admission"]) {
		t.Fatal("venue evidence lost real TDM access or deny")
	}
	if evidence["access_type"] != "tdm_exception" || evidence["gdpr_relevant"] != true || evidence["privacy_classification"] != "unknown" || !reflect.DeepEqual(evidence["policy_attestation"], event["policy_attestation"]) || !reflect.DeepEqual(evidence["local_poc_admission"], event["local_poc_admission"]) {
		t.Fatal("event evidence lost real TDM access or deny")
	}
	for i, item := range read.Items {
		if !reflect.DeepEqual(item, page["items"].([]any)[i]) {
			t.Fatal("dry-run or evidence helper rewrote source metadata")
		}
	}
}

func TestLocalPOCTDMRequiresDevelopmentClientAndPageMarker(t *testing.T) {
	raw := pocMarshal(t, tdmPOCPage(t))
	for name, client := range map[string]*RoeduClient{
		"production client":    pocClient(t, "http://127.0.0.1:8077", false, raw),
		"remote endpoint":      pocClient(t, "https://data.example.org", true, raw),
		"loopback-looking DNS": pocClient(t, "http://127.0.0.1.example.org", true, raw),
	} {
		t.Run(name, func(t *testing.T) {
			if read, err := client.Read(context.Background(), "Cluj-Napoca"); err == nil || len(read.Items) != 0 || read.LocalPOC {
				t.Fatal("TDM POC page accepted outside the development loopback gate")
			}
		})
	}
	for name, marker := range map[string]any{"false": false, "string": "true", "number": json.Number("1"), "null": nil} {
		t.Run("page marker/"+name, func(t *testing.T) {
			page := tdmPOCPage(t)
			page["local_poc"] = marker
			if _, err := pocClient(t, "http://127.0.0.1:8077", true, pocMarshal(t, page)).Read(context.Background(), "Cluj-Napoca"); err == nil {
				t.Fatal("invalid TDM page marker accepted")
			}
		})
	}
	page := tdmPOCPage(t)
	delete(page, "local_poc")
	if _, err := pocClient(t, "http://127.0.0.1:8077", true, pocMarshal(t, page)).Read(context.Background(), "Cluj-Napoca"); !errors.Is(err, ErrProductRefused) {
		t.Fatal("TDM admission accepted on an unmarked page", err)
	}
}
