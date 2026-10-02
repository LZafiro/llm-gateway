package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/LZafiro/llm-gateway/internal/ledger"
	"github.com/LZafiro/llm-gateway/internal/store/db"
)

const deleteBatchSize = 5000

func (s *Store) InsertLedger(ctx context.Context, entries []ledger.Entry) error {
	rows := make([]db.InsertLedgerEntriesParams, 0, len(entries))
	for _, e := range entries {
		attempts, err := json.Marshal(e.Attempts)
		if err != nil {
			return fmt.Errorf("encode attempts: %w", err)
		}
		if e.Attempts == nil {
			attempts = []byte("[]")
		}
		rows = append(rows, db.InsertLedgerEntriesParams{
			RequestID:        e.RequestID,
			CreatedAt:        pgtype.Timestamptz{Time: e.CreatedAt, Valid: true},
			TenantID:         e.TenantID,
			Source:           string(e.Source),
			EndUser:          optionalText(e.EndUser),
			RequestedModel:   e.RequestedModel,
			Provider:         optionalText(e.Provider),
			Model:            optionalText(e.Model),
			Stream:           e.Stream,
			Cache:            e.Cache,
			Similarity:       optionalFloat(e.Similarity),
			Status:           clampInt16(e.Status),
			ErrorCode:        optionalText(e.ErrorCode),
			Attempts:         attempts,
			PromptTokens:     clampInt32(int64(e.Usage.PromptTokens)),
			CompletionTokens: clampInt32(int64(e.Usage.CompletionTokens)),
			UsageEstimated:   e.UsageEstimated,
			CostUsd:          e.CostUSD,
			EmbeddingCostUsd: e.EmbeddingCostUSD,
			SavedUsd:         e.SavedUSD,
			LatencyMs:        clampInt32(e.Latency.Milliseconds()),
			TtfbMs:           optionalMillis(e.TTFB),
			OverheadMs:       optionalMillis(e.Overhead),
		})
	}
	if _, err := s.queries.InsertLedgerEntries(ctx, rows); err != nil {
		return fmt.Errorf("insert ledger entries: %w", err)
	}
	return nil
}

func (s *Store) DeleteLedgerBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	var total int64
	for {
		n, err := s.queries.DeleteLedgerBefore(ctx, db.DeleteLedgerBeforeParams{
			Cutoff:    pgtype.Timestamptz{Time: cutoff, Valid: true},
			BatchSize: deleteBatchSize,
		})
		total += n
		if err != nil {
			return total, fmt.Errorf("delete ledger entries: %w", err)
		}
		if n < deleteBatchSize {
			return total, nil
		}
	}
}

func (s *Store) MarkRun(ctx context.Context, job string, at time.Time) error {
	return s.queries.UpsertMaintenanceRun(ctx, db.UpsertMaintenanceRunParams{Job: job, LastRun: pgtype.Timestamptz{Time: at, Valid: true}})
}

func optionalText(v string) pgtype.Text {
	return pgtype.Text{String: v, Valid: v != ""}
}

func optionalFloat(v *float64) pgtype.Float4 {
	if v == nil {
		return pgtype.Float4{}
	}
	return pgtype.Float4{Float32: float32(*v), Valid: true}
}

func optionalMillis(d *time.Duration) pgtype.Int4 {
	if d == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: clampInt32(d.Milliseconds()), Valid: true}
}

func clampInt32(v int64) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < math.MinInt32 {
		return math.MinInt32
	}
	return int32(v)
}

func clampInt16(v int) int16 {
	if v > math.MaxInt16 {
		return math.MaxInt16
	}
	if v < math.MinInt16 {
		return math.MinInt16
	}
	return int16(v)
}
