package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LZafiro/llm-gateway/internal/config"
	"github.com/LZafiro/llm-gateway/internal/logging"
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
	case "keys":
		return keys(ctx, store.New(pool), args[1:], os.Stdout)
	default:
		return fmt.Errorf("unknown command %q, want serve, migrate, keys or healthcheck", command)
	}
}
