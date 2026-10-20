package web

import (
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

	"github.com/flosch/pongo2/v6"
)

var completionPreviewOutput = flag.String("native-completion-preview-output", "", "fresh private output for synthetic current Go-rendered desktop fixtures; no DB or real account data")

// This optional exporter calls the ordinary current Renderer, with its actual
// nonce/headers. It never calls a historical renderer or changes a baseline.
// Desktop qualification invokes it explicitly; default unit runs make no UI claim.
func TestNativeCompletionDesktopExport(t *testing.T) {
	if *completionPreviewOutput == "" {
		return
	}
	output := *completionPreviewOutput
	if !filepath.IsAbs(output) || filepath.Clean(output) != output {
		t.Fatal("absolute fresh output required")
	}
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	var states []map[string]any
	for _, language := range []string{"en", "ro"} {
		names := make([]string, 0, len(completionReferenceNames))
		for name := range completionReferenceNames {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			renderer, request, data := completionPreviewFixture(t, name)
			renderer.CSPEnforce = true
			delete(data, "request") // Common renderer owns its actual CSP nonce.
			data["language"] = language
			if language == "ro" {
				data["display_theme"] = "dark"
				data["display_text"] = "large"
				data["display_motion"] = "reduce"
			}
			request.Header.Set("Accept-Language", language)
			response := httptest.NewRecorder()
			if err := renderer.Render(response, request, name, data); err != nil {
				t.Fatal(name, language, err)
			}
			if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/html; charset=utf-8" {
				t.Fatal("current ordinary HTML transport unavailable")
			}
			id := strings.TrimSuffix(strings.TrimPrefix(name, "web/"), ".html") + "-" + language
			if err := os.WriteFile(filepath.Join(output, id+".html"), response.Body.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			states = append(states, map[string]any{"id": id, "template": name, "language": language, "fragment": name == "web/_person_card.html", "headers": response.Header(), "bytes": response.Body.Len(), "sha256": guiPublicHash(response.Body.Bytes())})
		}
	}
	if len(states) != 78 {
		t.Fatal("all39 selected destinations in both languages required")
	}
	manifest := map[string]any{"kind": "current-native-completion-desktop-fixtures", "scope": "Synthetic authorized producer-shaped snapshots and ordinary current Renderer; no registered-GET, authentication, real-account, provider or production claim", "states": states}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil || os.WriteFile(filepath.Join(output, "manifest.json"), append(raw, '\n'), 0o600) != nil {
		t.Fatal("preview manifest unavailable")
	}
}

func completionPreviewFixture(t *testing.T, name string) (*Renderer, *http.Request, pongo2.Context) {
	t.Helper()
	switch {
	case name == "web/my_meetups.html" || name == "web/my_venues.html":
		r, request, data, _ := collectionsNativeFixture(t, name == "web/my_venues.html")
		return r, request, data
	case informationTemplate(name):
		return informationNativeFixture(t, strings.TrimSuffix(strings.TrimPrefix(name, "web/"), ".html"), name == "web/my_donations.html")
	case nativeSeriesGaugeTemplates[name]:
		return seriesGaugeNativeFixture(t, name)
	case nativeTrustTemplates[name]:
		return trustFixture(t, name)
	case name == "web/place_detail.html":
		return publicPlaceNativeFixture(t)
	case publicNativeTemplate(name):
		kind := strings.TrimSuffix(strings.TrimPrefix(name, "web/"), ".html")
		r, request, data, _ := publicNativeFixture(t, kind, kind == "communities_graph" || kind == "event_detail")
		return r, request, data
	case name == "web/activity_form.html" || name == "web/activity_edit.html" || name == "web/activity_detail.html" || name == "web/activity_safe_exit.html" || name == "web/place_propose.html" || name == "web/place_claim.html" || name == "web/places_pending.html" || name == "web/messages.html":
		return workflowsFixture(t, name)
	}
	t.Fatal(fmt.Sprintf("unclassified completion preview %q", name))
	return nil, nil, nil
}
