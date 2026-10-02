package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/LZafiro/llm-gateway/internal/breaker"
	"github.com/LZafiro/llm-gateway/internal/ledger"
	"github.com/LZafiro/llm-gateway/internal/provider"
)

func TestRecordDerivesMetricsFromEntry(t *testing.T) {
	m := New()
	overhead := 2 * time.Millisecond
	m.Record(ledger.Entry{
		RequestedModel: "fast", Status: 200, Cache: "miss", Source: ledger.SourceAPI,
		Provider: "openai", Model: "gpt-4o-mini",
		Attempts: []ledger.Attempt{
			{Provider: "anthropic", Model: "claude-haiku-4-5", Kind: "connection", Injected: true, LatencyMS: 1},
			{Provider: "anthropic", Model: "claude-haiku-4-5", Kind: "connection", Injected: true, LatencyMS: 1},
			{Provider: "openai", Model: "gpt-4o-mini", LatencyMS: 300},
		},
		Usage:   provider.Usage{PromptTokens: 10, CompletionTokens: 20},
		CostUSD: 0.5, Latency: 310 * time.Millisecond, Overhead: &overhead,
	})
	checks := []struct {
		name string
		got  float64
		want float64
	}{
		{"requests", testutil.ToFloat64(m.requests.WithLabelValues("fast", "200", "miss", "api")), 1},
		{"failed attempts", testutil.ToFloat64(m.attempts.WithLabelValues("anthropic", "claude-haiku-4-5", "connection")), 2},
		{"successful attempts", testutil.ToFloat64(m.attempts.WithLabelValues("openai", "gpt-4o-mini", "success")), 1},
		{"failovers", testutil.ToFloat64(m.failovers.WithLabelValues("anthropic", "openai")), 1},
		{"chaos", testutil.ToFloat64(m.chaosInjections.WithLabelValues("anthropic", "connection")), 2},
		{"cost", testutil.ToFloat64(m.cost.WithLabelValues("openai", "gpt-4o-mini")), 0.5},
		{"completion tokens", testutil.ToFloat64(m.tokens.WithLabelValues("openai", "gpt-4o-mini", "completion")), 20},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestRateLimitedAndSavings(t *testing.T) {
	m := New()
	m.Record(ledger.Entry{RequestedModel: "fast", Status: 429, ErrorCode: "rate_limit_exceeded", TenantName: "demo", Cache: "miss"})
	m.Record(ledger.Entry{RequestedModel: "fast", Status: 200, Cache: "exact", SavedUSD: 0.25})
	if got := testutil.ToFloat64(m.rateLimited.WithLabelValues("demo")); got != 1 {
		t.Errorf("rate limited = %v", got)
	}
	if got := testutil.ToFloat64(m.saved.WithLabelValues("exact")); got != 0.25 {
		t.Errorf("saved = %v", got)
	}
}

func TestHandlerExposesGatewayMetrics(t *testing.T) {
	m := New()
	m.InitBreakers([]string{"anthropic"})
	m.BreakerChanged("anthropic", breaker.Closed, breaker.Open)
	m.Gauge("ratelimit_buckets", "buckets", func() float64 { return 3 })
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	for _, want := range []string{`gateway_breaker_state{provider="anthropic"} 2`, "gateway_ratelimit_buckets 3", "go_goroutines"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("metrics output missing %q", want)
		}
	}
}
