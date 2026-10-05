package contracts

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type NativeEvidence struct {
	File       string   `json:"go_file"`
	Test       string   `json:"test"`
	Scenario   string   `json:"scenario"`
	Assertions []string `json:"assertions"`
}

type Entry struct {
	LegacyCase
	Classification string           `json:"classification"`
	Evidence       []NativeEvidence `json:"native_evidence"`
	Rationale      string           `json:"rationale"`
	Gaps           []string         `json:"gaps"`
	Verification   string           `json:"runtime_verification"`
}

type Manifest struct {
	Version    int     `json:"schema_version"`
	SourceHead string  `json:"source_head"`
	Entries    []Entry `json:"entries"`
}

type Report struct {
	Total      int      `json:"total_source_cases"`
	Verified   int      `json:"verified_source_cases"`
	Unresolved []string `json:"unresolved"`
	Invalid    []string `json:"invalid"`
}

// ReadJSON rejects trailing data and unbounded manifests. Extra source tags
// describing static parametrization are allowed, but never imply verification.
func ReadJSON(path string, value any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (32<<20)+1))
	if err != nil {
		return err
	}
	if len(raw) > 32<<20 {
		return fmt.Errorf("contract manifest exceeds limit")
	}
	return json.Unmarshal(raw, value)
}

func evidenceFile(root, name string) (string, error) {
	if filepath.IsAbs(name) || filepath.Clean(name) != name || strings.HasPrefix(name, "..") || !strings.HasSuffix(name, "_test.go") {
		return "", fmt.Errorf("invalid evidence file %s", name)
	}
	path := filepath.Join(root, name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("evidence source unavailable: %s", name)
	}
	return path, nil
}

func testFunctions(root, filename string) (map[string]bool, error) {
	path, err := evidenceFile(root, filename)
	if err != nil {
		return nil, err
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, decl := range file.Decls {
		if f, ok := decl.(*ast.FuncDecl); ok && f.Recv == nil && strings.HasPrefix(f.Name.Name, "Test") && f.Body != nil {
			names[f.Name.Name] = true
		}
	}
	return names, nil
}

// Check preserves the case baseline and validates explicit native evidence.
// A real named test plus assertions/rationale and a reviewed successful runtime
// status are required; route-name matches or a larger Go test count are not
// semantic coverage. Reviewers must confirm receipts and evidence equivalence.
// The runtime_verification status is a manifest claim: Check reads it as
// written and never compares it against any test run, so the verified count it
// reports is manifest-claimed, not run-checked.
func Check(root string, inventory Inventory, manifests []Manifest) Report {
	report := Report{Total: len(inventory.Cases), Unresolved: []string{}, Invalid: []string{}}
	if inventory.Version != 1 || inventory.SourceHead == "" || len(inventory.Cases) == 0 {
		report.Invalid = append(report.Invalid, "missing or invalid frozen inventory")
	}
	baseline, entries := map[string]LegacyCase{}, map[string]Entry{}
	functions := map[string]map[string]bool{}
	for _, item := range inventory.Cases {
		if item.File == "" || item.Line < 1 || len(item.SourceHash) != 64 || !strings.HasPrefix(item.ID, item.File+"::") {
			report.Invalid = append(report.Invalid, "invalid inventory provenance: "+item.ID)
		}
		if baseline[item.ID].ID != "" {
			report.Invalid = append(report.Invalid, "duplicate inventory case: "+item.ID)
		}
		baseline[item.ID] = item
	}
	for _, manifest := range manifests {
		if manifest.Version != 1 || manifest.SourceHead != inventory.SourceHead {
			report.Invalid = append(report.Invalid, "manifest baseline/version mismatch")
		}
		for _, entry := range manifest.Entries {
			if entries[entry.ID].ID != "" {
				report.Invalid = append(report.Invalid, "duplicate coverage case: "+entry.ID)
			}
			entries[entry.ID] = entry
			if baseline[entry.ID].ID == "" {
				report.Invalid = append(report.Invalid, "unknown coverage case: "+entry.ID)
			}
		}
	}
	for _, original := range inventory.Cases {
		entry, exists := entries[original.ID]
		if !exists || entry.Classification == "unresolved" || entry.Verification != "passed" || len(entry.Gaps) > 0 {
			report.Unresolved = append(report.Unresolved, original.ID)
			continue
		}
		valid := entry.SourceHash == original.SourceHash && entry.File == original.File && entry.Line == original.Line && entry.Rationale != "" && len(entry.Evidence) > 0
		valid = valid && (entry.Classification == "native_semantic_coverage" || entry.Classification == "native_policy_replacement")
		for _, evidence := range entry.Evidence {
			if functions[evidence.File] == nil {
				names, err := testFunctions(root, evidence.File)
				if err != nil {
					valid = false
				} else {
					functions[evidence.File] = names
				}
			}
			valid = valid && functions[evidence.File][evidence.Test] && evidence.Scenario != "" && len(evidence.Assertions) > 0
		}
		if !valid {
			report.Invalid = append(report.Invalid, "invalid evidence for: "+original.ID)
		} else {
			report.Verified++
		}
	}
	sort.Strings(report.Unresolved)
	sort.Strings(report.Invalid)
	return report
}
