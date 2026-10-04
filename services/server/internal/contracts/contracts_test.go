package contracts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInventoryDoesNotExecuteOrCollectQuotedReferenceSource(t *testing.T) {
	source := `"""def test_docstring_is_not_a_case(): pass
class Hidden: pass"""
raise RuntimeError("reference source must not execute")
# def test_comment(): pass
class ContractTests:
    def test_member(self):
        text = "def test_string(): pass"
    async def test_async_member(self): pass
class OtherTests:
    def test_member(self): pass
def test_module_case(): pass
`
	cases, err := SourceCases("apps/sample/tests/test_contract.py", []byte(source))
	if err != nil || len(cases) != 4 {
		t.Fatal("source-data inventory lost the declaration contract", len(cases), err)
	}
	for i, suffix := range []string{"::ContractTests::test_member", "::ContractTests::test_async_member", "::OtherTests::test_member", "::test_module_case"} {
		if !strings.HasSuffix(cases[i].ID, suffix) || len(cases[i].SourceHash) != 64 || cases[i].Line < 1 {
			t.Fatal("case provenance or class qualification differs", cases[i].ID)
		}
	}
}

func TestCoverageGateDoesNotTurnCountsOrDeclaredMappingsIntoVerification(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "real_test.go"), []byte("package fixture\nfunc TestConcreteBehavior(t *testing.T) { t.Fatal(\"assertion\") }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	original := LegacyCase{"reference.py::test_behavior", "reference.py", 4, strings.Repeat("a", 64)}
	inventory := Inventory{1, "baseline", []LegacyCase{original}}
	entry := Entry{original, "native_semantic_coverage", []NativeEvidence{{"real_test.go", "TestConcreteBehavior", "exact positive and negative wire cases", []string{"field cap and failure status"}}}, "independent native expected values", nil, "passed"}
	valid := Manifest{1, "baseline", []Entry{entry}}
	if report := Check(root, inventory, []Manifest{valid}); report.Verified != 1 || len(report.Invalid) != 0 || len(report.Unresolved) != 0 {
		t.Fatal("qualified linked evidence was rejected", report)
	}
	for _, mutate := range []func(*Entry){
		func(e *Entry) { e.Verification = "not_run" },
		func(e *Entry) { e.Classification = "unresolved" },
		func(e *Entry) { e.Evidence = nil },
		func(e *Entry) {
			e.Evidence = []NativeEvidence{{"real_test.go", "TestDoesNotExist", "case", []string{"assert"}}}
		},
		func(e *Entry) { e.SourceHash = strings.Repeat("b", 64) },
		func(e *Entry) { e.Gaps = []string{"no real fixture"} },
	} {
		copy := entry
		mutate(&copy)
		report := Check(root, inventory, []Manifest{{1, "baseline", []Entry{copy}}})
		if report.Verified != 0 || len(report.Unresolved)+len(report.Invalid) == 0 {
			t.Fatal("unverified reference behavior passed retirement gate", report)
		}
	}
	if report := Check(root, inventory, nil); len(report.Unresolved) != 1 {
		t.Fatal("missing case mapping was not blocked")
	}
	for _, invalid := range []Inventory{{}, {Version: 1, SourceHead: "baseline"}, {Version: 2, SourceHead: "baseline", Cases: []LegacyCase{original}}, {Version: 1, Cases: []LegacyCase{original}}} {
		if report := Check(root, invalid, nil); len(report.Invalid) == 0 {
			t.Fatal("missing or invalid baseline manufactured a green retirement gate")
		}
	}
}

func TestFrozenCoverageInventoryHasCompleteUniqueProvenance(t *testing.T) {
	var inventory Inventory
	if err := ReadJSON("testdata/legacy-test-inventory.json", &inventory); err != nil {
		t.Fatal(err)
	}
	if inventory.Version != 1 || inventory.SourceHead != "ce3d0e3ee9f180ce2e95140300b12582db9895d6" || len(inventory.Cases) != 2671 {
		t.Fatal("retirement baseline changed without explicit source review")
	}
	seen := map[string]bool{}
	for _, item := range inventory.Cases {
		if seen[item.ID] || len(item.SourceHash) != 64 || item.Line < 1 || !strings.HasPrefix(item.ID, item.File+"::") {
			t.Fatal("incomplete or duplicate source provenance", item.ID)
		}
		seen[item.ID] = true
	}
}
