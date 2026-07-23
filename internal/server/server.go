package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ohchanwu/jobcron/internal/ai"
	"github.com/ohchanwu/jobcron/internal/credential"
	"github.com/ohchanwu/jobcron/internal/profile"
	"github.com/ohchanwu/jobcron/internal/scoring"
	"github.com/ohchanwu/jobcron/internal/scraper"
	"github.com/ohchanwu/jobcron/internal/storage"
	"github.com/ohchanwu/jobcron/web"
)

// scrapeNewCap bounds how many new postings one scrape will detail-fetch — a
// defensive limit; the 점핏 신입 universe is well under it.
const scrapeNewCap = 50

// Edited-JD refresh (T7). The scrape detail-fetches NEW postings only; an
// already-seen posting whose JD the employer later edits never gets re-fetched,
// so its content_hash — and thus its cached Stage-1 extraction and score — stay
// frozen at first sight. To catch edits without re-fetching everything (cost +
// politeness) or nothing (the staleness), each scrape re-fetches up to
// detailRefreshCap already-seen postings PER SOURCE, choosing the ones whose
// detail is oldest, and only those at least detailRefreshMinAge stale (so a
// same-day re-scrape doesn't re-fetch what it just fetched). Oldest-first
// rotation guarantees every posting's detail is rechecked within a bounded
// number of scrapes; JD edits are infrequent, so a day-plus latency to catch one
// is fine for a calm daily briefing. For full-listing sources (데모데이 /
// Greenhouse / 그리팅, whose FetchDetail is a no-op) the re-fetch is free and
// still picks up the listing's current text; for 점핏 / 랠릿 it is a real
// 1-req/s detail fetch, bounded by the cap.
const (
	detailRefreshCap    = 10
	detailRefreshMinAge = 24 * time.Hour
)

// Sweep windows. Postings that have not been seen in any scrape for
// sweepStaleWindow, OR were first seen more than sweepOldWindow ago AND
// are not always_open, get hard-deleted at the end of every scrape.
// Bookmarked postings are exempt from both rules.
const (
	sweepStaleWindow = 3 * 24 * time.Hour
	sweepOldWindow   = 90 * 24 * time.Hour
)

// defaultRunTokenCap is the per-run AI token ceiling (input + output). A
// generous in-memory limit so one scrape or re-rate can't run away on cost.
// Exceeding it halts AI for the rest of the run and falls back to regex scoring.
//
// The ceiling is SOFT: a call is admitted while there is headroom and debited
// after it returns, so the last admitted call can overspend by up to its own
// cost before the next canSpend() halts. That's fine for a generous cap; the
// hard, persisted, cross-run enforcement is the ai_usage daily ledger below.
// (Renamed from maxRunInputTokensDefault — it counts input+output, not just
// input.)
const defaultRunTokenCap = 150_000

func aiRunTokenCapForUSDCents(cents int) int {
	return tokenCapForUSDCents(cents, profile.DefaultAIRunUSDCents, defaultRunTokenCap)
}

func aiDailyTokenCapForUSDCents(cents int) int {
	return tokenCapForUSDCents(cents, profile.DefaultAIDailyUSDCents, profile.DefaultDailyTokenCap)
}

func aiMonthlyTokenCapForUSDCents(cents int) int {
	return tokenCapForUSDCents(cents, profile.DefaultAIDailyUSDCents, profile.DefaultDailyTokenCap)
}

func tokenCapForUSDCents(cents, defaultCents, defaultTokens int) int {
	if cents <= 0 {
		cents = defaultCents
	}
	if defaultCents <= 0 {
		return defaultTokens
	}
	cap := (defaultTokens * cents) / defaultCents
	if cap < 1 {
		return 1
	}
	return cap
}

func minPositive(a, b int) int {
	if a <= 0 {
		return b
	}
	if b <= 0 || a < b {
		return a
	}
	return b
}

// Server wires storage, one or more scrapers, and the HTTP handlers together.
type Server struct {
	store                    *storage.Store
	sources                  []scraper.Scraper
	tmpl                     *template.Template
	flight                   *singleFlight
	rerates                  *rerateTracker
	csrfSecret               []byte
	loginLimiter             *loginRateLimiter
	loginIPLimiter           *loginRateLimiter
	signupLimiter            *loginRateLimiter
	accountMutationLimiter   *loginRateLimiter
	accountMutationIPLimiter *loginRateLimiter
	passwordWorkSlots        chan struct{}

	credentialCipher credential.Cipher
	newAIProvider    aiProviderFactory
	// afterRenderProfileSnapshot is a deterministic concurrency seam used by
	// render/profile-save regression tests. Production leaves it nil.
	afterRenderProfileSnapshot func()
	// afterRenderScoreSnapshot is a deterministic concurrency seam used by
	// render/profile-save regression tests. Production leaves it nil.
	afterRenderScoreSnapshot func()

	demoMode            bool   // read-only public demo mode
	productionMode      bool   // require owner login for protected HTTP routes
	localUserID         int64  // immutable positive owner for non-production PostgreSQL
	adminToken          string // optional safety token for operator GET mutators in demo mode
	proxySecret         string // optional shared secret that allows Caddy forwarded-client headers
	signupAccessCode    string
	stage1SponsorUserID int64
}

type aiProviderFactory func(provider, key, model string, rateLimit time.Duration) (ai.Provider, error)

// AIRuntime is immutable AI configuration resolved once for one user's
// operation. It has no standalone key field, but Provider owns the decrypted
// credential for the operation lifetime. The runtime must remain ephemeral and
// is never cached on Server.
type AIRuntime struct {
	UserID             int64
	Provider           ai.Provider
	EligibilityVersion string
	DealbreakerVersion string
	ScoreVersion       string
	RunTokenCap        int
	DailyTokenCap      int
	MonthlyTokenCap    int
	PerCallCap         int
}

// SetCredentialCipher installs the process-level credential envelope cipher.
// The cipher is immutable and must be configured before any AI operation.
func (s *Server) SetCredentialCipher(c credential.Cipher) { s.credentialCipher = c }

func (s *Server) aiRuntimeForUser(ctx context.Context, userID int64) (*AIRuntime, error) {
	if userID <= 0 {
		return nil, fmt.Errorf("server: AI runtime requires a positive user ID")
	}
	profileJSON, _, found, err := s.store.ProfileForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("server: load AI profile: %w", err)
	}
	if !found {
		return nil, nil
	}
	prof, err := profile.Unmarshal(profileJSON)
	if err != nil {
		return nil, fmt.Errorf("server: decode AI profile: %w", err)
	}
	if strings.TrimSpace(prof.AIProvider) == "" {
		return nil, nil
	}
	provider, err := credential.NormalizeProvider(prof.AIProvider)
	if err != nil {
		return nil, fmt.Errorf("server: normalize AI provider: %w", err)
	}
	encrypted, found, err := s.store.UserAICredential(ctx, userID, provider)
	if err != nil {
		return nil, fmt.Errorf("server: load AI credential: %w", err)
	}
	if !found {
		return nil, nil
	}
	if s.credentialCipher == nil {
		return nil, fmt.Errorf("server: credential cipher is not configured")
	}
	key, err := s.credentialCipher.Open(
		userID,
		provider,
		encrypted.Ciphertext,
		encrypted.Nonce,
		encrypted.EncryptionVersion,
	)
	if err != nil {
		return nil, fmt.Errorf("server: decrypt AI credential: %w", err)
	}
	model := strings.TrimSpace(prof.AIModel)
	if model == "" {
		model = ai.DefaultModel(provider)
	}
	factory := s.newAIProvider
	if factory == nil {
		factory = ai.New
	}
	aiProvider, err := factory(provider, key, model, ai.SuggestedRateLimit(provider))
	if err != nil {
		return nil, fmt.Errorf("server: construct AI provider: %w", err)
	}
	return &AIRuntime{
		UserID:             userID,
		Provider:           aiProvider,
		EligibilityVersion: ai.EligibilityVersion(provider, model),
		DealbreakerVersion: ai.DealbreakerVersion(provider, model),
		ScoreVersion:       ai.ScoreVersion(provider, model),
		RunTokenCap:        aiRunTokenCapForUSDCents(prof.EffectiveAIRunUSDCapCents()),
		DailyTokenCap:      minPositive(prof.EffectiveAIDailyTokenCap(), aiDailyTokenCapForUSDCents(prof.EffectiveAIDailyUSDCapCents())),
		MonthlyTokenCap:    aiMonthlyTokenCapForUSDCents(prof.EffectiveAIMonthlyUSDCapCents()),
		PerCallCap:         prof.EffectiveAIPerCallCap(),
	}, nil
}

// runtimeForRender resolves one runtime for a read-only page operation. A
// missing or unreadable credential hides AI controls while rule-based content
// remains available; paid operations surface resolution errors explicitly.
func (s *Server) runtimeForRender(ctx context.Context, userID int64) *AIRuntime {
	if userID <= 0 {
		return nil
	}
	runtime, err := s.aiRuntimeForUser(ctx, userID)
	if err != nil {
		return nil
	}
	return runtime
}

// SetDemoMode makes the HTTP surface read-only. Visitor bookmark/hide state is
// handled by browser localStorage in this mode; no request should mutate the DB.
func (s *Server) SetDemoMode(on bool) { s.demoMode = on }

// SetProductionMode requires cookie-session authentication for protected pages.
func (s *Server) SetProductionMode(on bool) { s.productionMode = on }

// SetSignupAccessCode installs the cohort signup gate before serving requests.
func (s *Server) SetSignupAccessCode(code string) { s.signupAccessCode = code }

// SetStage1SponsorUserID installs the account that funds global Stage-1 misses.
func (s *Server) SetStage1SponsorUserID(userID int64) { s.stage1SponsorUserID = userID }

// SetSessionSecret makes security tokens derive from the configured production
// SESSION_SECRET. New still creates a random development secret so tests and
// local non-production runs do not need configuration.
func (s *Server) SetSessionSecret(secret []byte) {
	if len(secret) == 0 {
		return
	}
	s.csrfSecret = append([]byte(nil), secret...)
}

// SetAdminToken sets the operator token accepted by /api/scrape in demo mode.
// An empty token means the endpoint is refused like every other mutator.
func (s *Server) SetAdminToken(token string) { s.adminToken = token }

// SetProxySecret allows the app to trust forwarded client-IP headers stamped
// by the configured reverse proxy. Leave empty unless the proxy injects the
// same secret and the app is not directly exposed to the public internet.
func (s *Server) SetProxySecret(secret string) { s.proxySecret = strings.TrimSpace(secret) }

func (s *Server) validAdminToken(r *http.Request) bool {
	if s.adminToken == "" {
		return false
	}
	got := r.URL.Query().Get("token")
	if got == "" {
		got = r.Header.Get("X-Jobcron-Admin-Token")
	}
	wantHash := sha256.Sum256([]byte(s.adminToken))
	gotHash := sha256.Sum256([]byte(got))
	return subtle.ConstantTimeCompare(wantHash[:], gotHash[:]) == 1
}

func optionalUserID(userIDOpt []int64) int64 {
	if len(userIDOpt) == 0 {
		return 0
	}
	return userIDOpt[0]
}

func (s *Server) profileJSON(ctx context.Context, userID int64) (string, string, bool, error) {
	if userID == 0 {
		return s.store.Profile(ctx)
	}
	return s.store.ProfileForUser(ctx, userID)
}

func (s *Server) saveProfileJSON(ctx context.Context, userID int64, canonical string) (string, bool, error) {
	if userID == 0 {
		return s.store.SaveProfile(ctx, canonical)
	}
	return s.store.SaveProfileForUser(ctx, userID, canonical)
}

func (s *Server) scoresByPostingID(ctx context.Context, userID int64) (map[int64]storage.Score, error) {
	if userID == 0 {
		return s.store.ScoresByPostingID(ctx)
	}
	return s.store.ScoresByPostingID(ctx, userID)
}

type renderProfileSnapshot struct {
	profile profile.Profile
	hash    string
	found   bool
}

// loadRenderProfileSnapshot captures the one profile JSON/hash pair a page
// render must use for both layout decisions and score freshness filtering.
func (s *Server) loadRenderProfileSnapshot(ctx context.Context, userID int64) (renderProfileSnapshot, error) {
	profileJSON, profileHash, found, err := s.profileJSON(ctx, userID)
	if err != nil {
		return renderProfileSnapshot{}, err
	}
	if !found {
		return renderProfileSnapshot{}, nil
	}
	prof, err := profile.Unmarshal(profileJSON)
	if err != nil {
		return renderProfileSnapshot{}, fmt.Errorf("server: decode profile: %w", err)
	}
	snapshot := renderProfileSnapshot{profile: prof, hash: profileHash, found: true}
	if s.afterRenderProfileSnapshot != nil {
		s.afterRenderProfileSnapshot()
	}
	return snapshot, nil
}

// scoresForRenderSnapshot is the render boundary: a committed profile may be
// newer than its score rows when best-effort rescoring fails. Never render a
// score computed for another profile hash, and never re-read the profile after
// this check because that could pair old scores with a newer profile.
func (s *Server) scoresForRenderSnapshot(ctx context.Context, userID int64, snapshot renderProfileSnapshot) (map[int64]storage.Score, error) {
	scores, err := s.scoresByPostingID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !snapshot.found {
		return map[int64]storage.Score{}, nil
	}
	for postingID, score := range scores {
		if score.ProfileHash != snapshot.hash {
			delete(scores, postingID)
		}
	}
	if s.afterRenderScoreSnapshot != nil {
		s.afterRenderScoreSnapshot()
	}
	return scores, nil
}

func (s *Server) bookmarkedIDs(ctx context.Context, userID int64) (map[int64]bool, error) {
	if userID == 0 {
		return s.store.BookmarkedIDs(ctx)
	}
	return s.store.BookmarkedIDsForUser(ctx, userID)
}

func (s *Server) notInterestedIDs(ctx context.Context, userID int64) (map[int64]bool, error) {
	if userID == 0 {
		return s.store.NotInterestedIDs(ctx)
	}
	return s.store.NotInterestedIDs(ctx, userID)
}

// New builds a Server over the given storage and one or more scrapers. The
// scrape pipeline iterates sources in the order they are registered, so the
// most-trusted source should come first. It parses the embedded HTML
// templates, panicking on a malformed template (a developer error caught at
// startup).
func New(store *storage.Store, sources ...scraper.Scraper) *Server {
	return newServer(store, 0, sources...)
}

// NewForLocalUser builds a non-production PostgreSQL server with a verified
// positive no-login user.
func NewForLocalUser(store *storage.Store, userID int64, sources ...scraper.Scraper) *Server {
	if userID <= 0 {
		panic("server.NewForLocalUser: user ID must be positive")
	}
	return newServer(store, userID, sources...)
}

func newServer(store *storage.Store, localUserID int64, sources ...scraper.Scraper) *Server {
	if len(sources) == 0 {
		panic("server.New: at least one scraper is required")
	}
	srv := &Server{
		store:                    store,
		sources:                  sources,
		flight:                   newSingleFlight(),
		rerates:                  newRerateTracker(),
		csrfSecret:               newCSRFSecret(),
		loginLimiter:             newLoginRateLimiter(),
		loginIPLimiter:           newLoginRateLimiter(),
		signupLimiter:            newLoginRateLimiter(),
		accountMutationLimiter:   newLoginRateLimiter(),
		accountMutationIPLimiter: newLoginRateLimiter(),
		passwordWorkSlots:        make(chan struct{}, passwordWorkConcurrency),
		newAIProvider:            ai.New,
		localUserID:              localUserID,
	}
	funcs := template.FuncMap{
		"sourceLabel":       sourceLabel,
		"registeredSources": srv.allRegisteredSources,
		"sourcePillGroups":  srv.sourcePillGroups,
		"absInt":            absInt,
		"usdCents":          usdCents,
		"demoMode":          func() bool { return srv.demoMode },
		"productionMode":    func() bool { return srv.productionMode },
		"navData":           func(active, csrfToken string) navView { return navView{Active: active, CSRFToken: csrfToken} },
	}
	srv.tmpl = template.Must(template.New("").Funcs(funcs).ParseFS(web.FS, "*.html"))
	return srv
}

// ScrapeResult summarizes one scrape run across every active source.
type ScrapeResult = storage.ScrapeResult

// scrapeAllKey is the singleflight key for a multi-source scrape run. We
// hold one global lock for the whole pipeline rather than one per source —
// the user clicks one button, sees one progress stream, and would be
// confused by partial states. Per-source locks would matter if scrapes were
// triggered independently.
const scrapeAllKey = "_all_"

func (s *Server) runScrapeWithHistory(ctx context.Context, trigger string, emit func(event, data string), userID int64, runtime *AIRuntime) (result ScrapeResult, err error) {
	run, startErr := s.store.StartScrapeRun(ctx, trigger)
	if startErr != nil {
		return ScrapeResult{}, startErr
	}
	status := storage.ScrapeRunStatusSuccess
	errorSummary := ""
	defer func() {
		if r := recover(); r != nil {
			status = storage.ScrapeRunStatusFailure
			err = fmt.Errorf("server: scrape panic: %v", r)
			errorSummary = err.Error()
		} else if err != nil {
			status = storage.ScrapeRunStatusFailure
			errorSummary = err.Error()
		}
		finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if finishErr := s.store.FinishScrapeRun(finishCtx, run.ID, result, status, truncateScrapeRunError(errorSummary)); finishErr != nil && err == nil {
			err = finishErr
		}
	}()
	return s.runScrapeForTrigger(ctx, trigger, emit, userID, runtime)
}

func truncateScrapeRunError(s string) string {
	const max = 500
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// runScrape executes the full pipeline across every enabled source: robots
// check → listing → detail fetch → upsert → sweep → score. Disabled sources
// are skipped entirely and their data is frozen in the DB so re-enabling a
// source does not require a fresh scrape.
func (s *Server) runScrape(ctx context.Context, emit func(event, data string), userID int64, runtime *AIRuntime) (ScrapeResult, error) {
	return s.runScrapeForTrigger(ctx, storage.ScrapeTriggerManual, emit, userID, runtime)
}

func (s *Server) runScrapeForTrigger(ctx context.Context, trigger string, emit func(event, data string), userID int64, runtime *AIRuntime) (ScrapeResult, error) {
	prof, profileOK, err := s.loadProfile(ctx, userID)
	if err != nil {
		return ScrapeResult{}, err
	}

	var active []scraper.Scraper
	for _, src := range s.sources {
		if !profileOK || prof.SourceEnabled(src.Source()) {
			active = append(active, src)
		}
	}
	if len(active) == 0 {
		emit("status", "활성화된 공고 출처가 없어요 — 프로필 설정에서 켜주세요.")
		return ScrapeResult{}, nil
	}

	// Set the expectation up front: the per-source clients pace at 1 req/s
	// so a 50-posting first scrape takes ~a minute. Without this line a
	// user staring at the progress counter might think the tool is hung.
	emit("status", "천천히 가져올게요 — 출처 사이트에 부담을 주지 않으려고 1초에 하나씩 받아와요. ☕")

	now := time.Now().UTC()
	var res ScrapeResult
	// One AI token budget for the whole run (persists across sources). nil
	// when AI is off, so no per-posting budget bookkeeping happens at all.
	freshAI := freshAIAllowedForTrigger(trigger, profileOK, prof, runtime)
	budget := s.newAIBudget(ctx, userID, runtime)
	calls := &callCap{}
	if runtime != nil {
		calls.max = runtime.PerCallCap
	}
	if !freshAI {
		budget = nil
	}
	// succeeded tracks sources that completed without error this run; only
	// they get their data swept. A source that failed cannot tell us what
	// is stale (no fresh baseline this run), so we leave its existing rows
	// untouched until the next successful scrape.
	var succeeded []scraper.Scraper
	var detailedPostings []scraper.Posting
	for _, src := range active {
		sub, detailed, err := s.runScrapeSource(ctx, src, now, userID, runtime, budget, emit)
		if err != nil {
			// Per-source fault isolation: one source's failure must not
			// abort the whole briefing. Surface the failure as a status
			// line and move on; the user still gets a working briefing
			// from every source that did succeed.
			emit("status", fmt.Sprintf("[%s] 스크랩에 실패했어요 — 다른 출처를 계속할게요.", sourceLabel(src.Source())))
			res.Failed++
			continue
		}
		detailedPostings = append(detailedPostings, detailed...)
		succeeded = append(succeeded, src)
		res.Listed += sub.Listed
		res.New += sub.New
		res.Refreshed += sub.Refreshed
	}

	activeIDs := make([]string, 0, len(succeeded))
	for _, src := range succeeded {
		activeIDs = append(activeIDs, src.Source())
	}
	removed, err := s.store.SweepStalePostings(ctx, now, sweepStaleWindow, sweepOldWindow, activeIDs)
	if err != nil {
		return res, fmt.Errorf("server: sweep stale postings: %w", err)
	}
	res.Removed = removed
	if removed > 0 {
		emit("status", fmt.Sprintf("오래된 공고 %d개를 정리했어요", removed))
	}

	duplicates, err := s.markCrossPortalDuplicates(ctx)
	if err != nil {
		return res, fmt.Errorf("server: dedup pass: %w", err)
	}
	res.Duplicates = duplicates
	if duplicates > 0 {
		emit("status", fmt.Sprintf("다른 사이트에 똑같이 올라온 공고 %d개를 묶었어요", duplicates))
	}
	if freshAI && len(detailedPostings) > 0 {
		emit("status", "제외 조건의 문맥을 확인하는 중...")
		_, providerErr := s.validateDealbreakers(ctx, userID, detailedPostings, prof, runtime, budget, calls, emit)
		if providerErr != nil {
			emit("status", providerFailureMessage(providerErr))
		}
	}

	// Cold-start banner (D6): if the per-run AI budget ran out mid-scrape during
	// Stage-1 extraction, some postings were scored by regex instead of AI. A
	// mixed briefing is fine — surface it calmly, never as a failure.
	if budget != nil && budget.isDegraded() {
		emit("status", "오늘 AI 예산을 다 써서 일부는 일반 점수로 분석했어요 — 프로필 설정에서 한도를 바꿀 수 있어요.")
	}

	emit("status", "공고에 점수를 매기는 중...")
	scored, err := s.scoreAll(ctx, userID, runtime)
	if err != nil {
		return res, err
	}
	res.Scored = scored

	// Auto-rate the fresh briefing with Stage-2 so new postings show their
	// evidence-cited AI chip without a manual 재평가. Runs AFTER the offline
	// scoreAll (so "visible" reflects real scores), over the today surface only,
	// through the same worker pool as 재평가. Stage 1A, Stage 1B, and Stage 2
	// share this scrape's token budget; Stage 1B and Stage 2 also share the
	// runtime.PerCallCap counter so contextual validation cannot bypass it.
	if freshAI {
		if vis, verr := s.visibleForRerate(ctx, "today", now, userID, runtime); verr == nil && len(vis) > 0 {
			emit("status", "새 공고를 AI로 분석하는 중...")
			rated, _, provErr := s.rateStage2(ctx, vis, prof, userID, runtime, budget, calls, emit)
			if rated > 0 {
				// Merge the fresh Stage-2 deltas into the rendered scores.
				if rescored, rerr := s.scoreAll(ctx, userID, runtime); rerr == nil {
					res.Scored = rescored
				}
			}
			if provErr != nil {
				// The auto-rate hit a provider error (bad key/model after a switch).
				// Surface it calmly inline — the scrape itself still succeeds.
				emit("status", providerFailureMessage(provErr))
			}
			if budget != nil && budget.isDegraded() {
				emit("status", "오늘 AI 예산을 다 써서 일부 공고는 아직 분석하지 못했어요 — 프로필 설정에서 한도를 바꿀 수 있어요.")
			}
		}
	}
	return res, nil
}

func freshAIAllowedForTrigger(trigger string, profileOK bool, prof profile.Profile, runtime *AIRuntime) bool {
	if runtime == nil || !profileOK {
		return false
	}
	if trigger == storage.ScrapeTriggerScheduled {
		return prof.ScheduledAIEnabled
	}
	return true
}

// aiBudget gates AI spending for one run against two ceilings: a per-run,
// in-memory cap (runCap) and a rolling daily cap (dailyCap) enforced against the
// persisted ai_usage ledger. The daily total at the run's START is read once
// (dailyAtStart); each admitted call's tokens are added to runSpent AND written
// through to the ledger, so the cap holds across process restarts and across
// runs in the same day. (Within a single day, concurrent AI runs are kept from
// double-spending the daily budget by the scrape⟷re-rate exclusion — T7.)
type aiBudget struct {
	store          *storage.Store
	userID         int64
	day            string // UTC date, e.g. "2026-06-03"
	month          string // UTC month, e.g. "2026-06"
	runCap         int
	dailyCap       int
	monthlyCap     int
	dailyAtStart   int // ledger total (input+output) when the run began
	monthlyAtStart int // ledger total for the UTC month when the run began

	// mu guards the mutable counters so the concurrent 재평가 worker pool
	// (handleRerateSSE) can canSpend/debit from several goroutines without a
	// data race. The scrape path is sequential, so the lock is uncontended
	// there. Optimistic overshoot is bounded: workers that pass canSpend
	// before an in-flight call debits can collectively exceed the cap by at
	// most (pool size) calls — acceptable for a soft token ceiling.
	mu       sync.Mutex
	runSpent int  // input+output debited this run
	degraded bool // true once either cap was hit mid-run
}

// newAIBudget returns a fresh per-run budget, or nil when AI is off (so the
// scrape loop skips all AI bookkeeping). It reads the day's ledger total once so
// the daily cap accounts for spend from earlier runs (and earlier process
// lifetimes) the same day.
func (s *Server) newAIBudget(ctx context.Context, userID int64, runtime *AIRuntime) *aiBudget {
	if runtime == nil || userID <= 0 || runtime.UserID != userID {
		return nil
	}
	now := time.Now().UTC()
	day := now.Format("2006-01-02")
	month := now.Format("2006-01")
	in, out, err := s.store.AIUsageForDay(ctx, userID, day)
	if err != nil {
		in, out = 0, 0 // a ledger read error must not block scoring — start from 0
	}
	monthIn, monthOut, err := s.store.AIUsageForMonth(ctx, userID, month)
	if err != nil {
		monthIn, monthOut = 0, 0
	}
	return &aiBudget{
		store:          s.store,
		userID:         userID,
		day:            day,
		month:          month,
		runCap:         runtime.RunTokenCap,
		dailyCap:       runtime.DailyTokenCap,
		monthlyCap:     runtime.MonthlyTokenCap,
		dailyAtStart:   in + out,
		monthlyAtStart: monthIn + monthOut,
	}
}

// canSpend reports whether either cap still has headroom, marking the run
// degraded the first time it does not (so the caller surfaces the calm banner).
func (b *aiBudget) canSpend() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.runSpent >= b.runCap ||
		b.dailyAtStart+b.runSpent >= b.dailyCap ||
		b.monthlyAtStart+b.runSpent >= b.monthlyCap {
		b.degraded = true
		return false
	}
	return true
}

// debit records a call's token usage: against the in-memory run counter and
// (best-effort) through to the persisted daily ledger. A ledger write failure is
// swallowed — it must never fail a scrape — but the in-memory run cap still
// holds for the rest of this run. The ledger write happens outside the lock so a
// slow disk write does not serialize the worker pool's budget checks.
func (b *aiBudget) debit(ctx context.Context, u ai.Usage) {
	b.mu.Lock()
	b.runSpent += u.InputTokens + u.OutputTokens
	b.mu.Unlock()
	_ = b.store.AddAIUsage(ctx, b.userID, b.day, u.InputTokens, u.OutputTokens)
}

// isDegraded reports whether either cap was hit during the run.
func (b *aiBudget) isDegraded() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.degraded
}

// runScrapeSource scrapes one source, emitting source-prefixed status events
// so the user can tell which source is currently active in the stream.
func (s *Server) runScrapeSource(
	ctx context.Context, src scraper.Scraper, now time.Time, userID int64, runtime *AIRuntime, budget *aiBudget, emit func(event, data string),
) (ScrapeResult, []scraper.Posting, error) {
	label := sourceLabel(src.Source())
	emit("status", fmt.Sprintf("[%s] robots.txt 확인 중...", label))
	if err := src.CheckAccess(ctx); err != nil {
		return ScrapeResult{}, nil, fmt.Errorf("server: %s access denied: %w", src.Source(), err)
	}
	emit("status", fmt.Sprintf("[%s] ✓ 허용됐어요 — 공고 목록을 가져오는 중...", label))
	listing, err := src.FetchListing(ctx, 0)
	if err != nil {
		return ScrapeResult{}, nil, fmt.Errorf("server: %s fetch listing: %w", src.Source(), err)
	}
	seen, err := s.store.SeenDetail(ctx, src.Source())
	if err != nil {
		return ScrapeResult{}, nil, err
	}
	res := ScrapeResult{Listed: len(listing)}
	var detailedPostings []scraper.Posting

	// refreshCand is an already-seen posting eligible for an edited-JD re-fetch:
	// the listing posting (carries the id/url FetchDetail needs) plus its stored
	// row id and how stale its detail is.
	type refreshCand struct {
		p     scraper.Posting
		id    int64
		detAt time.Time
	}
	var fresh []scraper.Posting
	var cands []refreshCand
	staleBefore := now.Add(-detailRefreshMinAge)
	for _, p := range listing {
		if info, ok := seen[p.SourcePostingID]; ok {
			p.LastSeenAt = now
			if _, _, err := s.store.UpsertPosting(ctx, p); err != nil {
				return res, detailedPostings, fmt.Errorf("server: refresh seen posting: %w", err)
			}
			if info.DetailFetchedAt.Before(staleBefore) {
				cands = append(cands, refreshCand{p: p, id: info.ID, detAt: info.DetailFetchedAt})
			}
			continue
		}
		fresh = append(fresh, p)
	}
	if len(fresh) > scrapeNewCap {
		fresh = fresh[:scrapeNewCap]
	}
	emit("count", fmt.Sprintf("[%s] ✓ 새로운 공고 %d개를 찾았어요", label, len(fresh)))

	for _, p := range fresh {
		detailed, err := src.FetchDetail(ctx, p)
		if err != nil {
			continue
		}
		detailed.FirstSeenAt = now
		detailed.LastSeenAt = now
		id, _, err := s.store.UpsertPosting(ctx, detailed)
		if err != nil {
			return res, detailedPostings, fmt.Errorf("server: insert new posting: %w", err)
		}
		detailed.ID = id
		detailedPostings = append(detailedPostings, detailed)
		res.New++
		emit("progress", fmt.Sprintf("[%s] 공고 %d/%d 가져오는 중...", label, res.New, len(fresh)))
		// Stage-1 AI extraction (cache-read, D2). Best-effort: any failure
		// leaves no ai_extractions row and the posting is scored by the regex
		// path exactly as v1.5 — the scrape never fails on an AI error.
		s.extractStage1(ctx, id, detailed, now, userID, runtime, budget)
	}

	// Edited-JD refresh (T7): re-fetch the detail of the stalest already-seen
	// postings (oldest detail_fetched_at first, capped per source). A changed JD
	// flows new content_hash → fresh Stage-1 extraction → re-score; an unchanged
	// JD is a cheap no-op (content_hash matches, extraction cache hits). The
	// per-source cap bounds the added 1-req/s detail fetches for 점핏/랠릿; it is
	// free for the no-op-FetchDetail sources.
	sort.Slice(cands, func(i, j int) bool { return cands[i].detAt.Before(cands[j].detAt) })
	if len(cands) > detailRefreshCap {
		cands = cands[:detailRefreshCap]
	}
	for i, c := range cands {
		detailed, err := src.FetchDetail(ctx, c.p)
		if err != nil {
			continue // transient detail failure — try again a later scrape
		}
		detailed.LastSeenAt = now
		if err := s.store.RefreshPostingDetail(ctx, c.id, detailed, now); err != nil {
			return res, detailedPostings, fmt.Errorf("server: refresh posting detail: %w", err)
		}
		detailed.ID = c.id
		detailedPostings = append(detailedPostings, detailed)
		res.Refreshed++
		emit("progress", fmt.Sprintf("[%s] 기존 공고 새로고침 %d/%d...", label, i+1, len(cands)))
		s.extractStage1(ctx, c.id, detailed, now, userID, runtime, budget)
	}
	return res, detailedPostings, nil
}

// extractStage1 runs and caches the Stage-1 AI extraction for one new posting,
// if AI is enabled and the per-run budget has headroom. It writes only the
// ai_extractions cache row — the postings columns stay a faithful source
// mirror (D2). Every failure path is silent (regex fallback at score time).
func (s *Server) extractStage1(ctx context.Context, id int64, p scraper.Posting, now time.Time, userID int64, runtime *AIRuntime, budget *aiBudget) {
	if runtime == nil || runtime.UserID != userID || budget == nil || !budget.canSpend() {
		return
	}
	sent, contentHash, _ := ai.ModelInput(p)
	// Cache hit (same content already extracted under this ai_version): reuse,
	// no provider call. New postings always miss; this matters for the T7
	// re-rate backfill and idempotent re-runs.
	if _, ok, err := s.store.AIExtraction(ctx, id, contentHash, runtime.EligibilityVersion); err == nil && ok {
		return
	}
	ext, usage, err := runtime.Provider.Extract(ctx, sent)
	if err != nil {
		return // timeout / 5xx / malformed JSON / out-of-range → regex fallback
	}
	budget.debit(ctx, usage)
	// Best-effort cache write; a failure here just means a regex score this pass.
	_ = s.store.UpsertAIExtraction(ctx, id, contentHash, runtime.EligibilityVersion, ext, now)
}

// loadProfile fetches the saved profile, returning ok=false when none has
// been saved yet. Returning ok=false instead of an error keeps the scrape
// pipeline from blowing up before the user even has a chance to set up.
func (s *Server) loadProfile(ctx context.Context, userIDOpt ...int64) (profile.Profile, bool, error) {
	userID := optionalUserID(userIDOpt)
	jsonStr, _, ok, err := s.profileJSON(ctx, userID)
	if err != nil {
		return profile.Profile{}, false, err
	}
	if !ok {
		return profile.Profile{}, false, nil
	}
	p, err := profile.Unmarshal(jsonStr)
	if err != nil {
		return profile.Profile{}, false, fmt.Errorf("server: decode profile: %w", err)
	}
	return p, true, nil
}

// RescoreAll re-scores every stored posting against the supplied user's current
// profile. RescoreSoleOwner is the exact-owner startup entry point, resolving
// that owner and runtime before delegating to scoreAll, so a posting left
// unscored by an INTERRUPTED scrape is healed on the next boot rather than
// rendering as a blank card. Fix 2A keeps CLIENT navigation from leaving the
// unscored state; this covers PROCESS death — a crash / SIGKILL / OOM / deploy
// restart in the window between UpsertPosting committing a new row and the
// end-of-run scoreAll, which nothing else self-heals. It is the exported entry
// point to scoreAll; it never calls the AI provider (merge-only, D10), so the
// startup pass is a cheap cache-only re-score. No-op when no profile is saved.
func (s *Server) RescoreAll(ctx context.Context, userID int64, runtime *AIRuntime) (int, error) {
	return s.scoreAll(ctx, userID, runtime)
}

// RescoreSoleOwner heals cached scores at startup only when ownership is exact.
// Zero users is a clean no-op; multiple users returns a stable operator error.
func (s *Server) RescoreSoleOwner(ctx context.Context) (int, error) {
	if s.store.Dialect() == storage.DialectSQLite {
		return s.scoreAll(ctx, 0, nil)
	}
	userID, ok, err := s.store.SoleOwnerUserID(ctx)
	if err != nil || !ok {
		return 0, err
	}
	runtime, err := s.aiRuntimeForUser(ctx, userID)
	if err != nil {
		scored, scoreErr := s.scoreAll(ctx, userID, nil)
		return scored, errors.Join(err, scoreErr)
	}
	return s.scoreAll(ctx, userID, runtime)
}

// scoreAll scores every stored posting against the current profile and upserts
// the score rows. It is a no-op when no profile has been saved yet.
func (s *Server) scoreAll(ctx context.Context, userID int64, runtime *AIRuntime) (int, error) {
	profJSON, profHash, ok, err := s.profileJSON(ctx, userID)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, nil
	}
	prof, err := profile.Unmarshal(profJSON)
	if err != nil {
		return 0, fmt.Errorf("server: decode profile: %w", err)
	}
	postings, err := s.store.AllPostings(ctx)
	if err != nil {
		return 0, err
	}
	// Batch-load the Stage-1 extractions and Stage-2 deltas once (no N+1) when
	// AI is enabled; scoreCareer/educationDealbreaker prefer the extractions,
	// and the deltas merge as the "AI 분석" line item. This is merge-only — D10:
	// scoreAll NEVER calls the provider; paid Stage 1A/1B work runs at scrape
	// time, while a 재평가 may run Stage 1B followed by Stage 2.
	var (
		exts                   map[int64]ai.Extraction
		dealbreakerValidations map[int64]map[string]storage.AIDealbreakerValidation
		freshDeltas            map[int64]ai.Delta // keyed by the CURRENT goal text (ai_input_hash) + current ai_version
		latestDeltas           map[int64]ai.Delta // newest per posting under the current ai_version, any goal text — stale fallback for a goal edit
		anyVerDeltas           map[int64]ai.Delta // newest per posting across ANY ai_version — stale fallback for a provider/model switch
	)
	if runtime != nil {
		if userID <= 0 || runtime.UserID != userID {
			return 0, fmt.Errorf("server: AI runtime user mismatch")
		}
		exts, err = s.store.AIExtractionsByPostingID(ctx, runtime.EligibilityVersion)
		if err != nil {
			return 0, err
		}
		if s.store.Dialect() == storage.DialectPostgres {
			dealbreakerValidations, err = s.store.AIDealbreakerValidationsByPostingID(ctx, userID, runtime.DealbreakerVersion)
			if err != nil {
				return 0, err
			}
		}
		aiInputHash := profile.AIInputHash(prof)
		freshDeltas, err = s.store.AIScoresByPostingID(ctx, userID, aiInputHash, runtime.ScoreVersion)
		if err != nil {
			return 0, err
		}
		latestDeltas, err = s.store.LatestAIScoresByPostingID(ctx, userID, runtime.ScoreVersion)
		if err != nil {
			return 0, err
		}
		// Cross-version fallback: when the user switches provider/model, ai_version
		// rotates and orphans every prior row from the two version-scoped lookups
		// above. Without this, the AI chip would VANISH on a provider switch; with
		// it, the latest prior reading persists faded ("이전 설정 기준") until a
		// 재평가 refreshes it under the new provider/model.
		anyVerDeltas, err = s.store.LatestAIScoresAnyVersionByPostingID(ctx, userID)
		if err != nil {
			return 0, err
		}
	} else if s.demoMode && s.store.Dialect() == storage.DialectSQLite {
		// The public demo runs without a provider runtime, but the uploaded database
		// already contains tonight's cached Stage-2 deltas. Merge the newest
		// cached row as current so the first server boot does not erase the
		// visible AI chips.
		// Demo SQLite preserves its non-secret cache for rendering without
		// resolving or reading the legacy key file.
		anyVerDeltas, err = s.store.LatestAIScoresAnyVersionByPostingID(ctx, 1)
		if err != nil {
			return 0, err
		}
	}
	for _, p := range postings {
		var ext *ai.Extraction
		if e, ok := exts[p.ID]; ok {
			ext = &e
		}
		// Prefer a delta computed against the CURRENT goal text + provider/model
		// (fresh). Else fall back, marked Stale (still summed into the Total — T1),
		// so the chip reads "(이전 설정 기준)": first to the latest row under the
		// current ai_version (a goal edit), then to the latest row under ANY
		// ai_version (a provider/model switch, which rotated ai_version and orphaned
		// the prior rows). The cross-version tier is what keeps the chip from
		// vanishing when the user changes provider.
		var delta *ai.Delta
		if d, ok := freshDeltas[p.ID]; ok {
			d.Stale = false
			delta = &d
		} else if d, ok := latestDeltas[p.ID]; ok {
			d.Stale = true
			delta = &d
		} else if d, ok := anyVerDeltas[p.ID]; ok {
			d.Stale = !s.demoMode
			delta = &d
		}
		var validations map[string]ai.DealbreakerValidation
		if cached := dealbreakerValidations[p.ID]; len(cached) > 0 {
			_, contentHash, _ := ai.ModelInput(p)
			validations = make(map[string]ai.DealbreakerValidation)
			for _, candidate := range scoring.DealbreakerCandidates(p, prof) {
				if row, ok := cached[contentHash+"\x00"+candidate.ID]; ok {
					validations[candidate.ID] = row.Validation
				}
			}
		}
		result := scoring.Score(p, prof, ext, delta, validations)
		breakdown, err := json.Marshal(result)
		if err != nil {
			return 0, fmt.Errorf("server: marshal score: %w", err)
		}
		sc := storage.Score{
			PostingID:     p.ID,
			ProfileHash:   profHash,
			Total:         result.Total,
			BreakdownJSON: string(breakdown),
			ComputedAt:    time.Now().UTC(),
		}
		if userID == 0 {
			err = s.store.UpsertScore(ctx, sc)
		} else {
			err = s.store.UpsertScoreForUser(ctx, userID, sc)
		}
		if err != nil {
			return 0, fmt.Errorf("server: save score: %w", err)
		}
	}
	return len(postings), nil
}
