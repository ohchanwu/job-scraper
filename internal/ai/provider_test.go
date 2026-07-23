package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestHTTPProviderValidateDealbreakers(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		io.WriteString(w, `{"content":[{"type":"text","text":"{\"checks\":[{\"candidate_id\":\"research\",\"verdict\":\"not_applicable\",\"evidence\":\"리서치 아님\"}]}"}],"usage":{"input_tokens":12,"output_tokens":6}}`)
	}))
	defer srv.Close()

	p, err := newHTTPProvider(anthropicSpec, "sk", "claude-x", srv.URL, 0)
	if err != nil {
		t.Fatalf("newHTTPProvider: %v", err)
	}
	got, usage, err := p.ValidateDealbreakers(context.Background(), "리서치 아님", []DealbreakerCandidate{{ID: "research", Phrase: "리서치"}})
	if err != nil {
		t.Fatalf("ValidateDealbreakers: %v", err)
	}
	if len(got) != 1 || got[0].Verdict != DealbreakerNotApplicable || usage.InputTokens != 12 || usage.OutputTokens != 6 {
		t.Fatalf("validation = %+v, usage = %+v", got, usage)
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d, want 1", calls)
	}

	got, usage, err = p.ValidateDealbreakers(context.Background(), "ignored", nil)
	if err != nil || got != nil || usage != (Usage{}) || calls != 1 {
		t.Fatalf("empty candidates must skip provider: got=%+v usage=%+v err=%v calls=%d", got, usage, err, calls)
	}
}

func TestModelsForProvider(t *testing.T) {
	for _, prov := range Providers() {
		models := ModelsForProvider(prov)
		if len(models) == 0 {
			t.Fatalf("ModelsForProvider(%q) is empty — the dropdown would offer nothing", prov)
		}
		// The dropdown's first model must equal the provider's default, so the
		// "기본값" (empty) choice and the first explicit option agree.
		if models[0] != DefaultModel(prov) {
			t.Errorf("ModelsForProvider(%q)[0] = %q, want the default %q first", prov, models[0], DefaultModel(prov))
		}
	}
	if ModelsForProvider("groq") != nil {
		t.Error("an unknown provider must return nil models")
	}
}

func TestProviderRegistryIncludesOpenAIAndGemini(t *testing.T) {
	wantProviders := []string{"anthropic", "openai", "gemini"}
	if got := Providers(); !slices.Equal(got, wantProviders) {
		t.Fatalf("Providers() = %q, want %q", got, wantProviders)
	}
	wantModels := map[string][]string{
		"openai": {"gpt-5.6-luna"},
		"gemini": {"gemini-3.5-flash-lite"},
	}
	for provider, want := range wantModels {
		if got := ModelsForProvider(provider); !slices.Equal(got, want) {
			t.Errorf("ModelsForProvider(%q) = %q, want %q", provider, got, want)
		}
		if got := DefaultModel(provider); got != want[0] {
			t.Errorf("DefaultModel(%q) = %q, want %q", provider, got, want[0])
		}
	}
}

func TestModelsByProviderReturnsACopy(t *testing.T) {
	m := ModelsByProvider()
	if _, ok := m["anthropic"]; !ok {
		t.Fatal("ModelsByProvider missing anthropic")
	}
	// Mutating the returned map/slices must not corrupt the package's source of
	// truth — the form marshals this map to JSON every render.
	m["anthropic"][0] = "tampered"
	m["injected"] = []string{"x"}
	if ModelsForProvider("anthropic")[0] == "tampered" {
		t.Error("ModelsByProvider leaked a shared slice — caller mutation reached package state")
	}
	if ModelsForProvider("injected") != nil {
		t.Error("ModelsByProvider leaked a shared map — caller insertion reached package state")
	}
}

func TestSuggestedRateLimit(t *testing.T) {
	// A single supported provider → the uniform self-imposed request-start
	// spacing (aiRequestSpacing), regardless of the name passed. Asserts against
	// the const so a deliberate retune (e.g. 1s → 1.2s) keeps the test honest
	// without re-pinning a literal here.
	for _, provider := range []string{"anthropic", "unknown", ""} {
		if got := SuggestedRateLimit(provider); got != aiRequestSpacing {
			t.Errorf("SuggestedRateLimit(%q) = %v, want %v", provider, got, aiRequestSpacing)
		}
	}
}
