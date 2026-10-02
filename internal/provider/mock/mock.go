package mock

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/LZafiro/llm-gateway/internal/provider"
)

const (
	Name         = "mock"
	maxEcho      = 200
	charsPerWord = 4
)

type Provider struct {
	latency time.Duration
}

func New(latency time.Duration) *Provider {
	return &Provider{latency: latency}
}

func (p *Provider) Name() string {
	return Name
}

func (p *Provider) Complete(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	if err := p.wait(ctx); err != nil {
		return provider.ChatResponse{}, err
	}
	content := Reply(req.Messages)
	return provider.ChatResponse{Content: content, FinishReason: "stop", Usage: EstimateUsage(req.Messages, content)}, nil
}

func (p *Provider) Stream(ctx context.Context, req provider.ChatRequest) (provider.ChunkStream, error) {
	if err := p.wait(ctx); err != nil {
		return nil, err
	}
	content := Reply(req.Messages)
	return &stream{pieces: Split(content), usage: EstimateUsage(req.Messages, content)}, nil
}

func (p *Provider) wait(ctx context.Context) error {
	if p.latency <= 0 {
		return nil
	}
	timer := time.NewTimer(p.latency)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return provider.TransportError(ctx, Name, ctx.Err())
	}
}

func Reply(messages []provider.Message) string {
	var last string
	for _, m := range messages {
		if m.Role == provider.RoleUser {
			last = m.Content
		}
	}
	last = strings.Join(strings.Fields(last), " ")
	if runes := []rune(last); len(runes) > maxEcho {
		last = string(runes[:maxEcho]) + "..."
	}
	return "This is a mock response to: " + last
}

func Split(content string) []string {
	words := strings.SplitAfter(content, " ")
	pieces := make([]string, 0, len(words))
	for _, w := range words {
		if w != "" {
			pieces = append(pieces, w)
		}
	}
	return pieces
}

func EstimateUsage(messages []provider.Message, completion string) provider.Usage {
	var prompt int
	for _, m := range messages {
		prompt += estimateTokens(m.Content)
	}
	return provider.Usage{PromptTokens: prompt, CompletionTokens: estimateTokens(completion)}
}

func estimateTokens(s string) int {
	return (len(s) + charsPerWord - 1) / charsPerWord
}

type stream struct {
	pieces []string
	usage  provider.Usage
	next   int
}

func (s *stream) Next() (provider.Chunk, error) {
	switch {
	case s.next < len(s.pieces):
		s.next++
		return provider.Chunk{Content: s.pieces[s.next-1]}, nil
	case s.next == len(s.pieces):
		s.next++
		usage := s.usage
		return provider.Chunk{FinishReason: "stop", Usage: &usage}, nil
	default:
		return provider.Chunk{}, io.EOF
	}
}

func (s *stream) Close() error {
	return nil
}
