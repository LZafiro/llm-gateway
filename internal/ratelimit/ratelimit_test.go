package ratelimit

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestBurstThenRefill(t *testing.T) {
	c := &clock{now: time.Unix(0, 0)}
	l := New(c.Now)
	for i := range 3 {
		d := l.Allow(1, 1, 3)
		if !d.Allowed || d.Remaining != 2-i || d.Limit != 3 {
			t.Fatalf("request %d: %+v", i, d)
		}
	}
	d := l.Allow(1, 1, 3)
	if d.Allowed || d.RetryAfter != time.Second || d.Remaining != 0 || d.Reset != 3*time.Second {
		t.Fatalf("4th request: %+v", d)
	}
	c.Advance(time.Second)
	if d := l.Allow(1, 1, 3); !d.Allowed {
		t.Fatalf("after refill: %+v", d)
	}
}

func TestKeysAreIndependent(t *testing.T) {
	l := New(time.Now)
	l.Allow(1, 1, 1)
	if d := l.Allow(2, 1, 1); !d.Allowed {
		t.Fatal("key 2 limited by key 1")
	}
}

func TestChangedLimitsResetBucket(t *testing.T) {
	l := New(func() time.Time { return time.Unix(0, 0) })
	l.Allow(1, 1, 1)
	if d := l.Allow(1, 1, 5); !d.Allowed || d.Remaining != 4 {
		t.Fatalf("after limit change: %+v", d)
	}
}

func TestConcurrentRequestsNeverExceedBurst(t *testing.T) {
	l := New(func() time.Time { return time.Unix(0, 0) })
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for range 1000 {
		wg.Go(func() {
			if l.Allow(42, 1, 10).Allowed {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()
	if allowed.Load() != 10 {
		t.Fatalf("allowed = %d, want 10", allowed.Load())
	}
}

func TestSweepEvictsIdleBuckets(t *testing.T) {
	c := &clock{now: time.Unix(0, 0)}
	l := New(c.Now)
	l.Allow(1, 1, 1)
	c.Advance(5 * time.Minute)
	l.Allow(2, 1, 1)
	c.Advance(6 * time.Minute)
	if n := l.Sweep(10 * time.Minute); n != 1 || l.Len() != 1 {
		t.Fatalf("evicted = %d, len = %d", n, l.Len())
	}
	if d := l.Allow(1, 1, 1); !d.Allowed {
		t.Fatal("evicted bucket did not start full")
	}
}
