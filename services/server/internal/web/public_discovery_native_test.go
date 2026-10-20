package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

func publicNativeFixture(t *testing.T, kind string, signed bool) (*Renderer, *http.Request, pongo2.Context, string) {
	t.Helper()
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	path := "/generated-public/" + kind + "/"
	request := httptest.NewRequest(http.MethodGet, "https://public.example"+path+"?city=Preserved&activity=chosen", nil)
	actor := platform.Actor{}
	if signed {
		actor = platform.Actor{ID: 981, PublicID: "55555555-5555-4555-8555-555555555981", Username: "generated-public", DisplayName: "Generated <viewer>", Cohort: "adult", AgeBand: "adult", IsActive: true}
		request = platform.WithActor(request, actor)
	}
	data := pongo2.Context{"language": "en", "display_theme": "auto", "display_text": "normal", "display_motion": "auto", "site_name": "Generated public site", "canonical_url": "https://public.example" + path, "csrf": "synthetic-public-csrf", "og_locale": "en_GB", "user": socialActor(actor), "request": map[string]any{"path": path, "csp_nonce": "synthetic-public-nonce"}, "PRIVATE_unselected": "PRIVATE-unselected-public-snapshot", "local_poc_mode": false, "local_poc_content": false}
	stamp := time.Date(2026, time.October, 25, 1, 30, 0, 0, time.UTC)
	switch kind {
	case "communities_graph":
		data["communities"] = []map[string]any{{"name": "Zed first supplied", "slug": "zed-supplied"}, {"name": "Alpha second supplied", "slug": "alpha-supplied"}}
	case "discover":
		data["activities"] = []map[string]any{{"title": "Zed first supplied", "activity_type": map[string]any{"name": "Supplied activity type"}, "place": map[string]any{"name": "Supplied public place"}, "starts_at": stamp}, {"title": "Alpha second supplied", "activity_type": map[string]any{"name": "Other kind"}, "place": map[string]any{"name": nil}, "starts_at": stamp.Add(time.Hour)}}
		data["groups"] = []map[string]any{{"title": "Supplied public group", "description": strings.Repeat("ș", 205), "activity_type": nil, "category": map[string]any{"name": "Supplied category"}, "area": map[string]any{"name": "Supplied area"}}}
	case "places":
		data["categories"] = []map[string]any{{"slug": "zed-category", "name": "Zed category"}, {"slug": "alpha-category", "name": "Alpha category"}}
		data["type_vocabulary"] = []map[string]any{{"slug": "public-type", "name": "Public </script><script>payload</script>", "aliases": []any{"second alias", "first alias"}, "category": "child-category", "categoryName": "Child category", "topCategory": "parent-category", "topCategoryName": "Parent category"}}
	case "event_detail":
		data["event"] = map[string]any{"pk": int64(9007199254740993), "title": "Supplied public event", "description": "First line\nSecond line\n\nSecond paragraph", "starts_at": stamp, "ends_at": stamp.Add(time.Hour), "place": map[string]any{"pk": int64(9007199254740991), "display_name": "Supplied <hall>"}, "activity_type": map[string]any{"pk": int64(71), "name": "Supplied type"}, "url": "https://event.example/source?q=keep&second=two", "local_poc": true, "get_source_display": "RO-EDU", "get_lifecycle_status_display": "Scheduled"}
		data["event_discoverable"] = true
		data["event_reliability"] = nil
		data["can_report_event"] = signed
		data["can_convene"] = signed
		data["attribution_credit"] = map[string]any{"attribution": "Supplied source credit", "license_name": "CC BY 4.0", "provenance_url": "https://source.example/record?original=1&keep=2"}
		data["share_kind"] = "event"
		data["share_obj_id"] = int64(9007199254740993)
		data["share_targets"] = []any{}
	case "open_data":
		data["snapshot_available"] = false
	}
	return NewRenderer(root), request, data, "web/" + kind + ".html"
}

func TestNativePublicDiscoveryPreservesSelectedOrderAndPublicOnlyFields(t *testing.T) {
	r, request, data, name := publicNativeFixture(t, "discover", false)
	before, _ := json.Marshal(data)
	p, err := r.publicNativePage(request, name, data)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(data)
	if !bytes.Equal(before, after) || len(p.Activities) != 2 || p.Activities[0].Title != "Zed first supplied" || p.Activities[1].Title != "Alpha second supplied" || p.Activities[1].Place != "" || p.Groups[0].Type != "Supplied category" || p.Groups[0].Description != strings.Repeat("ș", 197)+"..." {
		t.Fatal("order, nullable public fields, exact rune truncation or source immutability changed")
	}
	raw, err := r.renderPublicNative(request, name, data)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if strings.Index(body, "Zed first supplied") > strings.Index(body, "Alpha second supplied") || !strings.Contains(body, djangoDate(data["activities"].([]map[string]any)[0]["starts_at"].(time.Time), "j M Y, H:i")) {
		t.Fatal("source dates/order changed")
	}
	for _, private := range []string{"PRIVATE-unselected-public-snapshot", "owner_id", "guardian_identifier", "cohort", "Supplied type secret"} {
		if strings.Contains(body, private) {
			t.Fatal("unselected private snapshot field rendered")
		}
	}
	if strings.Contains(body, "/logout/") || strings.Contains(body, "data-meetups-owner") || strings.Contains(body, "/activities/new/") {
		t.Fatal("anonymous shell gained signed controls")
	}
	data["activities"].([]map[string]any)[0]["title"] = "mutated"
	if p.Activities[0].Title != "Zed first supplied" {
		t.Fatal("projection retained mutable rows")
	}
}

func TestNativePublicMapAndGraphKeepExistingHooksJSONAndFallbacks(t *testing.T) {
	r, request, data, name := publicNativeFixture(t, "places", false)
	before, _ := json.Marshal(data)
	p, err := r.publicNativePage(request, name, data)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(data)
	if !bytes.Equal(before, after) {
		t.Fatal("map snapshot mutated")
	}
	var vocabulary []publicNativeVocabulary
	if err := json.Unmarshal([]byte(p.MapVocabulary), &vocabulary); err != nil || len(vocabulary) != 1 || len(vocabulary[0].Aliases) != 2 || vocabulary[0].Aliases[0] != "second alias" {
		t.Fatal("vocabulary shape or aliases order changed", err)
	}
	raw, err := r.renderPublicNative(request, name, data)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, hook := range []string{`id="places-type-vocabulary"`, `type="application/json"`, `id="map-search"`, `data-vocab-island="places-type-vocabulary"`, `aria-controls="map-search-listbox"`, `data-map-search-clear hidden`, `id="map-search-listbox"`, `data-map-filter="category"`, `data-map-filter="has-upcoming"`, `data-map-filter="open-now"`, `id="map"`, `data-places-url="/api/places/?page_size=500"`, `data-worker-url="/static/vendor/maplibre/maplibre-gl-csp-worker.js"`, `href="/places/list/"`, `src="/static/vendor/maplibre/maplibre-gl-csp.js"`, `src="/static/js/places-map.js"`} {
		if !strings.Contains(body, hook) {
			t.Fatalf("lost map hook %s", hook)
		}
	}
	if strings.Contains(body, "</script><script>payload") || strings.Contains(body, "PRIVATE-unselected-public-snapshot") || strings.Index(body, "Zed category") > strings.Index(body, "Alpha category") {
		t.Fatal("map JSON injection, privacy or category ordering changed")
	}
	r, request, data, name = publicNativeFixture(t, "communities_graph", true)
	raw, err = r.renderPublicNative(request, name, data)
	if err != nil {
		t.Fatal(err)
	}
	body = string(raw)
	for _, hook := range []string{`id="mz-graph-search"`, `id="mz-graph-counts" checked`, `id="mz-graph"`, `<noscript>`, `href="/communities/"`, `/static/css/community-graph-vendor.css`, `/static/js/vendor/3d-force-graph-csp.min.js`, `/static/js/community-graph.js`, `href="/communities/zed-supplied/"`} {
		if !strings.Contains(body, hook) {
			t.Fatalf("lost graph hook %s", hook)
		}
	}
	if strings.Index(body, "Zed first supplied") > strings.Index(body, "Alpha second supplied") {
		t.Fatal("graph text fallback order changed")
	}
}

func TestNativePublicEventPreservesSuppliedActionsDisclosuresAndDSTDates(t *testing.T) {
	r, request, data, name := publicNativeFixture(t, "event_detail", true)
	data["related_landing"] = map[string]any{"name": "Related <topic>", "url": "/things-to-do/test-area/test-type/"}
	p, err := r.publicNativePage(request, name, data)
	if err != nil {
		t.Fatal(err)
	}
	stamp := data["event"].(map[string]any)["starts_at"].(time.Time)
	if p.Event.Start != djangoDate(stamp, "l j F Y, H:i") || p.Event.End != djangoDate(stamp.Add(time.Hour), "H:i") || !strings.Contains(p.Event.OrganizeURL, "starts_at=2026-10-25T03%3A30") || !strings.Contains(p.Event.OrganizeURL, "place=9007199254740991&activity_type=71") || !strings.Contains(string(p.Event.Related), `href="/things-to-do/test-area/test-type/"`) {
		t.Fatal("per-instant Bucharest DST date, exact prefill identities, or related URL changed")
	}
	raw, err := r.renderPublicNative(request, name, data)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, wanted := range []string{"LOCAL POC — not cleared for publication", "CC BY 4.0", "Supplied source credit", "Supplied &lt;hall&gt;", "Related &lt;topic&gt;", `name="kind" value="cancelled"`, `name="kind" value="moved"`, `name="kind" value="wrong_time"`, routeURL("event_report", int64(9007199254740993)), `First line<br>Second line`, `Second paragraph`, routeURL("gauge_create") + `?event=9007199254740993`} {
		if !strings.Contains(body, wanted) {
			t.Fatalf("event disclosure/action lost: %s", wanted)
		}
	}
	if strings.Contains(body, "PRIVATE-unselected-public-snapshot") || strings.Contains(body, "reported this event may have changed") {
		t.Fatal("nil reliability fabricated warning or private fields leaked")
	}
	data["event_discoverable"], data["can_report_event"], data["can_convene"] = false, false, false
	data["event_reliability"] = "unverified"
	data["event"].(map[string]any)["get_lifecycle_status_display"] = "Cancelled"
	raw, err = r.renderPublicNative(request, name, data)
	if err != nil {
		t.Fatal(err)
	}
	body = string(raw)
	if strings.Contains(body, `name="kind"`) || strings.Contains(body, "starts_at=") || strings.Contains(body, "?event=") || !strings.Contains(body, "This event is not listed as an upcoming activity.") || !strings.Contains(body, "Members reported this event may have changed") {
		t.Fatal("supplied discoverability/report/convene flags changed")
	}
}

func TestNativePublicSEOJSONAndSnapshotDisclosureStayProducerSupplied(t *testing.T) {
	r, request, data, name := publicNativeFixture(t, "open_data", false)
	data["structured_data"] = publicLD(map[string]any{"@context": "https://schema.org", "@type": "Dataset", "name": "Public </script><script>payload", "id": int64(9007199254740993), "source_access": "metadata_only", "tdm": "reserved"})
	data["breadcrumb_data"] = publicLD(map[string]any{"@type": "BreadcrumbList", "itemListElement": []any{}})
	data["google_site_verification"] = "synthetic <verification>"
	data["bing_site_verification"] = "synthetic-bing"
	p, err := r.publicNativePage(request, name, data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(p.Structured), "9007199254740993") || !strings.Contains(string(p.Structured), `"source_access":"metadata_only"`) || strings.Contains(string(p.Structured), "</script>") {
		t.Fatal("public JSON numeric/source-access/TDM bytes or escaping changed")
	}
	raw, err := r.renderPublicNative(request, name, data)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, `rel="canonical" href="https://public.example/generated-public/open_data/"`) || !strings.Contains(body, `property="og:locale" content="en_GB"`) || strings.Contains(body, "?city=Preserved") || strings.Contains(body, "snapshot/manifest.json") || strings.Count(body, `type="application/ld+json"`) != 2 || !strings.Contains(body, "metadata_only") || !strings.Contains(body, "ODbL") {
		t.Fatal("canonical/query, SEO, licence copy, JSON hooks or disabled snapshot changed")
	}
	data["snapshot_available"] = true
	raw, err = r.renderPublicNative(request, name, data)
	if err != nil || !strings.Contains(string(raw), "/open-data/snapshot/manifest.json") {
		t.Fatal("supplied available snapshot link lost", err)
	}
	data["structured_data"] = pongo2.AsSafeValue(`{"name":"</script><img src=x onerror=payload>","n":9007199254740993}`)
	p, err = r.publicNativePage(request, name, data)
	if err != nil || strings.Contains(string(p.Structured), "<img") || !strings.Contains(string(p.Structured), "9007199254740993") {
		t.Fatal("pre-marked producer JSON bypassed validating HTML-safe re-encoding")
	}
}

func TestNativePublicEscapingMalformedSnapshotsAndDispatchIsolation(t *testing.T) {
	r, request, data, name := publicNativeFixture(t, "event_detail", false)
	data["event"].(map[string]any)["title"] = `<script>title</script>`
	data["event"].(map[string]any)["description"] = `<img src=x onerror=payload>`
	data["event"].(map[string]any)["url"] = "javascript:payload"
	data["messages"] = []socialMessage{{"message": "<script>notice</script>", "tags": "error\" onclick=payload"}}
	data["language"] = "ro"
	raw, err := r.renderPublicNative(request, name, data)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if strings.Contains(body, "<script>title") || strings.Contains(body, "<script>notice") || strings.Contains(body, "<img src=x") || strings.Contains(body, `href="javascript:`) || !strings.Contains(body, `<html lang="ro"`) {
		t.Fatal("escaped public content, safe href or language changed")
	}
	// Server.page deliberately leaves csrf empty for SPA public _data=1
	// attempts. BuildSPA's unsupported public route falls back to these exact
	// selected HTML pages; the projection must preserve that valid caller state.
	// Home is the caller's explicit exception and already has a supplied token.
	for _, kind := range []string{"discover", "places", "event_detail", "open_data", "place_detail"} {
		var r *Renderer
		var request *http.Request
		var data pongo2.Context
		var name string
		if kind == "place_detail" {
			r, request, data = publicPlaceNativeFixture(t)
			name = "web/place_detail.html"
		} else {
			r, request, data, name = publicNativeFixture(t, kind, true)
		}
		request.URL.RawQuery = "_data=1"
		data["csrf"] = ""
		if raw, err := r.renderPublicNative(request, name, data); err != nil || len(raw) == 0 {
			t.Fatalf("valid signed public fallback csrf rejected: %s: %v", kind, err)
		}
		response := httptest.NewRecorder()
		if err := r.Render(response, request, name, data); err != nil || response.Body.Len() == 0 {
			t.Fatalf("ordinary signed public fallback dispatch rejected: %s: %v", kind, err)
		}
	}
	graphRenderer, graphRequest, graphData, graphName := publicNativeFixture(t, "communities_graph", true)
	graphData["csrf"] = ""
	if raw, err := graphRenderer.renderPublicNative(graphRequest, graphName, graphData); err == nil || len(raw) != 0 {
		t.Fatal("private graph lost its csrf check")
	}
	for key, mutate := range map[string]func(pongo2.Context){"list-type": func(d pongo2.Context) { d["activities"] = []any{false} }, "row-type": func(d pongo2.Context) { d["activities"].([]map[string]any)[0]["title"] = true }, "date-type": func(d pongo2.Context) { d["activities"].([]map[string]any)[0]["starts_at"] = "not-a-date" }, "json-invalid": func(d pongo2.Context) { d["structured_data"] = pongo2.AsSafeValue(`{"unfinished":`) }, "json-scalar": func(d pongo2.Context) { d["structured_data"] = pongo2.AsSafeValue(`"not-an-object"`) }, "json-raw-type": func(d pongo2.Context) { d["structured_data"] = `{"not":"producer-value"}` }, "path-mismatch": func(d pongo2.Context) { d["request"].(map[string]any)["path"] = "/foreign/" }, "unsafe-canonical": func(d pongo2.Context) { d["canonical_url"] = "javascript:payload" }, "unknown-language": func(d pongo2.Context) { d["language"] = "unknown" }} {
		t.Run(key, func(t *testing.T) {
			r, request, data, name := publicNativeFixture(t, "discover", false)
			mutate(data)
			if raw, err := r.renderPublicNative(request, name, data); err == nil || len(raw) != 0 {
				t.Fatal("malformed public snapshot produced bytes")
			}
		})
	}
	r, request, data, _ = publicNativeFixture(t, "landing", false)
	for _, unselected := range []string{"web/activities.html", "web/events.html", "web/places_list.html", "web/landing_detail.html", "web/saved_searches.html"} {
		if raw, err := r.renderPublicNative(request, unselected, data); !errors.Is(err, errPublicNativeSnapshot) || len(raw) != 0 {
			t.Fatal("existing SPA counterpart admitted by public7 renderer", unselected)
		}
	}
	r, _, data, name = publicNativeFixture(t, "communities_graph", true)
	if raw, err := r.renderPublicNative(httptest.NewRequest(http.MethodGet, "https://public.example/", nil), name, data); !errors.Is(err, platform.ErrForbidden) || len(raw) != 0 {
		t.Fatal("anonymous private graph directly admitted")
	}
}

func TestNativePublicCacheConcurrencyNoPartialOutputAndOrdinaryHeaders(t *testing.T) {
	parsed, err := loadPublicNativeTemplates()
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadPublicNativeTemplates()
	if err != nil || parsed != second {
		t.Fatal("public template cache changed")
	}
	broken, err := template.New("public-discovery").Parse(`{{define "public-discovery"}}partial{{.MissingField}}{{end}}`)
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := executePublicNativeTemplate(broken, publicNativePage{}); err == nil || len(raw) != 0 {
		t.Fatal("failed execution leaked partial bytes")
	}
	r, request, data, name := publicNativeFixture(t, "landing", false)
	expected, err := r.renderPublicNative(request, name, data)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw, err := r.renderPublicNative(request, name, data)
			if err != nil {
				failures <- err
			} else if !bytes.Equal(raw, expected) {
				failures <- errors.New("concurrent public body changed")
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	for _, kind := range []string{"landing", "communities_graph", "discover", "places", "event_detail", "open_data"} {
		r, request, data, name := publicNativeFixture(t, kind, kind == "communities_graph")
		response := httptest.NewRecorder()
		if err := r.Render(response, request, name, data); err != nil {
			t.Fatal(kind, err)
		}
		if response.Header().Get("Cache-Control") != "private, no-cache" || response.Header().Get("Vary") != "Cookie" || response.Header().Get("Content-Security-Policy-Report-Only") == "" || response.Header().Get("Set-Cookie") != "" || !strings.Contains(response.Body.String(), `class="public-discovery-native`) {
			t.Fatal("ordinary native dispatch or outer header policy changed", kind)
		}
	}
}
