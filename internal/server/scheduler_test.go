package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ohchanwu/jobcron/internal/ai"
	"github.com/ohchanwu/jobcron/internal/credential"
	"github.com/ohchanwu/jobcron/internal/profile"
	"github.com/ohchanwu/jobcron/internal/scraper"
	"github.com/ohchanwu/jobcron/internal/storage"
)

func TestNextScheduledRunTodayWhenTimeStillAheadInKST(t *testing.T) {
	loc := kstLocation()
	now := time.Date(2026, 7, 10, 7, 0, 0, 0, loc)

	next, err := nextScheduledRun(now, "08:00")
	if err != nil {
		t.Fatalf("nextScheduledRun: %v", err)
	}

	want := time.Date(2026, 7, 10, 8, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("next = %s, want %s", next.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestNextScheduledRunTomorrowWhenTimeAlreadyPassedInKST(t *testing.T) {
	loc := kstLocation()
	now := time.Date(2026, 7, 10, 9, 0, 0, 0, loc)

	next, err := nextScheduledRun(now, "08:00")
	if err != nil {
		t.Fatalf("nextScheduledRun: %v", err)
	}

	want := time.Date(2026, 7, 11, 8, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("next = %s, want %s", next.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestNextScheduledRunInvalidTimeReturnsClearError(t *testing.T) {
	_, err := nextScheduledRun(time.Date(2026, 7, 10, 7, 0, 0, 0, time.UTC), "8am")
	if err == nil {
		t.Fatal("nextScheduledRun succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "daily scrape time") || !strings.Contains(err.Error(), "HH:MM") {
		t.Fatalf("error = %q, want clear HH:MM daily scrape time error", err.Error())
	}
}

func TestStartSchedulerRunsScheduledScrapeAfterSleep(t *testing.T) {
	f := &fakeScraper{listing: []scraper.Posting{listingPosting("1", "백엔드 신입")}}
	srv, st := newTestServer(t, f)
	ctx := context.Background()
	if _, err := st.CreateOwnerUser(ctx, "scheduler@example.invalid", "synthetic-hash"); err != nil {
		t.Fatalf("CreateOwnerUser: %v", err)
	}
	profJSON, _ := profile.Marshal(profile.Profile{CareerYears: 0})
	if _, _, err := st.SaveProfile(ctx, profJSON); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}

	schedulerCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sleepCalls := 0
	err := StartScheduler(schedulerCtx, SchedulerConfig{
		Server:          srv,
		DailyScrapeTime: "08:00",
		Now: func() time.Time {
			return time.Date(2026, 7, 10, 7, 0, 0, 0, kstLocation())
		},
		Sleep: func(ctx context.Context, d time.Duration) error {
			sleepCalls++
			if sleepCalls == 1 {
				if d != time.Hour {
					t.Fatalf("sleep duration = %s, want 1h", d)
				}
				return nil
			}
			cancel()
			return ctx.Err()
		},
	})
	if err != nil {
		t.Fatalf("StartScheduler: %v", err)
	}

	waitForScheduler(t, schedulerCtx.Done(), func() bool {
		run, ok, err := st.LatestScrapeRun(context.Background())
		return err == nil && ok && run.Trigger == storage.ScrapeTriggerScheduled && run.Status == storage.ScrapeRunStatusSuccess
	})
}

func TestRunScheduledScrapeUsesLegacySQLiteProfileWithoutAuthUser(t *testing.T) {
	f := &fakeScraper{listing: []scraper.Posting{listingPosting("sqlite-scheduled", "SQLite 예약 공고")}}
	srv, st := newTestServer(t, f)
	profJSON, _ := profile.Marshal(profile.Profile{CareerYears: 0})
	if _, _, err := st.SaveProfile(context.Background(), profJSON); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}

	srv.runScheduledScrape(context.Background())

	run, ok, err := st.LatestScrapeRun(context.Background())
	if err != nil || !ok || run.Status != storage.ScrapeRunStatusSuccess {
		t.Fatalf("SQLite scheduled run = %+v ok=%v err=%v, want success", run, ok, err)
	}
	postings, err := st.AllPostings(context.Background())
	if err != nil || len(postings) != 1 {
		t.Fatalf("SQLite scheduled postings = %d err=%v, want 1", len(postings), err)
	}
	scores, err := st.ScoresByPostingID(context.Background())
	if err != nil || len(scores) != 1 {
		t.Fatalf("SQLite scheduled scores = %d err=%v, want 1", len(scores), err)
	}
}

func TestStartSchedulerRecordsSkippedRunWhenScrapeLockBusy(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	if _, err := st.CreateOwnerUser(context.Background(), "scheduler-busy@example.invalid", "synthetic-hash"); err != nil {
		t.Fatalf("CreateOwnerUser: %v", err)
	}
	lease := srv.flight.tryAcquire(scrapeAllKey)
	if lease == nil {
		t.Fatal("failed to arrange busy scrape lock")
	}
	defer lease.release()

	schedulerCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sleepCalls := 0
	err := StartScheduler(schedulerCtx, SchedulerConfig{
		Server:          srv,
		DailyScrapeTime: "08:00",
		Now: func() time.Time {
			return time.Date(2026, 7, 10, 7, 0, 0, 0, kstLocation())
		},
		Sleep: func(ctx context.Context, d time.Duration) error {
			sleepCalls++
			if sleepCalls == 1 {
				return nil
			}
			cancel()
			return ctx.Err()
		},
	})
	if err != nil {
		t.Fatalf("StartScheduler: %v", err)
	}

	waitForScheduler(t, schedulerCtx.Done(), func() bool {
		run, ok, err := st.LatestScrapeRun(context.Background())
		return err == nil && ok &&
			run.Trigger == storage.ScrapeTriggerScheduled &&
			run.Status == storage.ScrapeRunStatusFailure &&
			strings.Contains(run.ErrorSummary, "skipped") &&
			strings.Contains(run.ErrorSummary, "scrape already running")
	})
}

func TestRecordSkippedScheduledRunFinishesAfterCallerContextCanceled(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	ctx, cancel := context.WithCancel(context.Background())

	srv.recordSkippedScheduledRunAfterStart(ctx, "skipped: scrape already running", cancel)

	run, ok, err := st.LatestScrapeRun(context.Background())
	if err != nil || !ok {
		t.Fatalf("LatestScrapeRun ok=%v err=%v", ok, err)
	}
	if run.Trigger != storage.ScrapeTriggerScheduled {
		t.Fatalf("Trigger = %q, want scheduled", run.Trigger)
	}
	if run.Status != storage.ScrapeRunStatusFailure {
		t.Fatalf("Status = %q, want failure", run.Status)
	}
	if run.FinishedAt == nil {
		t.Fatal("FinishedAt = nil, want skipped run finalized")
	}
	if run.ErrorSummary != "skipped: scrape already running" {
		t.Fatalf("ErrorSummary = %q, want skipped reason", run.ErrorSummary)
	}
}

func TestRunScheduledScrapeRefusesMissingOrAmbiguousOwner(t *testing.T) {
	tests := []struct {
		name       string
		seedOwners func(*testing.T, *storage.Store)
		wantReason string
	}{
		{
			name:       "missing owner",
			seedOwners: func(*testing.T, *storage.Store) {},
			wantReason: "skipped: scheduled owner is unavailable",
		},
		{
			name: "ambiguous owners",
			seedOwners: func(t *testing.T, st *storage.Store) {
				insertAIRuntimeTestUser(t, st, "scheduler-owner-a@example.invalid")
				insertAIRuntimeTestUser(t, st, "scheduler-owner-b@example.invalid")
			},
			wantReason: "skipped: scheduled owner is ambiguous",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, st := newPostgresTestServer(t, &fakeScraper{accessPanic: "scheduler must not call scraper"})
			tt.seedOwners(t, st)

			srv.runScheduledScrape(context.Background())

			assertSkippedScheduledRun(t, st, tt.wantReason)
		})
	}
}

func TestRunScheduledScrapeDegradesUnavailableAIRuntimeToRules(t *testing.T) {
	f := &fakeScraper{listing: []scraper.Posting{listingPosting("runtime-fallback", "예약 규칙 점수 공고")}}
	srv, st := newPostgresTestServer(t, f)
	userID := insertAIRuntimeTestUser(t, st, "scheduler-runtime@example.invalid")
	saveAIRuntimeProfile(t, st, userID, profile.Profile{AIProvider: "anthropic"})
	encryptingCipher := newAIRuntimeTestCipher(t, 0x61)
	saveAIRuntimeCredential(t, st, encryptingCipher, userID, "anthropic", "test-api-key")
	wrongCipher, err := credential.NewAESGCMCipher(make([]byte, credential.MasterKeyBytes))
	if err != nil {
		t.Fatalf("NewAESGCMCipher: %v", err)
	}
	srv.SetCredentialCipher(wrongCipher)

	srv.runScheduledScrape(context.Background())

	run, ok, err := st.LatestScrapeRun(context.Background())
	if err != nil || !ok || run.Status != storage.ScrapeRunStatusSuccess {
		t.Fatalf("scheduled fallback run = %+v ok=%v err=%v, want success", run, ok, err)
	}
	postings, err := st.AllPostings(context.Background())
	if err != nil || len(postings) != 1 {
		t.Fatalf("scheduled fallback postings = %d err=%v, want 1", len(postings), err)
	}
	scores, err := st.ScoresByPostingID(context.Background(), userID)
	if err != nil || len(scores) != 1 {
		t.Fatalf("scheduled fallback scores = %d err=%v, want 1", len(scores), err)
	}
}

func TestRunScheduledScrapeValidatesDealbreakersForSoleOptedInOwner(t *testing.T) {
	p := listingPosting("scheduled-dealbreaker", "신입 리서치 개발자")
	p.Description = "리서치 업무를 수행합니다"
	f := &fakeScraper{
		listing: []scraper.Posting{listingPosting("scheduled-dealbreaker", p.Title)},
		details: map[string]scraper.Posting{"scheduled-dealbreaker": p},
	}
	srv, st := newPostgresTestServer(t, f)
	userID := insertAIRuntimeTestUser(t, st, "scheduler-dealbreaker@example.invalid")
	cipher := newAIRuntimeTestCipher(t, 0x66)
	srv.SetCredentialCipher(cipher)
	saveAIRuntimeProfile(t, st, userID, profile.Profile{
		AIProvider:         "anthropic",
		AIModel:            "shared-model",
		ScheduledAIEnabled: true,
		Dealbreakers:       []string{"리서치"},
	})
	saveAIRuntimeCredential(t, st, cipher, userID, "anthropic", "scheduled-dealbreaker-key")
	provider := &ai.StubProvider{
		NameVal: "anthropic",
		ExtractFn: func(context.Context, string) (ai.Extraction, ai.Usage, error) {
			return ai.Extraction{Newcomer: true, EducationEnum: ai.EduNone}, ai.Usage{InputTokens: 1}, nil
		},
		ValidateDealbreakersFn: func(_ context.Context, _ string, candidates []ai.DealbreakerCandidate) ([]ai.DealbreakerValidation, ai.Usage, error) {
			return []ai.DealbreakerValidation{{CandidateID: candidates[0].ID, Verdict: ai.DealbreakerApplies, Evidence: "리서치 업무"}}, ai.Usage{InputTokens: 2}, nil
		},
	}
	srv.newAIProvider = func(string, string, string, time.Duration) (ai.Provider, error) { return provider, nil }

	srv.runScheduledScrape(context.Background())

	if provider.ValidateDealbreakersCalls != 1 {
		t.Fatalf("scheduled validation calls=%d, want 1", provider.ValidateDealbreakersCalls)
	}
	run, ok, err := st.LatestScrapeRun(context.Background())
	if err != nil || !ok || run.Status != storage.ScrapeRunStatusSuccess {
		t.Fatalf("scheduled run=%+v ok=%v err=%v", run, ok, err)
	}
}

func TestRunScheduledScrapeSkipsPaidAIForSoleOptedOutOwner(t *testing.T) {
	p := listingPosting("scheduled-dealbreaker-opt-out", "신입 리서치 개발자")
	p.Description = "리서치 업무를 수행합니다"
	f := &fakeScraper{
		listing: []scraper.Posting{listingPosting("scheduled-dealbreaker-opt-out", p.Title)},
		details: map[string]scraper.Posting{"scheduled-dealbreaker-opt-out": p},
	}
	srv, st := newPostgresTestServer(t, f)
	userID := insertAIRuntimeTestUser(t, st, "scheduler-dealbreaker-opt-out@example.invalid")
	cipher := newAIRuntimeTestCipher(t, 0x67)
	srv.SetCredentialCipher(cipher)
	saveAIRuntimeProfile(t, st, userID, profile.Profile{
		AIProvider:         "anthropic",
		AIModel:            "shared-model",
		ScheduledAIEnabled: false,
		Dealbreakers:       []string{"리서치"},
	})
	saveAIRuntimeCredential(t, st, cipher, userID, "anthropic", "scheduled-dealbreaker-opt-out-key")
	provider := &ai.StubProvider{NameVal: "anthropic"}
	srv.newAIProvider = func(string, string, string, time.Duration) (ai.Provider, error) { return provider, nil }

	srv.runScheduledScrape(context.Background())

	if provider.Calls != 0 || provider.ValidateDealbreakersCalls != 0 || provider.ScoreDeltaCalls != 0 {
		t.Fatalf("scheduled opt-out paid calls=(extract:%d validate:%d score:%d), want all zero",
			provider.Calls, provider.ValidateDealbreakersCalls, provider.ScoreDeltaCalls)
	}
	run, ok, err := st.LatestScrapeRun(context.Background())
	if err != nil || !ok || run.Status != storage.ScrapeRunStatusSuccess {
		t.Fatalf("scheduled opt-out run=%+v ok=%v err=%v", run, ok, err)
	}
	scores, err := st.ScoresByPostingID(context.Background(), userID)
	var total int
	for _, score := range scores {
		total = score.Total
	}
	if err != nil || len(scores) != 1 || total != -1 {
		t.Fatalf("scheduled opt-out conservative scores=%+v err=%v, want one excluded row", scores, err)
	}
}

type firstOpenBlockingCipher struct {
	inner   credential.Cipher
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *firstOpenBlockingCipher) Seal(userID int64, provider, plaintext string) ([]byte, []byte, int16, error) {
	return c.inner.Seal(userID, provider, plaintext)
}

func (c *firstOpenBlockingCipher) Open(userID int64, provider string, ciphertext, nonce []byte, version int16) (string, error) {
	c.once.Do(func() {
		close(c.entered)
		<-c.release
	})
	return c.inner.Open(userID, provider, ciphertext, nonce, version)
}

func TestRunScheduledScrapeHoldsFlightLockDuringRuntimeResolution(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	srv.SetProductionMode(true)
	userID, cookie := createSessionUser(t, st, "scheduler-lock@example.invalid", "scheduler-lock-session")
	baseCipher := newAIRuntimeTestCipher(t, 0x75)
	saveAIRuntimeProfile(t, st, userID, profile.Profile{CareerYears: 0, JobLikes: "old scheduler goal", AIProvider: "anthropic"})
	saveAIRuntimeCredential(t, st, baseCipher, userID, "anthropic", "scheduler-lock-key")
	blockingCipher := &firstOpenBlockingCipher{inner: baseCipher, entered: make(chan struct{}), release: make(chan struct{})}
	srv.SetCredentialCipher(blockingCipher)
	srv.newAIProvider = func(provider, key, model string, _ time.Duration) (ai.Provider, error) {
		return &fingerprintProvider{name: provider, keyFingerprint: keyFingerprint(key)}, nil
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.runScheduledScrape(context.Background())
	}()
	defer func() {
		select {
		case <-blockingCipher.release:
		default:
			close(blockingCipher.release)
		}
	}()
	select {
	case <-blockingCipher.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not enter AI runtime resolution")
	}

	form := url.Values{"job_likes": {"must not commit during scheduler resolution"}}
	req := httptest.NewRequest(http.MethodPost, "/profile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	addCSRFToRequest(req, srv, cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("profile save status = %d, want 409 while scheduler resolves runtime", rec.Code)
	}
	got, _, ok, err := st.ProfileForUser(context.Background(), userID)
	if err != nil || !ok || !strings.Contains(got, "old scheduler goal") || strings.Contains(got, "must not commit") {
		t.Fatalf("profile committed during scheduler runtime resolution: ok=%v err=%v profile=%s", ok, err, got)
	}

	close(blockingCipher.release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduled scrape did not finish after runtime resolution resumed")
	}
}

func assertSkippedScheduledRun(t *testing.T, st *storage.Store, wantReason string) {
	t.Helper()
	run, ok, err := st.LatestScrapeRun(context.Background())
	if err != nil || !ok {
		t.Fatalf("LatestScrapeRun ok=%v err=%v", ok, err)
	}
	if run.Trigger != storage.ScrapeTriggerScheduled || run.Status != storage.ScrapeRunStatusFailure {
		t.Fatalf("scheduled run = trigger %q status %q, want scheduled failure", run.Trigger, run.Status)
	}
	if run.ErrorSummary != wantReason {
		t.Fatalf("ErrorSummary = %q, want %q", run.ErrorSummary, wantReason)
	}
}

func waitForScheduler(t *testing.T, done <-chan struct{}, ok func() bool) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ok() {
			return
		}
		select {
		case <-done:
			if ok() {
				return
			}
			t.Fatal("scheduler stopped before expected condition")
		case <-deadline:
			t.Fatal("timed out waiting for scheduler")
		case <-ticker.C:
		}
	}
}
