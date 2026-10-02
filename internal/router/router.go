package router

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"time"

	"github.com/LZafiro/llm-gateway/internal/breaker"
	"github.com/LZafiro/llm-gateway/internal/config"
	"github.com/LZafiro/llm-gateway/internal/provider"
)

var (
	ErrDeadline        = errors.New("request deadline exceeded")
	errAttemptTimeout  = fmt.Errorf("attempt timeout: %w", context.DeadlineExceeded)
	errRequestDeadline = fmt.Errorf("%w: %w", ErrDeadline, context.DeadlineExceeded)
)

type Attempt struct {
	Target   Target
	Kind     provider.ErrorKind
	Status   int
	Injected bool
	Latency  time.Duration
}

type Outcome struct {
	Target   Target
	Attempts []Attempt
	Skipped  []Target
	Waited   time.Duration
}

func (o Outcome) UpstreamTime() time.Duration {
	total := o.Waited
	for _, a := range o.Attempts {
		total += a.Latency
	}
	return total
}

type ExhaustedError struct {
	Route Route
	Last  error
}

func (e *ExhaustedError) Error() string {
	if e.Last == nil {
		return fmt.Sprintf("all providers unavailable for %q: every breaker is open", e.Route.Requested)
	}
	return fmt.Sprintf("all providers unavailable for %q: %v", e.Route.Requested, e.Last)
}

func (e *ExhaustedError) Unwrap() error {
	return e.Last
}

type Stream struct {
	First provider.Chunk
	provider.ChunkStream
}

type Options struct {
	Resilience config.Resilience
	Breakers   *breaker.Set
	Now        func() time.Time
	Sleep      func(ctx context.Context, d time.Duration) error
	Rand       func() float64
}

type Router struct {
	table     table
	providers map[string]provider.Provider
	cfg       config.Resilience
	breakers  *breaker.Set
	now       func() time.Time
	sleep     func(ctx context.Context, d time.Duration) error
	rand      func() float64
}

func New(routes config.Routes, providers map[string]provider.Provider, opts Options) (*Router, error) {
	t, err := newTable(routes, providers)
	if err != nil {
		return nil, err
	}
	r := &Router{table: t, providers: providers, cfg: opts.Resilience, breakers: opts.Breakers, now: opts.Now, sleep: opts.Sleep, rand: opts.Rand}
	if r.now == nil {
		r.now = time.Now
	}
	if r.sleep == nil {
		r.sleep = sleep
	}
	if r.rand == nil {
		r.rand = rand.Float64
	}
	if r.breakers == nil {
		names := make([]string, 0, len(providers))
		for name := range providers {
			names = append(names, name)
		}
		r.breakers = breaker.NewSet(names, config.Default().Breaker, r.now, nil)
	}
	return r, nil
}

func (r *Router) Resolve(model string) (Route, error) {
	return r.table.resolve(model)
}

func (r *Router) Models() []Model {
	return r.table.models
}

func (r *Router) Complete(ctx context.Context, route Route, req provider.ChatRequest) (provider.ChatResponse, Outcome, error) {
	var resp provider.ChatResponse
	outcome, err := r.run(ctx, route, req, func(g guard, p provider.Provider, req provider.ChatRequest) error {
		defer g.release()
		var err error
		resp, err = p.Complete(g.ctx, req)
		return err
	})
	return resp, outcome, err
}

func (r *Router) Stream(ctx context.Context, route Route, req provider.ChatRequest) (*Stream, Outcome, error) {
	var out *Stream
	outcome, err := r.run(ctx, route, req, func(g guard, p provider.Provider, req provider.ChatRequest) error {
		upstream, err := p.Stream(g.ctx, req)
		if err != nil {
			g.release()
			return err
		}
		first, err := upstream.Next()
		if err != nil {
			_ = upstream.Close()
			g.release()
			if errors.Is(err, io.EOF) {
				return provider.MalformedError(p.Name(), errors.New("stream ended before first chunk"))
			}
			return err
		}
		g.commit()
		out = &Stream{First: first, ChunkStream: &releasingStream{ChunkStream: upstream, release: g.release}}
		return nil
	})
	return out, outcome, err
}

type guard struct {
	ctx     context.Context
	commit  func()
	release func()
}

func (g guard) deadlineHit() bool {
	return errors.Is(context.Cause(g.ctx), ErrDeadline)
}

type call func(g guard, p provider.Provider, req provider.ChatRequest) error

func (r *Router) run(ctx context.Context, route Route, req provider.ChatRequest, do call) (Outcome, error) {
	deadline := r.now().Add(r.cfg.RequestDeadline)
	var outcome Outcome
	var last error
	for _, target := range route.Targets {
		p := r.providers[target.Provider]
		b := r.breakers.For(target.Provider)
		req.Model = target.Model
		for n := 1; n <= r.cfg.MaxAttemptsPerProvider; n++ {
			ticket, ok := b.Allow()
			if !ok {
				if n == 1 {
					outcome.Skipped = append(outcome.Skipped, target)
				}
				break
			}
			if !r.now().Before(deadline) {
				b.Record(ticket, breaker.Ignored)
				return outcome, ErrDeadline
			}
			g := r.guard(ctx, deadline)
			start := r.now()
			err := do(g, p, req)
			attempt := Attempt{Target: target, Latency: r.now().Sub(start)}
			if err == nil {
				b.Record(ticket, breaker.Success)
				outcome.Attempts = append(outcome.Attempts, attempt)
				outcome.Target = target
				return outcome, nil
			}
			perr := asProviderError(target, err)
			attempt.Kind, attempt.Status, attempt.Injected = perr.Kind, perr.Status, perr.Injected
			outcome.Attempts = append(outcome.Attempts, attempt)
			b.Record(ticket, breakerOutcome(perr))
			switch {
			case g.deadlineHit():
				return outcome, ErrDeadline
			case ctx.Err() != nil:
				return outcome, provider.TransportError(ctx, target.Provider, ctx.Err())
			case !perr.AllowsFailover():
				return outcome, perr
			}
			last = perr
			if !perr.Retryable() || n == r.cfg.MaxAttemptsPerProvider {
				break
			}
			wait, ok := r.backoff(n, perr.RetryAfter)
			if !ok || !r.now().Add(wait).Before(deadline) {
				break
			}
			if err := r.sleep(ctx, wait); err != nil {
				return outcome, provider.TransportError(ctx, target.Provider, err)
			}
			outcome.Waited += wait
		}
	}
	if !r.now().Before(deadline) {
		return outcome, ErrDeadline
	}
	return outcome, &ExhaustedError{Route: route, Last: last}
}

func (r *Router) guard(parent context.Context, deadline time.Time) guard {
	ctx, cancel := context.WithCancelCause(parent)
	timeout, cause := r.cfg.AttemptTimeout, errAttemptTimeout
	if remaining := deadline.Sub(r.now()); remaining <= timeout {
		timeout, cause = remaining, errRequestDeadline
	}
	timer := time.AfterFunc(timeout, func() { cancel(cause) })
	return guard{
		ctx:     ctx,
		commit:  func() { timer.Stop() },
		release: func() { timer.Stop(); cancel(context.Canceled) },
	}
}

func (r *Router) backoff(attempt int, retryAfter time.Duration) (time.Duration, bool) {
	ceiling := min(r.cfg.BackoffCap, r.cfg.BackoffBase<<(attempt-1))
	wait := time.Duration(r.rand() * float64(ceiling))
	if retryAfter > 0 {
		wait = max(wait, retryAfter)
	}
	return wait, wait <= r.cfg.BackoffCap
}

func asProviderError(target Target, err error) *provider.Error {
	if perr, ok := provider.AsError(err); ok {
		return perr
	}
	return &provider.Error{Provider: target.Provider, Kind: provider.KindMalformed, Err: err}
}

func breakerOutcome(perr *provider.Error) breaker.Outcome {
	if perr.CountsAsFailure() {
		return breaker.Failure
	}
	return breaker.Ignored
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type releasingStream struct {
	provider.ChunkStream
	release func()
}

func (s *releasingStream) Close() error {
	defer s.release()
	return s.ChunkStream.Close()
}
