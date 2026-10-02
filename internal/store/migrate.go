package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

func newProvider(pool *pgxpool.Pool) (*goose.Provider, *sql.DB, error) {
	files, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, nil, fmt.Errorf("open migrations: %w", err)
	}
	db := stdlib.OpenDBFromPool(pool)
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files)
	if err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("create migration provider: %w", err)
	}
	return provider, db, nil
}

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	provider, db, err := newProvider(pool)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

func PendingMigrations(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	provider, db, err := newProvider(pool)
	if err != nil {
		return false, err
	}
	defer func() { _ = db.Close() }()
	pending, err := provider.HasPending(ctx)
	if err != nil {
		return false, fmt.Errorf("check migrations: %w", err)
	}
	return pending, nil
}
