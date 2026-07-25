package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestChatCompletionsProductionEndpoints(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		wantURL string
	}{
		{name: "openai", baseURL: "https://api.openai.com/v1", wantURL: "https://api.openai.com/v1/chat/completions"},
		{name: "gemini", baseURL: "https://generativelanguage.googleapis.com/v1beta/openai", wantURL: "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := newChatCompletionsSpec(tt.name, tt.baseURL)
			if got := spec.defaultBaseURL + spec.path; got != tt.wantURL {
				t.Fatalf("endpoint = %q, want %q", got, tt.wantURL)
			}
		})
	}
}

func TestChatCompletionsRequestAndResponseContract(t *testing.T) {
	for _, name := range []string{"openai", "gemini"} {
		t.Run(name, func(t *testing.T) {
			var gotMethod, gotHost, gotPath, gotAuth string
			var gotBody struct {
				Model               string `json:"model"`
				MaxCompletionTokens int    `json:"max_completion_tokens"`
				ResponseFormat      struct {
					Type string `json:"type"`
				} `json:"response_format"`
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotHost, gotPath = r.Method, r.Host, r.URL.Path
				gotAuth = r.Header.Get("Authorization")
				if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
					t.Errorf("decode request: %v", err)
				}
				io.WriteString(w, `{"choices":[{"message":{"content":"{\"ok\":true}"}}],"usage":{"prompt_tokens":5,"completion_tokens":9}}`)
			}))
			defer srv.Close()

			spec := newChatCompletionsSpec(name, srv.URL)
			p, err := newHTTPProvider(spec, "secret", "model-x", srv.URL, 0)
			if err != nil {
				t.Fatalf("newHTTPProvider: %v", err)
			}
			text, usage, err := p.complete(context.Background(), "system text", "user text")
			if err != nil {
				t.Fatalf("complete: %v", err)
			}

			if gotMethod != http.MethodPost || gotHost != strings.TrimPrefix(srv.URL, "http://") || gotPath != "/chat/completions" {
				t.Fatalf("request = %s host=%q path=%q", gotMethod, gotHost, gotPath)
			}
			if gotAuth != "Bearer secret" {
				t.Fatalf("Authorization = %q", gotAuth)
			}
			if gotBody.Model != "model-x" || gotBody.MaxCompletionTokens != maxOutputTokens || gotBody.ResponseFormat.Type != "json_object" {
				t.Fatalf("request body = %+v", gotBody)
			}
			if len(gotBody.Messages) != 2 ||
				gotBody.Messages[0].Role != "system" || gotBody.Messages[0].Content != "system text" ||
				gotBody.Messages[1].Role != "user" || gotBody.Messages[1].Content != "user text" {
				t.Fatalf("messages = %+v", gotBody.Messages)
			}
			if text != `{"ok":true}` || usage != (Usage{InputTokens: 5, OutputTokens: 9}) {
				t.Fatalf("text = %q, usage = %+v", text, usage)
			}
		})
	}
}

// TestChatCompletionsValidateDealbreakersV2 proves the shared chat-completions
// adapter parses the version-2 dealbreaker schema end-to-end — no adapter-side
// branching, same provider-independent parser as Anthropic.
func TestChatCompletionsValidateDealbreakersV2(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"content":"{\"checks\":[{\"candidate_id\":\"research\",\"verdict\":\"not_applicable\",\"reason_code\":\"explicitly_negated\",\"reason_evidence\":\"리서치 아님\"}]}"}}],"usage":{"prompt_tokens":12,"completion_tokens":6}}`)
	}))
	defer srv.Close()

	p, err := newHTTPProvider(newChatCompletionsSpec("openai", srv.URL), "secret", "model-x", srv.URL, 0)
	if err != nil {
		t.Fatalf("newHTTPProvider: %v", err)
	}
	got, usage, err := p.ValidateDealbreakers(context.Background(), "리서치 아님",
		[]DealbreakerCandidate{{ID: "research", Phrase: "리서치", Match: DealbreakerMatch{Evidence: "리서치 아님", Source: DealbreakerMatchDescription}}})
	if err != nil {
		t.Fatalf("ValidateDealbreakers: %v", err)
	}
	if len(got) != 1 || got[0].Verdict != DealbreakerNotApplicable || got[0].ReasonCode != DealbreakerReasonExplicitlyNegated {
		t.Fatalf("validation = %+v", got)
	}
	if usage != (Usage{InputTokens: 12, OutputTokens: 6}) {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestChatCompletionsRejectsMalformedEnvelope(t *testing.T) {
	for _, name := range []string{"openai", "gemini"} {
		t.Run(name, func(t *testing.T) {
			spec := newChatCompletionsSpec(name, "https://example.com")
			for _, body := range []string{
				`not json`,
				`{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`,
				`{"choices":[{"message":{"content":""}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`,
				`{"choices":[{"message":{"content":" \n\t"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`,
				`{"choices":[{"message":{"content":"{}"}}]}`,
				`{"choices":[{"message":{"content":"{}"}}],"usage":{"prompt_tokens":0,"completion_tokens":1}}`,
				`{"choices":[{"message":{"content":"{}"}}],"usage":{"prompt_tokens":-1,"completion_tokens":1}}`,
				`{"choices":[{"message":{"content":"{}"}}],"usage":{"prompt_tokens":1,"completion_tokens":-1}}`,
			} {
				text, usage, err := spec.parseResp([]byte(body))
				if err == nil {
					t.Fatalf("parseResp(%q) succeeded", body)
				}
				if text != "" || usage != (Usage{}) {
					t.Fatalf("parseResp(%q) returned text %q and usage %+v on error", body, text, usage)
				}
			}
		})
	}
}

func TestChatCompletionsNon2xxAPIErrorIsBounded(t *testing.T) {
	for _, name := range []string{"openai", "gemini"} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
				io.WriteString(w, strings.Repeat("x", 1<<20))
			}))
			defer srv.Close()

			p, err := newHTTPProvider(newChatCompletionsSpec(name, srv.URL), "secret", "model-x", srv.URL, 0)
			if err != nil {
				t.Fatalf("newHTTPProvider: %v", err)
			}
			_, _, err = p.complete(context.Background(), "system", "user")
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error = %T %v, want *APIError", err, err)
			}
			if apiErr.Provider != name || apiErr.Status != http.StatusTooManyRequests || len(apiErr.Error()) > 256 {
				t.Fatalf("APIError = provider=%q status=%d len=%d", apiErr.Provider, apiErr.Status, len(apiErr.Error()))
			}
		})
	}
}

func TestChatCompletionsPacingCancellation(t *testing.T) {
	for _, name := range []string{"openai", "gemini"} {
		t.Run(name, func(t *testing.T) {
			var calls int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				io.WriteString(w, `{"choices":[{"message":{"content":"{}"}}],"usage":{"prompt_tokens":1,"completion_tokens":0}}`)
			}))
			defer srv.Close()

			p, err := newHTTPProvider(newChatCompletionsSpec(name, srv.URL), "secret", "model-x", srv.URL, time.Hour)
			if err != nil {
				t.Fatalf("newHTTPProvider: %v", err)
			}
			if _, _, err := p.complete(context.Background(), "system", "user"); err != nil {
				t.Fatalf("first complete: %v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			if _, _, err := p.complete(ctx, "system", "user"); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("second complete error = %v, want deadline exceeded", err)
			}
			if calls != 1 {
				t.Fatalf("HTTP calls = %d, want 1", calls)
			}
		})
	}
}
