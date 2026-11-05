package config

import (
	"strings"
	"testing"
	"time"
)

func TestConfig_DefaultValidation(t *testing.T) {
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("expected LoadConfig to succeed with defaults, got: %v", err)
	}

	if cfg.Environment != "development" {
		t.Errorf("expected default environment development, got: %s", cfg.Environment)
	}
}

func TestConfig_ProductionValidationRules(t *testing.T) {
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("failed to load default config: %v", err)
	}

	cfg.Environment = "production"
	// Should fail because JWT secret is default, DB password is default, SSLMode is disable
	err = cfg.Validate()
	if err == nil {
		t.Fatalf("expected validation error in production with default secrets, got nil")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "JWT_SECRET_KEY") {
		t.Errorf("expected error about JWT_SECRET_KEY, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "DB_PASSWORD") {
		t.Errorf("expected error about DB_PASSWORD, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "DB_SSL_MODE") {
		t.Errorf("expected error about DB_SSL_MODE, got: %s", errMsg)
	}

	// Now fix all production requirements
	cfg.Auth.JWTSecretKey = "a_very_long_secure_production_jwt_secret_key_minimum_32_bytes_long"
	cfg.DB.Password = "strong_prod_db_password_123!"
	cfg.DB.SSLMode = "require"

	if err := cfg.Validate(); err != nil {
		t.Errorf("expected validation to succeed with secure credentials, got: %v", err)
	}
}

func TestConfig_RedactionInStringer(t *testing.T) {
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("failed to load default config: %v", err)
	}

	cfg.Auth.JWTSecretKey = "super_secret_jwt_key_that_must_never_be_logged"
	cfg.Auth.APITokenSalt = "super_secret_salt_123"
	cfg.DB.Password = "super_secret_database_password_456"

	cfgStr := cfg.String()
	authStr := cfg.Auth.String()
	dbStr := cfg.DB.String()

	// Verify secrets do NOT appear in string representation
	if strings.Contains(cfgStr, "super_secret_jwt_key_that_must_never_be_logged") {
		t.Errorf("Config.String() leaked JWTSecretKey")
	}
	if strings.Contains(cfgStr, "super_secret_salt_123") {
		t.Errorf("Config.String() leaked APITokenSalt")
	}
	if strings.Contains(cfgStr, "super_secret_database_password_456") {
		t.Errorf("Config.String() leaked DB.Password")
	}

	if strings.Contains(authStr, "super_secret_jwt_key_that_must_never_be_logged") ||
		strings.Contains(authStr, "super_secret_salt_123") {
		t.Errorf("AuthConfig.String() leaked secrets")
	}
	if !strings.Contains(authStr, "[REDACTED]") {
		t.Errorf("AuthConfig.String() should contain [REDACTED]")
	}

	if strings.Contains(dbStr, "super_secret_database_password_456") {
		t.Errorf("DBConfig.String() leaked password")
	}
	if !strings.Contains(dbStr, "[REDACTED]") {
		t.Errorf("DBConfig.String() should contain [REDACTED]")
	}
}

func TestConfig_BoundaryValidation(t *testing.T) {
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("failed to load default config: %v", err)
	}

	cfg.HTTP.Port = 70000
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected error for invalid port 70000")
	}

	cfg.HTTP.Port = 8080
	cfg.Heartbeat.Interval = 10 * time.Second
	cfg.Heartbeat.Timeout = 5 * time.Second
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected error when Heartbeat.Interval >= Heartbeat.Timeout")
	}
}

