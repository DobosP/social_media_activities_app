package main

import (
	"math"
	"testing"
	"time"
)

func TestHaversineMeters(t *testing.T) {
	// Central Park <-> Sports Field fixture coordinates: ~1099.5m apart.
	lat1, lon1 := 46.7712, 23.6236
	lat2, lon2 := 46.7810881, 23.6236

	d := haversineMeters(lat1, lon1, lat2, lon2)
	if math.Abs(d-1099.5) > 5 {
		t.Errorf("haversineMeters = %.2f, want ~1099.5", d)
	}

	// Same point -> 0.
	if d0 := haversineMeters(lat1, lon1, lat1, lon1); d0 != 0 {
		t.Errorf("haversineMeters(same point) = %.4f, want 0", d0)
	}
}

func TestParseNear(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
		lat     float64
		lon     float64
	}{
		{"valid", "46.7712,23.6236", false, 46.7712, 23.6236},
		{"valid with spaces", "46.7712, 23.6236", false, 46.7712, 23.6236},
		{"missing comma", "46.7712", true, 0, 0},
		{"non numeric", "abc,def", true, 0, 0},
		{"out of range lat", "200,23.6236", true, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lat, lon, err := parseNear(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if lat != tc.lat || lon != tc.lon {
				t.Errorf("got (%v,%v), want (%v,%v)", lat, lon, tc.lat, tc.lon)
			}
		})
	}
}

func TestParseDateBound(t *testing.T) {
	t.Run("RFC3339", func(t *testing.T) {
		got, err := parseDateBound("2026-07-02T18:00:00Z", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := time.Date(2026, 7, 2, 18, 0, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("date only, from (no advance)", func(t *testing.T) {
		got, err := parseDateBound("2026-07-02", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("date only, to (exclusive +24h)", func(t *testing.T) {
		got, err := parseDateBound("2026-07-02", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		if _, err := parseDateBound("not-a-date", false); err == nil {
			t.Fatal("expected an error")
		}
	})
}

func TestParsePaging(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		p, err := parsePaging("", "", 50, 200)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Limit != 50 || p.Offset != 0 {
			t.Errorf("got %+v", p)
		}
	})

	t.Run("clamped to max", func(t *testing.T) {
		p, err := parsePaging("10000", "", 50, 200)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Limit != 200 {
			t.Errorf("Limit = %d, want 200 (clamped)", p.Limit)
		}
	})

	t.Run("negative limit is an error", func(t *testing.T) {
		if _, err := parsePaging("-1", "", 50, 200); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("negative offset is an error", func(t *testing.T) {
		if _, err := parsePaging("", "-1", 50, 200); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("non numeric is an error", func(t *testing.T) {
		if _, err := parsePaging("abc", "", 50, 200); err == nil {
			t.Fatal("expected an error")
		}
		if _, err := parsePaging("", "abc", 50, 200); err == nil {
			t.Fatal("expected an error")
		}
	})
}

func TestParseRadius(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		r, err := parseRadius("", 5000, 100000)
		if err != nil || r != 5000 {
			t.Errorf("got (%d, %v), want (5000, nil)", r, err)
		}
	})
	t.Run("clamped", func(t *testing.T) {
		r, err := parseRadius("500000", 5000, 100000)
		if err != nil || r != 100000 {
			t.Errorf("got (%d, %v), want (100000, nil)", r, err)
		}
	})
	t.Run("invalid", func(t *testing.T) {
		if _, err := parseRadius("abc", 5000, 100000); err == nil {
			t.Fatal("expected an error")
		}
		if _, err := parseRadius("-1", 5000, 100000); err == nil {
			t.Fatal("expected an error")
		}
	})
}
