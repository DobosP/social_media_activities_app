package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoader_InitialLoad(t *testing.T) {
	loader, _ := newLoadedLoader(t)
	snap := loader.Current()
	if snap == nil {
		t.Fatal("expected snapshot")
	}
	if snap.GeneratedAt != "2026-07-12T10:00:00Z" {
		t.Errorf("GeneratedAt = %q", snap.GeneratedAt)
	}
	if len(snap.Events) != 4 {
		t.Errorf("len(Events) = %d, want 4", len(snap.Events))
	}
	if len(snap.Places) != 3 {
		t.Errorf("len(Places) = %d, want 3", len(snap.Places))
	}
	if len(snap.Activities) != 2 {
		t.Errorf("len(Activities) = %d, want 2", len(snap.Activities))
	}
	if snap.TaxonomyRaw == nil {
		t.Error("expected taxonomy to be loaded")
	}
	if snap.Site != "https://example.org" {
		t.Errorf("Site = %q", snap.Site)
	}
}

func TestLoader_EventActivityFallback(t *testing.T) {
	loader, _ := newLoadedLoader(t)
	snap := loader.Current()
	var byID = map[int64]EventRecord{}
	for _, e := range snap.Events {
		byID[e.ID] = e
	}
	// event 1 sets "activity" directly.
	if byID[1].Activity != "chess" {
		t.Errorf("event 1 activity = %q, want chess", byID[1].Activity)
	}
	// event 2 only sets "activity_type"; the loader must fall back to it.
	if byID[2].Activity != "football" {
		t.Errorf("event 2 activity (from activity_type fallback) = %q, want football", byID[2].Activity)
	}
	// event 3 has no place_summary at all.
	if byID[3].HasCoords {
		t.Error("event 3 should have no coordinates")
	}
}

func TestLoader_MissingDirectory(t *testing.T) {
	loader := NewLoader(filepath.Join(t.TempDir(), "does-not-exist"), testLogger())
	changed, err := loader.CheckReload()
	if err == nil {
		t.Fatal("expected an error for a missing snapshot directory")
	}
	if changed {
		t.Error("changed should be false on error")
	}
	if loader.Current() != nil {
		t.Error("Current() should be nil before any successful load")
	}
}

func TestLoader_ReloadOnChange(t *testing.T) {
	loader, dir := newLoadedLoader(t)
	initial := loader.Current()

	// No change: a second check should be a no-op.
	changed, err := loader.CheckReload()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected no change when manifest is untouched")
	}
	if loader.Current() != initial {
		t.Error("snapshot pointer should not change when nothing changed")
	}

	// Append a 5th event and rewrite the manifest with an updated
	// generated_at/site/count so both mtime and size change.
	writeUpdatedFixtures(t, dir)

	changed, err = loader.CheckReload()
	if err != nil {
		t.Fatalf("unexpected error on reload: %v", err)
	}
	if !changed {
		t.Fatal("expected a change to be detected")
	}
	updated := loader.Current()
	if updated == initial {
		t.Error("snapshot pointer should have been swapped")
	}
	if len(updated.Events) != 5 {
		t.Errorf("len(Events) after reload = %d, want 5", len(updated.Events))
	}
	if updated.GeneratedAt != "2026-08-01T00:00:00Z" {
		t.Errorf("GeneratedAt after reload = %q", updated.GeneratedAt)
	}
}

func TestLoader_FailStaticOnCorruption(t *testing.T) {
	loader, dir := newLoadedLoader(t)
	good := loader.Current()

	// Corrupt events.json, but still touch/rewrite manifest.json so the
	// mtime/size-based change check fires and a reload is attempted.
	if err := os.WriteFile(filepath.Join(dir, "events.json"), []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("corrupt events.json: %v", err)
	}
	writeManifestWithSite(t, dir, "https://example.org/this-is-a-longer-updated-site-url")

	changed, err := loader.CheckReload()
	if err == nil {
		t.Fatal("expected an error from the corrupted events.json")
	}
	if changed {
		t.Error("changed should be false when the reload fails")
	}
	if loader.Current() != good {
		t.Error("the previous good snapshot should still be served after a failed reload")
	}
	if len(loader.Current().Events) != 4 {
		t.Errorf("len(Events) = %d, want the old snapshot's 4", len(loader.Current().Events))
	}
}

func TestLoader_StartAutoReload(t *testing.T) {
	loader, dir := newLoadedLoader(t)
	writeUpdatedFixtures(t, dir)

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		loader.StartAutoReload(5*time.Millisecond, stop)
		close(done)
	}()

	deadline := time.After(2 * time.Second)
	for {
		if len(loader.Current().Events) == 5 {
			break
		}
		select {
		case <-deadline:
			close(stop)
			<-done
			t.Fatal("auto reload did not pick up the change in time")
		case <-time.After(10 * time.Millisecond):
		}
	}
	close(stop)
	<-done
}

// writeUpdatedFixtures rewrites events.json (adding a 5th record) and
// manifest.json (bumping generated_at, site and the events count) in dir.
func writeUpdatedFixtures(t *testing.T, dir string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "events.json"))
	if err != nil {
		t.Fatalf("read events.json: %v", err)
	}
	var df datasetFile
	if err := json.Unmarshal(raw, &df); err != nil {
		t.Fatalf("parse events.json: %v", err)
	}
	extra := json.RawMessage(`{
		"id": 5,
		"title": "Board game marathon",
		"starts_at": "2026-08-01T12:00:00Z",
		"activity": "boardgames",
		"place_summary": null
	}`)
	df.Records = append(df.Records, extra)
	df.Count = len(df.Records)
	newRaw, err := json.Marshal(df)
	if err != nil {
		t.Fatalf("marshal events.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.json"), newRaw, 0o644); err != nil {
		t.Fatalf("write events.json: %v", err)
	}

	writeManifestWithCounts(t, dir, "2026-08-01T00:00:00Z", "https://example.org/updated", 5)
}

func writeManifestWithSite(t *testing.T, dir, site string) {
	t.Helper()
	writeManifestWithCounts(t, dir, "2026-08-01T00:00:00Z", site, 4)
}

func writeManifestWithCounts(t *testing.T, dir, generatedAt, site string, eventsCount int) {
	t.Helper()
	m := map[string]any{
		"schema_version": 1,
		"generated_at":   generatedAt,
		"site":           site,
		"datasets": map[string]any{
			"events":     map[string]any{"file": "events.json", "count": eventsCount},
			"places":     map[string]any{"file": "places.json", "count": 3},
			"activities": map[string]any{"file": "activities.json", "count": 2},
			"taxonomy":   map[string]any{"file": "taxonomy.json", "count": 1},
		},
		"licenses":  []map[string]any{{"license_name": "CC-BY-4.0", "attribution": "Example Attribution"}},
		"truncated": false,
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o644); err != nil {
		t.Fatalf("write manifest.json: %v", err)
	}
}
