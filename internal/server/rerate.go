package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ohchanwu/jobcron/internal/ai"
	"github.com/ohchanwu/jobcron/internal/profile"
	"github.com/ohchanwu/jobcron/internal/scoring"
	"github.com/ohchanwu/jobcron/internal/scraper"
	"github.com/ohchanwu/jobcron/internal/storage"
)

// providerCallError marks a runRerate failure where every attempted row hit an
// AI provider error (a bad key, a model the provider rejects, a transport
// failure) — as opposed to a storage/profile failure. handleRerateSSE unwraps it
// to show a calm, provider-specific message instead of the generic one, so the
// user who just switched provider learns what to fix rather than seeing a hollow
// "0/N analyzed."
type providerCallError struct{ err error }

func (e *providerCallError) Error() string { return e.err.Error() }
func (e *providerCallError) Unwrap() error { return e.err }

// providerFailureMessage maps an AI provider failure to a calm Korean message.
// An *ai.APIError is classified by HTTP status — 401/403 reads as a key problem,
// 400/404 as a model/provider mismatch, 429 as a usage cap — so the BYOK user who
// just switched provider learns exactly what to fix. Transport/parse errors fall
// back to a generic retry line. Always non-empty.
func providerFailureMessage(err error) string {
	var apiErr *ai.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case http.StatusUnauthorized, http.StatusForbidden:
			return "AI 키를 확인해주세요 — 키가 올바르지 않거나 권한이 없어요."
		case http.StatusBadRequest, http.StatusNotFound:
			return "선택한 모델이 이 제공자와 맞지 않아요 — 설정에서 모델을 확인해주세요."
		case http.StatusTooManyRequests:
			// OpenAI overloads 429 for two very different situations. A persistent
			// billing/credit problem (insufficient_quota) must NOT read as "retry
			// later" — the user needs to fix billing, not wait. A transient
			// rate-limit does retry. Distinguish on the error body.
			if strings.Contains(apiErr.Body, "insufficient_quota") {
				return "AI 제공자 사용 한도를 초과했어요 — 제공자 계정의 결제·요금제를 확인해주세요."
			}
			return "요청이 잠시 몰렸어요 — 잠시 후 다시 시도해 주세요."
		}
		return fmt.Sprintf("AI 제공자가 오류를 반환했어요 (%d) — 설정을 확인해 주세요.", apiErr.Status)
	}
	return "AI 분석에 실패했어요 — 키와 모델 설정을 확인하거나 잠시 후 다시 시도해 주세요."
}

// rerateWorkers bounds how many visible rows a 재평가 press analyzes
// concurrently. The provider's own 1-req/s limiter (internal/ai) spaces
// request STARTS for politeness/backpressure; this pool overlaps the
// multi-second LLM latencies so the press finishes in ~visible seconds
// instead of visible×latency. ~4-6 in flight saturates the 1/s start rate
// without bursting past it.
const rerateWorkers = 6

// callCap bounds how many not-yet-cached rows a single 재평가 press spends a
// provider call on (the user's AIRuntime.PerCallCap), safely across the
// worker pool. tryReserve atomically claims a slot when one is free.
type callCap struct {
	mu  sync.Mutex
	n   int
	max int
}

func (c *callCap) tryReserve() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n >= c.max {
		return false
	}
	c.n++
	return true
}

func (s *Server) validateDealbreakers(
	ctx context.Context,
	userID int64,
	postings []scraper.Posting,
	prof profile.Profile,
	runtime *AIRuntime,
	budget *aiBudget,
	calls *callCap,
	emit func(event, data string),
) (providerCalls int, providerErr error) {
	if runtime == nil || runtime.UserID != userID || budget == nil || calls == nil || userID <= 0 || s.store.Dialect() != storage.DialectPostgres {
		return 0, nil
	}
	cached, err := s.store.AIDealbreakerValidationsByPostingID(ctx, userID, runtime.DealbreakerVersion)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	for _, p := range postings {
		modelText, contentHash, _ := ai.ModelInput(p)
		candidates := scoring.DealbreakerCandidates(p, prof)
		unresolved := candidates[:0]
		for _, candidate := range candidates {
			if _, ok := cached[p.ID][contentHash+"\x00"+candidate.ID]; !ok {
				unresolved = append(unresolved, candidate)
			}
		}
		if len(unresolved) == 0 || !budget.canSpend() || !calls.tryReserve() {
			continue
		}
		validations, usage, err := runtime.Provider.ValidateDealbreakers(ctx, modelText, unresolved)
		providerCalls++
		if usage.InputTokens+usage.OutputTokens > 0 {
			budget.debit(ctx, usage)
		}
		if err != nil {
			if providerErr == nil {
				providerErr = err
			}
			continue
		}
		for _, validation := range validations {
			if err := s.store.UpsertAIDealbreakerValidation(ctx, userID, p.ID, contentHash, runtime.DealbreakerVersion, validation.CandidateID, validation, now); err != nil {
				return providerCalls, err
			}
		}
		emit("progress", fmt.Sprintf("공고 %d 문맥 확인 중...", p.ID))
	}
	return providerCalls, providerErr
}

// rerateInfo is the per-surface re-rate button view model. A nil *rerateInfo
// means "no AI key configured" — the template renders no button at all (design
// §4: no dead control). StaleCount drives the gold attention treatment for
// stale Stage-2 rows and unresolved contextual dealbreakers;
// Analyzed/Visible drive the persistent "N/M 분석됨" progress indicator.
type rerateInfo struct {
	Surface    string // "today" | "bookmarks" | "archive"
	StaleCount int    // rows with stale Stage 2 or pending contextual validation
	Analyzed   int    // visible rows with a current-goal AI delta cached (N)
	Visible    int    // total visible, non-excluded rows on this surface (M)
}

// buildRerateInfo returns the re-rate button state for a surface, or nil when AI
// is off (so the button is hidden). Across the given posting lists it counts the
// visible, non-excluded rows (Visible), how many rows carry a stale AI line or
// lack a current contextual validation (StaleCount), and how many already have
// a delta cached against the CURRENT
// goal text (Analyzed). Analyzed reads the fresh ai_scores cache, NOT the chips —
// a row analyzed but with no surviving signal shows no chip yet is still counted,
// which is the whole point of the indicator (it resolves "analyzed or just
// silent?"). A cache read error degrades Analyzed to 0; it never blocks render.
func (s *Server) buildRerateInfo(ctx context.Context, userID int64, runtime *AIRuntime, prof profile.Profile, surface string, lists ...[]dashboardPosting) *rerateInfo {
	if runtime == nil || runtime.UserID != userID {
		return nil
	}
	fresh, err := s.store.AIScoresByPostingID(ctx, userID, profile.AIInputHash(prof), runtime.ScoreVersion)
	if err != nil {
		fresh = nil
	}
	var validations map[int64]map[string]storage.AIDealbreakerValidation
	if s.store.Dialect() == storage.DialectPostgres {
		validations, err = s.store.AIDealbreakerValidationsByPostingID(ctx, userID, runtime.DealbreakerVersion)
		if err != nil {
			validations = nil
		}
	}
	info := &rerateInfo{Surface: surface}
	for _, list := range lists {
		for _, dp := range list {
			_, contentHash, _ := ai.ModelInput(dp.Posting)
			pendingValidation := false
			if s.store.Dialect() == storage.DialectPostgres {
				for _, candidate := range scoring.DealbreakerCandidates(dp.Posting, prof) {
					if _, ok := validations[dp.Posting.ID][contentHash+"\x00"+candidate.ID]; !ok {
						pendingValidation = true
						break
					}
				}
			}
			if pendingValidation {
				info.StaleCount++
			}
			if dp.Excluded {
				continue
			}
			info.Visible++
			if _, ok := fresh[dp.Posting.ID]; ok {
				info.Analyzed++
			}
			for _, li := range dp.Breakdown {
				if li.Stale {
					if !pendingValidation {
						info.StaleCount++
					}
					break
				}
			}
		}
	}
	return info
}

// validRerateSurface reports whether surface is one of the three re-ratable
// pages. /hidden is intentionally excluded (re-rating muted rows wastes tokens).
func validRerateSurface(surface string) bool {
	switch surface {
	case "today", "bookmarks", "archive":
		return true
	}
	return false
}

type rerateSummary struct {
	Analyzed      int
	Visible       int
	ProviderCalls int
}

// handleRerateSSE re-rates the VISIBLE rows of one surface with the Stage-2 AI
// delta, streaming progress as Server-Sent Events. It is mutually exclusive with
// a scrape and with another re-rate (it shares the scrape singleflight key — S7),
// so the daily-budget read-modify-write can't race a concurrent AI run. A
// terminal event (done|failed) fires on EVERY exit path via defer (S8).
func (s *Server) handleRerateSSE(w http.ResponseWriter, r *http.Request) {
	surface := r.URL.Query().Get("surface")
	if !validRerateSurface(surface) {
		http.Error(w, "알 수 없는 화면이에요.", http.StatusBadRequest)
		return
	}
	userID, err := s.stateUserID(r.Context(), r)
	if err != nil {
		writeAuthUnauthorized(w)
		return
	}
	ownerEntry := r.URL.Query().Get("entry")
	if !validRerateEntryToken(ownerEntry) {
		http.Error(w, "올바르지 않은 화면 기록이에요.", http.StatusBadRequest)
		return
	}
	// Acquire before resolving the runtime. Profile saves share this key, so the
	// profile, credential, and immutable runtime stay one consistent snapshot for
	// the complete detached operation.
	lease := s.flight.tryAcquire(scrapeAllKey)
	if lease == nil {
		http.Error(w, "이미 작업이 진행 중이에요. 잠시만 기다려 주세요.", http.StatusConflict)
		return
	}
	defer lease.release()
	runtime, err := s.aiRuntimeForUser(r.Context(), userID)
	if err != nil || runtime == nil {
		// No provider configured — there is nothing to re-rate. The button is
		// hidden in this state; this guards a direct request. 503 (not 409): the
		// feature is unavailable in this configuration, not in conflict with
		// another in-flight operation (that's the 409 below).
		http.Error(w, "AI가 설정되지 않았어요.", http.StatusServiceUnavailable)
		return
	}
	sw, err := newSSEWriter(w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	run := s.rerates.start(userID, surface, ownerEntry)
	emit := func(event, data string) {
		s.rerates.record(userID, surface, run.RunID, event, data)
		sw.event(event, data)
	}
	emit("run", fmt.Sprintf("%d", run.RunID))
	emit("run-token", run.RunToken)
	// S8: emit a terminal event on every exit. done is set only on the success
	// path; any early return (error, panic recovery by the http server) leaves
	// done false, so the client always sees a terminal event and the htmx
	// sse-connect is torn down (no auto-reconnect into a second re-rate).
	done := false
	failMsg := "AI 평가에 실패했어요. 잠시 후 다시 시도해 주세요."
	defer func() {
		if !done {
			emit("failed", failMsg)
		}
	}()

	// Bug 2A (parallel to handleScrapeSSE): detach the re-rate from the request
	// context. runRerate ends with scoreAll, the only step that copies the
	// freshly-cached AI deltas into the scores table the dashboard renders; if
	// client navigation cancelled the request mid-run, that terminal scoreAll
	// would be skipped and the chips the user just paid for would not appear
	// until an unrelated scrape/save. The per-row commit-before-call invariant
	// (S8) already makes a longer-running detached re-rate safe to interrupt;
	// SSE writes after disconnect are no-ops.
	ctx, cancel := context.WithTimeout(context.Background(), scrapeMaxDuration)
	defer cancel()
	summary, err := s.runRerate(ctx, surface, emit, userID, runtime)
	if err != nil {
		// A provider failure (every attempted row errored) carries a calm,
		// specific message; a storage/profile failure keeps the generic one. The
		// defer emits whichever is set into the terminal "failed" event.
		var pce *providerCallError
		if errors.As(err, &pce) {
			failMsg = providerFailureMessage(pce.err)
		}
		return
	}
	done = true
	message := rerateDoneMessage(summary)
	s.rerates.complete(userID, surface, run.RunID, rerateDoneOutcome(summary), message)
	sw.event("done", message)
}

func rerateDoneOutcome(summary rerateSummary) rerateOutcome {
	switch {
	case summary.Visible == 0:
		return rerateOutcomeEmpty
	case summary.ProviderCalls == 0 && summary.Analyzed >= summary.Visible:
		return rerateOutcomeCached
	case summary.Analyzed < summary.Visible:
		return rerateOutcomePartial
	default:
		return rerateOutcomeChanged
	}
}

// rerateDoneMessage is the terminal copy after a re-rate press. It states the
// honest N/M progress and, when the press did not finish the list (the per-call
// cap or token budget stopped it), why and what to do — so a counter that did
// not reach M reads as "intentional, press again," not "broken."
func rerateDoneMessage(summary rerateSummary) string {
	switch {
	case summary.Visible == 0:
		return "지금 화면에 분석할 공고가 없어요."
	case summary.ProviderCalls == 0 && summary.Analyzed >= summary.Visible:
		return "이미 모든 공고가 AI로 평가됐습니다. 추가 토큰은 사용하지 않았어요."
	case summary.Analyzed >= summary.Visible:
		return fmt.Sprintf("공고 %d개를 모두 AI로 분석했어요.", summary.Visible)
	default:
		return fmt.Sprintf(
			"공고 %d/%d개를 AI로 분석했어요 — 토큰을 아끼려고 한 번에 일정 개수만 분석해요. 더 보려면 다시 눌러주세요.",
			summary.Analyzed, summary.Visible)
	}
}

// runRerate first backfills Stage 1A and validates deterministic Stage 1B
// dealbreaker candidates across every row on the surface, including excluded
// rows, then rebuilds the visible set and computes (or reuses) Stage 2 for each,
// commits each delta BEFORE moving on (so a reconnect resumes from cache with no
// double-spend — S8), then re-scores so the fresh deltas land in the briefing.
// It is bounded by the surface's visible rows, never the whole DB.
//
// The rows run through a bounded worker pool (rerateWorkers): the per-row LLM
// latencies overlap, cutting wall-time ~4×, while the provider's 1-req/s limiter
// still spaces request starts. The shared token budget and the per-call counter
// are mutex-guarded; each worker commits its own ai_scores row.
//
// One press analyzes at most runtime.PerCallCap NOT-yet-cached rows (a legibility
// knob so the spend per click is predictable), still capped by the hard token
// budgets. Cached rows are free cache hits and don't count against the per-call
// cap, so a later press resumes on the still-uncached rows. It returns the
// cumulative analyzed count (N — visible rows now cached against the current
// goal) and the total visible rows (M) for the progress copy.
func (s *Server) runRerate(ctx context.Context, surface string, emit func(event, data string), userID int64, runtime *AIRuntime) (summary rerateSummary, err error) {
	if runtime == nil || runtime.UserID != userID {
		return summary, fmt.Errorf("server: rerate requires matching AI runtime")
	}
	prof, ok, err := s.loadProfile(ctx, userID)
	if err != nil || !ok {
		return summary, err
	}
	budget := s.newAIBudget(ctx, userID, runtime)
	calls := &callCap{max: runtime.PerCallCap}
	now := time.Now()
	candidates, err := s.candidatePostingsForRerate(ctx, surface, now, userID, runtime)
	if err != nil {
		return summary, err
	}
	// Stage 1A must run before contextual validation and the first score merge:
	// an extraction can correct a conservative career/education exclusion and
	// return the posting to the visible set selected for Stage 2. Eligibility
	// and Stage 2 have independent cache identities, so only extractStage1's own
	// exact eligibility/content cache check can make this call free.
	for _, p := range candidates {
		s.extractStage1(ctx, p.ID, p, now, userID, runtime, budget)
	}
	if validationCalls, validationErr := s.validateDealbreakers(ctx, userID, candidates, prof, runtime, budget, calls, emit); validationErr != nil {
		summary.ProviderCalls += validationCalls
		emit("status", providerFailureMessage(validationErr))
	} else {
		summary.ProviderCalls += validationCalls
	}
	if _, err := s.scoreAll(ctx, userID, runtime); err != nil {
		return summary, err
	}
	postings, err := s.visibleForRerate(ctx, surface, now, userID, runtime)
	if err != nil {
		return summary, err
	}
	summary.Visible = len(postings)
	if summary.Visible == 0 {
		return summary, nil
	}
	emit("status", "AI로 다시 분석하는 중이에요 — 여러 공고를 한 번에 살펴보고 있어요. ☕")

	var provErr error
	var stage2Calls int
	summary.Analyzed, stage2Calls, provErr = s.rateStage2(ctx, postings, prof, userID, runtime, budget, calls, emit)
	summary.ProviderCalls += stage2Calls
	if budget != nil && budget.isDegraded() {
		emit("status", "오늘 AI 예산을 다 써서 일부는 다시 분석하지 못했어요 — 프로필 설정에서 한도를 바꿀 수 있어요.")
	}
	if provErr != nil && summary.Analyzed > 0 {
		// Partial: some rows rated, some hit a provider error. Note it before the
		// done path reloads (the rows that succeeded still render their chips).
		emit("status", providerFailureMessage(provErr))
	}
	emit("status", "점수를 다시 매기는 중...")
	if _, err := s.scoreAll(ctx, userID, runtime); err != nil {
		return summary, err
	}
	if summary.Analyzed == 0 && provErr != nil {
		// Every attempted row failed against the provider — surface it so the SSE
		// terminal is a calm, specific "failed" instead of a hollow "0/N done."
		return summary, &providerCallError{err: provErr}
	}
	return summary, nil
}

// rateStage2 runs the Stage-2 ScoreDelta over `postings` through the bounded
// worker pool (rerateWorkers), committing each delta before moving on, and
// returns the count now cached against the current goal. It is shared by the
// 재평가 handler (runRerate) and the scrape's end-of-run auto-rate pass
// (runScrape), so a fresh briefing shows its AI chips without a manual 재평가.
// The caller owns the budget — so a scrape and a 재평가 each scope their own run
// cap — and the scoreAll merge that follows. The runtime's per-call cap
// bounds how many not-yet-cached rows one pass spends on, exactly as a 재평가
// press does; cached rows are free.
//
// The per-row LLM latencies overlap (cutting wall-time ~4×); the provider's
// limiter still spaces request starts. SSE progress writes stay on this
// goroutine (a ResponseWriter is not safe for concurrent writes); workers send
// results to a fully-buffered channel that a closer goroutine ends.
func (s *Server) rateStage2(ctx context.Context, postings []scraper.Posting, prof profile.Profile, userID int64, runtime *AIRuntime, budget *aiBudget, calls *callCap, emit func(event, data string)) (analyzed int, providerCalls int, provErr error) {
	if runtime == nil || runtime.UserID != userID || budget == nil || calls == nil || len(postings) == 0 {
		return 0, 0, nil
	}
	aiInputHash := profile.AIInputHash(prof)
	profileText := profile.BuildStage2ProfileText(prof)
	now := time.Now().UTC()
	total := len(postings)

	type rerateResult struct {
		cached         bool
		providerCalled bool
		err            error
	}
	results := make(chan rerateResult, total)
	sem := make(chan struct{}, rerateWorkers)
	var wg sync.WaitGroup
	for _, p := range postings {
		wg.Add(1)
		go func(p scraper.Posting) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			cached, providerCalled, err := s.rerateOne(ctx, p, aiInputHash, profileText, now, userID, runtime, budget, calls)
			results <- rerateResult{cached: cached, providerCalled: providerCalled, err: err}
		}(p)
	}
	go func() { wg.Wait(); close(results) }()

	completed := 0
	for r := range results {
		completed++
		if r.cached {
			analyzed++
		}
		if r.providerCalled {
			providerCalls++
		}
		// Keep the FIRST provider error as representative — a bad key or mismatched
		// model fails every row identically, so one classified message is enough.
		if r.err != nil && provErr == nil {
			provErr = r.err
		}
		emit("progress", fmt.Sprintf("공고 %d/%d 분석 중...", completed, total))
	}
	return analyzed, providerCalls, provErr
}

// rerateOne re-rates a single posting and reports whether it now has a delta
// cached against the current goal (analyzed). It checks the Stage-2 cache FIRST
// (free, reconnect-safe): a hit means the posting already has a delta against
// the current goal, so it counts as analyzed without spending. Stage 1A backfill
// happens across the full candidate surface before rateStage2 begins. When uncached,
// the row is analyzed only if the token budget can spend AND the per-call cap
// (calls) still has a slot; otherwise it is left for a later press. It commits
// the ai_scores row before returning, so a dropped connection resumes from cache
// with no double-spend. Safe to call from several pool workers at once: the
// budget and the per-call counter are mutex-guarded, and the store runs in WAL
// mode with a busy timeout. A provider/budget failure leaves the row uncached
// (regex score) but still consumes the reserved per-call slot, so a burst of
// failing calls can't ignore the cap.
func (s *Server) rerateOne(
	ctx context.Context, p scraper.Posting, aiInputHash, profileText string, now time.Time, userID int64, runtime *AIRuntime, budget *aiBudget, calls *callCap,
) (cached bool, providerCalled bool, err error) {
	// Already rated against the current goal text → reuse (reconnect-safe, no
	// re-spend, free). An empty cached delta still counts as analyzed.
	if _, ok, e := s.store.AIScore(ctx, userID, p.ID, aiInputHash, runtime.ScoreVersion); e == nil && ok {
		return true, false, nil
	}
	// Uncached: spend only if the token budget has headroom AND the per-call cap
	// has a free slot. canSpend is checked first so a cap-only miss doesn't mark
	// the budget degraded. Either miss leaves the row uncached for a later press —
	// not an error (no provider call was attempted).
	if budget == nil || !budget.canSpend() {
		return false, false, nil
	}
	if !calls.tryReserve() {
		return false, false, nil
	}
	sent, _, _ := ai.ModelInput(p)
	raw, usage, err := runtime.Provider.ScoreDelta(ctx, sent, profileText)
	if err != nil {
		// Provider error (bad key, mismatched model, transport) → no delta. Return
		// it so rateStage2 can surface a calm, specific message instead of letting
		// the failure read as a silent "not analyzed." The reserved slot is spent.
		return false, true, err
	}
	budget.debit(ctx, usage)
	// Gate: presence against the SENT (truncated) text, absence against the FULL
	// Description (S5). Survivors net into the stored delta.
	delta := ai.GateDelta(raw, sent, p.Description)
	if err := s.store.UpsertAIScore(ctx, userID, p.ID, aiInputHash, runtime.ScoreVersion, delta, now); err != nil {
		return false, true, err
	}
	return true, true, nil
}

func (s *Server) candidatePostingsForRerate(ctx context.Context, surface string, now time.Time, userID int64, runtime *AIRuntime) ([]scraper.Posting, error) {
	switch surface {
	case "today":
		b, err := s.buildBriefingWithRuntime(ctx, now, userID, runtime)
		if err != nil {
			return nil, err
		}
		return postingsOfAll(b.Today, b.Excluded), nil
	case "bookmarks":
		v, err := s.buildBookmarksWithRuntime(ctx, now, userID, runtime)
		if err != nil {
			return nil, err
		}
		return postingsOfAll(v.Postings), nil
	case "archive":
		v, err := s.buildArchiveWithRuntime(ctx, now, userID, runtime)
		if err != nil {
			return nil, err
		}
		lists := make([][]dashboardPosting, 0, len(v.Days)+1)
		for _, day := range v.Days {
			lists = append(lists, day.Postings)
		}
		lists = append(lists, v.Excluded)
		return postingsOfAll(lists...), nil
	}
	return nil, fmt.Errorf("server: unknown rerate surface %q", surface)
}

func postingsOfAll(lists ...[]dashboardPosting) []scraper.Posting {
	var out []scraper.Posting
	for _, list := range lists {
		for _, dp := range list {
			out = append(out, dp.Posting)
		}
	}
	return out
}

// visibleForRerate returns the non-dealbreaker postings currently shown on a
// surface — the exact rows the user sees, never the whole DB. Each surface
// reuses its existing page builder so re-rate and render agree on "visible".
func (s *Server) visibleForRerate(ctx context.Context, surface string, now time.Time, userID int64, runtime *AIRuntime) ([]scraper.Posting, error) {
	switch surface {
	case "today":
		b, err := s.buildBriefingWithRuntime(ctx, now, userID, runtime)
		if err != nil {
			return nil, err
		}
		return postingsOf(b.Today), nil
	case "bookmarks":
		v, err := s.buildBookmarksWithRuntime(ctx, now, userID, runtime)
		if err != nil {
			return nil, err
		}
		return postingsOf(v.Postings), nil
	case "archive":
		v, err := s.buildArchiveWithRuntime(ctx, now, userID, runtime)
		if err != nil {
			return nil, err
		}
		var out []scraper.Posting
		for _, day := range v.Days {
			out = append(out, postingsOf(day.Postings)...)
		}
		return out, nil
	}
	return nil, fmt.Errorf("server: unknown rerate surface %q", surface)
}

// postingsOf returns the underlying postings of the non-excluded rows in a
// dashboard list. Dealbreaker rows (Excluded) are skipped — the AI is never run
// on a Total:-1 posting (S4).
func postingsOf(dps []dashboardPosting) []scraper.Posting {
	out := make([]scraper.Posting, 0, len(dps))
	for _, dp := range dps {
		if dp.Excluded {
			continue
		}
		out = append(out, dp.Posting)
	}
	return out
}
