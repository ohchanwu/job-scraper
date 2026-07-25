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
	"github.com/ohchanwu/jobcron/internal/scoring"
	"github.com/ohchanwu/jobcron/internal/scraper"
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
		Dealbreakers: []string{"리서치", "병역특례"},
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

	// The 병역특례 incident, reproduced against the live provider: the phrase
	// reaches the matcher only through a structured welfare tag appended to the
	// description, and the correct explanation never repeats it. Under version 1
	// this stayed pending forever.
	benefit := listingPosting("stage1-live-benefit", "신입 서버 개발자")
	benefit.Description = "Go 백엔드 API를 개발합니다. 복지 제도를 폭넓게 지원합니다.\n병역특례 가능"
	benefit.Tags = []scraper.Tag{{Name: "병역특례 가능", Category: "welfare"}}
	benefit.FirstSeenAt, benefit.LastSeenAt = now, now
	benefitID := mustUpsert(t, st, benefit)

	// Seed the independent Stage 1A and Stage 2 caches so this paid gate spends
	// only on the three Stage 1B judgments it is intended to verify.
	for _, seeded := range []struct {
		id int64
		p  scraper.Posting
	}{
		{id: notApplicableID, p: notApplicable},
		{id: appliesID, p: applies},
		{id: benefitID, p: benefit},
	} {
		p := seeded.p
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
	for _, id := range []int64{notApplicableID, appliesID, benefitID} {
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
	if first.ProviderCalls != 3 {
		t.Fatalf("first provider calls = %d, want exactly 3 Stage 1B calls", first.ProviderCalls)
	}
	if extract, validation, scoreDelta := provider.counts(); extract != 0 || validation != 3 || scoreDelta != 0 {
		t.Fatalf("paid call split = extract:%d validation:%d score:%d, want 0:3:0",
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
	assertLiveServerMatch(t, "not_applicable", notApplicable, prof, notApplicableValidation)
	if notApplicableValidation.Validation.Verdict != ai.DealbreakerNotApplicable {
		t.Fatalf("negated research phrase verdict = %q, want not_applicable",
			notApplicableValidation.Validation.Verdict)
	}

	appliesValidation := onlyLiveValidation(t, "applies", validations[appliesID], provider)
	assertLiveServerMatch(t, "applies", applies, prof, appliesValidation)
	if appliesValidation.Validation.Verdict != ai.DealbreakerApplies {
		t.Fatalf("research responsibility verdict = %q, want applies", appliesValidation.Validation.Verdict)
	}
	switch appliesValidation.Validation.ReasonCode {
	case ai.DealbreakerReasonRequirement, ai.DealbreakerReasonResponsibility, ai.DealbreakerReasonExpectedCondition:
	default:
		t.Fatalf("applies reason code = %q, want a requirement-family code",
			appliesValidation.Validation.ReasonCode)
	}

	// The version-2 acceptance case: a benefit reading persists even though the
	// model's explanation need not repeat 병역특례, and the stored provenance is
	// the welfare tag the deterministic matcher actually hit.
	benefitValidation := onlyLiveValidation(t, "benefit", validations[benefitID], provider)
	assertLiveServerMatch(t, "benefit", benefit, prof, benefitValidation)
	if benefitValidation.Validation.Verdict != ai.DealbreakerNotApplicable {
		t.Fatalf("welfare-tag verdict = %q, want not_applicable", benefitValidation.Validation.Verdict)
	}
	if benefitValidation.Validation.ReasonCode != ai.DealbreakerReasonBenefitOrEligibility {
		t.Fatalf("welfare-tag reason code = %q, want benefit_or_eligibility",
			benefitValidation.Validation.ReasonCode)
	}
	if benefitValidation.Match.Source != ai.DealbreakerMatchStructuredTag ||
		benefitValidation.Match.Category != "welfare" {
		t.Fatalf("welfare-tag provenance = %+v, want the structured welfare tag", benefitValidation.Match)
	}

	brief, err := srv.buildBriefingWithRuntime(ctx, now, userID, runtime)
	if err != nil {
		t.Fatalf("build live briefing: %T", err)
	}
	restored := map[int64]bool{}
	for _, dp := range brief.Today {
		restored[dp.Posting.ID] = true
	}
	if !restored[notApplicableID] || !restored[benefitID] {
		t.Fatal("not_applicable postings did not re-enter Today")
	}
	if len(brief.Excluded) != 1 || brief.Excluded[0].Posting.ID != appliesID {
		t.Fatal("applies posting did not remain excluded")
	}
	if got := renderedEvidence(brief.Excluded[0].ExclusionReasons); got != appliesValidation.Match.Evidence {
		t.Fatalf("excluded card rendered %q, want the deterministic server match", got)
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

// assertLiveServerMatch proves the persisted row carries the server's own
// deterministic match, and that any optional reason evidence the live provider
// chose to send is grounded in the full Stage 1B input without ever standing in
// for that match.
func assertLiveServerMatch(
	t *testing.T,
	label string,
	p scraper.Posting,
	prof profile.Profile,
	row storage.AIDealbreakerValidation,
) {
	t.Helper()
	var want ai.DealbreakerMatch
	for _, candidate := range scoring.DealbreakerCandidates(p, prof) {
		if candidate.ID == row.KeywordHash {
			want = candidate.Match
		}
	}
	if want.Evidence == "" {
		t.Fatalf("%s: stored row has no matching server candidate", label)
	}
	if row.Match != want {
		t.Fatalf("%s: stored match = %+v, want the server candidate match %+v", label, row.Match, want)
	}
	modelText, _ := ai.DealbreakerModelInput(p)
	if quote := row.Validation.ReasonEvidence; quote != "" && !strings.Contains(modelText, quote) {
		t.Fatalf("%s: persisted reason evidence is not grounded in the model input", label)
	}
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
