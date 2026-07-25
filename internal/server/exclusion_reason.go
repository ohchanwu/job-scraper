package server

import (
	"github.com/ohchanwu/jobcron/internal/ai"
	"github.com/ohchanwu/jobcron/internal/scoring"
	"github.com/ohchanwu/jobcron/internal/tokenmatch"
)

type exclusionTextSegment struct {
	Text   string
	Marked bool
}

type exclusionReasonView struct {
	Label       string
	Status      string
	SourceLabel string
	Evidence    []exclusionTextSegment
	HasEvidence bool
}

func exclusionReasonViews(reasons []scoring.ExclusionReason) []exclusionReasonView {
	views := make([]exclusionReasonView, 0, len(reasons))
	for _, reason := range reasons {
		view := exclusionReasonView{
			Label:       reason.Label,
			Status:      exclusionReasonStatus(reason.Confidence),
			SourceLabel: exclusionSourceLabel(reason.Source, reason.Category),
		}
		if reason.Evidence != "" {
			view.HasEvidence = true
			view.Evidence = splitExclusionEvidence(reason.Evidence, reason.Phrase, reason.Kind == "keyword")
		}
		views = append(views, view)
	}
	return views
}

func exclusionReasonStatus(confidence string) string {
	switch confidence {
	case "confirmed":
		return "AI 문맥 확인"
	case "uncertain":
		return "AI 문맥 확인 불확실"
	case "unverified":
		return "규칙 기반 · AI 문맥 확인 없음"
	case "deterministic":
		return "규칙 기반"
	default:
		return "규칙 기반 · AI 문맥 확인 없음"
	}
}

// exclusionSourceLabel names where the deterministic matcher found the phrase.
// It reads only the stored server match, never provider output. An absent
// source renders nothing; an unrecognized one falls back to the calm generic
// label rather than leaking a raw enum into the briefing.
func exclusionSourceLabel(source ai.DealbreakerMatchSource, category string) string {
	switch source {
	case "":
		return ""
	case ai.DealbreakerMatchStructuredTag:
		if category == "welfare" {
			return "복지 태그"
		}
	case ai.DealbreakerMatchTitle:
		return "제목"
	case ai.DealbreakerMatchCompany:
		return "회사"
	case ai.DealbreakerMatchDescription:
		return "공고 본문"
	}
	return "공고 정보"
}

func splitExclusionEvidence(evidence, phrase string, mark bool) []exclusionTextSegment {
	if !mark || phrase == "" {
		return []exclusionTextSegment{{Text: evidence}}
	}
	start, end, ok := tokenmatch.Find(evidence, phrase)
	if !ok {
		return []exclusionTextSegment{{Text: evidence}}
	}
	segments := make([]exclusionTextSegment, 0, 3)
	if start > 0 {
		segments = append(segments, exclusionTextSegment{Text: evidence[:start]})
	}
	segments = append(segments, exclusionTextSegment{Text: evidence[start:end], Marked: true})
	if end < len(evidence) {
		segments = append(segments, exclusionTextSegment{Text: evidence[end:]})
	}
	return segments
}
