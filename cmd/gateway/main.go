package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LZafiro/llm-gateway/internal/api"
	"github.com/LZafiro/llm-gateway/internal/breaker"
	"github.com/LZafiro/llm-gateway/internal/chaos"
	"github.com/LZafiro/llm-gateway/internal/config"
	"github.com/LZafiro/llm-gateway/internal/gateway"
	"github.com/LZafiro/llm-gateway/internal/logging"
	"github.com/LZafiro/llm-gateway/internal/provider"
	"github.com/LZafiro/llm-gateway/internal/provider/anthropic"
	"github.com/LZafiro/llm-gateway/internal/provider/mock"
	"github.com/LZafiro/llm-gateway/internal/provider/openai"
	"github.com/LZafiro/llm-gateway/internal/router"
	"github.com/LZafiro/llm-gateway/internal/store"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	command := "serve"
	if len(args) > 0 {
		command = args[0]
	}
	if command == "healthcheck" {
		return healthcheck(ctx)
	}
	cfg, err := config.Load(os.Getenv("GATEWAY_CONFIG"), os.Getenv)
	if err != nil {
		return err
	}
	logger := logging.New(os.Stdout, cfg.Server.LogLevel).With("version", version)
	pool, err := pgxpool.New(ctx, cfg.Secrets.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	switch command {
	case "serve":
		return serve(ctx, cfg, logger, pool)
	case "migrate":
		if len(args) > 1 && args[1] != "up" {
			return fmt.Errorf("unknown migrate subcommand %q, want up", args[1])
		}
		if err := store.Migrate(ctx, pool); err != nil {
			return err
		}
		logger.Info("migrations applied")
		return nil
	default:
		return fmt.Errorf("unknown command %q, want serve, migrate or healthcheck", command)
	}
}

func serve(ctx context.Context, cfg config.Config, logger *slog.Logger, pool *pgxpool.Pool) error {
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
		return fmt.Errorf("build routes: %w", err)
	}
	handler := api.NewRouter(api.Deps{
		Gateway:    gateway.New(routes),
		Chaos:      chaosStore,
		AdminToken: cfg.Secrets.AdminToken,
		Logger:     logger,
		Readiness: map[string]api.ReadinessCheck{
			"postgres":   pool.Ping,
			"migrations": migrationsApplied(pool),
		},
	})
	server := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           handler,
		ReadHeaderTimeout: cfg.Server.ReadTimeout,
	}
	errs := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.Server.Addr)
		errs <- server.ListenAndServe()
	}()
	select {
	case err := <-errs:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.Server.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
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

const healthcheckURL = "http://127.0.0.1:8080/healthz"

func healthcheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthcheckURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("healthcheck: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck: status %d", resp.StatusCode)
	}
	return nil
}
