package server

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestSingleFlight(t *testing.T) {
	sf := newSingleFlight()
	if !sf.tryAcquire("jumpit") {
		t.Fatal("first acquire = false, want true")
	}
	if sf.tryAcquire("jumpit") {
		t.Error("second acquire while held = true, want false")
	}
	if !sf.tryAcquire("wanted") {
		t.Error("acquire of a different source = false, want true")
	}
	sf.release("jumpit")
	if !sf.tryAcquire("jumpit") {
		t.Error("acquire after release = false, want true")
	}
}

func TestSingleFlightAcquireRespectsCancellation(t *testing.T) {
	sf := newSingleFlight()
	if !sf.tryAcquire("jumpit") {
		t.Fatal("initial acquire")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sf.acquire(ctx, "jumpit") {
		t.Fatal("acquire succeeded after cancellation")
	}
	sf.release("jumpit")
	if sf.acquire(ctx, "jumpit") {
		t.Fatal("free acquire succeeded after cancellation")
	}
	if !sf.acquire(context.Background(), "jumpit") {
		t.Fatal("acquire after release")
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
