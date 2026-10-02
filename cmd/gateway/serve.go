package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math/rand/v2"
	"net/http"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LZafiro/llm-gateway/internal/api"
	"github.com/LZafiro/llm-gateway/internal/auth"
	"github.com/LZafiro/llm-gateway/internal/breaker"
	"github.com/LZafiro/llm-gateway/internal/chaos"
	"github.com/LZafiro/llm-gateway/internal/config"
	"github.com/LZafiro/llm-gateway/internal/gateway"
	"github.com/LZafiro/llm-gateway/internal/ledger"
	"github.com/LZafiro/llm-gateway/internal/maintenance"
	"github.com/LZafiro/llm-gateway/internal/provider"
	"github.com/LZafiro/llm-gateway/internal/provider/anthropic"
	"github.com/LZafiro/llm-gateway/internal/provider/mock"
	"github.com/LZafiro/llm-gateway/internal/provider/openai"
	"github.com/LZafiro/llm-gateway/internal/ratelimit"
	"github.com/LZafiro/llm-gateway/internal/router"
	"github.com/LZafiro/llm-gateway/internal/store"
)

const (
	bucketSweepInterval = 5 * time.Minute
	bucketIdleTTL       = 10 * time.Minute
)

func serve(ctx context.Context, cfg config.Config, logger *slog.Logger, pool *pgxpool.Pool) error {
	st := store.New(pool)

	keySet := auth.NewKeySet(st, logger)
	if err := keySet.Reload(ctx); err != nil {
		return err
	}
	go keySet.Run(ctx, cfg.Auth.RefreshInterval)

	limiter := ratelimit.New(time.Now)
	go limiter.Run(ctx, bucketSweepInterval, bucketIdleTTL)

	writer := ledger.NewWriter(st, cfg.Ledger, logger, ledger.Hooks{})
	go writer.Run(context.WithoutCancel(ctx))
	defer writer.Close()

	go maintenance.New(st, logger, time.Now, maintenance.Task{
		Name: "ledger_retention",
		Run: func(ctx context.Context, now time.Time) (int64, error) {
			return st.DeleteLedgerBefore(ctx, now.Add(-cfg.Ledger.Retention))
		},
	}).Run(ctx)

	routes, chaosStore, err := buildRouter(ctx, cfg, logger)
	if err != nil {
		return err
	}
	handler := api.NewRouter(api.Deps{
		Gateway: gateway.New(gateway.Options{
			Router:   routes,
			Limiter:  limiter,
			Pricing:  ledger.Pricing(cfg.Pricing),
			Recorder: writer,
		}),
		Keys:       keySet,
		Chaos:      chaosStore,
		AdminToken: cfg.Secrets.AdminToken,
		Logger:     logger,
		Readiness: map[string]api.ReadinessCheck{
			"postgres":   pool.Ping,
			"migrations": migrationsApplied(pool),
		},
	})
	return listen(ctx, cfg.Server, handler, logger)
}

func buildRouter(ctx context.Context, cfg config.Config, logger *slog.Logger) (*router.Router, *chaos.Store, error) {
	upstreams := buildProviders(cfg)
	names := slices.Sorted(maps.Keys(upstreams))
	logger.Info("providers enabled", "providers", names)
	chaosStore := chaos.NewStore(names, cfg.Chaos, time.Now, func(name string, rule chaos.Rule, active bool) {
		logger.Warn("chaos rule changed", "provider", name, "active", active, "source", string(rule.Source), "down", rule.Down, "error_rate", rule.ErrorRate, "latency", rule.Latency)
	})
	go chaosStore.Run(ctx, time.Second)
	providers := make(map[string]provider.Provider, len(upstreams))
	for name, p := range upstreams {
		providers[name] = chaos.Wrap(p, chaosStore, rand.Float64)
	}
	breakers := breaker.NewSet(names, cfg.Breaker, time.Now, func(name string, from, to breaker.State) {
		logger.Warn("breaker changed", "provider", name, "from", from.String(), "to", to.String())
	})
	routes, err := router.New(cfg.Routes, providers, router.Options{Resilience: cfg.Resilience, Breakers: breakers})
	if err != nil {
		return nil, nil, fmt.Errorf("build routes: %w", err)
	}
	return routes, chaosStore, nil
}

func buildProviders(cfg config.Config) map[string]provider.Provider {
	httpClient := provider.NewHTTPClient(cfg.Providers.ResponseHeaderTimeout)
	providers := map[string]provider.Provider{
		mock.Name: mock.New(cfg.Providers.Mock.Latency),
	}
	if key := cfg.Secrets.AnthropicAPIKey; key != "" {
		providers[anthropic.Name] = anthropic.New(cfg.Providers.Anthropic.BaseURL, key, httpClient)
	}
	if key := cfg.Secrets.OpenAIAPIKey; key != "" {
		providers[openai.Name] = openai.New(cfg.Providers.OpenAI.BaseURL, key, httpClient)
	}
	return providers
}

func listen(ctx context.Context, cfg config.Server, handler http.Handler, logger *slog.Logger) error {
	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadTimeout,
	}
	errs := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.Addr)
		errs <- server.ListenAndServe()
	}()
	select {
	case err := <-errs:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

func migrationsApplied(pool *pgxpool.Pool) api.ReadinessCheck {
	return func(ctx context.Context) error {
		pending, err := store.PendingMigrations(ctx, pool)
		if err != nil {
			return err
		}
		if pending {
			return errors.New("pending migrations")
		}
		return nil
	}
}
