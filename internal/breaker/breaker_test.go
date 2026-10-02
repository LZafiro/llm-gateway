package breaker

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LZafiro/llm-gateway/internal/config"
)

var cfg = config.Breaker{WindowSize: 20, MinCalls: 5, FailureRatio: 0.5, OpenDuration: 10 * time.Second, HalfOpenProbes: 1}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type recorder struct {
	mu      sync.Mutex
	changes []string
}

func (r *recorder) record(_ string, from, to State) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.changes = append(r.changes, from.String()+">"+to.String())
}

func newBreaker() (*Breaker, *fakeClock, *recorder) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	rec := &recorder{}
	return New("p", cfg, clock.Now, rec.record), clock, rec
}

func call(t *testing.T, b *Breaker, o Outcome) {
	t.Helper()
	ticket, ok := b.Allow()
	if !ok {
		t.Fatal("call rejected")
	}
	b.Record(ticket, o)
}

func TestOpensAfterThreeFailuresOutOfFive(t *testing.T) {
	b, _, rec := newBreaker()
	for _, o := range []Outcome{Success, Failure, Success, Failure} {
		call(t, b, o)
	}
	if b.State() != Closed {
		t.Fatalf("opened before min calls")
	}
	call(t, b, Failure)
	if b.State() != Open || len(rec.changes) != 1 || rec.changes[0] != "closed>open" {
		t.Fatalf("state = %s, changes = %v", b.State(), rec.changes)
	}
	if _, ok := b.Allow(); ok {
		t.Fatal("open breaker allowed a call")
	}
}

func TestStaysClosedBelowRatio(t *testing.T) {
	b, _, _ := newBreaker()
	for range 10 {
		call(t, b, Success)
	}
	for range 9 {
		call(t, b, Failure)
	}
	if b.State() != Closed {
		t.Fatalf("state = %s, want closed with 9/19 failures", b.State())
	}
}

func TestWindowSlides(t *testing.T) {
	b, _, _ := newBreaker()
	for range 9 {
		call(t, b, Failure)
		call(t, b, Success)
		call(t, b, Success)
	}
	if b.State() != Closed {
		t.Fatalf("state = %s, want closed with 1/3 failure ratio", b.State())
	}
}

func TestIgnoredOutcomesDoNotCount(t *testing.T) {
	b, _, _ := newBreaker()
	for range 10 {
		call(t, b, Ignored)
	}
	for range 4 {
		call(t, b, Failure)
	}
	if b.State() != Closed {
		t.Fatal("ignored outcomes counted toward min calls")
	}
}

func TestOpenDurationIsExact(t *testing.T) {
	b, clock, _ := newBreaker()
	for range 5 {
		call(t, b, Failure)
	}
	clock.Advance(10*time.Second - time.Nanosecond)
	if _, ok := b.Allow(); ok {
		t.Fatal("allowed before open duration elapsed")
	}
	clock.Advance(time.Nanosecond)
	if b.State() != HalfOpen {
		t.Fatalf("state = %s, want half_open", b.State())
	}
}

func TestHalfOpenLetsExactlyOneProbeThrough(t *testing.T) {
	b, clock, _ := newBreaker()
	for range 5 {
		call(t, b, Failure)
	}
	clock.Advance(10 * time.Second)
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			if _, ok := b.Allow(); ok {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()
	if allowed.Load() != 1 {
		t.Fatalf("allowed %d probes, want 1", allowed.Load())
	}
}

func TestProbeOutcomes(t *testing.T) {
	tests := []struct {
		outcome Outcome
		want    State
	}{
		{Success, Closed},
		{Failure, Open},
		{Ignored, HalfOpen},
	}
	for _, tt := range tests {
		t.Run(tt.want.String(), func(t *testing.T) {
			b, clock, rec := newBreaker()
			for range 5 {
				call(t, b, Failure)
			}
			clock.Advance(10 * time.Second)
			call(t, b, tt.outcome)
			if b.State() != tt.want {
				t.Fatalf("state = %s, want %s (changes %v)", b.State(), tt.want, rec.changes)
			}
			if tt.outcome == Ignored {
				if _, ok := b.Allow(); !ok {
					t.Fatal("ignored probe did not release its slot")
				}
			}
		})
	}
}

func TestClosingResetsWindow(t *testing.T) {
	b, clock, _ := newBreaker()
	for range 5 {
		call(t, b, Failure)
	}
	clock.Advance(10 * time.Second)
	call(t, b, Success)
	for range 4 {
		call(t, b, Failure)
	}
	if b.State() != Closed {
		t.Fatal("old failures survived closing")
	}
}

func TestStaleTicketsAreIgnored(t *testing.T) {
	b, clock, _ := newBreaker()
	stale, _ := b.Allow()
	for range 5 {
		call(t, b, Failure)
	}
	clock.Advance(10 * time.Second)
	probe, _ := b.Allow()
	b.Record(stale, Success)
	if b.State() != HalfOpen {
		t.Fatalf("stale success changed state to %s", b.State())
	}
	b.Record(probe, Failure)
	if b.State() != Open {
		t.Fatalf("state = %s, want open", b.State())
	}
}

func TestCallbackMayReadState(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	var b *Breaker
	var seen State
	b = New("p", cfg, clock.Now, func(_ string, _, _ State) { seen = b.State() })
	for range 5 {
		call(t, b, Failure)
	}
	if seen != Open {
		t.Fatalf("callback saw %s", seen)
	}
}
