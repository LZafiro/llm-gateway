package chaos

import (
	"context"
	"net/http"
	"time"

	"github.com/LZafiro/llm-gateway/internal/provider"
)

type Provider struct {
	inner provider.Provider
	store *Store
	rand  func() float64
}

func Wrap(inner provider.Provider, store *Store, rand func() float64) *Provider {
	return &Provider{inner: inner, store: store, rand: rand}
}

func (p *Provider) Name() string {
	return p.inner.Name()
}

func (p *Provider) Complete(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	if err := p.inject(ctx); err != nil {
		return provider.ChatResponse{}, err
	}
	return p.inner.Complete(ctx, req)
}

func (p *Provider) Stream(ctx context.Context, req provider.ChatRequest) (provider.ChunkStream, error) {
	if err := p.inject(ctx); err != nil {
		return nil, err
	}
	return p.inner.Stream(ctx, req)
}

func (p *Provider) inject(ctx context.Context) error {
	rule, ok := p.store.Get(p.Name())
	if !ok {
		return nil
	}
	delay := rule.Latency
	if rule.Jitter > 0 {
		delay += time.Duration(p.rand() * float64(rule.Jitter))
	}
	if delay > 0 {
		if err := wait(ctx, delay); err != nil {
			return provider.TransportError(ctx, p.Name(), err)
		}
	}
	if rule.Down {
		return &provider.Error{Provider: p.Name(), Kind: provider.KindConnection, Injected: true, Message: "chaos: provider is down"}
	}
	if rule.ErrorRate > 0 && p.rand() < rule.ErrorRate {
		return &provider.Error{Provider: p.Name(), Kind: provider.KindServer, Status: http.StatusServiceUnavailable, Injected: true, Message: "chaos: injected server error"}
	}
	return nil
}

func wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
