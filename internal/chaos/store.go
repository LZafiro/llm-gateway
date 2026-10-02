package chaos

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/LZafiro/llm-gateway/internal/config"
)

type Source string

const (
	SourceAdmin  Source = "admin"
	SourcePublic Source = "public"
)

const maxJitter = 5 * time.Second

var (
	ErrUnknownProvider = errors.New("unknown provider")
	ErrAdminRuleActive = errors.New("an admin chaos rule is active for this provider")
	ErrPublicDisabled  = errors.New("public chaos is disabled")
)

type CooldownError struct {
	Remaining time.Duration
}

func (e *CooldownError) Error() string {
	return fmt.Sprintf("provider was taken down recently, retry in %s", e.Remaining.Round(time.Second))
}

type Rule struct {
	Down      bool
	ErrorRate float64
	Latency   time.Duration
	Jitter    time.Duration
	ExpiresAt time.Time
	Source    Source
}

func (r Rule) activeAt(now time.Time) bool {
	return r.ExpiresAt.IsZero() || now.Before(r.ExpiresAt)
}

type ChangeFunc func(provider string, rule Rule, active bool)

type Store struct {
	cfg       config.Chaos
	now       func() time.Time
	onChange  ChangeFunc
	providers map[string]bool

	mu      sync.RWMutex
	rules   map[string]Rule
	pressed map[string]time.Time
}

func NewStore(providers []string, cfg config.Chaos, now func() time.Time, onChange ChangeFunc) *Store {
	if onChange == nil {
		onChange = func(string, Rule, bool) {}
	}
	known := make(map[string]bool, len(providers))
	for _, p := range providers {
		known[p] = true
	}
	return &Store{cfg: cfg, now: now, onChange: onChange, providers: known, rules: map[string]Rule{}, pressed: map[string]time.Time{}}
}

func (s *Store) Get(provider string) (Rule, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rule, ok := s.rules[provider]
	if !ok || !rule.activeAt(s.now()) {
		return Rule{}, false
	}
	return rule, true
}

func (s *Store) Snapshot() map[string]Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := s.now()
	active := make(map[string]Rule, len(s.rules))
	for name, rule := range s.rules {
		if rule.activeAt(now) {
			active[name] = rule
		}
	}
	return active
}

func (s *Store) Set(provider string, rule Rule) (Rule, error) {
	if !s.providers[provider] {
		return Rule{}, ErrUnknownProvider
	}
	if err := s.validate(rule); err != nil {
		return Rule{}, err
	}
	rule.Source = SourceAdmin
	s.mu.Lock()
	s.rules[provider] = rule
	s.mu.Unlock()
	s.onChange(provider, rule, true)
	return rule, nil
}

func (s *Store) Clear(provider string) error {
	if !s.providers[provider] {
		return ErrUnknownProvider
	}
	s.mu.Lock()
	rule, existed := s.rules[provider]
	delete(s.rules, provider)
	s.mu.Unlock()
	if existed {
		s.onChange(provider, rule, false)
	}
	return nil
}

func (s *Store) PressDown(provider string) (Rule, error) {
	if !s.cfg.Public.Enabled {
		return Rule{}, ErrPublicDisabled
	}
	if !s.providers[provider] {
		return Rule{}, ErrUnknownProvider
	}
	s.mu.Lock()
	now := s.now()
	if current, ok := s.rules[provider]; ok && current.Source == SourceAdmin && current.activeAt(now) {
		s.mu.Unlock()
		return Rule{}, ErrAdminRuleActive
	}
	if last, ok := s.pressed[provider]; ok {
		if remaining := last.Add(s.cfg.Public.Cooldown).Sub(now); remaining > 0 {
			s.mu.Unlock()
			return Rule{}, &CooldownError{Remaining: remaining}
		}
	}
	rule := Rule{Down: true, ExpiresAt: now.Add(s.cfg.Public.DownDuration), Source: SourcePublic}
	s.rules[provider] = rule
	s.pressed[provider] = now
	s.mu.Unlock()
	s.onChange(provider, rule, true)
	return rule, nil
}

func (s *Store) Expire() {
	s.mu.Lock()
	now := s.now()
	expired := map[string]Rule{}
	for name, rule := range s.rules {
		if !rule.activeAt(now) {
			expired[name] = rule
			delete(s.rules, name)
		}
	}
	s.mu.Unlock()
	for _, name := range sortedKeys(expired) {
		s.onChange(name, expired[name], false)
	}
}

func (s *Store) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Expire()
		}
	}
}

func (s *Store) validate(rule Rule) error {
	var errs []error
	if rule.ErrorRate < 0 || rule.ErrorRate > 1 {
		errs = append(errs, errors.New("error_rate must be between 0 and 1"))
	}
	if rule.Latency < 0 || rule.Latency > s.cfg.MaxLatency {
		errs = append(errs, fmt.Errorf("latency must be between 0 and %s", s.cfg.MaxLatency))
	}
	if rule.Jitter < 0 || rule.Jitter > maxJitter {
		errs = append(errs, fmt.Errorf("jitter must be between 0 and %s", maxJitter))
	}
	return errors.Join(errs...)
}

func sortedKeys(m map[string]Rule) []string {
	return slices.Sorted(maps.Keys(m))
}
