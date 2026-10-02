package chaos

import (
	"context"
	"errors"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LZafiro/llm-gateway/internal/config"
	"github.com/LZafiro/llm-gateway/internal/provider"
	"github.com/LZafiro/llm-gateway/internal/provider/providertest"
)

var chaosConfig = config.Chaos{
	Public:     config.PublicChaos{Enabled: true, DownDuration: 30 * time.Second, Cooldown: 60 * time.Second},
	MaxLatency: 20 * time.Second,
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type change struct {
	provider string
	source   Source
	active   bool
}

func newStore() (*Store, *clock, *[]change) {
	c := &clock{now: time.Unix(1767225600, 0)}
	changes := &[]change{}
	store := NewStore([]string{"anthropic", "openai"}, chaosConfig, c.Now, func(p string, r Rule, active bool) {
		*changes = append(*changes, change{p, r.Source, active})
	})
	return store, c, changes
}

func healthy() *providertest.Fake {
	return &providertest.Fake{ProviderName: "anthropic", Response: provider.ChatResponse{Content: "ok"}}
}

func TestErrorRateIsStatistical(t *testing.T) {
	store, _, _ := newStore()
	if _, err := store.Set("anthropic", Rule{ErrorRate: 0.3}); err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewPCG(1, 2))
	p := Wrap(healthy(), store, rng.Float64)
	failures := 0
	for range 1000 {
		if _, err := p.Complete(t.Context(), provider.ChatRequest{}); err != nil {
			providertest.RequireKind(t, err, provider.KindServer)
			failures++
		}
	}
	if failures < 270 || failures > 330 {
		t.Errorf("failures = %d, want 300 ± 30", failures)
	}
}

func TestDownInjectsConnectionErrorWithoutCallingUpstream(t *testing.T) {
	store, _, _ := newStore()
	inner := healthy()
	if _, err := store.Set("anthropic", Rule{Down: true}); err != nil {
		t.Fatal(err)
	}
	p := Wrap(inner, store, rand.Float64)
	_, err := p.Complete(t.Context(), provider.ChatRequest{})
	providertest.RequireKind(t, err, provider.KindConnection)
	if perr, _ := provider.AsError(err); !perr.Injected || !perr.Retryable() {
		t.Errorf("err = %+v", perr)
	}
	_, err = p.Stream(t.Context(), provider.ChatRequest{})
	providertest.RequireKind(t, err, provider.KindConnection)
	if inner.Calls() != 0 {
		t.Errorf("upstream called %d times", inner.Calls())
	}
}

func TestLatencyIsAddedAndHonorsCancellation(t *testing.T) {
	store, _, _ := newStore()
	if _, err := store.Set("anthropic", Rule{Latency: 30 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	p := Wrap(healthy(), store, rand.Float64)
	start := time.Now()
	if _, err := p.Complete(t.Context(), provider.ChatRequest{}); err != nil || time.Since(start) < 30*time.Millisecond {
		t.Fatalf("err = %v, elapsed = %v", err, time.Since(start))
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer cancel()
	_, err := p.Complete(ctx, provider.ChatRequest{})
	providertest.RequireKind(t, err, provider.KindTimeout)
}

func TestNoRulePassesThrough(t *testing.T) {
	store, _, _ := newStore()
	resp, err := Wrap(healthy(), store, rand.Float64).Complete(t.Context(), provider.ChatRequest{})
	if err != nil || resp.Content != "ok" {
		t.Fatalf("resp = %+v, err = %v", resp, err)
	}
}

func TestPublicDownExpiresAfterDownDuration(t *testing.T) {
	store, c, changes := newStore()
	if _, err := store.PressDown("anthropic"); err != nil {
		t.Fatal(err)
	}
	c.Advance(30*time.Second - time.Nanosecond)
	if _, ok := store.Get("anthropic"); !ok {
		t.Fatal("rule expired early")
	}
	c.Advance(time.Nanosecond)
	if _, ok := store.Get("anthropic"); ok {
		t.Fatal("rule still active at 30s")
	}
	store.Expire()
	want := []change{{"anthropic", SourcePublic, true}, {"anthropic", SourcePublic, false}}
	if len(*changes) != 2 || (*changes)[0] != want[0] || (*changes)[1] != want[1] {
		t.Errorf("changes = %+v", *changes)
	}
}

func TestPublicCooldown(t *testing.T) {
	store, c, _ := newStore()
	if _, err := store.PressDown("anthropic"); err != nil {
		t.Fatal(err)
	}
	c.Advance(45 * time.Second)
	_, err := store.PressDown("anthropic")
	var cooldown *CooldownError
	if !errors.As(err, &cooldown) || cooldown.Remaining != 15*time.Second {
		t.Fatalf("err = %v", err)
	}
	if _, err := store.PressDown("openai"); err != nil {
		t.Fatalf("cooldown leaked across providers: %v", err)
	}
	c.Advance(15 * time.Second)
	if _, err := store.PressDown("anthropic"); err != nil {
		t.Fatalf("after cooldown: %v", err)
	}
}

func TestPublicCannotOverrideAdminRule(t *testing.T) {
	store, _, _ := newStore()
	if _, err := store.Set("anthropic", Rule{ErrorRate: 0.5}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PressDown("anthropic"); !errors.Is(err, ErrAdminRuleActive) {
		t.Fatalf("err = %v", err)
	}
	if rule, _ := store.Get("anthropic"); rule.ErrorRate != 0.5 || rule.Down {
		t.Errorf("admin rule overwritten: %+v", rule)
	}
}

func TestStoreValidation(t *testing.T) {
	store, _, _ := newStore()
	tests := []struct {
		provider string
		rule     Rule
		want     string
	}{
		{"unknown", Rule{}, "unknown provider"},
		{"anthropic", Rule{ErrorRate: 1.5}, "error_rate"},
		{"anthropic", Rule{Latency: time.Minute}, "latency"},
		{"anthropic", Rule{Jitter: -time.Second}, "jitter"},
	}
	for _, tt := range tests {
		if _, err := store.Set(tt.provider, tt.rule); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Set(%s, %+v) err = %v, want %q", tt.provider, tt.rule, err, tt.want)
		}
	}
	if err := store.Clear("unknown"); !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("Clear err = %v", err)
	}
}

func TestPublicDisabled(t *testing.T) {
	cfg := chaosConfig
	cfg.Public.Enabled = false
	store := NewStore([]string{"anthropic"}, cfg, time.Now, nil)
	if _, err := store.PressDown("anthropic"); !errors.Is(err, ErrPublicDisabled) {
		t.Fatalf("err = %v", err)
	}
}
