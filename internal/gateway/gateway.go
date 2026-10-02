package gateway

import (
	"context"
	"time"

	"github.com/LZafiro/llm-gateway/internal/auth"
	"github.com/LZafiro/llm-gateway/internal/ledger"
	"github.com/LZafiro/llm-gateway/internal/provider"
	"github.com/LZafiro/llm-gateway/internal/ratelimit"
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
	ID     string
	Tenant auth.Key
	Source ledger.Source
	Model  string
	Stream bool
	Chat   provider.ChatRequest
}

type Meta struct {
	Provider  string
	Model     string
	Attempts  int
	Cache     CacheStatus
	RateLimit *ratelimit.Decision
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

type Limiter interface {
	Allow(key int64, rate float64, burst int) ratelimit.Decision
}

type Recorder interface {
	Record(entry ledger.Entry)
}

type Options struct {
	Router   *router.Router
	Limiter  Limiter
	Pricing  ledger.Pricing
	Recorder Recorder
	Now      func() time.Time
}

type Service struct {
	router   *router.Router
	limiter  Limiter
	pricing  ledger.Pricing
	recorder Recorder
	now      func() time.Time
}

func New(opts Options) *Service {
	s := &Service{router: opts.Router, limiter: opts.Limiter, pricing: opts.Pricing, recorder: opts.Recorder, now: opts.Now}
	if s.now == nil {
		s.now = time.Now
	}
	if s.recorder == nil {
		s.recorder = discard{}
	}
	return s
}

func (s *Service) Models() []router.Model {
	return s.router.Models()
}

func (s *Service) Complete(ctx context.Context, req Request) (Completion, error) {
	t := s.begin(req)
	decision, err := s.limit(req)
	if err != nil {
		t.finish(Meta{Cache: CacheMiss, RateLimit: decision}, router.Outcome{}, provider.Usage{}, err)
		return Completion{Meta: Meta{Cache: CacheMiss, RateLimit: decision}}, err
	}
	route, err := s.router.Resolve(req.Model)
	if err != nil {
		t.finish(Meta{Cache: CacheMiss, RateLimit: decision}, router.Outcome{}, provider.Usage{}, err)
		return Completion{Meta: Meta{Cache: CacheMiss, RateLimit: decision}}, err
	}
	resp, outcome, err := s.router.Complete(ctx, route, req.Chat)
	meta := metaFor(outcome, decision)
	overhead := s.now().Sub(t.start) - outcome.UpstreamTime()
	if err == nil {
		t.entry.Overhead = &overhead
	}
	t.finish(meta, outcome, resp.Usage, err)
	return Completion{Meta: meta, Response: resp}, err
}

func (s *Service) Stream(ctx context.Context, req Request) (Streaming, error) {
	t := s.begin(req)
	decision, err := s.limit(req)
	if err != nil {
		t.finish(Meta{Cache: CacheMiss, RateLimit: decision}, router.Outcome{}, provider.Usage{}, err)
		return Streaming{Meta: Meta{Cache: CacheMiss, RateLimit: decision}}, err
	}
	route, err := s.router.Resolve(req.Model)
	if err != nil {
		t.finish(Meta{Cache: CacheMiss, RateLimit: decision}, router.Outcome{}, provider.Usage{}, err)
		return Streaming{Meta: Meta{Cache: CacheMiss, RateLimit: decision}}, err
	}
	stream, outcome, err := s.router.Stream(ctx, route, req.Chat)
	meta := metaFor(outcome, decision)
	if err != nil {
		t.finish(meta, outcome, provider.Usage{}, err)
		return Streaming{Meta: meta}, err
	}
	ttfb := s.now().Sub(t.start)
	overhead := ttfb - outcome.UpstreamTime()
	t.entry.TTFB, t.entry.Overhead = &ttfb, &overhead
	rest := &recordingStream{ChunkStream: stream, tracker: t, meta: meta, outcome: outcome}
	rest.observe(stream.First)
	return Streaming{Meta: meta, First: stream.First, Rest: rest}, nil
}

func (s *Service) limit(req Request) (*ratelimit.Decision, error) {
	if s.limiter == nil {
		return nil, nil
	}
	d := s.limiter.Allow(req.Tenant.ID, req.Tenant.Rate, req.Tenant.Burst)
	if !d.Allowed {
		return &d, &RateLimitError{Decision: d}
	}
	return &d, nil
}

func metaFor(outcome router.Outcome, decision *ratelimit.Decision) Meta {
	return Meta{
		Provider:  outcome.Target.Provider,
		Model:     outcome.Target.Model,
		Attempts:  len(outcome.Attempts),
		Cache:     CacheMiss,
		RateLimit: decision,
	}
}

type Recorders []Recorder

func (rs Recorders) Record(entry ledger.Entry) {
	for _, r := range rs {
		r.Record(entry)
	}
}

type discard struct{}

func (discard) Record(ledger.Entry) {}
