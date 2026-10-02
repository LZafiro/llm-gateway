//go:build integration

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LZafiro/llm-gateway/internal/auth"
	"github.com/LZafiro/llm-gateway/internal/ledger"
	"github.com/LZafiro/llm-gateway/internal/provider"
)

func migratedStore(t *testing.T) *Store {
	t.Helper()
	pool := startPostgres(t)
	if err := Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	return New(pool)
}

func TestAPIKeyLifecycle(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)
	key, plaintext, err := st.CreateAPIKey(ctx, "demo", 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if key.Prefix != plaintext[:10] || !key.Active {
		t.Errorf("key = %+v", key)
	}
	if _, _, err := st.CreateAPIKey(ctx, "demo", 2, 10); err == nil {
		t.Error("duplicate name accepted")
	}

	set := auth.NewKeySet(st, nil)
	if err := set.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	found, err := set.Lookup(plaintext)
	if err != nil || found.ID != key.ID || found.Rate != 2 || found.Burst != 10 {
		t.Fatalf("Lookup = %+v, %v", found, err)
	}

	if err := st.RevokeAPIKey(ctx, "demo"); err != nil {
		t.Fatal(err)
	}
	if err := st.RevokeAPIKey(ctx, "demo"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second revoke err = %v", err)
	}
	if err := set.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := set.Lookup(plaintext); !errors.Is(err, auth.ErrInvalidKey) {
		t.Error("revoked key still accepted")
	}
	list, err := st.ListAPIKeys(ctx)
	if err != nil || len(list) != 1 || list[0].Active {
		t.Errorf("list = %+v, %v", list, err)
	}
}

func TestInsertLedgerAndRetention(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	ttfb, overhead := 120*time.Millisecond, 3*time.Millisecond
	similarity := 0.9731
	entries := []ledger.Entry{
		{
			RequestID: "new", CreatedAt: now, TenantID: 1, Source: ledger.SourceAPI, EndUser: "visitor",
			RequestedModel: "fast", Provider: "anthropic", Model: "claude-haiku-4-5", Stream: true, Cache: "semantic",
			Similarity: &similarity, Status: 200,
			Attempts: []ledger.Attempt{{Provider: "anthropic", Model: "claude-haiku-4-5", LatencyMS: 110, Injected: true, Kind: "connection"}},
			Usage:    provider.Usage{PromptTokens: 1234, CompletionTokens: 567},
			CostUSD:  0.004069, EmbeddingCostUSD: 0.00000002, SavedUSD: 0.00123456,
			Latency: 900 * time.Millisecond, TTFB: &ttfb, Overhead: &overhead,
		},
		{RequestID: "old", CreatedAt: now.Add(-40 * 24 * time.Hour), TenantID: 1, Source: ledger.SourceDemo, RequestedModel: "fast", Cache: "miss", Status: 503, ErrorCode: "all_providers_unavailable", Latency: time.Second},
	}
	if err := st.InsertLedger(ctx, entries); err != nil {
		t.Fatalf("InsertLedger: %v", err)
	}

	var cost, saved, embedding string
	var attempts []byte
	var ttfbMS, overheadMS *int32
	var errorCode *string
	err := st.pool.QueryRow(ctx, `SELECT cost_usd::text, saved_usd::text, embedding_cost_usd::text, attempts, ttfb_ms, overhead_ms, error_code FROM ledger WHERE request_id = 'new'`).
		Scan(&cost, &saved, &embedding, &attempts, &ttfbMS, &overheadMS, &errorCode)
	if err != nil {
		t.Fatal(err)
	}
	if cost != "0.00406900" || saved != "0.00123456" || embedding != "0.00000002" {
		t.Errorf("cost = %s, saved = %s, embedding = %s", cost, saved, embedding)
	}
	if *ttfbMS != 120 || *overheadMS != 3 || errorCode != nil || len(attempts) == 0 {
		t.Errorf("ttfb = %v, overhead = %v, error = %v, attempts = %s", *ttfbMS, *overheadMS, errorCode, attempts)
	}

	deleted, err := st.DeleteLedgerBefore(ctx, now.Add(-30*24*time.Hour))
	if err != nil || deleted != 1 {
		t.Fatalf("deleted = %d, err = %v", deleted, err)
	}

	if _, ok, err := st.LastRun(ctx, "daily"); err != nil || ok {
		t.Fatalf("LastRun before mark = %v, %v", ok, err)
	}
	if err := st.MarkRun(ctx, "daily", now); err != nil {
		t.Fatal(err)
	}
	if last, ok, err := st.LastRun(ctx, "daily"); err != nil || !ok || !last.Equal(now) {
		t.Fatalf("LastRun = %v, %v, %v", last, ok, err)
	}
}
