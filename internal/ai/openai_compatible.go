package ai

import (
	"encoding/json"
	"fmt"
	"net/http"
)

var (
	openaiSpec = newChatCompletionsSpec("openai", "https://api.openai.com/v1")
	geminiSpec = newChatCompletionsSpec("gemini", "https://generativelanguage.googleapis.com/v1beta/openai")
)

// newChatCompletionsSpec returns the shared OpenAI-compatible wire contract.
// baseURL includes the provider's API prefix, such as /v1 or /v1beta/openai.
func newChatCompletionsSpec(name, baseURL string) providerSpec {
	return providerSpec{
		name:           name,
		defaultBaseURL: baseURL,
		path:           "/chat/completions",
		buildBody: func(model, system, user string) any {
			return chatCompletionsRequest{
				Model:               model,
				MaxCompletionTokens: maxOutputTokens,
				ResponseFormat:      chatCompletionsResponseFormat{Type: "json_object"},
				Messages: []chatCompletionsMessage{
					{Role: "system", Content: system},
					{Role: "user", Content: user},
				},
			}
		},
		setAuth: func(h http.Header, apiKey string) {
			h.Set("Authorization", "Bearer "+apiKey)
		},
		parseResp: func(body []byte) (string, Usage, error) {
			var r chatCompletionsResponse
			if err := json.Unmarshal(body, &r); err != nil {
				return "", Usage{}, fmt.Errorf("ai: %s decode response: %w", name, err)
			}
			if len(r.Choices) == 0 {
				return "", Usage{}, fmt.Errorf("ai: %s response has no choices", name)
			}
			return r.Choices[0].Message.Content, Usage{
				InputTokens:  r.Usage.PromptTokens,
				OutputTokens: r.Usage.CompletionTokens,
			}, nil
		},
	}
}

type chatCompletionsRequest struct {
	Model               string                        `json:"model"`
	MaxCompletionTokens int                           `json:"max_completion_tokens"`
	ResponseFormat      chatCompletionsResponseFormat `json:"response_format"`
	Messages            []chatCompletionsMessage      `json:"messages"`
}

type chatCompletionsResponseFormat struct {
	Type string `json:"type"`
}

type chatCompletionsMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionsResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}
