package breaker

import (
	"sync"
	"time"

	"github.com/LZafiro/llm-gateway/internal/config"
)

type State int

const (
	Closed State = iota
	HalfOpen
	Open
)

func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case HalfOpen:
		return "half_open"
	case Open:
		return "open"
	}
	return "unknown"
}

type Outcome int

const (
	Success Outcome = iota
	Failure
	Ignored
)

type Ticket struct {
	generation uint64
	probe      bool
}

type ChangeFunc func(name string, from, to State)

type Breaker struct {
	name     string
	cfg      config.Breaker
	now      func() time.Time
	onChange ChangeFunc

	mu         sync.Mutex
	state      State
	generation uint64
	window     []bool
	next       int
	calls      int
	failures   int
	openedAt   time.Time
	probes     int
	pending    []change
}

type change struct {
	from, to State
}

func New(name string, cfg config.Breaker, now func() time.Time, onChange ChangeFunc) *Breaker {
	if onChange == nil {
		onChange = func(string, State, State) {}
	}
	return &Breaker{name: name, cfg: cfg, now: now, onChange: onChange, window: make([]bool, cfg.WindowSize)}
}

func (b *Breaker) State() State {
	defer b.notify()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refresh()
	return b.state
}

func (b *Breaker) Allow() (Ticket, bool) {
	defer b.notify()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refresh()
	switch b.state {
	case Closed:
		return Ticket{generation: b.generation}, true
	case HalfOpen:
		if b.probes >= b.cfg.HalfOpenProbes {
			return Ticket{}, false
		}
		b.probes++
		return Ticket{generation: b.generation, probe: true}, true
	case Open:
	}
	return Ticket{}, false
}

func (b *Breaker) Record(t Ticket, o Outcome) {
	defer b.notify()
	b.mu.Lock()
	defer b.mu.Unlock()
	if t.generation != b.generation {
		return
	}
	if t.probe {
		b.probes--
		switch o {
		case Success:
			b.transition(Closed)
		case Failure:
			b.transition(Open)
		case Ignored:
		}
		return
	}
	if o == Ignored {
		return
	}
	b.observe(o == Failure)
	if b.calls >= b.cfg.MinCalls && float64(b.failures) >= b.cfg.FailureRatio*float64(b.calls) {
		b.transition(Open)
	}
}

func (b *Breaker) refresh() {
	if b.state == Open && b.now().Sub(b.openedAt) >= b.cfg.OpenDuration {
		b.transition(HalfOpen)
	}
}

func (b *Breaker) notify() {
	b.mu.Lock()
	pending := b.pending
	b.pending = nil
	b.mu.Unlock()
	for _, c := range pending {
		b.onChange(b.name, c.from, c.to)
	}
}

func (b *Breaker) observe(failed bool) {
	if b.calls == len(b.window) {
		if b.window[b.next] {
			b.failures--
		}
	} else {
		b.calls++
	}
	b.window[b.next] = failed
	if failed {
		b.failures++
	}
	b.next = (b.next + 1) % len(b.window)
}

func (b *Breaker) transition(to State) {
	from := b.state
	b.state = to
	b.generation++
	b.probes = 0
	switch to {
	case Open:
		b.openedAt = b.now()
	case Closed:
		clear(b.window)
		b.next, b.calls, b.failures = 0, 0, 0
	case HalfOpen:
	}
	if from != to {
		b.pending = append(b.pending, change{from: from, to: to})
	}
}

type Set struct {
	breakers map[string]*Breaker
}

func NewSet(names []string, cfg config.Breaker, now func() time.Time, onChange ChangeFunc) *Set {
	s := &Set{breakers: make(map[string]*Breaker, len(names))}
	for _, name := range names {
		s.breakers[name] = New(name, cfg, now, onChange)
	}
	return s
}

func (s *Set) For(name string) *Breaker {
	return s.breakers[name]
}

func (s *Set) States() map[string]State {
	states := make(map[string]State, len(s.breakers))
	for name, b := range s.breakers {
		states[name] = b.State()
	}
	return states
}
