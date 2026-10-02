package ledger

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/LZafiro/llm-gateway/internal/config"
)

type DropReason string

const (
	DropBufferFull  DropReason = "buffer_full"
	DropFlushFailed DropReason = "flush_failed"
)

type Sink interface {
	InsertLedger(ctx context.Context, entries []Entry) error
}

type Hooks struct {
	Dropped func(reason DropReason, n int)
	Flushed func(n int, took time.Duration)
}

type Writer struct {
	sink         Sink
	cfg          config.Ledger
	logger       *slog.Logger
	hooks        Hooks
	retryDelays  []time.Duration
	finalTimeout time.Duration

	entries chan Entry
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
}

func NewWriter(sink Sink, cfg config.Ledger, logger *slog.Logger, hooks Hooks) *Writer {
	if hooks.Dropped == nil {
		hooks.Dropped = func(DropReason, int) {}
	}
	if hooks.Flushed == nil {
		hooks.Flushed = func(int, time.Duration) {}
	}
	return &Writer{
		sink:         sink,
		cfg:          cfg,
		logger:       logger,
		hooks:        hooks,
		retryDelays:  []time.Duration{200 * time.Millisecond, time.Second},
		finalTimeout: 5 * time.Second,
		entries:      make(chan Entry, cfg.BufferSize),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}
}

func (w *Writer) Record(e Entry) {
	select {
	case w.entries <- e:
	default:
		w.hooks.Dropped(DropBufferFull, 1)
	}
}

func (w *Writer) Run(ctx context.Context) {
	defer close(w.done)
	ticker := time.NewTicker(w.cfg.FlushInterval)
	defer ticker.Stop()
	batch := make([]Entry, 0, w.cfg.BatchSize)
	for {
		select {
		case e := <-w.entries:
			batch = append(batch, e)
			if len(batch) >= w.cfg.BatchSize {
				batch = w.flush(ctx, batch)
			}
		case <-ticker.C:
			batch = w.flush(ctx, batch)
		case <-w.stop:
			w.drain(ctx, batch)
			return
		}
	}
}

func (w *Writer) Close() {
	w.once.Do(func() { close(w.stop) })
	<-w.done
}

func (w *Writer) drain(parent context.Context, batch []Entry) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), w.finalTimeout)
	defer cancel()
	for {
		select {
		case e := <-w.entries:
			batch = append(batch, e)
			if len(batch) >= w.cfg.BatchSize {
				batch = w.flush(ctx, batch)
			}
		default:
			w.flush(ctx, batch)
			return
		}
	}
}

func (w *Writer) flush(ctx context.Context, batch []Entry) []Entry {
	if len(batch) == 0 {
		return batch
	}
	start := time.Now()
	err := w.sink.InsertLedger(ctx, batch)
	for _, delay := range w.retryDelays {
		if err == nil || ctx.Err() != nil {
			break
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
		}
		err = w.sink.InsertLedger(ctx, batch)
	}
	if err != nil {
		w.logger.Warn("ledger flush failed, dropping batch", "entries", len(batch), "error", err)
		w.hooks.Dropped(DropFlushFailed, len(batch))
	} else {
		w.hooks.Flushed(len(batch), time.Since(start))
	}
	return batch[:0]
}
