package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LZafiro/llm-gateway/internal/gateway"
	"github.com/LZafiro/llm-gateway/internal/provider"
	"github.com/LZafiro/llm-gateway/internal/provider/providertest"
	"github.com/LZafiro/llm-gateway/internal/ratelimit"
)

const chatBody = `{"model":"fast","user":"visitor-1","messages":[{"role":"user","content":"hi"}]}`

func TestRequiresAPIKey(t *testing.T) {
	h := newHandler(t, healthy("primary"), healthy("fallback"))
	for _, header := range []string{"", "Bearer wrong", "Basic " + testKey} {
		for _, target := range []string{"/v1/chat/completions", "/v1/models"} {
			method := http.MethodPost
			if target == "/v1/models" {
				method = http.MethodGet
			}
			req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(chatBody))
			if header != "" {
				req.Header.Set("Authorization", header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized || decode[errorEnvelope](t, rec.Body.String()).Error.Code != "invalid_api_key" {
				t.Errorf("%s with %q: status = %d, body = %s", target, header, rec.Code, rec.Body)
			}
		}
	}
}

func TestRateLimitHeadersAndRejection(t *testing.T) {
	now := fixedNow()
	limiter := ratelimit.New(func() time.Time { return now })
	limited := tenant
	limited.Rate, limited.Burst = 1, 2
	recorder := &memoryRecorder{}
	svcHandler := newHandlerWith(t, healthy("primary"), healthy("fallback"), handlerOptions{limiter: limiter, recorder: recorder, key: &limited})

	first := post(t, svcHandler, chatBody)
	if first.Code != http.StatusOK || first.Header().Get("X-RateLimit-Limit") != "2" || first.Header().Get("X-RateLimit-Remaining") != "1" || first.Header().Get("X-RateLimit-Reset") != "1" {
		t.Fatalf("first: status = %d, headers = %v", first.Code, first.Header())
	}
	post(t, svcHandler, chatBody)
	third := post(t, svcHandler, chatBody)
	if third.Code != http.StatusTooManyRequests || third.Header().Get("Retry-After") != "1" || third.Header().Get("X-RateLimit-Remaining") != "0" {
		t.Fatalf("third: status = %d, headers = %v", third.Code, third.Header())
	}
	envelope := decode[errorEnvelope](t, third.Body.String())
	if envelope.Error.Code != "rate_limit_exceeded" || envelope.Error.Type != "rate_limit_error" {
		t.Errorf("error = %+v", envelope.Error)
	}
	if e := recorder.last(t); e.Status != http.StatusTooManyRequests || e.ErrorCode != "rate_limit_exceeded" || len(e.Attempts) != 0 {
		t.Errorf("ledger entry = %+v", e)
	}
}

func TestLedgerEntryForSuccessfulFailover(t *testing.T) {
	recorder := &memoryRecorder{}
	h := newHandlerWith(t, down("primary"), healthy("fallback"), handlerOptions{recorder: recorder})
	rec := post(t, h, chatBody)
	e := recorder.last(t)
	if e.RequestID != rec.Header().Get(HeaderRequestID) || e.TenantID != tenant.ID || e.Source != "api" || e.EndUser != "visitor-1" {
		t.Errorf("identity = %+v", e)
	}
	if e.Status != 200 || e.Provider != "fallback" || e.Model != "f-1" || e.RequestedModel != "fast" || e.Cache != "miss" || e.Stream {
		t.Errorf("routing = %+v", e)
	}
	if len(e.Attempts) != 3 || e.Attempts[0].Kind != string(provider.KindConnection) || e.Attempts[2].Provider != "fallback" {
		t.Errorf("attempts = %+v", e.Attempts)
	}
	if e.Usage != (provider.Usage{PromptTokens: 7, CompletionTokens: 3}) || e.CostUSD != (7*2.0+3*10.0)/1e6 {
		t.Errorf("usage = %+v, cost = %v", e.Usage, e.CostUSD)
	}
	if e.Overhead == nil || e.TTFB != nil {
		t.Errorf("overhead = %v, ttfb = %v", e.Overhead, e.TTFB)
	}
}

func TestLedgerEntryForFailures(t *testing.T) {
	tests := []struct {
		name    string
		primary *providertest.Fake
		body    string
		status  int
		code    string
	}{
		{"exhausted", down("primary"), `{"model":"p-1","messages":[{"role":"user","content":"hi"}]}`, 503, "all_providers_unavailable"},
		{"unknown model", healthy("primary"), `{"model":"nope","messages":[{"role":"user","content":"hi"}]}`, 404, "model_not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &memoryRecorder{}
			post(t, newHandlerWith(t, tt.primary, healthy("fallback"), handlerOptions{recorder: recorder}), tt.body)
			if e := recorder.last(t); e.Status != tt.status || e.ErrorCode != tt.code || e.CostUSD != 0 {
				t.Errorf("entry = %+v", e)
			}
		})
	}
}

func TestLedgerEntryForStreams(t *testing.T) {
	interrupted := &providertest.Fake{ProviderName: "primary", Chunks: []provider.Chunk{{Content: "par"}}, StreamErr: &provider.Error{Kind: provider.KindServer}}
	tests := []struct {
		name      string
		primary   *providertest.Fake
		status    int
		code      string
		estimated bool
	}{
		{"complete", healthy("primary"), 200, "", false},
		{"interrupted", interrupted, 502, "stream_interrupted", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &memoryRecorder{}
			h := newHandlerWith(t, tt.primary, healthy("fallback"), handlerOptions{recorder: recorder})
			post(t, h, `{"model":"fast","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
			e := recorder.last(t)
			if e.Status != tt.status || e.ErrorCode != tt.code || !e.Stream || e.UsageEstimated != tt.estimated || e.TTFB == nil || e.Overhead == nil {
				t.Errorf("entry = %+v", e)
			}
		})
	}
}

func TestAbandonedStreamIsRecordedAsClientClosed(t *testing.T) {
	recorder := &memoryRecorder{}
	r := newTestRouter(t, healthy("primary"), healthy("fallback"))
	svc := gateway.New(gateway.Options{Router: r, Recorder: recorder, Now: fixedNow})
	streaming, err := svc.Stream(t.Context(), gateway.Request{ID: "r1", Tenant: tenant, Model: "fast", Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = streaming.Rest.Close()
	if e := recorder.last(t); e.Status != gateway.StatusClientClosed || e.ErrorCode != "client_closed_request" {
		t.Errorf("entry = %+v", e)
	}
}
