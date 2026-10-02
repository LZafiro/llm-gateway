package api

import (
	"context"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LZafiro/llm-gateway/internal/breaker"
	"github.com/LZafiro/llm-gateway/internal/chaos"
	"github.com/LZafiro/llm-gateway/internal/config"
	"github.com/LZafiro/llm-gateway/internal/gateway"
	"github.com/LZafiro/llm-gateway/internal/provider"
	"github.com/LZafiro/llm-gateway/internal/provider/providertest"
	"github.com/LZafiro/llm-gateway/internal/router"
)

const adminToken = "admin-secret"

type stack struct {
	handler  http.Handler
	breakers *breaker.Set
	primary  *providertest.Fake
}

func newStack(t *testing.T) stack {
	t.Helper()
	cfg := config.Default()
	primary, fallback := healthy("primary"), healthy("fallback")
	names := []string{"primary", "fallback"}
	store := chaos.NewStore(names, cfg.Chaos, fixedNow, nil)
	breakers := breaker.NewSet(names, cfg.Breaker, fixedNow, nil)
	r, err := router.New(config.Routes{Aliases: map[string][]string{"fast": {"primary/p-1", "fallback/f-1"}}},
		map[string]provider.Provider{
			"primary":  chaos.Wrap(primary, store, rand.Float64),
			"fallback": chaos.Wrap(fallback, store, rand.Float64),
		},
		router.Options{Resilience: cfg.Resilience, Breakers: breakers, Now: fixedNow, Sleep: func(context.Context, time.Duration) error { return nil }},
	)
	if err != nil {
		t.Fatal(err)
	}
	return stack{
		handler:  NewRouter(Deps{Gateway: gateway.New(r), Chaos: store, AdminToken: adminToken, Now: fixedNow}),
		breakers: breakers,
		primary:  primary,
	}
}

func admin(t *testing.T, h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAdminChaosRequiresToken(t *testing.T) {
	s := newStack(t)
	for _, token := range []string{"", "wrong"} {
		rec := admin(t, s.handler, http.MethodPut, "/admin/chaos/primary", token, `{"down":true}`)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("token %q: status = %d", token, rec.Code)
		}
	}
	disabled := NewRouter(Deps{Chaos: chaos.NewStore([]string{"p"}, config.Default().Chaos, time.Now, nil)})
	if rec := admin(t, disabled, http.MethodPut, "/admin/chaos/p", "", `{"down":true}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("empty admin token accepted: %d", rec.Code)
	}
}

func TestAdminChaosPutAndDelete(t *testing.T) {
	s := newStack(t)
	rec := admin(t, s.handler, http.MethodPut, "/admin/chaos/primary", adminToken, `{"error_rate":0.3,"latency_ms":800,"jitter_ms":200,"ttl_seconds":300}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	got := decode[chaosRuleJSON](t, rec.Body.String())
	if got.ErrorRate != 0.3 || got.LatencyMS != 800 || got.JitterMS != 200 || got.Source != "admin" || got.ExpiresAt == nil || !got.ExpiresAt.Equal(fixedNow().Add(300*time.Second)) {
		t.Errorf("rule = %+v", got)
	}
	if rec := admin(t, s.handler, http.MethodDelete, "/admin/chaos/primary", adminToken, ""); rec.Code != http.StatusNoContent {
		t.Errorf("delete status = %d", rec.Code)
	}
}

func TestAdminChaosRejectsInvalidInput(t *testing.T) {
	s := newStack(t)
	tests := []struct {
		path   string
		body   string
		status int
		code   string
	}{
		{"/admin/chaos/unknown", `{"down":true}`, 404, "provider_not_found"},
		{"/admin/chaos/primary", `{"error_rate":2}`, 400, "invalid_value"},
		{"/admin/chaos/primary", `{"ttl_seconds":0}`, 400, "invalid_value"},
		{"/admin/chaos/primary", `nope`, 400, "invalid_json"},
	}
	for _, tt := range tests {
		rec := admin(t, s.handler, http.MethodPut, tt.path, adminToken, tt.body)
		if rec.Code != tt.status || decode[errorEnvelope](t, rec.Body.String()).Error.Code != tt.code {
			t.Errorf("%s %s: status = %d, body = %s", tt.path, tt.body, rec.Code, rec.Body)
		}
	}
}

func TestChaosDownFailsOverAndOpensBreaker(t *testing.T) {
	s := newStack(t)
	if rec := admin(t, s.handler, http.MethodPut, "/admin/chaos/primary", adminToken, `{"down":true}`); rec.Code != http.StatusOK {
		t.Fatalf("put chaos: %d", rec.Code)
	}
	var attempts []string
	for range 4 {
		rec := post(t, s.handler, `{"model":"fast","messages":[{"role":"user","content":"hi"}]}`)
		if rec.Code != http.StatusOK || rec.Header().Get(HeaderProvider) != "fallback" {
			t.Fatalf("status = %d, provider = %q", rec.Code, rec.Header().Get(HeaderProvider))
		}
		attempts = append(attempts, rec.Header().Get(HeaderAttempts))
	}
	if strings.Join(attempts, ",") != "3,3,2,1" {
		t.Errorf("attempts = %v, want 3,3,2,1", attempts)
	}
	if s.breakers.For("primary").State() != breaker.Open || s.primary.Calls() != 0 {
		t.Errorf("breaker = %s, upstream calls = %d", s.breakers.For("primary").State(), s.primary.Calls())
	}
}
