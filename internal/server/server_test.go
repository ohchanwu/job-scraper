package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ohchanwu/jobcron/internal/profile"
	"github.com/ohchanwu/jobcron/internal/scraper"
	"github.com/ohchanwu/jobcron/internal/storage"
	"github.com/ohchanwu/jobcron/web"
)

// fakeScraper is a test double for scraper.Scraper — it returns canned data
// instead of hitting the live 점핏 API.
type fakeScraper struct {
	source         string
	listing        []scraper.Posting
	details        map[string]scraper.Posting
	accessErr      error
	accessPanic    string
	listingPanic   string
	detailCalls    []string // SourcePostingIDs FetchDetail was called for
	listingStarted chan struct{}
	listingRelease <-chan struct{}
}

func (f *fakeScraper) Source() string {
	if f.source != "" {
		return f.source
	}
	return "jumpit"
}
func (f *fakeScraper) Kind() scraper.SourceKind { return scraper.SourceKindAggregator }
func (f *fakeScraper) CheckAccess(ctx context.Context) error {
	if f.accessPanic != "" {
		panic(f.accessPanic)
	}
	return f.accessErr
}

func (f *fakeScraper) FetchListing(ctx context.Context, limit int) ([]scraper.Posting, error) {
	if f.listingPanic != "" {
		panic(f.listingPanic)
	}
	if f.listingStarted != nil {
		close(f.listingStarted)
	}
	if f.listingRelease != nil {
		select {
		case <-f.listingRelease:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.listing, nil
}

func (f *fakeScraper) FetchDetail(ctx context.Context, p scraper.Posting) (scraper.Posting, error) {
	f.detailCalls = append(f.detailCalls, p.SourcePostingID)
	if d, ok := f.details[p.SourcePostingID]; ok {
		return d, nil
	}
	return p, nil
}

// noopEmit is an SSE emit callback that discards events.
func noopEmit(event, data string) {}

func listingPosting(sourceID, title string) scraper.Posting {
	return scraper.Posting{
		Source: "jumpit", SourcePostingID: sourceID, Title: title,
		Company: "테스트회사", URL: "https://example.test/" + sourceID,
	}
}

func newTestServer(t *testing.T, f *fakeScraper) (*Server, *storage.Store) {
	t.Helper()
	st, err := storage.OpenSQLiteAt(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatalf("OpenAt: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st, f), st
}

func TestPrimaryRoutesMatchNavigationMeanings(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	profJSON, _ := profile.Marshal(profile.Profile{CareerYears: 0})
	if _, _, err := st.SaveProfile(context.Background(), profJSON); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}

	for _, tc := range []struct {
		path  string
		wants []string
	}{
		{path: "/", wants: []string{"<title>전체 공고 — 오늘의 채용 브리핑</title>", `<link rel="canonical" href="/">`, "<h1>전체 공고</h1>"}},
		{path: "/briefing", wants: []string{"<title>오늘의 채용 브리핑</title>", `<link rel="canonical" href="/briefing">`, "<h1>채용 브리핑</h1>"}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d, want 200", tc.path, rec.Code)
			}
			for _, want := range tc.wants {
				if !strings.Contains(rec.Body.String(), want) {
					t.Errorf("GET %s missing %q", tc.path, want)
				}
			}
		})
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/archive", nil))
	if rec.Code < 300 || rec.Code >= 400 || rec.Header().Get("Location") != "/" {
		t.Errorf("GET /archive = status %d location %q, want redirect to /", rec.Code, rec.Header().Get("Location"))
	}
}

func TestBriefingWithoutProfileRedirectsWithReason(t *testing.T) {
	srv, _ := newTestServer(t, &fakeScraper{})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/briefing", nil))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/profile?reason=profile-required" {
		t.Errorf("GET /briefing = status %d location %q, want 303 profile-required redirect", rec.Code, rec.Header().Get("Location"))
	}
}

func TestProfileRequiredGuidanceOnlyAppearsForReason(t *testing.T) {
	srv, _ := newTestServer(t, &fakeScraper{})
	const guidance = "데일리 브리핑에서 새 공고를 스크랩하려면 먼저 프로필을 저장해 주세요."

	for _, tc := range []struct {
		path string
		want bool
	}{{
		path: "/profile?reason=profile-required", want: true,
	}, {
		path: "/profile", want: false,
	}} {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if got := strings.Contains(rec.Body.String(), guidance); got != tc.want {
				t.Errorf("GET %s guidance present = %v, want %v", tc.path, got, tc.want)
			}
			if !strings.Contains(rec.Body.String(), `<link rel="canonical" href="/profile">`) {
				t.Errorf("GET %s missing profile canonical link", tc.path)
			}
		})
	}
}

func TestBriefingStatus(t *testing.T) {
	t.Run("profile required", func(t *testing.T) {
		srv, _ := newTestServer(t, &fakeScraper{})
		got := requestBriefingStatus(t, srv)
		if !got.ProfileRequired || got.Latest != "" {
			t.Fatalf("status = %+v, want profile_required with no latest", got)
		}
	})

	t.Run("profile with no postings", func(t *testing.T) {
		srv, st := newTestServer(t, &fakeScraper{})
		saveTestProfile(t, st, profile.Profile{})
		got := requestBriefingStatus(t, srv)
		if got.ProfileRequired || got.Latest != "" {
			t.Fatalf("status = %+v, want empty ready status", got)
		}
	})

	t.Run("latest eligible posting", func(t *testing.T) {
		srv, st := newTestServer(t, &fakeScraper{})
		saveTestProfile(t, st, profile.Profile{})

		now := time.Now().UTC().Truncate(time.Second)
		older := listingPosting("older", "먼저 본 공고")
		older.FirstSeenAt, older.LastSeenAt = now.Add(-time.Hour), now.Add(-time.Hour)
		newer := listingPosting("newer", "나중에 본 공고")
		newer.FirstSeenAt, newer.LastSeenAt = now, now
		for _, posting := range []scraper.Posting{older, newer} {
			if _, _, err := st.UpsertPosting(context.Background(), posting); err != nil {
				t.Fatalf("UpsertPosting(%s): %v", posting.SourcePostingID, err)
			}
		}
		if _, err := srv.scoreAll(context.Background(), 0, nil); err != nil {
			t.Fatalf("scoreAll: %v", err)
		}

		got := requestBriefingStatus(t, srv)
		if got.ProfileRequired || got.Latest != now.Format(time.RFC3339) {
			t.Fatalf("status = %+v, want latest %q", got, now.Format(time.RFC3339))
		}
	})

	t.Run("ineligible postings do not create a notification", func(t *testing.T) {
		srv, st := newTestServer(t, &fakeScraper{})
		saveTestProfile(t, st, profile.Profile{DisabledSources: []string{"jumpit"}})
		now := time.Now().UTC().Truncate(time.Second)
		posting := listingPosting("disabled", "비활성 소스 공고")
		posting.FirstSeenAt, posting.LastSeenAt = now, now
		if _, _, err := st.UpsertPosting(context.Background(), posting); err != nil {
			t.Fatalf("UpsertPosting: %v", err)
		}
		if _, err := srv.scoreAll(context.Background(), 0, nil); err != nil {
			t.Fatalf("scoreAll: %v", err)
		}
		got := requestBriefingStatus(t, srv)
		if got.Latest != "" {
			t.Fatalf("disabled posting produced latest = %q", got.Latest)
		}
	})
}

type briefingStatusResponse struct {
	ProfileRequired bool   `json:"profile_required"`
	Latest          string `json:"latest"`
}

func requestBriefingStatus(t *testing.T, srv *Server) briefingStatusResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/briefing-status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/briefing-status status = %d, body = %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var status briefingStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode briefing status: %v", err)
	}
	return status
}

func saveTestProfile(t *testing.T, st *storage.Store, p profile.Profile) {
	t.Helper()
	profJSON, err := profile.Marshal(p)
	if err != nil {
		t.Fatalf("Marshal profile: %v", err)
	}
	if _, _, err := st.SaveProfile(context.Background(), profJSON); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
}

func TestProfileFormDefaultsDemodayOffOnlyBeforeFirstSave(t *testing.T) {
	st, err := storage.OpenSQLiteAt(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := New(st, &fakeScraper{}, &fakeScraper{source: "demoday"})

	render := func() string {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/profile", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /profile = %d", rec.Code)
		}
		return rec.Body.String()
	}

	newBody := render()
	if !strings.Contains(newBody, `name="source_jumpit" checked`) {
		t.Fatal("Jumpit should default on")
	}
	if strings.Contains(newBody, `name="source_demoday" checked`) {
		t.Fatal("Demoday should default off before the first profile save")
	}

	saveTestProfile(t, st, profile.Profile{})
	savedBody := render()
	if !strings.Contains(savedBody, `name="source_demoday" checked`) {
		t.Fatal("an explicitly saved all-enabled profile must keep Demoday enabled")
	}
}

func TestProfileFormRegisteredSourcesPreservesRegistrationOrder(t *testing.T) {
	st, err := storage.OpenSQLiteAt(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := New(st,
		&fakeScraper{source: "jumpit"},
		&fakeScraper{source: "demoday"},
	)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/profile", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /profile = %d", rec.Code)
	}
	body := rec.Body.String()
	jumpit := strings.Index(body, `name="source_jumpit"`)
	demoday := strings.Index(body, `name="source_demoday"`)
	if jumpit < 0 || demoday < 0 || jumpit >= demoday {
		t.Fatalf("registered source order: jumpit=%d demoday=%d", jumpit, demoday)
	}
}

func TestApplicationPagesUseSharedPrimaryNav(t *testing.T) {
	for _, name := range []string{"index.html", "archive.html", "bookmarks.html", "hidden.html", "profile.html"} {
		t.Run(name, func(t *testing.T) {
			body, err := web.FS.ReadFile(name)
			if err != nil {
				t.Fatalf("ReadFile(%s): %v", name, err)
			}
			if !strings.Contains(string(body), `{{template "primaryNav"`) {
				t.Fatalf("%s does not invoke the shared primaryNav template", name)
			}
		})
	}

	shared, err := web.FS.ReadFile("_nav.html")
	if err != nil {
		t.Fatalf("ReadFile(_nav.html): %v", err)
	}
	nav := string(shared)
	for _, want := range []string{
		`{{define "primaryNav"}}`,
		`href="/briefing"`,
		`class="briefing-dot"`,
		`aria-label="새 데일리 브리핑 있음"`,
		`eq .Active "briefing"}} aria-current="page"`,
		`eq .Active "archive"}} class="active" aria-current="page"`,
		`eq .Active "bookmarks"}} class="active" aria-current="page"`,
		`eq .Active "hidden"}} class="active" aria-current="page"`,
		`eq .Active "profile"}} class="active" aria-current="page"`,
		`eq .Active "account"}} class="active" aria-current="page"`,
	} {
		if !strings.Contains(nav, want) {
			t.Errorf("shared nav missing %q", want)
		}
	}
}

func TestRunScrapeStoresAndScoresPostings(t *testing.T) {
	f := &fakeScraper{
		listing: []scraper.Posting{listingPosting("1", "백엔드 신입"), listingPosting("2", "AI 신입")},
		details: map[string]scraper.Posting{
			"1": listingPosting("1", "백엔드 신입"),
			"2": listingPosting("2", "AI 신입"),
		},
	}
	srv, st := newTestServer(t, f)
	ctx := context.Background()

	profJSON, _ := profile.Marshal(profile.Profile{CareerYears: 0})
	if _, _, err := st.SaveProfile(ctx, profJSON); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}

	res, err := srv.runScrape(ctx, noopEmit, 0, nil)
	if err != nil {
		t.Fatalf("runScrape: %v", err)
	}
	if res.Listed != 2 || res.New != 2 || res.Scored != 2 {
		t.Errorf("ScrapeResult = %+v, want {Listed:2 New:2 Scored:2}", res)
	}

	postings, err := st.AllPostings(ctx)
	if err != nil || len(postings) != 2 {
		t.Fatalf("AllPostings: got %d (err=%v), want 2", len(postings), err)
	}
	scores, err := st.ScoresByPostingID(ctx)
	if err != nil || len(scores) != 2 {
		t.Fatalf("ScoresByPostingID: got %d (err=%v), want 2", len(scores), err)
	}
}

func TestRunScrapeWithHistoryRecordsScheduledSuccess(t *testing.T) {
	f := &fakeScraper{
		listing: []scraper.Posting{listingPosting("1", "백엔드 신입")},
		details: map[string]scraper.Posting{
			"1": listingPosting("1", "백엔드 신입"),
		},
	}
	srv, st := newTestServer(t, f)
	ctx := context.Background()
	profJSON, _ := profile.Marshal(profile.Profile{CareerYears: 0})
	if _, _, err := st.SaveProfile(ctx, profJSON); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}

	res, err := srv.runScrapeWithHistory(ctx, storage.ScrapeTriggerScheduled, noopEmit, 0, nil)
	if err != nil {
		t.Fatalf("runScrapeWithHistory: %v", err)
	}
	if res.Listed != 1 || res.New != 1 || res.Scored != 1 {
		t.Fatalf("ScrapeResult = %+v, want one listed/new/scored posting", res)
	}
	run, ok, err := st.LatestScrapeRun(ctx)
	if err != nil || !ok {
		t.Fatalf("LatestScrapeRun ok=%v err=%v", ok, err)
	}
	if run.Trigger != storage.ScrapeTriggerScheduled {
		t.Errorf("Trigger = %q, want scheduled", run.Trigger)
	}
	if run.Status != storage.ScrapeRunStatusSuccess {
		t.Errorf("Status = %q, want success", run.Status)
	}
	if run.Result != res {
		t.Errorf("stored result = %+v, want %+v", run.Result, res)
	}
	if run.ErrorSummary != "" {
		t.Errorf("ErrorSummary = %q, want empty", run.ErrorSummary)
	}
}

func TestRunScrapeWithHistoryRecordsPanicAsFailure(t *testing.T) {
	f := &fakeScraper{listingPanic: "listing exploded"}
	srv, st := newTestServer(t, f)
	ctx := context.Background()
	profJSON, _ := profile.Marshal(profile.Profile{CareerYears: 0})
	if _, _, err := st.SaveProfile(ctx, profJSON); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}

	if _, err := srv.runScrapeWithHistory(ctx, storage.ScrapeTriggerManual, noopEmit, 0, nil); err == nil {
		t.Fatal("runScrapeWithHistory succeeded, want panic converted to error")
	}
	run, ok, err := st.LatestScrapeRun(ctx)
	if err != nil || !ok {
		t.Fatalf("LatestScrapeRun ok=%v err=%v", ok, err)
	}
	if run.Trigger != storage.ScrapeTriggerManual {
		t.Errorf("Trigger = %q, want manual", run.Trigger)
	}
	if run.Status != storage.ScrapeRunStatusFailure {
		t.Errorf("Status = %q, want failure", run.Status)
	}
	if !strings.Contains(run.ErrorSummary, "listing exploded") {
		t.Errorf("ErrorSummary = %q, want panic text", run.ErrorSummary)
	}
	if run.FinishedAt == nil {
		t.Fatal("FinishedAt = nil, want failure finish time")
	}
}

func TestRunScrapeWithHistoryRecordsReturnedErrorAsFailure(t *testing.T) {
	f := &fakeScraper{listing: []scraper.Posting{listingPosting("1", "신입 공고")}}
	srv, st := newTestServer(t, f)
	ctx := context.Background()
	if _, _, err := st.SaveProfile(ctx, "{not-json"); err != nil {
		t.Fatalf("SaveProfile malformed JSON fixture: %v", err)
	}

	if _, err := srv.runScrapeWithHistory(ctx, storage.ScrapeTriggerManual, noopEmit, 0, nil); err == nil {
		t.Fatal("runScrapeWithHistory succeeded with malformed profile, want returned error")
	}
	run, ok, err := st.LatestScrapeRun(ctx)
	if err != nil || !ok {
		t.Fatalf("LatestScrapeRun ok=%v err=%v", ok, err)
	}
	if run.Trigger != storage.ScrapeTriggerManual {
		t.Errorf("Trigger = %q, want manual", run.Trigger)
	}
	if run.Status != storage.ScrapeRunStatusFailure {
		t.Errorf("Status = %q, want failure", run.Status)
	}
	if run.ErrorSummary == "" {
		t.Fatal("ErrorSummary = empty, want returned error summary")
	}
	if !strings.Contains(run.ErrorSummary, "profile") {
		t.Errorf("ErrorSummary = %q, want profile load error", run.ErrorSummary)
	}
	if run.FinishedAt == nil {
		t.Fatal("FinishedAt = nil, want failure finish time")
	}
}

func TestRunScrapeSkipsDetailForFreshKnownPostings(t *testing.T) {
	f := &fakeScraper{
		listing: []scraper.Posting{listingPosting("1", "기존 공고"), listingPosting("2", "새 공고")},
	}
	srv, st := newTestServer(t, f)
	ctx := context.Background()

	// Seed posting 1 with FRESH detail (fetched just now). The edited-JD refresh
	// (T7) only re-fetches a known posting whose detail is >detailRefreshMinAge
	// stale, so a fresh known posting is still skipped — only the new posting 2
	// gets a detail fetch.
	seen := listingPosting("1", "기존 공고")
	seen.FirstSeenAt = time.Now().UTC()
	seen.LastSeenAt = seen.FirstSeenAt
	if _, _, err := st.UpsertPosting(ctx, seen); err != nil {
		t.Fatalf("seed UpsertPosting: %v", err)
	}

	if _, err := srv.runScrape(ctx, noopEmit, 0, nil); err != nil {
		t.Fatalf("runScrape: %v", err)
	}
	if len(f.detailCalls) != 1 || f.detailCalls[0] != "2" {
		t.Errorf("FetchDetail called for %v, want only [2] — posting 1 is known and detail-fresh", f.detailCalls)
	}
}

// TestRunScrapeRefetchesStaleDetail is the T7 edited-JD path: a known posting
// whose detail is >detailRefreshMinAge stale IS re-fetched, and a changed JD is
// written through (so its content_hash, extraction, and score can refresh).
func TestRunScrapeRefetchesStaleDetail(t *testing.T) {
	edited := listingPosting("1", "기존 공고")
	edited.Description = "수정된 공고 본문 — 경력 3년 이상으로 변경되었습니다."
	f := &fakeScraper{
		listing: []scraper.Posting{listingPosting("1", "기존 공고")},
		details: map[string]scraper.Posting{"1": edited},
	}
	srv, st := newTestServer(t, f)
	ctx := context.Background()

	// Seed posting 1 with STALE detail (48h ago) and the ORIGINAL body.
	seen := listingPosting("1", "기존 공고")
	seen.Description = "원래 공고 본문."
	seen.FirstSeenAt = time.Now().Add(-48 * time.Hour).UTC()
	seen.LastSeenAt = seen.FirstSeenAt
	id, _, err := st.UpsertPosting(ctx, seen)
	if err != nil {
		t.Fatalf("seed UpsertPosting: %v", err)
	}

	if _, err := srv.runScrape(ctx, noopEmit, 0, nil); err != nil {
		t.Fatalf("runScrape: %v", err)
	}

	found := false
	for _, c := range f.detailCalls {
		if c == "1" {
			found = true
		}
	}
	if !found {
		t.Errorf("stale known posting 1 should have been re-fetched; detailCalls=%v", f.detailCalls)
	}
	got, ok, err := st.PostingByID(ctx, id)
	if err != nil || !ok {
		t.Fatalf("PostingByID: ok=%v err=%v", ok, err)
	}
	if got.Description != edited.Description {
		t.Errorf("description not refreshed:\n got %q\nwant %q", got.Description, edited.Description)
	}
}

func TestRunScrapeSweepsStalePostings(t *testing.T) {
	// One pre-existing posting last seen 10 days ago. The scrape returns
	// only a fresh listing, so the pre-existing one is not re-seen and
	// becomes stale relative to the new MAX(last_seen_at).
	f := &fakeScraper{listing: []scraper.Posting{listingPosting("fresh", "갓 올라온 공고")}}
	srv, st := newTestServer(t, f)
	ctx := context.Background()

	stale := listingPosting("stale", "오래 안 보이던 공고")
	stale.FirstSeenAt = time.Now().Add(-15 * 24 * time.Hour).UTC()
	stale.LastSeenAt = time.Now().Add(-10 * 24 * time.Hour).UTC()
	if _, _, err := st.UpsertPosting(ctx, stale); err != nil {
		t.Fatalf("seed stale posting: %v", err)
	}

	var sweepStatus string
	emit := func(event, data string) {
		if event == "status" && strings.Contains(data, "정리했어요") {
			sweepStatus = data
		}
	}
	res, err := srv.runScrape(ctx, emit, 0, nil)
	if err != nil {
		t.Fatalf("runScrape: %v", err)
	}
	if res.Removed != 1 {
		t.Errorf("ScrapeResult.Removed = %d, want 1 (the stale posting)", res.Removed)
	}
	if sweepStatus == "" {
		t.Error("scrape did not emit a sweep status message when removing postings")
	}

	postings, _ := st.AllPostings(ctx)
	if len(postings) != 1 || postings[0].SourcePostingID != "fresh" {
		t.Errorf("postings after sweep = %+v, want only [fresh]", postings)
	}
}

func TestRunScrapeIsolatesPerSourceFailures(t *testing.T) {
	// When the only source fails, runScrape now reports it via the result
	// (Failed=1) and emits a status, rather than returning an error. Real
	// callers wire multiple sources; a single failure should not abort
	// the whole briefing.
	f := &fakeScraper{accessErr: errors.New("robots.txt disallows")}
	srv, _ := newTestServer(t, f)
	res, err := srv.runScrape(context.Background(), noopEmit, 0, nil)
	if err != nil {
		t.Fatalf("runScrape returned unexpected error: %v", err)
	}
	if res.Failed != 1 {
		t.Errorf("res.Failed = %d, want 1 (the access-denied source)", res.Failed)
	}
	if res.New != 0 {
		t.Errorf("res.New = %d, want 0 (failed source produced nothing)", res.New)
	}
}

func TestHandleScrapeStreamsSSE(t *testing.T) {
	f := &fakeScraper{listing: []scraper.Posting{listingPosting("1", "신입 공고")}}
	srv, st := newTestServer(t, f)
	profJSON, _ := profile.Marshal(profile.Profile{})
	if _, _, err := st.SaveProfile(context.Background(), profJSON); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scrape", nil))

	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{"event: status", "event: count", "event: done"} {
		if !strings.Contains(body, want) {
			t.Errorf("SSE stream missing %q\n--- body ---\n%s", want, body)
		}
	}
}

func TestHandleScrapeRejectsConcurrentScrape(t *testing.T) {
	srv, _ := newTestServer(t, &fakeScraper{})
	lease := srv.flight.tryAcquire(scrapeAllKey) // a scrape is already in progress
	defer lease.release()

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scrape", nil))
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 when a scrape is already running", rec.Code)
	}
}

func TestHandleProfileSaveThenLoad(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})

	form := url.Values{}
	form.Set("career_years", "0")
	form.Set("salary_floor_man", "5000")
	form.Set("max_education", "3") // bachelor
	form.Set("stacks", "React,20\nGo,30")
	form.Set("cities", "서울, 판교")
	form.Set("location_weight", "15")
	form.Set("remote_ok", "on")
	form.Set("dealbreakers", "병역특례")
	form.Set("job_likes", "백엔드 API 설계")

	req := httptest.NewRequest(http.MethodPost, "/profile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save status = %d, want 303; body=%s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Location") != "/briefing" {
		t.Fatalf("save location = %q, want /briefing", rec.Header().Get("Location"))
	}

	jsonStr, _, ok, err := st.Profile(context.Background())
	if err != nil || !ok {
		t.Fatalf("Profile: ok=%v err=%v", ok, err)
	}
	p, err := profile.Unmarshal(jsonStr)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(p.Stacks) != 2 || p.Stacks[0].Name != "React" || p.Stacks[0].Weight != 20 {
		t.Errorf("stacks = %+v", p.Stacks)
	}
	if p.MaxEducation != profile.EducationBachelor {
		t.Errorf("max education = %v, want bachelor", p.MaxEducation)
	}
	if p.SalaryFloorKRW != 50_000_000 {
		t.Errorf("salary floor = %d, want 50000000", p.SalaryFloorKRW)
	}
	if len(p.Dealbreakers) != 1 || p.Dealbreakers[0] != "병역특례" {
		t.Errorf("dealbreakers = %v", p.Dealbreakers)
	}
	if p.JobLikes != "백엔드 API 설계" {
		t.Errorf("job_likes = %q, want the saved goal text", p.JobLikes)
	}

	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/profile", nil))
	body := rec2.Body.String()
	if rec2.Code != http.StatusOK || !strings.Contains(body, "병역특례") || !strings.Contains(body, "백엔드 API 설계") {
		t.Errorf("GET /profile: code=%d, body missing saved data", rec2.Code)
	}
	if strings.Contains(body, `name="must_have"`) {
		t.Error("GET /profile still renders the removed 필수 키워드 textarea")
	}
}

func TestFirstRunRedirectsToProfile(t *testing.T) {
	srv, _ := newTestServer(t, &fakeScraper{})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/briefing", nil))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/profile?reason=profile-required" {
		t.Errorf("first run: code=%d loc=%q, want 303 -> profile-required guidance",
			rec.Code, rec.Header().Get("Location"))
	}
}

func TestDeadlineBadge(t *testing.T) {
	kst := time.FixedZone("KST", 9*3600)
	now := time.Date(2026, 5, 22, 15, 0, 0, 0, kst)
	at := func(y int, m time.Month, d int) *time.Time {
		v := time.Date(y, m, d, 23, 59, 59, 0, kst)
		return &v
	}
	cases := []struct {
		name       string
		closedAt   *time.Time
		alwaysOpen bool
		wantText   string
		wantKind   string
	}{
		{"closes today", at(2026, 5, 22), false, "오늘 마감", "urgent"},
		{"closes tomorrow", at(2026, 5, 23), false, "마감 D-1", "urgent"},
		{"closes in 3 days", at(2026, 5, 25), false, "마감 D-3", "urgent"},
		{"closes in 4 days is calm", at(2026, 5, 26), false, "마감 D-4", "calm"},
		{"closes in 10 days is calm", at(2026, 6, 1), false, "마감 D-10", "calm"},
		{"closes in 30 days is calm", at(2026, 6, 21), false, "마감 D-30", "calm"},
		{"already past its deadline", at(2026, 5, 21), false, "마감", "urgent"},
		{"always open", nil, true, "상시채용", "open"},
		{"always open ignores a stray closed date", at(2026, 6, 1), true, "상시채용", "open"},
		{"no closing date on file", nil, false, "마감 정보 없음", "none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deadlineBadge(tc.closedAt, tc.alwaysOpen, now)
			if got.Text != tc.wantText || got.Kind != tc.wantKind {
				t.Errorf("deadlineBadge = {Text:%q Kind:%q}, want {Text:%q Kind:%q}",
					got.Text, got.Kind, tc.wantText, tc.wantKind)
			}
		})
	}
}

// TestDeadlineBadgeRendersTieredClasses verifies the template wiring end to
// end: each deadline state must reach the rendered HTML as the right label
// inside a deadline-<kind> class, so the CSS can tier the urgency. Covers the
// four non-past states on the briefing (past "마감" is exercised on /archive by
// TestArchiveRoutesExpiredToExcluded, since expired rows never reach /).
func TestDeadlineBadgeRendersTieredClasses(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	ctx := context.Background()
	zero := 0
	profJSON, _ := profile.Marshal(profile.Profile{MinScore: &zero})
	if _, _, err := st.SaveProfile(ctx, profJSON); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}

	now := time.Date(2026, 5, 22, 6, 0, 0, 0, time.UTC) // 15:00 KST May 22
	kst := time.FixedZone("KST", 9*3600)
	mk := func(id, title string, alwaysOpen, noClose bool, closeDay int) {
		p := listingPosting(id, title)
		p.FirstSeenAt, p.LastSeenAt = now, now
		p.AlwaysOpen = alwaysOpen
		if !alwaysOpen && !noClose {
			c := time.Date(2026, 5, closeDay, 23, 59, 59, 0, kst)
			p.ClosedAt = &c
		}
		rowID := mustUpsert(t, st, p)
		scoreEach(t, st, map[int64]int{rowID: 50}) // scored, as scoreAll always does post-scrape (Bug 2B skips unscored)
	}
	mk("open", "상시 공고", true, false, 0)     // 상시채용  → deadline-open
	mk("none", "정보없음 공고", false, true, 0)   // 마감 정보 없음 → deadline-none
	mk("urgent", "급한 공고", false, false, 24) // 마감 D-2  → deadline-urgent
	mk("calm", "여유 공고", false, false, 42)   // 마감 D-20 (May 42 = Jun 11) → deadline-calm

	b, err := srv.buildBriefing(ctx, now)
	if err != nil {
		t.Fatalf("buildBriefing: %v", err)
	}
	rec := httptest.NewRecorder()
	srv.render(rec, "index.html", b)
	body := rec.Body.String()

	for _, want := range []string{
		`class="deadline deadline-open">상시채용`,
		`class="deadline deadline-none">마감 정보 없음`,
		`class="deadline deadline-urgent">마감 D-2`,
		`class="deadline deadline-calm">마감 D-20`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("briefing HTML missing %q\n--- body ---\n%s", want, body)
		}
	}
}

func TestDashboardShowsOnlyTodaysPostings(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	ctx := context.Background()
	profJSON, _ := profile.Marshal(profile.Profile{})
	if _, _, err := st.SaveProfile(ctx, profJSON); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}

	today := listingPosting("today1", "오늘 본 공고")
	today.FirstSeenAt = time.Now().UTC()
	today.LastSeenAt = today.FirstSeenAt
	old := listingPosting("old1", "예전에 본 공고")
	old.FirstSeenAt = time.Now().Add(-72 * time.Hour).UTC()
	old.LastSeenAt = old.FirstSeenAt
	// Both postings are scored: in production scoreAll always runs after a
	// scrape, so a rendered posting always has a score row (Bug 2B skips the
	// unscored). This test exercises the date filter, not the scoring path.
	todayID := mustUpsert(t, st, today)
	oldID := mustUpsert(t, st, old)
	scoreEach(t, st, map[int64]int{todayID: 50, oldID: 50})

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/briefing", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "오늘 본 공고") {
		t.Error("dashboard is missing today's posting")
	}
	if strings.Contains(body, "예전에 본 공고") {
		t.Error("dashboard shows a posting first seen on a previous day")
	}
}

// TestDashboardHidesPostingsBelowMinScore covers the 2026-05-28 soft-hide
// knob: a posting whose Total falls below Profile.MinScore should land in
// the dealbreaker-excluded section, not the main "Today" list. Setting
// MinScore = 0 disables the hide; nil falls back to DefaultMinScore.
func TestDashboardHidesPostingsBelowMinScore(t *testing.T) {
	// Engineered so the posting scores exactly 25 (career exact, no
	// stacks/location/salary). A profile of {} hides it; MinScore=0
	// shows it.
	mk := func(t *testing.T, minScore *int) string {
		t.Helper()
		srv, st := newTestServer(t, &fakeScraper{})
		ctx := context.Background()
		prof := profile.Profile{MinScore: minScore}
		profJSON, _ := profile.Marshal(prof)
		if _, _, err := st.SaveProfile(ctx, profJSON); err != nil {
			t.Fatalf("SaveProfile: %v", err)
		}
		p := listingPosting("score25", "신입 백엔드 일반 공고")
		p.FirstSeenAt = time.Now().UTC()
		p.LastSeenAt = p.FirstSeenAt
		if _, _, err := st.UpsertPosting(ctx, p); err != nil {
			t.Fatalf("UpsertPosting: %v", err)
		}
		if _, err := srv.scoreAll(ctx, 0, nil); err != nil {
			t.Fatalf("scoreAll: %v", err)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/briefing", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		return rec.Body.String()
	}

	// Default (MinScore=nil → 40). The 25-point posting is hidden under
	// the "관심 밖" excluded section but appears in the body.
	bodyDefault := mk(t, nil)
	if !strings.Contains(bodyDefault, "신입 백엔드 일반 공고") {
		t.Error("default profile: posting absent from body entirely")
	}
	if !strings.Contains(bodyDefault, "관심 밖") {
		t.Error("default profile: 관심 밖 (excluded) section not rendered when a low-scoring posting exists")
	}

	// MinScore=0 explicitly shows everything. With no low-scoring rows
	// hidden, the 관심 밖 section should NOT render.
	zero := 0
	bodyZero := mk(t, &zero)
	if !strings.Contains(bodyZero, "신입 백엔드 일반 공고") {
		t.Error("MinScore=0: posting absent from body")
	}
	if strings.Contains(bodyZero, "관심 밖") {
		t.Error("MinScore=0: 관심 밖 section rendered despite no rows below threshold")
	}
}

func TestDashboardShowsScoredPostings(t *testing.T) {
	f := &fakeScraper{listing: []scraper.Posting{listingPosting("1", "백엔드 신입 개발자")}}
	srv, st := newTestServer(t, f)
	ctx := context.Background()
	profJSON, _ := profile.Marshal(profile.Profile{})
	if _, _, err := st.SaveProfile(ctx, profJSON); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	if _, err := srv.runScrape(ctx, noopEmit, 0, nil); err != nil {
		t.Fatalf("runScrape: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/briefing", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "백엔드 신입 개발자") {
		t.Errorf("dashboard body does not list the scraped posting")
	}
}
