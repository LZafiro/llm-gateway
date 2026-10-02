package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const minimalRoutes = "routes:\n  direct:\n    - mock/mock-1\n"

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAppliesDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, minimalRoutes), env(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != ":8080" || cfg.Server.LogLevel != "info" {
		t.Errorf("server = %+v", cfg.Server)
	}
	if cfg.Providers.Anthropic.BaseURL != "https://api.anthropic.com" || cfg.Providers.ResponseHeaderTimeout != 15*time.Second {
		t.Errorf("providers = %+v", cfg.Providers)
	}
	if cfg.Secrets.DatabaseURL != "postgres://x" {
		t.Errorf("DatabaseURL = %q", cfg.Secrets.DatabaseURL)
	}
}

func TestLoadOverridesFromFileAndEnv(t *testing.T) {
	content := "server:\n  addr: \":9090\"\n  shutdown_timeout: 3s\n" + minimalRoutes
	cfg, err := Load(writeConfig(t, content), env(map[string]string{
		"DATABASE_URL":       "postgres://x",
		"ANTHROPIC_BASE_URL": "http://mockprovider:9090",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != ":9090" || cfg.Server.ShutdownTimeout != 3*time.Second {
		t.Errorf("server = %+v", cfg.Server)
	}
	if cfg.Providers.Anthropic.BaseURL != "http://mockprovider:9090" {
		t.Errorf("anthropic base url = %q", cfg.Providers.Anthropic.BaseURL)
	}
}

func TestLoadRejectsInvalidConfig(t *testing.T) {
	withDB := map[string]string{"DATABASE_URL": "x"}
	tests := []struct {
		name    string
		yaml    string
		env     map[string]string
		wantErr string
	}{
		{"missing database url", minimalRoutes, map[string]string{}, "DATABASE_URL"},
		{"empty addr", "server:\n  addr: \"\"\n" + minimalRoutes, withDB, "server.addr"},
		{"malformed yaml", "server: [", withDB, "parse config"},
		{"no routes", "", withDB, "at least one alias"},
		{"unqualified direct", "routes:\n  direct:\n    - gpt-4o-mini\n", withDB, "must be provider/model"},
		{"empty chain", "routes:\n  aliases:\n    fast: []\n", withDB, "chain is empty"},
		{"bad attempts", "resilience:\n  max_attempts_per_provider: 0\n" + minimalRoutes, withDB, "max_attempts_per_provider"},
		{"deadline below attempt", "resilience:\n  request_deadline: 1s\n" + minimalRoutes, withDB, "attempt_timeout"},
		{"min calls above window", "breaker:\n  min_calls: 50\n" + minimalRoutes, withDB, "min_calls"},
		{"alias with slash", "routes:\n  aliases:\n    a/b:\n      - mock/mock-1\n", withDB, "must not contain"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.yaml), env(tt.env))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
