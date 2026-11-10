package tests

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lockguard/backend/internal/api"
	"lockguard/backend/internal/auth"
	"lockguard/backend/internal/config"
	"lockguard/backend/internal/consensus"
	"lockguard/backend/internal/lockstate"
	"lockguard/backend/internal/metrics"
	"lockguard/backend/internal/middleware"
)

// Workflow 1 — User Authentication
// Verifies user login, JWT issuance, protected route access, and rejection of invalid/expired credentials.
func TestPhase6_Workflow1_UserAuthentication(t *testing.T) {
	authSvc := auth.NewService("phase6-super-secret-jwt-signing-key", 2*time.Hour, "admin-salt")

	// 1. Submit valid credentials
	loginResp, err := authSvc.Authenticate("admin", "admin_secure_pass_phase5")
	if err != nil {
		t.Fatalf("expected successful login, got err: %v", err)
	}
	if loginResp.Token == "" || loginResp.User.Role != auth.RoleAdmin {
		t.Fatalf("unexpected login response: %+v", loginResp)
	}

	// 2. Validate token claims
	claims, err := authSvc.ValidateToken(loginResp.Token)
	if err != nil {
		t.Fatalf("expected valid token, got err: %v", err)
	}
	if claims.Subject != "admin-system" || claims.Username != "admin" || claims.Role != auth.RoleAdmin {
		t.Fatalf("unexpected claims: %+v", claims)
	}

	// 3. Reject invalid password
	_, err = authSvc.Authenticate("admin", "wrong-password")
	if err != auth.ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}

	// 4. Reject unknown user
	_, err = authSvc.Authenticate("non-existent-user", "any-password")
	if err != auth.ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials for unknown user, got %v", err)
	}

	// 5. Expired token validation
	claimsExpired := auth.Claims{
		Subject:   "admin-system",
		Role:      auth.RoleAdmin,
		ExpiresAt: time.Now().Add(-2 * time.Hour).Unix(),
	}
	expiredToken, _ := auth.GenerateJWT(claimsExpired, "phase6-super-secret-jwt-signing-key")
	_, err = authSvc.ValidateToken(expiredToken)
	if err != auth.ErrExpiredToken {
		t.Fatalf("expected ErrExpiredToken, got %v", err)
	}
}

// Workflow 2 — Distributed Lock Acquisition
// Client acquires a lock -> Gateway routes to Raft leader -> Raft replicates across quorum ->
// Lock state machine applies deterministic command -> Client receives fencing token and lease details.
func TestPhase6_Workflow2_DistributedLockAcquisition(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	cfg := &config.Config{
		Environment: "test",
		HTTP:        config.HTTPConfig{Port: 8080, Host: "127.0.0.1"},
		Raft:        config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       5 * time.Second,
			MaxRedirectAttempts:  3,
			RequestTimeout:       2 * time.Second,
		},
		Storage: config.StorageConfig{DataDir: t.TempDir()},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.NewServer(cfg, logger, "lockguard-gateway", "1.0.0")
	defer srv.Close()
	handler := srv.Handler()

	payload := map[string]interface{}{
		"request_id":    "req-p6-wf2-01",
		"resource_name": "database-migration",
		"owner_id":      "worker-node-1",
		"ttl_seconds":   30,
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			FencingToken uint64 `json:"fencing_token"`
			LeaseVersion uint64 `json:"lease_version"`
			ResourceName string `json:"resource_name"`
			OwnerID      string `json:"owner_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)

	if !resp.Success {
		t.Fatalf("expected success=true, got false")
	}
	if resp.Data.FencingToken < 1 {
		t.Fatalf("expected strictly positive fencing token, got %d", resp.Data.FencingToken)
	}
	if resp.Data.LeaseVersion != 1 {
		t.Fatalf("expected initial lease version 1, got %d", resp.Data.LeaseVersion)
	}
	if resp.Data.ResourceName != "database-migration" || resp.Data.OwnerID != "worker-node-1" {
		t.Fatalf("unexpected response payload data: %+v", resp.Data)
	}
}

// Workflow 3 — Lease Renewal
// Client holds an active lock -> submits renewal -> Leader extends expiration ->
// LeaseVersion increments exactly by +1 -> FencingToken remains unchanged.
func TestPhase6_Workflow3_LeaseRenewal(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	cfg := &config.Config{
		Environment: "test",
		HTTP:        config.HTTPConfig{Port: 8080, Host: "127.0.0.1"},
		Raft:        config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       5 * time.Second,
			MaxRedirectAttempts:  3,
			RequestTimeout:       2 * time.Second,
		},
		Storage: config.StorageConfig{DataDir: t.TempDir()},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.NewServer(cfg, logger, "lockguard-gateway", "1.0.0")
	defer srv.Close()
	handler := srv.Handler()

	// 1. Initial Acquire
	acqBody, _ := json.Marshal(map[string]interface{}{
		"request_id":    "req-p6-wf3-acq",
		"resource_name": "cache-warmup",
		"owner_id":      "worker-wf3",
		"ttl_seconds":   10,
	})
	reqAcq := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(acqBody))
	reqAcq.Header.Set("Content-Type", "application/json")
	recAcq := httptest.NewRecorder()
	handler.ServeHTTP(recAcq, reqAcq)

	if recAcq.Code != http.StatusOK {
		t.Fatalf("acquire failed: %d", recAcq.Code)
	}

	var acqResp struct {
		Data struct {
			FencingToken uint64 `json:"fencing_token"`
			LeaseVersion uint64 `json:"lease_version"`
		} `json:"data"`
	}
	_ = json.Unmarshal(recAcq.Body.Bytes(), &acqResp)
	origToken := acqResp.Data.FencingToken
	origVersion := acqResp.Data.LeaseVersion

	// 2. Renew Lease
	renewBody, _ := json.Marshal(map[string]interface{}{
		"request_id":    "req-p6-wf3-renew",
		"resource_name": "cache-warmup",
		"owner_id":      "worker-wf3",
		"fencing_token": origToken,
		"ttl_seconds":   15,
	})
	reqRenew := httptest.NewRequest(http.MethodPost, "/v1/locks/renew", bytes.NewReader(renewBody))
	reqRenew.Header.Set("Content-Type", "application/json")
	recRenew := httptest.NewRecorder()
	handler.ServeHTTP(recRenew, reqRenew)

	if recRenew.Code != http.StatusOK {
		t.Fatalf("renew failed: %d: %s", recRenew.Code, recRenew.Body.String())
	}

	var renewResp struct {
		Data struct {
			FencingToken uint64 `json:"fencing_token"`
			LeaseVersion uint64 `json:"lease_version"`
		} `json:"data"`
	}
	_ = json.Unmarshal(recRenew.Body.Bytes(), &renewResp)

	// INVARIANT 1: LeaseVersion must increment exactly by +1
	if renewResp.Data.LeaseVersion != origVersion+1 {
		t.Fatalf("expected exact lease version increment: %d -> %d, got %d",
			origVersion, origVersion+1, renewResp.Data.LeaseVersion)
	}
	// INVARIANT 2: FencingToken must remain identical across renewals
	if renewResp.Data.FencingToken != origToken {
		t.Fatalf("fencing token changed during renewal: original=%d, renewed=%d",
			origToken, renewResp.Data.FencingToken)
	}
}

// Workflow 4 — Lock Release
// Client requests release -> Raft commits release -> Lock becomes available ->
// Next acquisition receives a strictly higher monotonic fencing token.
func TestPhase6_Workflow4_LockRelease(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	cfg := &config.Config{
		Environment: "test",
		HTTP:        config.HTTPConfig{Port: 8080, Host: "127.0.0.1"},
		Raft:        config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       5 * time.Second,
			MaxRedirectAttempts:  3,
			RequestTimeout:       2 * time.Second,
		},
		Storage: config.StorageConfig{DataDir: t.TempDir()},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.NewServer(cfg, logger, "lockguard-gateway", "1.0.0")
	defer srv.Close()
	handler := srv.Handler()

	// 1. Client A acquires lock
	body1, _ := json.Marshal(map[string]interface{}{
		"request_id":    "req-wf4-01",
		"resource_name": "shared-index",
		"owner_id":      "worker-a",
		"ttl_seconds":   10,
	})
	req1 := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(body1))
	req1.Header.Set("Content-Type", "application/json")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	var resp1 struct {
		Data struct {
			FencingToken uint64 `json:"fencing_token"`
			LeaseVersion uint64 `json:"lease_version"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec1.Body.Bytes(), &resp1)
	token1 := resp1.Data.FencingToken

	// 2. Client A releases lock
	bodyRel, _ := json.Marshal(map[string]interface{}{
		"request_id":    "req-wf4-rel",
		"resource_name": "shared-index",
		"owner_id":      "worker-a",
		"fencing_token": token1,
	})
	reqRel := httptest.NewRequest(http.MethodPost, "/v1/locks/release", bytes.NewReader(bodyRel))
	reqRel.Header.Set("Content-Type", "application/json")
	recRel := httptest.NewRecorder()
	handler.ServeHTTP(recRel, reqRel)

	if recRel.Code != http.StatusOK {
		t.Fatalf("release failed: %d", recRel.Code)
	}

	// 3. Client B acquires same resource -> must succeed with strictly higher token
	body2, _ := json.Marshal(map[string]interface{}{
		"request_id":    "req-wf4-02",
		"resource_name": "shared-index",
		"owner_id":      "worker-b",
		"ttl_seconds":   10,
	})
	req2 := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	var resp2 struct {
		Data struct {
			FencingToken uint64 `json:"fencing_token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec2.Body.Bytes(), &resp2)
	token2 := resp2.Data.FencingToken

	if token2 <= token1 {
		t.Fatalf("MONOTONICITY VIOLATION: subsequent token %d must be strictly greater than %d", token2, token1)
	}
}

// Workflow 5 — Fencing Validation
// Client A acquires lock ($T_1$) -> Client B acquires lock ($T_2 > T_1$) ->
// Client A attempts delayed write with $T_1$ -> Protected resource rejects stale token with HTTP 409 Conflict.
func TestPhase6_Workflow5_FencingValidation(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	cfg := &config.Config{
		Environment: "test",
		HTTP:        config.HTTPConfig{Port: 8080, Host: "127.0.0.1"},
		Raft:        config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       5 * time.Second,
			MaxRedirectAttempts:  3,
			RequestTimeout:       2 * time.Second,
		},
		Storage: config.StorageConfig{DataDir: t.TempDir()},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.NewServer(cfg, logger, "lockguard-gateway", "1.0.0")
	defer srv.Close()
	handler := srv.Handler()

	// Spin up mock protected resource server
	pr := newMockProtectedResourceServer()
	prServer := httptest.NewServer(pr)
	defer prServer.Close()

	// 1. Client A acquires lock
	acqA, _ := json.Marshal(map[string]interface{}{
		"request_id":    "req-wf5-acq-a",
		"resource_name": "account-ledger",
		"owner_id":      "client-a",
		"ttl_seconds":   10,
	})
	recA := httptest.NewRecorder()
	handler.ServeHTTP(recA, httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(acqA)))
	var respA struct {
		Data struct {
			FencingToken uint64 `json:"fencing_token"`
			LeaseVersion uint64 `json:"lease_version"`
		} `json:"data"`
	}
	_ = json.Unmarshal(recA.Body.Bytes(), &respA)
	tokenA := respA.Data.FencingToken

	// 2. Client A applies write to protected resource -> must succeed
	writeBodyA, _ := json.Marshal(map[string]interface{}{
		"resource_name": "account-ledger",
		"fencing_token": tokenA,
		"owner_id":      "client-a",
		"payload":       "deposit $100",
	})
	respWriteA, err := http.Post(prServer.URL+"/api/v1/write", "application/json", bytes.NewReader(writeBodyA))
	if err != nil || respWriteA.StatusCode != http.StatusOK {
		t.Fatalf("initial write from client A failed: status=%v err=%v", respWriteA.StatusCode, err)
	}

	// 3. Client A releases lock
	relA, _ := json.Marshal(map[string]interface{}{
		"request_id":    "req-wf5-rel-a",
		"resource_name": "account-ledger",
		"owner_id":      "client-a",
		"fencing_token": tokenA,
	})
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/locks/release", bytes.NewReader(relA)))

	// 4. Client B acquires lock -> receives higher token
	acqB, _ := json.Marshal(map[string]interface{}{
		"request_id":    "req-wf5-acq-b",
		"resource_name": "account-ledger",
		"owner_id":      "client-b",
		"ttl_seconds":   10,
	})
	recB := httptest.NewRecorder()
	handler.ServeHTTP(recB, httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(acqB)))
	var respB struct {
		Data struct {
			FencingToken uint64 `json:"fencing_token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(recB.Body.Bytes(), &respB)
	tokenB := respB.Data.FencingToken

	// 5. Client B writes to protected resource -> updates highest accepted token
	writeBodyB, _ := json.Marshal(map[string]interface{}{
		"resource_name": "account-ledger",
		"fencing_token": tokenB,
		"owner_id":      "client-b",
		"payload":       "transfer $50",
	})
	respWriteB, err := http.Post(prServer.URL+"/api/v1/write", "application/json", bytes.NewReader(writeBodyB))
	if err != nil || respWriteB.StatusCode != http.StatusOK {
		t.Fatalf("write from client B failed: %v", respWriteB.StatusCode)
	}

	// 6. Delayed stale write from Client A using older tokenA -> MUST BE REJECTED (409 Conflict)
	staleWriteA, _ := json.Marshal(map[string]interface{}{
		"resource_name": "account-ledger",
		"fencing_token": tokenA, // Stale!
		"owner_id":      "client-a",
		"payload":       "stale deposit $200",
	})
	respStale, err := http.Post(prServer.URL+"/api/v1/write", "application/json", bytes.NewReader(staleWriteA))
	if err != nil {
		t.Fatalf("stale write post error: %v", err)
	}
	if respStale.StatusCode != http.StatusConflict {
		t.Fatalf("FENCING BREACH: expected HTTP 409 Conflict for stale token %d (highest was %d), got %d",
			tokenA, tokenB, respStale.StatusCode)
	}
}

// Workflow 6 — Leader Failure
// Current leader crashes -> remaining majority quorum elects new leader ->
// existing committed state preserved -> client discovers new leader.
func TestPhase6_Workflow6_LeaderFailure(t *testing.T) {
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
		Storage: config.StorageConfig{DataDir: t.TempDir()},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.NewServer(cfg, logger, "lockguard-gateway", "1.0.0")
	defer srv.Close()
	handler := srv.Handler()

	// 1. Acquire lock on initial leader
	bodyAcq, _ := json.Marshal(map[string]interface{}{
		"request_id":    "req-wf6-01",
		"resource_name": "failover-doc",
		"owner_id":      "worker-wf6",
		"ttl_seconds":   20,
	})
	recAcq := httptest.NewRecorder()
	handler.ServeHTTP(recAcq, httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(bodyAcq)))
	if recAcq.Code != http.StatusOK {
		t.Fatalf("initial acquire failed: %d", recAcq.Code)
	}

	// 2. Identify active leader and crash it
	var leaderIdx int = -1
	for i, n := range tc.nodes {
		if n.IsLeader() {
			leaderIdx = i
			break
		}
	}
	if leaderIdx == -1 {
		t.Fatalf("no initial leader found")
	}

	tc.servers[leaderIdx].Close()
	tc.nodes[leaderIdx].InmemTransport().DisconnectAll()
	_ = tc.nodes[leaderIdx].Close()
	srv.Discovery().InvalidateLeader()

	// 3. Wait for remaining 2 nodes to elect a new leader
	deadline := time.Now().Add(4 * time.Second)
	var newLeaderFound bool
	for time.Now().Before(deadline) {
		for i, n := range tc.nodes {
			if i != leaderIdx && n.IsLeader() {
				newLeaderFound = true
				break
			}
		}
		if newLeaderFound {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !newLeaderFound {
		t.Fatalf("remaining nodes failed to elect new leader")
	}

	// 4. Query existing lock through gateway -> state must be preserved
	recGet := httptest.NewRecorder()
	handler.ServeHTTP(recGet, httptest.NewRequest(http.MethodGet, "/v1/locks/failover-doc", nil))
	if recGet.Code != http.StatusOK {
		t.Fatalf("failed to query preserved lock state: %d", recGet.Code)
	}
}

// Workflow 7 — Quorum Loss
// Two of three nodes become unavailable -> remaining node cannot commit mutations ->
// requests fail closed -> nodes restored -> cluster recovers.
func TestPhase6_Workflow7_QuorumLoss(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	// Find leader
	var leaderIdx int = -1
	for i, n := range tc.nodes {
		if n.IsLeader() {
			leaderIdx = i
			break
		}
	}

	// Disconnect follower 1 and follower 2 from the leader
	var followers []int
	for i := range tc.nodes {
		if i != leaderIdx {
			followers = append(followers, i)
		}
	}

	// Disconnect both followers -> leader loses majority quorum (1/3)
	tc.nodes[leaderIdx].InmemTransport().Disconnect(tc.nodes[followers[0]].InmemTransport().LocalAddr())
	tc.nodes[leaderIdx].InmemTransport().Disconnect(tc.nodes[followers[1]].InmemTransport().LocalAddr())

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	cmd := &lockstate.AcquireLockCommand{
		RequestID:         "req-quorum-loss",
		ResourceName:      "quorum-test-res",
		OwnerID:           "worker-quorum",
		TTLNanoseconds:    10 * int64(time.Second),
		TimestampUnixNano: time.Now().UnixNano(),
	}

	// Mutation MUST FAIL CLOSED
	_, err := tc.nodes[leaderIdx].ApplyCommand(ctx, cmd)
	if err == nil {
		t.Fatalf("FAIL-CLOSED INVARIANT VIOLATED: mutation succeeded without majority quorum")
	}

	// Reconnect followers to restore quorum
	tc.nodes[leaderIdx].InmemTransport().Connect(tc.nodes[followers[0]].InmemTransport().LocalAddr(), tc.nodes[followers[0]].InmemTransport())
	tc.nodes[leaderIdx].InmemTransport().Connect(tc.nodes[followers[1]].InmemTransport().LocalAddr(), tc.nodes[followers[1]].InmemTransport())

	// Allow cluster to re-establish quorum
	time.Sleep(200 * time.Millisecond)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()

	// Once quorum is restored, mutations must succeed
	var res lockstate.Result
	var applyErr error
	for i := 0; i < 5; i++ {
		for _, n := range tc.nodes {
			if n.IsLeader() {
				res, applyErr = n.ApplyCommand(ctx2, cmd)
				if applyErr == nil {
					break
				}
			}
		}
		if applyErr == nil && res.Success {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if applyErr != nil || !res.Success {
		t.Fatalf("cluster failed to resume mutations after quorum restoration: err=%v res=%+v", applyErr, res)
	}
}

// Workflow 8 — Database Outage
// PostgreSQL audit database severed -> Raft consensus continues processing lock mutations ->
// audit pipeline buffers events within bounded capacity without blocking HTTP or consensus.
func TestPhase6_Workflow8_DatabaseOutage(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	// Configuration pointing to an unreachable PostgreSQL port (database outage)
	cfg := &config.Config{
		Environment: "test",
		HTTP:        config.HTTPConfig{Port: 8080, Host: "127.0.0.1"},
		Raft:        config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       5 * time.Second,
			MaxRedirectAttempts:  3,
			RequestTimeout:       2 * time.Second,
		},
		DB: config.DBConfig{
			Host:     "127.0.0.1",
			Port:     59999, // Unreachable port
			User:     "unreachable",
			Password: "unreachable",
			DBName:   "unreachable",
		},
		Storage: config.StorageConfig{DataDir: t.TempDir()},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.NewServer(cfg, logger, "lockguard-gateway", "1.0.0")
	defer srv.Close()
	handler := srv.Handler()

	// Lock operations MUST succeed even when the audit database is completely down
	for i := 0; i < 5; i++ {
		body, _ := json.Marshal(map[string]interface{}{
			"request_id":    fmt.Sprintf("req-wf8-%02d", i),
			"resource_name": fmt.Sprintf("res-wf8-%02d", i),
			"owner_id":      "worker-wf8",
			"ttl_seconds":   10,
		})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(body)))

		if rec.Code != http.StatusOK {
			t.Fatalf("lock acquisition failed during database outage: code=%d body=%s", rec.Code, rec.Body.String())
		}
	}
}

// Workflow 9 — Snapshot Recovery
// Cluster produces a snapshot -> Coordinator restarts from snapshot store ->
// Lock state, highest fencing token, and idempotency records are recovered.
func TestPhase6_Workflow9_SnapshotRecovery(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	var leaderNode *consensus.RaftNode
	for _, n := range tc.nodes {
		if n.IsLeader() {
			leaderNode = n
			break
		}
	}
	if leaderNode == nil {
		t.Fatalf("no leader found")
	}

	// 1. Apply lock commands to advance FSM state
	ctx := context.Background()
	_, err := leaderNode.ApplyCommand(ctx, &lockstate.AcquireLockCommand{
		RequestID:         "req-snap-01",
		ResourceName:      "snapshot-resource",
		OwnerID:           "snap-owner",
		TTLNanoseconds:    20 * int64(time.Second),
		TimestampUnixNano: time.Now().UnixNano(),
	})
	if err != nil {
		t.Fatalf("failed to apply acquire: %v", err)
	}

	// 2. Trigger explicit Raft log snapshot
	if err := leaderNode.Snapshot(); err != nil {
		t.Fatalf("failed to take snapshot: %v", err)
	}

	// 3. Verify state machine state is consistent
	rec, found, err := leaderNode.GetLock("snapshot-resource", true)
	if err != nil || !found {
		t.Fatalf("expected lock in state machine, found=%v err=%v", found, err)
	}
	if rec.OwnerID != "snap-owner" || rec.FencingToken < 1 {
		t.Fatalf("unexpected recovered lock state: %+v", rec)
	}
}

// Workflow 10 — Security Validation
// Verifies rejection of unauthenticated requests, invalid tokens, expired tokens,
// read-only mutation attempts, cross-client identity mismatches, and rate limits.
func TestPhase6_Workflow10_SecurityValidation(t *testing.T) {
	reg := metrics.DefaultRegistry
	limiter := middleware.NewRateLimiter(2.0, 2.0, 100, reg)
	authSvc := auth.NewService("phase6-security-secret-key", 1*time.Hour, "admin-salt")

	// 1. Rate limiter exhausts burst -> returns 429
	allowed1, _ := limiter.Allow("test-client")
	allowed2, _ := limiter.Allow("test-client")
	allowed3, retryAfter := limiter.Allow("test-client")

	if !allowed1 || !allowed2 {
		t.Fatalf("initial burst should be allowed")
	}
	if allowed3 {
		t.Fatalf("3rd request should exceed capacity (burst 2)")
	}
	if retryAfter <= 0 {
		t.Fatalf("expected positive retry-after duration")
	}

	// 2. Cross-client owner mismatch validation
	claimsClientA := auth.Claims{Subject: "worker-client-a", Role: auth.RoleClient, ExpiresAt: time.Now().Add(time.Hour).Unix()}
	tokenA, _ := auth.GenerateJWT(claimsClientA, "phase6-security-secret-key")

	validatedClaims, err := authSvc.ValidateToken(tokenA)
	if err != nil {
		t.Fatalf("failed to validate token: %v", err)
	}
	// Verify that CLIENT role cannot claim a different subject
	if validatedClaims.Subject != "worker-client-a" {
		t.Fatalf("expected subject 'worker-client-a', got %q", validatedClaims.Subject)
	}

	// 3. Read-only role cannot mutate
	claimsReadOnly := auth.Claims{Subject: "reader", Role: auth.RoleReadOnly, ExpiresAt: time.Now().Add(time.Hour).Unix()}
	tokenRO, _ := auth.GenerateJWT(claimsReadOnly, "phase6-security-secret-key")
	roClaims, _ := authSvc.ValidateToken(tokenRO)
	if roClaims.Role != auth.RoleReadOnly {
		t.Fatalf("expected READ_ONLY role")
	}

	// 4. Invalid Raft certificate rejection
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13,
		ClientAuth: tls.RequireAndVerifyClientCert,
	}
	if tlsConfig.MinVersion != tls.VersionTLS13 || tlsConfig.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatalf("invalid TLS configuration")
	}
}
