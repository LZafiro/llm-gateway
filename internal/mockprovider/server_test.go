package mockprovider

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LZafiro/llm-gateway/internal/provider"
	"github.com/LZafiro/llm-gateway/internal/provider/anthropic"
	"github.com/LZafiro/llm-gateway/internal/provider/openai"
	"github.com/LZafiro/llm-gateway/internal/provider/providertest"
)

func TestAdaptersRoundTripThroughMockServer(t *testing.T) {
	srv := httptest.NewServer(New(0).Handler())
	t.Cleanup(srv.Close)
	httpClient := provider.NewHTTPClient(5 * time.Second)
	clients := []provider.Provider{
		anthropic.New(srv.URL, "k", httpClient),
		openai.New(srv.URL, "k", httpClient),
	}
	req := provider.ChatRequest{Model: "m", Messages: []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "ping"},
	}}
	want := "This is a mock response to: ping"
	for _, c := range clients {
		t.Run(c.Name(), func(t *testing.T) {
			resp, err := c.Complete(t.Context(), req)
			if err != nil || resp.Content != want || resp.FinishReason != "stop" || resp.Usage.PromptTokens == 0 {
				t.Fatalf("Complete = %+v, %v", resp, err)
			}
			stream, err := c.Stream(t.Context(), req)
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			chunks, err := providertest.Drain(stream)
			if err != nil {
				t.Fatalf("Drain: %v", err)
			}
			content, finish, usage := providertest.Concat(chunks)
			if content != want || finish != "stop" || usage == nil || *usage != resp.Usage {
				t.Errorf("stream = %q %q %+v, want usage %+v", content, finish, usage, resp.Usage)
			}
		})
	}
}
