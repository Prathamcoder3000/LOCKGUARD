package persistence

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestConfigLoadAndSanitization(t *testing.T) {
	os.Setenv("DATABASE_HOST", "db.example.com")
	os.Setenv("DATABASE_PORT", "5433")
	os.Setenv("DATABASE_NAME", "audit_test_db")
	os.Setenv("DATABASE_USER", "secops")
	os.Setenv("DATABASE_PASSWORD", "super_secret_password_123")
	os.Setenv("DATABASE_SSLMODE", "require")
	os.Setenv("DATABASE_MAX_OPEN_CONNS", "45")
	os.Setenv("DATABASE_MAX_IDLE_CONNS", "15")
	os.Setenv("DATABASE_CONN_MAX_LIFETIME", "45m")
	os.Setenv("DATABASE_CONN_TIMEOUT", "8s")

	defer func() {
		os.Unsetenv("DATABASE_HOST")
		os.Unsetenv("DATABASE_PORT")
		os.Unsetenv("DATABASE_NAME")
		os.Unsetenv("DATABASE_USER")
		os.Unsetenv("DATABASE_PASSWORD")
		os.Unsetenv("DATABASE_SSLMODE")
		os.Unsetenv("DATABASE_MAX_OPEN_CONNS")
		os.Unsetenv("DATABASE_MAX_IDLE_CONNS")
		os.Unsetenv("DATABASE_CONN_MAX_LIFETIME")
		os.Unsetenv("DATABASE_CONN_TIMEOUT")
	}()

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected LoadConfigFromEnv error: %v", err)
	}

	if cfg.Host != "db.example.com" {
		t.Errorf("expected host 'db.example.com', got '%s'", cfg.Host)
	}
	if cfg.Port != 5433 {
		t.Errorf("expected port 5433, got %d", cfg.Port)
	}
	if cfg.Database != "audit_test_db" {
		t.Errorf("expected db 'audit_test_db', got '%s'", cfg.Database)
	}
	if cfg.User != "secops" {
		t.Errorf("expected user 'secops', got '%s'", cfg.User)
	}
	if cfg.Password != "super_secret_password_123" {
		t.Errorf("expected actual password to match")
	}
	if cfg.MaxOpenConns != 45 {
		t.Errorf("expected max open 45, got %d", cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns != 15 {
		t.Errorf("expected max idle 15, got %d", cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime != 45*time.Minute {
		t.Errorf("expected conn lifetime 45m, got %v", cfg.ConnMaxLifetime)
	}
	if cfg.ConnTimeout != 8*time.Second {
		t.Errorf("expected timeout 8s, got %v", cfg.ConnTimeout)
	}

	// Verify password redaction in SanitizedDSN and String
	sanitized := cfg.SanitizedDSN()
	if strings.Contains(sanitized, "super_secret_password_123") {
		t.Fatalf("SanitizedDSN leaks password: %s", sanitized)
	}
	if !strings.Contains(sanitized, "****") {
		t.Fatalf("SanitizedDSN should mask password with ****: %s", sanitized)
	}

	str := cfg.String()
	if strings.Contains(str, "super_secret_password_123") {
		t.Fatalf("String() leaks password: %s", str)
	}

	// Verify unmasked DSN for database driver
	dsn := cfg.DSN()
	if !strings.Contains(dsn, "password=super_secret_password_123") {
		t.Fatalf("DSN() should contain actual password for driver connection: %s", dsn)
	}
}

func TestConfigFallbackLegacyEnvVars(t *testing.T) {
	os.Setenv("DB_HOST", "legacy.example.com")
	os.Setenv("DB_PORT", "5434")
	os.Setenv("DB_NAME", "legacy_db")
	os.Setenv("DB_USER", "legacy_user")
	os.Setenv("DB_PASSWORD", "legacy_secret")

	defer func() {
		os.Unsetenv("DB_HOST")
		os.Unsetenv("DB_PORT")
		os.Unsetenv("DB_NAME")
		os.Unsetenv("DB_USER")
		os.Unsetenv("DB_PASSWORD")
	}()

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error with legacy env vars: %v", err)
	}

	if cfg.Host != "legacy.example.com" {
		t.Errorf("expected host 'legacy.example.com', got '%s'", cfg.Host)
	}
	if cfg.Port != 5434 {
		t.Errorf("expected port 5434, got %d", cfg.Port)
	}
	if cfg.Database != "legacy_db" {
		t.Errorf("expected db 'legacy_db', got '%s'", cfg.Database)
	}
	if cfg.User != "legacy_user" {
		t.Errorf("expected user 'legacy_user', got '%s'", cfg.User)
	}
}

func TestDatabaseNilConfigError(t *testing.T) {
	_, err := Open(nil)
	if err == nil {
		t.Fatal("expected error when opening with nil config, got nil")
	}
}

func TestHealthCheckUninitialized(t *testing.T) {
	db := &Database{cfg: &Config{ConnTimeout: 100 * time.Millisecond}}
	status := db.HealthCheck(context.Background())
	if status.Available {
		t.Fatal("expected status.Available to be false for uninitialized db")
	}
	if status.Error == "" {
		t.Fatal("expected error message in uninitialized status")
	}
}

