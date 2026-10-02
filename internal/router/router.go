package router

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/LZafiro/llm-gateway/internal/config"
	"github.com/LZafiro/llm-gateway/internal/provider"
)

type Attempt struct {
	Target  Target
	Kind    provider.ErrorKind
	Status  int
	Latency time.Duration
}

type Outcome struct {
	Target   Target
	Attempts []Attempt
}

type ExhaustedError struct {
	Route Route
	Last  error
}

func (e *ExhaustedError) Error() string {
	return fmt.Sprintf("all providers unavailable for %q: %v", e.Route.Requested, e.Last)
}

func (e *ExhaustedError) Unwrap() error {
	return e.Last
}

type Stream struct {
	First provider.Chunk
	provider.ChunkStream
}

type Router struct {
	table     table
	providers map[string]provider.Provider
	now       func() time.Time
}

func New(cfg config.Routes, providers map[string]provider.Provider) (*Router, error) {
	t, err := newTable(cfg, providers)
	if err != nil {
		return nil, err
	}
	return &Router{table: t, providers: providers, now: time.Now}, nil
}

func (r *Router) Resolve(model string) (Route, error) {
	return r.table.resolve(model)
}

func (r *Router) Models() []Model {
	return r.table.models
}

func (r *Router) Complete(ctx context.Context, route Route, req provider.ChatRequest) (provider.ChatResponse, Outcome, error) {
	var resp provider.ChatResponse
	outcome, err := r.run(ctx, route, func(ctx context.Context, p provider.Provider, req provider.ChatRequest) error {
		var err error
		resp, err = p.Complete(ctx, req)
		return err
	}, req)
	return resp, outcome, err
}

func (r *Router) Stream(ctx context.Context, route Route, req provider.ChatRequest) (*Stream, Outcome, error) {
	var out *Stream
	outcome, err := r.run(ctx, route, func(ctx context.Context, p provider.Provider, req provider.ChatRequest) error {
		upstream, err := p.Stream(ctx, req)
		if err != nil {
			return err
		}
		first, err := upstream.Next()
		if err != nil {
			_ = upstream.Close()
			if errors.Is(err, io.EOF) {
				return provider.MalformedError(p.Name(), errors.New("stream ended before first chunk"))
			}
			return err
		}
		out = &Stream{First: first, ChunkStream: upstream}
		return nil
	}, req)
	return out, outcome, err
}

type call func(ctx context.Context, p provider.Provider, req provider.ChatRequest) error

func (r *Router) run(ctx context.Context, route Route, do call, req provider.ChatRequest) (Outcome, error) {
	var outcome Outcome
	var last error
	for _, target := range route.Targets {
		p := r.providers[target.Provider]
		req.Model = target.Model
		start := r.now()
		err := do(ctx, p, req)
		attempt := Attempt{Target: target, Latency: r.now().Sub(start)}
		if err == nil {
			outcome.Attempts = append(outcome.Attempts, attempt)
			outcome.Target = target
			return outcome, nil
		}
		perr, ok := provider.AsError(err)
		if !ok {
			perr = &provider.Error{Provider: target.Provider, Kind: provider.KindMalformed, Err: err}
		}
		attempt.Kind, attempt.Status = perr.Kind, perr.Status
		outcome.Attempts = append(outcome.Attempts, attempt)
		if !perr.AllowsFailover() {
			return outcome, perr
		}
		last = perr
	}
	return outcome, &ExhaustedError{Route: route, Last: last}
}
