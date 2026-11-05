package tests

import (
	"os"
	"testing"

	"lockguard/backend/internal/config"
)

func TestDefaultConfig(t *testing.T) {
	// Clear any overrides for a clean baseline test
	os.Unsetenv("HTTP_PORT")
	os.Unsetenv("NODE_ID")
	os.Unsetenv("ENVIRONMENT")

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("expected valid default config, got error: %v", err)
	}

	if cfg.HTTP.Port != 8080 {
		t.Errorf("expected default HTTP port 8080, got %d", cfg.HTTP.Port)
	}

	if cfg.Raft.NodeID != "coordinator-a" {
		t.Errorf("expected default NodeID 'coordinator-a', got '%s'", cfg.Raft.NodeID)
	}

	if cfg.Environment != "development" {
		t.Errorf("expected default environment 'development', got '%s'", cfg.Environment)
	}

	if cfg.Lease.DefaultTTLSec != 10 {
		t.Errorf("expected default lease TTL 10s, got %d", cfg.Lease.DefaultTTLSec)
	}

	if len(cfg.Gateway.CoordinatorEndpoints) == 0 {
		t.Errorf("expected default coordinator endpoints to be non-empty")
	}

	if cfg.Heartbeat.Interval >= cfg.Heartbeat.Timeout {
		t.Errorf("expected heartbeat interval < timeout, got %v >= %v", cfg.Heartbeat.Interval, cfg.Heartbeat.Timeout)
	}

	if cfg.Renewal.SafetyMargin <= 0 {
		t.Errorf("expected positive renewal safety margin, got %v", cfg.Renewal.SafetyMargin)
	}
}

func TestConfigValidationFailure(t *testing.T) {
	// Test invalid port
	cfg := &config.Config{
		HTTP: config.HTTPConfig{
			Port: 70000, // Invalid port > 65535
		},
		Raft: config.RaftConfig{
			NodeID:       "node-1",
			ClusterNodes: []string{"node-1:7000"},
		},
		Lease: config.LeaseConfig{
			MinTTLSec:     1,
			MaxTTLSec:     30,
			DefaultTTLSec: 10,
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Errorf("expected validation error for port 70000, got nil")
	}

	// Test invalid lease constraints
	cfg.HTTP.Port = 8080
	cfg.Lease.MinTTLSec = 20
	cfg.Lease.MaxTTLSec = 10 // Max < Min

	if err := cfg.Validate(); err == nil {
		t.Errorf("expected validation error when MaxTTL < MinTTL, got nil")
	}
}

func TestEnvOverride(t *testing.T) {
	os.Setenv("HTTP_PORT", "9095")
	os.Setenv("NODE_ID", "coordinator-b")
	defer func() {
		os.Unsetenv("HTTP_PORT")
		os.Unsetenv("NODE_ID")
	}()

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error loading config with env: %v", err)
	}

	if cfg.HTTP.Port != 9095 {
		t.Errorf("expected HTTP port 9095, got %d", cfg.HTTP.Port)
	}

	if cfg.Raft.NodeID != "coordinator-b" {
		t.Errorf("expected NodeID 'coordinator-b', got '%s'", cfg.Raft.NodeID)
	}
}

