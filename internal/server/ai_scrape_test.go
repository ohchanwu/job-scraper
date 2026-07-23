package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ohchanwu/jobcron/internal/ai"
	"github.com/ohchanwu/jobcron/internal/profile"
	"github.com/ohchanwu/jobcron/internal/scraper"
	"github.com/ohchanwu/jobcron/internal/storage"
)

// newcomerStub is an AI provider that always extracts "신입 OK, 0 years, no
// education requirement" with a fixed token cost.
func newcomerStub() *ai.StubProvider {
	zero := 0
	return &ai.StubProvider{
		NameVal: "stub",
		ExtractFn: func(ctx context.Context, modelText string) (ai.Extraction, ai.Usage, error) {
			return ai.Extraction{MinCareer: 0, MaxCareer: &zero, Newcomer: true, EducationEnum: ai.EduNone, CareerEvidence: "신입 환영"},
				ai.Usage{InputTokens: 100, OutputTokens: 20}, nil
		},
	}
}

func saveSinipProfile(t *testing.T, srv *Server) {
	t.Helper()
	j, _ := profile.Marshal(profile.Profile{CareerYears: 0})
	if _, _, err := srv.store.SaveProfile(context.Background(), j); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
}

// aiExtractionCount counts cached extractions under the global contract version
// (one row per posting in these tests).
func aiExtractionCount(t *testing.T, srv *Server) int {
	t.Helper()
	postings, err := srv.store.AllPostings(context.Background())
	if err != nil {
		t.Fatalf("list postings: %v", err)
	}
	contentHashes := make(map[int64]string, len(postings))
	for _, p := range postings {
		_, contentHashes[p.ID], _ = ai.ModelInput(p)
	}
	m, err := srv.store.AIExtractionsByPostingID(context.Background(), contentHashes, ai.ExtractionContractVersion())
	if err != nil {
		t.Fatalf("count ai_extractions: %v", err)
	}
	return len(m)
}

// TestRunScrapeAutoRatesFreshBriefingWithStage2 proves the fix: a scrape now
// runs Stage-2 over the fresh briefing, so new postings carry their evidence-
// cited AI delta WITHOUT a manual 재평가 press.
func TestRunScrapeAutoRatesFreshBriefingWithStage2(t *testing.T) {
	mkDetail := func(id, title string) scraper.Posting {
		p := listingPosting(id, title)
		p.Description = "서버 개발자를 찾습니다" // the quote rerateStub cites → the gate keeps the delta
		return p
	}
	f := &fakeScraper{
		listing: []scraper.Posting{listingPosting("1", "백엔드 신입"), listingPosting("2", "서버 신입")},
		details: map[string]scraper.Posting{
			"1": mkDetail("1", "백엔드 신입"),
			"2": mkDetail("2", "서버 신입"),
		},
	}
	srv, st := newTestServer(t, f)
	runtime := testAIRuntime(1, rerateStub(), "test-model") // ScoreDeltaFn set; Stage-1 backfill errors harmlessly
	ctx := context.Background()
	zero := 0
	pj, _ := profile.Marshal(profile.Profile{CareerYears: 0, MinScore: &zero, JobLikes: "백엔드 서버 개발"})
	if _, _, err := st.SaveProfile(ctx, pj); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}

	if _, err := srv.runScrape(ctx, noopEmit, 1, runtime); err != nil {
		t.Fatalf("runScrape: %v", err)
	}

	// Both fresh, visible postings should have a Stage-2 delta cached against the
	// current goal — no 재평가 needed.
	if n := countAIScores(t, srv, runtime); n != 2 {
		t.Fatalf("after scrape: %d Stage-2 deltas cached, want 2 (auto-rated, no manual 재평가)", n)
	}
}

func TestRunScrapeOrdersDealbreakerValidationBeforeStage2(t *testing.T) {
	p := listingPosting("dealbreaker-order", "신입 리서치 개발자")
	p.Description = "리서치 아님. 서버 개발자를 찾습니다"
	f := &fakeScraper{
		listing: []scraper.Posting{listingPosting("dealbreaker-order", p.Title)},
		details: map[string]scraper.Posting{"dealbreaker-order": p},
	}
	srv, st := newPostgresTestServer(t, f)
	ctx := context.Background()
	userID := insertAIRuntimeTestUser(t, st, "dealbreaker-order@example.invalid")
	zero := 0
	prof := profile.Profile{CareerYears: 0, MinScore: &zero, Dealbreakers: []string{"리서치"}, JobLikes: "서버 개발"}
	saveAIRuntimeProfile(t, st, userID, prof)
	var order []string
	provider := &ai.StubProvider{
		NameVal: "stub",
		ExtractFn: func(context.Context, string) (ai.Extraction, ai.Usage, error) {
			order = append(order, "stage1a")
			return ai.Extraction{Newcomer: true, EducationEnum: ai.EduNone}, ai.Usage{InputTokens: 1}, nil
		},
		ValidateDealbreakersFn: func(_ context.Context, _ string, candidates []ai.DealbreakerCandidate) ([]ai.DealbreakerValidation, ai.Usage, error) {
			order = append(order, "stage1b")
			return []ai.DealbreakerValidation{{CandidateID: candidates[0].ID, Verdict: ai.DealbreakerNotApplicable, Evidence: "리서치 아님"}}, ai.Usage{InputTokens: 2}, nil
		},
		ScoreDeltaFn: func(context.Context, string, string) ([]ai.RawDeltaItem, ai.Usage, error) {
			postings, err := st.AllPostings(ctx)
			if err != nil || len(postings) != 1 {
				return nil, ai.Usage{}, errors.New("score merge missing before Stage 2")
			}
			score, ok, err := st.ScoreByPostingIDForUser(ctx, userID, postings[0].ID)
			if err != nil || !ok || score.Total == -1 {
				return nil, ai.Usage{}, errors.New("contextual score not merged before Stage 2")
			}
			order = append(order, "stage2")
			return []ai.RawDeltaItem{{Signal: "서버", Kind: ai.KindPresence, Delta: 1, Quote: "서버 개발"}}, ai.Usage{InputTokens: 3}, nil
		},
	}
	runtime := testAIRuntime(userID, provider, "shared-model")
	if _, err := srv.runScrape(ctx, noopEmit, userID, runtime); err != nil {
		t.Fatalf("runScrape: %v", err)
	}
	if got := strings.Join(order, " -> "); got != "stage1a -> stage1b -> stage2" {
		t.Fatalf("paid flow order = %q", got)
	}
}

func TestRunScrapeDealbreakerCacheHitSpendsNothing(t *testing.T) {
	p := listingPosting("dealbreaker-cache", "신입 리서치 개발자")
	p.Description = "리서치 아님. 서버 개발자를 찾습니다"
	f := &fakeScraper{
		listing: []scraper.Posting{listingPosting("dealbreaker-cache", p.Title)},
		details: map[string]scraper.Posting{"dealbreaker-cache": p},
	}
	srv, st := newPostgresTestServer(t, f)
	ctx := context.Background()
	userID := insertAIRuntimeTestUser(t, st, "dealbreaker-cache@example.invalid")
	zero := 0
	prof := profile.Profile{CareerYears: 0, MinScore: &zero, Dealbreakers: []string{"리서치"}, JobLikes: "서버 개발"}
	saveAIRuntimeProfile(t, st, userID, prof)
	provider := &ai.StubProvider{
		NameVal: "stub",
		ExtractFn: func(context.Context, string) (ai.Extraction, ai.Usage, error) {
			return ai.Extraction{Newcomer: true, EducationEnum: ai.EduNone}, ai.Usage{InputTokens: 1}, nil
		},
		ValidateDealbreakersFn: func(_ context.Context, _ string, candidates []ai.DealbreakerCandidate) ([]ai.DealbreakerValidation, ai.Usage, error) {
			return []ai.DealbreakerValidation{{CandidateID: candidates[0].ID, Verdict: ai.DealbreakerNotApplicable, Evidence: "리서치 아님"}}, ai.Usage{InputTokens: 2}, nil
		},
		ScoreDeltaFn: func(context.Context, string, string) ([]ai.RawDeltaItem, ai.Usage, error) {
			return nil, ai.Usage{InputTokens: 3}, nil
		},
	}
	runtime := testAIRuntime(userID, provider, "shared-model")
	if _, err := srv.runScrape(ctx, noopEmit, userID, runtime); err != nil {
		t.Fatal(err)
	}
	day := time.Now().UTC().Format("2006-01-02")
	inBefore, outBefore, _ := st.AIUsageForDay(ctx, userID, day)
	validationCalls := provider.ValidateDealbreakersCalls
	stage2Calls := provider.ScoreDeltaCalls
	if _, err := st.SQLDB().ExecContext(ctx, `UPDATE postings SET detail_fetched_at = $1`, time.Now().UTC().Add(-25*time.Hour)); err != nil {
		t.Fatalf("age detail cache: %v", err)
	}
	if _, err := srv.runScrape(ctx, noopEmit, userID, runtime); err != nil {
		t.Fatal(err)
	}
	inAfter, outAfter, _ := st.AIUsageForDay(ctx, userID, day)
	if provider.ValidateDealbreakersCalls != validationCalls || provider.ScoreDeltaCalls != stage2Calls || inAfter != inBefore || outAfter != outBefore {
		t.Fatalf("cache hit respent: validation %d->%d stage2 %d->%d usage (%d,%d)->(%d,%d)", validationCalls, provider.ValidateDealbreakersCalls, stage2Calls, provider.ScoreDeltaCalls, inBefore, outBefore, inAfter, outAfter)
	}
}

func TestRunScrapeDealbreakerFallbacksRetainExclusion(t *testing.T) {
	cases := []struct {
		name       string
		validation func(context.Context, string, []ai.DealbreakerCandidate) ([]ai.DealbreakerValidation, ai.Usage, error)
		configure  func(*AIRuntime)
		runtimeOff bool
	}{
		{name: "provider error", validation: func(context.Context, string, []ai.DealbreakerCandidate) ([]ai.DealbreakerValidation, ai.Usage, error) {
			return nil, ai.Usage{}, errors.New("provider unavailable")
		}},
		{name: "malformed response", validation: func(context.Context, string, []ai.DealbreakerCandidate) ([]ai.DealbreakerValidation, ai.Usage, error) {
			return nil, ai.Usage{InputTokens: 2}, errors.New("malformed response")
		}},
		{name: "invalid quote", validation: func(context.Context, string, []ai.DealbreakerCandidate) ([]ai.DealbreakerValidation, ai.Usage, error) {
			return nil, ai.Usage{InputTokens: 2}, nil
		}},
		{name: "missing credential", runtimeOff: true},
		{name: "exhausted budget", configure: func(runtime *AIRuntime) { runtime.RunTokenCap = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := listingPosting("fallback", "신입 리서치 개발자")
			p.Description = "리서치 업무를 수행합니다"
			f := &fakeScraper{listing: []scraper.Posting{listingPosting("fallback", p.Title)}, details: map[string]scraper.Posting{"fallback": p}}
			srv, st := newPostgresTestServer(t, f)
			ctx := context.Background()
			userID := insertAIRuntimeTestUser(t, st, "fallback-"+strings.ReplaceAll(tc.name, " ", "-")+"@example.invalid")
			saveAIRuntimeProfile(t, st, userID, profile.Profile{Dealbreakers: []string{"리서치"}})
			provider := &ai.StubProvider{NameVal: "stub", ValidateDealbreakersFn: tc.validation}
			runtime := testAIRuntime(userID, provider, "shared-model")
			if tc.configure != nil {
				tc.configure(runtime)
			}
			if tc.runtimeOff {
				runtime = nil
			}
			if _, err := srv.runScrape(ctx, noopEmit, userID, runtime); err != nil {
				t.Fatalf("fallback aborted scrape: %v", err)
			}
			postings, _ := st.AllPostings(ctx)
			score, ok, err := st.ScoreByPostingIDForUser(ctx, userID, postings[0].ID)
			if err != nil || !ok || score.Total != -1 || !strings.Contains(score.BreakdownJSON, `"confidence":"unverified"`) {
				t.Fatalf("fallback score=%+v ok=%v err=%v", score, ok, err)
			}
		})
	}
}

func TestScheduledScrapeSkipsFreshAIByDefault(t *testing.T) {
	f := &fakeScraper{
		listing: []scraper.Posting{listingPosting("1", "백엔드 신입")},
		details: map[string]scraper.Posting{"1": listingPosting("1", "백엔드 신입")},
	}
	srv, st := newTestServer(t, f)
	stub := rerateStub()
	runtime := testAIRuntime(1, stub, "test-model")
	saveSinipProfile(t, srv)
	ctx := context.Background()

	if _, err := srv.runScrapeForTrigger(ctx, storage.ScrapeTriggerScheduled, noopEmit, 1, runtime); err != nil {
		t.Fatalf("scheduled runScrape: %v", err)
	}

	if stub.Calls != 0 {
		t.Fatalf("Extract calls = %d, want 0 for scheduled AI default-off", stub.Calls)
	}
	if stub.ScoreDeltaCalls != 0 {
		t.Fatalf("ScoreDelta calls = %d, want 0 for scheduled AI default-off", stub.ScoreDeltaCalls)
	}
	if n := aiExtractionCount(t, srv); n != 0 {
		t.Fatalf("ai_extractions rows = %d, want 0", n)
	}
	if n := countAIScores(t, srv, runtime); n != 0 {
		t.Fatalf("ai_scores rows = %d, want 0", n)
	}
	if _, err := st.ScoresByPostingID(ctx); err != nil {
		t.Fatalf("regular scores should still be readable: %v", err)
	}
}

func TestScheduledScrapeRunsFreshAIWhenEnabled(t *testing.T) {
	p := listingPosting("1", "백엔드 신입")
	p.Description = "서버 개발자를 찾습니다"
	f := &fakeScraper{
		listing: []scraper.Posting{listingPosting("1", "백엔드 신입")},
		details: map[string]scraper.Posting{"1": p},
	}
	srv, st := newTestServer(t, f)
	stub := rerateStub()
	runtime := testAIRuntime(1, stub, "test-model")
	ctx := context.Background()
	zero := 0
	pj, _ := profile.Marshal(profile.Profile{
		CareerYears:        0,
		MinScore:           &zero,
		JobLikes:           "백엔드 서버 개발",
		ScheduledAIEnabled: true,
	})
	if _, _, err := st.SaveProfile(ctx, pj); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}

	if _, err := srv.runScrapeForTrigger(ctx, storage.ScrapeTriggerScheduled, noopEmit, 1, runtime); err != nil {
		t.Fatalf("scheduled runScrape: %v", err)
	}

	if stub.ScoreDeltaCalls == 0 {
		t.Fatal("scheduled AI opt-in should run Stage-2 over the fresh briefing")
	}
	if n := countAIScores(t, srv, runtime); n != 1 {
		t.Fatalf("ai_scores rows = %d, want 1", n)
	}
}

func TestRunUSDCapHaltsManualScrapeExtractionAfterFirstCall(t *testing.T) {
	f := &fakeScraper{
		listing: []scraper.Posting{listingPosting("1", "백엔드 신입"), listingPosting("2", "서버 신입")},
		details: map[string]scraper.Posting{
			"1": listingPosting("1", "백엔드 신입"),
			"2": listingPosting("2", "서버 신입"),
		},
	}
	srv, st := newTestServer(t, f)
	ctx := context.Background()
	pj, _ := profile.Marshal(profile.Profile{CareerYears: 0, AIRunUSDCapCents: 1})
	if _, _, err := st.SaveProfile(ctx, pj); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	stub := &ai.StubProvider{
		NameVal: "stub",
		ExtractFn: func(ctx context.Context, modelText string) (ai.Extraction, ai.Usage, error) {
			zero := 0
			return ai.Extraction{MinCareer: 0, MaxCareer: &zero, Newcomer: true, EducationEnum: ai.EduNone, CareerEvidence: "신입"},
				ai.Usage{InputTokens: aiRunTokenCapForUSDCents(1) + 1}, nil
		},
	}
	runtime := testAIRuntime(1, stub, "test-model")
	runtime.RunTokenCap = aiRunTokenCapForUSDCents(1)

	if _, err := srv.runScrape(ctx, noopEmit, 1, runtime); err != nil {
		t.Fatalf("runScrape: %v", err)
	}
	if stub.Calls != 1 {
		t.Fatalf("Extract calls = %d, want 1 because the run USD cap stops the second posting", stub.Calls)
	}
}

func TestRunUSDCapHaltsOptInScheduledScrapeExtractionAfterFirstCall(t *testing.T) {
	f := &fakeScraper{
		listing: []scraper.Posting{listingPosting("1", "백엔드 신입"), listingPosting("2", "서버 신입")},
		details: map[string]scraper.Posting{
			"1": listingPosting("1", "백엔드 신입"),
			"2": listingPosting("2", "서버 신입"),
		},
	}
	srv, st := newTestServer(t, f)
	ctx := context.Background()
	pj, _ := profile.Marshal(profile.Profile{CareerYears: 0, ScheduledAIEnabled: true, AIRunUSDCapCents: 1})
	if _, _, err := st.SaveProfile(ctx, pj); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	stub := &ai.StubProvider{
		NameVal: "stub",
		ExtractFn: func(ctx context.Context, modelText string) (ai.Extraction, ai.Usage, error) {
			zero := 0
			return ai.Extraction{MinCareer: 0, MaxCareer: &zero, Newcomer: true, EducationEnum: ai.EduNone, CareerEvidence: "신입"},
				ai.Usage{InputTokens: aiRunTokenCapForUSDCents(1) + 1}, nil
		},
	}
	runtime := testAIRuntime(1, stub, "test-model")
	runtime.RunTokenCap = aiRunTokenCapForUSDCents(1)

	if _, err := srv.runScrapeForTrigger(ctx, storage.ScrapeTriggerScheduled, noopEmit, 1, runtime); err != nil {
		t.Fatalf("scheduled runScrape: %v", err)
	}
	if stub.Calls != 1 {
		t.Fatalf("Extract calls = %d, want 1 because the run USD cap stops the second scheduled posting", stub.Calls)
	}
}

func TestRunScrapeStubProviderExtractsNewPostings(t *testing.T) {
	f := &fakeScraper{
		listing: []scraper.Posting{listingPosting("1", "백엔드 신입"), listingPosting("2", "AI 신입")},
		details: map[string]scraper.Posting{
			"1": listingPosting("1", "백엔드 신입"),
			"2": listingPosting("2", "AI 신입"),
		},
	}
	srv, st := newTestServer(t, f)
	stub := newcomerStub()
	runtime := testAIRuntime(1, stub, "test-model")
	saveSinipProfile(t, srv)
	ctx := context.Background()

	if _, err := srv.runScrape(ctx, noopEmit, 1, runtime); err != nil {
		t.Fatalf("runScrape: %v", err)
	}

	// One ai_extractions row per new posting, and exactly one Extract call each.
	if n := aiExtractionCount(t, srv); n != 2 {
		t.Errorf("ai_extractions rows = %d, want 2", n)
	}
	if stub.Calls != 2 {
		t.Errorf("Extract calls = %d, want 2 (one per new posting)", stub.Calls)
	}
	if len(f.detailCalls) != 2 {
		t.Errorf("FetchDetail calls = %d, want 2", len(f.detailCalls))
	}
	// The cached extraction is readable under the run's ai_version.
	postings, err := st.AllPostings(ctx)
	if err != nil {
		t.Fatalf("AllPostings: %v", err)
	}
	contentHashes := make(map[int64]string, len(postings))
	for _, p := range postings {
		_, contentHashes[p.ID], _ = ai.ModelInput(p)
	}
	exts, err := st.AIExtractionsByPostingID(ctx, contentHashes, ai.ExtractionContractVersion())
	if err != nil || len(exts) != 2 {
		t.Fatalf("AIExtractionsByPostingID: got %d (err=%v), want 2", len(exts), err)
	}
	for id, e := range exts {
		if !e.Newcomer || e.EducationEnum != ai.EduNone {
			t.Errorf("posting %d extraction = %+v, want newcomer/none", id, e)
		}
	}
}

func TestRunScrapeProviderFailsScrapeContinues(t *testing.T) {
	f := &fakeScraper{
		listing: []scraper.Posting{listingPosting("1", "신입 공고")},
		details: map[string]scraper.Posting{"1": listingPosting("1", "신입 공고")},
	}
	srv, st := newTestServer(t, f)
	runtime := testAIRuntime(1, &ai.StubProvider{NameVal: "stub", ExtractFn: func(ctx context.Context, _ string) (ai.Extraction, ai.Usage, error) {
		return ai.Extraction{}, ai.Usage{}, errors.New("provider down")
	}}, "test-model")
	saveSinipProfile(t, srv)
	ctx := context.Background()

	res, err := srv.runScrape(ctx, noopEmit, 1, runtime)
	if err != nil {
		t.Fatalf("runScrape must not fail when the provider errors: %v", err)
	}
	if res.New != 1 || res.Scored != 1 {
		t.Errorf("ScrapeResult = %+v, want the posting still inserted + scored", res)
	}
	if n := aiExtractionCount(t, srv); n != 0 {
		t.Errorf("ai_extractions rows = %d, want 0 (provider failed, no cache row)", n)
	}
	// Posting is present and scored via the regex path.
	if ps, _ := st.AllPostings(ctx); len(ps) != 1 {
		t.Errorf("postings = %d, want 1", len(ps))
	}
}

func TestRunScrapeNoExtractorMakesNoAICalls(t *testing.T) {
	f := &fakeScraper{
		listing: []scraper.Posting{listingPosting("1", "신입")},
		details: map[string]scraper.Posting{"1": listingPosting("1", "신입")},
	}
	srv, _ := newTestServer(t, f) // no SetAIProvider — AI off (the default)
	saveSinipProfile(t, srv)
	if _, err := srv.runScrape(context.Background(), noopEmit, 0, nil); err != nil {
		t.Fatalf("runScrape: %v", err)
	}
}

func TestRunScrapeAlreadySeenDoesNotExtract(t *testing.T) {
	f := &fakeScraper{
		listing: []scraper.Posting{listingPosting("1", "기존 공고")},
		details: map[string]scraper.Posting{"1": listingPosting("1", "기존 공고")},
	}
	srv, st := newTestServer(t, f)
	stub := newcomerStub()
	runtime := testAIRuntime(1, stub, "test-model")
	saveSinipProfile(t, srv)
	ctx := context.Background()

	// Pre-insert the posting so it's "known" — the bump path must skip detail
	// AND extraction.
	known := listingPosting("1", "기존 공고")
	known.FirstSeenAt = time.Now().UTC()
	known.LastSeenAt = time.Now().UTC()
	if _, _, err := st.UpsertPosting(ctx, known); err != nil {
		t.Fatalf("seed posting: %v", err)
	}

	if _, err := srv.runScrape(ctx, noopEmit, 1, runtime); err != nil {
		t.Fatalf("runScrape: %v", err)
	}
	if len(f.detailCalls) != 0 {
		t.Errorf("FetchDetail called %v for a known posting; want none", f.detailCalls)
	}
	if stub.Calls != 0 {
		t.Errorf("Extract called %d times for a known posting; want 0 (no steady-state token burn)", stub.Calls)
	}
	if n := aiExtractionCount(t, srv); n != 0 {
		t.Errorf("ai_extractions rows = %d, want 0", n)
	}
}

func TestExtractStage1CacheHitSurvivesProviderAndModelChange(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	ctx := context.Background()
	p := listingPosting("1", "백엔드 신입")
	p.Description = "서버 개발자를 찾습니다"
	id := mustUpsert(t, st, p)
	originalProvider := newcomerStub()
	originalProvider.NameVal = "provider-a"
	originalRuntime := testAIRuntime(1, originalProvider, "model-a")
	srv.extractStage1(ctx, id, p, time.Now().UTC(), 1, originalRuntime, srv.newAIBudget(ctx, 1, originalRuntime))
	if originalProvider.Calls != 1 {
		t.Fatalf("initial Extract calls = %d, want cache population", originalProvider.Calls)
	}

	changedProvider := newcomerStub()
	changedProvider.NameVal = "provider-b"
	runtime := testAIRuntime(1, changedProvider, "model-b")
	srv.extractStage1(ctx, id, p, time.Now().UTC(), 1, runtime, srv.newAIBudget(ctx, 1, runtime))

	if changedProvider.Calls != 0 {
		t.Fatalf("Extract called %d times after provider/model change; want global cache hit", changedProvider.Calls)
	}
}

func TestExtractStage1CacheMissesWhenContentOrContractChanges(t *testing.T) {
	for _, tc := range []struct {
		name        string
		seedVersion string
		change      func(*scraper.Posting)
	}{
		{
			name:        "posting content",
			seedVersion: ai.ExtractionContractVersion(),
			change: func(p *scraper.Posting) {
				p.Description = "변경된 서버 공고"
			},
		},
		{name: "extraction contract", seedVersion: "prior-contract-version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, st := newTestServer(t, &fakeScraper{})
			ctx := context.Background()
			p := listingPosting("1", "백엔드 신입")
			p.Description = "서버 개발자를 찾습니다"
			id := mustUpsert(t, st, p)
			_, oldHash, _ := ai.ModelInput(p)
			seeded := ai.Extraction{Newcomer: true, EducationEnum: ai.EduNone}
			if err := st.UpsertAIExtraction(ctx, id, oldHash, tc.seedVersion, seeded, time.Now()); err != nil {
				t.Fatalf("seed extraction: %v", err)
			}
			if tc.change != nil {
				tc.change(&p)
			}

			stub := newcomerStub()
			runtime := testAIRuntime(1, stub, "model-b")
			srv.extractStage1(ctx, id, p, time.Now().UTC(), 1, runtime, srv.newAIBudget(ctx, 1, runtime))

			if stub.Calls != 1 {
				t.Fatalf("Extract calls = %d, want cache miss", stub.Calls)
			}
			_, newHash, _ := ai.ModelInput(p)
			if _, ok, err := st.AIExtraction(ctx, id, newHash, ai.ExtractionContractVersion()); err != nil || !ok {
				t.Fatalf("current extraction cache row: ok=%v err=%v", ok, err)
			}
		})
	}
}

func TestExtractStage1FailuresDoNotWriteCache(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "provider error", err: errors.New("provider down")},
		{name: "malformed output", err: errors.New("ai: decode extraction response")},
		{name: "validation failure", err: errors.New("ai: min_career out of range")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, st := newTestServer(t, &fakeScraper{})
			ctx := context.Background()
			p := listingPosting("1", "백엔드 신입")
			id := mustUpsert(t, st, p)
			stub := &ai.StubProvider{
				NameVal: "stub",
				ExtractFn: func(context.Context, string) (ai.Extraction, ai.Usage, error) {
					return ai.Extraction{}, ai.Usage{}, tc.err
				},
			}
			runtime := testAIRuntime(1, stub, "model")

			srv.extractStage1(ctx, id, p, time.Now().UTC(), 1, runtime, srv.newAIBudget(ctx, 1, runtime))

			if n := aiExtractionCount(t, srv); n != 0 {
				t.Fatalf("ai_extractions rows = %d, want 0", n)
			}
		})
	}
}

func TestExtractStage1DeterministicFallbackDoesNotWriteCache(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	p := listingPosting("1", "백엔드 신입")
	id := mustUpsert(t, st, p)
	stub := newcomerStub()
	runtime := testAIRuntime(1, stub, "model")

	srv.extractStage1(context.Background(), id, p, time.Now().UTC(), 1, runtime, nil)

	if stub.Calls != 0 {
		t.Fatalf("Extract calls = %d, want deterministic fallback without provider", stub.Calls)
	}
	if n := aiExtractionCount(t, srv); n != 0 {
		t.Fatalf("ai_extractions rows = %d, want 0", n)
	}
}

func TestScoreAllIgnoresExtractionForPriorContentHash(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	ctx := context.Background()
	p := listingPosting("1", "백엔드 신입")
	p.Description = "기존 공고"
	p.EducationName = ""
	id := mustUpsert(t, st, p)
	_, oldHash, _ := ai.ModelInput(p)
	stale := ai.Extraction{Newcomer: true, EducationEnum: ai.EduBachelor}
	if err := st.UpsertAIExtraction(ctx, id, oldHash, ai.ExtractionContractVersion(), stale, time.Now()); err != nil {
		t.Fatalf("seed stale extraction: %v", err)
	}
	p.Description = "수정된 공고"
	if err := st.RefreshPostingDetail(ctx, id, p, time.Now().UTC()); err != nil {
		t.Fatalf("RefreshPostingDetail: %v", err)
	}
	saveProfileJSON(t, srv, profile.Profile{
		CareerYears:  0,
		MaxEducation: profile.EducationHighSchool,
	})
	runtime := testAIRuntime(1, newcomerStub(), "model")

	if _, err := srv.scoreAll(ctx, 1, runtime); err != nil {
		t.Fatalf("scoreAll: %v", err)
	}
	score, ok, err := st.ScoreByPostingID(ctx, id)
	if err != nil || !ok {
		t.Fatalf("ScoreByPostingID: ok=%v err=%v", ok, err)
	}
	if score.Total == -1 {
		t.Fatal("score reused stale education extraction after posting content changed")
	}
}

func TestRunScrapePerRunBudgetHalts(t *testing.T) {
	f := &fakeScraper{
		listing: []scraper.Posting{listingPosting("1", "a"), listingPosting("2", "b"), listingPosting("3", "c")},
		details: map[string]scraper.Posting{
			"1": listingPosting("1", "a"), "2": listingPosting("2", "b"), "3": listingPosting("3", "c"),
		},
	}
	srv, _ := newTestServer(t, f)
	stub := newcomerStub() // each Extract spends 120 tokens (100 in + 20 out)
	runtime := testAIRuntime(1, stub, "test-model")
	runtime.RunTokenCap = 5 // tiny: the first call exhausts it
	saveSinipProfile(t, srv)

	var events []string
	emit := func(ev, data string) { events = append(events, ev+"\x00"+data) }

	if _, err := srv.runScrape(context.Background(), emit, 1, runtime); err != nil {
		t.Fatalf("runScrape: %v", err)
	}
	if stub.Calls != 1 {
		t.Errorf("Extract calls = %d, want 1 (budget halts after the first)", stub.Calls)
	}
	if n := aiExtractionCount(t, srv); n != 1 {
		t.Errorf("ai_extractions rows = %d, want 1", n)
	}
	var sawColdStart bool
	for _, e := range events {
		if strings.HasPrefix(e, "status\x00") && strings.Contains(e, "AI 예산") {
			sawColdStart = true
		}
	}
	if !sawColdStart {
		t.Errorf("expected a calm cold-start status when the budget halted; events=%v", events)
	}
}

// TestRunScrapeEndToEndScoreCorrection ties T4 (extraction wired) to T3
// (cache-read scoring): a posting whose body reads "경력 5년 이상" (regex would
// score a 신입 user 0) is corrected by the AI extraction to newcomer=true and
// scores the full newcomer award.
func TestRunScrapeEndToEndScoreCorrection(t *testing.T) {
	body := listingPosting("1", "백엔드 엔지니어")
	body.Description = "경력 5년 이상 우대"
	body.Newcomer = false
	body.MinCareer = 5
	f := &fakeScraper{
		listing: []scraper.Posting{listingPosting("1", "백엔드 엔지니어")},
		details: map[string]scraper.Posting{"1": body},
	}
	srv, st := newTestServer(t, f)
	runtime := testAIRuntime(1, newcomerStub(), "test-model")
	saveSinipProfile(t, srv) // 신입 (CareerYears 0)
	ctx := context.Background()

	if _, err := srv.runScrape(ctx, noopEmit, 1, runtime); err != nil {
		t.Fatalf("runScrape: %v", err)
	}
	scores, err := st.ScoresByPostingID(ctx)
	if err != nil || len(scores) != 1 {
		t.Fatalf("ScoresByPostingID: got %d (err=%v), want 1", len(scores), err)
	}
	for _, sc := range scores {
		// 신입 award is the default career weight (25). Without the AI
		// correction the regex override would score this 0.
		if sc.Total != profile.DefaultCareerWeight {
			t.Errorf("Total = %d, want %d (AI-corrected full newcomer award)", sc.Total, profile.DefaultCareerWeight)
		}
	}
}
