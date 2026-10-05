package media

import "time"

// VideoBatchTimeout is the longest a drain of limit video claims may need: each
// claim is bounded by its stale-lease window. ProcessPendingVideos takes no
// claim that the caller's remaining deadline cannot cover.
func (s *Service) VideoBatchTimeout(limit int) time.Duration {
	return time.Duration(max(limit, 1)) * s.policy.VideoStaleProcessing
}
