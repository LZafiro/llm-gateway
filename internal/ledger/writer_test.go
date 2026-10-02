package ledger

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/LZafiro/llm-gateway/internal/config"
	"github.com/LZafiro/llm-gateway/internal/provider"
)

type memorySink struct {
	mu       sync.Mutex
	batches  [][]Entry
	failures int
	calls    int
}

func (s *memorySink) InsertLedger(_ context.Context, entries []Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.failures > 0 {
		s.failures--
		return errors.New("postgres unavailable")
	}
	s.batches = append(s.batches, append([]Entry(nil), entries...))
	return nil
}

func (s *memorySink) total() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, b := range s.batches {
		n += len(b)
	}
	return n
}

type drops struct {
	mu     sync.Mutex
	counts map[DropReason]int
}

func (d *drops) record(reason DropReason, n int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.counts[reason] += n
}

func (d *drops) get(reason DropReason) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.counts[reason]
}

func newWriter(sink Sink, cfg config.Ledger) (*Writer, *drops) {
	d := &drops{counts: map[DropReason]int{}}
	w := NewWriter(sink, cfg, slog.New(slog.DiscardHandler), Hooks{Dropped: d.record})
	w.retryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	return w, d
}

func entry(i int) Entry {
	return Entry{RequestID: fmt.Sprintf("req-%d", i)}
}

func TestFlushesFullBatches(t *testing.T) {
	sink := &memorySink{}
	w, _ := newWriter(sink, config.Ledger{BufferSize: 100, BatchSize: 10, FlushInterval: time.Hour})
	go w.Run(context.Background())
	for i := range 25 {
		w.Record(entry(i))
	}
	w.Close()
	if len(sink.batches) != 3 || len(sink.batches[0]) != 10 || len(sink.batches[2]) != 5 {
		t.Fatalf("batches = %d", len(sink.batches))
	}
}

func TestFlushesOnInterval(t *testing.T) {
	sink := &memorySink{}
	w, _ := newWriter(sink, config.Ledger{BufferSize: 100, BatchSize: 100, FlushInterval: 10 * time.Millisecond})
	go w.Run(context.Background())
	defer w.Close()
	w.Record(entry(1))
	deadline := time.Now().Add(time.Second)
	for sink.total() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if sink.total() != 1 {
		t.Fatal("entry not flushed on interval")
	}
}

func TestDropsWhenBufferIsFull(t *testing.T) {
	w, d := newWriter(&memorySink{}, config.Ledger{BufferSize: 2, BatchSize: 10, FlushInterval: time.Hour})
	for i := range 5 {
		w.Record(entry(i))
	}
	if d.get(DropBufferFull) != 3 {
		t.Fatalf("dropped = %d, want 3", d.get(DropBufferFull))
	}
}

func TestRetriesThenDropsFailedBatch(t *testing.T) {
	sink := &memorySink{failures: 3}
	w, d := newWriter(sink, config.Ledger{BufferSize: 10, BatchSize: 2, FlushInterval: time.Hour})
	go w.Run(context.Background())
	w.Record(entry(1))
	w.Record(entry(2))
	w.Record(entry(3))
	w.Record(entry(4))
	w.Close()
	if sink.calls != 4 || d.get(DropFlushFailed) != 2 || sink.total() != 2 {
		t.Fatalf("calls = %d, dropped = %d, stored = %d", sink.calls, d.get(DropFlushFailed), sink.total())
	}
}

func TestCloseFlushesBufferedEntries(t *testing.T) {
	sink := &memorySink{}
	w, _ := newWriter(sink, config.Ledger{BufferSize: 100, BatchSize: 100, FlushInterval: time.Hour})
	for i := range 50 {
		w.Record(entry(i))
	}
	go w.Run(context.Background())
	w.Close()
	if sink.total() != 50 {
		t.Fatalf("persisted %d, want 50", sink.total())
	}
}

func TestCost(t *testing.T) {
	pricing := Pricing{"anthropic/claude-haiku-4-5": {InputPerMTok: 1, OutputPerMTok: 5}}
	got := pricing.Cost("anthropic/claude-haiku-4-5", provider.Usage{PromptTokens: 1234, CompletionTokens: 567})
	if want := 0.004069; fmt.Sprintf("%.8f", got) != fmt.Sprintf("%.8f", want) {
		t.Fatalf("cost = %.8f, want %.8f", got, want)
	}
	if pricing.Cost("unknown/model", provider.Usage{PromptTokens: 10}) != 0 {
		t.Fatal("unknown model has non-zero cost")
	}
}
