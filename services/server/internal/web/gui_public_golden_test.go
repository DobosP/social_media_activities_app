package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/flosch/pongo2/v6"
)

// This optional producer captures the ORIGINAL native Pongo renderer, not a
// templ candidate, Django oracle, owner-signed matrix or Native Live gate.
var guiPublicGoldenOutput = flag.String("gui-public-golden-output", "", "fresh private directory for synthetic original-native public HTML fixtures")
var guiPublicGoldenReference = flag.String("gui-public-golden-reference", "", "unchanged private original capture bound by docs/reviews/gui-public-original/checkpoint.json")

type guiPublicCase struct {
	ID, Group, Path, Language, Profile string
	Snapshot                           bool
}

func guiPublicCases() []guiPublicCase {
	var cases []guiPublicCase
	for _, language := range []string{"en", "ro"} {
		for _, page := range []struct{ name, path string }{{"privacy", "/privacy/"}, {"terms", "/terms/"}} {
			for _, profile := range []string{"default", "contrast"} {
				cases = append(cases, guiPublicCase{page.name + "-" + language + "-" + profile, "G0", page.path, language, profile, false})
			}
		}
		for _, available := range []bool{false, true} {
			cases = append(cases, guiPublicCase{fmt.Sprintf("open-data-%s-snapshot-%t", language, available), "G0", "/open-data/", language, "default", available})
		}
		cases = append(cases, guiPublicCase{"landing-" + language, "G1", "/", language, "default", false})
	}
	return cases
}

func guiPublicHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func guiPublicSources(root string) (map[string]string, error) {
	names := []string{"templates/base.html", "apps/web/templates/web/privacy.html", "apps/web/templates/web/terms.html", "apps/web/templates/web/open_data.html", "apps/web/templates/web/landing.html", "docs/reviews/gui-public-original/checkpoint.json"}
	for _, name := range []string{"server.go", "router.go", "renderer.go", "templates.go", "i18n.go", "views.go", "public_pages.go", "public_downloads.go", "public_structured.go", "routes.json", "public_routes.json", "gui_public_golden_test.go", "gui_public_trace_test.go"} {
		names = append(names, "services/server/internal/web/"+name)
	}
	err := filepath.WalkDir(filepath.Join(root, "locale"), func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && filepath.Ext(name) == ".po" {
			relative, err := filepath.Rel(root, name)
			if err != nil {
				return err
			}
			names = append(names, filepath.ToSlash(relative))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	result := make(map[string]string, len(names))
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return nil, err
		}
		result[name] = guiPublicHash(raw)
	}
	return result, nil
}

func guiPublicOutput(directory string) (*os.Root, error) {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return nil, fmt.Errorf("capture output must be a fresh canonical absolute directory")
	}
	parent := filepath.Dir(directory)
	for current := parent; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("capture ancestry must be regular directories")
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	info, err := os.Stat(parent)
	if err != nil || info.Mode().Perm() != 0o700 {
		return nil, fmt.Errorf("capture parent must be private mode0700")
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		return nil, err // Existing outputs and aliases are never overwritten.
	}
	return os.OpenRoot(directory)
}

func guiPublicWrite(output *os.Root, name string, raw []byte) error {
	file, err := output.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func TestGUIPublicOriginalCapture(t *testing.T) {
	root, err := filepath.Abs("../../../../")
	if err != nil {
		t.Fatal(err)
	}
	before, err := guiPublicSources(root)
	if err != nil {
		t.Fatal(err)
	}
	var reference map[string]guiPublicReferenceRecord
	if *guiPublicGoldenReference != "" {
		checkpoint, err := os.ReadFile(filepath.Join(root, "docs/reviews/gui-public-original/checkpoint.json"))
		if err != nil {
			t.Fatal(err)
		}
		reference, err = guiPublicReadReference(*guiPublicGoldenReference, checkpoint)
		if err != nil {
			t.Fatal(err)
		}
	}
	var output *os.Root
	if *guiPublicGoldenOutput != "" {
		output, err = guiPublicOutput(*guiPublicGoldenOutput)
		if err != nil {
			t.Fatal(err)
		}
		defer output.Close()
	}
	records := []map[string]any{}
	for _, fixture := range guiPublicCases() {
		t.Run(fixture.ID, func(t *testing.T) {
			// No session/account/domain query is admitted. This real auth service is
			// used only by the unchanged anonymous page's CSRF-preservation path.
			auth, err := authcore.New(authcore.Config{PublicURL: "https://gui-fixture.invalid"}, accounts.NewStore(nil))
			if err != nil {
				t.Fatal(err)
			}
			config := Config{Root: root, PublicURL: "https://gui-fixture.invalid", SiteName: "Synthetic public GUI fixture"}
			if fixture.Snapshot {
				config.SnapshotDir = t.TempDir()
				// Availability presentation only. This is NOT an exported dataset,
				// ingestion input or a claim about the snapshot's content/schema.
				if err := os.WriteFile(filepath.Join(config.SnapshotDir, "manifest.json"), []byte("{}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			server := NewServer(nil, auth, nil, nil, nil, nil, config)
			trace := &guiPublicTraceLoader{delegate: server.Renderer.loader}
			server.Renderer.set = pongo2.NewSet("native-social", trace)
			mux := http.NewServeMux()
			server.Register(mux)
			request := httptest.NewRequest(http.MethodGet, config.PublicURL+fixture.Path, nil)
			request.AddCookie(&http.Cookie{Name: "django_language", Value: fixture.Language})
			request.AddCookie(&http.Cookie{Name: "csrftoken", Value: strings.Repeat("g", 52)}) // fictional, uncredentialed input
			if fixture.Profile == "contrast" {
				for name, value := range map[string]string{"display_theme": "contrast", "display_text": "larger", "display_motion": "reduce"} {
					request.AddCookie(&http.Cookie{Name: name, Value: value})
				}
			}
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			raw := response.Body.Bytes()
			body := string(raw)
			if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/html; charset=utf-8" {
				t.Fatalf("original public renderer: status=%d MIME=%q", response.Code, response.Header().Get("Content-Type"))
			}
			for _, required := range []string{`lang="` + fixture.Language + `"`, `id="main"`, `href="/login/"`, `href="/register/"`, `href="/privacy/"`, `href="/terms/"`, `/static/css/base.css`} {
				if !strings.Contains(body, required) {
					t.Errorf("actual public HTML missing %q", required)
				}
			}
			for _, private := range []string{`class="has-tabbar"`, `data-meetups-owner=`, `action="/logout/"`} {
				if strings.Contains(body, private) {
					t.Errorf("anonymous fixture exposed authenticated shell %q", private)
				}
			}
			if fixture.Language == "ro" && !strings.Contains(body, "Activități") {
				t.Error("actual Romanian catalog was not used")
			}
			if fixture.Profile == "contrast" {
				for _, attribute := range []string{`data-theme="contrast"`, `data-text="larger"`, `data-motion="reduce"`} {
					if !strings.Contains(body, attribute) {
						t.Errorf("actual display branch missing %q", attribute)
					}
				}
			}
			if fixture.Path == "/open-data/" {
				if strings.Contains(body, `href="/open-data/snapshot/manifest.json"`) != fixture.Snapshot {
					t.Error("actual snapshot availability branch differs")
				}
				if !strings.Contains(body, `type="application/ld+json"`) || strings.Contains(body, "Snapshot manifest") != fixture.Snapshot {
					t.Error("actual dataset structured-data branch differs")
				}
			}
			policy := response.Header().Get("Content-Security-Policy-Report-Only")
			if policy == "" || response.Header().Get("Content-Security-Policy") != "" || response.Header().Get("Set-Cookie") != "" {
				t.Error("original anonymous report-only/fictional-CSRF behavior changed")
			}
			if err := guiPublicCheckLoads(fixture, trace.loads); err != nil {
				t.Error(err)
			}
			record := map[string]any{"case": fixture, "status": response.Code, "body_file": fixture.ID + ".html", "body_bytes": len(raw), "body_sha256": guiPublicHash(raw), "csp_header_sha256": guiPublicHash([]byte(policy)), "template_loads": trace.loads, "assertions_passed": !t.Failed()}
			if reference != nil {
				record["original_reference"] = reference[fixture.ID]
			}
			records = append(records, record)
			if output != nil {
				if err := guiPublicWrite(output, fixture.ID+".html", raw); err != nil {
					t.Error(err)
				}
			}
		})
	}
	after, err := guiPublicSources(root)
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) || len(records) != 14 {
		t.Error("source changed or original public case matrix was incomplete")
	}
	if reference != nil {
		checkpoint, err := os.ReadFile(filepath.Join(root, "docs/reviews/gui-public-original/checkpoint.json"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := guiPublicReadReference(*guiPublicGoldenReference, checkpoint); err != nil {
			t.Error(err)
		}
	}
	if output != nil {
		status := "captured"
		if t.Failed() {
			status = "failed"
		}
		manifest := map[string]any{"schema": 1, "status": status, "engine": "original-native-pongo2", "scope": "14 synthetic anonymous public registered-handler fixtures only; not Django oracle, Native Live, signed group coverage or retirement", "source_before": before, "source_after": after, "cases": records, "normalization": "none: raw HTML retained; nonces remain per-render, no body/attribute rewriting", "template_load_scope": "actual successful original-loader streams, not template-conditional coverage", "original_reference_verified": reference != nil, "raw_golden_parity": "not evaluated", "template_conditional_coverage": nil}
		raw, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := guiPublicWrite(output, "manifest.json", append(raw, '\n')); err != nil {
			t.Fatal(err)
		}
		t.Logf("original public capture: %d cases; private manifest at %s/manifest.json", len(records), *guiPublicGoldenOutput)
	}
}

func TestGUIPublicCaptureRefusesExistingOutput(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "private-parent")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(parent, "capture")
	first, err := guiPublicOutput(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := guiPublicWrite(first, "fixture.html", []byte("original bytes")); err != nil {
		t.Fatal(err)
	}
	if _, err := guiPublicOutput(directory); err == nil {
		t.Fatal("existing output accepted")
	}
	if err := guiPublicWrite(first, "fixture.html", []byte("replacement")); err == nil {
		t.Fatal("existing fixture overwritten")
	}
	raw, err := first.ReadFile("fixture.html")
	if err != nil || string(raw) != "original bytes" {
		t.Fatal("retained fixture changed")
	}
}
