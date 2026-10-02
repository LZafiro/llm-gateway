package mock

import (
	"context"
	"testing"
	"time"

	"github.com/LZafiro/llm-gateway/internal/provider"
	"github.com/LZafiro/llm-gateway/internal/provider/providertest"
)

var request = provider.ChatRequest{Messages: []provider.Message{
	{Role: provider.RoleSystem, Content: "sys"},
	{Role: provider.RoleUser, Content: "What   is\na gateway?"},
}}

func TestCompleteAndStreamAgree(t *testing.T) {
	p := New(0)
	resp, err := p.Complete(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "This is a mock response to: What is a gateway?" || resp.FinishReason != "stop" {
		t.Errorf("resp = %+v", resp)
	}
	stream, err := p.Stream(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := providertest.Drain(stream)
	if err != nil {
		t.Fatal(err)
	}
	content, finish, usage := providertest.Concat(chunks)
	if content != resp.Content || finish != "stop" || usage == nil || *usage != resp.Usage {
		t.Errorf("stream = %q %q %+v, want %q %+v", content, finish, usage, resp.Content, resp.Usage)
	}
}

func TestLatencyHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	_, err := New(time.Second).Complete(ctx, request)
	providertest.RequireKind(t, err, provider.KindTimeout)
}
