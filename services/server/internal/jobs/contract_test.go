package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPythonROEDUPrivacyValidatorGolden(t *testing.T) {
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
		got := ValidatePackItem(row.Item)
		if got != row.Valid {
			t.Fatalf("privacy oracle%d got%v want%v kind%v", i, got, row.Valid, row.Item["kind"])
		}
	}
}
func TestPythonPlaceMappingAndDuplicateRatioGoldens(t *testing.T) {
	raw, err := os.ReadFile("testdata/place-mapping.json")
	if err != nil {
		t.Fatal(err)
	}
	var mappings []struct {
		Tags    map[string]any
		Matches []struct {
			Slug       string  `json:"slug"`
			RuleID     string  `json:"rule_id"`
			Confidence float64 `json:"confidence"`
		}
	}
	if json.Unmarshal(raw, &mappings) != nil {
		t.Fatal("invalid public mapping oracle")
	}
	for i, row := range mappings {
		wanted := []PlaceMatch{}
		for _, m := range row.Matches {
			wanted = append(wanted, PlaceMatch{m.Slug, m.RuleID, m.Confidence})
		}
		if got := MatchPlaceTags(row.Tags); !reflect.DeepEqual(got, wanted) {
			t.Fatalf("mapping oracle%d got%v want%v", i, got, wanted)
		}
	}
	raw, err = os.ReadFile("testdata/place-similarity.json")
	if err != nil {
		t.Fatal(err)
	}
	var ratios []struct {
		A, B  string
		Ratio float64
	}
	if json.Unmarshal(raw, &ratios) != nil {
		t.Fatal("invalid public name oracle")
	}
	for i, row := range ratios {
		if got := PlaceNameSimilarity(row.A, row.B); got != row.Ratio {
			t.Fatalf("duplicate oracle%d got%.17g want%.17g", i, got, row.Ratio)
		}
	}
}
func TestIngestionRejectsNonPublicAddressesAndControlMetadata(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "169.254.169.254", "100.64.0.1", "192.0.2.1", "198.18.0.1", "203.0.113.1", "240.0.0.1", "::1", "::ffff:169.254.169.254", "2001:db8::1", "3fff::1"} {
		if publicIP(net.ParseIP(address)) {
			t.Fatal("non-public ingestion address accepted", address)
		}
	}
	for _, address := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !publicIP(net.ParseIP(address)) {
			t.Fatal("public ingestion address rejected", address)
		}
	}
	if text("embedded\ttab", 100, false) || text("embedded\x01control", 100, false) {
		t.Fatal("control characters accepted in public contract")
	}
}
func TestCommonsLicensedMetadataAndNoExternalAssetHost(t *testing.T) {
	info := map[string]any{"thumburl": "https://upload.wikimedia.org/generated.png", "thumbmime": "image/png", "descriptionurl": "https://commons.wikimedia.org/wiki/File:Generated.png", "extmetadata": map[string]any{"Artist": map[string]any{"value": "<b>Generated artist</b>"}, "LicenseShortName": map[string]any{"value": "CC BY-SA 4.0"}}}
	var calls int
	client := &CommonsClient{HTTP: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		raw := []byte("generated-image-bytes")
		if r.URL.Hostname() == "commons.wikimedia.org" {
			raw, _ = json.Marshal(map[string]any{"query": map[string]any{"pages": map[string]any{"1": map[string]any{"imageinfo": []any{info}}}}})
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw))}, nil
	})}}
	image, err := client.Image(context.Background(), "File:Generated.png")
	if err != nil || image == nil || image.Attribution != "Generated artist, CC BY-SA 4.0, via Wikimedia Commons" || len(image.Bytes) == 0 || calls != 2 {
		t.Fatal("licensed Commons metadata lost", image, err, calls)
	}
	info["thumburl"] = "https://outside.example/malicious.png"
	image, err = client.Image(context.Background(), "File:Generated.png")
	if err != nil || image != nil || calls != 3 {
		t.Fatal("third-party asset host fetched", image, err, calls)
	}
	info["thumburl"] = "https://upload.wikimedia.org/generated.png"
	info["extmetadata"].(map[string]any)["LicenseShortName"] = map[string]any{"value": "CC BY-NC 4.0"}
	image, err = client.Image(context.Background(), "File:Generated.png")
	if err != nil || image != nil || calls != 4 {
		t.Fatal("uncleared license downloaded", image, err, calls)
	}
	for _, raw := range []string{"https://evil.example/?commons.wikimedia.org/wiki/File:Bad", "https://commons.wikimedia.org.evil.example/wiki/File:Bad"} {
		if CommonsFileTitle(map[string]any{"image": raw}) != "" {
			t.Fatal("substring host bypass accepted")
		}
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestROEDUReleasePagingAndRefusal(t *testing.T) {
	raw, err := os.ReadFile("testdata/roedu-page.json")
	if err != nil {
		t.Fatal(err)
	}
	client := &RoeduClient{BaseURL: "https://generated-roedu.example", APIKey: "generated-public-test-key", HTTP: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("X-API-Key") != "generated-public-test-key" {
			t.Fatal("transport credential header changed")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw)), Header: make(http.Header)}, nil
	})}}
	read, err := client.Read(context.Background(), "Cluj-Napoca")
	if err != nil || !read.Complete || len(read.Items) != 2 {
		t.Fatal("valid promoted read", read.Complete, len(read.Items), err)
	}
	var page map[string]any
	if json.Unmarshal(raw, &page) != nil {
		t.Fatal("fixture decode")
	}
	page["pagination"] = map[string]any{"next_cursor": "repeat"}
	repeated, _ := json.Marshal(page)
	client.HTTP.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(repeated))}, nil
	})
	if _, err = client.Read(context.Background(), "Cluj-Napoca"); err == nil {
		t.Fatal("repeated cursor accepted")
	}
}
func TestICSRecurringMonthReanchorAndBoundedExpansion(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	events, err := ParseICS("BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:generated\r\nSUMMARY:January meetup\r\nDTSTART:20260131T180000Z\r\nRRULE:FREQ=MONTHLY;COUNT=3\r\nEND:VEVENT\r\nEND:VCALENDAR", now)
	if err != nil || len(events) != 3 || events[1].Starts.Day() != 28 || events[2].Starts.Day() != 31 {
		t.Fatal("month recurrence drift", events, err)
	}
	events, err = ParseICS("BEGIN:VEVENT\nSUMMARY:folded\n title\nDTSTART:20260101T180000Z\nRRULE:FREQ=DAILY\nEND:VEVENT", now)
	if err != nil || len(events) > 120 || len(events) == 0 || events[0].Title != "foldedtitle" {
		t.Fatal("bounded calendar expansion", len(events), err)
	}
	if _, err = ParseICS(strings.Repeat("x", (5<<20)+1), now); err == nil {
		t.Fatal("oversized calendar accepted")
	}
}
func TestDueJobsHaveNativeHandlersAndDisabledSources(t *testing.T) {
	r := New(nil, DefaultConfig())
	if len(DueNames) != 27 || len(r.handlers) != 27 {
		t.Fatal("scheduled job mapping incomplete")
	}
	for _, name := range DueNames {
		if r.handlers[name] == nil {
			t.Fatal("missing native due job", name)
		}
	}
	for _, name := range []string{"sync_roedu", "indexnow_batch_submit", "export_agent_snapshot"} {
		_, err := r.Run(context.Background(), name, nil)
		if err != nil {
			t.Fatal("disabled opt-in source errored", name, err)
		}
	}
}
func TestDueJobTimeoutDoesNotStarveLaterDuties(t *testing.T) {
	config := DefaultConfig()
	config.JobTimeout = 5 * time.Millisecond
	r := New(nil, config)
	calls := 0
	for _, name := range DueNames {
		current := name
		r.handlers[name] = func(ctx context.Context, _ map[string]json.RawMessage) (any, error) {
			calls++
			if current == "sync_event_feeds" {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return 1, nil
		}
	}
	rows, err := r.RunDue(context.Background())
	if err == nil || len(rows) != 27 || calls != 27 || rows[18].Status != "failed" {
		t.Fatal("ingestion timeout starved later duties", len(rows), calls, err)
	}
}
