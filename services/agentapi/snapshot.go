package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// EventRecord is the typed view of a single event used for filtering and
// sorting. Raw preserves reviewed fields after schema and field validation.
type EventRecord struct {
	Raw         json.RawMessage
	ID          int64
	Title       string
	Description string
	StartsAt    time.Time
	HasStarts   bool
	Activity    string
	PlaceID     int64
	HasPlaceID  bool
	PlaceName   string
	PlaceCity   string
	PlaceLat    float64
	PlaceLon    float64
	HasCoords   bool
}

// PlaceRecord is the typed view of a single place.
type PlaceRecord struct {
	Raw           json.RawMessage
	ID            int64
	Name          string
	City          string
	Lat           float64
	Lon           float64
	HasCoords     bool
	ActivityTypes []string
}

// ActivityRecord is the typed view of a single activity instance.
type ActivityRecord struct {
	Raw          json.RawMessage
	ID           int64
	Title        string
	StartsAt     time.Time
	HasStarts    bool
	Status       string
	ActivityType string
	PlaceID      int64
	HasPlaceID   bool
}

// Snapshot is an immutable, fully-loaded view of the agent data snapshot.
// A new Snapshot is built on every successful reload and swapped in
// atomically; readers never observe a partially-updated snapshot.
type Snapshot struct {
	ManifestRaw  json.RawMessage
	Site         string
	Licenses     json.RawMessage
	GeneratedAt  string
	LoadedAt     time.Time
	TaxonomyRaw  json.RawMessage
	Events       []EventRecord
	Places       []PlaceRecord
	Activities   []ActivityRecord
	RecordCounts map[string]int

	// version is a short, stable, content-derived tag used to build ETags.
	// It covers the manifest and exact dataset content digests.
	version string
}

// manifestDoc describes the reviewed, checksum-bound schema 2 publication.
// Unknown fields are rejected before any raw payload becomes public.
type manifestDoc struct {
	SchemaVersion int                    `json:"schema_version"`
	GeneratedAt   string                 `json:"generated_at"`
	Site          string                 `json:"site"`
	Datasets      map[string]datasetInfo `json:"datasets"`
	Licenses      json.RawMessage        `json:"licenses"`
	Truncated     bool                   `json:"truncated"`
}

type datasetInfo struct {
	File   string `json:"file"`
	Count  int    `json:"count"`
	SHA256 string `json:"sha256"`
}

// datasetFile mirrors the shared {schema_version, generated_at, count,
// records} envelope used by events.json / places.json / activities.json.
type datasetFile struct {
	SchemaVersion int               `json:"schema_version"`
	GeneratedAt   string            `json:"generated_at"`
	Count         int               `json:"count"`
	Records       []json.RawMessage `json:"records"`
}

type eventFields struct {
	ID           int64  `json:"id"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	StartsAt     string `json:"starts_at"`
	Activity     string `json:"activity"`
	ActivityType string `json:"activity_type"`
	PlaceID      *int64 `json:"place_id"`
	PlaceSummary *struct {
		Name string   `json:"name"`
		City string   `json:"city"`
		Lat  *float64 `json:"lat"`
		Lon  *float64 `json:"lon"`
	} `json:"place_summary"`
}

type placeFields struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	City          string   `json:"city"`
	Lat           *float64 `json:"lat"`
	Lon           *float64 `json:"lon"`
	ActivityTypes []string `json:"activity_types"`
}

type activityFields struct {
	ID           int64  `json:"id"`
	Title        string `json:"title"`
	StartsAt     string `json:"starts_at"`
	Status       string `json:"status"`
	ActivityType string `json:"activity_type"`
	PlaceID      *int64 `json:"place_id"`
}

// Loader owns the current Snapshot and knows how to (re)build it from a
// snapshot directory on disk. It fails static: any read/parse error during a
// reload attempt leaves the previously loaded Snapshot (if any) in place.
type Loader struct {
	dir     string
	logger  *log.Logger
	current atomic.Pointer[Snapshot]

	mu                 sync.Mutex // guards the fields below; serializes reload attempts
	lastManifestDigest string
	everAttempt        bool
}

// NewLoader creates a Loader for the given snapshot directory. Call
// CheckReload at least once before serving traffic.
func NewLoader(dir string, logger *log.Logger) *Loader {
	return &Loader{dir: dir, logger: logger}
}

// Current returns the currently loaded Snapshot, or nil if none has ever
// loaded successfully.
func (l *Loader) Current() *Snapshot {
	return l.current.Load()
}

// CheckReload hashes the bounded manifest and, if its content changed,
// attempts to load a
// full new Snapshot. It returns whether a new Snapshot was swapped in, and
// any error encountered (which is also logged). On error the previous
// Snapshot, if any, continues to be served.
func (l *Loader) CheckReload() (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	snap, err := l.load()
	if err != nil {
		if !l.everAttempt || l.current.Load() != nil {
			l.logger.Printf("snapshot: reload rejected; previous validated snapshot retained")
		}
		l.everAttempt = true
		return false, err
	}
	if snap.version == l.lastManifestDigest {
		return false, nil
	}
	l.lastManifestDigest = snap.version
	l.everAttempt = true
	l.current.Store(snap)
	l.logger.Printf("snapshot: loaded generated_at=%s events=%d places=%d activities=%d",
		snap.GeneratedAt, snap.RecordCounts["events"], snap.RecordCounts["places"], snap.RecordCounts["activities"])
	return true, nil
}

// StartAutoReload runs CheckReload every interval until stop is closed.
func (l *Loader) StartAutoReload(interval time.Duration, stop <-chan struct{}) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			_, _ = l.CheckReload()
		}
	}
}

func (l *Loader) load() (*Snapshot, error) {
	root, err := os.OpenRoot(l.dir)
	if err != nil {
		return nil, fmt.Errorf("snapshot directory unavailable")
	}
	defer root.Close()
	manifestRaw, err := readSnapshotFile(root, "manifest.json", 1<<20)
	if err != nil {
		return nil, err
	}
	var m manifestDoc
	if err := strictJSON(manifestRaw, &m); err != nil {
		return nil, err
	}
	if err := validateManifest(m); err != nil {
		return nil, err
	}
	if sha256Sum(manifestRaw) == l.lastManifestDigest {
		return l.current.Load(), nil
	}
	snap := &Snapshot{
		ManifestRaw: manifestRaw, Site: m.Site, Licenses: m.Licenses,
		GeneratedAt: m.GeneratedAt, LoadedAt: time.Now().UTC(), RecordCounts: map[string]int{},
	}
	for _, name := range []string{"events", "places", "activities", "taxonomy"} {
		info := m.Datasets[name]
		raw, err := readSnapshotFile(root, info.File, snapshotByteCaps[name])
		if err != nil {
			return nil, err
		}
		if sha256Sum(raw) != info.SHA256 {
			return nil, fmt.Errorf("snapshot checksum mismatch")
		}
		if name == "taxonomy" {
			if err := validateTaxonomy(raw, m.GeneratedAt, info.Count); err != nil {
				return nil, err
			}
			snap.TaxonomyRaw = raw
			continue
		}
		var df datasetFile
		if err := strictJSON(raw, &df); err != nil {
			return nil, err
		}
		if df.SchemaVersion != snapshotSchema || df.GeneratedAt != m.GeneratedAt || df.Count != info.Count || len(df.Records) != info.Count {
			return nil, fmt.Errorf("incoherent snapshot dataset")
		}
		if err := validateRecords(name, df.Records); err != nil {
			return nil, err
		}
		switch name {
		case "events":
			snap.Events, err = parseEvents(df)
		case "places":
			snap.Places, err = parsePlaces(df)
		case "activities":
			snap.Activities, err = parseActivities(df)
		}
		if err != nil {
			return nil, fmt.Errorf("invalid snapshot record")
		}
		snap.RecordCounts[name] = len(df.Records)
	}
	// A concurrent publisher cannot make a mixed release look coherent: all files
	// must match the committed manifest, which must still be identical here.
	finalManifest, err := readSnapshotFile(root, "manifest.json", 1<<20)
	if err != nil || sha256Sum(finalManifest) != sha256Sum(manifestRaw) {
		return nil, fmt.Errorf("snapshot changed during load")
	}
	snap.version = sha256Sum(manifestRaw)
	return snap, nil
}

func parseEvents(df datasetFile) ([]EventRecord, error) {
	out := make([]EventRecord, 0, len(df.Records))
	for _, raw := range df.Records {
		var f eventFields
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, fmt.Errorf("parse event record: %w", err)
		}
		rec := EventRecord{
			Raw:         raw,
			ID:          f.ID,
			Title:       f.Title,
			Description: f.Description,
			Activity:    f.Activity,
		}
		if rec.Activity == "" {
			rec.Activity = f.ActivityType
		}
		if f.PlaceID != nil {
			rec.PlaceID = *f.PlaceID
			rec.HasPlaceID = true
		}
		if t, err := parseRFC3339(f.StartsAt); err == nil {
			rec.StartsAt = t
			rec.HasStarts = true
		}
		if f.PlaceSummary != nil {
			rec.PlaceName = f.PlaceSummary.Name
			rec.PlaceCity = f.PlaceSummary.City
			if f.PlaceSummary.Lat != nil && f.PlaceSummary.Lon != nil {
				rec.PlaceLat = *f.PlaceSummary.Lat
				rec.PlaceLon = *f.PlaceSummary.Lon
				rec.HasCoords = true
			}
		}
		out = append(out, rec)
	}
	return out, nil
}

func parsePlaces(df datasetFile) ([]PlaceRecord, error) {
	out := make([]PlaceRecord, 0, len(df.Records))
	for _, raw := range df.Records {
		var f placeFields
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, fmt.Errorf("parse place record: %w", err)
		}
		rec := PlaceRecord{
			Raw:           raw,
			ID:            f.ID,
			Name:          f.Name,
			City:          f.City,
			ActivityTypes: f.ActivityTypes,
		}
		if f.Lat != nil && f.Lon != nil {
			rec.Lat = *f.Lat
			rec.Lon = *f.Lon
			rec.HasCoords = true
		}
		out = append(out, rec)
	}
	return out, nil
}

func parseActivities(df datasetFile) ([]ActivityRecord, error) {
	out := make([]ActivityRecord, 0, len(df.Records))
	for _, raw := range df.Records {
		var f activityFields
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, fmt.Errorf("parse activity record: %w", err)
		}
		rec := ActivityRecord{
			Raw:          raw,
			ID:           f.ID,
			Title:        f.Title,
			Status:       f.Status,
			ActivityType: f.ActivityType,
		}
		if t, err := parseRFC3339(f.StartsAt); err == nil {
			rec.StartsAt = t
			rec.HasStarts = true
		}
		if f.PlaceID != nil {
			rec.PlaceID = *f.PlaceID
			rec.HasPlaceID = true
		}
		out = append(out, rec)
	}
	return out, nil
}

func parseRFC3339(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("empty timestamp")
	}
	return time.Parse(time.RFC3339, s)
}

// sha256Sum returns the lowercase hex-encoded SHA-256 digest of content.
func sha256Sum(content []byte) string {
	sum := sha256.Sum256(content)
	return fmt.Sprintf("%x", sum)
}
