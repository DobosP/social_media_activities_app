package media

import "time"

// VideoBatchTimeout is the longest a drain of limit video claims may need: each
// claim is bounded by its stale-lease window. A shorter caller deadline kills a
// valid transcode and spends one of its attempts.
func (s *Service) VideoBatchTimeout(limit int) time.Duration {
	return time.Duration(max(limit, 1)) * s.policy.VideoStaleProcessing
}
