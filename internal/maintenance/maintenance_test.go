package maintenance

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestNextRun(t *testing.T) {
	tests := []struct {
		now  string
		want string
	}{
		{"2026-10-02T01:00:00Z", "2026-10-02T03:00:00Z"},
		{"2026-10-02T03:00:00Z", "2026-10-03T03:00:00Z"},
		{"2026-10-02T22:30:00Z", "2026-10-03T03:00:00Z"},
	}
	for _, tt := range tests {
		now, _ := time.Parse(time.RFC3339, tt.now)
		if got := NextRun(now).Format(time.RFC3339); got != tt.want {
			t.Errorf("NextRun(%s) = %s, want %s", tt.now, got, tt.want)
		}
	}
}

type memoryStore struct {
	last   time.Time
	marked []time.Time
}

func (m *memoryStore) LastRun(context.Context, string) (time.Time, bool, error) {
	return m.last, !m.last.IsZero(), nil
}

func (m *memoryStore) MarkRun(_ context.Context, _ string, at time.Time) error {
	m.marked = append(m.marked, at)
	return nil
}

func TestRunOnceExecutesTasksAndMarks(t *testing.T) {
	store := &memoryStore{}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	var cutoffs []time.Time
	task := Task{Name: "ledger", Run: func(_ context.Context, at time.Time) (int64, error) {
		cutoffs = append(cutoffs, at)
		return 3, nil
	}}
	s := New(store, slog.New(slog.DiscardHandler), func() time.Time { return now }, task)
	s.RunOnce(t.Context())
	if len(cutoffs) != 1 || !cutoffs[0].Equal(now) || len(store.marked) != 1 {
		t.Fatalf("cutoffs = %v, marked = %v", cutoffs, store.marked)
	}
}

func TestRunSkipsStartupRunWhenRecent(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	store := &memoryStore{last: now.Add(-time.Hour)}
	runs := 0
	s := New(store, slog.New(slog.DiscardHandler), func() time.Time { return now }, Task{Name: "t", Run: func(context.Context, time.Time) (int64, error) {
		runs++
		return 0, nil
	}})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	s.Run(ctx)
	if runs != 0 {
		t.Fatalf("ran %d times at startup despite recent run", runs)
	}
	store.last = now.Add(-25 * time.Hour)
	s.Run(ctx)
	if runs != 1 {
		t.Fatalf("ran %d times, want 1 after stale last run", runs)
	}
}
