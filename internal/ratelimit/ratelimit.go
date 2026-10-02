package ratelimit

import (
	"context"
	"math"
	"sync"
	"time"
)

const shardCount = 16

type Decision struct {
	Allowed    bool
	Limit      int
	Remaining  int
	Reset      time.Duration
	RetryAfter time.Duration
}

type bucket struct {
	tokens   float64
	rate     float64
	burst    int
	updated  time.Time
	lastSeen time.Time
}

type shard struct {
	mu      sync.Mutex
	buckets map[int64]*bucket
}

type Limiter struct {
	shards [shardCount]shard
	now    func() time.Time
}

func New(now func() time.Time) *Limiter {
	l := &Limiter{now: now}
	for i := range l.shards {
		l.shards[i].buckets = map[int64]*bucket{}
	}
	return l
}

func (l *Limiter) Allow(key int64, rate float64, burst int) Decision {
	s := &l.shards[shardIndex(key)]
	s.mu.Lock()
	defer s.mu.Unlock()
	now := l.now()
	b, ok := s.buckets[key]
	if !ok || b.rate != rate || b.burst != burst {
		b = &bucket{tokens: float64(burst), rate: rate, burst: burst, updated: now}
		s.buckets[key] = b
	}
	b.tokens = min(float64(burst), b.tokens+now.Sub(b.updated).Seconds()*rate)
	b.updated, b.lastSeen = now, now
	d := Decision{Limit: burst}
	if b.tokens >= 1 {
		b.tokens--
		d.Allowed = true
	} else {
		d.RetryAfter = ceilSeconds((1 - b.tokens) / rate)
	}
	d.Remaining = int(math.Floor(b.tokens))
	d.Reset = ceilSeconds((float64(burst) - b.tokens) / rate)
	return d
}

func (l *Limiter) Sweep(idle time.Duration) int {
	cutoff := l.now().Add(-idle)
	evicted := 0
	for i := range l.shards {
		s := &l.shards[i]
		s.mu.Lock()
		for key, b := range s.buckets {
			if b.lastSeen.Before(cutoff) {
				delete(s.buckets, key)
				evicted++
			}
		}
		s.mu.Unlock()
	}
	return evicted
}

func (l *Limiter) Len() int {
	n := 0
	for i := range l.shards {
		s := &l.shards[i]
		s.mu.Lock()
		n += len(s.buckets)
		s.mu.Unlock()
	}
	return n
}

func (l *Limiter) Run(ctx context.Context, interval, idle time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.Sweep(idle)
		}
	}
}

func shardIndex(key int64) int {
	index := key % shardCount
	if index < 0 {
		index = -index
	}
	return int(index)
}

func ceilSeconds(seconds float64) time.Duration {
	return time.Duration(math.Ceil(seconds)) * time.Second
}
