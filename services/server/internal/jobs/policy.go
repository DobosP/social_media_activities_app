package jobs

import (
	"fmt"
	"time"
)

func (c Config) ValidatePolicy() error {
	if c.SavedSearchMatchBatch < 1 || c.SavedSearchMatchBatch > 10000 {
		return fmt.Errorf("SAVED_SEARCH_MATCH_BATCH is outside native bounds")
	}
	if c.DeferredBatch < 1 || c.DeferredBatch > 1000 {
		return fmt.Errorf("DEFERRED_TASKS_BATCH is outside native bounds")
	}
	if c.SavedSearchNotifyLimit < 1 || c.SavedSearchNotifyLimit > 10000 {
		return fmt.Errorf("SAVED_SEARCH_NOTIFY_RATE_LIMIT is outside native bounds")
	}
	if c.SavedSearchNotifyWindow < time.Second || c.SavedSearchNotifyWindow > 7*24*time.Hour {
		return fmt.Errorf("SAVED_SEARCH_NOTIFY_WINDOW_SECONDS is outside native bounds")
	}
	return nil
}
