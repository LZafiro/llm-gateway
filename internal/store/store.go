package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LZafiro/llm-gateway/internal/auth"
	"github.com/LZafiro/llm-gateway/internal/store/db"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	pool    *pgxpool.Pool
	queries *db.Queries
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, queries: db.New(pool)}
}

func (s *Store) Queries() *db.Queries {
	return s.queries
}

type APIKey struct {
	ID        int64
	Name      string
	Prefix    string
	Rate      float64
	Burst     int
	Active    bool
	CreatedAt time.Time
}

func (s *Store) CreateAPIKey(ctx context.Context, name string, rate float64, burst int) (APIKey, string, error) {
	if name == "" || rate <= 0 || burst < 1 || burst > math.MaxInt32 {
		return APIKey{}, "", errors.New("create api key: name is required, rate must be positive and burst at least 1")
	}
	plaintext, prefix, hash := auth.Generate()
	row, err := s.queries.CreateAPIKey(ctx, db.CreateAPIKeyParams{
		Name:       name,
		KeyHash:    hash,
		KeyPrefix:  prefix,
		RatePerSec: rate,
		Burst:      int32(burst),
	})
	if err != nil {
		return APIKey{}, "", fmt.Errorf("create api key: %w", err)
	}
	return APIKey{
		ID: row.ID, Name: row.Name, Prefix: row.KeyPrefix, Rate: row.RatePerSec,
		Burst: int(row.Burst), Active: row.Active, CreatedAt: row.CreatedAt.Time,
	}, plaintext, nil
}

func (s *Store) ListAPIKeys(ctx context.Context) ([]APIKey, error) {
	rows, err := s.queries.ListAPIKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	keys := make([]APIKey, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, APIKey{
			ID: row.ID, Name: row.Name, Prefix: row.KeyPrefix, Rate: row.RatePerSec,
			Burst: int(row.Burst), Active: row.Active, CreatedAt: row.CreatedAt.Time,
		})
	}
	return keys, nil
}

func (s *Store) RevokeAPIKey(ctx context.Context, name string) error {
	n, err := s.queries.RevokeAPIKey(ctx, name)
	if err != nil {
		return fmt.Errorf("revoke api key: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("active key %q: %w", name, ErrNotFound)
	}
	return nil
}

func (s *Store) ActiveKeys(ctx context.Context) ([]auth.StoredKey, error) {
	rows, err := s.queries.ListActiveAPIKeys(ctx)
	if err != nil {
		return nil, err
	}
	keys := make([]auth.StoredKey, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, auth.StoredKey{
			Key:  auth.Key{ID: row.ID, Name: row.Name, Prefix: row.KeyPrefix, Rate: row.RatePerSec, Burst: int(row.Burst)},
			Hash: row.KeyHash,
		})
	}
	return keys, nil
}

func (s *Store) LastRun(ctx context.Context, job string) (time.Time, bool, error) {
	last, err := s.queries.GetMaintenanceRun(ctx, job)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return last.Time, true, nil
}
