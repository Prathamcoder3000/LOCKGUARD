package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lockguard/backend/internal/api"
	"lockguard/backend/internal/config"
)

func TestHealthEndpoint(t *testing.T) {
	handler := api.HealthHandler("lockguard-test", "0.1.0", "test-node")

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200 OK, got %d", rr.Code)
	}

	var resp api.APIResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Success {
		t.Errorf("expected success true, got false")
	}

	dataMap, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data map, got %T", resp.Data)
	}

	if dataMap["status"] != "ok" {
		t.Errorf("expected status 'ok', got '%v'", dataMap["status"])
	}

	if dataMap["service"] != "lockguard-test" {
		t.Errorf("expected service 'lockguard-test', got '%v'", dataMap["service"])
	}
}

func TestReadinessEndpoint(t *testing.T) {
	cfg := &config.Config{
		Storage: config.StorageConfig{
			DataDir: t.TempDir(),
		},
		Raft: config.RaftConfig{
			NodeID: "test-node",
		},
	}

	handler := api.ReadinessHandler(cfg, "lockguard-test", "test-node")

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200 OK, got %d", rr.Code)
	}

	var resp api.APIResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode readiness response: %v", err)
	}

	if !resp.Success {
		t.Errorf("expected success true")
	}
}

func TestMethodNotAllowedOnHealth(t *testing.T) {
	handler := api.HealthHandler("lockguard-test", "0.1.0", "test-node")

	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405 Method Not Allowed, got %d", rr.Code)
	}
}

