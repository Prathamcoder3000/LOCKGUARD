package tests

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"lockguard/backend/internal/api"
	"lockguard/backend/internal/config"
)

func TestServerRoutingAndClusterStatus(t *testing.T) {
	cfg := &config.Config{
		Environment: "test",
		HTTP: config.HTTPConfig{
			Port: 8080,
			Host: "127.0.0.1",
		},
		Raft: config.RaftConfig{
			NodeID:       "node-test",
			ClusterNodes: []string{"node-test:7000", "node-2:7000", "node-3:7000"},
		},
		Storage: config.StorageConfig{
			DataDir: t.TempDir(),
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.NewServer(cfg, logger, "lockguard-test", "0.1.0")
	handler := srv.Handler()

	// Test GET /api/v1/cluster/status
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cluster/status", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK for /api/v1/cluster/status, got %d", rr.Code)
	}

	var resp api.APIResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	dataMap, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data map, got %T", resp.Data)
	}

	if dataMap["design_principle"] != "CONSISTENCY FIRST — FAIL CLOSED" {
		t.Errorf("expected CONSISTENCY FIRST — FAIL CLOSED, got %v", dataMap["design_principle"])
	}
}

func TestStandardErrorFormatting(t *testing.T) {
	rr := httptest.NewRecorder()
	api.RespondError(rr, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid parameters provided", map[string]string{
		"field": "lease_ttl",
	})

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}

	var errResp api.ErrorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to parse error response: %v", err)
	}

	if errResp.Success {
		t.Errorf("expected success false on error")
	}

	if errResp.Error.Code != api.ErrCodeBadRequest {
		t.Errorf("expected code %s, got %s", api.ErrCodeBadRequest, errResp.Error.Code)
	}
}

