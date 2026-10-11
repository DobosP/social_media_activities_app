package web

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// This receiver consumes the accepted raw corpus; it never renders, requests
// HTTP, changes an oracle, or calls the earlier two-script nonce compare method.
var guiPublicGetWalkNormalizeReference = flag.String("gui-public-get-walk-normalize-reference", "", "private unchanged accepted R6 G1 raw corpus; opt-in strict SDK diagnostics only")

const guiG1NormalizationReferenceSHA = "ecd1e22d36b7f925d47f649ef0c5c92a26e7dc487b78374e9cc26db32dfe5f13"

//go:embed testdata/gui-public-get-walk/normalization-reference.json
var guiG1NormalizationReferenceBytes []byte

type guiG1Binding struct {
	Bytes    int    `json:"bytes"`
	SHA256   string `json:"sha256"`
	FullMode uint32 `json:"full_mode"`
}

type guiG1NormalizationPins struct {
	Schema                   int          `json:"schema"`
	Kind                     string       `json:"kind"`
	CandidateSource          string       `json:"candidate_source"`
	CaptureInvocation        string       `json:"capture_invocation"`
	HistoricalSource         string       `json:"historical_source"`
	Manifest                 guiG1Binding `json:"manifest"`
	CasesSHA256              string       `json:"cases_sha256"`
	SourceInputsSHA256       string       `json:"source_inputs_sha256"`
	CaptureObservationSHA256 string       `json:"capture_observation_sha256"`
	RawResultSHA256           string       `json:"raw_result_sha256"`
	MethodSHA256              string       `json:"method_sha256"`
	Counts                   struct {
		Cases        int `json:"cases"`
		Members      int `json:"members"`
		HTML         int `json:"HTML"`
		Redirects    int `json:"redirects"`
		Missing      int `json:"missing"`
		SourceInputs int `json:"source_inputs"`
	} `json:"counts"`
}

type guiG1Receipt struct {
	ID                 string                  `json:"id"`
	Route              string                  `json:"route"`
	Path               string                  `json:"path"`
	State              string                  `json:"state"`
	Language           string                  `json:"language"`
	Profile            string                  `json:"profile"`
	Method             string                  `json:"method"`
	RequestActor       string                  `json:"request_actor"`
	ActualStatus       int                     `json:"actual_status"`
	ExpectedStatus     int                     `json:"expected_status"`
	ExpectedTemplate   string                  `json:"expected_template"`
	FailureCode        string                  `json:"failure_code"`
	HTTPAssertionsPass bool                    `json:"http_assertions_pass"`
	BodyFile           string                  `json:"body_file"`
	BodyBytes          int                     `json:"body_bytes"`
	BodySHA256         string                  `json:"body_sha256"`
	ResponseHeaders    map[string]string       `json:"response_headers"`
	TemplateLoads      []guiPublicTemplateLoad `json:"template_loads"`
	MutationControls   []string                `json:"mutation_controls"`
}

type guiG1Manifest struct {
	Schema                      int                     `json:"schema"`
	Kind                        string                  `json:"kind"`
	Status                      string                  `json:"status"`
	Scope                       string                  `json:"scope"`
	HistoricalProducer          string                  `json:"historical_producer"`
	SourceInputsSHA256           string                  `json:"source_inputs_sha256"`
	CaseRegistrySHA256           string                  `json:"case_registry_sha256"`
	ExpectedCases               int                     `json:"expected_cases"`
	InputsUnchanged             bool                    `json:"inputs_unchanged"`
	Original15ReferenceVerified bool                    `json:"original15_reference_verified"`
	Normalization               string                  `json:"normalization"`
	InputsBefore                map[string]guiG1Binding `json:"inputs_before"`
	InputsAfter                 map[string]guiG1Binding `json:"inputs_after"`
	Cases                       []guiG1Receipt          `json:"cases"`
}

type guiG1Corpus struct {
	Manifest      guiG1Manifest
	Bodies        map[string][]byte
	Bindings      map[string]guiG1Binding
	FileInfo      map[string]os.FileInfo
	DirectoryInfo os.FileInfo
}

func guiG1StrictJSON(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		return fmt.Errorf("JSON-frame")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return fmt.Errorf("JSON-frame")
	}
	return nil
}

func guiG1Pins() (guiG1NormalizationPins, error) {
	var pins guiG1NormalizationPins
	if guiPublicHash(guiG1NormalizationReferenceBytes) != guiG1NormalizationReferenceSHA || guiG1StrictJSON(guiG1NormalizationReferenceBytes, &pins) != nil {
		return pins, fmt.Errorf("reference-pins")
	}
	if pins.Schema != 1 || pins.Kind != "accepted-raw-G1-input-reference-not-normalized-baseline" || pins.CandidateSource != "a115deb5d706745a22d5e1049b0b938cae10cfad" || pins.CaptureInvocation != "cbd00d1a-927c-4a5c-a27c-00838941dfdd" || pins.HistoricalSource != guiPublicGetHistory || pins.CasesSHA256 != guiPublicGetCasesSHA || pins.SourceInputsSHA256 != guiPublicGetSourceSHA || pins.Counts.Cases != 37 || pins.Counts.Members != 38 || pins.Counts.HTML != 31 || pins.Counts.Redirects != 2 || pins.Counts.Missing != 4 || pins.Counts.SourceInputs != 102 || pins.Manifest.FullMode != 0o100600 {
		return pins, fmt.Errorf("reference-pins")
	}
	return pins, nil
}

func guiG1CurrentInputs(root string) (map[string]guiG1Binding, error) {
	rows, err := guiPublicGetBoundInputs(root)
	if err != nil {
		return nil, fmt.Errorf("source-inputs")
	}
	raw, err := json.Marshal(rows)
	var typed map[string]guiG1Binding
	if err != nil || guiG1StrictJSON(raw, &typed) != nil || len(typed) != 102 {
		return nil, fmt.Errorf("source-inputs")
	}
	return typed, nil
}

func guiG1PrivateRead(name string, limit int64) ([]byte, guiG1Binding, os.FileInfo, error) {
	var empty guiG1Binding
	before, err := os.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0o600 || before.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return nil, empty, nil, fmt.Errorf("private-file")
	}
	raw, err := guiPublicBoundFile(name, limit)
	after, statErr := os.Lstat(name)
	if err != nil || statErr != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || before.Size() != after.Size() || before.ModTime() != after.ModTime() || len(raw) == 0 {
		return nil, empty, nil, fmt.Errorf("private-file")
	}
	return raw, guiG1Binding{len(raw), guiPublicHash(raw), 0o100600}, after, nil
}

func guiG1ReadCorpus(directory string, pin guiG1Binding, cases []guiPublicGetCase, inputs map[string]guiG1Binding) (*guiG1Corpus, error) {
	info, err := os.Lstat(directory)
	resolved, resolveErr := filepath.EvalSymlinks(directory)
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || err != nil || resolveErr != nil || resolved != directory || !info.IsDir() || info.Mode().Perm() != 0o700 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return nil, fmt.Errorf("directory")
	}
	entries, err := os.ReadDir(directory)
	wanted := map[string]bool{"manifest.json": true}
	for _, c := range cases {
		wanted[c.ID+".body"] = true
	}
	if err != nil || len(cases) != 37 || len(wanted) != 38 || len(entries) != len(wanted) {
		return nil, fmt.Errorf("member-census")
	}
	for _, entry := range entries {
		if !wanted[entry.Name()] {
			return nil, fmt.Errorf("member-census")
		}
	}
	raw, binding, fileInfo, err := guiG1PrivateRead(filepath.Join(directory, "manifest.json"), 512<<10)
	if err != nil || binding != pin {
		return nil, fmt.Errorf("manifest-binding")
	}
	var manifest guiG1Manifest
	if guiG1StrictJSON(raw, &manifest) != nil || manifest.Schema != 1 || manifest.Kind != "historical-native-G1-registered-GET-walk" || manifest.Status != "pass" || manifest.HistoricalProducer != guiPublicGetHistory || manifest.SourceInputsSHA256 != guiPublicGetSourceSHA || manifest.CaseRegistrySHA256 != guiPublicGetCasesSHA || manifest.ExpectedCases != 37 || len(manifest.Cases) != 37 || !manifest.InputsUnchanged || !manifest.Original15ReferenceVerified || manifest.Normalization != "NOT RUN; raw M0 source-native oracle capture is not strict golden/Live acceptance" {
		return nil, fmt.Errorf("manifest-identity")
	}
	if len(inputs) != 102 || !reflect.DeepEqual(manifest.InputsBefore, inputs) || !reflect.DeepEqual(manifest.InputsAfter, inputs) {
		return nil, fmt.Errorf("source-inputs")
	}
	corpus := &guiG1Corpus{manifest, map[string][]byte{}, map[string]guiG1Binding{"manifest.json": binding}, map[string]os.FileInfo{"manifest.json": fileInfo}, info}
	counts := map[int]int{}
	seen := map[string]bool{}
	mutations := []string{"status", "content-type", "cache", "template", "language", "canonical", "robots", "heading", "footer"}
	for i, c := range cases {
		row := manifest.Cases[i]
		pattern := strings.NewReplacer(`\{place\}`, `[1-9][0-9]*`, `\{event\}`, `[1-9][0-9]*`).Replace(regexp.QuoteMeta(c.Path))
		if row.ID != c.ID || seen[row.ID] || row.Route != c.Route || row.State != c.State || row.Language != c.Language || row.Profile != c.Profile || row.Method != "GET" || row.RequestActor != "anonymous" || row.ActualStatus != c.Status || row.ExpectedStatus != c.Status || row.ExpectedTemplate != c.Template || !row.HTTPAssertionsPass || row.FailureCode != "" || !regexp.MustCompile("^(?:"+pattern+")$").MatchString(row.Path) {
			return nil, fmt.Errorf("receipt-identity")
		}
		seen[row.ID] = true
		if len(row.ResponseHeaders) != 4 {
			return nil, fmt.Errorf("receipt-headers")
		}
		for _, key := range []string{"Content-Type", "Cache-Control", "Location", "Vary"} {
			if _, exists := row.ResponseHeaders[key]; !exists {
				return nil, fmt.Errorf("receipt-headers")
			}
		}
		if c.Status == 200 {
			if !strings.HasPrefix(row.ResponseHeaders["Content-Type"], "text/html") || row.ResponseHeaders["Cache-Control"] != "private, no-cache" || !strings.Contains(row.ResponseHeaders["Vary"], "Cookie") || len(row.TemplateLoads) < 2 || row.TemplateLoads[0].Name != c.Template {
				return nil, fmt.Errorf("receipt-headers")
			}
			base := false
			for _, load := range row.TemplateLoads {
				base = base || load.Name == "base.html"
				if load.Bytes <= 0 || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(load.SHA256) {
					return nil, fmt.Errorf("receipt-template")
				}
			}
			if !base {
				return nil, fmt.Errorf("receipt-template")
			}
		} else if len(row.TemplateLoads) != 0 || c.Status == 302 && row.ResponseHeaders["Location"] != "/login/?next="+url.QueryEscape(c.Path) {
			return nil, fmt.Errorf("nonHTML-receipt")
		}
		if c.ID == "landing-en-default" {
			if !reflect.DeepEqual(row.MutationControls, mutations) {
				return nil, fmt.Errorf("receipt-mutations")
			}
		} else if len(row.MutationControls) != 0 {
			return nil, fmt.Errorf("receipt-mutations")
		}
		if row.BodyFile != c.ID+".body" {
			return nil, fmt.Errorf("body-name")
		}
		body, bodyBinding, bodyInfo, readErr := guiG1PrivateRead(filepath.Join(directory, row.BodyFile), 512<<10)
		if readErr != nil || bodyBinding.Bytes != row.BodyBytes || bodyBinding.SHA256 != row.BodySHA256 {
			return nil, fmt.Errorf("body-file")
		}
		corpus.Bodies[c.ID] = body
		corpus.Bindings[row.BodyFile] = bodyBinding
		corpus.FileInfo[row.BodyFile] = bodyInfo
		counts[c.Status]++
	}
	if counts[200] != 31 || counts[302] != 2 || counts[404] != 4 {
		return nil, fmt.Errorf("status-census")
	}
	after, err := os.Lstat(directory)
	if err != nil || !os.SameFile(info, after) || info.Mode() != after.Mode() {
		return nil, fmt.Errorf("directory")
	}
	return corpus, nil
}

func guiG1SameCorpus(before, after *guiG1Corpus) bool {
	if !os.SameFile(before.DirectoryInfo, after.DirectoryInfo) || !reflect.DeepEqual(before.Bindings, after.Bindings) {
		return false
	}
	for name, info := range before.FileInfo {
		if !os.SameFile(info, after.FileInfo[name]) {
			return false
		}
	}
	return true
}

func guiG1HardCodes(hard []string) []string {
	seen := map[string]bool{}
	for _, finding := range hard {
		code := "withheld"
		for prefix, allowed := range map[string]string{"empty URL at ": "empty-url", "failed URL sanitization at ": "failed-url", "template syntax leak at ": "template-leak", "invalid JSON script at ": "invalid-json", "HTML parse: ": "html-parse", "oracle HTML parse: ": "oracle-parse"} {
			if strings.HasPrefix(finding, prefix) {
				code = allowed
				break
			}
		}
		seen[code] = true
	}
	codes := []string{}
	for code := range seen {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

func guiG1ReplaceOnce(raw []byte, before, after string) ([]byte, error) {
	if bytes.Count(raw, []byte(before)) != 1 || before == after {
		return nil, fmt.Errorf("mutation-target")
	}
	return bytes.Replace(append([]byte(nil), raw...), []byte(before), []byte(after), 1), nil
}

func guiG1SwapOnce(raw []byte, left, right string) ([]byte, error) {
	a, b := bytes.Index(raw, []byte(left)), bytes.Index(raw, []byte(right))
	if bytes.Count(raw, []byte(left)) != 1 || bytes.Count(raw, []byte(right)) != 1 || a < 0 || b <= a+len(left) {
		return nil, fmt.Errorf("mutation-target")
	}
	out := append([]byte(nil), raw[:a]...)
	out = append(out, right...)
	out = append(out, raw[a+len(left):b]...)
	out = append(out, left...)
	return append(out, raw[b+len(right):]...), nil
}

func TestGUIPublicGetWalkNormalize(t *testing.T) {
	pins, err := guiG1Pins()
	if err != nil {
		t.Fatal("reference-pins")
	}
	if *guiPublicGetWalkNormalizeReference == "" {
		t.Log("NORMALIZATION NOT REQUESTED: reference metadata only; no corpus/SDK/HTTP qualification")
		return
	}
	root, err := filepath.Abs("../../../..")
	cases, caseErr := guiPublicGetCases()
	inputs, inputErr := guiG1CurrentInputs(root)
	if err != nil || caseErr != nil || inputErr != nil {
		t.Fatal("source-inputs")
	}
	corpus, err := guiG1ReadCorpus(*guiPublicGetWalkNormalizeReference, pins.Manifest, cases, inputs)
	if err != nil {
		t.Fatalf("raw corpus refused: %s", err) // Only this receiver's constant codes.
	}
	tool, err := guiPublicNewNormalizer(root)
	if err != nil || tool == nil || *guiPublicGoldenReference == "" {
		t.Fatal("explicit original15/binary/archive bindings required")
	}
	checkpoint, err := os.ReadFile(filepath.Join(root, "docs/reviews/gui-public-original/checkpoint.json"))
	original, originalErr := guiPublicReadReference(*guiPublicGoldenReference, checkpoint)
	if err != nil || originalErr != nil {
		t.Fatal("original15 reference refused")
	}
	attempts := 0
	t.Cleanup(func() {
		afterInputs, afterErr := guiG1CurrentInputs(root)
		after, corpusErr := guiG1ReadCorpus(*guiPublicGetWalkNormalizeReference, pins.Manifest, cases, inputs)
		next, referenceErr := guiPublicReadReference(*guiPublicGoldenReference, checkpoint)
		if afterErr != nil || !reflect.DeepEqual(inputs, afterInputs) || corpusErr != nil || !guiG1SameCorpus(corpus, after) || referenceErr != nil || !reflect.DeepEqual(original, next) || tool.checkUnchanged() != nil {
			t.Error("source/raw corpus/original15/binary/archive changed")
		}
		t.Logf("strict receiver wrapper API attempts=%d; this is not a browser or process-start census", attempts)
	})
	canonical := map[string][]byte{}
	for _, row := range corpus.Manifest.Cases {
		t.Run(row.ID, func(t *testing.T) {
			body := corpus.Bodies[row.ID]
			if row.ActualStatus != 200 {
				t.Logf("measured raw receipt retained: status=%d body_sha256=%s; nonHTML NOT NORMALIZED", row.ActualStatus, guiPublicHash(body))
				return
			}
			attempts++
			normalized, hard, normalizeErr := tool.normalize(body)
			codes := guiG1HardCodes(hard)
			t.Logf("raw_sha256=%s canonical_sha256=%s canonical_bytes=%d hard_count=%d hard_codes=%v success=%t; single-input diagnostic, no renderer parity", guiPublicHash(body), guiPublicHash(normalized), len(normalized), len(hard), codes, normalizeErr == nil)
			if normalizeErr != nil || len(hard) != 0 {
				t.Errorf("strict normalization refused; hard_count=%d hard_codes=%v", len(hard), codes)
				return // The other named cases still run and retain their findings.
			}
			canonical[row.ID] = normalized
		})
	}
	viewport := `<meta name="viewport" content="width=device-width, initial-scale=1">`
	heading := `<h1>Meet people. Do real things. In person.</h1>`
	link := `<link rel="canonical" href="https://fixture.local/">`
	controls := []struct {
		name, source, wantHard string
		equal bool
		change func([]byte) ([]byte, error)
	}{
		{"nonce-value-equivalent", "events-populated", "", true, func(raw []byte) ([]byte, error) {
			matches := regexp.MustCompile(` nonce="[^"]+"`).FindAll(raw, -1)
			if len(matches) != 1 {
				return nil, fmt.Errorf("mutation-target")
			}
			return guiG1ReplaceOnce(raw, string(matches[0]), ` nonce="synthetic-g1-volatility"`)
		}},
		{"attribute-order-equivalent", "landing-en-default", "", true, func(raw []byte) ([]byte, error) {
			return guiG1ReplaceOnce(raw, viewport, `<meta content="width=device-width, initial-scale=1" name="viewport">`)
		}},
		{"visible-text-differs", "landing-en-default", "", false, func(raw []byte) ([]byte, error) {
			return guiG1ReplaceOnce(raw, heading, `<h1>Meet synthetic neighbours. Do real things. In person.</h1>`)
		}},
		{"link-differs", "landing-en-default", "", false, func(raw []byte) ([]byte, error) {
			return guiG1ReplaceOnce(raw, link, `<link rel="canonical" href="https://fixture.local/synthetic-visible-difference/">`)
		}},
		{"node-order-differs", "landing-en-default", "", false, func(raw []byte) ([]byte, error) {
			return guiG1SwapOnce(raw, `<meta name="robots" content="index, follow">`, link)
		}},
		{"empty-url-hard-refusal", "landing-en-default", "empty-url", false, func(raw []byte) ([]byte, error) {
			return guiG1ReplaceOnce(raw, link, `<link rel="canonical" href="">`)
		}},
		{"template-leak-hard-refusal", "landing-en-default", "template-leak", false, func(raw []byte) ([]byte, error) {
			return guiG1ReplaceOnce(raw, heading, `<h1>{{synthetic_g1_leak}}</h1>`)
		}},
	}
	for _, control := range controls {
		t.Run(control.name, func(t *testing.T) {
			originalRaw := corpus.Bodies[control.source]
			beforeSHA := guiPublicHash(originalRaw)
			baseline, exists := canonical[control.source]
			if !exists {
				t.Fatal("hard-free source diagnostic required before a mutation can pass")
			}
			changed, changeErr := control.change(originalRaw)
			if changeErr != nil || bytes.Equal(changed, originalRaw) || beforeSHA != guiPublicHash(originalRaw) {
				t.Fatal("unique mutation target/buffer preservation")
			}
			attempts++
			normalized, hard, normalizeErr := tool.normalize(changed)
			codes := guiG1HardCodes(hard)
			t.Logf("mutation=%s source=%s canonical_sha256=%s hard_count=%d hard_codes=%v", control.name, control.source, guiPublicHash(normalized), len(hard), codes)
			if control.wantHard != "" {
				if normalizeErr == nil || len(hard) != 1 || !reflect.DeepEqual(codes, []string{control.wantHard}) {
					t.Fatal("owning hard finding required for intentional refusal")
				}
				return
			}
			if normalizeErr != nil || len(hard) != 0 || bytes.Equal(baseline, normalized) != control.equal {
				t.Fatal("canonical equivalence/difference must be independent of hard refusal")
			}
		})
	}
}

// Synthetic corpus fixtures exercise custody only. Bodies are identical small
// bytes, not copied oracle responses; supplied test pins cannot bypass the real
// top-level receiver's fixed accepted-manifest metadata.
func guiG1SyntheticCorpus(t *testing.T, actual *guiG1Corpus) (string, guiG1Binding) {
	t.Helper()
	directory := t.TempDir()
	raw, err := json.Marshal(actual.Manifest)
	var manifest guiG1Manifest
	if err != nil || guiG1StrictJSON(raw, &manifest) != nil {
		t.Fatal("synthetic manifest setup")
	}
	body := []byte("<p>synthetic custody only</p>")
	for i := range manifest.Cases {
		row := &manifest.Cases[i]
		row.BodyBytes, row.BodySHA256 = len(body), guiPublicHash(body)
		if os.WriteFile(filepath.Join(directory, row.BodyFile), body, 0o600) != nil {
			t.Fatal("synthetic body setup")
		}
	}
	return directory, guiG1SyntheticManifest(t, directory, manifest)
}

func guiG1SyntheticManifest(t *testing.T, directory string, manifest guiG1Manifest) guiG1Binding {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil || os.WriteFile(filepath.Join(directory, "manifest.json"), raw, 0o600) != nil {
		t.Fatal("synthetic manifest setup")
	}
	return guiG1Binding{len(raw), guiPublicHash(raw), 0o100600}
}

func TestGUIPublicGetWalkNormalizeCorpusRefusals(t *testing.T) {
	pins, err := guiG1Pins()
	if err != nil {
		t.Fatal("reference-pins")
	}
	if *guiPublicGetWalkNormalizeReference == "" {
		t.Log("CORPUS CONTROLS NOT REQUESTED: metadata only; no normalization qualification")
		return
	}
	root, err := filepath.Abs("../../../..")
	cases, caseErr := guiPublicGetCases()
	inputs, inputErr := guiG1CurrentInputs(root)
	if err != nil || caseErr != nil || inputErr != nil {
		t.Fatal("source-inputs")
	}
	actual, err := guiG1ReadCorpus(*guiPublicGetWalkNormalizeReference, pins.Manifest, cases, inputs)
	if err != nil {
		t.Fatal("accepted actual corpus required before synthetic controls")
	}
	t.Cleanup(func() {
		after, readErr := guiG1ReadCorpus(*guiPublicGetWalkNormalizeReference, pins.Manifest, cases, inputs)
		if readErr != nil || !guiG1SameCorpus(actual, after) {
			t.Error("actual raw corpus changed during synthetic controls")
		}
	})
	controls := []struct {
		name, code string
		change func(*testing.T, string, guiG1Binding) guiG1Binding
	}{
		{"missing-body", "member-census", func(t *testing.T, directory string, pin guiG1Binding) guiG1Binding {
			if os.Remove(filepath.Join(directory, cases[0].ID+".body")) != nil {
				t.Fatal("synthetic mutation")
			}
			return pin
		}},
		{"extra-member", "member-census", func(t *testing.T, directory string, pin guiG1Binding) guiG1Binding {
			if os.WriteFile(filepath.Join(directory, "unlisted.body"), []byte("synthetic"), 0o600) != nil {
				t.Fatal("synthetic mutation")
			}
			return pin
		}},
		{"alias-equal-existing-body", "body-file", func(t *testing.T, directory string, pin guiG1Binding) guiG1Binding {
			// All bodies/declared hashes are equal and the38-name set stays exact.
			name := filepath.Join(directory, cases[0].ID+".body")
			if os.Remove(name) != nil || os.Symlink(cases[1].ID+".body", name) != nil {
				t.Fatal("synthetic mutation")
			}
			return pin
		}},
		{"changed-body-same-length", "body-file", func(t *testing.T, directory string, pin guiG1Binding) guiG1Binding {
			name := filepath.Join(directory, cases[0].ID+".body")
			raw, readErr := os.ReadFile(name)
			if readErr != nil {
				t.Fatal("synthetic mutation")
			}
			raw[3] ^= 1
			if os.WriteFile(name, raw, 0o600) != nil {
				t.Fatal("synthetic mutation")
			}
			return pin
		}},
		{"wrong-manifest-digest", "manifest-binding", func(t *testing.T, directory string, pin guiG1Binding) guiG1Binding {
			pin.SHA256 = strings.Repeat("0", 64)
			return pin
		}},
		{"duplicate-case-repinned", "receipt-identity", func(t *testing.T, directory string, pin guiG1Binding) guiG1Binding {
			raw, readErr := os.ReadFile(filepath.Join(directory, "manifest.json"))
			var frame guiG1Manifest
			if readErr != nil || guiG1StrictJSON(raw, &frame) != nil {
				t.Fatal("synthetic mutation")
			}
			frame.Cases[1].ID = frame.Cases[0].ID
			return guiG1SyntheticManifest(t, directory, frame)
		}},
		{"omitted-case-repinned", "manifest-identity", func(t *testing.T, directory string, pin guiG1Binding) guiG1Binding {
			raw, readErr := os.ReadFile(filepath.Join(directory, "manifest.json"))
			var frame guiG1Manifest
			if readErr != nil || guiG1StrictJSON(raw, &frame) != nil {
				t.Fatal("synthetic mutation")
			}
			frame.Cases = frame.Cases[:len(frame.Cases)-1]
			return guiG1SyntheticManifest(t, directory, frame)
		}},
		{"changed-source-pin-repinned", "source-inputs", func(t *testing.T, directory string, pin guiG1Binding) guiG1Binding {
			raw, readErr := os.ReadFile(filepath.Join(directory, "manifest.json"))
			var frame guiG1Manifest
			if readErr != nil || guiG1StrictJSON(raw, &frame) != nil {
				t.Fatal("synthetic mutation")
			}
			name := "services/server/go.mod"
			value, exists := frame.InputsBefore[name]
			if !exists {
				t.Fatal("synthetic source target")
			}
			value.SHA256 = strings.Repeat("0", 64)
			frame.InputsBefore[name] = value
			return guiG1SyntheticManifest(t, directory, frame)
		}},
		{"nonprivate-body", "body-file", func(t *testing.T, directory string, pin guiG1Binding) guiG1Binding {
			if os.Chmod(filepath.Join(directory, cases[0].ID+".body"), 0o644) != nil {
				t.Fatal("synthetic mutation")
			}
			return pin
		}},
	}
	for _, control := range controls {
		t.Run(control.name, func(t *testing.T) {
			directory, pin := guiG1SyntheticCorpus(t, actual)
			if _, positiveErr := guiG1ReadCorpus(directory, pin, cases, inputs); positiveErr != nil {
				t.Fatal("synthetic positive custody frame must pass before mutation")
			}
			pin = control.change(t, directory, pin)
			if _, refused := guiG1ReadCorpus(directory, pin, cases, inputs); refused == nil || refused.Error() != control.code {
				t.Fatalf("owning corpus refusal required: %s", control.code)
			}
		})
	}
}
