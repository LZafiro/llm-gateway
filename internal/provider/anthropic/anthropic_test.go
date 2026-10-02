package anthropic

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
		Model: "claude-haiku-4-5",
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: "Be brief."},
			{Role: provider.RoleUser, Content: "Hi"},
			{Role: provider.RoleUser, Content: "there"},
		},
		Temperature: ptr(1.7),
		Stop:        []string{"END"},
		User:        "visitor-1",
	}
}

func TestCompleteTranslatesRequestAndResponse(t *testing.T) {
	srv := providertest.NewServer(t, providertest.Reply{Status: http.StatusOK, Fixture: "complete_success.json"})
	resp, err := newClient(srv.URL).Complete(t.Context(), sampleRequest())
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "Hello, world!" || resp.FinishReason != "length" {
		t.Errorf("resp = %+v", resp)
	}
	if resp.Usage != (provider.Usage{PromptTokens: 21, CompletionTokens: 6}) {
		t.Errorf("usage = %+v", resp.Usage)
	}

	got := srv.Requests()[0]
	if got.Path != "/v1/messages" || got.Header.Get("x-api-key") != "sk-test" || got.Header.Get("anthropic-version") != apiVersion {
		t.Errorf("request path/header = %s %v", got.Path, got.Header)
	}
	var body messageRequest
	if err := json.Unmarshal(got.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.System != "Be brief." || len(body.Messages) != 1 || body.Messages[0].Content != "Hi\n\nthere" {
		t.Errorf("system/messages = %q %+v", body.System, body.Messages)
	}
	if body.MaxTokens != defaultMaxTokens || *body.Temperature != 1 || body.StopSequences[0] != "END" || body.Metadata.UserID != "visitor-1" {
		t.Errorf("params = %+v", body)
	}
}

func TestStreamTranslatesEvents(t *testing.T) {
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
	if usage == nil || *usage != (provider.Usage{PromptTokens: 21, CompletionTokens: 6}) {
		t.Errorf("usage = %+v", usage)
	}
}

func TestStreamErrors(t *testing.T) {
	tests := []struct {
		fixture string
		kind    provider.ErrorKind
	}{
		{"stream_overloaded.sse", provider.KindOverloaded},
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

func TestStatusErrorsAreClassified(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		fixture    string
		header     http.Header
		kind       provider.ErrorKind
		retryAfter time.Duration
	}{
		{"overloaded", 529, "error_overloaded.json", nil, provider.KindOverloaded, 0},
		{"rate limited", http.StatusTooManyRequests, "error_overloaded.json", http.Header{"Retry-After": {"3"}}, provider.KindRateLimited, 3 * time.Second},
		{"server", http.StatusInternalServerError, "error_overloaded.json", nil, provider.KindServer, 0},
		{"client", http.StatusBadRequest, "error_invalid_request.json", nil, provider.KindClient, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := providertest.NewServer(t, providertest.Reply{Status: tt.status, Fixture: tt.fixture, Header: tt.header})
			_, err := newClient(srv.URL).Complete(t.Context(), sampleRequest())
			providertest.RequireKind(t, err, tt.kind)
			perr, _ := provider.AsError(err)
			if perr.RetryAfter != tt.retryAfter || perr.Status != tt.status {
				t.Errorf("err = %+v", perr)
			}
		})
	}
}

func TestConnectionErrorIsClassified(t *testing.T) {
	_, err := newClient("http://127.0.0.1:1").Complete(t.Context(), sampleRequest())
	providertest.RequireKind(t, err, provider.KindConnection)
}
