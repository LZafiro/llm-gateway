package router

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/LZafiro/llm-gateway/internal/config"
	"github.com/LZafiro/llm-gateway/internal/provider"
	"github.com/LZafiro/llm-gateway/internal/provider/providertest"
)

var routes = config.Routes{
	Aliases: map[string][]string{"fast": {"a/model-a", "b/model-b"}},
	Direct:  []string{"a/model-a", "b/model-b", "b/shared", "c/shared"},
}

var resilience = config.Resilience{
	MaxAttemptsPerProvider: 2,
	BackoffBase:            100 * time.Millisecond,
	BackoffCap:             time.Second,
	AttemptTimeout:         15 * time.Second,
	RequestDeadline:        30 * time.Second,
}

func noSleep(context.Context, time.Duration) error { return nil }

func newRouter(t *testing.T, a, b *providertest.Fake) *Router {
	t.Helper()
	r, err := New(routes, map[string]provider.Provider{"a": a, "b": b, "c": &providertest.Fake{ProviderName: "c"}}, Options{Resilience: resilience, Sleep: noSleep})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestResolve(t *testing.T) {
	r := newRouter(t, &providertest.Fake{ProviderName: "a"}, &providertest.Fake{ProviderName: "b"})
	tests := []struct {
		model string
		want  []Target
	}{
		{"fast", []Target{{"a", "model-a"}, {"b", "model-b"}}},
		{"a/model-a", []Target{{"a", "model-a"}}},
		{"model-b", []Target{{"b", "model-b"}}},
	}
	for _, tt := range tests {
		route, err := r.Resolve(tt.model)
		if err != nil || !reflect.DeepEqual(route.Targets, tt.want) {
			t.Errorf("Resolve(%q) = %+v, %v; want %+v", tt.model, route.Targets, err, tt.want)
		}
	}
	for _, model := range []string{"shared", "unknown", "x/model-a"} {
		if _, err := r.Resolve(model); !errors.Is(err, ErrModelNotFound) {
			t.Errorf("Resolve(%q) err = %v, want ErrModelNotFound", model, err)
		}
	}
}

func TestNewRejectsUnavailableProvider(t *testing.T) {
	_, err := New(config.Routes{Direct: []string{"missing/m"}}, map[string]provider.Provider{}, Options{Resilience: resilience})
	if err == nil {
		t.Fatal("want error for unavailable provider")
	}
}

func TestModelsListsAliasesThenDirect(t *testing.T) {
	r := newRouter(t, &providertest.Fake{ProviderName: "a"}, &providertest.Fake{ProviderName: "b"})
	models := r.Models()
	if models[0] != (Model{ID: "fast", OwnedBy: "gateway"}) || models[1] != (Model{ID: "a/model-a", OwnedBy: "a"}) || len(models) != 5 {
		t.Errorf("models = %+v", models)
	}
}

func TestCompleteFailsOverOnRetryableError(t *testing.T) {
	a := &providertest.Fake{ProviderName: "a", Err: &provider.Error{Provider: "a", Kind: provider.KindServer, Status: 503}}
	b := &providertest.Fake{ProviderName: "b", Response: provider.ChatResponse{Content: "from b"}}
	r := newRouter(t, a, b)
	route, _ := r.Resolve("fast")
	resp, outcome, err := r.Complete(t.Context(), route, provider.ChatRequest{})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "from b" || outcome.Target != (Target{"b", "model-b"}) || b.LastModel() != "model-b" {
		t.Errorf("resp = %+v, outcome = %+v", resp, outcome)
	}
	if len(outcome.Attempts) != 3 || outcome.Attempts[0].Kind != provider.KindServer || outcome.Attempts[1].Kind != provider.KindServer || outcome.Attempts[2].Kind != "" {
		t.Errorf("attempts = %+v", outcome.Attempts)
	}
	if a.Calls() != 2 {
		t.Errorf("a called %d times, want 2", a.Calls())
	}
}

func TestCompleteDoesNotFailOverOnClientError(t *testing.T) {
	a := &providertest.Fake{ProviderName: "a", Err: &provider.Error{Provider: "a", Kind: provider.KindClient, Status: 400}}
	b := &providertest.Fake{ProviderName: "b"}
	r := newRouter(t, a, b)
	route, _ := r.Resolve("fast")
	_, _, err := r.Complete(t.Context(), route, provider.ChatRequest{})
	providertest.RequireKind(t, err, provider.KindClient)
	if b.Calls() != 0 {
		t.Errorf("b called %d times, want 0", b.Calls())
	}
}

func TestCompleteReportsExhaustion(t *testing.T) {
	down := &provider.Error{Kind: provider.KindConnection}
	r := newRouter(t, &providertest.Fake{ProviderName: "a", Err: down}, &providertest.Fake{ProviderName: "b", Err: down})
	route, _ := r.Resolve("fast")
	_, outcome, err := r.Complete(t.Context(), route, provider.ChatRequest{})
	var exhausted *ExhaustedError
	if !errors.As(err, &exhausted) || len(outcome.Attempts) != 4 {
		t.Fatalf("err = %v, attempts = %d", err, len(outcome.Attempts))
	}
}

func TestStreamFailsOverBeforeFirstChunk(t *testing.T) {
	tests := []struct {
		name string
		a    *providertest.Fake
	}{
		{"open fails", &providertest.Fake{ProviderName: "a", Err: &provider.Error{Kind: provider.KindOverloaded}}},
		{"first chunk fails", &providertest.Fake{ProviderName: "a", StreamErr: &provider.Error{Kind: provider.KindServer}}},
		{"empty stream", &providertest.Fake{ProviderName: "a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &providertest.Fake{ProviderName: "b", Chunks: []provider.Chunk{{Content: "hi"}, {FinishReason: "stop"}}}
			r := newRouter(t, tt.a, b)
			route, _ := r.Resolve("fast")
			stream, outcome, err := r.Stream(t.Context(), route, provider.ChatRequest{})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			if stream.First.Content != "hi" || outcome.Target.Provider != "b" {
				t.Errorf("first = %+v, outcome = %+v", stream.First, outcome)
			}
			rest, err := providertest.Drain(stream)
			if err != nil || len(rest) != 1 || rest[0].FinishReason != "stop" {
				t.Errorf("rest = %+v, err = %v", rest, err)
			}
		})
	}
}

func TestStreamCommitsAfterFirstChunk(t *testing.T) {
	a := &providertest.Fake{ProviderName: "a", Chunks: []provider.Chunk{{Content: "par"}}, StreamErr: &provider.Error{Kind: provider.KindServer}}
	b := &providertest.Fake{ProviderName: "b"}
	r := newRouter(t, a, b)
	route, _ := r.Resolve("fast")
	stream, outcome, err := r.Stream(t.Context(), route, provider.ChatRequest{})
	if err != nil || outcome.Target.Provider != "a" {
		t.Fatalf("Stream: %v, outcome = %+v", err, outcome)
	}
	_, err = providertest.Drain(stream)
	providertest.RequireKind(t, err, provider.KindServer)
	if b.Calls() != 0 {
		t.Errorf("b called after commit")
	}
}
