package web

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"path/filepath"
	"testing"
)

// Owner decision Q6 (ADR-0056): a focused GUI DOM test whose opt-in binding
// flags are ALL absent validates its committed sources and the shared
// registry-pinned bindings, logs that normalization was not requested and
// passes. It never calls t.Skip. Any one of these flags, even empty, selects
// the flagged path, which keeps every existing requirement, so a partial flag
// set still fails exactly as before.
var guiSourceOnlyFlags = []string{
	"gui-public-normalizer-binary",
	"gui-public-normalizer-sha256",
	"gui-public-normalizer-archive",
	"gui-public-golden-reference",
}

// Committed inputs of the released normalizer and original15 bindings. Their
// bytes/SHA-256/type/exec bits are checked against the hash-pinned preference
// registry; the private archive, binary and corpus are never opened.
var guiSourceOnlyBindingPaths = []string{
	"tools/gui-public-golden/release-bindings.json",
	"tools/gui-public-golden/normalize.go",
	"tools/gui-public-golden/normalize_test.go",
	"docs/reviews/gui-public-original/checkpoint.json",
	"services/server/internal/web/gui_public_normalize_test.go",
	"services/server/internal/web/gui_public_trace_test.go",
}

func guiSourceOnlyFlagsAbsent() bool {
	given := false
	flag.Visit(func(f *flag.Flag) {
		for _, name := range guiSourceOnlyFlags {
			if f.Name == name {
				given = true
			}
		}
	})
	return !given && *guiPublicNormalizerBinary == "" && *guiPublicNormalizerSHA256 == "" && *guiPublicNormalizerArchive == "" && *guiPublicGoldenReference == ""
}

func guiSourceOnlySHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return len(value) == 64 && err == nil && len(decoded) == 32
}

func guiSourceOnlyBindings(root string) error {
	var registry guiPreferenceGETRegistry
	if guiG1StrictJSON(guiPreferencesGETRegistryBytes, &registry) != nil || guiPreferenceGETRegistryCheck(registry) != nil {
		return fmt.Errorf("source-only-registry")
	}
	raw, err := guiPublicBoundFile(filepath.Join(root, "services/server/internal/web/testdata/gui-preferences-get/registry.json"), 1<<20)
	if err != nil || !bytes.Equal(raw, guiPreferencesGETRegistryBytes) {
		return fmt.Errorf("source-only-registry-source")
	}
	files := map[string][]byte{}
	for _, name := range guiSourceOnlyBindingPaths {
		wanted, pinned := registry.Sources[name]
		raw, binding, err := guiCurrentTransportReadBinding(root, name, 2<<20)
		if !pinned || err != nil || !guiPublicGetBindingLive(binding, wanted) {
			return fmt.Errorf("source-only-binding")
		}
		files[name] = raw
	}
	var release struct {
		Policy  string `json:"policy"`
		Archive struct {
			File string `json:"file"`
		} `json:"go_archive"`
	}
	if json.Unmarshal(files["tools/gui-public-golden/release-bindings.json"], &release) != nil || release.Policy != guiPublicNormalizePolicy || release.Archive.File != "web-kit-go-core-v1.6.tgz" {
		return fmt.Errorf("source-only-release-frame")
	}
	// The metadata half of guiPublicReadReference: case matrix and record
	// identity only. No original body is read or claimed.
	var checkpoint guiPublicReferenceCheckpoint
	cases := guiPublicCases()
	if json.Unmarshal(files["docs/reviews/gui-public-original/checkpoint.json"], &checkpoint) != nil || len(checkpoint.Cases) != len(cases) || !guiSourceOnlySHA256(checkpoint.ManifestSHA256) {
		return fmt.Errorf("source-only-checkpoint-frame")
	}
	for index, fixture := range cases {
		record := checkpoint.Cases[index]
		if record.Case != fixture || record.File != fixture.ID+".html" || record.FullMode != 0o100600 || record.Bytes < 1 || record.Bytes > 512<<10 || !guiSourceOnlySHA256(record.SHA256) {
			return fmt.Errorf("source-only-checkpoint-case")
		}
	}
	return nil
}

// guiSourceOnlyDefault reports true when the caller must return after its
// committed source checks: every focused flag is absent and the shared
// bindings validated. It fails the test if those committed bindings differ.
func guiSourceOnlyDefault(t *testing.T, root, scope string) bool {
	t.Helper()
	if !guiSourceOnlyFlagsAbsent() {
		return false
	}
	if err := guiSourceOnlyBindings(root); err != nil {
		t.Fatalf("committed %s source-only binding refused: %s", scope, err)
	}
	t.Logf("NORMALIZATION NOT REQUESTED: %s committed source inverses and registry-pinned normalizer/original15 bindings validated; no released normalizer, archive, original15 corpus, DOM comparison or GUI qualification", scope)
	return true
}
