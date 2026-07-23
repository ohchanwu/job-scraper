package ai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAnthropicClientParsesMessagesResponse drives the shared `complete`
// helper against a canned Anthropic /v1/messages body, asserting the request
// carried the right auth/version headers and path, and that the assistant
// text + token usage are parsed.
func TestAnthropicClientParsesMessagesResponse(t *testing.T) {
	var gotPath, gotKey, gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		io.WriteString(w, `{"content":[{"type":"text","text":"hello"}],"usage":{"input_tokens":11,"output_tokens":7}}`)
	}))
	defer srv.Close()

	p, err := newHTTPProvider(anthropicSpec, "sk-test", "claude-x", srv.URL, 0)
	if err != nil {
		t.Fatalf("newHTTPProvider: %v", err)
	}
	text, usage, err := p.complete(context.Background(), "system", "user")
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if text != "hello" {
		t.Fatalf("text = %q, want %q", text, "hello")
	}
	if usage.InputTokens != 11 || usage.OutputTokens != 7 {
		t.Fatalf("usage = %+v, want {11 7}", usage)
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("path = %q, want /v1/messages", gotPath)
	}
	if gotKey != "sk-test" {
		t.Fatalf("x-api-key = %q, want sk-test", gotKey)
	}
	if gotVersion != anthropicVersion {
		t.Fatalf("anthropic-version = %q, want %q", gotVersion, anthropicVersion)
	}
}

func TestCompleteNon200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"bad key"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	p, err := newHTTPProvider(anthropicSpec, "sk-bad", "claude-x", srv.URL, 0)
	if err != nil {
		t.Fatalf("newHTTPProvider: %v", err)
	}
	_, _, err = p.complete(context.Background(), "s", "u")
	if err == nil {
		t.Fatal("a 401 response must be an error")
	}
	// The status must survive as a typed *APIError so the server can tell a 401
	// (bad key) from a 404 (bad model) and surface a specific, calm message.
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("non-2xx must be an *APIError, got %T: %v", err, err)
	}
	if apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("APIError.Status = %d, want 401", apiErr.Status)
	}
}

func TestNewFactory(t *testing.T) {
	for _, name := range []string{"anthropic", "openai", "gemini"} {
		p, err := New(name, "sk", "model", 0)
		if err != nil {
			t.Fatalf("New(%q): %v", name, err)
		}
		if p.Name() != name {
			t.Fatalf("New(%q).Name() = %q", name, p.Name())
		}
	}
	if _, err := New("groq", "sk", "model", 0); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("New(unknown) err = %v, want ErrUnknownProvider", err)
	}
}

// Both live providers satisfy Provider.
var (
	_ Provider = (*httpProvider)(nil)
)
