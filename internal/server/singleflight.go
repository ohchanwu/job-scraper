package server

import (
	"context"
	"sync"
)

// singleFlight serializes each bounded operation key across scrapes, rerates,
// profile saves, scheduled runs, and account deletion.
type singleFlight struct {
	mu    sync.Mutex
	gates map[string]*flightGate
}

type flightGate struct {
	token   chan struct{}
	waiters int
	owner   uint64
	next    uint64
}

type flightLease struct {
	flight *singleFlight
	key    string
	owner  uint64
}

func newSingleFlight() *singleFlight {
	return &singleFlight{gates: map[string]*flightGate{}}
}

// tryAcquire returns an ownership lease, or nil when key is already held.
func (s *singleFlight) tryAcquire(key string) *flightLease {
	s.mu.Lock()
	defer s.mu.Unlock()
	gate := s.gate(key)
	if gate.waiters > 0 {
		return nil
	}
	select {
	case <-gate.token:
		return s.claim(key, gate)
	default:
		return nil
	}
}

// acquire waits until key is available or ctx is cancelled.
func (s *singleFlight) acquire(ctx context.Context, key string) *flightLease {
	if ctx.Err() != nil {
		return nil
	}
	s.mu.Lock()
	gate := s.gate(key)
	if gate.waiters == 0 {
		select {
		case <-gate.token:
			lease := s.claim(key, gate)
			s.mu.Unlock()
			if ctx.Err() != nil {
				lease.release()
				return nil
			}
			return lease
		default:
		}
	}
	gate.waiters++
	s.mu.Unlock()

	acquired := false
	select {
	case <-gate.token:
		acquired = true
	case <-ctx.Done():
	}
	s.mu.Lock()
	gate.waiters--
	if acquired && ctx.Err() == nil {
		lease := s.claim(key, gate)
		s.mu.Unlock()
		return lease
	}
	s.mu.Unlock()
	if acquired {
		select {
		case gate.token <- struct{}{}:
		default:
		}
	}
	return nil
}

func (l *flightLease) release() {
	if l != nil {
		l.flight.release(l.key, l.owner)
	}
}

// release transfers key ownership only when the lease still owns it.
func (s *singleFlight) release(key string, owner uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	gate := s.gates[key]
	if gate == nil || gate.owner != owner {
		return
	}
	gate.owner = 0
	select {
	case gate.token <- struct{}{}:
	default:
	}
}

func (s *singleFlight) claim(key string, gate *flightGate) *flightLease {
	gate.next++
	if gate.next == 0 {
		gate.next++
	}
	gate.owner = gate.next
	return &flightLease{flight: s, key: key, owner: gate.owner}
}

func (s *singleFlight) gate(key string) *flightGate {
	gate := s.gates[key]
	if gate == nil {
		gate = &flightGate{token: make(chan struct{}, 1)}
		gate.token <- struct{}{}
		s.gates[key] = gate
	}
	return gate
}
