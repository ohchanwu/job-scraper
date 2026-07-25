//go:build browserfixture

// Package server's browser fixture: a long-running, credential-free preview of
// the contextual-dealbreaker exclusion surface for human and /browse
// inspection. It exists only in the test binary behind the `browserfixture`
// build tag — it adds no production route, flag, or runtime dependency.
//
//	go test -tags browserfixture ./internal/server \
//	  -run TestDealbreakerProvenanceBrowserFixture -v -count=1 -timeout=0
//
// Everything it serves is synthetic: two invented postings, one invented owner,
// two invented destination pages, and a deterministic ai.StubProvider standing
// in for a paid backend. No real key is read and no external host is contacted.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ohchanwu/jobcron/internal/ai"
	"github.com/ohchanwu/jobcron/internal/profile"
	"github.com/ohchanwu/jobcron/internal/scoring"
	"github.com/ohchanwu/jobcron/internal/scraper"
)

// fixtureSessionToken is a synthetic session secret for a synthetic user in a
// throwaway schema. It authenticates nothing outside this test binary.
const fixtureSessionToken = "browser-fixture-session-token"

// fixtureAPIKey is the plaintext the stub provider factory expects. It is not a
// credential for any service.
const fixtureAPIKey = "browser-fixture-synthetic-key"

// fixtureRole is one synthetic posting's destination page. A QA walk that
// clicks a posting link has to land somewhere that proves WHICH role opened;
// the postings' original example.test URLs did not resolve at all, so the
// external-link step could never actually pass.
type fixtureRole struct {
	path    string
	title   string
	company string
	marker  string // unique to this role — never appears on the other page
	detail  string
}

var fixtureRoles = []fixtureRole{
	{
		path:    "/__fixture/role/benefit",
		title:   "신입 백엔드 개발자",
		company: "가상회사",
		marker:  "FIXTURE-ROLE-BENEFIT",
		detail:  "Go 백엔드 API를 개발합니다. 병역특례는 지원 가능한 복지 혜택이며, 담당 업무가 아닙니다.",
	},
	{
		path:    "/__fixture/role/requirement",
		title:   "신입 사용자 조사 담당자",
		company: "가상연구소",
		marker:  "FIXTURE-ROLE-REQUIREMENT",
		detail:  "담당 업무는 리서치 입니다. 사용자 요구를 조사하고 결과를 분석합니다.",
	},
}

func fixtureRoleByMarker(marker string) fixtureRole {
	for _, role := range fixtureRoles {
		if role.marker == marker {
			return role
		}
	}
	panic("browser fixture: unknown role marker " + marker)
}

// fixtureRoutes is the single source of truth for every path the fixture mux
// adds. TestBrowserFixtureAddsNoProductionRoute walks it against the shipped
// handler, so a new fixture route cannot be added without also being proved
// absent from production.
var fixtureRoutes = func() []string {
	paths := []string{"/__fixture/login", "/__fixture/stop", "/__fixture/stats"}
	for _, role := range fixtureRoles {
		paths = append(paths, role.path)
	}
	return paths
}()

// fixtureCallCounter makes the stub's paid-call count browser-observable. It
// carries its own mutex because /__fixture/stats is read from an HTTP handler
// while a re-rate may still be incrementing it — ai.StubProvider's counters are
// exported fields with no locked reader, so reading them directly would race.
type fixtureCallCounter struct {
	mu sync.Mutex
	n  int
}

func (c *fixtureCallCounter) observe() {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
}

func (c *fixtureCallCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// countingFixtureProvider wraps the stub so Stage 1B calls are counted behind
// the counter's own lock.
type countingFixtureProvider struct {
	ai.Provider
	calls *fixtureCallCounter
}

func (p *countingFixtureProvider) ValidateDealbreakers(
	ctx context.Context,
	modelText string,
	candidates []ai.DealbreakerCandidate,
) ([]ai.DealbreakerValidation, ai.Usage, error) {
	p.calls.observe()
	return p.Provider.ValidateDealbreakers(ctx, modelText, candidates)
}

type fixtureMuxConfig struct {
	app    http.Handler // the real, shipped handler
	cookie *http.Cookie
	calls  *fixtureCallCounter
	stop   func()
}

// newFixtureMux composes the fixture-only routes in front of the real handler.
// Every fixture route lives here and nowhere near production wiring.
func newFixtureMux(cfg fixtureMuxConfig) *http.ServeMux {
	mux := http.NewServeMux()

	// Test-only login: hands the browser the synthetic session cookie so the walk
	// starts on the real, authenticated UI rather than a bespoke preview page.
	mux.HandleFunc("/__fixture/login", func(w http.ResponseWriter, r *http.Request) {
		if cfg.cookie != nil {
			http.SetCookie(w, &http.Cookie{Name: cfg.cookie.Name, Value: cfg.cookie.Value, Path: "/"})
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})

	// Test-only stop: the documented cleanup path, so nobody has to hunt the
	// process down after inspection.
	mux.HandleFunc("/__fixture/stop", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("browser fixture stopping\n"))
		if cfg.stop != nil {
			cfg.stop()
		}
	})

	// Test-only stats: lets a browser walk PROVE the paid-call count instead of
	// inferring it from the Korean "no extra tokens" copy, which is exactly the
	// string a caching bug would keep showing.
	mux.HandleFunc("/__fixture/stats", func(w http.ResponseWriter, _ *http.Request) {
		n := 0
		if cfg.calls != nil {
			n = cfg.calls.count()
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]int{"stage1b_calls": n})
	})

	for _, role := range fixtureRoles {
		role := role
		mux.HandleFunc(role.path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!doctype html><html lang="ko"><head><meta charset="utf-8">`+
				`<title>%s · %s</title></head><body>`+
				`<h1>%s</h1><p>%s</p><p>%s</p><p data-role-marker="%s">%s</p>`+
				`</body></html>`,
				role.title, role.company, role.title, role.company, role.detail, role.marker, role.marker)
		})
	}

	mux.Handle("/", cfg.app)
	return mux
}

// TestBrowserFixtureAddsNoProductionRoute proves every fixture route lives on
// the fixture's own mux, not on the shipped handler. It runs (and fails) inside
// the same tagged build that defines them, so the fixture cannot quietly grow a
// production surface.
func TestBrowserFixtureAddsNoProductionRoute(t *testing.T) {
	srv, _ := newTestServer(t, &fakeScraper{})
	if len(fixtureRoutes) != 3+len(fixtureRoles) {
		t.Fatalf("fixtureRoutes = %v, want every fixture path listed", fixtureRoutes)
	}
	for _, path := range fixtureRoutes {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("production handler served %s with status %d, want 404", path, rec.Code)
		}
	}
}

// TestFixtureRoleDestinationsProveTheRole: a browser QA step that clicks a
// posting link must land somewhere that identifies WHICH role it opened.
// example.test does not resolve, so the old synthetic URLs made the
// external-link check unverifiable.
func TestFixtureRoleDestinationsProveTheRole(t *testing.T) {
	mux := newFixtureMux(fixtureMuxConfig{app: http.NotFoundHandler()})
	for _, role := range fixtureRoles {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, role.path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", role.path, rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, role.title) || !strings.Contains(body, role.marker) {
			t.Fatalf("GET %s did not identify its role: %s", role.path, body)
		}
		for _, other := range fixtureRoles {
			if other.path == role.path {
				continue
			}
			if strings.Contains(body, other.marker) {
				t.Fatalf("GET %s also carries %s's marker — the destinations are not distinguishable",
					role.path, other.path)
			}
		}
	}
}

// TestFixtureStatsReportsStage1BCalls makes the paid-call count browser
// observable, so QA can prove "no re-spend" from the counter instead of
// trusting the Korean UI copy.
func TestFixtureStatsReportsStage1BCalls(t *testing.T) {
	counter := &fixtureCallCounter{}
	mux := newFixtureMux(fixtureMuxConfig{app: http.NotFoundHandler(), calls: counter})

	read := func() int {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/__fixture/stats", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /__fixture/stats status = %d", rec.Code)
		}
		var got struct {
			Stage1BCalls int `json:"stage1b_calls"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode stats: %v (body %s)", err, rec.Body.String())
		}
		return got.Stage1BCalls
	}

	if n := read(); n != 0 {
		t.Fatalf("initial stage1b_calls = %d, want 0", n)
	}
	counter.observe()
	counter.observe()
	if n := read(); n != 2 {
		t.Fatalf("stage1b_calls = %d, want 2", n)
	}
	if n := read(); n != 2 {
		t.Fatalf("reading the counter changed it: %d", n)
	}
}

func TestDealbreakerProvenanceBrowserFixture(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	ctx := context.Background()
	// Cookie-session auth, so the walk goes through the same request path a real
	// user does rather than a local-user shortcut.
	srv.SetProductionMode(true)

	// Bind first: each synthetic posting's URL must point at this listener's own
	// role page, so the QA external-link step lands on a real, role-identifying
	// destination instead of an unresolvable example.test host.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on loopback: %v", err)
	}
	addr := ln.Addr().String()
	roleURL := func(marker string) string {
		return "http://" + addr + fixtureRoleByMarker(marker).path
	}

	userID, cookie := createSessionUser(t, st, "browser-fixture@example.invalid", fixtureSessionToken)
	zero := 0
	prof := profile.Profile{
		CareerYears:  0,
		MinScore:     &zero,
		JobLikes:     "백엔드 서버 개발",
		Dealbreakers: []string{"병역특례", "리서치"},
		AIProvider:   "anthropic",
		AIModel:      "browser-fixture-model",
		// One provider call per press, so reaching zero pending needs two presses
		// — the multi-press path the browser walk has to exercise.
		AIPerCallCap: 1,
	}
	saveAIRuntimeProfile(t, st, userID, prof)

	cipher := newAIRuntimeTestCipher(t, 0x7b)
	srv.SetCredentialCipher(cipher)
	saveAIRuntimeCredential(t, st, cipher, userID, "anthropic", fixtureAPIKey)

	now := time.Now().UTC()
	// The incident posting: 병역특례 reaches the matcher only through a welfare
	// tag appended to the description, so version 1 could never resolve it.
	benefitRole := fixtureRoleByMarker("FIXTURE-ROLE-BENEFIT")
	benefit := listingPosting("browser-fixture-benefit", benefitRole.title)
	benefit.Company = benefitRole.company
	benefit.URL = roleURL(benefitRole.marker)
	benefit.Description = "Go 백엔드 API를 개발합니다. 서버 개발자를 찾습니다.\n병역특례 가능"
	benefit.Tags = []scraper.Tag{{Name: "병역특례 가능", Category: "welfare"}}
	benefit.FirstSeenAt, benefit.LastSeenAt = now, now
	benefit.ID = mustUpsert(t, st, benefit)

	// The control posting: the phrase really is a role responsibility, so it must
	// stay excluded. Its title deliberately omits the phrase so the row shows
	// 공고 본문 provenance, distinct from the benefit row's 복지 태그.
	requirementRole := fixtureRoleByMarker("FIXTURE-ROLE-REQUIREMENT")
	requirement := listingPosting("browser-fixture-requirement", requirementRole.title)
	requirement.Company = requirementRole.company
	requirement.URL = roleURL(requirementRole.marker)
	requirement.Description = requirementRole.detail
	requirement.FirstSeenAt, requirement.LastSeenAt = now, now
	requirement.ID = mustUpsert(t, st, requirement)

	verdicts := map[string]struct {
		verdict ai.DealbreakerVerdict
		reason  ai.DealbreakerReasonCode
	}{
		"병역특례": {ai.DealbreakerNotApplicable, ai.DealbreakerReasonBenefitOrEligibility},
		"리서치":  {ai.DealbreakerApplies, ai.DealbreakerReasonRequirement},
	}
	stub := &ai.StubProvider{
		NameVal: "anthropic",
		ValidateDealbreakersFn: func(_ context.Context, _ string, candidates []ai.DealbreakerCandidate) ([]ai.DealbreakerValidation, ai.Usage, error) {
			out := make([]ai.DealbreakerValidation, 0, len(candidates))
			for _, candidate := range candidates {
				decided, ok := verdicts[candidate.Phrase]
				if !ok {
					continue
				}
				out = append(out, ai.DealbreakerValidation{
					CandidateID: candidate.ID,
					Verdict:     decided.verdict,
					ReasonCode:  decided.reason,
				})
			}
			return out, ai.Usage{InputTokens: 12, OutputTokens: 4}, nil
		},
		ExtractFn: func(context.Context, string) (ai.Extraction, ai.Usage, error) {
			return ai.Extraction{}, ai.Usage{}, fmt.Errorf("browser fixture: Stage 1A must be served from cache")
		},
		ScoreDeltaFn: func(context.Context, string, string) ([]ai.RawDeltaItem, ai.Usage, error) {
			return nil, ai.Usage{}, fmt.Errorf("browser fixture: Stage 2 must be served from cache")
		},
	}
	calls := &fixtureCallCounter{}
	provider := &countingFixtureProvider{Provider: stub, calls: calls}
	srv.newAIProvider = func(_ string, key string, _ string, _ time.Duration) (ai.Provider, error) {
		if key != fixtureAPIKey {
			return nil, fmt.Errorf("browser fixture: unexpected synthetic credential")
		}
		return provider, nil
	}

	runtime, err := srv.aiRuntimeForUser(ctx, userID)
	if err != nil || runtime == nil {
		t.Fatalf("resolve fixture AI runtime: runtime=%v err=%v", runtime, err)
	}
	// Preseed Stage 1A and Stage 2 so a browser press can only ever move Stage 1B
	// — the two stub functions above turn any other call into a hard failure.
	for _, p := range []scraper.Posting{benefit, requirement} {
		_, contentHash, _ := ai.ModelInput(p)
		if err := st.UpsertAIExtraction(ctx, p.ID, contentHash, ai.ExtractionContractVersion(),
			ai.Extraction{Newcomer: true, EducationEnum: ai.EduNone}, now); err != nil {
			t.Fatalf("seed Stage 1A cache: %v", err)
		}
		if err := st.UpsertAIScore(ctx, userID, p.ID, profile.AIInputHash(prof),
			runtime.ScoreVersion, ai.Delta{}, now); err != nil {
			t.Fatalf("seed Stage 2 cache: %v", err)
		}
	}
	if _, err := srv.scoreAll(ctx, userID, runtime); err != nil {
		t.Fatalf("seed deterministic scores: %v", err)
	}
	for _, p := range []scraper.Posting{benefit, requirement} {
		score, ok, err := st.ScoreByPostingIDForUser(ctx, userID, p.ID)
		if err != nil || !ok || score.Total != -1 {
			t.Fatalf("posting %d is not conservatively excluded before the walk: %+v", p.ID, score)
		}
		if len(scoring.DealbreakerCandidates(p, prof)) != 1 {
			t.Fatalf("posting %d must carry exactly one candidate for the two-press walk", p.ID)
		}
	}

	stop := make(chan struct{})
	var stopOnce sync.Once
	mux := newFixtureMux(fixtureMuxConfig{
		app:    srv.Handler(),
		cookie: cookie,
		calls:  calls,
		stop:   func() { stopOnce.Do(func() { close(stop) }) },
	})

	httpSrv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = httpSrv.Serve(ln) }()
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	})

	t.Logf("BROWSER_FIXTURE_URL=http://%s/__fixture/login", addr)
	t.Logf("BROWSER_FIXTURE_STOP_URL=http://%s/__fixture/stop", addr)
	t.Logf("BROWSER_FIXTURE_STATS_URL=http://%s/__fixture/stats", addr)
	t.Logf("fixture ready: 2 postings, both excluded, per-call cap 1 — press AI 평가 twice")
	<-stop
}
