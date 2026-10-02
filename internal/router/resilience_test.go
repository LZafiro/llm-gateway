package router

import (
	"context"
	"errors"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/LZafiro/llm-gateway/internal/breaker"
	"github.com/LZafiro/llm-gateway/internal/config"
	"github.com/LZafiro/llm-gateway/internal/provider"
	"github.com/LZafiro/llm-gateway/internal/provider/providertest"
)

type funcProvider struct {
	name     string
	complete func(ctx context.Context) (provider.ChatResponse, error)
	stream   func(ctx context.Context) (provider.ChunkStream, error)
}

func (f *funcProvider) Name() string { return f.name }

func (f *funcProvider) Complete(ctx context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return f.complete(ctx)
}

func (f *funcProvider) Stream(ctx context.Context, _ provider.ChatRequest) (provider.ChunkStream, error) {
	return f.stream(ctx)
}

func blockUntilDone(name string) func(ctx context.Context) (provider.ChatResponse, error) {
	return func(ctx context.Context) (provider.ChatResponse, error) {
		<-ctx.Done()
		return provider.ChatResponse{}, provider.TransportError(ctx, name, ctx.Err())
	}
}

type sleepSpy struct {
	mu    sync.Mutex
	waits []time.Duration
}

func (s *sleepSpy) sleep(_ context.Context, d time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waits = append(s.waits, d)
	return nil
}

func build(t *testing.T, a, b provider.Provider, opts Options) *Router {
	t.Helper()
	if opts.Resilience == (config.Resilience{}) {
		opts.Resilience = resilience
	}
	if opts.Sleep == nil {
		opts.Sleep = noSleep
	}
	r, err := New(config.Routes{Aliases: map[string][]string{"fast": {"a/model-a", "b/model-b"}}}, map[string]provider.Provider{"a": a, "b": b}, opts)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func complete(t *testing.T, r *Router) (provider.ChatResponse, Outcome, error) {
	t.Helper()
	route, err := r.Resolve("fast")
	if err != nil {
		t.Fatal(err)
	}
	return r.Complete(t.Context(), route, provider.ChatRequest{})
}

func ok(name string) *providertest.Fake {
	return &providertest.Fake{ProviderName: name, Response: provider.ChatResponse{Content: "from " + name}}
}

func TestBackoffUsesFullJitter(t *testing.T) {
	r := &Router{cfg: resilience, rand: func() float64 { return 0.5 }}
	tests := []struct {
		attempt    int
		retryAfter time.Duration
		want       time.Duration
		ok         bool
	}{
		{1, 0, 50 * time.Millisecond, true},
		{2, 0, 100 * time.Millisecond, true},
		{5, 0, 500 * time.Millisecond, true},
		{1, 300 * time.Millisecond, 300 * time.Millisecond, true},
		{1, 2 * time.Second, 2 * time.Second, false},
	}
	for _, tt := range tests {
		got, ok := r.backoff(tt.attempt, tt.retryAfter)
		if got != tt.want || ok != tt.ok {
			t.Errorf("backoff(%d, %v) = %v, %v; want %v, %v", tt.attempt, tt.retryAfter, got, ok, tt.want, tt.ok)
		}
	}
}

func TestRetriesWithBackoffBeforeFailingOver(t *testing.T) {
	spy := &sleepSpy{}
	a := &providertest.Fake{ProviderName: "a", Err: &provider.Error{Kind: provider.KindServer}}
	r := build(t, a, ok("b"), Options{Sleep: spy.sleep, Rand: func() float64 { return 1 }})
	resp, outcome, err := complete(t, r)
	if err != nil || resp.Content != "from b" {
		t.Fatalf("resp = %+v, err = %v", resp, err)
	}
	if a.Calls() != 2 || len(outcome.Attempts) != 3 {
		t.Errorf("a calls = %d, attempts = %d", a.Calls(), len(outcome.Attempts))
	}
	if len(spy.waits) != 1 || spy.waits[0] != 100*time.Millisecond {
		t.Errorf("waits = %v", spy.waits)
	}
}

func TestRetryAfterBeyondCapFailsOverImmediately(t *testing.T) {
	spy := &sleepSpy{}
	a := &providertest.Fake{ProviderName: "a", Err: &provider.Error{Kind: provider.KindRateLimited, RetryAfter: 5 * time.Second}}
	r := build(t, a, ok("b"), Options{Sleep: spy.sleep})
	if _, _, err := complete(t, r); err != nil {
		t.Fatal(err)
	}
	if a.Calls() != 1 || len(spy.waits) != 0 {
		t.Errorf("a calls = %d, waits = %v", a.Calls(), spy.waits)
	}
}

func TestMalformedFailsOverWithoutRetry(t *testing.T) {
	a := &providertest.Fake{ProviderName: "a", Err: &provider.Error{Kind: provider.KindMalformed}}
	r := build(t, a, ok("b"), Options{})
	if _, _, err := complete(t, r); err != nil {
		t.Fatal(err)
	}
	if a.Calls() != 1 {
		t.Errorf("a calls = %d, want 1", a.Calls())
	}
}

func TestOpenBreakerSkipsProvider(t *testing.T) {
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	a := &providertest.Fake{ProviderName: "a", Err: &provider.Error{Kind: provider.KindConnection, Injected: true}}
	breakers := breaker.NewSet([]string{"a", "b"}, config.Default().Breaker, clock, nil)
	r := build(t, a, ok("b"), Options{Breakers: breakers, Now: clock})

	attempts := []int{}
	for range 4 {
		_, outcome, err := complete(t, r)
		if err != nil {
			t.Fatal(err)
		}
		attempts = append(attempts, len(outcome.Attempts))
	}
	if want := []int{3, 3, 2, 1}; !slices.Equal(attempts, want) {
		t.Errorf("attempts per request = %v, want %v", attempts, want)
	}
	if breakers.For("a").State() != breaker.Open || a.Calls() != 5 {
		t.Errorf("breaker = %s, a calls = %d", breakers.For("a").State(), a.Calls())
	}
	_, outcome, _ := complete(t, r)
	if len(outcome.Skipped) != 1 || outcome.Skipped[0].Provider != "a" {
		t.Errorf("skipped = %+v", outcome.Skipped)
	}
}

func TestInjectedFlagReachesAttemptTrace(t *testing.T) {
	a := &providertest.Fake{ProviderName: "a", Err: &provider.Error{Kind: provider.KindConnection, Injected: true}}
	_, outcome, _ := complete(t, build(t, a, ok("b"), Options{}))
	if !outcome.Attempts[0].Injected || outcome.Attempts[2].Injected {
		t.Errorf("attempts = %+v", outcome.Attempts)
	}
}

func TestAttemptTimeoutRetriesThenFailsOver(t *testing.T) {
	a := &funcProvider{name: "a", complete: blockUntilDone("a")}
	cfg := resilience
	cfg.AttemptTimeout = 20 * time.Millisecond
	r := build(t, a, ok("b"), Options{Resilience: cfg})
	resp, outcome, err := complete(t, r)
	if err != nil || resp.Content != "from b" {
		t.Fatalf("resp = %+v, err = %v", resp, err)
	}
	if outcome.Attempts[0].Kind != provider.KindTimeout || outcome.Attempts[1].Kind != provider.KindTimeout {
		t.Errorf("attempts = %+v", outcome.Attempts)
	}
}

func TestRequestDeadlineIsEnforced(t *testing.T) {
	block := &funcProvider{name: "a", complete: blockUntilDone("a")}
	cfg := resilience
	cfg.AttemptTimeout = 40 * time.Millisecond
	cfg.RequestDeadline = 60 * time.Millisecond
	r := build(t, block, &funcProvider{name: "b", complete: blockUntilDone("b")}, Options{Resilience: cfg})
	start := time.Now()
	_, _, err := complete(t, r)
	elapsed := time.Since(start)
	if !errors.Is(err, ErrDeadline) {
		t.Fatalf("err = %v, want ErrDeadline", err)
	}
	if elapsed > cfg.RequestDeadline+50*time.Millisecond {
		t.Errorf("elapsed %v exceeds deadline + 50ms", elapsed)
	}
}

func TestClientCancellationStopsRouting(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	a := &funcProvider{name: "a", complete: func(ctx context.Context) (provider.ChatResponse, error) {
		cancel()
		<-ctx.Done()
		return provider.ChatResponse{}, provider.TransportError(ctx, "a", ctx.Err())
	}}
	b := ok("b")
	r := build(t, a, b, Options{})
	route, _ := r.Resolve("fast")
	_, _, err := r.Complete(ctx, route, provider.ChatRequest{})
	providertest.RequireKind(t, err, provider.KindCanceled)
	if b.Calls() != 0 {
		t.Errorf("b called after cancellation")
	}
}

type slowStream struct {
	ctx   context.Context
	sent  int
	pause time.Duration
}

func (s *slowStream) Next() (provider.Chunk, error) {
	s.sent++
	switch s.sent {
	case 1:
		return provider.Chunk{Content: "first"}, nil
	case 2:
		select {
		case <-time.After(s.pause):
			return provider.Chunk{Content: "second"}, nil
		case <-s.ctx.Done():
			return provider.Chunk{}, provider.TransportError(s.ctx, "a", s.ctx.Err())
		}
	default:
		return provider.Chunk{}, io.EOF
	}
}

func (s *slowStream) Close() error { return nil }

func TestCommittedStreamOutlivesAttemptTimeout(t *testing.T) {
	cfg := resilience
	cfg.AttemptTimeout = 20 * time.Millisecond
	cfg.RequestDeadline = 40 * time.Millisecond
	a := &funcProvider{name: "a", stream: func(ctx context.Context) (provider.ChunkStream, error) {
		return &slowStream{ctx: ctx, pause: 80 * time.Millisecond}, nil
	}}
	r := build(t, a, ok("b"), Options{Resilience: cfg})
	route, _ := r.Resolve("fast")
	stream, _, err := r.Stream(t.Context(), route, provider.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	rest, err := providertest.Drain(stream)
	if err != nil || len(rest) != 1 || rest[0].Content != "second" {
		t.Fatalf("rest = %+v, err = %v", rest, err)
	}
}
