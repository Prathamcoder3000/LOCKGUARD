package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"lockguard/backend/internal/api"
	"lockguard/backend/internal/config"
)

// mockProtectedResourceServer simulates the independent protected resource service
// enforcing fencing token monotonicity.
type mockProtectedResourceServer struct {
	mu     sync.Mutex
	states map[string]struct {
		HighestToken uint64
		OwnerID      string
	}
}

func newMockProtectedResourceServer() *mockProtectedResourceServer {
	return &mockProtectedResourceServer{
		states: make(map[string]struct {
			HighestToken uint64
			OwnerID      string
		}),
	}
}

func (m *mockProtectedResourceServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/health" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}

	if r.URL.Path != "/api/v1/write" || r.Method != http.MethodPost {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	var req struct {
		ResourceName string `json:"resource_name"`
		FencingToken uint64 `json:"fencing_token"`
		OwnerID      string `json:"owner_id"`
		Payload      string `json:"payload"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	current, exists := m.states[req.ResourceName]
	if !exists {
		m.states[req.ResourceName] = struct {
			HighestToken uint64
			OwnerID      string
		}{
			HighestToken: req.FencingToken,
			OwnerID:      req.OwnerID,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "token": req.FencingToken})
		return
	}

	// Stale token check
	if req.FencingToken < current.HighestToken {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error": map[string]interface{}{
				"code":    "ERR_FENCING_TOKEN_STALE",
				"message": fmt.Sprintf("stale fencing token: received %d, highest is %d", req.FencingToken, current.HighestToken),
			},
		})
		return
	}

	// Same token, different owner -> conflict
	if req.FencingToken == current.HighestToken && req.OwnerID != current.OwnerID {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error": map[string]interface{}{
				"code":    "ERR_OWNER_MISMATCH",
				"message": "token belongs to another owner",
			},
		})
		return
	}

	// Advance epoch
	m.states[req.ResourceName] = struct {
		HighestToken uint64
		OwnerID      string
	}{
		HighestToken: req.FencingToken,
		OwnerID:      req.OwnerID,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "token": req.FencingToken})
}

// TestPhase4_ProtectedResource_FencingSafety verifies end-to-end fencing safety:
// 1. Owner A acquires lock -> receives fencing token 1.
// 2. Owner A performs write on protected resource -> 200 OK.
// 3. Owner A's lease expires/released.
// 4. Owner B acquires lock -> receives fencing token 2.
// 5. Owner B performs write on protected resource -> 200 OK.
// 6. Stale Owner A sends delayed write with fencing token 1 -> Protected resource REJECTS with 409 Conflict.
// 7. Duplicate writes from Owner B with token 2 -> 200 OK.
func TestPhase4_ProtectedResource_FencingSafety(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	cfg := &config.Config{
		Environment: "test",
		HTTP:        config.HTTPConfig{Port: 8080, Host: "127.0.0.1"},
		Raft:        config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       50 * time.Millisecond,
			MaxRedirectAttempts:  2,
			RequestTimeout:       2 * time.Second,
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gwServer := api.NewServer(cfg, logger, "lockguard-gateway", "0.4.0")
	gwHandler := gwServer.Handler()

	// Start mock Protected Resource server
	protectedMock := newMockProtectedResourceServer()
	protectedServer := httptest.NewServer(protectedMock)
	defer protectedServer.Close()

	// 1. Owner A acquires lock
	acqBodyA, _ := json.Marshal(map[string]interface{}{
		"request_id":    "req-fencing-ownerA",
		"resource_name": "shared-storage",
		"owner_id":      "owner-A",
		"ttl_seconds":   10,
	})
	reqAcqA := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(acqBodyA))
	recAcqA := httptest.NewRecorder()
	gwHandler.ServeHTTP(recAcqA, reqAcqA)

	if recAcqA.Code != http.StatusOK {
		t.Fatalf("Owner A acquire failed: %s", recAcqA.Body.String())
	}
	var respA struct {
		Data struct {
			FencingToken uint64 `json:"fencing_token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(recAcqA.Body.Bytes(), &respA)
	tokenA := respA.Data.FencingToken
	if tokenA != 1 {
		t.Fatalf("expected token 1 for Owner A, got %d", tokenA)
	}

	// 2. Owner A performs write to Protected Resource
	writeBodyA, _ := json.Marshal(map[string]interface{}{
		"resource_name": "shared-storage",
		"owner_id":      "owner-A",
		"fencing_token": tokenA,
		"payload":       "data-written-by-owner-A",
	})
	respWriteA, err := http.Post(protectedServer.URL+"/api/v1/write", "application/json", bytes.NewReader(writeBodyA))
	if err != nil || respWriteA.StatusCode != http.StatusOK {
		t.Fatalf("Owner A protected write failed: %v, status: %d", err, respWriteA.StatusCode)
	}
	_ = respWriteA.Body.Close()

	// 3. Conclude Owner A's epoch (release lock)
	relBodyA, _ := json.Marshal(map[string]interface{}{
		"request_id":    "req-fencing-relA",
		"resource_name": "shared-storage",
		"owner_id":      "owner-A",
		"fencing_token": tokenA,
	})
	reqRelA := httptest.NewRequest(http.MethodPost, "/v1/locks/release", bytes.NewReader(relBodyA))
	recRelA := httptest.NewRecorder()
	gwHandler.ServeHTTP(recRelA, reqRelA)
	if recRelA.Code != http.StatusOK {
		t.Fatalf("Owner A release failed: %s", recRelA.Body.String())
	}

	// 4. Owner B acquires lock -> receives fencing token 2
	acqBodyB, _ := json.Marshal(map[string]interface{}{
		"request_id":    "req-fencing-ownerB",
		"resource_name": "shared-storage",
		"owner_id":      "owner-B",
		"ttl_seconds":   10,
	})
	reqAcqB := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(acqBodyB))
	recAcqB := httptest.NewRecorder()
	gwHandler.ServeHTTP(recAcqB, reqAcqB)

	if recAcqB.Code != http.StatusOK {
		t.Fatalf("Owner B acquire failed: %s", recAcqB.Body.String())
	}
	var respB struct {
		Data struct {
			FencingToken uint64 `json:"fencing_token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(recAcqB.Body.Bytes(), &respB)
	tokenB := respB.Data.FencingToken
	if tokenB <= tokenA {
		t.Fatalf("expected token B (%d) > token A (%d)", tokenB, tokenA)
	}

	// 5. Owner B performs write on Protected Resource -> 200 OK
	writeBodyB, _ := json.Marshal(map[string]interface{}{
		"resource_name": "shared-storage",
		"owner_id":      "owner-B",
		"fencing_token": tokenB,
		"payload":       "data-written-by-owner-B",
	})
	respWriteB, err := http.Post(protectedServer.URL+"/api/v1/write", "application/json", bytes.NewReader(writeBodyB))
	if err != nil || respWriteB.StatusCode != http.StatusOK {
		t.Fatalf("Owner B protected write failed: %v, status: %d", err, respWriteB.StatusCode)
	}
	_ = respWriteB.Body.Close()

	// 6. DELAYED REQUEST: Owner A (superseded leaseholder) sends delayed write with token 1
	// MUST BE REJECTED with 409 Conflict ERR_FENCING_TOKEN_STALE
	delayedWriteA, _ := json.Marshal(map[string]interface{}{
		"resource_name": "shared-storage",
		"owner_id":      "owner-A",
		"fencing_token": tokenA, // STALE TOKEN
		"payload":       "stale-data-trying-to-overwrite",
	})
	respDelayedA, err := http.Post(protectedServer.URL+"/api/v1/write", "application/json", bytes.NewReader(delayedWriteA))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer respDelayedA.Body.Close()

	if respDelayedA.StatusCode != http.StatusConflict {
		t.Fatalf("CRITICAL FENCING LEAK: protected resource accepted stale write! Code: %d", respDelayedA.StatusCode)
	}

	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(respDelayedA.Body).Decode(&errResp)
	if errResp.Error.Code != "ERR_FENCING_TOKEN_STALE" {
		t.Errorf("expected ERR_FENCING_TOKEN_STALE, got %s", errResp.Error.Code)
	}
	t.Log("Successfully verified that stale fencing token was rejected by protected resource!")

	// 7. Duplicate write from active Owner B with token 2 -> 200 OK
	respDupB, err := http.Post(protectedServer.URL+"/api/v1/write", "application/json", bytes.NewReader(writeBodyB))
	if err != nil || respDupB.StatusCode != http.StatusOK {
		t.Fatalf("Owner B repeated legitimate write failed: %v", err)
	}
	_ = respDupB.Body.Close()
}
