package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/LZafiro/llm-gateway/internal/breaker"
	"github.com/LZafiro/llm-gateway/internal/ledger"
)

const namespace = "gateway"

var latencyBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

var overheadBuckets = []float64{0.0005, 0.001, 0.002, 0.005, 0.01, 0.025, 0.05, 0.1}

type Metrics struct {
	registry         *prometheus.Registry
	requests         *prometheus.CounterVec
	requestDuration  *prometheus.HistogramVec
	attempts         *prometheus.CounterVec
	providerLatency  *prometheus.HistogramVec
	failovers        *prometheus.CounterVec
	chaosInjections  *prometheus.CounterVec
	breakerState     *prometheus.GaugeVec
	cost             *prometheus.CounterVec
	saved            *prometheus.CounterVec
	tokens           *prometheus.CounterVec
	overhead         prometheus.Histogram
	rateLimited      *prometheus.CounterVec
	ledgerDropped    *prometheus.CounterVec
	ledgerFlush      prometheus.Histogram
	keyReloadFailure prometheus.Counter
}

func New() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "requests_total", Help: "Completion requests by outcome.",
		}, []string{"requested_model", "status", "cache", "source"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Name: "request_duration_seconds", Help: "End to end request latency.", Buckets: latencyBuckets,
		}, []string{"stream", "cache"}),
		attempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "provider_attempts_total", Help: "Upstream attempts by outcome.",
		}, []string{"provider", "model", "outcome"}),
		providerLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Name: "provider_latency_seconds", Help: "Upstream attempt latency, time to first byte for streams.", Buckets: latencyBuckets,
		}, []string{"provider", "model"}),
		failovers: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "failovers_total", Help: "Fallbacks from one provider to the next.",
		}, []string{"from", "to"}),
		chaosInjections: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "chaos_injections_total", Help: "Faults injected by chaos rules.",
		}, []string{"provider", "kind"}),
		breakerState: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace, Name: "breaker_state", Help: "Circuit breaker state: 0 closed, 1 half-open, 2 open.",
		}, []string{"provider"}),
		cost: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "cost_usd_total", Help: "Upstream cost in USD.",
		}, []string{"provider", "model"}),
		saved: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "saved_usd_total", Help: "Cost avoided by cache hits in USD.",
		}, []string{"layer"}),
		tokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "tokens_total", Help: "Tokens consumed upstream.",
		}, []string{"provider", "model", "direction"}),
		overhead: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace, Name: "overhead_seconds", Help: "Time spent inside the gateway, excluding upstream calls and backoff.", Buckets: overheadBuckets,
		}),
		rateLimited: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "ratelimit_rejections_total", Help: "Requests rejected by the token bucket.",
		}, []string{"key_name"}),
		ledgerDropped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "ledger_dropped_total", Help: "Ledger entries dropped.",
		}, []string{"reason"}),
		ledgerFlush: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace, Name: "ledger_flush_duration_seconds", Help: "Duration of ledger batch writes.", Buckets: latencyBuckets,
		}),
		keyReloadFailure: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Name: "api_key_reload_failures_total", Help: "Failed API key reloads.",
		}),
	}
	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.requests, m.requestDuration, m.attempts, m.providerLatency, m.failovers, m.chaosInjections,
		m.breakerState, m.cost, m.saved, m.tokens, m.overhead, m.rateLimited, m.ledgerDropped, m.ledgerFlush,
		m.keyReloadFailure,
	)
	return m
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{Registry: m.registry})
}

func (m *Metrics) Gauge(name, help string, value func() float64) {
	m.registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Namespace: namespace, Name: name, Help: help}, value))
}

func (m *Metrics) Record(e ledger.Entry) {
	m.requests.WithLabelValues(e.RequestedModel, strconv.Itoa(e.Status), e.Cache, string(e.Source)).Inc()
	m.requestDuration.WithLabelValues(strconv.FormatBool(e.Stream), e.Cache).Observe(e.Latency.Seconds())
	if e.ErrorCode == "rate_limit_exceeded" {
		m.rateLimited.WithLabelValues(e.TenantName).Inc()
	}
	for i, a := range e.Attempts {
		outcome := a.Kind
		if outcome == "" {
			outcome = "success"
		}
		m.attempts.WithLabelValues(a.Provider, a.Model, outcome).Inc()
		m.providerLatency.WithLabelValues(a.Provider, a.Model).Observe(float64(a.LatencyMS) / 1000)
		if a.Injected {
			m.chaosInjections.WithLabelValues(a.Provider, a.Kind).Inc()
		}
		if i > 0 && e.Attempts[i-1].Provider != a.Provider {
			m.failovers.WithLabelValues(e.Attempts[i-1].Provider, a.Provider).Inc()
		}
	}
	if e.Provider != "" && e.Cache == "miss" {
		m.cost.WithLabelValues(e.Provider, e.Model).Add(e.CostUSD)
		m.tokens.WithLabelValues(e.Provider, e.Model, "prompt").Add(float64(e.Usage.PromptTokens))
		m.tokens.WithLabelValues(e.Provider, e.Model, "completion").Add(float64(e.Usage.CompletionTokens))
	}
	if e.SavedUSD > 0 {
		m.saved.WithLabelValues(e.Cache).Add(e.SavedUSD)
	}
	if e.Overhead != nil {
		m.overhead.Observe(e.Overhead.Seconds())
	}
}

func (m *Metrics) BreakerChanged(provider string, _, to breaker.State) {
	m.breakerState.WithLabelValues(provider).Set(float64(to))
}

func (m *Metrics) InitBreakers(providers []string) {
	for _, p := range providers {
		m.breakerState.WithLabelValues(p).Set(float64(breaker.Closed))
	}
}

func (m *Metrics) LedgerHooks() ledger.Hooks {
	return ledger.Hooks{
		Dropped: func(reason ledger.DropReason, n int) { m.ledgerDropped.WithLabelValues(string(reason)).Add(float64(n)) },
		Flushed: func(_ int, took time.Duration) { m.ledgerFlush.Observe(took.Seconds()) },
	}
}

func (m *Metrics) KeyReloadFailed() {
	m.keyReloadFailure.Inc()
}
