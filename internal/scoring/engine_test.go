package scoring

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/ohchanwu/jobcron/internal/ai"
	"github.com/ohchanwu/jobcron/internal/profile"
	"github.com/ohchanwu/jobcron/internal/scraper"
)

// basePosting scores 0 in every category: senior career, no stacks, no tags,
// no location. Tests add only the field(s) they exercise.
func basePosting() scraper.Posting {
	return scraper.Posting{MinCareer: 10, MaxCareer: 20}
}

// baseProfile scores 0 in every category: 신입, no stacks/location/salary, no
// keywords.
func baseProfile() profile.Profile {
	return profile.Profile{CareerYears: 0}
}

func lineDelta(r ScoreResult, label string) (int, bool) {
	for _, li := range r.Breakdown {
		if li.Label == label {
			return li.Delta, true
		}
	}
	return 0, false
}

func TestScoreStacksSumsMatchedWeights(t *testing.T) {
	p := basePosting()
	p.StackTags = []string{"React", "TypeScript", "Java"}
	prof := baseProfile()
	prof.Stacks = []profile.StackPref{
		{Name: "react", Weight: 20}, // case-insensitive match
		{Name: "TypeScript", Weight: 10},
		{Name: "Go", Weight: 30}, // not tagged on the posting
	}

	r := scoreNoAI(p, prof)
	if r.Total != 30 {
		t.Errorf("Total = %d, want 30 (20 + 10 matched stacks)", r.Total)
	}
	if d, ok := lineDelta(r, "React"); !ok || d != 20 {
		t.Errorf("React line item = (%d,%v), want (20,true)", d, ok)
	}
	if d, ok := lineDelta(r, "TypeScript"); !ok || d != 10 {
		t.Errorf("TypeScript line item = (%d,%v), want (10,true)", d, ok)
	}
	if _, ok := lineDelta(r, "Go"); ok {
		t.Error("Go line item present, want absent (no matching tag)")
	}
}

func TestScoreStacksCapsAt50(t *testing.T) {
	p := basePosting()
	p.StackTags = []string{"A", "B", "C"}
	prof := baseProfile()
	prof.Stacks = []profile.StackPref{
		{Name: "A", Weight: 40},
		{Name: "B", Weight: 40},
		{Name: "C", Weight: 40},
	}
	if r := scoreNoAI(p, prof); r.Total != 50 {
		t.Errorf("Total = %d, want 50 (stack sum 120 capped at 50)", r.Total)
	}
}

// TestScoreCareerHonorsCustomWeight is the regression guard for the
// 2026-05-28 per-category-weights change: a profile with no CareerWeight
// set still scores the historical 25/10, and bumping CareerWeight to 40
// scales both the exact and near-miss awards by the same ratio.
func TestScoreCareerHonorsCustomWeight(t *testing.T) {
	p := basePosting()
	p.Newcomer, p.MinCareer, p.MaxCareer = true, 0, 0
	prof := baseProfile() // CareerWeight=0 → Effective=25
	if r := scoreNoAI(p, prof); r.Total != 25 {
		t.Errorf("default CareerWeight → Total = %d, want 25", r.Total)
	}

	prof.CareerWeight = 40
	if r := scoreNoAI(p, prof); r.Total != 40 {
		t.Errorf("CareerWeight=40 → Total = %d, want 40 (exact match award)", r.Total)
	}

	// Near-miss: 신입 profile, 1-3년 posting (adjacent). Near-miss
	// award is round(weight * 2/5): 40 * 2/5 = 16.
	p.Newcomer, p.MinCareer, p.MaxCareer = false, 1, 3
	if r := scoreNoAI(p, prof); r.Total != 16 {
		t.Errorf("CareerWeight=40 near-miss → Total = %d, want 16 (40 * 2/5)", r.Total)
	}
}

// TestScoreSalaryHonorsCustomWeight mirrors TestScoreCareerHonorsCustomWeight
// for the salary category. Clear-award is the user's SalaryWeight;
// ambiguous-award is half (round-half-up).
func TestScoreSalaryHonorsCustomWeight(t *testing.T) {
	p := basePosting()
	p.Tags = []scraper.Tag{{Category: "salary", Name: "평균연봉 5,000 이상"}}

	prof := baseProfile() // SalaryWeight=0 → Effective=10
	prof.SalaryFloorKRW = 40_000_000
	if r := scoreNoAI(p, prof); r.Total != 10 {
		t.Errorf("default SalaryWeight → Total = %d, want 10", r.Total)
	}

	prof.SalaryWeight = 30
	if r := scoreNoAI(p, prof); r.Total != 30 {
		t.Errorf("SalaryWeight=30 → Total = %d, want 30 (clear-award)", r.Total)
	}

	// Ambiguous (rate-only) — half of clear, rounded: 30 / 2 = 15.
	p.Tags = []scraper.Tag{{Category: "salary", Name: "연봉상승률 15% 이상"}}
	if r := scoreNoAI(p, prof); r.Total != 15 {
		t.Errorf("SalaryWeight=30 ambiguous → Total = %d, want 15 (30 / 2)", r.Total)
	}
}

func TestScoreCareer(t *testing.T) {
	cases := []struct {
		name     string
		years    int
		newcomer bool
		min, max int
		want     int
	}{
		{"신입 profile, newcomer posting", 0, true, 0, 0, 25},
		{"신입 profile, 1-3년 posting (adjacent)", 0, false, 1, 3, 10},
		{"신입 profile, senior posting", 0, false, 5, 10, 0},
		{"3년 profile, 1-5년 posting (in range)", 3, false, 1, 5, 25},
		{"3년 profile, 5-10년 posting (too far)", 3, false, 5, 10, 0},
		{"2년 profile, 3-6년 posting (adjacent)", 2, false, 3, 6, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := basePosting()
			p.Newcomer, p.MinCareer, p.MaxCareer = tc.newcomer, tc.min, tc.max
			prof := baseProfile()
			prof.CareerYears = tc.years
			if r := scoreNoAI(p, prof); r.Total != tc.want {
				t.Errorf("Total = %d, want %d", r.Total, tc.want)
			}
		})
	}
}

func TestScoreSalary(t *testing.T) {
	salaryTag := func(name string) scraper.Tag {
		return scraper.Tag{Category: "salary", Name: name}
	}
	cases := []struct {
		name  string
		tags  []scraper.Tag
		floor int
		want  int
	}{
		{"no salary tag", []scraper.Tag{{Category: "welfare", Name: "휴가비 지원"}}, 0, 0},
		{"no tags at all", nil, 50_000_000, 0},
		{"salary tag clears the floor", []scraper.Tag{salaryTag("평균연봉 6,000 이상")}, 50_000_000, 10},
		{"salary tag below the floor", []scraper.Tag{salaryTag("평균연봉 3,000 이상")}, 50_000_000, 0},
		{"rate-only tag is ambiguous", []scraper.Tag{salaryTag("연봉상승률 15% 이상")}, 50_000_000, 5},
		{"salary tag with no floor set", []scraper.Tag{salaryTag("평균연봉 4,000 이상")}, 0, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := basePosting()
			p.Tags = tc.tags
			prof := baseProfile()
			prof.SalaryFloorKRW = tc.floor
			if r := scoreNoAI(p, prof); r.Total != tc.want {
				t.Errorf("Total = %d, want %d", r.Total, tc.want)
			}
		})
	}
}

func TestScoreSortsBreakdownByDelta(t *testing.T) {
	// A low stack weight makes the natural append order (stack, career,
	// location, salary) NOT delta-descending, so this catches a missing sort.
	p := scraper.Posting{
		Newcomer:  true,
		StackTags: []string{"React"},
		Location:  "서울 강남구",
		Tags:      []scraper.Tag{{Category: "salary", Name: "평균연봉 6,000 이상"}},
	}
	prof := profile.Profile{
		CareerYears:    0,
		Stacks:         []profile.StackPref{{Name: "React", Weight: 5}},
		Location:       profile.LocationPref{Cities: []string{"서울"}, Weight: 15},
		SalaryFloorKRW: 50_000_000,
	}
	r := scoreNoAI(p, prof)
	if r.Total != 55 {
		t.Errorf("Total = %d, want 55 (5 + 25 + 15 + 10)", r.Total)
	}
	for i := 1; i < len(r.Breakdown); i++ {
		if r.Breakdown[i-1].Delta < r.Breakdown[i].Delta {
			t.Fatalf("breakdown not sorted by delta desc: %+v", r.Breakdown)
		}
	}
	if r.Breakdown[0].Delta != 25 {
		t.Errorf("breakdown[0].Delta = %d, want 25 (career, the largest)", r.Breakdown[0].Delta)
	}
}

func TestScorePerfectPostingScores100(t *testing.T) {
	p := scraper.Posting{
		Newcomer:  true,
		StackTags: []string{"React"},
		Location:  "서울 강남구",
		Tags:      []scraper.Tag{{Category: "salary", Name: "평균연봉 6,000 이상"}},
	}
	prof := profile.Profile{
		CareerYears:    0,
		Stacks:         []profile.StackPref{{Name: "React", Weight: 50}},
		Location:       profile.LocationPref{Cities: []string{"서울"}, Weight: 15},
		SalaryFloorKRW: 50_000_000,
	}
	r := scoreNoAI(p, prof)
	if r.Total != 100 {
		t.Errorf("Total = %d, want 100 (50 + 25 + 15 + 10)", r.Total)
	}
	if len(r.Breakdown) != 4 {
		t.Errorf("breakdown has %d items, want 4", len(r.Breakdown))
	}
}

func TestScoreDealbreakerKeyword(t *testing.T) {
	p := basePosting()
	p.Description = "백엔드 개발자 모집. 병특 지원은 받지 않습니다."
	p.StackTags = []string{"React"} // would otherwise contribute points
	prof := baseProfile()
	prof.Stacks = []profile.StackPref{{Name: "React", Weight: 30}}
	prof.Dealbreakers = []string{"병특"}

	r := scoreNoAI(p, prof)
	if r.Total != -1 {
		t.Errorf("Total = %d, want -1 (dealbreaker keyword hit)", r.Total)
	}
	if r.DealbreakerHit == nil || r.DealbreakerHit.Kind != "keyword" || r.DealbreakerHit.Phrase != "병특" {
		t.Errorf("DealbreakerHit = %+v, want {keyword 병특}", r.DealbreakerHit)
	}
	if r.Breakdown != nil {
		t.Errorf("Breakdown = %+v, want nil on a dealbreaker hit", r.Breakdown)
	}
}

func TestScoreDealbreakerKeywordIsTokenExact(t *testing.T) {
	// "병특" must NOT match the single token "병특혜택없음" — the same
	// token-exact behavior the Step 0 spike validated.
	p := basePosting()
	p.Description = "복지: 병특혜택없음"
	prof := baseProfile()
	prof.Dealbreakers = []string{"병특"}
	if r := scoreNoAI(p, prof); r.Total == -1 {
		t.Error("dealbreaker '병특' wrongly matched the token '병특혜택없음'")
	}
}

func TestDealbreakerCandidatesReturnsEveryMatchInProfileOrder(t *testing.T) {
	p := basePosting()
	p.Description = "이 포지션은 사용자 리서치 아님. 야근 업무를 담당합니다."
	prof := baseProfile()
	prof.Dealbreakers = []string{"사용자 리서치", "!!!", "야근"}

	got := DealbreakerCandidates(p, prof)
	if len(got) != 2 {
		t.Fatalf("candidates = %+v, want two real matches", got)
	}
	for i, tc := range []struct {
		phrase    string
		canonical string
	}{
		{"사용자 리서치", "사용자\x00리서치"},
		{"야근", "야근"},
	} {
		sum := sha256.Sum256([]byte(tc.canonical))
		wantID := hex.EncodeToString(sum[:])
		if got[i].Phrase != tc.phrase || got[i].ID != wantID || len(got[i].ID) != 64 {
			t.Errorf("candidate %d = %+v, want phrase=%q id=%q", i, got[i], tc.phrase, wantID)
		}
	}
}

func TestScoreSuppressesHitOnlyWhenNotApplicable(t *testing.T) {
	p := basePosting()
	p.Description = "리서치 아님"
	prof := baseProfile()
	prof.Dealbreakers = []string{"리서치"}
	candidate := DealbreakerCandidates(p, prof)[0]

	r := Score(p, prof, nil, nil, map[string]ai.DealbreakerValidation{
		candidate.ID: {
			CandidateID:    candidate.ID,
			Verdict:        ai.DealbreakerNotApplicable,
			ReasonCode:     ai.DealbreakerReasonExplicitlyNegated,
			ReasonEvidence: "리서치 아님",
		},
	})
	if r.Total == -1 || r.DealbreakerHit != nil {
		t.Fatalf("not_applicable hit was retained: %+v", r)
	}
	for _, reason := range r.ExclusionReasons {
		if reason.Kind == "keyword" {
			t.Fatalf("suppressed keyword remained in reasons: %+v", r.ExclusionReasons)
		}
	}
}

func TestScoreRejectsMismatchedValidationCandidateID(t *testing.T) {
	p := basePosting()
	p.Description = "리서치 아님"
	prof := baseProfile()
	prof.Dealbreakers = []string{"리서치"}
	candidate := DealbreakerCandidates(p, prof)[0]

	r := Score(p, prof, nil, nil, map[string]ai.DealbreakerValidation{
		candidate.ID: {
			CandidateID:    "different-candidate",
			Verdict:        ai.DealbreakerNotApplicable,
			ReasonCode:     ai.DealbreakerReasonExplicitlyNegated,
			ReasonEvidence: "리서치 아님",
		},
	})
	if r.Total != -1 || len(r.ExclusionReasons) != 1 {
		t.Fatalf("mismatched candidate suppressed exclusion: %+v", r)
	}
	if got := r.ExclusionReasons[0]; got.Phrase != "리서치" || got.Confidence != "unverified" || got.Evidence != "" {
		t.Fatalf("mismatched candidate reason = %+v, want conservative unverified exclusion", got)
	}
}

func TestScoreRetainsAppliesUncertainMissingAndUnavailableHits(t *testing.T) {
	p := basePosting()
	p.Description = "사용자 리서치 업무를 수행합니다"
	prof := baseProfile()
	prof.Dealbreakers = []string{"리서치"}
	candidate := DealbreakerCandidates(p, prof)[0]

	// The provider's optional reason evidence is deliberately unrelated text: a
	// rendered reason must quote the server match, never the model.
	const modelQuote = "모델이 고른 전혀 다른 문장"

	tests := []struct {
		name        string
		validations map[string]ai.DealbreakerValidation
		confidence  string
		evidence    string
	}{
		{"applies", map[string]ai.DealbreakerValidation{candidate.ID: {CandidateID: candidate.ID, Verdict: ai.DealbreakerApplies, ReasonCode: ai.DealbreakerReasonRequirement, ReasonEvidence: modelQuote}}, "confirmed", candidate.Match.Evidence},
		{"uncertain", map[string]ai.DealbreakerValidation{candidate.ID: {CandidateID: candidate.ID, Verdict: ai.DealbreakerUncertain, ReasonCode: ai.DealbreakerReasonInsufficientContext}}, "uncertain", candidate.Match.Evidence},
		{"missing", map[string]ai.DealbreakerValidation{}, "unverified", ""},
		{"unavailable", nil, "unverified", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := Score(p, prof, nil, nil, tc.validations)
			if r.Total != -1 || r.DealbreakerHit == nil || r.DealbreakerHit.Phrase != "리서치" {
				t.Fatalf("retained hit = %+v, want keyword dealbreaker", r)
			}
			if len(r.ExclusionReasons) != 1 {
				t.Fatalf("reasons = %+v, want one", r.ExclusionReasons)
			}
			got := r.ExclusionReasons[0]
			if got.Kind != "keyword" || got.Label != "제외 키워드: 리서치" || got.Phrase != "리서치" || got.Confidence != tc.confidence || got.Evidence != tc.evidence {
				t.Errorf("reason = %+v", got)
			}
			if got.Evidence == modelQuote {
				t.Errorf("reason quoted provider reason evidence: %+v", got)
			}
			if got.Source != candidate.Match.Source {
				t.Errorf("reason source = %q, want %q", got.Source, candidate.Match.Source)
			}
		})
	}
}

// TestScoreExclusionReasonCarriesStructuredTagProvenance is the 병역특례
// incident: the phrase reaches the combined text only through a structured
// welfare tag, so the rendered reason must name that tag as its source.
func TestScoreExclusionReasonCarriesStructuredTagProvenance(t *testing.T) {
	p := basePosting()
	p.Description = "백엔드 개발자를 찾습니다.\n병역특례 가능"
	p.Tags = []scraper.Tag{{Name: "병역특례 가능", Category: "welfare"}}
	prof := baseProfile()
	prof.Dealbreakers = []string{"병역특례"}
	candidate := DealbreakerCandidates(p, prof)[0]

	r := Score(p, prof, nil, nil, map[string]ai.DealbreakerValidation{
		candidate.ID: {
			CandidateID:    candidate.ID,
			Verdict:        ai.DealbreakerApplies,
			ReasonCode:     ai.DealbreakerReasonRequirement,
			ReasonEvidence: "지원자는 반드시 근무해야 합니다",
		},
	})
	if len(r.ExclusionReasons) != 1 {
		t.Fatalf("reasons = %+v, want one", r.ExclusionReasons)
	}
	got := r.ExclusionReasons[0]
	want := ExclusionReason{
		Kind:       "keyword",
		Label:      "제외 키워드: 병역특례",
		Phrase:     "병역특례",
		Evidence:   "병역특례 가능",
		Source:     ai.DealbreakerMatchStructuredTag,
		Category:   "welfare",
		Confidence: "confirmed",
	}
	if got != want {
		t.Fatalf("reason = %+v, want %+v", got, want)
	}
}

// TestScoreClearsExclusionOnlyWhenEveryCandidateIsNotApplicable locks the
// multi-candidate rule: one unresolved candidate keeps the whole posting out.
func TestScoreClearsExclusionOnlyWhenEveryCandidateIsNotApplicable(t *testing.T) {
	p := basePosting()
	p.Description = "이 포지션은 리서치 아님. 야근 없음."
	prof := baseProfile()
	prof.Dealbreakers = []string{"리서치", "야근"}
	candidates := DealbreakerCandidates(p, prof)
	if len(candidates) != 2 {
		t.Fatalf("candidates = %+v, want two", candidates)
	}
	notApplicable := func(c ai.DealbreakerCandidate) ai.DealbreakerValidation {
		return ai.DealbreakerValidation{
			CandidateID: c.ID,
			Verdict:     ai.DealbreakerNotApplicable,
			ReasonCode:  ai.DealbreakerReasonExplicitlyNegated,
		}
	}

	partial := Score(p, prof, nil, nil, map[string]ai.DealbreakerValidation{
		candidates[0].ID: notApplicable(candidates[0]),
	})
	if partial.Total != -1 || partial.DealbreakerHit == nil || partial.DealbreakerHit.Phrase != "야근" {
		t.Fatalf("partial clearance restored the posting: %+v", partial)
	}

	all := Score(p, prof, nil, nil, map[string]ai.DealbreakerValidation{
		candidates[0].ID: notApplicable(candidates[0]),
		candidates[1].ID: notApplicable(candidates[1]),
	})
	if all.Total == -1 || all.DealbreakerHit != nil {
		t.Fatalf("full clearance kept the posting excluded: %+v", all)
	}
}

func TestScoreDoesNotHideLaterApplicableHit(t *testing.T) {
	p := basePosting()
	p.Description = "이 포지션은 리서치 아님. 야근 업무를 담당합니다."
	prof := baseProfile()
	prof.Dealbreakers = []string{"리서치", "야근"}
	candidates := DealbreakerCandidates(p, prof)

	r := Score(p, prof, nil, nil, map[string]ai.DealbreakerValidation{
		candidates[0].ID: {CandidateID: candidates[0].ID, Verdict: ai.DealbreakerNotApplicable, ReasonCode: ai.DealbreakerReasonExplicitlyNegated},
		candidates[1].ID: {CandidateID: candidates[1].ID, Verdict: ai.DealbreakerApplies, ReasonCode: ai.DealbreakerReasonResponsibility},
	})
	if r.Total != -1 || r.DealbreakerHit == nil || r.DealbreakerHit.Phrase != "야근" {
		t.Fatalf("result = %+v, want retained later 야근 hit", r)
	}
	if len(r.ExclusionReasons) != 1 || r.ExclusionReasons[0].Phrase != "야근" || r.ExclusionReasons[0].Evidence != candidates[1].Match.Evidence {
		t.Fatalf("reasons = %+v, want only confirmed 야근", r.ExclusionReasons)
	}
}

func TestScoreStructuredEducationCareerAndMinScoreReasons(t *testing.T) {
	t.Run("education with exact evidence", func(t *testing.T) {
		p := basePosting()
		prof := baseProfile()
		prof.MaxEducation = profile.EducationHighSchool
		ext := &ai.Extraction{EducationEnum: ai.EduBachelor, EducationEvidence: "대학교 졸업 이상", Newcomer: true}
		r := Score(p, prof, ext, nil, nil)
		want := ExclusionReason{Kind: "education", Label: "학력 조건 불일치", Phrase: "대졸(4년)", Evidence: "대학교 졸업 이상", Confidence: "confirmed"}
		if len(r.ExclusionReasons) != 1 || r.ExclusionReasons[0] != want {
			t.Fatalf("education reasons = %+v, want %+v", r.ExclusionReasons, want)
		}
	})

	t.Run("career then MinScore", func(t *testing.T) {
		p := basePosting()
		p.Description = "경력 2년 이상의 백엔드 개발자를 찾습니다"
		prof := baseProfile()
		min := 40
		prof.MinScore = &min
		ext := &ai.Extraction{MinCareer: 2, MaxCareer: intPtr(5), CareerEvidence: "경력 2년 이상의 백엔드 개발자", EducationEnum: ai.EduNone}
		r := Score(p, prof, ext, nil, nil)
		if len(r.ExclusionReasons) != 2 {
			t.Fatalf("reasons = %+v, want career and MinScore", r.ExclusionReasons)
		}
		if got := r.ExclusionReasons[0]; got.Kind != "career" || got.Label != "신입 지원 불가" || got.Phrase != "2-5년" || got.Evidence != ext.CareerEvidence || got.Confidence != "confirmed" {
			t.Errorf("career reason = %+v", got)
		}
		if got := r.ExclusionReasons[1]; got.Kind != "min_score" || got.Label != "기준 점수 미달: 0점 / 기준 40점" || got.Phrase != "" || got.Evidence != "" || got.Confidence != "deterministic" {
			t.Errorf("MinScore reason = %+v", got)
		}
	})
}

func TestScoreAcceptsLegacyExtractionWithoutEducationEvidence(t *testing.T) {
	p := basePosting()
	p.EducationName = "대학교졸업(4년) 이상"
	prof := baseProfile()
	prof.MaxEducation = profile.EducationHighSchool
	ext := &ai.Extraction{EducationEnum: ai.EduBachelor, Newcomer: true}

	r := Score(p, prof, ext, nil, nil)
	if r.Total != -1 || len(r.ExclusionReasons) != 1 {
		t.Fatalf("legacy extraction did not retain education exclusion: %+v", r)
	}
	if got := r.ExclusionReasons[0]; got.Kind != "education" || got.Phrase != "대졸(4년)" || got.Evidence != "" || got.Confidence != "confirmed" {
		t.Fatalf("legacy education reason = %+v", got)
	}
}

func TestScoreEducationDealbreaker(t *testing.T) {
	cases := []struct {
		name    string
		eduName string
		maxEdu  profile.EducationLevel
		wantHit bool
	}{
		{"4년제 required, user has only highschool", "대학교졸업(4년) 이상", profile.EducationHighSchool, true},
		{"4년제 required, user has a bachelor's", "대학교졸업(4년) 이상", profile.EducationBachelor, false},
		{"user 학력무관 — filter off", "대학교졸업(4년) 이상", profile.EducationAny, false},
		{"posting itself is 학력무관", "학력무관", profile.EducationHighSchool, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := basePosting()
			p.EducationName = tc.eduName
			prof := baseProfile()
			prof.MaxEducation = tc.maxEdu
			r := scoreNoAI(p, prof)
			gotHit := r.DealbreakerHit != nil && r.DealbreakerHit.Kind == "education"
			if gotHit != tc.wantHit {
				t.Errorf("education dealbreaker = %v, want %v (hit %+v)", gotHit, tc.wantHit, r.DealbreakerHit)
			}
		})
	}
}

func TestScoreLocation(t *testing.T) {
	remoteTag := scraper.Tag{ID: "w1", Name: "재택근무 가능", Category: "welfare"}
	cases := []struct {
		name     string
		location string
		tags     []scraper.Tag
		cities   []string
		weight   int
		remoteOK bool
		want     int
	}{
		{"city substring match", "서울 강남구 테헤란로", nil, []string{"서울"}, 15, false, 15},
		// User-reported gap (2026-05-28): entering 강남 in cities should
		// match a posting in 강남구. Both directions of the prefix should
		// also work — entering 강남구 should match a posting that lists
		// just 강남, and 서울 should match 서울특별시.
		{"강남 matches 강남구", "서울 강남구 역삼동", nil, []string{"강남"}, 15, false, 15},
		{"강남구 matches 강남구", "서울 강남구 역삼동", nil, []string{"강남구"}, 15, false, 15},
		{"서울 matches 서울특별시", "서울특별시 송파구", nil, []string{"서울"}, 15, false, 15},
		// Adjacent district must NOT match — 강남 should not accept 강북.
		{"강남 does NOT match 강북구", "서울 강북구 미아동", nil, []string{"강남"}, 15, false, 0},
		{"no city match", "부산 해운대구", nil, []string{"서울", "판교"}, 15, false, 0},
		{"weight below the cap", "서울 마포구", nil, []string{"서울"}, 10, false, 10},
		{"weight above the cap clamps", "서울 마포구", nil, []string{"서울"}, 99, false, 15},
		{"remote OK + remote tag", "부산 해운대구", []scraper.Tag{remoteTag}, []string{"서울"}, 15, true, 15},
		{"remote tag but RemoteOK off", "부산", []scraper.Tag{remoteTag}, []string{"서울"}, 15, false, 0},
		{"remote OK but no remote tag", "부산", nil, []string{"서울"}, 15, true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := basePosting()
			p.Location = tc.location
			p.Tags = tc.tags
			prof := baseProfile()
			prof.Location = profile.LocationPref{Cities: tc.cities, Weight: tc.weight, RemoteOK: tc.remoteOK}
			if r := scoreNoAI(p, prof); r.Total != tc.want {
				t.Errorf("Total = %d, want %d", r.Total, tc.want)
			}
		})
	}
}
