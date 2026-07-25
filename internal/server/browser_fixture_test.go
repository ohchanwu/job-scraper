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
// and a deterministic ai.StubProvider standing in for a paid backend. No real
// key is read and no external host is contacted.
package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
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

// TestBrowserFixtureAddsNoProductionRoute proves the login/stop routes live on
// the fixture's own mux, not on the shipped handler. It runs (and fails) inside
// the same tagged build that defines them, so the fixture cannot quietly grow a
// production surface.
func TestBrowserFixtureAddsNoProductionRoute(t *testing.T) {
	srv, _ := newTestServer(t, &fakeScraper{})
	for _, path := range []string{"/__fixture/login", "/__fixture/stop"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("production handler served %s with status %d, want 404", path, rec.Code)
		}
	}
}

func TestDealbreakerProvenanceBrowserFixture(t *testing.T) {
	srv, st := newPostgresTestServer(t, &fakeScraper{})
	ctx := context.Background()
	// Cookie-session auth, so the walk goes through the same request path a real
	// user does rather than a local-user shortcut.
	srv.SetProductionMode(true)

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
	benefit := listingPosting("browser-fixture-benefit", "신입 백엔드 개발자")
	benefit.Company = "가상회사"
	benefit.Description = "Go 백엔드 API를 개발합니다. 서버 개발자를 찾습니다.\n병역특례 가능"
	benefit.Tags = []scraper.Tag{{Name: "병역특례 가능", Category: "welfare"}}
	benefit.FirstSeenAt, benefit.LastSeenAt = now, now
	benefit.ID = mustUpsert(t, st, benefit)

	// The control posting: the phrase really is a role responsibility, so it must
	// stay excluded. Its title deliberately omits the phrase so the row shows
	// 공고 본문 provenance, distinct from the benefit row's 복지 태그.
	requirement := listingPosting("browser-fixture-requirement", "신입 사용자 조사 담당자")
	requirement.Company = "가상연구소"
	requirement.Description = "담당 업무는 리서치 입니다. 사용자 요구를 조사하고 결과를 분석합니다."
	requirement.FirstSeenAt, requirement.LastSeenAt = now, now
	requirement.ID = mustUpsert(t, st, requirement)

	verdicts := map[string]struct {
		verdict ai.DealbreakerVerdict
		reason  ai.DealbreakerReasonCode
	}{
		"병역특례": {ai.DealbreakerNotApplicable, ai.DealbreakerReasonBenefitOrEligibility},
		"리서치":  {ai.DealbreakerApplies, ai.DealbreakerReasonRequirement},
	}
	provider := &ai.StubProvider{
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
	mux := http.NewServeMux()
	// Test-only login: hands the browser the synthetic session cookie so the walk
	// starts on the real, authenticated UI rather than a bespoke preview page.
	mux.HandleFunc("/__fixture/login", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: cookie.Name, Value: cookie.Value, Path: "/"})
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
	// Test-only stop: the documented cleanup path, so nobody has to hunt the
	// process down after inspection.
	mux.HandleFunc("/__fixture/stop", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("browser fixture stopping\n"))
		stopOnce.Do(func() { close(stop) })
	})
	mux.Handle("/", srv.Handler())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on loopback: %v", err)
	}
	addr := ln.Addr().String()
	httpSrv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = httpSrv.Serve(ln) }()
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	})

	t.Logf("BROWSER_FIXTURE_URL=http://%s/__fixture/login", addr)
	t.Logf("BROWSER_FIXTURE_STOP_URL=http://%s/__fixture/stop", addr)
	t.Logf("fixture ready: 2 postings, both excluded, per-call cap 1 — press AI 평가 twice")
	<-stop
}
