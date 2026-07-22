package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestHiddenPageListsMutedPostings verifies /hidden lists every muted posting
// and nothing else.
func TestHiddenPageListsMutedPostings(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	ctx := context.Background()
	mutedID := mustUpsert(t, st, listingPosting("muted", "숨긴 공고 제목"))
	mustUpsert(t, st, listingPosting("visible", "안 숨긴 공고 제목"))
	scoreEach(t, st, map[int64]int{mutedID: 50})
	if err := st.SetNotInterested(ctx, mutedID, time.Now()); err != nil {
		t.Fatalf("SetNotInterested: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hidden", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "숨긴 공고 제목") {
		t.Error("/hidden does not list the muted posting")
	}
	if strings.Contains(body, "안 숨긴 공고 제목") {
		t.Error("/hidden lists a posting that was never muted")
	}
}

// TestHiddenPageRendersEyeOn verifies the mute toggle on /hidden renders in the
// on state, so clicking it un-hides.
func TestHiddenPageRendersEyeOn(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	ctx := context.Background()
	id := mustUpsert(t, st, listingPosting("muted", "숨긴 공고 제목"))
	scoreEach(t, st, map[int64]int{id: 50})
	if err := st.SetNotInterested(ctx, id, time.Now()); err != nil {
		t.Fatalf("SetNotInterested: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hidden", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `class="not-interested on"`) {
		t.Error("/hidden does not render the mute toggle in the on state")
	}
}

// TestHiddenPageMarksBookmarkedRows verifies a muted posting that is also
// bookmarked renders its bookmark toggle in the on state on /hidden.
func TestHiddenPageMarksBookmarkedRows(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	ctx := context.Background()
	id := mustUpsert(t, st, listingPosting("both", "숨기고 저장한 공고"))
	scoreEach(t, st, map[int64]int{id: 50})
	if err := st.SetNotInterested(ctx, id, time.Now()); err != nil {
		t.Fatalf("SetNotInterested: %v", err)
	}
	if err := st.SetBookmark(ctx, id, time.Now()); err != nil {
		t.Fatalf("SetBookmark: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hidden", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `class="bookmark on"`) {
		t.Error("/hidden does not mark the bookmarked row's bookmark toggle as on")
	}
}

// TestHiddenPageEmptyState verifies the quiet empty state shows when nothing is
// hidden.
func TestHiddenPageEmptyState(t *testing.T) {
	srv, _ := newTestServer(t, &fakeScraper{})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hidden", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "숨긴 공고가 없어요") {
		t.Error("/hidden empty-state copy missing")
	}
}

// TestHiddenPageHasNavLink verifies the 숨긴 공고 nav link is present and marked
// active on /hidden.
func TestHiddenPageHasNavLink(t *testing.T) {
	srv, _ := newTestServer(t, &fakeScraper{})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hidden", nil))
	if !strings.Contains(rec.Body.String(), `<a href="/hidden" class="active" aria-current="page">숨긴 공고</a>`) {
		t.Error("/hidden missing the active 숨긴 공고 nav link")
	}
}

// TestHiddenOrdersByMostRecentlyMuted locks the ordering contract buildHidden
// relies on (storage muted_at DESC): the most recently hidden posting is first.
func TestHiddenOrdersByMostRecentlyMuted(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	ctx := context.Background()
	base := time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC)
	first := mustUpsert(t, st, listingPosting("a", "먼저 숨긴 공고"))
	second := mustUpsert(t, st, listingPosting("b", "그다음 숨긴 공고"))
	third := mustUpsert(t, st, listingPosting("c", "마지막에 숨긴 공고"))
	scoreEach(t, st, map[int64]int{first: 50, second: 50, third: 50})
	// Mute in ascending time order; expect descending (latest first) in the view.
	if err := st.SetNotInterested(ctx, first, base); err != nil {
		t.Fatalf("mute first: %v", err)
	}
	if err := st.SetNotInterested(ctx, second, base.Add(time.Hour)); err != nil {
		t.Fatalf("mute second: %v", err)
	}
	if err := st.SetNotInterested(ctx, third, base.Add(2*time.Hour)); err != nil {
		t.Fatalf("mute third: %v", err)
	}

	view, err := srv.buildHidden(ctx, time.Now())
	if err != nil {
		t.Fatalf("buildHidden: %v", err)
	}
	got := postingTitles(view.Postings)
	want := []string{"마지막에 숨긴 공고", "그다음 숨긴 공고", "먼저 숨긴 공고"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("/hidden order = %v, want %v (most-recently-muted first)", got, want)
	}
}

// TestHiddenMarksDealbreakerExcluded verifies a muted posting that is a
// dealbreaker hit (Total < 0) is flagged Excluded, so /hidden renders it dimmed
// with "—" rather than a bogus score.
func TestHiddenMarksDealbreakerExcluded(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	ctx := context.Background()
	id := mustUpsert(t, st, listingPosting("db", "제외 점수 숨긴 공고"))
	if err := st.SetNotInterested(ctx, id, time.Now()); err != nil {
		t.Fatalf("SetNotInterested: %v", err)
	}
	scoreEach(t, st, map[int64]int{id: -1})

	view, err := srv.buildHidden(ctx, time.Now())
	if err != nil {
		t.Fatalf("buildHidden: %v", err)
	}
	if len(view.Postings) != 1 {
		t.Fatalf("want 1 posting, got %d", len(view.Postings))
	}
	if !view.Postings[0].Excluded {
		t.Error("dealbreaker-hit muted posting should be marked Excluded (dimmed with —) on /hidden")
	}
}
