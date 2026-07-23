package server

import (
	"context"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"
)

func TestSingleFlight(t *testing.T) {
	sf := newSingleFlight()
	jumpit := sf.tryAcquire("jumpit")
	if jumpit == nil {
		t.Fatal("first acquire = false, want true")
	}
	if sf.tryAcquire("jumpit") != nil {
		t.Error("second acquire while held = true, want false")
	}
	wanted := sf.tryAcquire("wanted")
	if wanted == nil {
		t.Error("acquire of a different source = false, want true")
	}
	jumpit.release()
	jumpit = sf.tryAcquire("jumpit")
	if jumpit == nil {
		t.Error("acquire after release = false, want true")
	}
	jumpit.release()
	wanted.release()
}

func TestSingleFlightAcquireRespectsCancellation(t *testing.T) {
	sf := newSingleFlight()
	held := sf.tryAcquire("jumpit")
	if held == nil {
		t.Fatal("initial acquire")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sf.acquire(ctx, "jumpit") != nil {
		t.Fatal("acquire succeeded after cancellation")
	}
	held.release()
	if sf.acquire(ctx, "jumpit") != nil {
		t.Fatal("free acquire succeeded after cancellation")
	}
	held = sf.acquire(context.Background(), "jumpit")
	if held == nil {
		t.Fatal("acquire after release")
	}
	held.release()
}

type flightOwner struct {
	id    int
	lease *flightLease
}

func TestSingleFlightHandsOwnershipToOneWaiterInOrder(t *testing.T) {
	sf := newSingleFlight()
	const source = "all"
	held := sf.tryAcquire(source)
	if held == nil {
		t.Fatal("initial acquire")
	}
	owners := make(chan flightOwner, 2)
	for i := 1; i <= 2; i++ {
		go func(owner int) {
			if lease := sf.acquire(context.Background(), source); lease != nil {
				owners <- flightOwner{id: owner, lease: lease}
			}
		}(i)
		waitForSingleFlightWaiters(t, sf, source, i)
	}

	held.release()
	first := <-owners
	if first.id != 1 {
		t.Fatalf("first owner=%d, want 1", first.id)
	}
	select {
	case got := <-owners:
		t.Fatalf("second owner=%d acquired without release", got.id)
	default:
	}
	first.lease.release()
	second := <-owners
	if second.id != 2 {
		t.Fatalf("second owner=%d, want 2", second.id)
	}
	second.lease.release()
}

func TestSingleFlightCancelledWaiterCannotStealHandoff(t *testing.T) {
	sf := newSingleFlight()
	const source = "all"
	held := sf.tryAcquire(source)
	if held == nil {
		t.Fatal("initial acquire")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancelled := make(chan *flightLease, 1)
	go func() { cancelled <- sf.acquire(ctx, source) }()
	waitForSingleFlightWaiters(t, sf, source, 1)
	owner := make(chan *flightLease, 1)
	go func() { owner <- sf.acquire(context.Background(), source) }()
	waitForSingleFlightWaiters(t, sf, source, 2)

	cancel()
	if <-cancelled != nil {
		t.Fatal("cancelled waiter acquired")
	}
	waitForSingleFlightWaiters(t, sf, source, 1)
	held.release()
	lease := <-owner
	if lease == nil {
		t.Fatal("remaining waiter did not acquire")
	}
	lease.release()
}

func TestSingleFlightTryAcquireCannotBargeAheadOfWaiter(t *testing.T) {
	sf := newSingleFlight()
	const source = "all"
	held := sf.tryAcquire(source)
	if held == nil {
		t.Fatal("initial acquire")
	}
	owner := make(chan *flightLease, 1)
	go func() { owner <- sf.acquire(context.Background(), source) }()
	waitForSingleFlightWaiters(t, sf, source, 1)

	held.release()
	for i := 0; i < 100; i++ {
		if sf.tryAcquire(source) != nil {
			t.Fatalf("tryAcquire barged on attempt %d", i+1)
		}
	}
	lease := <-owner
	if lease == nil {
		t.Fatal("queued waiter did not acquire")
	}
	lease.release()
}

func TestSingleFlightDuplicateReleaseCannotCreateOwners(t *testing.T) {
	sf := newSingleFlight()
	const source = "all"
	lease := sf.tryAcquire(source)
	if lease == nil {
		t.Fatal("initial acquire")
	}
	lease.release()
	lease.release()
	lease = sf.tryAcquire(source)
	if lease == nil {
		t.Fatal("acquire after release")
	}
	if sf.tryAcquire(source) != nil {
		t.Fatal("duplicate release created a second owner")
	}
	lease.release()

	(&flightLease{flight: sf, key: "missing", owner: 1}).release()
	missing := sf.tryAcquire("missing")
	if missing == nil || sf.tryAcquire("missing") != nil {
		t.Fatal("release without ownership changed token count")
	}
	missing.release()
}

func TestSingleFlightStaleOwnerCannotReleaseSuccessor(t *testing.T) {
	sf := newSingleFlight()
	const source = "all"
	first := sf.tryAcquire(source)
	if first == nil {
		t.Fatal("initial acquire")
	}
	owners := make(chan *flightLease, 2)
	for i := 1; i <= 2; i++ {
		go func() { owners <- sf.acquire(context.Background(), source) }()
		waitForSingleFlightWaiters(t, sf, source, i)
	}

	first.release()
	second := <-owners
	first.release()
	sf.mu.Lock()
	gate := sf.gates[source]
	gotOwner, gotWaiters := gate.owner, gate.waiters
	sf.mu.Unlock()
	if gotOwner != second.owner || gotWaiters != 1 {
		t.Fatalf("stale release changed owner/waiters to %d/%d, want %d/1", gotOwner, gotWaiters, second.owner)
	}
	second.release()
	third := <-owners
	third.release()
}

func waitForSingleFlightWaiters(t *testing.T, sf *singleFlight, source string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		sf.mu.Lock()
		got := 0
		if gate := sf.gates[source]; gate != nil {
			got = gate.waiters
		}
		sf.mu.Unlock()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiters=%d, want %d", got, want)
		}
		runtime.Gosched()
	}
}

func TestSSEWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	sw, err := newSSEWriter(rec)
	if err != nil {
		t.Fatalf("newSSEWriter: %v", err)
	}
	sw.event("status", "robots.txt 확인중")
	sw.event("count", "새 공고 5개")

	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
	want := "event: status\ndata: robots.txt 확인중\n\n" +
		"event: count\ndata: 새 공고 5개\n\n"
	if got := rec.Body.String(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestSSEWriterCollapsesNewlinesInData(t *testing.T) {
	rec := httptest.NewRecorder()
	sw, err := newSSEWriter(rec)
	if err != nil {
		t.Fatalf("newSSEWriter: %v", err)
	}
	sw.event("status", "line one\nline two")
	if got := rec.Body.String(); got != "event: status\ndata: line one line two\n\n" {
		t.Errorf("body = %q, want newlines collapsed to keep SSE framing intact", got)
	}
}
