package platform

import (
	"net/http"
	"time"
)

// ExtendDeadlines lifts the short server-wide read/write timeouts for one
// bounded transfer; a zero duration leaves that side unchanged. Writers must
// expose Unwrap() http.ResponseWriter, or the call degrades to the defaults.
func ExtendDeadlines(w http.ResponseWriter, read, write time.Duration) {
	controller, now := http.NewResponseController(w), time.Now()
	if read > 0 {
		_ = controller.SetReadDeadline(now.Add(read)) // http.ErrNotSupported keeps defaults.
	}
	if write > 0 {
		_ = controller.SetWriteDeadline(now.Add(write))
	}
}

// TransferWriteTimeout sizes a streamed response at a 64 KiB/s floor plus 30 s
// headroom, capped at 30 minutes so one download cannot hold a connection open.
func TransferWriteTimeout(size int64) time.Duration {
	seconds := min(max(size, 0)/(64<<10), 30*60)
	return min(30*time.Second+time.Duration(seconds)*time.Second, 30*time.Minute)
}
