package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ohchanwu/jobcron/internal/ai"
	"github.com/ohchanwu/jobcron/internal/credential"
	"github.com/ohchanwu/jobcron/internal/profile"
	"github.com/ohchanwu/jobcron/internal/scoring"
	"github.com/ohchanwu/jobcron/internal/scraper"
	"github.com/ohchanwu/jobcron/internal/storage"
	"github.com/ohchanwu/jobcron/web"
)

// Handler builds the HTTP routing for the server.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleArchive)
	mux.HandleFunc("GET /briefing", s.handleDashboard)
	mux.Handle("GET /archive", http.RedirectHandler("/", http.StatusMovedPermanently))
	mux.HandleFunc("GET /bookmarks", s.handleBookmarks)
	mux.HandleFunc("GET /hidden", s.handleHidden)
	mux.HandleFunc("GET /profile", s.handleProfileForm)
	mux.HandleFunc("POST /profile", s.handleProfileSave)
	mux.HandleFunc("GET /account", s.handleAccount)
	mux.HandleFunc("POST /account/password", s.handleAccountPassword)
	mux.HandleFunc("POST /account/delete", s.handleAccountDelete)
	mux.HandleFunc("GET /login", s.handleLoginForm)
	mux.HandleFunc("POST /login", s.handleLoginPost)
	mux.HandleFunc("GET /signup", s.handleSignupForm)
	mux.HandleFunc("POST /signup", s.handleSignupPost)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.HandleFunc("GET /api/scrape", s.handleScrapeSSE)
	mux.HandleFunc("GET /api/rerate", s.handleRerateSSE)
	mux.HandleFunc("GET /api/rerate/status", s.handleRerateStatus)
	mux.HandleFunc("GET /api/briefing-status", s.handleBriefingStatus)
	mux.HandleFunc("PUT /api/bookmark/{id}", s.handleBookmarkAdd)
	mux.HandleFunc("DELETE /api/bookmark/{id}", s.handleBookmarkRemove)
	mux.HandleFunc("PUT /api/not-interested/{id}", s.handleNotInterestedAdd)
	mux.HandleFunc("DELETE /api/not-interested/{id}", s.handleNotInterestedRemove)
	// Browsers request /favicon.ico at the root regardless of the <link>
	// tags; redirect those to the embedded asset so they stop 404ing.
	mux.Handle("GET /favicon.ico", http.RedirectHandler("/static/favicon.ico", http.StatusMovedPermanently))
	mux.Handle("GET /static/", http.StripPrefix("/static/",
		http.FileServer(http.FS(web.FS))))
	var handler http.Handler = mux
	if s.productionMode && !s.demoMode {
		handler = s.requireAuth(limitAccountBody(limitSignupBody(s.csrfProtect(handler))))
	}
	if !s.demoMode {
		return handler
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.rejectDemoMutation(w, r) {
			return
		}
		handler.ServeHTTP(w, r)
	})
}

type briefingStatus struct {
	ProfileRequired bool   `json:"profile_required"`
	Latest          string `json:"latest"`
}

type navView struct {
	Active    string
	CSRFToken string
}

// handleBriefingStatus reports whether the current browser has a briefing to
// read. It is deliberately read-only: seen state belongs in browser storage.
func (s *Server) handleBriefingStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, err := s.stateUserID(ctx, r)
	if err != nil {
		writeAuthUnauthorized(w)
		return
	}
	_, _, hasProfile, err := s.profileJSON(ctx, userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	status := briefingStatus{ProfileRequired: !hasProfile}
	if hasProfile {
		b, err := s.buildBriefing(ctx, time.Now(), userID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var latest time.Time
		for _, postings := range [][]dashboardPosting{b.Today, b.Excluded} {
			for _, posting := range postings {
				if posting.Posting.FirstSeenAt.After(latest) {
					latest = posting.Posting.FirstSeenAt
				}
			}
		}
		if !latest.IsZero() {
			status.Latest = latest.UTC().Format(time.RFC3339)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(status); err != nil {
		log.Printf("encode briefing status: %v", err)
	}
}

func (s *Server) rejectDemoMutation(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "데모 모드에서는 변경할 수 없어요.", http.StatusForbidden)
		return true
	}
	switch r.URL.Path {
	case "/api/rerate":
		http.Error(w, "데모 모드에서는 AI 재평가를 실행할 수 없어요.", http.StatusForbidden)
		return true
	case "/api/scrape":
		if !s.validAdminToken(r) {
			http.Error(w, "데모 모드에서는 관리자 토큰이 있어야 스크랩할 수 있어요.", http.StatusForbidden)
			return true
		}
	}
	return false
}

// handleScrapeSSE runs the scrape pipeline, streaming progress to the client
// as Server-Sent Events. A second concurrent scrape is rejected with 409.
// The lock is global rather than per-source because the user clicks one
// button, sees one SSE stream, and would be confused by partial states.
func (s *Server) handleScrapeSSE(w http.ResponseWriter, r *http.Request) {
	userID, err := s.stateUserID(r.Context(), r)
	if err != nil {
		writeAuthUnauthorized(w)
		return
	}
	lease := s.flight.tryAcquire(scrapeAllKey)
	if lease == nil {
		http.Error(w, "이미 스크랩이 진행 중이에요. 잠시만 기다려 주세요.", http.StatusConflict)
		return
	}
	defer lease.release()
	sw, err := newSSEWriter(w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var runtime *AIRuntime
	if userID > 0 {
		runtime, err = s.aiRuntimeForUser(r.Context(), userID)
		if err != nil {
			log.Printf("jobcron: user %d AI runtime unavailable; continuing scrape with rule-based scoring: %v", userID, err)
			sw.event("status", "AI 설정을 불러오지 못해 규칙 기반 점수로 계속해요.")
			runtime = nil
		}
	}
	// Bug 2A: the scrape must NOT run on the request context. If it did,
	// navigating away from the briefing mid-scrape (which tears down the SSE
	// EventSource and cancels the request) would abort the scrape, leaving
	// postings inserted but unscored — the end-of-run scoreAll never reached,
	// so they render as blank cards. Detach onto a background context with a
	// generous ceiling so the scrape runs to completion and scores everything
	// regardless of the client. The handler still blocks here until it
	// finishes, so the singleflight lock is held for the scrape's real
	// duration; SSE writes to a since-disconnected client are no-ops
	// (sseWriter.event ignores write errors).
	ctx, cancel := context.WithTimeout(context.Background(), scrapeMaxDuration)
	defer cancel()
	res, err := s.runScrapeWithHistory(ctx, storage.ScrapeTriggerManual, sw.event, userID, runtime)
	if err != nil {
		sw.event("failed", "스크랩에 실패했어요. 잠시 후 다시 시도해 주세요.")
		return
	}
	sw.event("done", fmt.Sprintf("브리핑이 준비됐어요 — 새 공고 %d개", res.New))
}

// scrapeMaxDuration bounds a detached scrape (Bug 2A) so a wedged provider or
// stalled network can't hold the scrape singleflight lock forever. Generous: a
// full multi-source scrape paces detail fetches + AI extraction at ~1 req/s for
// up to scrapeNewCap new postings per source.
const scrapeMaxDuration = 15 * time.Minute

// dashboardPosting is one row of the daily briefing.
type dashboardPosting struct {
	Posting          scraper.Posting
	Total            int
	Excluded         bool
	Bookmarked       bool               // user has saved this posting
	NotInterested    bool               // user has muted this posting ("관심 없음")
	Explanation      string             // "React +20 · 신입 +25 ..." (used for excluded rows)
	Breakdown        []scoring.LineItem // structured line items, rendered as chips
	ExclusionReasons []exclusionReasonView
	Deadline         deadlineBadgeInfo // closing-date badge: text + urgency tier
	DuplicateSources []string          // sources of cross-portal duplicates collapsed into this canonical
}

// briefing is the daily-briefing view model: postings first seen today, split
// into the scored list and the dealbreaker-excluded list.
type briefing struct {
	Today                 []dashboardPosting
	Excluded              []dashboardPosting
	Date                  string      // "2026 / 05 / 23" (KST)
	Rerate                *rerateInfo // re-rate button state; nil = no AI key (button hidden)
	ShowScheduledAITip    bool
	AIEstimatedDailyCents int
	CSRFToken             string
}

// briefingCap bounds how many postings the daily briefing lists.
const briefingCap = 50

// kstZone is Korea Standard Time (UTC+9, no DST).
var kstZone = time.FixedZone("KST", 9*60*60)

// handleDashboard renders the daily briefing. On first run (no profile saved)
// it redirects to the profile form.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, err := s.stateUserID(ctx, r)
	if err != nil {
		writeAuthUnauthorized(w)
		return
	}
	_, _, hasProfile, err := s.profileJSON(ctx, userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !hasProfile {
		http.Redirect(w, r, "/profile?reason=profile-required", http.StatusSeeOther)
		return
	}
	b, err := s.buildBriefing(ctx, time.Now(), userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderWithRequest(w, r, "index.html", b)
}

// buildBriefing assembles today's briefing: postings first seen today and not
// yet past their closing date, each joined with its score, split into the
// scored list (sorted high to low, capped) and the dealbreaker-excluded list.
// Postings from sources the user has disabled in their profile are filtered
// out — disabled sources are invisible everywhere except /bookmarks. Cross-
// portal duplicates are also filtered (only the canonical row is rendered;
// the dashboardPosting carries its sibling sources via DuplicateSources for
// the "also on …" badge).
func (s *Server) buildBriefing(ctx context.Context, now time.Time, userIDOpt ...int64) (briefing, error) {
	userID := optionalUserID(userIDOpt)
	return s.buildBriefingWithRuntime(ctx, now, userID, s.runtimeForRender(ctx, userID))
}

func (s *Server) buildBriefingWithRuntime(ctx context.Context, now time.Time, userID int64, runtime *AIRuntime) (briefing, error) {
	snapshot, err := s.loadRenderProfileSnapshot(ctx, userID)
	if err != nil {
		return briefing{}, err
	}
	postings, err := s.store.CanonicalPostings(ctx)
	if err != nil {
		return briefing{}, err
	}
	scores, err := s.scoresForRenderSnapshot(ctx, userID, snapshot)
	if err != nil {
		return briefing{}, err
	}
	bookmarks, err := s.bookmarkedIDs(ctx, userID)
	if err != nil {
		return briefing{}, err
	}
	if s.demoMode {
		bookmarks = map[int64]bool{}
	}
	muted, err := s.notInterestedIDs(ctx, userID)
	if err != nil {
		return briefing{}, err
	}
	if s.demoMode {
		muted = map[int64]bool{}
	}
	dupSources, err := s.store.DuplicateSourcesByCanonical(ctx)
	if err != nil {
		return briefing{}, err
	}
	prof := snapshot.profile
	disabled := s.disabledSourceSet(prof.DisabledSources)
	b := briefing{Date: now.In(kstZone).Format("2006 / 01 / 02")}
	for _, p := range postings {
		if disabled[p.Source] || muted[p.ID] {
			continue
		}
		if !sameKSTDay(p.FirstSeenAt, now) || expired(p, now) {
			continue
		}
		// Bug 2B (render-time belt-and-suspenders): a posting with no score row
		// is mid-pipeline — an interrupted scrape inserted it before scoreAll
		// ran. Never render a scoreless card (no total, no 신입 chip); skip it.
		// This is the last line of defense behind two guards that between them
		// prevent the unscored state reaching render: handleScrapeSSE detaches
		// the scrape from the request context so client navigation can't cancel
		// it (Bug 2A), and main calls RescoreSoleOwner at startup so a process crash
		// between insert and scoring is healed on the next boot. Every scored
		// surface also omits postings whose score row is still missing, so a
		// partial operation never fabricates a zero score.
		sc, ok := scores[p.ID]
		if !ok {
			continue
		}
		dp := dashboardPosting{
			Posting:          p,
			Bookmarked:       bookmarks[p.ID],
			Deadline:         deadlineBadge(p.ClosedAt, p.AlwaysOpen, now),
			DuplicateSources: dupSources[p.ID],
		}
		dp.Total = sc.Total
		// Dealbreaker hits (Total = -1) are always excluded. The MinScore knob
		// collapses additional low-scoring rows out of the main "Today" list —
		// the user can still find them under the expandable "제외된 공고"
		// section. MinScore = 0 disables the soft-hide entirely. A bookmarked
		// posting is exempt from the soft MinScore hide (the user deliberately
		// saved it) but NOT from the dealbreaker hide — Total < 0 stays
		// unconditional.
		dp.Excluded = sc.Total < 0 || (sc.Total < prof.EffectiveMinScore() && !bookmarks[p.ID])
		var result scoring.ScoreResult
		if json.Unmarshal([]byte(sc.BreakdownJSON), &result) == nil {
			dp.Explanation = scoring.Explain(result)
			dp.Breakdown = result.Breakdown
			dp.ExclusionReasons = exclusionReasonViews(result.ExclusionReasons)
		}
		if dp.Excluded {
			b.Excluded = append(b.Excluded, dp)
		} else {
			b.Today = append(b.Today, dp)
		}
	}
	sort.SliceStable(b.Today, func(i, j int) bool { return b.Today[i].Total > b.Today[j].Total })
	if len(b.Today) > briefingCap {
		b.Today = b.Today[:briefingCap]
	}
	b.Rerate = s.buildRerateInfo(ctx, userID, runtime, prof, "today", b.Today, b.Excluded)
	b.ShowScheduledAITip = b.Rerate != nil && !prof.ScheduledAIEnabled
	b.AIEstimatedDailyCents = prof.EffectiveAIDailyUSDCapCents()
	return b, nil
}

// sameKSTDay reports whether a and b fall on the same calendar day in KST.
func sameKSTDay(a, b time.Time) bool {
	ak, bk := a.In(kstZone), b.In(kstZone)
	return ak.Year() == bk.Year() && ak.YearDay() == bk.YearDay()
}

// expired reports whether a posting is past its closing date.
func expired(p scraper.Posting, now time.Time) bool {
	return !p.AlwaysOpen && p.ClosedAt != nil && p.ClosedAt.Before(now)
}

// deadlineBadgeInfo is a rendered closing-date badge: a short Korean label
// plus an urgency tier the template maps to a CSS class (deadline-<Kind>).
type deadlineBadgeInfo struct {
	Text string // "오늘 마감" | "마감 D-10" | "상시채용" | "마감 정보 없음" | "마감"
	Kind string // "urgent" | "calm" | "open" | "none"
}

// deadlineBadge returns the closing-date badge for a posting. It is total —
// every posting gets exactly one badge, tiered by urgency so a job closing in
// a month doesn't shout in the same alarm color as one closing today (the P6
// calm thesis):
//
//	always-open               → "상시채용"       (open)    — rolling hiring, good news
//	no closing date on file   → "마감 정보 없음"  (none)    — usually an unparsed deadline
//	already past its deadline → "마감"           (urgent)
//	closes today              → "오늘 마감"       (urgent)
//	closes within 3 days      → "마감 D-N"        (urgent)
//	closes in 4+ days         → "마감 D-N"        (calm)
//
// Day counts are on KST calendar boundaries (so "D-1" means closes tomorrow
// regardless of the wall-clock time today); the past check uses the actual
// instant so it agrees with expired().
func deadlineBadge(closedAt *time.Time, alwaysOpen bool, now time.Time) deadlineBadgeInfo {
	if alwaysOpen {
		return deadlineBadgeInfo{Text: "상시채용", Kind: "open"}
	}
	if closedAt == nil {
		return deadlineBadgeInfo{Text: "마감 정보 없음", Kind: "none"}
	}
	if closedAt.Before(now) {
		return deadlineBadgeInfo{Text: "마감", Kind: "urgent"}
	}
	c, n := closedAt.In(kstZone), now.In(kstZone)
	closeDay := time.Date(c.Year(), c.Month(), c.Day(), 0, 0, 0, 0, kstZone)
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, kstZone)
	switch days := int(closeDay.Sub(today).Hours() / 24); {
	case days == 0:
		return deadlineBadgeInfo{Text: "오늘 마감", Kind: "urgent"}
	case days <= 3:
		return deadlineBadgeInfo{Text: fmt.Sprintf("마감 D-%d", days), Kind: "urgent"}
	default:
		return deadlineBadgeInfo{Text: fmt.Sprintf("마감 D-%d", days), Kind: "calm"}
	}
}

// profileForm is the view model for the profile form — flat string/int fields
// matching the HTML inputs.
type profileForm struct {
	CSRFToken        string
	ProfileRequired  bool
	CareerYears      int
	CareerWeight     int
	CareerNearMiss   int // derived: round(CareerWeight × 2/5), shown as a hint
	SalaryFloorMan   int
	SalaryWeight     int
	SalaryAmbiguous  int // derived: round(SalaryWeight ÷ 2), shown as a hint
	MinScore         int
	MaxEducation     int
	StacksText       string
	CitiesText       string
	LocationWeight   int
	RemoteOK         bool
	DealbreakersText string
	JobLikes         string // v2.0 AI goal fields (free text)
	JobDislikes      string
	ShortTermGoals   string
	LongTermGoals    string
	Sources          []sourceOption // one row per registered scraper

	// AI settings (v2.0 BYOK). The API key value is NEVER placed here — only
	// AIKeySaved (whether a key exists for the selected provider) crosses to the
	// template, so the secret is never re-rendered (design §5).
	AIProvider          string // "" | "anthropic"
	AIModel             string
	AIModels            []string    // selectable models for the CURRENT provider (server-side render)
	AIModelOptionsJSON  template.JS // provider→[]model map, for the client-side dropdown swap
	AIKeySaved          bool
	AIDailyTokenCap     int // raw (0 = use default); the form input value
	AIDailyCapEffective int // the cap actually in force, for the remaining line
	AITokensUsedToday   int
	AIRemainingToday    int
	AIPerCallCap        int // raw (0 = use default); the form input value
	AIPerCallCapEffect  int // the per-call cap actually in force, for the placeholder/hint
	ScheduledAIEnabled  bool
	AIMonthlyUSDCap     int
	AIMonthlyUSDDefault int
	AIDailyUSDCap       int
	AIDailyUSDDefault   int
	AIRunUSDCap         int
	AIRunUSDDefault     int
}

// handleProfileForm renders the profile form, pre-filled with any saved profile.
func (s *Server) handleProfileForm(w http.ResponseWriter, r *http.Request) {
	userID, err := s.stateUserID(r.Context(), r)
	if err != nil {
		writeAuthUnauthorized(w)
		return
	}
	jsonStr, _, ok, err := s.profileJSON(r.Context(), userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var p profile.Profile
	if ok {
		if p, err = profile.Unmarshal(jsonStr); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	form := toProfileForm(p)
	form.ProfileRequired = r.URL.Query().Get("reason") == "profile-required"
	form.Sources = s.sourceOptions(p.DisabledSources, !ok)
	s.fillAIFormState(r.Context(), userID, &form, p)
	s.renderWithRequest(w, r, "profile.html", form)
}

// fillAIFormState populates the AI section of the profile form: whether a key is
// already saved for the selected provider (so the form shows "•••• 저장됨"
// instead of an empty field), the effective daily cap, and today's usage /
// remaining. Read failures degrade quietly to "no key / zero used" — the form
// must still render.
func (s *Server) fillAIFormState(ctx context.Context, userID int64, form *profileForm, p profile.Profile) {
	form.AIDailyCapEffective = p.EffectiveAIDailyTokenCap()
	form.AIPerCallCapEffect = p.EffectiveAIPerCallCap()
	form.AIMonthlyUSDDefault = profile.DefaultAIMonthlyUSDCents
	form.AIDailyUSDDefault = profile.DefaultAIDailyUSDCents
	form.AIRunUSDDefault = profile.DefaultAIRunUSDCents
	// Provider-aware model dropdown: the current provider's models render
	// server-side; the full map drives the client-side swap when the provider
	// select changes (so a model id can't be paired with the wrong provider).
	form.AIModels = ai.ModelsForProvider(p.AIProvider)
	if b, err := json.Marshal(ai.ModelsByProvider()); err == nil {
		form.AIModelOptionsJSON = template.JS(b)
	}
	if userID > 0 && p.AIProvider != "" {
		_, form.AIKeySaved, _ = s.store.UserAICredential(ctx, userID, p.AIProvider)
	}
	in, out, err := s.store.AIUsageForDay(ctx, userID, time.Now().UTC().Format("2006-01-02"))
	if err == nil {
		form.AITokensUsedToday = in + out
	}
	if rem := form.AIDailyCapEffective - form.AITokensUsedToday; rem > 0 {
		form.AIRemainingToday = rem
	}
}

// handleProfileSave parses the submitted form, stores the profile, re-scores
// every posting against it, and redirects to the dashboard.
func (s *Server) handleProfileSave(w http.ResponseWriter, r *http.Request) {
	userID, err := s.stateUserID(r.Context(), r)
	if err != nil {
		writeAuthUnauthorized(w)
		return
	}
	// Profile changes alter score inputs and AI runtime configuration. Serialize
	// them with scrape/rerate so one operation cannot resolve an old runtime and
	// finish scoring against a newly committed profile.
	lease := s.flight.tryAcquire(scrapeAllKey)
	if lease == nil {
		http.Error(w, "이미 작업이 진행 중이에요. 잠시만 기다려 주세요.", http.StatusConflict)
		return
	}
	defer lease.release()
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	previousProfile, _, err := s.loadProfile(r.Context(), userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Sources: the form submits source_<id>=on for each checked box. We
	// invert that into the DisabledSources opt-out list (registered IDs
	// the user did NOT check), so a future scraper added in a later
	// release ships enabled-by-default without a profile rewrite.
	var disabled []string
	for _, src := range s.sources {
		id := src.Source()
		if r.FormValue("source_"+id) == "" {
			disabled = append(disabled, id)
		}
	}
	minScore := atoi(r.FormValue("min_score"))
	// A submitted daily cap equal to the default is stored as 0 so an unchanged
	// default stays absent from the canonical JSON (omitempty) — AI-off profiles
	// keep byte-identical bytes.
	dailyCap := atoi(r.FormValue("ai_daily_token_cap"))
	if dailyCap == profile.DefaultDailyTokenCap {
		dailyCap = 0
	}
	// Same convention for the per-call cap: storing the default as 0 keeps an
	// unchanged default absent from the canonical JSON (omitempty).
	perCallCap := atoi(r.FormValue("ai_per_call_cap"))
	if perCallCap == profile.DefaultAIPerCallCap {
		perCallCap = 0
	}
	monthlyUSDCap := atoi(r.FormValue("ai_monthly_usd_cap_cents"))
	if monthlyUSDCap == profile.DefaultAIMonthlyUSDCents {
		monthlyUSDCap = 0
	}
	dailyUSDCap := atoi(r.FormValue("ai_daily_usd_cap_cents"))
	if dailyUSDCap == profile.DefaultAIDailyUSDCents {
		dailyUSDCap = 0
	}
	runUSDCap := atoi(r.FormValue("ai_run_usd_cap_cents"))
	if runUSDCap == profile.DefaultAIRunUSDCents {
		runUSDCap = 0
	}
	p := profile.Profile{
		CareerYears:    atoi(r.FormValue("career_years")),
		CareerWeight:   atoi(r.FormValue("career_weight")),
		SalaryFloorKRW: atoi(r.FormValue("salary_floor_man")) * 10000,
		SalaryWeight:   atoi(r.FormValue("salary_weight")),
		MinScore:       &minScore,
		MaxEducation:   profile.EducationLevel(atoi(r.FormValue("max_education"))),
		Stacks:         parseStacks(r.FormValue("stacks")),
		Location: profile.LocationPref{
			Cities:   parseCSV(r.FormValue("cities")),
			Weight:   atoi(r.FormValue("location_weight")),
			RemoteOK: r.FormValue("remote_ok") != "",
		},
		Dealbreakers:         parseLines(r.FormValue("dealbreakers")),
		JobLikes:             strings.TrimSpace(r.FormValue("job_likes")),
		JobDislikes:          strings.TrimSpace(r.FormValue("job_dislikes")),
		ShortTermGoals:       strings.TrimSpace(r.FormValue("short_term_goals")),
		LongTermGoals:        strings.TrimSpace(r.FormValue("long_term_goals")),
		DisabledSources:      disabled,
		AIProvider:           aiProviderValue(r.FormValue("ai_provider")),
		AIModel:              strings.TrimSpace(r.FormValue("ai_model")),
		AIDailyTokenCap:      dailyCap,
		AIPerCallCap:         perCallCap,
		ScheduledAIEnabled:   r.FormValue("scheduled_ai_enabled") != "",
		AIMonthlyUSDCapCents: monthlyUSDCap,
		AIDailyUSDCapCents:   dailyUSDCap,
		AIRunUSDCapCents:     runUSDCap,
	}
	canonical, err := profile.Marshal(p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	dealbreakersChanged := profile.DealbreakerInputHash(previousProfile) != profile.DealbreakerInputHash(p)
	if userID == 0 {
		if _, _, err := s.saveProfileJSON(r.Context(), userID, canonical); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if dealbreakersChanged {
			s.rerates.invalidateUser(userID)
		}
		if _, err := s.scoreAll(r.Context(), userID, nil); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/briefing", http.StatusSeeOther)
		return
	}
	var encrypted *storage.EncryptedAICredential
	if plaintext := strings.TrimSpace(r.FormValue("ai_key")); plaintext != "" {
		provider, err := credential.NormalizeProvider(p.AIProvider)
		if err != nil {
			http.Error(w, "AI 제공자 설정을 확인해주세요.", http.StatusBadRequest)
			return
		}
		if s.credentialCipher == nil {
			http.Error(w, "AI 자격 증명 암호화를 사용할 수 없어요.", http.StatusServiceUnavailable)
			return
		}
		ciphertext, nonce, version, err := s.credentialCipher.Seal(userID, provider, plaintext)
		if err != nil {
			http.Error(w, "AI 자격 증명을 암호화하지 못했어요.", http.StatusInternalServerError)
			return
		}
		encrypted = &storage.EncryptedAICredential{UserID: userID, Provider: provider, Ciphertext: ciphertext, Nonce: nonce, EncryptionVersion: version}
	}
	if _, _, err := s.store.SaveProfileAndCredentialForUser(r.Context(), userID, canonical, encrypted); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if dealbreakersChanged {
		s.rerates.invalidateUser(userID)
	}
	runtime, err := s.aiRuntimeForUser(r.Context(), userID)
	if err != nil {
		log.Printf("jobcron: user %d profile saved but AI runtime unavailable; rescoring with rules: %v", userID, err)
		runtime = nil
	}
	if _, err := s.scoreAll(r.Context(), userID, runtime); err != nil {
		log.Printf("jobcron: user %d profile saved but rescore failed: %v", userID, err)
		if runtime != nil {
			if _, fallbackErr := s.scoreAll(r.Context(), userID, nil); fallbackErr != nil {
				log.Printf("jobcron: user %d rule-based rescore fallback failed: %v", userID, fallbackErr)
			}
		}
	}
	http.Redirect(w, r, "/briefing", http.StatusSeeOther)
}

// aiProviderValue normalizes the provider select value: only the known provider
// ids pass through; anything else (including the "없음/끄기" empty option) means
// AI off.
func aiProviderValue(v string) string {
	switch strings.TrimSpace(v) {
	case "anthropic":
		return "anthropic"
	default:
		return ""
	}
}

// toProfileForm converts a stored profile into the flat form view model.
func toProfileForm(p profile.Profile) profileForm {
	stacks := make([]string, 0, len(p.Stacks))
	for _, sp := range p.Stacks {
		stacks = append(stacks, sp.Name+","+strconv.Itoa(sp.Weight))
	}
	careerNearMiss, salaryAmbiguous := scoring.WeightHints(p)
	return profileForm{
		CareerYears:        p.CareerYears,
		CareerWeight:       p.EffectiveCareerWeight(),
		CareerNearMiss:     careerNearMiss,
		SalaryFloorMan:     p.SalaryFloorKRW / 10000,
		SalaryWeight:       p.EffectiveSalaryWeight(),
		SalaryAmbiguous:    salaryAmbiguous,
		MinScore:           p.EffectiveMinScore(),
		MaxEducation:       int(p.MaxEducation),
		StacksText:         strings.Join(stacks, "\n"),
		CitiesText:         strings.Join(p.Location.Cities, ", "),
		LocationWeight:     p.Location.Weight,
		RemoteOK:           p.Location.RemoteOK,
		DealbreakersText:   strings.Join(p.Dealbreakers, "\n"),
		JobLikes:           p.JobLikes,
		JobDislikes:        p.JobDislikes,
		ShortTermGoals:     p.ShortTermGoals,
		LongTermGoals:      p.LongTermGoals,
		AIProvider:         p.AIProvider,
		AIModel:            p.AIModel,
		AIDailyTokenCap:    p.AIDailyTokenCap, // raw: 0 renders as an empty input
		AIPerCallCap:       p.AIPerCallCap,    // raw: 0 renders as an empty input
		ScheduledAIEnabled: p.ScheduledAIEnabled,
		AIMonthlyUSDCap:    p.AIMonthlyUSDCapCents,
		AIDailyUSDCap:      p.AIDailyUSDCapCents,
		AIRunUSDCap:        p.AIRunUSDCapCents,
	}
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// atoi parses an integer form value, treating blank or malformed input as 0.
func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// absInt is a template helper: the absolute value of an int, used to render a
// negative AI delta as "−N" (the template supplies the U+2212 minus sign and
// the muted styling, so the magnitude is all that's needed here).
func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func usdCents(cents int) string {
	if cents%100 == 0 {
		return fmt.Sprintf("$%d", cents/100)
	}
	return fmt.Sprintf("$%d.%02d", cents/100, cents%100)
}

// parseLines splits textarea input into trimmed, non-empty lines.
func parseLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// parseCSV splits comma-separated input into trimmed, non-empty values.
func parseCSV(text string) []string {
	var out []string
	for _, part := range strings.Split(text, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// parseStacks parses one "name,weight" stack preference per line.
func parseStacks(text string) []profile.StackPref {
	var out []profile.StackPref
	for _, line := range parseLines(text) {
		name, weight, _ := strings.Cut(line, ",")
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		out = append(out, profile.StackPref{Name: name, Weight: atoi(weight)})
	}
	return out
}
