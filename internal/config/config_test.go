package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestLoadAppliesDefaultsWithoutFile(t *testing.T) {
	cfg, err := Load("", env(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", cfg.Server.Addr)
	}
	if cfg.Secrets.DatabaseURL != "postgres://x" {
		t.Errorf("DatabaseURL = %q", cfg.Secrets.DatabaseURL)
	}
}

func TestLoadOverridesFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := "server:\n  addr: \":9090\"\n  shutdown_timeout: 3s\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, env(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != ":9090" {
		t.Errorf("Addr = %q, want :9090", cfg.Server.Addr)
	}
	if cfg.Server.ShutdownTimeout != 3*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 3s", cfg.Server.ShutdownTimeout)
	}
	if cfg.Server.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want default info", cfg.Server.LogLevel)
	}
}

func TestLoadRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		env     map[string]string
		wantErr string
	}{
		{"missing database url", "", map[string]string{}, "DATABASE_URL"},
		{"empty addr", "server:\n  addr: \"\"\n", map[string]string{"DATABASE_URL": "x"}, "server.addr"},
		{"malformed yaml", "server: [", map[string]string{"DATABASE_URL": "x"}, "parse config"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path, env(tt.env))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
