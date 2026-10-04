package media

import "context"

// SetInlineVideoProcessing binds upload-triggered work to application lifetime,
// never the canceled HTTP request or short startup/migration context. It starts
// no work by itself; durable queued rows remain the timer's recovery contract.
func (s *Service) SetInlineVideoProcessing(lifetime context.Context, enabled bool) {
	if s == nil {
		return
	}
	s.inlineMu.Lock()
	defer s.inlineMu.Unlock()
	if s.inlineCancel != nil {
		s.inlineCancel()
	}
	s.inlineEnabled = enabled && lifetime != nil && lifetime.Err() == nil
	if s.inlineEnabled {
		s.inlineContext, s.inlineCancel = context.WithCancel(lifetime)
	}
}

// StopInlineVideoProcessing cancels and waits before storage/DB close. A single
// bounded batch is the only background media goroutine owned by the service.
func (s *Service) StopInlineVideoProcessing(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.inlineMu.Lock()
	s.inlineEnabled = false
	if s.inlineCancel != nil {
		s.inlineCancel()
	}
	done := s.inlineDone
	s.inlineMu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) kickVideoProcessing() {
	s.inlineMu.Lock()
	if !s.inlineEnabled || s.inlineContext == nil || s.inlineContext.Err() != nil || s.inlineDone != nil {
		s.inlineMu.Unlock()
		return
	}
	done := make(chan struct{})
	s.inlineDone = done
	ctx := s.inlineContext
	s.inlineMu.Unlock()
	go func() {
		defer func() { s.inlineMu.Lock(); s.inlineDone = nil; close(done); s.inlineMu.Unlock() }()
		_, _ = s.ProcessPendingVideos(ctx, 2)
	}()
}
