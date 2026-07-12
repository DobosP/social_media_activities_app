package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"sort"
	"strconv"
	"time"
)

const apiVersion = "v1"

const (
	dataCacheControl   = "public, max-age=300"
	staticCacheControl = "public, max-age=3600"
	noStoreCache       = "no-store"
)

// landingTemplate contains a {{SITE}} placeholder that handleLanding fills
// in from the currently loaded snapshot's manifest "site" field (falling
// back to a generic relative link when no snapshot has loaded yet), so the
// doc's "human version of this data" link always points at the right host.
//
//go:embed landing.md
var landingTemplate []byte

//go:embed openapi.json
var openapiJSON []byte

var openapiETag = computeStaticETag(openapiJSON)

// App wires configuration and the snapshot loader to the HTTP handlers.
type App struct {
	cfg    Config
	loader *Loader
}

func NewApp(cfg Config, loader *Loader) *App {
	return &App{cfg: cfg, loader: loader}
}

// routes builds the full /agent/v1 handler tree (without the outer
// logging/CORS/rate-limit middleware; see main.go for the middleware chain).
func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /agent/v1/{$}", a.handleLanding)
	mux.HandleFunc("GET /agent/v1/openapi.json", a.handleOpenAPI)
	mux.HandleFunc("GET /agent/v1/manifest", a.handleManifest)
	mux.HandleFunc("GET /agent/v1/events", a.handleEventsList)
	mux.HandleFunc("GET /agent/v1/events/{id}", a.handleEventDetail)
	mux.HandleFunc("GET /agent/v1/places", a.handlePlacesList)
	mux.HandleFunc("GET /agent/v1/places/{id}", a.handlePlaceDetail)
	mux.HandleFunc("GET /agent/v1/activities", a.handleActivitiesList)
	mux.HandleFunc("GET /agent/v1/taxonomy", a.handleTaxonomy)
	mux.HandleFunc("GET /agent/v1/healthz", a.handleHealthz)
	mux.HandleFunc("/agent/v1/", a.handleNotFound)
	return mux
}

// --- envelopes --------------------------------------------------------

type listEnvelope struct {
	APIVersion  string            `json:"api_version"`
	GeneratedAt string            `json:"generated_at"`
	Count       int               `json:"count"`
	Total       int               `json:"total"`
	Limit       int               `json:"limit"`
	Offset      int               `json:"offset"`
	License     json.RawMessage   `json:"license"`
	Site        string            `json:"site"`
	Data        []json.RawMessage `json:"data"`
}

type detailEnvelope struct {
	APIVersion  string          `json:"api_version"`
	GeneratedAt string          `json:"generated_at"`
	License     json.RawMessage `json:"license"`
	Site        string          `json:"site"`
	Data        json.RawMessage `json:"data"`
}

// --- shared helpers -----------------------------------------------------

// currentOrUnavailable fetches the loaded snapshot, writing a 503 and
// returning false when none has ever loaded.
func (a *App) currentOrUnavailable(w http.ResponseWriter, r *http.Request) (*Snapshot, bool) {
	snap := a.loader.Current()
	if snap == nil {
		writeError(w, r, http.StatusServiceUnavailable, "snapshot_unavailable", "no snapshot has been loaded yet")
		return nil, false
	}
	return snap, true
}

// computeETag builds the strong ETag for a data endpoint response: it is a
// function of the loaded snapshot's version (derived from manifest
// generated_at + dataset counts) and the request path+query, so it changes
// exactly when the snapshot changes or the query changes.
func computeETag(version string, r *http.Request) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(r.URL.Path))
	_, _ = h.Write([]byte{'?'})
	_, _ = h.Write([]byte(r.URL.RawQuery))
	return fmt.Sprintf("%q", fmt.Sprintf("%s-%x", version, h.Sum32()))
}

func computeStaticETag(content []byte) string {
	sum := sha256Sum(content)
	return fmt.Sprintf("%q", sum[:16])
}

// writeAPIErr renders an *apiError (or any error, defensively) as a 400
// invalid_parameter response.
func writeAPIErr(w http.ResponseWriter, r *http.Request, err error) {
	if ae, ok := err.(*apiError); ok {
		writeError(w, r, http.StatusBadRequest, ae.code, ae.message)
		return
	}
	writeError(w, r, http.StatusBadRequest, "invalid_parameter", err.Error())
}

// pageSlice returns s[offset:offset+limit], clamped to s's bounds.
func pageSlice[T any](s []T, offset, limit int) []T {
	if offset >= len(s) {
		return []T{}
	}
	end := offset + limit
	if end > len(s) || end < offset {
		end = len(s)
	}
	return s[offset:end]
}

// --- static docs ----------------------------------------------------------

func (a *App) handleLanding(w http.ResponseWriter, r *http.Request) {
	site := ""
	if snap := a.loader.Current(); snap != nil {
		site = snap.Site
	}
	body := bytes.ReplaceAll(landingTemplate, []byte("{{SITE}}"), []byte(site))
	etag := computeStaticETag(body)
	if ifNoneMatchHit(r, etag) {
		write304(w, r, staticCacheControl, etag)
		return
	}
	writeBody(w, r, http.StatusOK, "text/markdown; charset=utf-8", staticCacheControl, etag, body)
}

func (a *App) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	if ifNoneMatchHit(r, openapiETag) {
		write304(w, r, staticCacheControl, openapiETag)
		return
	}
	writeBody(w, r, http.StatusOK, "application/json; charset=utf-8", staticCacheControl, openapiETag, openapiJSON)
}

func (a *App) handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusNotFound, "not_found", "no such endpoint")
}

// --- healthz --------------------------------------------------------------

func (a *App) handleHealthz(w http.ResponseWriter, r *http.Request) {
	snap := a.loader.Current()
	if snap == nil {
		writeJSON(w, r, http.StatusServiceUnavailable, noStoreCache, "", map[string]string{
			"status": "no_snapshot",
		})
		return
	}
	age := int64(time.Since(snap.LoadedAt).Seconds())
	writeJSON(w, r, http.StatusOK, noStoreCache, "", map[string]any{
		"status":                "ok",
		"snapshot_generated_at": snap.GeneratedAt,
		"snapshot_age_seconds":  age,
	})
}

// --- manifest ---------------------------------------------------------

func (a *App) handleManifest(w http.ResponseWriter, r *http.Request) {
	snap, ok := a.currentOrUnavailable(w, r)
	if !ok {
		return
	}
	etag := computeETag(snap.version, r)
	if ifNoneMatchHit(r, etag) {
		write304(w, r, dataCacheControl, etag)
		return
	}

	var m map[string]any
	if err := json.Unmarshal(snap.ManifestRaw, &m); err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "failed to read manifest")
		return
	}
	m["snapshot_loaded_at"] = snap.LoadedAt.Format(time.RFC3339)
	m["record_counts"] = snap.RecordCounts
	writeJSON(w, r, http.StatusOK, dataCacheControl, etag, m)
}

// --- taxonomy ---------------------------------------------------------

func (a *App) handleTaxonomy(w http.ResponseWriter, r *http.Request) {
	snap, ok := a.currentOrUnavailable(w, r)
	if !ok {
		return
	}
	if snap.TaxonomyRaw == nil {
		writeError(w, r, http.StatusNotFound, "not_found", "taxonomy dataset not present in snapshot")
		return
	}
	etag := computeETag(snap.version, r)
	if ifNoneMatchHit(r, etag) {
		write304(w, r, dataCacheControl, etag)
		return
	}
	writeRawJSON(w, r, http.StatusOK, dataCacheControl, etag, snap.TaxonomyRaw)
}

// --- events -------------------------------------------------------------

func (a *App) handleEventsList(w http.ResponseWriter, r *http.Request) {
	snap, ok := a.currentOrUnavailable(w, r)
	if !ok {
		return
	}
	etag := computeETag(snap.version, r)
	if ifNoneMatchHit(r, etag) {
		write304(w, r, dataCacheControl, etag)
		return
	}

	q := r.URL.Query()
	filter := eventFilter{
		Activity: q.Get("activity"),
		City:     q.Get("city"),
		Q:        q.Get("q"),
	}
	if v := q.Get("from"); v != "" {
		t, err := parseDateBound(v, false)
		if err != nil {
			writeAPIErr(w, r, err)
			return
		}
		filter.From = &t
	}
	if v := q.Get("to"); v != "" {
		t, err := parseDateBound(v, true)
		if err != nil {
			writeAPIErr(w, r, err)
			return
		}
		filter.To = &t
	}
	if v := q.Get("near"); v != "" {
		lat, lon, err := parseNear(v)
		if err != nil {
			writeAPIErr(w, r, err)
			return
		}
		radius, err := parseRadius(q.Get("radius_m"), 5000, 100000)
		if err != nil {
			writeAPIErr(w, r, err)
			return
		}
		filter.HasNear = true
		filter.Lat, filter.Lon = lat, lon
		filter.RadiusM = float64(radius)
	} else if v := q.Get("radius_m"); v != "" {
		if _, err := parseRadius(v, 5000, 100000); err != nil {
			writeAPIErr(w, r, err)
			return
		}
	}

	paging, err := parsePaging(q.Get("limit"), q.Get("offset"), 50, a.cfg.MaxLimit)
	if err != nil {
		writeAPIErr(w, r, err)
		return
	}

	matched := make([]*EventRecord, 0, len(snap.Events))
	for i := range snap.Events {
		if filter.matches(&snap.Events[i]) {
			matched = append(matched, &snap.Events[i])
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		if !matched[i].StartsAt.Equal(matched[j].StartsAt) {
			return matched[i].StartsAt.Before(matched[j].StartsAt)
		}
		return matched[i].ID < matched[j].ID
	})

	total := len(matched)
	page := pageSlice(matched, paging.Offset, paging.Limit)
	data := make([]json.RawMessage, len(page))
	for i, e := range page {
		data[i] = e.Raw
	}

	env := listEnvelope{
		APIVersion:  apiVersion,
		GeneratedAt: snap.GeneratedAt,
		Count:       len(data),
		Total:       total,
		Limit:       paging.Limit,
		Offset:      paging.Offset,
		License:     snap.Licenses,
		Site:        snap.Site,
		Data:        data,
	}
	writeJSON(w, r, http.StatusOK, dataCacheControl, etag, env)
}

func (a *App) handleEventDetail(w http.ResponseWriter, r *http.Request) {
	snap, ok := a.currentOrUnavailable(w, r)
	if !ok {
		return
	}
	etag := computeETag(snap.version, r)
	if ifNoneMatchHit(r, etag) {
		write304(w, r, dataCacheControl, etag)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "not_found", "event not found")
		return
	}
	for i := range snap.Events {
		if snap.Events[i].ID == id {
			env := detailEnvelope{
				APIVersion:  apiVersion,
				GeneratedAt: snap.GeneratedAt,
				License:     snap.Licenses,
				Site:        snap.Site,
				Data:        snap.Events[i].Raw,
			}
			writeJSON(w, r, http.StatusOK, dataCacheControl, etag, env)
			return
		}
	}
	writeError(w, r, http.StatusNotFound, "not_found", "event not found")
}

// --- places ---------------------------------------------------------------

func (a *App) handlePlacesList(w http.ResponseWriter, r *http.Request) {
	snap, ok := a.currentOrUnavailable(w, r)
	if !ok {
		return
	}
	etag := computeETag(snap.version, r)
	if ifNoneMatchHit(r, etag) {
		write304(w, r, dataCacheControl, etag)
		return
	}

	q := r.URL.Query()
	filter := placeFilter{
		Activity: q.Get("activity"),
		City:     q.Get("city"),
		Q:        q.Get("q"),
	}
	if v := q.Get("near"); v != "" {
		lat, lon, err := parseNear(v)
		if err != nil {
			writeAPIErr(w, r, err)
			return
		}
		radius, err := parseRadius(q.Get("radius_m"), 5000, 100000)
		if err != nil {
			writeAPIErr(w, r, err)
			return
		}
		filter.HasNear = true
		filter.Lat, filter.Lon = lat, lon
		filter.RadiusM = float64(radius)
	} else if v := q.Get("radius_m"); v != "" {
		if _, err := parseRadius(v, 5000, 100000); err != nil {
			writeAPIErr(w, r, err)
			return
		}
	}

	paging, err := parsePaging(q.Get("limit"), q.Get("offset"), 50, a.cfg.MaxLimit)
	if err != nil {
		writeAPIErr(w, r, err)
		return
	}

	matched := make([]*PlaceRecord, 0, len(snap.Places))
	for i := range snap.Places {
		if filter.matches(&snap.Places[i]) {
			matched = append(matched, &snap.Places[i])
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].Name != matched[j].Name {
			return matched[i].Name < matched[j].Name
		}
		return matched[i].ID < matched[j].ID
	})

	total := len(matched)
	page := pageSlice(matched, paging.Offset, paging.Limit)
	data := make([]json.RawMessage, len(page))
	for i, p := range page {
		data[i] = p.Raw
	}

	env := listEnvelope{
		APIVersion:  apiVersion,
		GeneratedAt: snap.GeneratedAt,
		Count:       len(data),
		Total:       total,
		Limit:       paging.Limit,
		Offset:      paging.Offset,
		License:     snap.Licenses,
		Site:        snap.Site,
		Data:        data,
	}
	writeJSON(w, r, http.StatusOK, dataCacheControl, etag, env)
}

func (a *App) handlePlaceDetail(w http.ResponseWriter, r *http.Request) {
	snap, ok := a.currentOrUnavailable(w, r)
	if !ok {
		return
	}
	etag := computeETag(snap.version, r)
	if ifNoneMatchHit(r, etag) {
		write304(w, r, dataCacheControl, etag)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "not_found", "place not found")
		return
	}
	for i := range snap.Places {
		if snap.Places[i].ID == id {
			env := detailEnvelope{
				APIVersion:  apiVersion,
				GeneratedAt: snap.GeneratedAt,
				License:     snap.Licenses,
				Site:        snap.Site,
				Data:        snap.Places[i].Raw,
			}
			writeJSON(w, r, http.StatusOK, dataCacheControl, etag, env)
			return
		}
	}
	writeError(w, r, http.StatusNotFound, "not_found", "place not found")
}

// --- activities -------------------------------------------------------

func (a *App) handleActivitiesList(w http.ResponseWriter, r *http.Request) {
	snap, ok := a.currentOrUnavailable(w, r)
	if !ok {
		return
	}
	etag := computeETag(snap.version, r)
	if ifNoneMatchHit(r, etag) {
		write304(w, r, dataCacheControl, etag)
		return
	}

	q := r.URL.Query()
	filter := activityFilter{Activity: q.Get("activity")}
	if v := q.Get("place"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeAPIErr(w, r, invalidParam("place must be an integer id"))
			return
		}
		filter.HasPlaceID = true
		filter.PlaceID = id
	}
	if v := q.Get("from"); v != "" {
		t, err := parseDateBound(v, false)
		if err != nil {
			writeAPIErr(w, r, err)
			return
		}
		filter.From = &t
	}
	if v := q.Get("to"); v != "" {
		t, err := parseDateBound(v, true)
		if err != nil {
			writeAPIErr(w, r, err)
			return
		}
		filter.To = &t
	}

	paging, err := parsePaging(q.Get("limit"), q.Get("offset"), 50, a.cfg.MaxLimit)
	if err != nil {
		writeAPIErr(w, r, err)
		return
	}

	matched := make([]*ActivityRecord, 0, len(snap.Activities))
	for i := range snap.Activities {
		if filter.matches(&snap.Activities[i]) {
			matched = append(matched, &snap.Activities[i])
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		if !matched[i].StartsAt.Equal(matched[j].StartsAt) {
			return matched[i].StartsAt.Before(matched[j].StartsAt)
		}
		return matched[i].ID < matched[j].ID
	})

	total := len(matched)
	page := pageSlice(matched, paging.Offset, paging.Limit)
	data := make([]json.RawMessage, len(page))
	for i, act := range page {
		data[i] = act.Raw
	}

	env := listEnvelope{
		APIVersion:  apiVersion,
		GeneratedAt: snap.GeneratedAt,
		Count:       len(data),
		Total:       total,
		Limit:       paging.Limit,
		Offset:      paging.Offset,
		License:     snap.Licenses,
		Site:        snap.Site,
		Data:        data,
	}
	writeJSON(w, r, http.StatusOK, dataCacheControl, etag, env)
}
