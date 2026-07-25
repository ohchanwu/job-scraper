package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ohchanwu/jobcron/internal/scraper"
)

func TestBuildModelTextTruncationAndHashStability(t *testing.T) {
	short := scraper.Posting{Title: "신입 백엔드", Company: "가나다", Description: "서버 개발자를 찾습니다"}
	text, hash, truncated := ModelInput(short)
	if truncated {
		t.Fatal("short body should not truncate")
	}
	if !strings.Contains(text, "신입 백엔드") || !strings.Contains(text, "서버 개발자") {
		t.Fatalf("model text missing posting fields: %q", text)
	}
	if len(hash) != 12 {
		t.Fatalf("content hash = %q, want 12 hex chars", hash)
	}

	t.Run("NFC-equivalent inputs hash identically", func(t *testing.T) {
		composed := scraper.Posting{Title: "caf\u00e9", Company: "x", Description: "y"}    // é as one code point
		decomposed := scraper.Posting{Title: "cafe\u0301", Company: "x", Description: "y"} // e + combining acute
		_, h1, _ := ModelInput(composed)
		_, h2, _ := ModelInput(decomposed)
		if h1 != h2 {
			t.Fatalf("NFC-equivalent titles hashed differently: %s vs %s", h1, h2)
		}
	})

	t.Run("real content change changes hash", func(t *testing.T) {
		a := scraper.Posting{Title: "t", Company: "c", Description: "원본"}
		b := scraper.Posting{Title: "t", Company: "c", Description: "수정됨"}
		_, ha, _ := ModelInput(a)
		_, hb, _ := ModelInput(b)
		if ha == hb {
			t.Fatal("different descriptions must hash differently")
		}
	})

	t.Run("hash is over PRE-truncation text", func(t *testing.T) {
		// Two postings sharing the first 13000 description runes but differing
		// only far past the truncation cap: the SENT (truncated) text is
		// identical, yet the content_hash differs — proving the hash sees the
		// full pre-truncation text.
		base := strings.Repeat("가", 13000)
		p1 := scraper.Posting{Title: "t", Company: "c", Description: base}
		p2 := scraper.Posting{Title: "t", Company: "c", Description: base + "추가된뒷부분"}
		s1, h1, tr1 := ModelInput(p1)
		s2, h2, tr2 := ModelInput(p2)
		if !tr1 || !tr2 {
			t.Fatal("both long postings should truncate")
		}
		if s1 != s2 {
			t.Fatal("truncated sent text should be identical for the shared prefix")
		}
		if h1 == h2 {
			t.Fatal("content_hash must differ — it is taken pre-truncation, not on the sent text")
		}
		if len([]rune(s1)) != maxModelTextRunes {
			t.Fatalf("sent text = %d runes, want %d", len([]rune(s1)), maxModelTextRunes)
		}
	})
}

func TestDealbreakerModelInputUsesFullNormalizedTextAndSharedHash(t *testing.T) {
	p := scraper.Posting{
		Title:       "신입 백엔드",
		Company:     "가나다",
		Description: strings.Repeat("가", maxModelTextRunes) + " cafe\u0301",
	}

	modelText, modelHash, truncated := ModelInput(p)
	dealbreakerText, dealbreakerHash := DealbreakerModelInput(p)

	if !truncated || len([]rune(modelText)) != maxModelTextRunes {
		t.Fatalf("ModelInput = %d runes, truncated=%v; want capped at %d", len([]rune(modelText)), truncated, maxModelTextRunes)
	}
	if len([]rune(dealbreakerText)) <= maxModelTextRunes {
		t.Fatalf("DealbreakerModelInput = %d runes, want full text beyond %d", len([]rune(dealbreakerText)), maxModelTextRunes)
	}
	if !strings.Contains(dealbreakerText, "café") {
		t.Fatalf("DealbreakerModelInput missing NFC-normalized suffix: %q", dealbreakerText[len(dealbreakerText)-20:])
	}
	if dealbreakerHash != modelHash {
		t.Fatalf("content hashes differ: dealbreaker=%q model=%q", dealbreakerHash, modelHash)
	}
}

func TestParseExtractionValid(t *testing.T) {
	t.Run("max present", func(t *testing.T) {
		ext, err := parseExtraction([]byte(`{"min_career":2,"max_career":5,"newcomer":false,"education":"bachelor","career_evidence":"경력 2-5년","education_evidence":"학사 이상"}`), "경력 2-5년, 학사 이상")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if ext.MinCareer != 2 || ext.MaxCareer == nil || *ext.MaxCareer != 5 || ext.Newcomer || ext.EducationEnum != "bachelor" {
			t.Fatalf("parsed = %+v", ext)
		}
	})
	t.Run("max null is open bound", func(t *testing.T) {
		ext, err := parseExtraction([]byte(`{"min_career":3,"max_career":null,"newcomer":false,"education":"none","career_evidence":"경력 3년 이상","education_evidence":""}`), "경력 3년 이상")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if ext.MaxCareer != nil {
			t.Fatalf("null max_career should map to nil, got %v", *ext.MaxCareer)
		}
	})
	t.Run("tolerates markdown fences / prose", func(t *testing.T) {
		raw := "여기 결과입니다:\n```json\n{\"min_career\":0,\"max_career\":0,\"newcomer\":true,\"education\":\"none\",\"career_evidence\":\"신입\",\"education_evidence\":\"\"}\n```"
		ext, err := parseExtraction([]byte(raw), "신입")
		if err != nil {
			t.Fatalf("parse fenced: %v", err)
		}
		if !ext.Newcomer {
			t.Fatalf("parsed = %+v", ext)
		}
	})
}

func TestParseExtractionRejects(t *testing.T) {
	cases := map[string]string{
		"malformed JSON":     `{"min_career":2,`,
		"no object":          `just some prose, no json`,
		"min negative":       `{"min_career":-1,"newcomer":false,"education":"none","career_evidence":"","education_evidence":""}`,
		"min absurd":         `{"min_career":2026,"newcomer":false,"education":"none","career_evidence":"","education_evidence":""}`,
		"max below min":      `{"min_career":5,"max_career":2,"newcomer":false,"education":"none","career_evidence":"","education_evidence":""}`,
		"max above ceiling":  `{"min_career":0,"max_career":200,"newcomer":true,"education":"none","career_evidence":"","education_evidence":""}`,
		"education not enum": `{"min_career":0,"newcomer":true,"education":"phd","career_evidence":"","education_evidence":""}`,
		"education empty":    `{"min_career":0,"newcomer":true,"career_evidence":"","education_evidence":""}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseExtraction([]byte(raw), raw); err == nil {
				t.Fatalf("expected rejection for %s, got none", name)
			}
		})
	}
}

func TestParseExtractionRequiresRestrictiveCareerEvidence(t *testing.T) {
	raw := []byte(`{"min_career":2,"max_career":null,"newcomer":false,"education":"none","career_evidence":"","education_evidence":""}`)
	if _, err := parseExtraction(raw, "경력 2년 이상"); err == nil {
		t.Fatal("restrictive career extraction without career evidence must be rejected")
	}
}

func TestParseExtractionRequiresRestrictiveEducationEvidence(t *testing.T) {
	raw := []byte(`{"min_career":0,"max_career":0,"newcomer":true,"education":"bachelor","career_evidence":"","education_evidence":""}`)
	if _, err := parseExtraction(raw, "학사 이상"); err == nil {
		t.Fatal("restrictive education extraction without education evidence must be rejected")
	}
}

func TestParseExtractionRequiresEvidenceKeys(t *testing.T) {
	cases := map[string]string{
		"career_evidence":    `{"min_career":0,"max_career":0,"newcomer":true,"education":"none","education_evidence":""}`,
		"education_evidence": `{"min_career":0,"max_career":0,"newcomer":true,"education":"none","career_evidence":""}`,
	}
	for missing, raw := range cases {
		t.Run(missing, func(t *testing.T) {
			if _, err := parseExtraction([]byte(raw), "신입 지원 가능"); err == nil {
				t.Fatalf("extraction without %s key must be rejected", missing)
			}
		})
	}
}

func TestExtractionRejectsEvidenceAbsentFromPosting(t *testing.T) {
	raw := []byte(`{"min_career":2,"max_career":null,"newcomer":false,"education":"none","career_evidence":"경력 2년 이상","education_evidence":""}`)
	if _, err := parseExtraction(raw, "신입 지원 가능"); err == nil {
		t.Fatal("evidence absent from the posting must be rejected")
	}
}

func TestExtractionAcceptsSeparateVerbatimEvidence(t *testing.T) {
	raw := []byte(`{"min_career":2,"max_career":null,"newcomer":false,"education":"bachelor","career_evidence":"경력 2년 이상","education_evidence":"학사 이상"}`)
	ext, err := parseExtraction(raw, "지원 자격: 경력 2년 이상, 학사 이상")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ext.CareerEvidence != "경력 2년 이상" || ext.EducationEvidence != "학사 이상" {
		t.Fatalf("evidence = %q, %q", ext.CareerEvidence, ext.EducationEvidence)
	}
}

// TestFirstJSONObjectDepthAware locks the T6 depth-aware behavior: stop at the
// first object's matching brace, ignore braces inside string literals.
func TestFirstJSONObjectDepthAware(t *testing.T) {
	t.Run("stops at first object, ignores a second", func(t *testing.T) {
		obj, err := firstJSONObject([]byte(`prose {"a":1} more {"b":2}`))
		if err != nil || string(obj) != `{"a":1}` {
			t.Fatalf("got %q err=%v", string(obj), err)
		}
	})
	t.Run("nested object balanced", func(t *testing.T) {
		obj, err := firstJSONObject([]byte(`{"a":{"b":2}} trailing }`))
		if err != nil || string(obj) != `{"a":{"b":2}}` {
			t.Fatalf("got %q err=%v", string(obj), err)
		}
	})
	t.Run("ignores braces inside strings", func(t *testing.T) {
		obj, err := firstJSONObject([]byte(`{"a":"}{ not a brace"}`))
		if err != nil || string(obj) != `{"a":"}{ not a brace"}` {
			t.Fatalf("got %q err=%v", string(obj), err)
		}
	})
	t.Run("error when no object", func(t *testing.T) {
		if _, err := firstJSONObject([]byte(`no json here`)); err == nil {
			t.Fatal("expected error")
		}
	})
}

// TestStripLeadingNumericPlus locks the +N sanitizer: strip a JSON-invalid
// leading '+' on numbers in value positions, never inside a string, leave
// negatives and plain digits alone.
func TestStripLeadingNumericPlus(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"d": +3}`, `{"d": 3}`},
		{`[+1,+2]`, `[1,2]`},
		{`{"d": -2}`, `{"d": -2}`},
		{`{"d": 5}`, `{"d": 5}`},
		{`{"q": "a: +3 b"}`, `{"q": "a: +3 b"}`},
		{`{"q": "C++"}`, `{"q": "C++"}`},
	}
	for _, c := range cases {
		if got := string(stripLeadingNumericPlus([]byte(c.in))); got != c.want {
			t.Errorf("stripLeadingNumericPlus(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestParseExtractionDepthAndPlus: the depth-aware scan + +N sanitizer applied
// to Stage-1 extraction too.
func TestParseExtractionDepthAndPlus(t *testing.T) {
	t.Run("two objects: first wins", func(t *testing.T) {
		raw := `{"min_career":0,"max_career":null,"newcomer":true,"education":"none","career_evidence":"신입 환영","education_evidence":""}
		{"min_career":5,"max_career":null,"newcomer":false,"education":"master","career_evidence":"무시","education_evidence":"무시"}`
		ext, err := parseExtraction([]byte(raw), "신입 환영")
		if err != nil || ext.MinCareer != 0 || !ext.Newcomer || ext.EducationEnum != "none" {
			t.Fatalf("expected the first object, got %+v err=%v", ext, err)
		}
	})
	t.Run("leading + on numbers tolerated", func(t *testing.T) {
		raw := `{"min_career": +2,"max_career": +5,"newcomer":false,"education":"bachelor","career_evidence":"경력 2-5년","education_evidence":"학사 이상"}`
		ext, err := parseExtraction([]byte(raw), "경력 2-5년, 학사 이상")
		if err != nil || ext.MinCareer != 2 || ext.MaxCareer == nil || *ext.MaxCareer != 5 {
			t.Fatalf("expected min 2 max 5, got %+v err=%v", ext, err)
		}
	})
}

// TestHTTPProviderExtractEndToEnd exercises the wired Extract: complete →
// parseExtraction, against a canned provider response.
func TestHTTPProviderExtractEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Anthropic envelope wrapping the extraction JSON.
		io.WriteString(w, `{"content":[{"type":"text","text":"{\"min_career\":0,\"max_career\":0,\"newcomer\":true,\"education\":\"bachelor\",\"career_evidence\":\"신입 환영\",\"education_evidence\":\"학사 이상\"}"}],"usage":{"input_tokens":40,"output_tokens":20}}`)
	}))
	defer srv.Close()

	p, err := newHTTPProvider(anthropicSpec, "sk", "claude-x", srv.URL, 0)
	if err != nil {
		t.Fatalf("newHTTPProvider: %v", err)
	}
	ext, usage, err := p.Extract(context.Background(), "신입 환영, 학사 이상")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !ext.Newcomer || ext.EducationEnum != "bachelor" || ext.CareerEvidence != "신입 환영" || ext.EducationEvidence != "학사 이상" {
		t.Fatalf("extract = %+v", ext)
	}
	if usage.InputTokens != 40 || usage.OutputTokens != 20 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestHTTPProviderExtractRejectsBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"content":[{"type":"text","text":"sorry I cannot help"}],"usage":{"input_tokens":5,"output_tokens":5}}`)
	}))
	defer srv.Close()

	p, _ := newHTTPProvider(anthropicSpec, "sk", "claude-x", srv.URL, 0)
	if _, _, err := p.Extract(context.Background(), "x"); err == nil {
		t.Fatal("a non-JSON model reply must surface as an error so the caller falls back")
	}
}
