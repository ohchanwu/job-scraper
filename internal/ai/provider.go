package ai

import (
	"context"
	"errors"
	"time"
)

// ErrNotImplemented is returned by a provider method whose body has not been
// wired yet (e.g. a stub with no func set, or ScoreDelta before Stage 2).
var ErrNotImplemented = errors.New("ai: not implemented")

// ErrUnknownProvider is returned by New for an unrecognized provider name.
var ErrUnknownProvider = errors.New("ai: unknown provider")

// Provider is the seam for a BYOK AI backend. It is data-in/data-out: a
// posting's model text goes in, structured facts come out. No tool-use, no
// streaming — that contract is what lets the one-host egress pin hold.
type Provider interface {
	// Name is the stable provider id, e.g. "anthropic" or "openai".
	Name() string

	// Extract reads one posting's assembled model text and returns the
	// structured career/education extraction. A non-nil error means the
	// caller must fall back to the offline regex path and persist no cache
	// row. Usage carries the token counts the ledger debits.
	Extract(ctx context.Context, modelText string) (Extraction, Usage, error)

	// ValidateDealbreakers decides whether deterministic phrase matches apply
	// in context and returns only citation-gated results. Missing candidate IDs
	// remain unresolved for the caller's conservative fallback.
	ValidateDealbreakers(ctx context.Context, modelText string, candidates []DealbreakerCandidate) ([]DealbreakerValidation, Usage, error)

	// ScoreDelta weighs one posting (modelText) against the applicant's
	// free-form goals (profileText) and returns the raw, un-gated per-signal
	// items. A non-nil error means the caller applies no delta for that
	// posting. The returned items are NOT citation-gated — the caller passes
	// them through GateDelta before trusting any of them.
	ScoreDelta(ctx context.Context, modelText, profileText string) ([]RawDeltaItem, Usage, error)
}

// DealbreakerCandidate is one deterministic profile-phrase match to validate.
type DealbreakerCandidate struct {
	ID     string
	Phrase string
}

type DealbreakerVerdict string

const (
	DealbreakerApplies       DealbreakerVerdict = "applies"
	DealbreakerNotApplicable DealbreakerVerdict = "not_applicable"
	DealbreakerUncertain     DealbreakerVerdict = "uncertain"
)

// DealbreakerValidation is one independently validated contextual judgment.
type DealbreakerValidation struct {
	CandidateID string
	Verdict     DealbreakerVerdict
	Evidence    string
}

// Extraction is the validated Stage-1 result, mirroring the ai_extractions
// columns. The caller supplies posting id / content_hash / ai_version; the
// model supplies these fields.
type Extraction struct {
	MinCareer         int    // years, lower bound; >= 0
	MaxCareer         *int   // nil = open upper bound (read maps nil -> experienceUpperOpen 99)
	Newcomer          bool   // the model's 신입-eligible judgment
	EducationEnum     string // raw enum: none|highschool|associate|bachelor|master|doctorate
	CareerEvidence    string // verbatim career quote from the posting
	EducationEvidence string // verbatim education quote from the posting
}

// DeltaItem is one gated per-signal contribution from the Stage-2 ScoreDelta
// path. Defined in Stage 1 so scoring.Score's signature can reference
// *ai.Delta; produced in Stage 2 (T5).
type DeltaItem struct {
	Signal      string `json:"signal"`
	Kind        string `json:"kind"` // "presence" | "absence"
	Delta       int    `json:"delta"`
	Evidence    string `json:"evidence,omitempty"`
	MatchedGoal string `json:"matched_goal,omitempty"`
}

// Delta is the Stage-2 AI score delta: the surviving gated items and their
// net sum. Defined in Stage 1 (type only); filled by ScoreDelta in Stage 2.
//
// Stale marks a delta computed against a PRIOR profile (the scoreAll merge
// sets it when it falls back to the latest cached row because no row matches
// the current ai_input_hash). A stale delta is still summed into the Total;
// the chip is just labelled "(이전 프로필 기준)". scoreCareer's merge reads it
// onto the AI LineItem (T6 renders the stale chrome).
type Delta struct {
	Items    []DeltaItem
	NetDelta int
	Stale    bool
}

// Usage is the token accounting returned by every provider call. Stage 1
// returns it; T9's ai_usage ledger debits it.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// ProviderInfo is one server-owned settings option.
type ProviderInfo struct {
	ID             string `json:"id"`
	Label          string `json:"label"`
	KeyPlaceholder string `json:"keyPlaceholder"`
}

// New constructs a live provider for the given name ("anthropic" | "openai" |
// "gemini")
// with the user's API key and chosen model. rateLimit is the minimum spacing
// between requests (pass 0 in tests to disable pacing). It returns
// ErrUnknownProvider for any other name. The returned provider is pinned to
// that provider's single API host.
func New(providerName, apiKey, model string, rateLimit time.Duration) (Provider, error) {
	spec, ok := specByName[providerName]
	if !ok {
		return nil, ErrUnknownProvider
	}
	return newHTTPProvider(spec, apiKey, model, spec.defaultBaseURL, rateLimit)
}

// DefaultModel returns the fallback model id for a provider, or "" for an
// unknown provider name. Used by the server when the profile sets a provider but
// no explicit model.
func DefaultModel(providerName string) string {
	models := modelsByProvider[providerName]
	if len(models) == 0 {
		return ""
	}
	return models[0]
}

// modelsByProvider is the set of selectable model ids per provider, default
// (cheapest) first. The profile form renders these as a dropdown so a non-existent
// model id can't be typed. The empty "" choice (rendered as "기본값") maps to
// DefaultModel. Keep the list short and current; a model the provider has retired
// would 404.
var modelsByProvider = map[string][]string{
	"anthropic": {"claude-haiku-4-5-20251001", "claude-sonnet-4-6", "claude-opus-4-8"},
	"openai":    {"gpt-5.6-luna"},
	"gemini":    {"gemini-3.5-flash-lite"},
}

// ModelsForProvider returns the selectable model ids for a provider (default
// first), or nil for an unknown provider name. The server renders the current
// provider's list server-side; ModelsByProvider feeds the client-side swap.
func ModelsForProvider(providerName string) []string {
	return modelsByProvider[providerName]
}

// ModelsByProvider returns a copy of the full provider→models map for the form's
// client-side dropdown swap (when the user changes the provider select, JS
// repopulates the model select from this map). A copy keeps the package's map
// unexported and immutable from the caller.
func ModelsByProvider() map[string][]string {
	out := make(map[string][]string, len(modelsByProvider))
	for k, v := range modelsByProvider {
		out[k] = append([]string(nil), v...)
	}
	return out
}

var providers = []ProviderInfo{
	{ID: "anthropic", Label: "Anthropic (Claude)", KeyPlaceholder: "sk-ant-..."},
	{ID: "openai", Label: "OpenAI", KeyPlaceholder: "sk-..."},
	{ID: "gemini", Label: "Google Gemini", KeyPlaceholder: "AIza..."},
}

// Providers lists the selectable providers for the settings UI, in display
// order.
func Providers() []ProviderInfo { return append([]ProviderInfo(nil), providers...) }

// aiRequestSpacing is the self-imposed minimum spacing between live AI request
// STARTS — the polite, backpressure-friendly pace the AI path has used since it
// went live (originally 1s, the aiRateLimit). A live measurement (2026-06-08,
// Haiku, real corpus) saw ~1-2 HTTP 429s per 40-call burst at 1s (~60 req/min) —
// occasional, not persistent, and almost certainly input-tokens-per-minute
// driven (~2k input tokens/call). 1.5s cleared the 429s entirely but cost ~50%
// wall-clock, so this was loosened to 1.2s (~50 req/min) as the middle ground:
// most of the 429 relief for a modest latency cost. A 429 is not fatal either —
// the caller surfaces it and the row retries on the next press. See
// internal/ai/AI_TUNING_NOTES.md for the data. The limiter only spaces STARTS —
// waitForRateLimit releases its lock before sleeping — so the worker pool still
// overlaps the multi-second call latencies.
const aiRequestSpacing = time.Second + (time.Second / 5) // 1.2s

// SuggestedRateLimit returns the uniform request-start spacing for a provider.
// The provider name is accepted for API stability but not branched on.
func SuggestedRateLimit(providerName string) time.Duration {
	return aiRequestSpacing
}
