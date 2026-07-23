package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ohchanwu/jobcron/internal/ai"
	"github.com/ohchanwu/jobcron/internal/profile"
)

// failingScoreDeltaStub is a provider whose ScoreDelta always fails with a typed
// APIError of the given HTTP status + body — the shape a provider error (a bad
// key 401, a model the provider rejects 404, a rate limit 429) takes.
func failingScoreDeltaStub(status int, body string) *ai.StubProvider {
	return &ai.StubProvider{
		NameVal: "stub",
		ScoreDeltaFn: func(ctx context.Context, modelText, profileText string) ([]ai.RawDeltaItem, ai.Usage, error) {
			return nil, ai.Usage{}, &ai.APIError{Provider: "anthropic", Status: status, Body: body}
		},
	}
}

// TestRerateSurfacesProviderError is the Bug 2 regression: when every row's
// ScoreDelta fails (bad key, bad model, or a rate limit), the re-rate must end in
// a calm, SPECIFIC "failed" event — not a hollow "0/N analyzed, press again" that
// silently blames token-saving for a hard provider error.
func TestRerateSurfacesProviderError(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantSub string
	}{
		{"bad model 404", http.StatusNotFound, `{"error":{"code":"model_not_found"}}`, "선택한 모델이 이 제공자와 맞지 않아요"},
		{"bad model 400", http.StatusBadRequest, `{"error":{"message":"bad request"}}`, "선택한 모델이 이 제공자와 맞지 않아요"},
		{"bad key 401", http.StatusUnauthorized, `{"error":{"message":"invalid api key"}}`, "AI 키를 확인해주세요"},
		{"no quota 429", http.StatusTooManyRequests, `{"error":{"type":"insufficient_quota"}}`, "결제"},
		{"rate limited 429", http.StatusTooManyRequests, `{"error":{"type":"rate_limit_exceeded"}}`, "잠시"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _, _ := seedRerate(t)
			runtime := testAIRuntime(1, failingScoreDeltaStub(tc.status, tc.body), "test-model")
			_, err := srv.runRerate(context.Background(), "today", noopEmit, 1, runtime)
			var pce *providerCallError
			if !errors.As(err, &pce) {
				t.Fatalf("runRerate error = %v, want providerCallError", err)
			}
			if msg := providerFailureMessage(pce.err); !strings.Contains(msg, tc.wantSub) {
				t.Fatalf("failed message %q missing %q", msg, tc.wantSub)
			}
		})
	}
}

func TestProviderFailureMessageClassifies(t *testing.T) {
	const privateMarker = "private-provider-output-marker"
	cases := []struct {
		err     error
		wantSub string
	}{
		{&ai.APIError{Status: http.StatusUnauthorized, Body: privateMarker}, "AI 키를 확인해주세요"},
		{&ai.APIError{Status: http.StatusForbidden, Body: privateMarker}, "AI 키를 확인해주세요"},
		{&ai.APIError{Status: http.StatusBadRequest, Body: privateMarker}, "선택한 모델이 이 제공자와 맞지 않아요"},
		{&ai.APIError{Status: http.StatusNotFound, Body: privateMarker}, "선택한 모델이 이 제공자와 맞지 않아요"},
		{&ai.APIError{Status: http.StatusTooManyRequests, Body: `{"error":{"type":"insufficient_quota","detail":"` + privateMarker + `"}}`}, "결제"},
		{&ai.APIError{Status: http.StatusTooManyRequests, Body: `{"error":{"type":"rate_limit_exceeded","detail":"` + privateMarker + `"}}`}, "잠시"},
		{&ai.APIError{Status: http.StatusInternalServerError, Body: privateMarker}, "(500)"},
		{errors.New("malformed provider output: " + privateMarker), "AI 분석에 실패했어요"},
	}
	for _, tc := range cases {
		got := providerFailureMessage(tc.err)
		if !strings.Contains(got, tc.wantSub) {
			t.Errorf("providerFailureMessage(%v) = %q, want substring %q", tc.err, got, tc.wantSub)
		}
		if strings.Contains(got, privateMarker) {
			t.Errorf("providerFailureMessage leaked provider output: %q", got)
		}
	}
}

// TestModelSwitchKeepsAIChipStale is the Bug 1 regression: changing the AI model
// (or provider) rotates ai_version and orphans the prior ai_scores row from the
// version-scoped lookups. The cross-version fallback must keep the chip rendered —
// faded with "(이전 설정 기준)" — instead of letting it vanish entirely.
func TestModelSwitchKeepsAIChipStale(t *testing.T) {
	srv, _ := newTestServer(t, &fakeScraper{})
	ctx := context.Background()
	zero := 0
	prof := profile.Profile{CareerYears: 0, MinScore: &zero, JobLikes: "백엔드 서버 개발"}
	pj, _ := profile.Marshal(prof)
	if _, _, err := srv.store.SaveProfile(ctx, pj); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	now := time.Now().UTC()
	p := listingPosting("sw1", "신입 백엔드")
	p.Description = "서버 개발자를 찾습니다"
	p.FirstSeenAt, p.LastSeenAt = now, now
	id, _, _ := srv.store.UpsertPosting(ctx, p)

	// Model A (haiku) rated the posting: cache a delta under A's ai_version and
	// the current goal hash, exactly as a 재평가 under A would.
	runtimeA := testAIRuntime(1, &ai.StubProvider{NameVal: "anthropic"}, "claude-haiku-4-5-20251001")
	delta := ai.Delta{NetDelta: 7, Items: []ai.DeltaItem{
		{Signal: "백엔드", Kind: ai.KindPresence, Delta: 7, Evidence: "서버 개발자를 찾습니다", MatchedGoal: "좋아하는 업무"},
	}}
	if err := srv.store.UpsertAIScore(ctx, 1, id, profile.AIInputHash(prof), runtimeA.ScoreVersion, delta, now); err != nil {
		t.Fatalf("seed delta under provider A: %v", err)
	}
	if _, err := srv.scoreAll(ctx, 1, runtimeA); err != nil {
		t.Fatalf("scoreAll under A: %v", err)
	}

	bodyA := renderDashboard(t, srv)
	if !strings.Contains(bodyA, "AI 분석") {
		t.Fatalf("provider A: AI chip should render fresh:\n%s", bodyA)
	}
	if strings.Contains(bodyA, "이전 설정 기준") {
		t.Fatalf("provider A: a freshly-rated chip must NOT be marked stale")
	}

	// Switch to model B (sonnet) → ai_version rotates. Without the cross-version
	// fallback, both the fresh and version-scoped stale lookups miss and the chip
	// vanishes. With it, the chip persists, faded.
	runtimeB := testAIRuntime(1, &ai.StubProvider{NameVal: "anthropic"}, "claude-sonnet-4-6")
	if _, err := srv.scoreAll(ctx, 1, runtimeB); err != nil {
		t.Fatalf("scoreAll under B: %v", err)
	}

	bodyB := renderDashboard(t, srv)
	if !strings.Contains(bodyB, "AI 분석") {
		t.Fatalf("BUG 1: AI chip VANISHED after a model switch instead of going stale:\n%s", bodyB)
	}
	if !strings.Contains(bodyB, "이전 설정 기준") {
		t.Fatalf("BUG 1: chip not marked stale ('이전 설정 기준') after a model switch:\n%s", bodyB)
	}
	// The stale delta is still summed into the Total (the chosen behavior).
	scores, _ := srv.store.ScoresByPostingID(ctx)
	if sc, ok := scores[id]; !ok || sc.Total < 7 {
		t.Fatalf("stale AI +7 must still count toward the Total; got %+v", scores[id])
	}
}

// TestProfileFormRendersModelDropdown is the Bug 2B regression: the model field
// is a <select> of the provider's models, not free text, and ships the model map
// for the client-side swap.
func TestProfileFormRendersModelDropdown(t *testing.T) {
	srv, _ := newTestServer(t, &fakeScraper{})
	ctx := context.Background()
	prof := profile.Profile{AIProvider: "anthropic", AIModel: "claude-haiku-4-5-20251001"}
	pj, _ := profile.Marshal(prof)
	if _, _, err := srv.store.SaveProfile(ctx, pj); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/profile", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<select name="ai_model"`) {
		t.Fatalf("model field must be a <select>, not free text:\n%s", body)
	}
	if !strings.Contains(body, `<option value="claude-haiku-4-5-20251001"`) {
		t.Fatalf("anthropic model options missing from the dropdown:\n%s", body)
	}
	for _, want := range []string{
		`value="anthropic"`,
		`Anthropic (Claude)`,
		`value="openai"`,
		`OpenAI`,
		`value="gemini"`,
		`Google Gemini`,
		`"openai":["gpt-5.6-luna"]`,
		`"gemini":["gemini-3.5-flash-lite"]`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("profile form missing server-owned provider option %q:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "window.aiModelOptions") {
		t.Fatal("missing the client-side model-options data island")
	}
}

func TestProfileSaveRejectsInvalidProviderModelSelection(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
	}{
		{name: "unknown provider", provider: "groq", model: ""},
		{name: "anthropic with openai model", provider: "anthropic", model: "gpt-5.6-luna"},
		{name: "openai with gemini model", provider: "openai", model: "gemini-3.5-flash-lite"},
		{name: "model while AI off", provider: "", model: "claude-haiku-4-5-20251001"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newTestServer(t, &fakeScraper{})
			seed, _ := profile.Marshal(profile.Profile{JobLikes: "unchanged"})
			if _, _, err := srv.store.SaveProfile(context.Background(), seed); err != nil {
				t.Fatalf("seed profile: %v", err)
			}

			form := url.Values{
				"ai_provider": {tt.provider},
				"ai_model":    {tt.model},
				"job_likes":   {"must not persist"},
			}
			req := httptest.NewRequest(http.MethodPost, "/profile", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%q", rec.Code, rec.Body.String())
			}
			got, _, found, err := srv.store.Profile(context.Background())
			if err != nil || !found || got != seed {
				t.Fatalf("profile changed after rejected selection: found=%v err=%v got=%q want=%q", found, err, got, seed)
			}
		})
	}
}

// TestProfileFormForeignModelNotSelectable proves a saved model id that isn't in
// the provider's list (e.g. a leftover from a removed provider) is not a
// selectable <option>, so it can't be re-submitted — the form falls back to the
// default on save.
func TestProfileFormForeignModelNotSelectable(t *testing.T) {
	srv, _ := newTestServer(t, &fakeScraper{})
	ctx := context.Background()
	// A foreign model id (a removed OpenAI model) stranded under anthropic.
	prof := profile.Profile{AIProvider: "anthropic", AIModel: "gpt-4o-mini"}
	pj, _ := profile.Marshal(prof)
	if _, _, err := srv.store.SaveProfile(ctx, pj); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/profile", nil))
	body := rec.Body.String()
	if strings.Contains(body, `<option value="gpt-4o-mini"`) {
		t.Fatalf("a foreign model must not be a selectable option under anthropic:\n%s", body)
	}
	if !strings.Contains(body, `<option value="claude-haiku-4-5-20251001"`) {
		t.Fatalf("anthropic options should be the model choices:\n%s", body)
	}
}

// renderDashboard GETs "/" and returns the HTML body.
func renderDashboard(t *testing.T, srv *Server) string {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}
