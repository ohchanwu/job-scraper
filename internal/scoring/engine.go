package scoring

import (
	"fmt"
	"sort"

	"github.com/ohchanwu/jobcron/internal/ai"
	"github.com/ohchanwu/jobcron/internal/profile"
	"github.com/ohchanwu/jobcron/internal/scraper"
)

// aiLineLabel is the chip label for the merged Stage-2 AI delta.
const aiLineLabel = "AI 분석"

// LineItem is one contribution to a posting's score. The json tags keep the
// persisted breakdown_json stable; Evidence/Stale are omitempty so non-AI
// line items (and breakdown_json written before v2.0) stay byte-minimal and
// still unmarshal.
type LineItem struct {
	Label    string         `json:"Label"`
	Delta    int            `json:"Delta"`
	Reason   string         `json:"Reason"`
	Evidence []EvidenceItem `json:"evidence,omitempty"` // AI 분석 popover (Stage 2)
	Stale    bool           `json:"stale,omitempty"`    // delta computed vs a prior profile
}

// EvidenceItem is one cited signal behind the AI delta — rendered in the
// Evidence popover (T6). Quote is the model's cited text for a presence
// signal; absence signals carry no quote.
type EvidenceItem struct {
	Kind        string `json:"kind"` // "presence" | "absence"
	Delta       int    `json:"delta"`
	Quote       string `json:"quote,omitempty"`
	MatchedGoal string `json:"matched_goal,omitempty"`
}

// DealbreakerHit records why a posting was excluded from scoring.
type DealbreakerHit struct {
	Kind   string // "keyword" | "education"
	Phrase string // the offending or missing phrase
}

// ExclusionReason is one persisted explanation for an excluded posting.
//
// For a contextual keyword exclusion, Evidence/Source/Category come from the
// server-owned DealbreakerMatch — never from the provider's optional reason
// evidence. The model owns the verdict; the detector owns the proof.
type ExclusionReason struct {
	Kind       string                    `json:"kind"`
	Label      string                    `json:"label"`
	Phrase     string                    `json:"phrase,omitempty"`
	Evidence   string                    `json:"evidence,omitempty"`
	Source     ai.DealbreakerMatchSource `json:"source,omitempty"`
	Category   string                    `json:"category,omitempty"`
	Confidence string                    `json:"confidence"`
}

// ScoreResult is the outcome of scoring one posting against a profile.
type ScoreResult struct {
	Total            int        // 0..100, or -1 on a dealbreaker hit
	Breakdown        []LineItem // contributions, ordered by Delta descending
	DealbreakerHit   *DealbreakerHit
	ExclusionReasons []ExclusionReason `json:"exclusion_reasons,omitempty"`
}

// Score evaluates a posting against a user profile, producing a 0..100 total
// with an explained breakdown.
//
// ext is the cached Stage-1 AI extraction (nil = none): when present,
// scoreCareer and educationDealbreaker prefer the AI career/education facts
// over the regex path. delta is the cached Stage-2 AI score delta (nil = none):
// when present it is merged as one "AI 분석" line item.
//
// A dealbreaker short-circuits FIRST, before any AI delta is merged — a
// dealbroken posting (Total: -1) never carries an AI line item (D3/S4).
func Score(
	p scraper.Posting,
	prof profile.Profile,
	ext *ai.Extraction,
	delta *ai.Delta,
	validations map[string]ai.DealbreakerValidation,
) ScoreResult {
	if hit, reasons := hardExclusionReasons(p, prof, ext, validations); hit != nil {
		return ScoreResult{Total: -1, DealbreakerHit: hit, ExclusionReasons: reasons}
	}

	var breakdown []LineItem
	breakdown = append(breakdown, scoreStacks(p, prof)...)
	if li, ok := scoreCareer(p, prof, ext); ok {
		breakdown = append(breakdown, li)
	}
	if li, ok := scoreLocation(p, prof); ok {
		breakdown = append(breakdown, li)
	}
	if li, ok := scoreSalary(p, prof); ok {
		breakdown = append(breakdown, li)
	}
	// Append the AI line only when at least one signal survived the citation
	// gate. A delta with no surviving items contributes nothing and must show no
	// chip — the calm surface stays silent rather than rendering an empty
	// "AI 분석" chip (design §c).
	if delta != nil && len(delta.Items) > 0 {
		breakdown = append(breakdown, aiLineItem(*delta))
	}

	sort.SliceStable(breakdown, func(i, j int) bool {
		return breakdown[i].Delta > breakdown[j].Delta
	})

	total := 0
	for _, li := range breakdown {
		total += li.Delta
	}
	// Clamp to [0,100]: a negative net AI delta must floor at 0, not collide
	// with the -1 dealbreaker sentinel (D3).
	if total > maxTotal {
		total = maxTotal
	}
	if total < 0 {
		total = 0
	}

	var reasons []ExclusionReason
	if reason, ok := careerExclusionReason(p, prof, ext); ok {
		reasons = append(reasons, reason)
	}
	if minimum := prof.EffectiveMinScore(); total < minimum {
		reasons = append(reasons, ExclusionReason{
			Kind:       "min_score",
			Label:      fmt.Sprintf("기준 점수 미달: %d점 / 기준 %d점", total, minimum),
			Confidence: "deterministic",
		})
	}
	return ScoreResult{Total: total, Breakdown: breakdown, ExclusionReasons: reasons}
}

func hardExclusionReasons(
	p scraper.Posting,
	prof profile.Profile,
	ext *ai.Extraction,
	validations map[string]ai.DealbreakerValidation,
) (*DealbreakerHit, []ExclusionReason) {
	var hit *DealbreakerHit
	var reasons []ExclusionReason
	for _, candidate := range DealbreakerCandidates(p, prof) {
		validation, ok := validations[candidate.ID]
		if ok && validation.CandidateID == candidate.ID && validation.Verdict == ai.DealbreakerNotApplicable {
			continue
		}

		reason := ExclusionReason{
			Kind:       dealbreakerKeyword,
			Label:      "제외 키워드: " + candidate.Phrase,
			Phrase:     candidate.Phrase,
			Source:     candidate.Match.Source,
			Category:   candidate.Match.Category,
			Confidence: "unverified",
		}
		if ok && validation.CandidateID == candidate.ID {
			switch validation.Verdict {
			case ai.DealbreakerApplies:
				reason.Confidence = "confirmed"
				reason.Evidence = candidate.Match.Evidence
			case ai.DealbreakerUncertain:
				reason.Confidence = "uncertain"
				reason.Evidence = candidate.Match.Evidence
			}
		}
		if hit == nil {
			hit = &DealbreakerHit{Kind: dealbreakerKeyword, Phrase: candidate.Phrase}
		}
		reasons = append(reasons, reason)
	}

	if phrase, ok := educationDealbreaker(p, prof, ext); ok {
		reason := ExclusionReason{
			Kind:       dealbreakerEducation,
			Label:      "학력 조건 불일치",
			Phrase:     phrase,
			Evidence:   p.EducationName,
			Confidence: "deterministic",
		}
		if ext != nil {
			reason.Evidence = ext.EducationEvidence
			reason.Confidence = "confirmed"
		}
		if hit == nil {
			hit = &DealbreakerHit{Kind: dealbreakerEducation, Phrase: phrase}
		}
		reasons = append(reasons, reason)
	}
	return hit, reasons
}

func careerExclusionReason(p scraper.Posting, prof profile.Profile, ext *ai.Extraction) (ExclusionReason, bool) {
	minC, maxC, newcomer, _ := careerFacts(p, ext)
	years := prof.CareerYears
	if (newcomer && years == 0) || (years >= minC && years <= maxC) {
		return ExclusionReason{}, false
	}

	label := fmt.Sprintf("경력 %d년 지원 불가", years)
	if years == 0 {
		label = "신입 지원 불가"
	}
	reason := ExclusionReason{
		Kind:       "career",
		Label:      label,
		Phrase:     formatExperienceRange(minC, maxC),
		Confidence: "deterministic",
	}
	if ext != nil {
		reason.Evidence = ext.CareerEvidence
		reason.Confidence = "confirmed"
	}
	return reason, true
}

// aiLineItem folds an AI Delta into one "AI 분석" line item: the net delta as
// the chip value, the surviving signals as Evidence for the popover, and the
// stale flag passed through.
func aiLineItem(d ai.Delta) LineItem {
	evidence := make([]EvidenceItem, 0, len(d.Items))
	for _, it := range d.Items {
		evidence = append(evidence, EvidenceItem{
			Kind:        it.Kind,
			Delta:       it.Delta,
			Quote:       it.Evidence,
			MatchedGoal: it.MatchedGoal,
		})
	}
	li := LineItem{Label: aiLineLabel, Delta: d.NetDelta, Reason: aiLineLabel, Stale: d.Stale}
	if len(evidence) > 0 {
		li.Evidence = evidence
	}
	return li
}
