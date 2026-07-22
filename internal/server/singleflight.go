package server

import (
	"context"
	"sync"
)

// singleFlight gates concurrent scrapes: at most one in flight per source.
type singleFlight struct {
	mu      sync.Mutex
	running map[string]chan struct{}
}

func newSingleFlight() *singleFlight {
	return &singleFlight{running: map[string]chan struct{}{}}
}

// tryAcquire marks source as scraping and returns true, or returns false when
// a scrape for that source is already in progress.
func (s *singleFlight) tryAcquire(source string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.running[source]; ok {
		return false
	}
	s.running[source] = make(chan struct{})
	return true
}

// acquire waits until source is available or ctx is cancelled.
func (s *singleFlight) acquire(ctx context.Context, source string) bool {
	for {
		if ctx.Err() != nil {
			return false
		}
		s.mu.Lock()
		done, running := s.running[source]
		if !running {
			if ctx.Err() != nil {
				s.mu.Unlock()
				return false
			}
			s.running[source] = make(chan struct{})
			s.mu.Unlock()
			return true
		}
		s.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			return false
		}
	}
}

// release marks source as no longer scraping.
func (s *singleFlight) release(source string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if done, ok := s.running[source]; ok {
		delete(s.running, source)
		close(done)
	}
}
