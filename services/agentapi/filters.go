package main

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// apiError is a typed error carrying the (code, message) pair used to build
// the JSON error envelope. Handlers translate it into a 400 response.
type apiError struct {
	code    string
	message string
}

func (e *apiError) Error() string { return e.message }

func invalidParam(format string, args ...any) *apiError {
	return &apiError{code: "invalid_parameter", message: fmt.Sprintf(format, args...)}
}

const earthRadiusM = 6371000.0

// haversineMeters returns the great-circle distance between two lat/lon
// points, in meters.
func haversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	phi1 := lat1 * math.Pi / 180
	phi2 := lat2 * math.Pi / 180
	dPhi := (lat2 - lat1) * math.Pi / 180
	dLambda := (lon2 - lon1) * math.Pi / 180

	a := math.Sin(dPhi/2)*math.Sin(dPhi/2) +
		math.Cos(phi1)*math.Cos(phi2)*math.Sin(dLambda/2)*math.Sin(dLambda/2)
	// Floating-point rounding at antipodes can put a just above one. A NaN
	// distance would incorrectly survive the radius comparison.
	a = min(1.0, max(0.0, a))
	c := 2 * math.Asin(math.Sqrt(a))
	return earthRadiusM * c
}

// parseNear parses a "lat,lon" query parameter.
func parseNear(s string) (lat, lon float64, err error) {
	parts := strings.SplitN(s, ",", 2)
	if len(parts) != 2 {
		return 0, 0, invalidParam("near must be in the form lat,lon")
	}
	lat, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	lon, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err1 != nil || err2 != nil {
		return 0, 0, invalidParam("near must be in the form lat,lon")
	}
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return 0, 0, invalidParam("near coordinates out of range")
	}
	if math.IsNaN(lat) || math.IsInf(lat, 0) || math.IsNaN(lon) || math.IsInf(lon, 0) {
		return 0, 0, invalidParam("near coordinates must be finite")
	}
	return lat, lon, nil
}

// parseNearQuery accepts the original compact coordinate pair and the
// main app's split coordinate spelling. Reject partial or competing centers;
// an ignored coordinate must never widen a requested geographic window.
func parseNearQuery(q url.Values) (hasNear bool, lat, lon float64, err error) {
	compact, hasCompact := q["near"]
	latValues, hasLat := q["near_lat"]
	lonValues, hasLon := q["near_lon"]
	if hasCompact {
		if hasLat || hasLon || len(compact) != 1 || compact[0] == "" {
			return false, 0, 0, invalidParam("use either near=lat,lon or one near_lat and near_lon pair")
		}
		lat, lon, err = parseNear(compact[0])
		return err == nil, lat, lon, err
	}
	if hasLat || hasLon {
		if !hasLat || !hasLon || len(latValues) != 1 || len(lonValues) != 1 || latValues[0] == "" || lonValues[0] == "" {
			return false, 0, 0, invalidParam("near_lat and near_lon must be supplied together once")
		}
		lat, lon, err = parseNear(latValues[0] + "," + lonValues[0])
		return err == nil, lat, lon, err
	}
	return false, 0, 0, nil
}

// parseDateBound parses an RFC3339 timestamp or a YYYY-MM-DD date. When
// endExclusive is true and only a date (no time) is given, the returned
// time is advanced by 24h so it can be used as an exclusive upper bound
// covering the whole given day.
func parseDateBound(s string, endExclusive bool) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		if endExclusive {
			return t.AddDate(0, 0, 1), nil
		}
		return t, nil
	}
	return time.Time{}, invalidParam("invalid date/time: %q (expected RFC3339 or YYYY-MM-DD)", s)
}

// paging holds validated limit/offset values.
type paging struct {
	Limit  int
	Offset int
}

// parsePaging parses and validates limit/offset query parameters, clamping
// limit to maxLimit and defaulting to defaultLimit/0.
func parsePaging(rawLimit, rawOffset string, defaultLimit, maxLimit int) (paging, error) {
	p := paging{Limit: defaultLimit, Offset: 0}
	if p.Limit > maxLimit {
		p.Limit = maxLimit
	}
	if rawLimit != "" {
		n, err := strconv.Atoi(rawLimit)
		if err != nil {
			return p, invalidParam("limit must be a non-negative integer")
		}
		if n < 0 {
			return p, invalidParam("limit must be a non-negative integer")
		}
		if n > maxLimit {
			n = maxLimit
		}
		p.Limit = n
	}
	if rawOffset != "" {
		n, err := strconv.Atoi(rawOffset)
		if err != nil {
			return p, invalidParam("offset must be a non-negative integer")
		}
		if n < 0 {
			return p, invalidParam("offset must be a non-negative integer")
		}
		p.Offset = n
	}
	return p, nil
}

// parseRadius parses radius_m, defaulting when empty and clamping to max.
func parseRadius(raw string, def, max int) (int, error) {
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, invalidParam("radius_m must be a non-negative integer")
	}
	if n > max {
		n = max
	}
	return n, nil
}

// eventFilter holds the validated filter parameters for /agent/v1/events.
type eventFilter struct {
	Activity   string
	HasPlaceID bool
	PlaceID    int64
	City       string
	From       *time.Time
	To         *time.Time
	HasNear    bool
	Lat, Lon   float64
	RadiusM    float64
	Q          string
}

func (f *eventFilter) matches(e *EventRecord) bool {
	if f.Activity != "" && e.Activity != f.Activity {
		return false
	}
	if f.HasPlaceID && (!e.HasPlaceID || e.PlaceID != f.PlaceID) {
		return false
	}
	if f.City != "" && !strings.EqualFold(e.PlaceCity, f.City) {
		return false
	}
	if f.From != nil && (!e.HasStarts || e.StartsAt.Before(*f.From)) {
		return false
	}
	if f.To != nil && (!e.HasStarts || !e.StartsAt.Before(*f.To)) {
		return false
	}
	if f.HasNear {
		if !e.HasCoords {
			return false
		}
		if haversineMeters(f.Lat, f.Lon, e.PlaceLat, e.PlaceLon) > f.RadiusM {
			return false
		}
	}
	if f.Q != "" {
		q := strings.ToLower(f.Q)
		if !strings.Contains(strings.ToLower(e.Title), q) &&
			!strings.Contains(strings.ToLower(e.Description), q) &&
			!strings.Contains(strings.ToLower(e.PlaceName), q) {
			return false
		}
	}
	return true
}

// placeFilter holds the validated filter parameters for /agent/v1/places.
type placeFilter struct {
	Activity string
	City     string
	HasNear  bool
	Lat, Lon float64
	RadiusM  float64
	Q        string
}

func (f *placeFilter) matches(p *PlaceRecord) bool {
	if f.Activity != "" {
		found := false
		for _, a := range p.ActivityTypes {
			if a == f.Activity {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if f.City != "" && !strings.EqualFold(p.City, f.City) {
		return false
	}
	if f.HasNear {
		if !p.HasCoords {
			return false
		}
		if haversineMeters(f.Lat, f.Lon, p.Lat, p.Lon) > f.RadiusM {
			return false
		}
	}
	if f.Q != "" && !strings.Contains(strings.ToLower(p.Name), strings.ToLower(f.Q)) {
		return false
	}
	return true
}

// activityFilter holds the validated filter parameters for
// /agent/v1/activities.
type activityFilter struct {
	Activity   string
	HasPlaceID bool
	PlaceID    int64
	From       *time.Time
	To         *time.Time
}

func (f *activityFilter) matches(a *ActivityRecord) bool {
	if f.Activity != "" && a.ActivityType != f.Activity {
		return false
	}
	if f.HasPlaceID && (!a.HasPlaceID || a.PlaceID != f.PlaceID) {
		return false
	}
	if f.From != nil && (!a.HasStarts || a.StartsAt.Before(*f.From)) {
		return false
	}
	if f.To != nil && (!a.HasStarts || !a.StartsAt.Before(*f.To)) {
		return false
	}
	return true
}
