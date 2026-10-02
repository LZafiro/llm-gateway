package gateway

import (
	"context"

	"github.com/LZafiro/llm-gateway/internal/provider"
	"github.com/LZafiro/llm-gateway/internal/router"
)

type CacheStatus string

const (
	CacheMiss     CacheStatus = "miss"
	CacheExact    CacheStatus = "exact"
	CacheSemantic CacheStatus = "semantic"
	CacheBypass   CacheStatus = "bypass"
)

type Request struct {
	Model string
	Chat  provider.ChatRequest
}

type Meta struct {
	Provider string
	Model    string
	Attempts int
	Cache    CacheStatus
}

type Completion struct {
	Meta     Meta
	Response provider.ChatResponse
}

type Streaming struct {
	Meta  Meta
	First provider.Chunk
	Rest  provider.ChunkStream
}

type Service struct {
	router *router.Router
}

func New(r *router.Router) *Service {
	return &Service{router: r}
}

func (s *Service) Models() []router.Model {
	return s.router.Models()
}

func (s *Service) Complete(ctx context.Context, req Request) (Completion, error) {
	route, err := s.router.Resolve(req.Model)
	if err != nil {
		return Completion{Meta: Meta{Cache: CacheMiss}}, err
	}
	resp, outcome, err := s.router.Complete(ctx, route, req.Chat)
	return Completion{Meta: meta(outcome), Response: resp}, err
}

func (s *Service) Stream(ctx context.Context, req Request) (Streaming, error) {
	route, err := s.router.Resolve(req.Model)
	if err != nil {
		return Streaming{Meta: Meta{Cache: CacheMiss}}, err
	}
	stream, outcome, err := s.router.Stream(ctx, route, req.Chat)
	if err != nil {
		return Streaming{Meta: meta(outcome)}, err
	}
	return Streaming{Meta: meta(outcome), First: stream.First, Rest: stream}, nil
}

func meta(outcome router.Outcome) Meta {
	return Meta{
		Provider: outcome.Target.Provider,
		Model:    outcome.Target.Model,
		Attempts: len(outcome.Attempts),
		Cache:    CacheMiss,
	}
}
