package web

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNotificationInboxDatePairsMatchFrozenHelper(t *testing.T) {
	zone, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		t.Fatal("reference Bucharest timezone unavailable")
	}
	calls := 0
	wrongName := false
	dates := notificationInboxDates{loadLocation: func(name string) (*time.Location, error) {
		wrongName = wrongName || name != "Europe/Bucharest"
		calls++
		return zone, nil
	}}
	for _, vector := range []struct {
		name  string
		stamp time.Time
	}{
		{"winter", time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC)},
		{"summer", time.Date(2026, time.July, 15, 10, 0, 0, 0, time.UTC)},
		{"spring-before", time.Date(2026, time.March, 29, 0, 59, 59, 999999999, time.UTC)},
		{"spring-after", time.Date(2026, time.March, 29, 1, 0, 0, 0, time.UTC)},
		{"autumn-before", time.Date(2026, time.October, 25, 0, 59, 59, 999999999, time.UTC)},
		{"autumn-after", time.Date(2026, time.October, 25, 1, 0, 0, 0, time.UTC)},
		{"day-year-rollover", time.Date(2026, time.December, 31, 23, 30, 0, 0, time.UTC)},
		{"single-day-subsecond", time.Date(2026, time.January, 1, 0, 3, 59, 123456789, time.UTC)},
		{"source-west-offset", time.Date(2026, time.July, 15, 10, 8, 59, 987654321, time.FixedZone("source-west", -5*60*60))},
		{"source-east-offset", time.Date(2026, time.January, 1, 0, 3, 0, 1, time.FixedZone("source-east", 5*60*60+45*60))},
		{"typed-extended-year", time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)},
	} {
		t.Run(vector.name, func(t *testing.T) {
			when, iso := dates.format(vector.stamp)
			if wrongName || when != djangoDate(vector.stamp, "j M H:i") || iso != djangoDate(vector.stamp, "c") {
				t.Fatal("page-local date pair differs from frozen source helper")
			}
		})
	}
	if calls != 1 {
		t.Fatal("one page resolved its timezone more than once")
	}
}

func TestNotificationInboxDatesZeroAndLoadFailurePreserveEachOffset(t *testing.T) {
	calls := 0
	dates := notificationInboxDates{loadLocation: func(name string) (*time.Location, error) {
		if name != "Europe/Bucharest" {
			t.Fatal("source timezone name changed")
		}
		calls++
		return nil, errors.New("synthetic timezone failure")
	}}
	// Constructing an empty page's formatter and rejecting zero instants cause no load.
	if calls != 0 || dates.attempted {
		t.Fatal("empty date formatter loaded timezone")
	}
	for _, zero := range []time.Time{{}, time.Time{}.In(time.FixedZone("zero-offset", 2*60*60))} {
		when, iso := dates.format(zero)
		if when != "" || iso != "" || calls != 0 || dates.attempted {
			t.Fatal("zero timestamp loaded timezone or produced a date")
		}
	}
	for _, vector := range []struct {
		stamp     time.Time
		when, iso string
	}{
		{time.Date(2026, time.July, 15, 10, 8, 59, 987654321, time.FixedZone("source-west", -5*60*60)), "15 Jul 10:08", "2026-07-15T10:08:59-05:00"},
		{time.Date(2026, time.January, 1, 0, 3, 0, 1, time.FixedZone("source-east", 5*60*60+45*60)), "1 Jan 00:03", "2026-01-01T00:03:00+05:45"},
		{time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC), "1 Jan 00:00", "2026-01-01T00:00:00Z"},
	} {
		when, iso := dates.format(vector.stamp)
		if when != vector.when || iso != vector.iso || calls != 1 {
			t.Fatal("load failure changed a row's own location/offset or retried in-page")
		}
	}
}

func TestNotificationInboxDatesNextPageRetriesAfterFailure(t *testing.T) {
	zone, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		t.Fatal("reference Bucharest timezone unavailable")
	}
	calls := 0
	loader := func(name string) (*time.Location, error) {
		if name != "Europe/Bucharest" {
			t.Fatal("source timezone name changed")
		}
		calls++
		if calls == 1 {
			return nil, errors.New("synthetic first-page timezone failure")
		}
		return zone, nil
	}
	stamp := time.Date(2026, time.July, 15, 10, 8, 59, 123456789, time.FixedZone("source-west", -5*60*60))
	first := notificationInboxDates{loadLocation: loader}
	for i := 0; i < 100; i++ {
		when, iso := first.format(stamp)
		if when != "15 Jul 10:08" || iso != "2026-07-15T10:08:59-05:00" {
			t.Fatal("first-page fallback changed")
		}
	}
	if calls != 1 {
		t.Fatal("failed location was retried within one page")
	}
	second := notificationInboxDates{loadLocation: loader}
	wantWhen, wantISO := djangoDate(stamp, "j M H:i"), djangoDate(stamp, "c")
	for i := 0; i < 100; i++ {
		when, iso := second.format(stamp)
		if when != wantWhen || iso != wantISO {
			t.Fatal("new page retained old failure or changed source date")
		}
	}
	if calls != 2 {
		t.Fatal("new page did not independently retry timezone resolution")
	}
}

func TestNotificationInboxDatesIndependentConcurrentPages(t *testing.T) {
	zone, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		t.Fatal("reference Bucharest timezone unavailable")
	}
	stamp := time.Date(2026, time.July, 15, 10, 0, 0, 0, time.UTC)
	wantWhen, wantISO := djangoDate(stamp, "j M H:i"), djangoDate(stamp, "c")
	var calls atomic.Int32
	var badName atomic.Bool
	var wait sync.WaitGroup
	failures := make(chan bool, 16)
	for i := 0; i < 16; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			dates := notificationInboxDates{loadLocation: func(name string) (*time.Location, error) {
				if name != "Europe/Bucharest" {
					badName.Store(true)
				}
				calls.Add(1)
				return zone, nil
			}}
			failed := false
			for j := 0; j < 100; j++ {
				when, iso := dates.format(stamp)
				failed = failed || when != wantWhen || iso != wantISO
			}
			failures <- failed
		}()
	}
	wait.Wait()
	close(failures)
	for failed := range failures {
		if failed {
			t.Fatal("independent page date output changed")
		}
	}
	if calls.Load() != 16 || badName.Load() {
		t.Fatal("date formatter shared state across pages")
	}
}
