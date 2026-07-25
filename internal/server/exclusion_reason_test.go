package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ohchanwu/jobcron/internal/profile"
	"github.com/ohchanwu/jobcron/internal/scoring"
	"github.com/ohchanwu/jobcron/internal/storage"
)

func TestExclusionReasonViewShowsEveryReasonInOrder(t *testing.T) {
	reasons := []scoring.ExclusionReason{
		{Label: "제외 키워드: 리서치", Confidence: "confirmed"},
		{Label: "학력 조건 불일치", Confidence: "uncertain"},
		{Label: "신입 지원 불가", Confidence: "unverified"},
		{Label: "기준 점수 미달: 20점 / 기준 40점", Confidence: "deterministic"},
		{Label: "이전 데이터", Confidence: ""},
		{Label: "알 수 없는 판정", Confidence: "future-value"},
	}

	got := exclusionReasonViews(reasons)
	wantStatus := []string{
		"AI 문맥 확인",
		"AI 문맥 확인 불확실",
		"규칙 기반 · AI 문맥 확인 없음",
		"규칙 기반",
		"규칙 기반 · AI 문맥 확인 없음",
		"규칙 기반 · AI 문맥 확인 없음",
	}
	if len(got) != len(reasons) {
		t.Fatalf("views = %d, want %d", len(got), len(reasons))
	}
	for i := range got {
		if got[i].Label != reasons[i].Label || got[i].Status != wantStatus[i] {
			t.Errorf("view[%d] = %+v, want label %q status %q", i, got[i], reasons[i].Label, wantStatus[i])
		}
	}
}

func TestExclusionReasonViewMarksKeywordWithMatchingSemantics(t *testing.T) {
	tests := []struct {
		name     string
		evidence string
		phrase   string
		want     []exclusionTextSegment
	}{
		{
			name:     "case folded",
			evidence: "Lead Research projects",
			phrase:   "research",
			want: []exclusionTextSegment{
				{Text: "Lead "},
				{Text: "Research", Marked: true},
				{Text: " projects"},
			},
		},
		{
			name:     "punctuation separated token sequence",
			evidence: "데이터 연구-개발을 수행합니다",
			phrase:   "연구 개발을",
			want: []exclusionTextSegment{
				{Text: "데이터 "},
				{Text: "연구-개발을", Marked: true},
				{Text: " 수행합니다"},
			},
		},
		{
			name:     "NFC equivalent preserves original bytes",
			evidence: "Cafe\u0301 분석",
			phrase:   "Café",
			want: []exclusionTextSegment{
				{Text: "Cafe\u0301", Marked: true},
				{Text: " 분석"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitExclusionEvidence(tt.evidence, tt.phrase, true)
			if len(got) != len(tt.want) {
				t.Fatalf("segments = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("segment[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExclusionReasonViewSplitsMarkedKeywordWithoutHTML(t *testing.T) {
	got := exclusionReasonViews([]scoring.ExclusionReason{{
		Kind:       "keyword",
		Phrase:     "리서치",
		Evidence:   "<b>사용자 리서치</b>를 직접 수행합니다",
		Confidence: "confirmed",
	}})
	if len(got) != 1 || len(got[0].Evidence) != 3 {
		t.Fatalf("views = %+v, want one view with three evidence segments", got)
	}
	if got[0].Evidence[0].Text != "<b>사용자 " || got[0].Evidence[0].Marked {
		t.Errorf("prefix = %+v", got[0].Evidence[0])
	}
	if got[0].Evidence[1].Text != "리서치" || !got[0].Evidence[1].Marked {
		t.Errorf("keyword = %+v", got[0].Evidence[1])
	}
	if got[0].Evidence[2].Text != "</b>를 직접 수행합니다" || got[0].Evidence[2].Marked {
		t.Errorf("suffix = %+v", got[0].Evidence[2])
	}
}

func TestExcludedReasonEscapesProviderOutput(t *testing.T) {
	srv, _ := newTestServer(t, &fakeScraper{})
	views := exclusionReasonViews([]scoring.ExclusionReason{{
		Kind:       "keyword",
		Label:      "제외 키워드: 리서치",
		Phrase:     "리서치",
		Evidence:   "<script>alert(1)</script> 사용자 리서치",
		Confidence: "confirmed",
	}})
	var out bytes.Buffer
	if err := srv.tmpl.ExecuteTemplate(&out, "exclusion-reasons", views); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if strings.Contains(body, "<script>") || !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("provider output was not escaped: %s", body)
	}
	if !strings.Contains(body, "<mark>리서치</mark>") {
		t.Fatalf("matched keyword was not marked: %s", body)
	}
}

func TestDailyAndArchiveRenderExclusionReasons(t *testing.T) {
	srv, st := newTestServer(t, &fakeScraper{})
	ctx := context.Background()
	profJSON, _ := profile.Marshal(profile.Profile{})
	profileHash, _, err := st.SaveProfile(ctx, profJSON)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	p := listingPosting("excluded-reason", "문맥 확인 공고")
	p.FirstSeenAt, p.LastSeenAt = now, now
	id := mustUpsert(t, st, p)
	result := scoring.ScoreResult{Total: -1, ExclusionReasons: []scoring.ExclusionReason{{
		Kind:       "career",
		Label:      "신입 지원 불가",
		Evidence:   "경력 2년 이상의 백엔드 개발자를 찾습니다",
		Confidence: "confirmed",
	}}}
	breakdown, _ := json.Marshal(result)
	if err := st.UpsertScore(ctx, storage.Score{
		PostingID:     id,
		ProfileHash:   profileHash,
		Total:         -1,
		BreakdownJSON: string(breakdown),
		ComputedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/briefing", "/"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, rec.Code)
		}
		body := rec.Body.String()
		for _, want := range []string{"제외 이유", "신입 지원 불가", "경력 2년 이상의 백엔드 개발자를 찾습니다", "AI 문맥 확인"} {
			if !strings.Contains(body, want) {
				t.Errorf("GET %s missing %q", path, want)
			}
		}
		for _, preserved := range []string{`aria-label="관심 없음"`, `aria-label="북마크"`, `target="_blank"`} {
			if !strings.Contains(body, preserved) {
				t.Errorf("GET %s lost adjacent action %q", path, preserved)
			}
		}
	}
}

func TestRerateHintCoversPendingContextualValidation(t *testing.T) {
	srv, _ := newTestServer(t, &fakeScraper{})
	var out bytes.Buffer
	if err := srv.tmpl.ExecuteTemplate(&out, "rerateButton", &rerateInfo{
		PendingCount:        3,
		PendingContextCount: 2,
		PendingScoreCount:   1,
	}); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if !strings.Contains(body, "AI 평가 ·3") {
		t.Fatalf("rerate button = %s, want unique pending posting count", body)
	}
	if !strings.Contains(body, "AI 문맥 확인이 필요한 공고 2개") {
		t.Fatalf("rerate hint = %s", body)
	}
	if !strings.Contains(body, "다시 평가할 공고 1개") {
		t.Fatalf("rerate hint = %s, want separate Stage-2 pending count", body)
	}
}
