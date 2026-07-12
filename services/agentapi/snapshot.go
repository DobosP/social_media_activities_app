package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// EventRecord is the typed view of a single event used for filtering and
// sorting. Raw holds the original bytes exactly as read from events.json so
// that unknown/extra fields flow through to API responses untouched.
type EventRecord struct {
	Raw       json.RawMessage
	ID        int64
	Title     string
	StartsAt  time.Time
	HasStarts bool
	Activity  string
	PlaceCity string
	PlaceLat  float64
	PlaceLon  float64
	HasCoords bool
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
	// It changes iff the manifest's generated_at or dataset counts change.
	version string
}

// manifestDoc mirrors the on-disk manifest.json shape closely enough to
// extract the fields the server needs; unknown fields are preserved via
// Raw for verbatim pass-through on /agent/v1/manifest.
type manifestDoc struct {
	SchemaVersion int                    `json:"schema_version"`
	GeneratedAt   string                 `json:"generated_at"`
	Site          string                 `json:"site"`
	Datasets      map[string]datasetInfo `json:"datasets"`
	Licenses      json.RawMessage        `json:"licenses"`
	Truncated     bool                   `json:"truncated"`
}

type datasetInfo struct {
	File  string `json:"file"`
	Count int    `json:"count"`
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
	StartsAt     string `json:"starts_at"`
	Activity     string `json:"activity"`
	ActivityType string `json:"activity_type"`
	PlaceSummary *struct {
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

	mu          sync.Mutex // guards the fields below; serializes reload attempts
	lastMTime   time.Time
	lastSize    int64
	everAttempt bool
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

// CheckReload stats manifest.json in the snapshot directory and, if its
// mtime or size changed since the last successful check, attempts to load a
// full new Snapshot. It returns whether a new Snapshot was swapped in, and
// any error encountered (which is also logged). On error the previous
// Snapshot, if any, continues to be served.
func (l *Loader) CheckReload() (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	manifestPath := filepath.Join(l.dir, "manifest.json")
	info, err := os.Stat(manifestPath)
	if err != nil {
		if !l.everAttempt {
			l.logger.Printf("snapshot: no manifest at %s yet: %v", manifestPath, err)
		}
		l.everAttempt = true
		return false, err
	}

	if l.everAttempt && info.ModTime().Equal(l.lastMTime) && info.Size() == l.lastSize {
		return false, nil
	}

	snap, err := l.load()
	if err != nil {
		l.logger.Printf("snapshot: reload failed, keeping previous snapshot: %v", err)
		l.everAttempt = true
		return false, err
	}

	l.lastMTime = info.ModTime()
	l.lastSize = info.Size()
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
	manifestPath := filepath.Join(l.dir, "manifest.json")
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m manifestDoc
	if err := json.Unmarshal(manifestRaw, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	snap := &Snapshot{
		ManifestRaw:  json.RawMessage(append([]byte(nil), manifestRaw...)),
		Site:         m.Site,
		Licenses:     m.Licenses,
		GeneratedAt:  m.GeneratedAt,
		LoadedAt:     time.Now().UTC(),
		RecordCounts: map[string]int{},
	}

	if events, ok := m.Datasets["events"]; ok {
		recs, err := l.loadEvents(events.File)
		if err != nil {
			return nil, fmt.Errorf("load events: %w", err)
		}
		snap.Events = recs
		snap.RecordCounts["events"] = len(recs)
	}
	if places, ok := m.Datasets["places"]; ok {
		recs, err := l.loadPlaces(places.File)
		if err != nil {
			return nil, fmt.Errorf("load places: %w", err)
		}
		snap.Places = recs
		snap.RecordCounts["places"] = len(recs)
	}
	if activities, ok := m.Datasets["activities"]; ok {
		recs, err := l.loadActivities(activities.File)
		if err != nil {
			return nil, fmt.Errorf("load activities: %w", err)
		}
		snap.Activities = recs
		snap.RecordCounts["activities"] = len(recs)
	}
	if taxonomy, ok := m.Datasets["taxonomy"]; ok && taxonomy.File != "" {
		raw, err := os.ReadFile(filepath.Join(l.dir, taxonomy.File))
		if err != nil {
			return nil, fmt.Errorf("load taxonomy: %w", err)
		}
		if !json.Valid(raw) {
			return nil, fmt.Errorf("load taxonomy: invalid JSON in %s", taxonomy.File)
		}
		snap.TaxonomyRaw = json.RawMessage(raw)
	}

	snap.version = computeVersion(snap.GeneratedAt, snap.RecordCounts)

	return snap, nil
}

func (l *Loader) readDataset(file string) (datasetFile, error) {
	var df datasetFile
	raw, err := os.ReadFile(filepath.Join(l.dir, file))
	if err != nil {
		return df, err
	}
	if err := json.Unmarshal(raw, &df); err != nil {
		return df, err
	}
	return df, nil
}

func (l *Loader) loadEvents(file string) ([]EventRecord, error) {
	df, err := l.readDataset(file)
	if err != nil {
		return nil, err
	}
	out := make([]EventRecord, 0, len(df.Records))
	for _, raw := range df.Records {
		var f eventFields
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, fmt.Errorf("parse event record: %w", err)
		}
		rec := EventRecord{
			Raw:      raw,
			ID:       f.ID,
			Title:    f.Title,
			Activity: f.Activity,
		}
		if rec.Activity == "" {
			rec.Activity = f.ActivityType
		}
		if t, err := parseRFC3339(f.StartsAt); err == nil {
			rec.StartsAt = t
			rec.HasStarts = true
		}
		if f.PlaceSummary != nil {
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

func (l *Loader) loadPlaces(file string) ([]PlaceRecord, error) {
	df, err := l.readDataset(file)
	if err != nil {
		return nil, err
	}
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

func (l *Loader) loadActivities(file string) ([]ActivityRecord, error) {
	df, err := l.readDataset(file)
	if err != nil {
		return nil, err
	}
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

// computeVersion derives a short, stable tag from the manifest's
// generated_at timestamp plus the actually-loaded record counts. It changes
// iff the snapshot content changes, which is exactly what ETags need.
func computeVersion(generatedAt string, counts map[string]int) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|events=%d|places=%d|activities=%d",
		generatedAt, counts["events"], counts["places"], counts["activities"])
	sum := h.Sum(nil)
	return fmt.Sprintf("%x", sum)[:16]
}
