package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ohchanwu/jobcron/internal/ai"
	"github.com/ohchanwu/jobcron/internal/profile"
)

// rerateStub returns a provider whose ScoreDelta cites a quote that is present
// in the seeded postings, so the gate keeps it. It counts ScoreDelta calls.
func rerateStub() *ai.StubProvider {
	return &ai.StubProvider{
		NameVal: "stub",
		ScoreDeltaFn: func(ctx context.Context, modelText, profileText string) ([]ai.RawDeltaItem, ai.Usage, error) {
			return []ai.RawDeltaItem{
				{Signal: "백엔드", Kind: ai.KindPresence, Delta: 7, Quote: "서버 개발자를 찾습니다", MatchedGoal: "좋아하는 업무"},
			}, ai.Usage{InputTokens: 50, OutputTokens: 10}, nil
		},
	}
}

// seedRerate saves a show-everything profile and two postings first seen today,
// then scores them so they are visible on /today. Returns the server + store.
func seedRerate(t *testing.T) (*Server, *ai.StubProvider, *AIRuntime) {
	t.Helper()
	srv, st := newTestServer(t, &fakeScraper{})
	ctx := context.Background()
	zero := 0
	prof := profile.Profile{CareerYears: 0, MinScore: &zero, JobLikes: "백엔드 서버 개발"}
	pj, _ := profile.Marshal(prof)
	if _, _, err := st.SaveProfile(ctx, pj); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	now := time.Now().UTC()
	for _, id := range []string{"r1", "r2"} {
		p := listingPosting(id, "신입 백엔드 개발자")
		p.Description = "서버 개발자를 찾습니다"
		p.FirstSeenAt, p.LastSeenAt = now, now
		if _, _, err := st.UpsertPosting(ctx, p); err != nil {
			t.Fatalf("UpsertPosting: %v", err)
		}
	}
	stub := rerateStub()
	runtime := testAIRuntime(1, stub, "test-model")
	if _, err := srv.scoreAll(ctx, 1, runtime); err != nil { // initial scores → rows visible on today
		t.Fatalf("scoreAll: %v", err)
	}
	return srv, stub, runtime
}

func TestRunRerateRatesVisibleRows(t *testing.T) {
	srv, stub, runtime := seedRerate(t)
	ctx := context.Background()

	summary, err := srv.runRerate(ctx, "today", noopEmit, 1, runtime)
	if err != nil {
		t.Fatalf("runRerate: %v", err)
	}
	if summary.Analyzed != 2 {
		t.Errorf("rated %d rows, want 2", summary.Analyzed)
	}
	if stub.ScoreDeltaCalls != 2 {
		t.Errorf("ScoreDelta calls = %d, want 2 (one per visible uncached row)", stub.ScoreDeltaCalls)
	}
	// Both postings now carry a fresh ai_scores row.
	deltas, err := srv.store.AIScoresByPostingID(ctx, 1, profile.AIInputHash(currentProfile(t, srv)), runtime.ScoreVersion)
	if err != nil {
		t.Fatalf("AIScoresByPostingID: %v", err)
	}
	if len(deltas) != 2 {
		t.Fatalf("ai_scores rows = %d, want 2", len(deltas))
	}
	// The cited delta survived the gate and landed in the stored score breakdown.
	scores, _ := srv.store.ScoresByPostingID(ctx)
	for id, sc := range scores {
		if sc.Total < 7 {
			t.Errorf("posting %d Total = %d, want >= 7 (AI +7 merged)", id, sc.Total)
		}
	}
}

func TestRunRerateValidatesExcludedPostingBeforeStage2(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	ctx := context.Background()
	userID := insertAIRuntimeTestUser(t, st, "rerate-dealbreaker@example.invalid")
	zero := 0
	prof := profile.Profile{CareerYears: 0, MinScore: &zero, Dealbreakers: []string{"리서치"}, JobLikes: "서버 개발"}
	saveAIRuntimeProfile(t, st, userID, prof)
	now := time.Now().UTC()
	p := listingPosting("rerate-dealbreaker", "신입 리서치 개발자")
	p.Description = "리서치 아님. 서버 개발자를 찾습니다"
	p.FirstSeenAt, p.LastSeenAt = now, now
	postingID := mustUpsert(t, st, p)
	provider := &ai.StubProvider{
		NameVal: "stub",
		ValidateDealbreakersFn: func(_ context.Context, _ string, candidates []ai.DealbreakerCandidate) ([]ai.DealbreakerValidation, ai.Usage, error) {
			return []ai.DealbreakerValidation{{CandidateID: candidates[0].ID, Verdict: ai.DealbreakerNotApplicable, Evidence: "리서치 아님"}}, ai.Usage{InputTokens: 2}, nil
		},
		ScoreDeltaFn: func(context.Context, string, string) ([]ai.RawDeltaItem, ai.Usage, error) {
			return []ai.RawDeltaItem{{Signal: "서버", Kind: ai.KindPresence, Delta: 1, Quote: "서버 개발"}}, ai.Usage{InputTokens: 3}, nil
		},
	}
	runtime := testAIRuntime(userID, provider, "shared-model")
	if _, err := srv.scoreAll(ctx, userID, runtime); err != nil {
		t.Fatal(err)
	}
	before, ok, err := st.ScoreByPostingIDForUser(ctx, userID, postingID)
	if err != nil || !ok || before.Total != -1 {
		t.Fatalf("precondition score=%+v ok=%v err=%v", before, ok, err)
	}

	summary, err := srv.runRerate(ctx, "today", noopEmit, userID, runtime)
	if err != nil {
		t.Fatalf("runRerate: %v", err)
	}
	if provider.ValidateDealbreakersCalls != 1 || provider.ScoreDeltaCalls != 1 || summary.Visible != 1 || summary.Analyzed != 1 {
		t.Fatalf("summary=%+v validation=%d stage2=%d", summary, provider.ValidateDealbreakersCalls, provider.ScoreDeltaCalls)
	}
	after, ok, err := st.ScoreByPostingIDForUser(ctx, userID, postingID)
	if err != nil || !ok || after.Total == -1 {
		t.Fatalf("posting did not re-enter Today: score=%+v ok=%v err=%v", after, ok, err)
	}
}

func TestRunRerateBackfillsStage1DespiteCurrentStage2Cache(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	ctx := context.Background()
	userID := insertAIRuntimeTestUser(t, st, "rerate-stage1-order@example.invalid")
	zero := 0
	prof := profile.Profile{
		CareerYears:  0,
		MinScore:     &zero,
		MaxEducation: profile.EducationHighSchool,
		JobLikes:     "서버 개발",
	}
	saveAIRuntimeProfile(t, st, userID, prof)
	now := time.Now().UTC()
	p := listingPosting("rerate-stage1-order", "신입 서버 개발자")
	p.Description = "서버 개발자를 찾습니다"
	p.EducationName = "대졸(4년)"
	p.FirstSeenAt, p.LastSeenAt = now, now
	postingID := mustUpsert(t, st, p)
	provider := &ai.StubProvider{
		NameVal: "stub",
		ExtractFn: func(context.Context, string) (ai.Extraction, ai.Usage, error) {
			return ai.Extraction{Newcomer: true, EducationEnum: ai.EduNone}, ai.Usage{InputTokens: 2}, nil
		},
		ScoreDeltaFn: func(context.Context, string, string) ([]ai.RawDeltaItem, ai.Usage, error) {
			return nil, ai.Usage{InputTokens: 3}, nil
		},
	}
	runtime := testAIRuntime(userID, provider, "shared-model")
	if err := st.UpsertAIScore(ctx, userID, postingID, profile.AIInputHash(prof), runtime.ScoreVersion, ai.Delta{}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.scoreAll(ctx, userID, runtime); err != nil {
		t.Fatal(err)
	}
	before, ok, err := st.ScoreByPostingIDForUser(ctx, userID, postingID)
	if err != nil || !ok || before.Total != -1 {
		t.Fatalf("precondition score=%+v ok=%v err=%v", before, ok, err)
	}

	summary, err := srv.runRerate(ctx, "today", noopEmit, userID, runtime)
	if err != nil {
		t.Fatalf("runRerate: %v", err)
	}
	if provider.Calls != 1 || provider.ScoreDeltaCalls != 0 || summary.Visible != 1 || summary.Analyzed != 1 {
		t.Fatalf("summary=%+v stage1=%d stage2=%d, want Stage 1A despite a free Stage 2 hit", summary, provider.Calls, provider.ScoreDeltaCalls)
	}
	after, ok, err := st.ScoreByPostingIDForUser(ctx, userID, postingID)
	if err != nil || !ok || after.Total == -1 {
		t.Fatalf("Stage 1 extraction did not restore posting before Stage 2: score=%+v ok=%v err=%v", after, ok, err)
	}
}

func TestRunRerateReconnectReusesCache(t *testing.T) {
	srv, stub, runtime := seedRerate(t)
	ctx := context.Background()

	first, err := srv.runRerate(ctx, "today", noopEmit, 1, runtime)
	if err != nil {
		t.Fatalf("first runRerate: %v", err)
	}
	if first.ProviderCalls != 2 {
		t.Fatalf("first ProviderCalls = %d, want 2", first.ProviderCalls)
	}
	callsAfterFirst := stub.ScoreDeltaCalls

	second, err := srv.runRerate(ctx, "today", noopEmit, 1, runtime)
	if err != nil {
		t.Fatalf("second runRerate: %v", err)
	}
	if second.Analyzed != 2 || second.ProviderCalls != 0 {
		t.Fatalf("second summary = %+v, want analyzed cache hits and zero calls", second)
	}
	if stub.ScoreDeltaCalls != callsAfterFirst {
		t.Fatalf("provider calls changed %d -> %d", callsAfterFirst, stub.ScoreDeltaCalls)
	}
	if got := rerateDoneMessage(second); got != "이미 모든 공고가 AI로 평가됐습니다. 추가 토큰은 사용하지 않았어요." {
		t.Fatalf("no-op copy = %q", got)
	}
}

func TestRunRerateBudgetMessagePointsToProfileSettings(t *testing.T) {
	srv, _, runtime := seedRerate(t)
	runtime.RunTokenCap = 0
	ctx := context.Background()
	var messages []string
	emit := func(event, data string) {
		if event == "status" {
			messages = append(messages, data)
		}
	}

	if _, err := srv.runRerate(ctx, "today", emit, 1, runtime); err != nil {
		t.Fatalf("runRerate: %v", err)
	}
	body := strings.Join(messages, "\n")
	if !strings.Contains(body, "오늘 AI 예산을 다 써서") || !strings.Contains(body, "프로필 설정") {
		t.Fatalf("budget cap message should explain the cap and point to settings, got:\n%s", body)
	}
}

// TestRerateProgressesAcrossPressesUnderBudget proves the behavior a user with a
// large list depends on: when the per-run token budget halts a re-rate partway,
// each following press skips the already-rated rows (Stage-2 cache hits, no
// re-spend) and rates the ones after them, until the whole list is done. Each
// posting's ScoreDelta is called exactly once across all presses — never
// re-rated — even though the concurrent worker pool can overshoot the per-run
// cap by a bounded number of calls.
func TestRerateProgressesAcrossPressesUnderBudget(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	ctx := context.Background()
	zero := 0
	prof := profile.Profile{CareerYears: 0, MinScore: &zero, JobLikes: "백엔드 서버 개발"}
	pj, _ := profile.Marshal(prof)
	if _, _, err := st.SaveProfile(ctx, pj); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	now := time.Now().UTC()
	const total = 24
	for i := 0; i < total; i++ {
		p := listingPosting("p"+string(rune('a'+i)), "신입 백엔드 개발자")
		p.Description = "서버 개발자를 찾습니다"
		p.FirstSeenAt, p.LastSeenAt = now, now
		if _, _, err := st.UpsertPosting(ctx, p); err != nil {
			t.Fatalf("UpsertPosting: %v", err)
		}
	}
	stub := rerateStub() // each ScoreDelta debits 60 tokens (50 in + 10 out)
	runtime := testAIRuntime(1, stub, "test-model")
	// Per-run cap ≈ 8 calls (8×60=480), so the run halts well short of `total`.
	// The concurrent worker pool can overshoot the cap by at most rerateWorkers
	// calls (workers that pass canSpend before in-flight debits land), so one
	// press rates somewhere in [8, 8+rerateWorkers] rows — always strictly fewer
	// than `total`. The daily cap is high so only the run cap binds.
	runtime.RunTokenCap = 480
	runtime.DailyTokenCap = 1_000_000
	if _, err := srv.scoreAll(ctx, 1, runtime); err != nil {
		t.Fatalf("scoreAll: %v", err)
	}

	// Press 1 halts partway: the run budget stops it before the whole list.
	if _, err := srv.runRerate(ctx, "today", noopEmit, 1, runtime); err != nil {
		t.Fatalf("press 1: %v", err)
	}
	rated1 := countAIScores(t, srv, runtime)
	if rated1 == 0 || rated1 >= total {
		t.Fatalf("press 1 rated %d rows, want a partial run (0 < n < %d): the budget must halt it partway", rated1, total)
	}
	if stub.ScoreDeltaCalls != rated1 {
		t.Fatalf("press 1: %d ScoreDelta calls but %d rows rated — every successful call must cache exactly one row",
			stub.ScoreDeltaCalls, rated1)
	}

	// Keep pressing: each press resumes on the still-uncached rows (cache hits
	// skip the already-rated ones, no re-spend) until the whole list is rated.
	for press := 2; countAIScores(t, srv, runtime) < total; press++ {
		if press > 20 {
			t.Fatalf("re-rate did not finish the list within 20 presses (rated %d/%d)", countAIScores(t, srv, runtime), total)
		}
		if _, err := srv.runRerate(ctx, "today", noopEmit, 1, runtime); err != nil {
			t.Fatalf("press %d: %v", press, err)
		}
	}

	// Every row rated exactly once across all presses — never re-rated.
	if stub.ScoreDeltaCalls != total {
		t.Fatalf("total ScoreDelta calls = %d, want %d (each row rated exactly once, never re-rated)",
			stub.ScoreDeltaCalls, total)
	}

	// A press over a fully-rated list spends nothing.
	if _, err := srv.runRerate(ctx, "today", noopEmit, 1, runtime); err != nil {
		t.Fatalf("final press: %v", err)
	}
	if stub.ScoreDeltaCalls != total {
		t.Fatalf("final press made extra ScoreDelta calls (%d > %d) — a fully-rated list must re-spend nothing",
			stub.ScoreDeltaCalls, total)
	}
}

// countAIScores counts rows in ai_scores under the server's ai_version for the
// current profile's goal text (the fresh, current-goal deltas).
func countAIScores(t *testing.T, srv *Server, runtime *AIRuntime) int {
	t.Helper()
	m, err := srv.store.AIScoresByPostingID(context.Background(),
		1, profile.AIInputHash(currentProfile(t, srv)), runtime.ScoreVersion)
	if err != nil {
		t.Fatalf("AIScoresByPostingID: %v", err)
	}
	return len(m)
}

func TestRerateMutuallyExclusiveWithScrape(t *testing.T) {
	srv, _, _ := seedRerate(t)

	t.Run("re-rate is rejected while a scrape holds the lock", func(t *testing.T) {
		lease := srv.flight.tryAcquire(scrapeAllKey)
		if lease == nil {
			t.Fatal("could not acquire the scrape lock for the test")
		}
		defer lease.release()
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/rerate?surface=today&entry=entry-token-00000001", nil))
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 while scrape lock is held", rec.Code)
		}
	})

	t.Run("a scrape is rejected while a re-rate holds the lock", func(t *testing.T) {
		lease := srv.flight.tryAcquire(scrapeAllKey)
		if lease == nil {
			t.Fatal("could not acquire the shared lock for the test")
		}
		defer lease.release()
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scrape", nil))
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 (shared scrape⟷re-rate lock)", rec.Code)
		}
	})
}

func TestRerateRejectedWhenAIOff(t *testing.T) {
	srv, _ := newTestServer(t, &fakeScraper{}) // no SetAIProvider
	saveSinipProfile(t, srv)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/rerate?surface=today&entry=entry-token-00000001", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (AI not configured)", rec.Code)
	}
}

func TestRerateRejectsUnknownSurface(t *testing.T) {
	srv, _, _ := seedRerate(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/rerate?surface=hidden", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (unknown surface)", rec.Code)
	}
}

func TestRerateButtonHiddenWithoutKey(t *testing.T) {
	// AI off → no button rendered (design §4: no dead control).
	srv, _ := newTestServer(t, &fakeScraper{})
	saveSinipProfile(t, srv)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.Contains(rec.Body.String(), `id="rerate"`) {
		t.Fatal("the re-rate button must be hidden when no AI key is configured")
	}

}

// TestRerateButtonShowsStaleCount: a visible row whose delta was computed
// against a prior profile gives the button the gold attention treatment with a
// count ("AI 평가 ·1" + has-stale).
func TestRerateButtonShowsStaleCount(t *testing.T) {
	srv, _ := newTestServer(t, &fakeScraper{})
	ctx := context.Background()
	zero := 0
	prof := profile.Profile{CareerYears: 0, MinScore: &zero, JobLikes: "백엔드 서버 개발"}
	pj, _ := profile.Marshal(prof)
	if _, _, err := srv.store.SaveProfile(ctx, pj); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	now := time.Now().UTC()
	p := listingPosting("s1", "신입 백엔드")
	p.Description = "서버 개발자를 찾습니다"
	p.FirstSeenAt, p.LastSeenAt = now, now
	id, _, _ := srv.store.UpsertPosting(ctx, p)
	runtime := testAIRuntime(1, rerateStub(), "test-model")

	// Seed a delta under a NON-current input hash → the merge falls back to it
	// and marks it stale (no fresh row for the profile's current goal text).
	if err := srv.store.UpsertAIScore(ctx, 1, id, "stalehash9999", runtime.ScoreVersion,
		ai.Delta{NetDelta: -3, Items: []ai.DeltaItem{{Signal: "x", Kind: ai.KindPresence, Delta: -3, Evidence: "서버 개발자를 찾습니다"}}}, now); err != nil {
		t.Fatalf("seed stale ai_score: %v", err)
	}
	if _, err := srv.scoreAll(ctx, 1, runtime); err != nil {
		t.Fatalf("scoreAll: %v", err)
	}

	b, err := srv.buildBriefing(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	info := srv.buildRerateInfo(ctx, 1, runtime, prof, "today", b.Today)
	if info == nil || info.StaleCount != 1 {
		t.Fatalf("rerate info = %+v, want one stale row", info)
	}
}

// TestRerateRespectsPerCallCap proves the user-adjustable per-call cap (R1):
// one press analyzes at most runtime.PerCallCap NOT-yet-cached rows even when the
// token budget has ample headroom, and a later press resumes on the rest. This
// is a legibility knob distinct from the hard token caps.
func TestRerateRespectsPerCallCap(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	ctx := context.Background()
	zero := 0
	prof := profile.Profile{CareerYears: 0, MinScore: &zero, JobLikes: "백엔드 서버 개발"}
	pj, _ := profile.Marshal(prof)
	if _, _, err := st.SaveProfile(ctx, pj); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	now := time.Now().UTC()
	const total = 5
	for i := 0; i < total; i++ {
		p := listingPosting("c"+string(rune('1'+i)), "신입 백엔드 개발자")
		p.Description = "서버 개발자를 찾습니다"
		p.FirstSeenAt, p.LastSeenAt = now, now
		if _, _, err := st.UpsertPosting(ctx, p); err != nil {
			t.Fatalf("UpsertPosting: %v", err)
		}
	}
	stub := rerateStub()
	runtime := testAIRuntime(1, stub, "test-model")
	runtime.RunTokenCap = 1_000_000 // generous: the per-call cap, not tokens, is the limiter
	runtime.PerCallCap = 2          // analyze at most 2 fresh rows per press
	if _, err := srv.scoreAll(ctx, 1, runtime); err != nil {
		t.Fatalf("scoreAll: %v", err)
	}

	// Press 1: 2 fresh rows analyzed, 3 left.
	summary, err := srv.runRerate(ctx, "today", noopEmit, 1, runtime)
	if err != nil {
		t.Fatalf("press 1: %v", err)
	}
	if summary.Visible != total {
		t.Fatalf("press 1 visible = %d, want %d", summary.Visible, total)
	}
	if summary.Analyzed != 2 || stub.ScoreDeltaCalls != 2 {
		t.Fatalf("press 1: analyzed=%d ScoreDelta=%d, want 2 and 2 (per-call cap)", summary.Analyzed, stub.ScoreDeltaCalls)
	}
	// Press 2: 2 cache hits (free) + 2 new = 4 analyzed cumulatively, 4 calls total.
	summary, err = srv.runRerate(ctx, "today", noopEmit, 1, runtime)
	if err != nil {
		t.Fatalf("press 2: %v", err)
	}
	if summary.Analyzed != 4 || stub.ScoreDeltaCalls != 4 {
		t.Fatalf("press 2: analyzed=%d ScoreDelta=%d, want 4 and 4", summary.Analyzed, stub.ScoreDeltaCalls)
	}
	// Press 3: the last row. All analyzed, no further presses needed.
	summary, err = srv.runRerate(ctx, "today", noopEmit, 1, runtime)
	if err != nil {
		t.Fatalf("press 3: %v", err)
	}
	if summary.Analyzed != total || stub.ScoreDeltaCalls != total {
		t.Fatalf("press 3: analyzed=%d ScoreDelta=%d, want %d and %d", summary.Analyzed, stub.ScoreDeltaCalls, total, total)
	}
}

// TestRerateInfoCountsCacheNotChips proves the N/M indicator is honest about a
// row that was analyzed but produced no surviving signal (R1): such a row has a
// cached delta but renders NO AI chip. The indicator counts the cache, so it
// reads "1/1", not "0/1" — resolving the "analyzed or just silent?" ambiguity.
func TestRerateInfoCountsCacheNotChips(t *testing.T) {
	srv, _ := newTestServer(t, &fakeScraper{})
	ctx := context.Background()
	zero := 0
	prof := profile.Profile{CareerYears: 0, MinScore: &zero, JobLikes: "백엔드 서버 개발"}
	pj, _ := profile.Marshal(prof)
	if _, _, err := srv.store.SaveProfile(ctx, pj); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	now := time.Now().UTC()
	p := listingPosting("e1", "신입 백엔드 개발자")
	p.Description = "서버 개발자를 찾습니다"
	p.FirstSeenAt, p.LastSeenAt = now, now
	id, _, err := srv.store.UpsertPosting(ctx, p)
	if err != nil {
		t.Fatalf("UpsertPosting: %v", err)
	}
	runtime := testAIRuntime(1, rerateStub(), "test-model")

	// An EMPTY current-goal delta: analyzed, but nothing survived the gate → no chip.
	hash := profile.AIInputHash(currentProfile(t, srv))
	if err := srv.store.UpsertAIScore(ctx, 1, id, hash, runtime.ScoreVersion, ai.Delta{}, now); err != nil {
		t.Fatalf("seed empty delta: %v", err)
	}
	if _, err := srv.scoreAll(ctx, 1, runtime); err != nil {
		t.Fatalf("scoreAll: %v", err)
	}

	b, err := srv.buildBriefing(ctx, time.Now())
	if err != nil {
		t.Fatalf("buildBriefing: %v", err)
	}
	b.Rerate = srv.buildRerateInfo(ctx, 1, runtime, prof, "today", b.Today)
	if b.Rerate == nil {
		t.Fatal("Rerate info nil; want analyzed/visible counts when AI is on")
	}
	if b.Rerate.Analyzed != 1 || b.Rerate.Visible != 1 {
		t.Fatalf("indicator = %d/%d, want 1/1 (the empty-delta cache row counts as analyzed)",
			b.Rerate.Analyzed, b.Rerate.Visible)
	}
}

func TestRerateDoneMessage(t *testing.T) {
	if got := rerateDoneMessage(rerateSummary{}); got != "지금 화면에 분석할 공고가 없어요." {
		t.Errorf("empty: %q", got)
	}
	if got := rerateDoneMessage(rerateSummary{Analyzed: 5, Visible: 5, ProviderCalls: 5}); got != "공고 5개를 모두 AI로 분석했어요." {
		t.Errorf("complete: %q", got)
	}
	got := rerateDoneMessage(rerateSummary{Analyzed: 3, Visible: 7, ProviderCalls: 3})
	if !strings.Contains(got, "3/7") || !strings.Contains(got, "다시 눌러") {
		t.Errorf("partial copy missing N/M or the press-again cue: %q", got)
	}
}

func currentProfile(t *testing.T, srv *Server) profile.Profile {
	t.Helper()
	j, _, ok, err := srv.store.Profile(context.Background())
	if err != nil || !ok {
		t.Fatalf("load profile: ok=%v err=%v", ok, err)
	}
	p, err := profile.Unmarshal(j)
	if err != nil {
		t.Fatalf("unmarshal profile: %v", err)
	}
	return p
}
