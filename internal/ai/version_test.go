package ai

import (
	"regexp"
	"testing"
)

var hex12 = regexp.MustCompile(`^[0-9a-f]{12}$`)

func TestTaskVersionsAreStableAndPartitioned(t *testing.T) {
	score := ScoreVersion("anthropic", "claude-x")
	if !hex12.MatchString(score) {
		t.Fatalf("ScoreVersion = %q, want 12 lowercase hex chars", score)
	}
	if score != "925859b252bb" {
		t.Fatalf("ScoreVersion = %q, want the pre-split Stage 2 identity", score)
	}
	if score != AIVersion("anthropic", "claude-x") {
		t.Fatal("Stage 2 cache identity changed")
	}
	if DealbreakerVersion("anthropic", "claude-x") == score {
		t.Fatal("dealbreaker and score versions must be separate")
	}
	dealbreaker := DealbreakerVersion("anthropic", "claude-x")
	if dealbreaker != taskVersion("anthropic", "claude-x", "dealbreaker", DealbreakerPromptVersion) {
		t.Fatal("DealbreakerVersion must use the dealbreaker task name and prompt version")
	}
	if ScoreVersion("anthropic", "claude-x") != score {
		t.Fatal("score version must be stable")
	}

	versions := []struct {
		name string
		fn   func(string, string) string
	}{
		{"dealbreaker", DealbreakerVersion},
		{"score", ScoreVersion},
	}
	for _, version := range versions {
		base := version.fn("anthropic", "claude-x")
		if version.fn("openai", "claude-x") == base {
			t.Errorf("%s version did not rotate with provider", version.name)
		}
		if version.fn("anthropic", "claude-y") == base {
			t.Errorf("%s version did not rotate with model", version.name)
		}
	}

	if ScoreVersion("ab", "c") == ScoreVersion("a", "bc") {
		t.Fatal("version parts must be NUL-separated")
	}
}

func TestExtractionContractVersionIdentifiesOnlyTheFactContract(t *testing.T) {
	got := ExtractionContractVersion()
	if !hex12.MatchString(got) {
		t.Fatalf("ExtractionContractVersion = %q, want 12 lowercase hex chars", got)
	}
	if want := taskVersion("extraction-contract", "1"); got != want {
		t.Fatalf("ExtractionContractVersion = %q, want %q", got, want)
	}
	if got == DealbreakerVersion("anthropic", "claude-x") ||
		got == ScoreVersion("anthropic", "claude-x") {
		t.Fatal("global extraction contract must be separate from user-scoped judgments")
	}
}
