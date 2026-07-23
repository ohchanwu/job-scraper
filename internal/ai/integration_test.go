//go:build integration

package ai

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ohchanwu/jobcron/internal/scraper"
)

// Live BYOK provider tests. Excluded from normal runs by the `integration` build
// tag AND skipped unless the provider's key env var is set, so they never run in
// CI and never spend tokens without an explicit opt-in. Run one with:
//
//	JOBCRON_ANTHROPIC_KEY=sk-ant-... go test -tags integration -run Live ./internal/ai/
//	JOBCRON_TEST_OPENAI_API_KEY=sk-... go test -tags integration -run LiveOpenAI ./internal/ai/
//	JOBCRON_TEST_GEMINI_API_KEY=...    go test -tags integration -run LiveGemini ./internal/ai/
//
// They confirm the REAL request/response shape and JSON-mode parsing — the
// external-surface check a stub cannot give: that the live model honors the
// extraction/scoring JSON contract our gates validate.

// liveSamplePosting is a realistic 신입 backend JD for the live calls.
func liveSamplePosting() scraper.Posting {
	return scraper.Posting{
		Title:       "신입 백엔드 개발자 채용",
		Company:     "테스트컴퍼니",
		Location:    "서울 강남구",
		Description: "Java/Spring 기반 서버를 개발합니다. 학력 무관, 경력 무관(신입 환영). 주 5일 근무, 재택 병행 가능. 코드 리뷰 문화가 있습니다.",
	}
}

func liveProvider(t *testing.T, name, envKey, model string) Provider {
	t.Helper()
	key := os.Getenv(envKey)
	if key == "" {
		t.Skipf("%s not set — skipping live %s test", envKey, name)
	}
	if m := os.Getenv("JOBCRON_AI_MODEL"); m != "" {
		model = m // let the runner override the model
	}
	p, err := New(name, key, model, time.Second)
	if err != nil {
		t.Fatalf("construct live %s provider", name)
	}
	return p
}

// runLiveContract exercises Extract + ScoreDelta + the citation gate against a
// real provider and asserts the JSON contracts hold.
func runLiveContract(t *testing.T, p Provider) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	post := liveSamplePosting()
	sent, _, _ := ModelInput(post)

	// Stage 1 — extraction.
	ext, usage, err := p.Extract(ctx, sent)
	if err != nil {
		t.Fatal("live extraction failed")
	}
	if !validEducationEnum[ext.EducationEnum] {
		t.Error("live extraction returned an education enum outside the allowed set")
	}
	if ext.MinCareer < 0 || ext.MinCareer > careerYearsMax {
		t.Error("live extraction returned min_career outside the allowed range")
	}
	if usage.InputTokens == 0 {
		t.Error("usage input_tokens = 0 — the provider's usage block was not parsed")
	}

	// Stage 2 — score delta + citation gate. The model may legitimately return
	// zero items for this JD; what must hold is that the reply PARSES and every
	// surviving presence quote is real (the gate guarantees it).
	profileText := "좋아하는 업무: 백엔드 서버 개발\n피하고 싶은 업무: 잦은 야근"
	raw, usage2, err := p.ScoreDelta(ctx, sent, profileText)
	if err != nil {
		t.Fatal("live score delta failed")
	}
	if usage2.InputTokens == 0 {
		t.Error("ScoreDelta usage input_tokens = 0")
	}
	delta := GateDelta(raw, sent, post.Description)
	for _, it := range delta.Items {
		if it.Kind == KindPresence && !tokenSubsequence(sent, it.Evidence) {
			t.Error("a surviving presence item's evidence is not in the sent text")
		}
	}
}

func TestLiveAnthropic(t *testing.T) {
	runLiveContract(t, liveProvider(t, "anthropic", "JOBCRON_ANTHROPIC_KEY", DefaultModel("anthropic")))
}

func TestLiveOpenAI(t *testing.T) {
	runLiveContract(t, liveProvider(t, "openai", "JOBCRON_TEST_OPENAI_API_KEY", DefaultModel("openai")))
}

func TestLiveGemini(t *testing.T) {
	runLiveContract(t, liveProvider(t, "gemini", "JOBCRON_TEST_GEMINI_API_KEY", DefaultModel("gemini")))
}
