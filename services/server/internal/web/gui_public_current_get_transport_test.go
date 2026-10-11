package web

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/flosch/pongo2/v6"
)

// The existing G1 root dispatches here only when explicitly requested. Its
// absent-flag body and all historical producer/receiver assertions stay intact.
var guiPublicCurrentGetTransport = flag.Bool("gui-public-current-get-transport", false, "explicit synthetic current-template OS/FS registered GET parity; existing isolated PG and released normalizer required; no capture")

var guiCurrentTransportFiles = []string{
	"apps/web/templates/web/_activity_card.html",
	"apps/web/templates/web/_near_me.html",
	"apps/web/templates/web/_share_box.html",
	"apps/web/templates/web/campaigns.html",
	"apps/web/templates/web/discover.html",
	"apps/web/templates/web/donate.html",
	"apps/web/templates/web/event_detail.html",
	"apps/web/templates/web/events.html",
	"apps/web/templates/web/landing.html",
	"apps/web/templates/web/landing_city.html",
	"apps/web/templates/web/landing_detail.html",
	"apps/web/templates/web/landing_index.html",
	"apps/web/templates/web/partners.html",
	"apps/web/templates/web/place_detail.html",
	"apps/web/templates/web/places.html",
	"apps/web/templates/web/places_list.html",
	"apps/web/templates/web/series_detail.html",
	"apps/web/templates/web/series_list.html",
	"locale/ro/LC_MESSAGES/django.po",
	"templates/base.html",
}

// The unchanged typed G1 source guard owns102 paths. These22 additional paths
// close old110/current20/new-module union124; no response corpus is loaded here.
var guiCurrentTransportAdditionalInputs = []string{
	"apps/web/templates/web/_share_box.html",
	"apps/web/templates/web/campaigns.html",
	"apps/web/templates/web/discover.html",
	"apps/web/templates/web/donate.html",
	"apps/web/templates/web/event_detail.html",
	"apps/web/templates/web/events.html",
	"apps/web/templates/web/landing_city.html",
	"apps/web/templates/web/landing_detail.html",
	"apps/web/templates/web/landing_index.html",
	"apps/web/templates/web/partners.html",
	"apps/web/templates/web/place_detail.html",
	"apps/web/templates/web/series_detail.html",
	"apps/web/templates/web/series_list.html",
	"services/server/internal/donations/ledger.go",
	"services/server/internal/social/activities.go",
	"services/server/internal/social/groups.go",
	"services/server/internal/social/service.go",
	"services/server/internal/testdb/testdb.go",
	"services/server/internal/web/gui_public_current_get_transport_test.go",
	"services/server/internal/web/gui_public_get_walk_normalize_test.go",
	"services/server/internal/web/social_pages_test.go",
	"services/server/internal/web/testdata/gui-public-get-walk/normalization-reference.json",
}

type guiCurrentTransportSnapshot struct {
	files    fstest.MapFS
	raw      map[string][]byte
	bindings map[string]guiG1Binding
}

type guiCurrentTransportResponse struct {
	recorder  *httptest.ResponseRecorder
	loads     []guiPublicTemplateLoad
	canonical string
}

type guiCurrentTransportPair struct {
	item     guiPublicGetCase
	disk     guiCurrentTransportResponse
	files    guiCurrentTransportResponse
	campaign int64
	enforce  bool
}

func guiCurrentTransportReadBinding(root, name string, limit int64) ([]byte, guiG1Binding, error) {
	if !fs.ValidPath(name) || strings.Contains(name, "\\") {
		return nil, guiG1Binding{}, fmt.Errorf("current-input-path")
	}
	path := filepath.Join(root, filepath.FromSlash(name))
	raw, err := guiPublicBoundFile(path, limit)
	info, statErr := os.Lstat(path)
	if err != nil || statErr != nil || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return nil, guiG1Binding{}, fmt.Errorf("current-input-file")
	}
	return raw, guiG1Binding{len(raw), guiPublicHash(raw), 0o100000 | uint32(info.Mode().Perm())}, nil
}

func guiCurrentTransportInputs(root string) (map[string]guiG1Binding, error) {
	rows, err := guiG1CurrentInputs(root)
	if err != nil || len(rows) != 102 {
		return nil, fmt.Errorf("current-source102")
	}
	for _, name := range guiCurrentTransportAdditionalInputs {
		if _, exists := rows[name]; exists {
			return nil, fmt.Errorf("current-source-duplicate")
		}
		_, binding, err := guiCurrentTransportReadBinding(root, name, 2<<20)
		if err != nil {
			return nil, err
		}
		rows[name] = binding
	}
	if len(guiCurrentTransportAdditionalInputs) != 22 || len(rows) != 124 {
		return nil, fmt.Errorf("current-source124")
	}
	return rows, nil
}

func guiCurrentTransportSnapshotFrom(root string) (*guiCurrentTransportSnapshot, error) {
	snapshot := &guiCurrentTransportSnapshot{files: fstest.MapFS{}, raw: map[string][]byte{}, bindings: map[string]guiG1Binding{}}
	for _, name := range guiCurrentTransportFiles {
		if _, exists := snapshot.files[name]; exists {
			return nil, fmt.Errorf("current-template-duplicate")
		}
		limit := int64(512 << 10)
		if strings.HasPrefix(name, "locale/") {
			limit = 2 << 20
		}
		raw, binding, err := guiCurrentTransportReadBinding(root, name, limit)
		if err != nil {
			return nil, err
		}
		snapshot.raw[name] = append([]byte(nil), raw...)
		snapshot.files[name] = &fstest.MapFile{Data: append([]byte(nil), raw...), Mode: 0o444}
		snapshot.bindings[name] = binding
	}
	if len(snapshot.files) != 20 {
		return nil, fmt.Errorf("current-template20")
	}
	return snapshot, nil
}

func (s *guiCurrentTransportSnapshot) check(root string) error {
	if len(s.files) != 20 || len(s.raw) != 20 || len(s.bindings) != 20 {
		return fmt.Errorf("current-template20")
	}
	for _, name := range guiCurrentTransportFiles {
		file, exists := s.files[name]
		if !exists || file == nil || file.Mode != 0o444 || !bytes.Equal(file.Data, s.raw[name]) {
			return fmt.Errorf("current-memory-template-changed")
		}
		limit := int64(512 << 10)
		if strings.HasPrefix(name, "locale/") {
			limit = 2 << 20
		}
		raw, binding, err := guiCurrentTransportReadBinding(root, name, limit)
		if err != nil || binding != s.bindings[name] || !bytes.Equal(raw, s.raw[name]) {
			return fmt.Errorf("current-source-template-changed")
		}
	}
	return nil
}

func guiCurrentTransportPrerequisites(dsn string, tool *guiPublicNormalizer) error {
	if guiPublicGetRequireDSN(dsn) != nil {
		return fmt.Errorf("current-DSN-required")
	}
	if tool == nil {
		return fmt.Errorf("current-normalizer-required")
	}
	return nil
}

func guiCurrentTransportGET(t *testing.T, fixture guiPublicGetFixture, snapshot *guiCurrentTransportSnapshot, item guiPublicGetCase, filesystem bool, requests *int) guiCurrentTransportResponse {
	t.Helper()
	s := fixture.server
	renderer := NewRenderer(s.Config.Root)
	if filesystem {
		var err error
		renderer, err = NewRendererFS(s.Config.Root, snapshot.files)
		if err != nil {
			t.Fatal("current-FS-constructor")
		}
	}
	renderer.CSPEnforce = s.Config.CSPEnforce
	trace := &guiPublicTraceLoader{delegate: renderer.loader}
	renderer.set = pongo2.NewSet("native-social-current-G1", trace)
	s.Renderer = renderer
	mux := http.NewServeMux()
	s.Register(mux)
	request := httptest.NewRequest(http.MethodGet, s.Config.PublicURL+item.Path, nil)
	request.AddCookie(&http.Cookie{Name: "django_language", Value: item.Language})
	request.AddCookie(&http.Cookie{Name: "csrftoken", Value: strings.Repeat("g", 52)})
	if item.Profile == "contrast" {
		for name, value := range map[string]string{"display_theme": "contrast", "display_text": "larger", "display_motion": "reduce"} {
			request.AddCookie(&http.Cookie{Name: name, Value: value})
		}
	}
	recorder := httptest.NewRecorder()
	*requests = *requests + 1
	mux.ServeHTTP(recorder, request)
	canonical := s.Config.PublicURL + request.URL.Path
	if item.Route == "place_detail" || item.Route == "place_detail_slug" {
		canonical = fmt.Sprintf("%s/places/%d/original-synthetic-hall/", s.Config.PublicURL, fixture.place)
	}
	if item.Route == "event_detail" || item.Route == "event_detail_slug" {
		canonical = fmt.Sprintf("%s/events/%d/original-synthetic-event/", s.Config.PublicURL, fixture.event)
	}
	return guiCurrentTransportResponse{recorder, append([]guiPublicTemplateLoad(nil), trace.loads...), canonical}
}

func guiCurrentTransportResponseCheck(item guiPublicGetCase, response guiCurrentTransportResponse, snapshot *guiCurrentTransportSnapshot, campaign int64, enforce bool) (string, error) {
	if response.recorder == nil || response.recorder.Code != item.Status {
		return "", fmt.Errorf("current-status")
	}
	if err := guiPublicGetCheck(item, response.recorder, response.loads, response.canonical); err != nil {
		return "", fmt.Errorf("current-http-%s", err.Error())
	}
	for _, load := range response.loads {
		name := "apps/web/templates/" + load.Name
		if load.Name == "base.html" {
			name = "templates/base.html"
		}
		raw, exists := snapshot.raw[name]
		if !exists {
			return "", fmt.Errorf("current-template-stream")
		}
		transformed := []byte(transformTemplate(string(raw)))
		if load.Bytes != len(transformed) || load.SHA256 != guiPublicHash(transformed) {
			return "", fmt.Errorf("current-template-stream")
		}
	}
	if item.Status != http.StatusOK {
		return "", nil
	}
	if item.ID == "donate-selected" && !bytes.Contains(response.recorder.Body.Bytes(), []byte(fmt.Sprintf(`value="%d" selected`, campaign))) {
		return "", fmt.Errorf("current-campaign-selection")
	}
	policy, other := "Content-Security-Policy-Report-Only", "Content-Security-Policy"
	if enforce {
		policy, other = other, policy
	}
	if response.recorder.Header().Get(policy) == "" || response.recorder.Header().Get(other) != "" {
		return "", fmt.Errorf("current-CSP-header-kind")
	}
	binding, err := guiPublicScriptNonceBinding(response.recorder.Body.String(), response.recorder.Header().Get(policy), nil)
	if err != nil {
		return "", fmt.Errorf("current-nonce-binding")
	}
	nonceHash, okay := binding["header_nonce_sha256"].(string)
	if !okay || len(nonceHash) != 64 {
		return "", fmt.Errorf("current-nonce-binding")
	}
	return nonceHash, nil
}

func guiCurrentTransportPairCheck(pair guiCurrentTransportPair, snapshot *guiCurrentTransportSnapshot) error {
	if _, err := guiCurrentTransportResponseCheck(pair.item, pair.disk, snapshot, pair.campaign, pair.enforce); err != nil {
		return err
	}
	if _, err := guiCurrentTransportResponseCheck(pair.item, pair.files, snapshot, pair.campaign, pair.enforce); err != nil {
		return err
	}
	for _, name := range []string{"Content-Type", "Cache-Control", "Location", "Vary", "Reporting-Endpoints"} {
		if pair.disk.recorder.Header().Get(name) != pair.files.recorder.Header().Get(name) {
			return fmt.Errorf("current-HTTP-header-agreement")
		}
	}
	if pair.disk.canonical != pair.files.canonical {
		return fmt.Errorf("current-canonical-request")
	}
	if pair.item.Status != http.StatusOK && !bytes.Equal(pair.disk.recorder.Body.Bytes(), pair.files.recorder.Body.Bytes()) {
		return fmt.Errorf("current-native-negative-body")
	}
	return nil
}

func guiCurrentTransportCanonicalAgreement(disk, files []byte) error {
	if len(disk) == 0 || len(files) == 0 || !bytes.Equal(disk, files) {
		return fmt.Errorf("current-canonical-difference")
	}
	return nil
}

func guiCurrentTransportClone(response guiCurrentTransportResponse) guiCurrentTransportResponse {
	copy := response
	copy.loads = append([]guiPublicTemplateLoad(nil), response.loads...)
	copy.recorder = &httptest.ResponseRecorder{Code: response.recorder.Code, HeaderMap: response.recorder.Header().Clone(), Body: bytes.NewBuffer(append([]byte(nil), response.recorder.Body.Bytes()...))}
	return copy
}

func guiPublicCurrentGetTransportWalk(t *testing.T) {
	t.Helper()
	if *guiPublicGetWalkOutput != "" || *guiPublicGoldenOutput != "" || *guiPublicGoldenFSOutput != "" || *guiPublicGetWalkNormalizeReference != "" {
		t.Fatal("current-transport-is-not-a-capture-mode")
	}
	if guiPublicGetRequireDSN(*webDomainDSN) != nil {
		t.Fatal("current-DSN-required-before-testdb")
	}
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal("current-root")
	}
	tool, err := guiPublicNewNormalizer(root)
	if err != nil || guiCurrentTransportPrerequisites(*webDomainDSN, tool) != nil {
		t.Fatal("current-normalizer-prerequisites-before-testdb")
	}
	checkpoint, err := os.ReadFile(filepath.Join(root, "docs/reviews/gui-public-original/checkpoint.json"))
	if err != nil {
		t.Fatal("current-original15-checkpoint")
	}
	original, err := guiPublicReadReference(*guiPublicGoldenReference, checkpoint)
	if err != nil {
		t.Fatal("current-original15-reference")
	}
	before, err := guiCurrentTransportInputs(root)
	if err != nil {
		t.Fatal("current-source124")
	}
	snapshot, err := guiCurrentTransportSnapshotFrom(root)
	if err != nil {
		t.Fatal("current-template20")
	}
	requests, pairs, HTML, negative := 0, 0, 0, 0
	t.Cleanup(func() {
		after, sourceErr := guiCurrentTransportInputs(root)
		nextCheckpoint, checkpointErr := os.ReadFile(filepath.Join(root, "docs/reviews/gui-public-original/checkpoint.json"))
		nextOriginal, referenceErr := guiPublicReadReference(*guiPublicGoldenReference, nextCheckpoint)
		if sourceErr != nil || !reflect.DeepEqual(before, after) || snapshot.check(root) != nil || checkpointErr != nil || !bytes.Equal(checkpoint, nextCheckpoint) || referenceErr != nil || !reflect.DeepEqual(original, nextOriginal) || tool.checkUnchanged() != nil {
			t.Error("current-source-template-original-tool-changed")
		}
		t.Logf("current transport requests=%d pairs=%d HTML=%d native_negative=%d source_inputs=%d; current OS/FS only, no historical-current equality or baseline", requests, pairs, HTML, negative, len(before))
		if requests != 74 || pairs != 37 || HTML != 31 || negative != 6 {
			t.Error("current-transport-incomplete-census")
		}
	})
	cases, err := guiPublicGetCases()
	if err != nil || len(cases) != 37 {
		t.Fatal("current-registry37")
	}
	fixtures := map[string]guiPublicGetFixture{}
	for _, state := range []string{"empty", "populated"} {
		fixtures[state] = guiPublicGetFixtureFor(t, root, state)
	}
	var baseline *guiCurrentTransportPair
	var baselineDiskCanonical []byte
	var baselineCanonical []byte
	for _, item := range cases {
		t.Run("current-transport-"+item.ID, func(t *testing.T) {
			fixture := fixtures[item.State]
			item.Path = strings.NewReplacer("{place}", fmt.Sprint(fixture.place), "{event}", fmt.Sprint(fixture.event)).Replace(item.Path)
			pair := guiCurrentTransportPair{item: item, campaign: fixture.campaign, enforce: fixture.server.Config.CSPEnforce}
			pair.disk = guiCurrentTransportGET(t, fixture, snapshot, item, false, &requests)
			pair.files = guiCurrentTransportGET(t, fixture, snapshot, item, true, &requests)
			if err := guiCurrentTransportPairCheck(pair, snapshot); err != nil {
				t.Fatalf("current paired HTTP refused: %s", err.Error())
			}
			if item.Status != http.StatusOK {
				negative++
				pairs++
				t.Logf("current pair=%s status=%d os_body_sha256=%s fs_body_sha256=%s; native negative NOT HTML NORMALIZED", item.ID, item.Status, guiPublicHash(pair.disk.recorder.Body.Bytes()), guiPublicHash(pair.files.recorder.Body.Bytes()))
				return
			}
			disk, diskHard, diskErr := tool.normalize(pair.disk.recorder.Body.Bytes())
			files, filesHard, filesErr := tool.normalize(pair.files.recorder.Body.Bytes())
			t.Logf("current pair=%s status=200 os_body_sha256=%s fs_body_sha256=%s os_canonical_sha256=%s fs_canonical_sha256=%s os_hard_count=%d fs_hard_count=%d os_hard_codes=%v fs_hard_codes=%v; no renderer-oracle or baseline claim", item.ID, guiPublicHash(pair.disk.recorder.Body.Bytes()), guiPublicHash(pair.files.recorder.Body.Bytes()), guiPublicHash(disk), guiPublicHash(files), len(diskHard), len(filesHard), guiG1HardCodes(diskHard), guiG1HardCodes(filesHard))
			if diskErr != nil || filesErr != nil || len(diskHard) != 0 || len(filesHard) != 0 {
				t.Fatal("current-paired-strict-normalization-refused")
			}
			if err := guiCurrentTransportCanonicalAgreement(disk, files); err != nil {
				t.Fatal("current-canonical-difference")
			}
			pairs++
			HTML++
			if item.ID == "landing-en-default" {
				retained := pair
				baseline = &retained
				baselineDiskCanonical = append([]byte(nil), disk...)
				baselineCanonical = append([]byte(nil), files...)
			}
		})
	}
	for _, name := range []string{"missing-dsn", "missing-normalizer", "status-mismatch", "template-stream-mismatch", "nonce-binding", "canonical-difference", "owning-hard-finding"} {
		t.Run("current-transport-refuse-"+name, func(t *testing.T) {
			if guiCurrentTransportPrerequisites(*webDomainDSN, tool) != nil {
				t.Fatal("genuine current prerequisites must pass before refusal")
			}
			if name == "missing-dsn" {
				if err := guiCurrentTransportPrerequisites("", tool); err == nil || err.Error() != "current-DSN-required" {
					t.Fatal("missing current DSN admitted")
				}
				return
			}
			if name == "missing-normalizer" {
				if err := guiCurrentTransportPrerequisites(*webDomainDSN, nil); err == nil || err.Error() != "current-normalizer-required" {
					t.Fatal("missing current normalizer admitted")
				}
				return
			}
			if baseline == nil || len(baselineCanonical) == 0 || guiCurrentTransportPairCheck(*baseline, snapshot) != nil || guiCurrentTransportCanonicalAgreement(baselineDiskCanonical, baselineCanonical) != nil {
				t.Fatal("genuine current paired observation must pass before mutation")
			}
			beforeBody := guiPublicHash(baseline.files.recorder.Body.Bytes())
			changed := *baseline
			changed.files = guiCurrentTransportClone(baseline.files)
			switch name {
			case "status-mismatch":
				changed.files.recorder.Code = 503
				if err := guiCurrentTransportPairCheck(changed, snapshot); err == nil || err.Error() != "current-status" {
					t.Fatal("copied current status mismatch admitted")
				}
			case "template-stream-mismatch":
				if len(changed.files.loads) == 0 {
					t.Fatal("current transformed stream target absent")
				}
				changed.files.loads[0].SHA256 = strings.Repeat("0", 64)
				if err := guiCurrentTransportPairCheck(changed, snapshot); err == nil || err.Error() != "current-template-stream" {
					t.Fatal("current stream mismatch hidden by equal HTML")
				}
			case "nonce-binding":
				tags := regexp.MustCompile(`<script nonce="[^"]+" id="site-js"`).FindAll(changed.files.recorder.Body.Bytes(), -1)
				if len(tags) != 1 {
					t.Fatal("unique current script nonce target required")
				}
				raw, err := guiG1ReplaceOnce(changed.files.recorder.Body.Bytes(), string(tags[0]), `<script nonce="wrong-synthetic-current-nonce" id="site-js"`)
				if err != nil {
					t.Fatal("current nonce mutation target")
				}
				changed.files.recorder.Body = bytes.NewBuffer(raw)
				canonical, hard, normalizeErr := tool.normalize(raw)
				if normalizeErr != nil || len(hard) != 0 || !bytes.Equal(canonical, baselineCanonical) {
					t.Fatal("public nonce volatility must precede native binding refusal")
				}
				if err := guiCurrentTransportPairCheck(changed, snapshot); err == nil || err.Error() != "current-nonce-binding" {
					t.Fatal("wrong actual header/script binding hidden by canonical equality")
				}
			case "canonical-difference":
				raw, err := guiG1ReplaceOnce(changed.files.recorder.Body.Bytes(), `<h1>Meet people. Do real things. In person.</h1>`, `<h1>Meet synthetic neighbours. Do real things. In person.</h1>`)
				if err != nil {
					t.Fatal("current visible-text mutation target")
				}
				canonical, hard, normalizeErr := tool.normalize(raw)
				if normalizeErr != nil || len(hard) != 0 {
					t.Fatal("safe current text difference acquired unrelated hard failure")
				}
				if err := guiCurrentTransportCanonicalAgreement(baselineDiskCanonical, canonical); err == nil || err.Error() != "current-canonical-difference" {
					t.Fatal("current visible text difference admitted")
				}
			case "owning-hard-finding":
				raw, err := guiG1ReplaceOnce(changed.files.recorder.Body.Bytes(), `<link rel="canonical" href="https://fixture.local/">`, `<link rel="canonical" href="">`)
				if err != nil {
					t.Fatal("current hard mutation target")
				}
				_, hard, normalizeErr := tool.normalize(raw)
				if normalizeErr == nil || len(hard) != 1 || !reflect.DeepEqual(guiG1HardCodes(hard), []string{"empty-url"}) {
					t.Fatal("current hard refusal must own exactly empty-url")
				}
			}
			if guiPublicHash(baseline.files.recorder.Body.Bytes()) != beforeBody || snapshot.check(root) != nil {
				t.Fatal("current control changed genuine input buffers")
			}
		})
	}
}
