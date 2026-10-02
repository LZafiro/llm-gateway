//go:build integration

package store

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func startPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	container, err := postgres.Run(ctx, "pgvector/pgvector:pg17",
		postgres.WithDatabase("gateway"),
		postgres.WithUsername("gateway"),
		postgres.WithPassword("gateway"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestMigrateAppliesAllMigrations(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)

	pending, err := PendingMigrations(ctx, pool)
	if err != nil || !pending {
		t.Fatalf("before migrate: pending = %v, err = %v; want true, nil", pending, err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	pending, err = PendingMigrations(ctx, pool)
	if err != nil || pending {
		t.Fatalf("after migrate: pending = %v, err = %v; want false, nil", pending, err)
	}

	var extension string
	if err := pool.QueryRow(ctx, "SELECT extname FROM pg_extension WHERE extname = 'vector'").Scan(&extension); err != nil {
		t.Fatalf("vector extension missing: %v", err)
	}
}
