package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ohchanwu/jobcron/internal/profile"
	"github.com/ohchanwu/jobcron/internal/storage"
)

// scoreEach upserts a Total for each posting id, failing the test on error.
func scoreEach(t *testing.T, st *storage.Store, totals map[int64]int) {
	t.Helper()
	ctx := context.Background()
	_, profileHash, found, err := st.Profile(ctx)
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}
	if !found {
		profileJSON, marshalErr := profile.Marshal(profile.Profile{})
		if marshalErr != nil {
			t.Fatalf("profile.Marshal: %v", marshalErr)
		}
		profileHash, _, err = st.SaveProfile(ctx, profileJSON)
		if err != nil {
			t.Fatalf("SaveProfile: %v", err)
		}
	}
	for id, total := range totals {
		if err := st.UpsertScore(ctx, storage.Score{
			PostingID: id, ProfileHash: profileHash, Total: total,
			BreakdownJSON: "[]", ComputedAt: time.Now(),
		}); err != nil {
			t.Fatalf("UpsertScore id=%d: %v", id, err)
		}
	}
}

// contains reports whether want is among the dashboardPosting titles.
func contains(rows []dashboardPosting, want string) bool {
	for _, r := range rows {
		if r.Posting.Title == want {
			return true
		}
	}
	return false
}

func TestBookmarksPageUsesAllPostingsArchiveNav(t *testing.T) {
	srv, _ := newTestServer(t, &fakeScraper{})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/bookmarks", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<a href="/">전체 공고</a>`) {
		t.Error("/bookmarks nav missing 전체 공고 archive link")
	}
	if !strings.Contains(body, `<a href="/bookmarks" class="active" aria-current="page">북마크</a>`) {
		t.Error("/bookmarks should keep 북마크 as its active page label")
	}
}

// TestArchiveBookmarkExemptFromMinScore covers the bookmark override on the
// 전체 공고 page: a bookmarked posting scoring below MinScore stays in the main
// day-grouped list instead of sinking into the 관심 밖 collapsible, while an
// identical un-bookmarked posting is demoted, and a bookmarked dealbreaker hit
// (Total < 0) stays excluded — the dealbreaker rule wins over the bookmark.
func TestArchiveBookmarkExemptFromMinScore(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	ctx := context.Background()
	forty := 40
	profJSON, _ := profile.Marshal(profile.Profile{MinScore: &forty})
	if _, _, err := st.SaveProfile(ctx, profJSON); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}

	day := time.Date(2026, 5, 29, 6, 0, 0, 0, time.UTC)
	bmLow := listingPosting("bmlow", "북마크한 저점수 공고")
	bmLow.FirstSeenAt, bmLow.LastSeenAt = day, day
	plainLow := listingPosting("plainlow", "그냥 저점수 공고")
	plainLow.FirstSeenAt, plainLow.LastSeenAt = day, day
	bmDeal := listingPosting("bmdeal", "북마크한 제외 공고")
	bmDeal.FirstSeenAt, bmDeal.LastSeenAt = day, day
	bmLowID := mustUpsert(t, st, bmLow)
	plainLowID := mustUpsert(t, st, plainLow)
	bmDealID := mustUpsert(t, st, bmDeal)

	if err := st.SetBookmark(ctx, bmLowID, time.Now()); err != nil {
		t.Fatalf("SetBookmark bmLow: %v", err)
	}
	if err := st.SetBookmark(ctx, bmDealID, time.Now()); err != nil {
		t.Fatalf("SetBookmark bmDeal: %v", err)
	}
	scoreEach(t, st, map[int64]int{bmLowID: 20, plainLowID: 20, bmDealID: -1})

	view, err := srv.buildArchive(ctx, day.Add(time.Hour))
	if err != nil {
		t.Fatalf("buildArchive: %v", err)
	}

	var main []dashboardPosting
	for _, d := range view.Days {
		main = append(main, d.Postings...)
	}

	if !contains(main, "북마크한 저점수 공고") {
		t.Errorf("bookmarked below-MinScore posting was demoted; want it in the main list. main=%v excluded=%v",
			postingTitles(main), postingTitles(view.Excluded))
	}
	if contains(main, "그냥 저점수 공고") {
		t.Error("un-bookmarked below-MinScore posting leaked into the main list; want it Excluded")
	}
	if !contains(view.Excluded, "그냥 저점수 공고") {
		t.Error("un-bookmarked below-MinScore posting missing from Excluded")
	}
	if contains(main, "북마크한 제외 공고") {
		t.Error("bookmarked dealbreaker posting reached the main list; the dealbreaker rule must win over a bookmark")
	}
	if !contains(view.Excluded, "북마크한 제외 공고") {
		t.Error("bookmarked dealbreaker posting missing from Excluded; it must stay excluded")
	}
}

// TestBriefingBookmarkExemptFromMinScore is the briefing (/) counterpart: the
// same partition rule applies to today's briefing.
func TestBriefingBookmarkExemptFromMinScore(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	ctx := context.Background()
	forty := 40
	profJSON, _ := profile.Marshal(profile.Profile{MinScore: &forty})
	if _, _, err := st.SaveProfile(ctx, profJSON); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}

	now := time.Now().UTC()
	bmLow := listingPosting("bmlow", "북마크한 저점수 공고")
	bmLow.FirstSeenAt, bmLow.LastSeenAt = now, now
	plainLow := listingPosting("plainlow", "그냥 저점수 공고")
	plainLow.FirstSeenAt, plainLow.LastSeenAt = now, now
	bmDeal := listingPosting("bmdeal", "북마크한 제외 공고")
	bmDeal.FirstSeenAt, bmDeal.LastSeenAt = now, now
	bmLowID := mustUpsert(t, st, bmLow)
	plainLowID := mustUpsert(t, st, plainLow)
	bmDealID := mustUpsert(t, st, bmDeal)

	if err := st.SetBookmark(ctx, bmLowID, time.Now()); err != nil {
		t.Fatalf("SetBookmark bmLow: %v", err)
	}
	if err := st.SetBookmark(ctx, bmDealID, time.Now()); err != nil {
		t.Fatalf("SetBookmark bmDeal: %v", err)
	}
	scoreEach(t, st, map[int64]int{bmLowID: 20, plainLowID: 20, bmDealID: -1})

	b, err := srv.buildBriefing(ctx, now)
	if err != nil {
		t.Fatalf("buildBriefing: %v", err)
	}

	if !contains(b.Today, "북마크한 저점수 공고") {
		t.Errorf("bookmarked below-MinScore posting was demoted; want it in Today. today=%v excluded=%v",
			postingTitles(b.Today), postingTitles(b.Excluded))
	}
	if contains(b.Today, "그냥 저점수 공고") {
		t.Error("un-bookmarked below-MinScore posting leaked into Today; want it Excluded")
	}
	if !contains(b.Excluded, "그냥 저점수 공고") {
		t.Error("un-bookmarked below-MinScore posting missing from Excluded")
	}
	if contains(b.Today, "북마크한 제외 공고") {
		t.Error("bookmarked dealbreaker posting reached Today; the dealbreaker rule must win over a bookmark")
	}
	if !contains(b.Excluded, "북마크한 제외 공고") {
		t.Error("bookmarked dealbreaker posting missing from Excluded; it must stay excluded")
	}
}

// TestArchiveMutedBookmarkStaysHidden guards the precedence the task flagged:
// the bookmark override must not resurrect a muted posting on 전체 공고. A
// posting that is bookmarked AND muted AND below MinScore stays gone from
// /archive entirely (mute is filtered before the partition; it still shows on
// /bookmarks). The bookmark exemption only moves a row between the main list
// and the 관심 밖 collapsible — it never un-mutes.
func TestArchiveMutedBookmarkStaysHidden(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	ctx := context.Background()
	forty := 40
	profJSON, _ := profile.Marshal(profile.Profile{MinScore: &forty})
	if _, _, err := st.SaveProfile(ctx, profJSON); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}

	day := time.Date(2026, 5, 29, 6, 0, 0, 0, time.UTC)
	p := listingPosting("bmmuted", "북마크하고 숨긴 저점수 공고")
	p.FirstSeenAt, p.LastSeenAt = day, day
	id := mustUpsert(t, st, p)
	if err := st.SetBookmark(ctx, id, time.Now()); err != nil {
		t.Fatalf("SetBookmark: %v", err)
	}
	if err := st.SetNotInterested(ctx, id, time.Now()); err != nil {
		t.Fatalf("SetNotInterested: %v", err)
	}
	scoreEach(t, st, map[int64]int{id: 20})

	view, err := srv.buildArchive(ctx, day.Add(time.Hour))
	if err != nil {
		t.Fatalf("buildArchive: %v", err)
	}
	var all []dashboardPosting
	for _, d := range view.Days {
		all = append(all, d.Postings...)
	}
	all = append(all, view.Excluded...)
	if contains(all, "북마크하고 숨긴 저점수 공고") {
		t.Errorf("muted+bookmarked posting surfaced on /archive; mute must win. titles=%v", postingTitles(all))
	}
	if view.Total != 0 {
		t.Errorf("Total = %d, want 0 (the muted posting is filtered before the partition)", view.Total)
	}
}

// TestBookmarksShowBelowMinScore is the counterpart to the bookmark exemption:
// /bookmarks ignores MinScore entirely (it dims only dealbreaker hits), so a
// bookmarked posting scoring below MinScore stays fully visible there — the
// page is the deliberate keep-list, not a scored briefing.
func TestBookmarksShowBelowMinScore(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	ctx := context.Background()
	forty := 40
	profJSON, _ := profile.Marshal(profile.Profile{MinScore: &forty})
	if _, _, err := st.SaveProfile(ctx, profJSON); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	id := mustUpsert(t, st, listingPosting("bm", "저점수 북마크 공고"))
	if err := st.SetBookmark(ctx, id, time.Now()); err != nil {
		t.Fatalf("SetBookmark: %v", err)
	}
	scoreEach(t, st, map[int64]int{id: 10}) // below MinScore 40

	view, err := srv.buildBookmarks(ctx, time.Now())
	if err != nil {
		t.Fatalf("buildBookmarks: %v", err)
	}
	if len(view.Postings) != 1 || view.Postings[0].Posting.Title != "저점수 북마크 공고" {
		t.Errorf("/bookmarks should show a below-MinScore bookmark; got %v", postingTitles(view.Postings))
	}
	if view.Postings[0].Excluded {
		t.Error("a below-MinScore (non-dealbreaker) bookmark must not be dimmed on /bookmarks")
	}
}
