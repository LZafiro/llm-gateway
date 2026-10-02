package openai

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/LZafiro/llm-gateway/internal/provider"
	"github.com/LZafiro/llm-gateway/internal/provider/providertest"
)

func ptr[T any](v T) *T { return &v }

func newClient(url string) *Client {
	return New(url, "sk-test", provider.NewHTTPClient(5*time.Second))
}

func sampleRequest() provider.ChatRequest {
	return provider.ChatRequest{
		Model: "gpt-4o-mini",
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: "Be brief."},
			{Role: provider.RoleUser, Content: "Hi"},
		},
		MaxTokens:   ptr(64),
		Temperature: ptr(0.2),
		User:        "visitor-1",
	}
}

func TestCompleteTranslatesRequestAndResponse(t *testing.T) {
	srv := providertest.NewServer(t, providertest.Reply{Status: http.StatusOK, Fixture: "complete_success.json"})
	resp, err := newClient(srv.URL).Complete(t.Context(), sampleRequest())
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	want := provider.ChatResponse{Content: "Hello, world!", FinishReason: "stop", Usage: provider.Usage{PromptTokens: 19, CompletionTokens: 4}}
	if resp != want {
		t.Errorf("resp = %+v, want %+v", resp, want)
	}

	got := srv.Requests()[0]
	if got.Path != "/v1/chat/completions" || got.Header.Get("Authorization") != "Bearer sk-test" {
		t.Errorf("request path/header = %s %v", got.Path, got.Header)
	}
	var body chatRequest
	if err := json.Unmarshal(got.Body, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Messages) != 2 || body.Messages[0].Role != "system" || *body.MaxTokens != 64 || body.StreamOptions != nil {
		t.Errorf("body = %+v", body)
	}
}

func TestStreamRequestsUsageAndTranslatesChunks(t *testing.T) {
	srv := providertest.NewServer(t, providertest.Reply{Status: http.StatusOK, Fixture: "stream_success.sse"})
	stream, err := newClient(srv.URL).Stream(t.Context(), sampleRequest())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	chunks, err := providertest.Drain(stream)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	content, finish, usage := providertest.Concat(chunks)
	if content != "Hello, world!" || finish != "stop" {
		t.Errorf("content = %q, finish = %q", content, finish)
	}
	if usage == nil || *usage != (provider.Usage{PromptTokens: 19, CompletionTokens: 4}) {
		t.Errorf("usage = %+v", usage)
	}
	var body chatRequest
	if err := json.Unmarshal(srv.Requests()[0].Body, &body); err != nil {
		t.Fatal(err)
	}
	if !body.Stream || body.StreamOptions == nil || !body.StreamOptions.IncludeUsage {
		t.Errorf("stream options = %+v", body.StreamOptions)
	}
}

func TestStreamErrors(t *testing.T) {
	tests := []struct {
		fixture string
		kind    provider.ErrorKind
	}{
		{"stream_error.sse", provider.KindServer},
		{"stream_truncated.sse", provider.KindMalformed},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			srv := providertest.NewServer(t, providertest.Reply{Status: http.StatusOK, Fixture: tt.fixture})
			stream, err := newClient(srv.URL).Stream(t.Context(), sampleRequest())
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			_, err = providertest.Drain(stream)
			providertest.RequireKind(t, err, tt.kind)
		})
	}
}

func TestCompleteErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		fixture string
		kind    provider.ErrorKind
		message string
	}{
		{"rate limited", http.StatusTooManyRequests, "error_rate_limit.json", provider.KindRateLimited, "Rate limit reached for gpt-4o-mini"},
		{"context length", http.StatusBadRequest, "error_context_length.json", provider.KindClient, "This model's maximum context length is 128000 tokens."},
		{"bad gateway", http.StatusBadGateway, "complete_malformed.json", provider.KindServer, "<html><body>502 Bad Gateway</body></html>"},
		{"malformed body", http.StatusOK, "complete_malformed.json", provider.KindMalformed, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := providertest.NewServer(t, providertest.Reply{Status: tt.status, Fixture: tt.fixture})
			_, err := newClient(srv.URL).Complete(t.Context(), sampleRequest())
			providertest.RequireKind(t, err, tt.kind)
			if perr, _ := provider.AsError(err); perr.Message != tt.message {
				t.Errorf("message = %q, want %q", perr.Message, tt.message)
			}
		})
	}
}
