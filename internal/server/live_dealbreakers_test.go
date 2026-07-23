//go:build liveprovider

package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ohchanwu/jobcron/internal/ai"
	"github.com/ohchanwu/jobcron/internal/profile"
	"github.com/ohchanwu/jobcron/internal/storage"
)

// countingLiveProvider proves cache reuse without exposing prompts, responses,
// or credentials in test output.
type countingLiveProvider struct {
	ai.Provider

	mu              sync.Mutex
	extractCalls    int
	validationCalls int
	scoreDeltaCalls int
	outcomes        []string
}

func (p *countingLiveProvider) Extract(ctx context.Context, modelText string) (ai.Extraction, ai.Usage, error) {
	p.mu.Lock()
	p.extractCalls++
	p.mu.Unlock()
	return p.Provider.Extract(ctx, modelText)
}

func (p *countingLiveProvider) ValidateDealbreakers(
	ctx context.Context,
	modelText string,
	candidates []ai.DealbreakerCandidate,
) ([]ai.DealbreakerValidation, ai.Usage, error) {
	p.mu.Lock()
	p.validationCalls++
	p.mu.Unlock()
	validations, usage, err := p.Provider.ValidateDealbreakers(ctx, modelText, candidates)
	outcome := fmt.Sprintf("validations=%d", len(validations))
	if err != nil {
		outcome = fmt.Sprintf("error=%T", err)
		var apiErr *ai.APIError
		if errors.As(err, &apiErr) {
			outcome = fmt.Sprintf("api_status=%d", apiErr.Status)
		}
	} else if len(validations) > 0 {
		verdicts := make([]string, len(validations))
		for i, validation := range validations {
			verdicts[i] = string(validation.Verdict)
		}
		outcome += ":" + strings.Join(verdicts, ",")
	}
	p.mu.Lock()
	p.outcomes = append(p.outcomes, outcome)
	p.mu.Unlock()
	return validations, usage, err
}

func (p *countingLiveProvider) ScoreDelta(
	ctx context.Context,
	modelText string,
	profileText string,
) ([]ai.RawDeltaItem, ai.Usage, error) {
	p.mu.Lock()
	p.scoreDeltaCalls++
	p.mu.Unlock()
	return p.Provider.ScoreDelta(ctx, modelText, profileText)
}

func (p *countingLiveProvider) counts() (extract, validation, scoreDelta int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.extractCalls, p.validationCalls, p.scoreDeltaCalls
}

func (p *countingLiveProvider) validationOutcomes() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.outcomes, ";")
}

func TestLiveStage1BContextualDealbreakers(t *testing.T) {
	key := os.Getenv("JOBCRON_ANTHROPIC_KEY")
	if key == "" {
		t.Skip("JOBCRON_ANTHROPIC_KEY is not configured for the disposable live-provider gate")
	}
	model := ai.DefaultModel("anthropic")
	live, err := ai.New("anthropic", key, model, ai.SuggestedRateLimit("anthropic"))
	if err != nil {
		t.Fatalf("construct disposable live provider: %T", err)
	}
	provider := &countingLiveProvider{Provider: live}

	srv, st := newPostgresTestServer(t, &fakeScraper{})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	userID, _ := createSessionUser(t, st, "stage1-live@example.invalid", "stage1-live-session")
	zero := 0
	prof := profile.Profile{
		CareerYears:  0,
		MinScore:     &zero,
		Dealbreakers: []string{"리서치"},
	}
	saveAIRuntimeProfile(t, st, userID, prof)
	runtime := testAIRuntime(userID, provider, model)

	now := time.Now().UTC()
	notApplicable := listingPosting("stage1-live-not-applicable", "신입 백엔드 개발자")
	notApplicable.Description = "리서치 아님. 이 포지션은 사용자 조사나 시장 조사 업무를 수행하지 않습니다. Go 백엔드 API를 개발합니다."
	notApplicable.FirstSeenAt, notApplicable.LastSeenAt = now, now
	notApplicableID := mustUpsert(t, st, notApplicable)

	applies := listingPosting("stage1-live-applies", "신입 사용자 리서치 개발자")
	applies.Description = "담당 업무는 리서치 입니다. 사용자 요구를 조사하고 결과를 분석하여 제품 방향을 제안합니다."
	applies.FirstSeenAt, applies.LastSeenAt = now, now
	appliesID := mustUpsert(t, st, applies)

	// Seed the independent Stage 1A and Stage 2 caches so this paid gate spends
	// only on the two Stage 1B judgments it is intended to verify.
	for _, seeded := range []struct {
		id      int64
		posting string
	}{
		{id: notApplicableID, posting: "not-applicable"},
		{id: appliesID, posting: "applies"},
	} {
		var p = notApplicable
		if seeded.posting == "applies" {
			p = applies
		}
		_, contentHash, _ := ai.ModelInput(p)
		if err := st.UpsertAIExtraction(ctx, seeded.id, contentHash, ai.ExtractionContractVersion(),
			ai.Extraction{Newcomer: true, EducationEnum: ai.EduNone}, now); err != nil {
			t.Fatalf("seed Stage 1A cache: %T", err)
		}
		if err := st.UpsertAIScore(ctx, userID, seeded.id, profile.AIInputHash(prof),
			runtime.ScoreVersion, ai.Delta{}, now); err != nil {
			t.Fatalf("seed Stage 2 cache: %T", err)
		}
	}

	if _, err := srv.scoreAll(ctx, userID, runtime); err != nil {
		t.Fatalf("seed deterministic scores: %T", err)
	}
	for _, id := range []int64{notApplicableID, appliesID} {
		score, ok, err := st.ScoreByPostingIDForUser(ctx, userID, id)
		if err != nil || !ok || score.Total != -1 {
			t.Fatalf("deterministic exclusion precondition failed for posting %d", id)
		}
	}

	day := now.Format("2006-01-02")
	inBefore, outBefore, err := st.AIUsageForDay(ctx, userID, day)
	if err != nil {
		t.Fatalf("read initial usage: %T", err)
	}
	first, err := srv.runRerate(ctx, "today", noopEmit, userID, runtime)
	if err != nil {
		t.Fatalf("run disposable live rerate: %T", err)
	}
	if first.ProviderCalls != 2 {
		t.Fatalf("first provider calls = %d, want exactly 2 Stage 1B calls", first.ProviderCalls)
	}
	if extract, validation, scoreDelta := provider.counts(); extract != 0 || validation != 2 || scoreDelta != 0 {
		t.Fatalf("paid call split = extract:%d validation:%d score:%d, want 0:2:0",
			extract, validation, scoreDelta)
	}
	inAfter, outAfter, err := st.AIUsageForDay(ctx, userID, day)
	if err != nil {
		t.Fatalf("read usage after live gate: %T", err)
	}
	if inAfter+outAfter <= inBefore+outBefore {
		t.Fatal("Stage 1B provider calls did not debit the disposable user's ledger")
	}

	validations, err := st.AIDealbreakerValidationsByPostingID(ctx, userID, runtime.DealbreakerVersion)
	if err != nil {
		t.Fatalf("read validation cache: %T", err)
	}
	notApplicableValidation := onlyLiveValidation(t, "not_applicable", validations[notApplicableID], provider)
	if notApplicableValidation.Validation.Verdict != ai.DealbreakerNotApplicable ||
		!strings.Contains(notApplicable.Description, notApplicableValidation.Validation.Evidence) {
		t.Fatal("negated research phrase did not produce a citation-gated not_applicable verdict")
	}
	appliesValidation := onlyLiveValidation(t, "applies", validations[appliesID], provider)
	if appliesValidation.Validation.Verdict != ai.DealbreakerApplies ||
		!strings.Contains(applies.Description, appliesValidation.Validation.Evidence) {
		t.Fatal("actual research responsibility did not produce a citation-gated applies verdict")
	}

	brief, err := srv.buildBriefingWithRuntime(ctx, now, userID, runtime)
	if err != nil {
		t.Fatalf("build live briefing: %T", err)
	}
	if len(brief.Today) != 1 || brief.Today[0].Posting.ID != notApplicableID {
		t.Fatal("not_applicable posting did not re-enter Today")
	}
	if len(brief.Excluded) != 1 || brief.Excluded[0].Posting.ID != appliesID {
		t.Fatal("applies posting did not remain excluded")
	}
	if got := renderedEvidence(brief.Excluded[0].ExclusionReasons); got != appliesValidation.Validation.Evidence {
		t.Fatal("excluded card did not render the exact citation-gated evidence quote")
	}

	extractBefore, validationBefore, scoreBefore := provider.counts()
	secondInBefore, secondOutBefore := inAfter, outAfter
	second, err := srv.runRerate(ctx, "today", noopEmit, userID, runtime)
	if err != nil {
		t.Fatalf("run cache-hit rerate: %T", err)
	}
	if second.ProviderCalls != 0 {
		t.Fatalf("cache-hit provider calls = %d, want 0", second.ProviderCalls)
	}
	extractAfter, validationAfter, scoreAfter := provider.counts()
	if extractAfter != extractBefore || validationAfter != validationBefore || scoreAfter != scoreBefore {
		t.Fatal("cache-hit rerun reached the provider")
	}
	secondInAfter, secondOutAfter, err := st.AIUsageForDay(ctx, userID, day)
	if err != nil {
		t.Fatalf("read cache-hit usage: %T", err)
	}
	if secondInAfter != secondInBefore || secondOutAfter != secondOutBefore {
		t.Fatal("cache-hit rerun spent tokens")
	}

	t.Logf("live Stage 1B gate passed: calls=%d, tokens=%d, cache_calls=%d, cache_tokens=%d",
		first.ProviderCalls, (inAfter+outAfter)-(inBefore+outBefore), second.ProviderCalls,
		(secondInAfter+secondOutAfter)-(secondInBefore+secondOutBefore))
}

func onlyLiveValidation(
	t *testing.T,
	label string,
	rows map[string]storage.AIDealbreakerValidation,
	provider *countingLiveProvider,
) storage.AIDealbreakerValidation {
	t.Helper()
	if len(rows) != 1 {
		t.Fatalf("%s validation rows = %d, want 1; sanitized provider outcomes: %s",
			label, len(rows), provider.validationOutcomes())
	}
	for _, row := range rows {
		return row
	}
	panic("unreachable")
}

func renderedEvidence(reasons []exclusionReasonView) string {
	for _, reason := range reasons {
		if !reason.HasEvidence {
			continue
		}
		var evidence strings.Builder
		for _, segment := range reason.Evidence {
			evidence.WriteString(segment.Text)
		}
		return evidence.String()
	}
	return ""
}
