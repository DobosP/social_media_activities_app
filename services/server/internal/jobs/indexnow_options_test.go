package jobs

import (
	"context"
	"testing"
)

func TestIndexNowRejectsInvalidJobBoundsBeforeDatabaseOrHTTP(t *testing.T) {
	for _, values := range [][2]int{{-1, 1000}, {24*365 + 1, 1000}, {26, -1}, {26, 1001}} {
		config := DefaultConfig()
		config.IndexNowEnabled = true
		config.IndexNowKey = "synthetic-fixture-key"
		config.SiteBaseURL = "https://fixture.test"
		config.IndexNowWindowHours = values[0]
		config.IndexNowMaxURLs = values[1]
		if _, err := New(nil, config).IndexNow(context.Background()); err == nil {
			t.Fatal("invalid IndexNow bounds accepted")
		}
	}
	config := DefaultConfig()
	config.IndexNowWindowHours = -1
	config.IndexNowMaxURLs = -1
	result, err := New(nil, config).IndexNow(context.Background())
	if err != nil || result["disabled"] != true {
		t.Fatal("IndexNow explicit opt-in changed")
	}
}
