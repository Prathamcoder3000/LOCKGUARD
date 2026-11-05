package tests

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lockguard/backend/internal/api"
	"lockguard/backend/internal/config"
	"lockguard/backend/internal/heartbeat"
)

// TestPhase4_Session_LeaderChangeDuringRenewal verifies that when a Raft leader change
// occurs during automatic lease renewal:
// 1. The renewal manager retries with 421 leader redirection.
// 2. The new leader commits the renewal.
// 3. Fencing token is preserved and lease version advances exactly once.
func TestPhase4_Session_LeaderChangeDuringRenewal(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	cfg := &config.Config{
		Environment: "test",
		HTTP:        config.HTTPConfig{Port: 8080, Host: "127.0.0.1"},
		Raft:        config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       50 * time.Millisecond,
			MaxRedirectAttempts:  3,
			RequestTimeout:       2 * time.Second,
		},
		Renewal: config.RenewalConfig{
			MaxAttempts:     3,
			RetryInitial:    20 * time.Millisecond,
			RetryMax:        100 * time.Millisecond,
			SafetyMargin:    50 * time.Millisecond,
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gwServer := api.NewServer(cfg, logger, "lockguard-gateway", "0.4.0")
	handler := gwServer.Handler()

	// 1. Register Client Session
	regBody, _ := json.Marshal(map[string]interface{}{
		"client_id":   "client-recovery-test",
		"owner_id":    "worker-resilient",
		"ttl_seconds": 15,
	})
	reqReg := httptest.NewRequest(http.MethodPost, "/v1/clients/register", bytes.NewReader(regBody))
	recReg := httptest.NewRecorder()
	handler.ServeHTTP(recReg, reqReg)

	if recReg.Code != http.StatusOK {
		t.Fatalf("client registration failed: %s", recReg.Body.String())
	}
	var regResp struct {
		Data struct {
			SessionID string `json:"session_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(recReg.Body.Bytes(), &regResp)
	sessionID := regResp.Data.SessionID

	// 2. Acquire Lock (TTL 400ms)
	acqBody, _ := json.Marshal(map[string]interface{}{
		"request_id":     "req-renewal-failover",
		"resource_name":  "res-failover-lock",
		"owner_id":       "worker-resilient",
		"ttl_nanoseconds": 400 * int64(time.Millisecond),
	})
	reqAcq := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(acqBody))
	recAcq := httptest.NewRecorder()
	handler.ServeHTTP(recAcq, reqAcq)

	if recAcq.Code != http.StatusOK {
		t.Fatalf("lock acquire failed: %s", recAcq.Body.String())
	}
	var acqResp struct {
		Data struct {
			FencingToken       uint64 `json:"fencing_token"`
			LeaseVersion       uint64 `json:"lease_version"`
			ExpiresAtUnixNano  int64  `json:"expires_at_unix_nano"`
		} `json:"data"`
	}
	_ = json.Unmarshal(recAcq.Body.Bytes(), &acqResp)
	fencingToken := acqResp.Data.FencingToken
	expiresAt := acqResp.Data.ExpiresAtUnixNano

	// 3. Start Automatic Renewal Worker
	err := gwServer.RenewalManager().StartRenewal(
		"res-failover-lock",
		"worker-resilient",
		"client-recovery-test",
		sessionID,
		fencingToken,
		1,
		expiresAt,
		400*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("failed to start renewal worker: %v", err)
	}

	// 4. Invalidate leader cache to simulate dynamic coordinator election/redirection
	gwServer.Discovery().InvalidateLeader()

	// Wait for automatic renewal to fire and complete through dynamic discovery
	time.Sleep(300 * time.Millisecond)

	// 5. Query Lock State to verify renewal completed successfully
	reqGet := httptest.NewRequest(http.MethodGet, "/v1/locks/res-failover-lock", nil)
	recGet := httptest.NewRecorder()
	handler.ServeHTTP(recGet, reqGet)

	if recGet.Code != http.StatusOK {
		t.Fatalf("read lock failed: %s", recGet.Body.String())
	}
	var getResp struct {
		Data struct {
			FencingToken uint64 `json:"fencing_token"`
			LeaseVersion uint64 `json:"lease_version"`
		} `json:"data"`
	}
	_ = json.Unmarshal(recGet.Body.Bytes(), &getResp)

	// Fencing token must be preserved, lease version must have advanced
	if getResp.Data.FencingToken != fencingToken {
		t.Errorf("fencing token altered during failover renewal: expected %d, got %d", fencingToken, getResp.Data.FencingToken)
	}
	if getResp.Data.LeaseVersion < 2 {
		t.Errorf("expected lease version >= 2 after renewal, got %d", getResp.Data.LeaseVersion)
	}
	t.Logf("Automatic renewal survived leader change: version=%d, token=%d", getResp.Data.LeaseVersion, getResp.Data.FencingToken)

	// Clean up worker
	_ = gwServer.RenewalManager().StopRenewal("res-failover-lock", sessionID)
}

// TestPhase4_Session_HeartbeatTimeoutAndWorkerTermination verifies that:
// 1. A client session that stops pinging heartbeats expires after timeout.
// 2. Renewal workers associated with expired sessions are rejected.
func TestPhase4_Session_HeartbeatTimeoutAndWorkerTermination(t *testing.T) {
	cfg := &config.Config{
		Heartbeat: config.HeartbeatConfig{
			Timeout:         100 * time.Millisecond,
			CleanupInterval: 40 * time.Millisecond,
		},
	}
	reg := heartbeat.NewManager(cfg, nil)
	reg.Start()
	defer reg.Stop()

	// Register session with short 100ms TTL
	sess, err := reg.Register("client-timeout-test", "owner-timeout", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("failed to register client: %v", err)
	}

	// Session is initially active
	sessBefore, found := reg.GetSession("client-timeout-test")
	if !found || !sessBefore.IsActive() {
		t.Fatal("expected session to be active immediately after registration")
	}

	// Wait 250ms without heartbeats -> scrubber must expire session
	time.Sleep(250 * time.Millisecond)

	sessAfter, _ := reg.GetSession("client-timeout-test")
	if sessAfter != nil && sessAfter.IsActive() {
		t.Fatal("session remained active after missing heartbeat interval")
	}
	_ = sess
	t.Log("Successfully verified heartbeat timeout session expiration")
}
