package scoring

import (
	"slices"
	"strings"
	"testing"

	"github.com/ohchanwu/jobcron/internal/ai"
	"github.com/ohchanwu/jobcron/internal/profile"
	"github.com/ohchanwu/jobcron/internal/scraper"
	"github.com/ohchanwu/jobcron/internal/tokenmatch"
)

func TestTokenize(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"백엔드 개발자 모집", []string{"백엔드", "개발자", "모집"}},
		{"React, Node.js", []string{"react", "node", "js"}},
		{"개발자를", []string{"개발자를"}}, // particle attached — a single token
		{"  여러   공백  ", []string{"여러", "공백"}},
		{"Spring Boot 3", []string{"spring", "boot", "3"}},
		{"", nil},
		{"!!!", nil},
	}
	for _, tc := range cases {
		if got := tokenize(tc.in); !slices.Equal(got, tc.want) {
			t.Errorf("tokenize(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestTextContains(t *testing.T) {
	cases := []struct {
		text, phrase string
		want         bool
	}{
		{"백엔드 개발자 모집", "개발자", true},
		{"백엔드 개발자 모집", "개발", false},                  // token-exact: 개발 != 개발자
		{"백엔드 개발자를 뽑아요", "개발자", false},               // particle attached: 개발자를 != 개발자
		{"복지: 완전 재택 근무 가능", "완전 재택", true},           // contiguous multi-token phrase
		{"재택 완전 근무", "완전 재택", false},                 // tokens present but not contiguous/in order
		{"React Developer", "react developer", true}, // case-insensitive
		{"백엔드 개발자 모집", "", false},                    // empty phrase matches nothing
	}
	for _, tc := range cases {
		if got := textContains(tc.text, tc.phrase); got != tc.want {
			t.Errorf("textContains(%q, %q) = %v, want %v", tc.text, tc.phrase, got, tc.want)
		}
	}
}

func TestDealbreakerCandidatesRequireCombinedTextMatchBeforeStructuredTag(t *testing.T) {
	p := scraper.Posting{
		Title:       "백엔드 개발자",
		Company:     "가나다",
		Description: "함께 성장할 동료를 찾습니다",
		Tags:        []scraper.Tag{{Name: "병역특례 가능", Category: "welfare"}},
	}
	got := DealbreakerCandidates(p, profile.Profile{Dealbreakers: []string{"병역특례 가능"}})
	if len(got) != 0 {
		t.Fatalf("tag-only phrase produced candidates: %+v", got)
	}
}

func TestCanonicalDealbreakerMatchPriorityAndEvidence(t *testing.T) {
	tests := []struct {
		name    string
		posting scraper.Posting
		phrase  string
		want    ai.DealbreakerMatch
	}{
		{
			name: "structured tag before title",
			posting: scraper.Posting{
				Title:       "병역특례 가능 백엔드",
				Description: "복지: 병역특례 가능",
				Tags:        []scraper.Tag{{Name: "병역특례 가능", Category: "welfare"}},
			},
			phrase: "병역특례 가능",
			want:   ai.DealbreakerMatch{Evidence: "병역특례 가능", Source: ai.DealbreakerMatchStructuredTag, Category: "welfare"},
		},
		{
			name:    "title before company and description",
			posting: scraper.Posting{Title: "야근 없는 팀", Company: "야근 연구소", Description: "야근 없음"},
			phrase:  "야근",
			want:    ai.DealbreakerMatch{Evidence: "야근 없는 팀", Source: ai.DealbreakerMatchTitle},
		},
		{
			name:    "company before description",
			posting: scraper.Posting{Title: "백엔드", Company: "야근 연구소", Description: "야근 없음"},
			phrase:  "야근",
			want:    ai.DealbreakerMatch{Evidence: "야근 연구소", Source: ai.DealbreakerMatchCompany},
		},
		{
			name:    "description",
			posting: scraper.Posting{Title: "백엔드", Company: "가나다", Description: "복지\n야근 없음"},
			phrase:  "야근",
			want:    ai.DealbreakerMatch{Evidence: "야근 없음", Source: ai.DealbreakerMatchDescription},
		},
		{
			name: "structured tag requires its complete name in the description",
			posting: scraper.Posting{
				Description: "병역특례",
				Tags:        []scraper.Tag{{Name: "병역특례 가능", Category: "welfare"}},
			},
			phrase: "병역특례",
			want:   ai.DealbreakerMatch{Evidence: "병역특례", Source: ai.DealbreakerMatchDescription},
		},
		{
			name:    "cross field fallback",
			posting: scraper.Posting{Title: "Alpha", Company: "Beta"},
			phrase:  "alpha beta",
			want:    ai.DealbreakerMatch{Evidence: "Alpha Beta ", Source: ai.DealbreakerMatchCombined},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DealbreakerCandidates(tt.posting, profile.Profile{Dealbreakers: []string{tt.phrase}})
			if len(got) != 1 {
				t.Fatalf("candidates = %+v, want one", got)
			}
			if got[0].Match != tt.want {
				t.Fatalf("match = %+v, want %+v", got[0].Match, tt.want)
			}
			if len([]rune(got[0].Match.Evidence)) > maxDealbreakerEvidenceRunes ||
				!tokenmatch.Contains(got[0].Match.Evidence, tt.phrase) {
				t.Fatalf("match evidence is not bounded and phrase-bearing: %+v", got[0].Match)
			}
		})
	}
}

func TestCanonicalDealbreakerMatchChoosesShortestDescriptionLine(t *testing.T) {
	p := scraper.Posting{Description: "긴 설명 속 야근 조건\n야근 A\n야근 B"}
	got := DealbreakerCandidates(p, profile.Profile{Dealbreakers: []string{"야근"}})
	if len(got) != 1 || got[0].Match.Evidence != "야근 A" {
		t.Fatalf("candidates = %+v, want earliest shortest matching line", got)
	}
}

func TestCanonicalDealbreakerMatchBoundsLongEvidenceAroundOccurrence(t *testing.T) {
	line := strings.Repeat(".", 150) + "야근" + strings.Repeat(".", 150)
	got := DealbreakerCandidates(
		scraper.Posting{Description: line},
		profile.Profile{Dealbreakers: []string{"야근"}},
	)
	if len(got) != 1 {
		t.Fatalf("candidates = %+v, want one", got)
	}
	evidence := got[0].Match.Evidence
	want := strings.Repeat(".", 119) + "야근" + strings.Repeat(".", 119)
	if evidence != want {
		t.Fatalf("evidence = %q, want deterministic centered excerpt %q", evidence, want)
	}
	if len([]rune(evidence)) > 240 || !tokenmatch.Contains(evidence, "야근") {
		t.Fatalf("evidence must be <=240 runes and retain phrase: %q", evidence)
	}
}

func TestCanonicalDealbreakerMatchCompactsOversizedMatchSpan(t *testing.T) {
	tests := []struct {
		name   string
		line   string
		phrase string
		want   string
	}{
		{
			name:   "long separator run",
			line:   "alpha" + strings.Repeat(".", 241) + "beta",
			phrase: "alpha beta",
			want:   "alpha beta",
		},
		{
			name:   "decomposed source token",
			line:   "cafe\u0301" + strings.Repeat(".", 241) + "beta",
			phrase: "café beta",
			want:   "café beta",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DealbreakerCandidates(
				scraper.Posting{Description: tt.line},
				profile.Profile{Dealbreakers: []string{tt.phrase}},
			)
			if len(got) != 1 {
				t.Fatalf("candidates = %+v, want one", got)
			}
			evidence := got[0].Match.Evidence
			if evidence != tt.want {
				t.Fatalf("evidence = %q, want compact source match %q", evidence, tt.want)
			}
			if len([]rune(evidence)) > maxDealbreakerEvidenceRunes || !tokenmatch.Contains(evidence, tt.phrase) {
				t.Fatalf("evidence must be <=240 runes and phrase-bearing: %q", evidence)
			}
		})
	}
}

func TestDealbreakerCandidatesPreserveNormalizationParticlesOrderAndID(t *testing.T) {
	t.Run("NFC normalization", func(t *testing.T) {
		got := DealbreakerCandidates(
			scraper.Posting{Title: "café"},
			profile.Profile{Dealbreakers: []string{"cafe\u0301"}},
		)
		if len(got) != 1 || got[0].Match.Evidence != "café" {
			t.Fatalf("candidates = %+v, want normalized title match", got)
		}
	})

	t.Run("Korean particle remains token exact", func(t *testing.T) {
		got := DealbreakerCandidates(
			scraper.Posting{Description: "야근을 하지 않습니다"},
			profile.Profile{Dealbreakers: []string{"야근"}},
		)
		if len(got) != 0 {
			t.Fatalf("particle-attached token produced candidates: %+v", got)
		}
	})

	t.Run("profile order and existing IDs", func(t *testing.T) {
		got := DealbreakerCandidates(
			scraper.Posting{Title: "병역특례 야근"},
			profile.Profile{Dealbreakers: []string{"야근", "병역특례"}},
		)
		if len(got) != 2 {
			t.Fatalf("candidates = %+v, want two", got)
		}
		if got[0].Phrase != "야근" || got[0].ID != "e8aeeb6fc5aa42e226b8ca6b3642d2521cbcf865c90af77790f1821758c57c29" {
			t.Fatalf("first candidate = %+v", got[0])
		}
		if got[1].Phrase != "병역특례" || got[1].ID != "a3ac339de1dc825b5d33ae0fa00b156cba2a675c959fb19e1fb56f6b545ae4ec" {
			t.Fatalf("second candidate = %+v", got[1])
		}
	})
}
