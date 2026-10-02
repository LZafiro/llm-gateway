package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server  Server  `yaml:"server"`
	Secrets Secrets `yaml:"-"`
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
	return errors.Join(errs...)
}
