package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server     Server     `yaml:"server"`
	Providers  Providers  `yaml:"providers"`
	Routes     Routes     `yaml:"routes"`
	Resilience Resilience `yaml:"resilience"`
	Breaker    Breaker    `yaml:"breaker"`
	Chaos      Chaos      `yaml:"chaos"`
	Secrets    Secrets    `yaml:"-"`
}

type Resilience struct {
	MaxAttemptsPerProvider int           `yaml:"max_attempts_per_provider"`
	BackoffBase            time.Duration `yaml:"backoff_base"`
	BackoffCap             time.Duration `yaml:"backoff_cap"`
	AttemptTimeout         time.Duration `yaml:"attempt_timeout"`
	RequestDeadline        time.Duration `yaml:"request_deadline"`
}

type Breaker struct {
	WindowSize     int           `yaml:"window_size"`
	MinCalls       int           `yaml:"min_calls"`
	FailureRatio   float64       `yaml:"failure_ratio"`
	OpenDuration   time.Duration `yaml:"open_duration"`
	HalfOpenProbes int           `yaml:"half_open_probes"`
}

type Chaos struct {
	Public     PublicChaos   `yaml:"public"`
	MaxLatency time.Duration `yaml:"max_latency"`
}

type PublicChaos struct {
	Enabled      bool          `yaml:"enabled"`
	DownDuration time.Duration `yaml:"down_duration"`
	Cooldown     time.Duration `yaml:"cooldown"`
}

type Providers struct {
	Anthropic             Upstream      `yaml:"anthropic"`
	OpenAI                Upstream      `yaml:"openai"`
	Mock                  Mock          `yaml:"mock"`
	ResponseHeaderTimeout time.Duration `yaml:"response_header_timeout"`
}

type Upstream struct {
	BaseURL string `yaml:"base_url"`
}

type Mock struct {
	Latency time.Duration `yaml:"latency"`
}

type Routes struct {
	Aliases map[string][]string `yaml:"aliases"`
	Direct  []string            `yaml:"direct"`
}

type Server struct {
	Addr            string        `yaml:"addr"`
	LogLevel        string        `yaml:"log_level"`
	ReadTimeout     time.Duration `yaml:"read_timeout"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}

type Secrets struct {
	DatabaseURL     string
	RedisURL        string
	OpenAIAPIKey    string
	AnthropicAPIKey string
	AdminToken      string
	DemoAPIKey      string
}

func Default() Config {
	return Config{
		Server: Server{
			Addr:            ":8080",
			LogLevel:        "info",
			ReadTimeout:     10 * time.Second,
			ShutdownTimeout: 10 * time.Second,
		},
		Providers: Providers{
			Anthropic:             Upstream{BaseURL: "https://api.anthropic.com"},
			OpenAI:                Upstream{BaseURL: "https://api.openai.com"},
			ResponseHeaderTimeout: 15 * time.Second,
		},
		Resilience: Resilience{
			MaxAttemptsPerProvider: 2,
			BackoffBase:            100 * time.Millisecond,
			BackoffCap:             time.Second,
			AttemptTimeout:         15 * time.Second,
			RequestDeadline:        30 * time.Second,
		},
		Breaker: Breaker{
			WindowSize:     20,
			MinCalls:       5,
			FailureRatio:   0.5,
			OpenDuration:   10 * time.Second,
			HalfOpenProbes: 1,
		},
		Chaos: Chaos{
			Public:     PublicChaos{Enabled: true, DownDuration: 30 * time.Second, Cooldown: 60 * time.Second},
			MaxLatency: 20 * time.Second,
		},
	}
}

func Load(path string, getenv func(string) string) (Config, error) {
	cfg := Default()
	if path != "" {
		raw, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config: %w", err)
		}
	}
	overrideString(&cfg.Providers.Anthropic.BaseURL, getenv("ANTHROPIC_BASE_URL"))
	overrideString(&cfg.Providers.OpenAI.BaseURL, getenv("OPENAI_BASE_URL"))
	cfg.Secrets = Secrets{
		DatabaseURL:     getenv("DATABASE_URL"),
		RedisURL:        getenv("REDIS_URL"),
		OpenAIAPIKey:    getenv("OPENAI_API_KEY"),
		AnthropicAPIKey: getenv("ANTHROPIC_API_KEY"),
		AdminToken:      getenv("ADMIN_TOKEN"),
		DemoAPIKey:      getenv("DEMO_API_KEY"),
	}
	return cfg, cfg.validate()
}

func (c Config) validate() error {
	var errs []error
	if c.Server.Addr == "" {
		errs = append(errs, errors.New("server.addr is required"))
	}
	if c.Server.ShutdownTimeout <= 0 {
		errs = append(errs, errors.New("server.shutdown_timeout must be positive"))
	}
	if c.Secrets.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	errs = append(errs, c.Routes.validate()...)
	errs = append(errs, c.Resilience.validate()...)
	errs = append(errs, c.Breaker.validate()...)
	return errors.Join(errs...)
}

func (r Routes) validate() []error {
	var errs []error
	if len(r.Aliases) == 0 && len(r.Direct) == 0 {
		errs = append(errs, errors.New("routes: at least one alias or direct model is required"))
	}
	for alias, chain := range r.Aliases {
		if strings.Contains(alias, "/") {
			errs = append(errs, fmt.Errorf("routes.aliases.%s: alias must not contain '/'", alias))
		}
		if len(chain) == 0 {
			errs = append(errs, fmt.Errorf("routes.aliases.%s: chain is empty", alias))
		}
		for _, target := range chain {
			if !isQualified(target) {
				errs = append(errs, fmt.Errorf("routes.aliases.%s: %q must be provider/model", alias, target))
			}
		}
	}
	for _, target := range r.Direct {
		if !isQualified(target) {
			errs = append(errs, fmt.Errorf("routes.direct: %q must be provider/model", target))
		}
	}
	return errs
}

func (r Resilience) validate() []error {
	var errs []error
	if r.MaxAttemptsPerProvider < 1 {
		errs = append(errs, errors.New("resilience.max_attempts_per_provider must be at least 1"))
	}
	if r.BackoffBase <= 0 || r.BackoffCap < r.BackoffBase {
		errs = append(errs, errors.New("resilience.backoff_base must be positive and not exceed backoff_cap"))
	}
	if r.AttemptTimeout <= 0 || r.RequestDeadline < r.AttemptTimeout {
		errs = append(errs, errors.New("resilience.attempt_timeout must be positive and not exceed request_deadline"))
	}
	return errs
}

func (b Breaker) validate() []error {
	var errs []error
	if b.WindowSize < 1 || b.MinCalls < 1 || b.MinCalls > b.WindowSize {
		errs = append(errs, errors.New("breaker.min_calls must be between 1 and window_size"))
	}
	if b.FailureRatio <= 0 || b.FailureRatio > 1 {
		errs = append(errs, errors.New("breaker.failure_ratio must be in (0, 1]"))
	}
	if b.OpenDuration <= 0 || b.HalfOpenProbes < 1 {
		errs = append(errs, errors.New("breaker.open_duration and half_open_probes must be positive"))
	}
	return errs
}

func isQualified(target string) bool {
	providerName, model, ok := strings.Cut(target, "/")
	return ok && providerName != "" && model != ""
}

func overrideString(dst *string, value string) {
	if value != "" {
		*dst = value
	}
}
