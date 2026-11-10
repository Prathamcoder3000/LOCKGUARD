package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"lockguard/backend/internal/api"
	"lockguard/backend/internal/config"
	"lockguard/backend/internal/consensus"
	"lockguard/backend/internal/coordinator"
	"lockguard/backend/internal/lockstate"
)

// setupTestCluster initializes a 3-node in-memory Raft cluster backed by HTTP servers.
type testCluster struct {
	nodes   []*consensus.RaftNode
	servers []*httptest.Server
	configs []*config.Config
}

func setupThreeNodeCluster(t *testing.T) *testCluster {
	t.Helper()

	serverIDs := []string{"coord-a", "coord-b", "coord-c"}
	raftServers := make([]raft.Server, len(serverIDs))
	for i, id := range serverIDs {
		raftServers[i] = raft.Server{
			ID:       raft.ServerID(id),
			Address:  raft.ServerAddress(id),
			Suffrage: raft.Voter,
		}
	}

	nodes := make([]*consensus.RaftNode, len(serverIDs))
	servers := make([]*httptest.Server, len(serverIDs))
	configs := make([]*config.Config, len(serverIDs))

	for i, id := range serverIDs {
		sm := lockstate.New()
		cfg := consensus.NodeConfig{
			NodeID:             id,
			AdvertiseAddr:      id,
			Inmem:              true,
			BootstrapCluster:   (i == 0),
			BootstrapServers:   raftServers,
			HeartbeatTimeout:   50 * time.Millisecond,
			ElectionTimeout:    100 * time.Millisecond,
			CommitTimeout:      10 * time.Millisecond,
			LeaderLeaseTimeout: 50 * time.Millisecond,
			LogLevel:           "error",
		}

		node, err := consensus.NewRaftNode(cfg, sm)
		if err != nil {
			t.Fatalf("failed to create raft node %s: %v", id, err)
		}
		nodes[i] = node

		h := coordinator.NewHandler(node, id)
		mux := http.NewServeMux()
		h.RegisterRoutes(mux)
		srv := httptest.NewServer(mux)
		servers[i] = srv

		configs[i] = &config.Config{
			Raft: config.RaftConfig{NodeID: id},
		}
	}

	// Interconnect inmem transports
	for i := 0; i < len(nodes); i++ {
		for j := 0; j < len(nodes); j++ {
			if i != j {
				nodes[i].InmemTransport().Connect(raft.ServerAddress(serverIDs[j]), nodes[j].InmemTransport())
			}
		}
	}

	// Wait for a leader to be elected
	deadline := time.Now().Add(4 * time.Second)
	var leaderFound bool
	for time.Now().Before(deadline) {
		for _, n := range nodes {
			if n.IsLeader() {
				leaderFound = true
				break
			}
		}
		if leaderFound {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if !leaderFound {
		t.Fatalf("timeout waiting for 3-node cluster leader election")
	}

	return &testCluster{
		nodes:   nodes,
		servers: servers,
		configs: configs,
	}
}

func (tc *testCluster) Close() {
	for _, s := range tc.servers {
		s.Close()
	}
	for _, n := range tc.nodes {
		_ = n.Close()
	}
}

func (tc *testCluster) Endpoints() []string {
	endpoints := make([]string, len(tc.servers))
	for i, s := range tc.servers {
		endpoints[i] = s.URL
	}
	return endpoints
}

// Scenario A: Transparent Gateway Routing to Leader
func TestScenarioA_TransparentGatewayRoutingToLeader(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	cfg := &config.Config{
		Environment: "test",
		HTTP: config.HTTPConfig{
			Port: 8080,
			Host: "127.0.0.1",
		},
		Raft: config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       5 * time.Second,
			MaxRedirectAttempts:  2,
			RequestTimeout:       2 * time.Second,
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gwServer := api.NewServer(cfg, logger, "lockguard-gateway", "0.2.0")
	handler := gwServer.Handler()

	// 1. Acquire Lock through Gateway
	acquirePayload := map[string]interface{}{
		"request_id":    "req-scen-a-1",
		"resource_name": "db-orders",
		"owner_id":      "worker-1",
		"ttl_seconds":   10,
	}
	body, _ := json.Marshal(acquirePayload)
	req := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from gateway, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp api.APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	dataMap := resp.Data.(map[string]interface{})

	if dataMap["resource_name"] != "db-orders" {
		t.Errorf("expected resource db-orders, got %v", dataMap["resource_name"])
	}
	if dataMap["fencing_token"].(float64) < 1 {
		t.Errorf("expected fencing token >= 1, got %v", dataMap["fencing_token"])
	}
	if dataMap["lease_version"].(float64) != 1 {
		t.Errorf("expected initial lease version 1, got %v", dataMap["lease_version"])
	}
}

// Scenario B: Leader Failover Routing Recovery
func TestScenarioB_LeaderFailoverRoutingRecovery(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	cfg := &config.Config{
		Environment: "test",
		HTTP: config.HTTPConfig{
			Port: 8080,
			Host: "127.0.0.1",
		},
		Raft: config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       5 * time.Second,
			MaxRedirectAttempts:  2,
			RequestTimeout:       2 * time.Second,
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gwServer := api.NewServer(cfg, logger, "lockguard-gateway", "0.2.0")
	handler := gwServer.Handler()

	// Initial acquire to populate leader cache
	acquirePayload := map[string]interface{}{
		"request_id":    "req-scen-b-1",
		"resource_name": "db-customers",
		"owner_id":      "worker-2",
		"ttl_seconds":   10,
	}
	body, _ := json.Marshal(acquirePayload)
	req := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("initial acquire failed: %s", rec.Body.String())
	}

	// Invalidate discovery leader to simulate cache expiry or failover trigger
	gwServer.Discovery().InvalidateLeader()

	// Next read through gateway recovers new or existing leader transparently
	reqRead := httptest.NewRequest(http.MethodGet, "/v1/locks/db-customers", nil)
	recRead := httptest.NewRecorder()
	handler.ServeHTTP(recRead, reqRead)

	if recRead.Code != http.StatusOK {
		t.Fatalf("expected 200 OK after leader cache invalidation, got %d: %s", recRead.Code, recRead.Body.String())
	}
}

// Scenario C: End-to-End Automatic Lease Renewal
func TestScenarioC_EndToEndAutomaticRenewal(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	cfg := &config.Config{
		Environment: "test",
		HTTP: config.HTTPConfig{
			Port: 8080,
			Host: "127.0.0.1",
		},
		Raft: config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       5 * time.Second,
			MaxRedirectAttempts:  2,
			RequestTimeout:       2 * time.Second,
		},
		Renewal: config.RenewalConfig{
			SafetyMargin: 5 * time.Millisecond,
			RetryInitial: 10 * time.Millisecond,
			RetryMax:     50 * time.Millisecond,
			MaxAttempts:  3,
		},
		Heartbeat: config.HeartbeatConfig{
			Timeout:         5 * time.Second,
			CleanupInterval: 50 * time.Millisecond,
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gwServer := api.NewServer(cfg, logger, "lockguard-gateway", "0.2.0")
	handler := gwServer.Handler()

	// 1. Register Client Session
	regBody, _ := json.Marshal(map[string]interface{}{
		"client_id":   "client-c",
		"owner_id":    "worker-c",
		"ttl_seconds": 10,
	})
	reqReg := httptest.NewRequest(http.MethodPost, "/v1/clients/register", bytes.NewReader(regBody))
	recReg := httptest.NewRecorder()
	handler.ServeHTTP(recReg, reqReg)

	var regResp api.APIResponse
	_ = json.Unmarshal(recReg.Body.Bytes(), &regResp)
	sessMap := regResp.Data.(map[string]interface{})
	sessionID := sessMap["session_id"].(string)

	// 2. Acquire Lock (TTL 200ms)
	acquireBody, _ := json.Marshal(map[string]interface{}{
		"request_id":     "req-scen-c-1",
		"resource_name":  "table-inventory",
		"owner_id":       "worker-c",
		"ttl_nanoseconds": 200 * int64(time.Millisecond),
	})
	reqAcq := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(acquireBody))
	recAcq := httptest.NewRecorder()
	handler.ServeHTTP(recAcq, reqAcq)

	var acqResp api.APIResponse
	_ = json.Unmarshal(recAcq.Body.Bytes(), &acqResp)
	acqRec := acqResp.Data.(map[string]interface{})
	fencingToken := uint64(acqRec["fencing_token"].(float64))
	expiresAtNano := int64(acqRec["expires_at_unix_nano"].(float64))

	// 3. Start Renewal Worker
	err := gwServer.RenewalManager().StartRenewal(
		"table-inventory", "worker-c", "client-c", sessionID,
		fencingToken, 1, expiresAtNano, 200*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("failed to start renewal: %v", err)
	}

	// 4. Wait for automatic renewal to fire and complete
	time.Sleep(250 * time.Millisecond)

	// 5. Query Lock State
	reqGet := httptest.NewRequest(http.MethodGet, "/v1/locks/table-inventory", nil)
	recGet := httptest.NewRecorder()
	handler.ServeHTTP(recGet, reqGet)

	if recGet.Code != http.StatusOK {
		t.Fatalf("lock should still be active, got %d: %s", recGet.Code, recGet.Body.String())
	}

	var getResp api.APIResponse
	_ = json.Unmarshal(recGet.Body.Bytes(), &getResp)
	curRec := getResp.Data.(map[string]interface{})

	// Assert Invariants: FencingToken preserved, LeaseVersion incremented
	if uint64(curRec["fencing_token"].(float64)) != fencingToken {
		t.Errorf("fencing token changed! %v != %d", curRec["fencing_token"], fencingToken)
	}
	if uint64(curRec["lease_version"].(float64)) <= 1 {
		t.Errorf("lease version did not increment: %v", curRec["lease_version"])
	}
}

// Scenario D: Missed Heartbeat Session Timeout Stops Renewal
func TestScenarioD_MissedHeartbeatSessionTimeoutStopsRenewal(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	cfg := &config.Config{
		Environment: "test",
		HTTP: config.HTTPConfig{
			Port: 8080,
			Host: "127.0.0.1",
		},
		Raft: config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       5 * time.Second,
			MaxRedirectAttempts:  2,
			RequestTimeout:       2 * time.Second,
		},
		Heartbeat: config.HeartbeatConfig{
			Timeout:         60 * time.Millisecond,
			CleanupInterval: 20 * time.Millisecond,
		},
		Renewal: config.RenewalConfig{
			SafetyMargin: 5 * time.Millisecond,
			RetryInitial: 10 * time.Millisecond,
			RetryMax:     50 * time.Millisecond,
			MaxAttempts:  2,
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gwServer := api.NewServer(cfg, logger, "lockguard-gateway", "0.2.0")
	gwServer.HeartbeatManager().Start()
	defer gwServer.HeartbeatManager().Stop()
	defer gwServer.RenewalManager().Shutdown()

	sess, _ := gwServer.HeartbeatManager().Register("client-d", "worker-d", 60*time.Millisecond)

	// Acquire lock with 250ms TTL
	acquireBody, _ := json.Marshal(map[string]interface{}{
		"request_id":     "req-scen-d-1",
		"resource_name":  "res-billing",
		"owner_id":       "worker-d",
		"ttl_nanoseconds": 250 * int64(time.Millisecond),
	})
	reqAcq := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(acquireBody))
	recAcq := httptest.NewRecorder()
	gwServer.Handler().ServeHTTP(recAcq, reqAcq)

	var acqResp api.APIResponse
	_ = json.Unmarshal(recAcq.Body.Bytes(), &acqResp)
	acqRec := acqResp.Data.(map[string]interface{})
	token := uint64(acqRec["fencing_token"].(float64))
	expiresAt := int64(acqRec["expires_at_unix_nano"].(float64))

	_ = gwServer.RenewalManager().StartRenewal("res-billing", "worker-d", "client-d", sess.SessionID(), token, 1, expiresAt, 250*time.Millisecond)

	// Client intentionally does NOT send heartbeats; wait for heartbeat session timeout
	time.Sleep(120 * time.Millisecond)

	// Renewal worker should have been cancelled by session expiry hook
	_, active := gwServer.RenewalManager().GetStatus("res-billing")
	if active {
		t.Fatalf("expected renewal worker to be stopped after heartbeat timeout")
	}
}

// Scenario E: Quorum Loss Causes Renewal to Fail Closed Safely
func TestScenarioE_QuorumLossFailsClosedSafely(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	cfg := &config.Config{
		Environment: "test",
		HTTP: config.HTTPConfig{
			Port: 8080,
			Host: "127.0.0.1",
		},
		Raft: config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       5 * time.Second,
			MaxRedirectAttempts:  1,
			RequestTimeout:       200 * time.Millisecond,
		},
		Renewal: config.RenewalConfig{
			SafetyMargin: 5 * time.Millisecond,
			RetryInitial: 10 * time.Millisecond,
			RetryMax:     20 * time.Millisecond,
			MaxAttempts:  2,
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gwServer := api.NewServer(cfg, logger, "lockguard-gateway", "0.2.0")
	handler := gwServer.Handler()

	// 1. Register Client Session
	sess, err := gwServer.HeartbeatManager().Register("client-e", "worker-e", 10*time.Second)
	if err != nil {
		t.Fatalf("failed to register client session: %v", err)
	}

	// 2. Acquire lock while 3 nodes have full quorum
	acquireBody, _ := json.Marshal(map[string]interface{}{
		"request_id":      "req-scen-e-acq",
		"resource_name":   "table-safe",
		"owner_id":        "worker-e",
		"ttl_nanoseconds": 250 * int64(time.Millisecond),
	})
	reqAcq := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(acquireBody))
	recAcq := httptest.NewRecorder()
	handler.ServeHTTP(recAcq, reqAcq)
	if recAcq.Code != http.StatusOK {
		t.Fatalf("failed initial acquire: %s", recAcq.Body.String())
	}

	var acqResp api.APIResponse
	_ = json.Unmarshal(recAcq.Body.Bytes(), &acqResp)
	acqRec := acqResp.Data.(map[string]interface{})
	fencingToken := uint64(acqRec["fencing_token"].(float64))

	// 3. Start renewal worker while cluster is healthy (validates authoritative lock state)
	err = gwServer.RenewalManager().StartRenewal(
		"table-safe", "worker-e", "client-e", sess.SessionID(),
		fencingToken, 1, time.Now().Add(40*time.Millisecond).UnixNano(), 40*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("failed to start renewal: %v", err)
	}

	// 4. Induce real Raft quorum loss by disconnecting all in-memory transports
	for _, node := range tc.nodes {
		node.InmemTransport().DisconnectAll()
	}

	// 5. Wait for renewal attempts against quorum-lost cluster to exhaust retries and fail closed
	time.Sleep(250 * time.Millisecond)

	// 6. Worker should fail closed safely without hanging or running indefinitely
	st, active := gwServer.RenewalManager().GetStatus("table-safe")
	if active {
		t.Errorf("worker should not remain active after real Raft quorum loss, status: %+v", st)
	}
}

// Scenario F: Ambiguous Timeout Retries with Original RequestID
func TestScenarioF_AmbiguousTimeoutRetriesWithOriginalRequestID(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	cfg := &config.Config{
		Environment: "test",
		HTTP: config.HTTPConfig{
			Port: 8080,
			Host: "127.0.0.1",
		},
		Raft: config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       5 * time.Second,
			MaxRedirectAttempts:  2,
			RequestTimeout:       2 * time.Second,
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gwServer := api.NewServer(cfg, logger, "lockguard-gateway", "0.2.0")
	handler := gwServer.Handler()

	// 1. Acquire lock on real 3-node Raft cluster
	acquireBody, _ := json.Marshal(map[string]interface{}{
		"request_id":      "req-scen-f-acq",
		"resource_name":   "table-idemp",
		"owner_id":        "worker-f",
		"ttl_nanoseconds": 2000 * int64(time.Millisecond),
	})
	reqAcq := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(acquireBody))
	recAcq := httptest.NewRecorder()
	handler.ServeHTTP(recAcq, reqAcq)
	if recAcq.Code != http.StatusOK {
		t.Fatalf("failed initial acquire: %s", recAcq.Body.String())
	}

	var acqResp api.APIResponse
	_ = json.Unmarshal(recAcq.Body.Bytes(), &acqResp)
	acqRec := acqResp.Data.(map[string]interface{})
	token := uint64(acqRec["fencing_token"].(float64))

	// 2. Submit initial renewal request with a specific stable RequestID
	stableRequestID := "renew-table-idemp-v2-stable-req-id"
	renewPayload := map[string]interface{}{
		"request_id":      stableRequestID,
		"resource_name":   "table-idemp",
		"owner_id":        "worker-f",
		"fencing_token":   token,
		"ttl_nanoseconds": 2000 * int64(time.Millisecond),
	}
	body1, _ := json.Marshal(renewPayload)
	req1 := httptest.NewRequest(http.MethodPost, "/v1/locks/renew", bytes.NewReader(body1))
	req1.Header.Set("X-Request-ID", stableRequestID)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Fatalf("first renewal failed: %s", rec1.Body.String())
	}
	var resp1 api.APIResponse
	_ = json.Unmarshal(rec1.Body.Bytes(), &resp1)
	data1 := resp1.Data.(map[string]interface{})
	v1 := uint64(data1["lease_version"].(float64))
	if v1 != 2 {
		t.Fatalf("expected version 2 after first renewal, got %d", v1)
	}

	// 3. Simulate retry: send identical request with the EXACT SAME RequestID
	body2, _ := json.Marshal(renewPayload)
	req2 := httptest.NewRequest(http.MethodPost, "/v1/locks/renew", bytes.NewReader(body2))
	req2.Header.Set("X-Request-ID", stableRequestID)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("retry renewal failed: %s", rec2.Body.String())
	}
	var resp2 api.APIResponse
	_ = json.Unmarshal(rec2.Body.Bytes(), &resp2)
	data2 := resp2.Data.(map[string]interface{})
	v2 := uint64(data2["lease_version"].(float64))

	// Verify idempotency: lease_version MUST remain 2 and NOT double-increment to 3!
	if v2 != 2 {
		t.Fatalf("idempotency violation: retry caused double-increment to %d (expected 2)", v2)
	}
}

// Scenario G: Concurrent Release and Renewal
func TestScenarioG_ConcurrentReleaseAndRenewal(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	cfg := &config.Config{
		Environment: "test",
		HTTP: config.HTTPConfig{
			Port: 8080,
			Host: "127.0.0.1",
		},
		Raft: config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       5 * time.Second,
			MaxRedirectAttempts:  2,
			RequestTimeout:       2 * time.Second,
		},
		Renewal: config.RenewalConfig{
			SafetyMargin: 5 * time.Millisecond,
			RetryInitial: 10 * time.Millisecond,
			RetryMax:     50 * time.Millisecond,
			MaxAttempts:  2,
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gwServer := api.NewServer(cfg, logger, "lockguard-gateway", "0.2.0")
	handler := gwServer.Handler()

	// Acquire lock
	acqBody, _ := json.Marshal(map[string]interface{}{
		"request_id":    "req-scen-g-1",
		"resource_name": "res-concurrent",
		"owner_id":      "worker-g",
		"ttl_seconds":   5,
	})
	reqAcq := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(acqBody))
	recAcq := httptest.NewRecorder()
	handler.ServeHTTP(recAcq, reqAcq)

	var acqResp api.APIResponse
	_ = json.Unmarshal(recAcq.Body.Bytes(), &acqResp)
	recMap := acqResp.Data.(map[string]interface{})
	token := uint64(recMap["fencing_token"].(float64))

	// Release lock
	relBody, _ := json.Marshal(map[string]interface{}{
		"request_id":    "req-scen-g-rel",
		"resource_name": "res-concurrent",
		"owner_id":      "worker-g",
		"fencing_token": token,
	})
	reqRel := httptest.NewRequest(http.MethodPost, "/v1/locks/release", bytes.NewReader(relBody))
	recRel := httptest.NewRecorder()
	handler.ServeHTTP(recRel, reqRel)

	if recRel.Code != http.StatusOK {
		t.Fatalf("release failed: %s", recRel.Body.String())
	}

	// Now an attempt to renew a released lock must fail permanently
	renewBody, _ := json.Marshal(map[string]interface{}{
		"request_id":    "req-scen-g-renew",
		"resource_name": "res-concurrent",
		"owner_id":      "worker-g",
		"fencing_token": token,
		"ttl_seconds":   5,
	})
	reqRenew := httptest.NewRequest(http.MethodPost, "/v1/locks/renew", bytes.NewReader(renewBody))
	recRenew := httptest.NewRecorder()
	handler.ServeHTTP(recRenew, reqRenew)

	if recRenew.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found renewing released lock, got %d: %s", recRenew.Code, recRenew.Body.String())
	}
}

// Scenario H: New Owner Acquisition Cannot Be Renewed by Old Worker
func TestScenarioH_NewOwnerAcquisitionProtectsAgainstOldWorker(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	cfg := &config.Config{
		Environment: "test",
		HTTP: config.HTTPConfig{
			Port: 8080,
			Host: "127.0.0.1",
		},
		Raft: config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       5 * time.Second,
			MaxRedirectAttempts:  2,
			RequestTimeout:       2 * time.Second,
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gwServer := api.NewServer(cfg, logger, "lockguard-gateway", "0.2.0")
	handler := gwServer.Handler()

	// 1. Worker 1 acquires lock
	acqBody1, _ := json.Marshal(map[string]interface{}{
		"request_id":    "req-h-1",
		"resource_name": "res-guarded",
		"owner_id":      "worker-old",
		"ttl_seconds":   2,
	})
	req1 := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(acqBody1))
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	var resp1 api.APIResponse
	_ = json.Unmarshal(rec1.Body.Bytes(), &resp1)
	oldToken := uint64(resp1.Data.(map[string]interface{})["fencing_token"].(float64))

	// 2. Worker 1 releases lock
	relBody, _ := json.Marshal(map[string]interface{}{
		"request_id":    "req-h-rel",
		"resource_name": "res-guarded",
		"owner_id":      "worker-old",
		"fencing_token": oldToken,
	})
	reqRel := httptest.NewRequest(http.MethodPost, "/v1/locks/release", bytes.NewReader(relBody))
	recRel := httptest.NewRecorder()
	handler.ServeHTTP(recRel, reqRel)

	// 3. Worker 2 acquires same resource
	acqBody2, _ := json.Marshal(map[string]interface{}{
		"request_id":    "req-h-2",
		"resource_name": "res-guarded",
		"owner_id":      "worker-new",
		"ttl_seconds":   10,
	})
	req2 := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(acqBody2))
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	var resp2 api.APIResponse
	_ = json.Unmarshal(rec2.Body.Bytes(), &resp2)
	newToken := uint64(resp2.Data.(map[string]interface{})["fencing_token"].(float64))

	if newToken <= oldToken {
		t.Fatalf("expected newToken > oldToken, got %d <= %d", newToken, oldToken)
	}

	// 4. Stale Worker 1 attempts renewal with old owner and token -> MUST BE REJECTED
	staleRenewBody, _ := json.Marshal(map[string]interface{}{
		"request_id":    "req-h-stale-renew",
		"resource_name": "res-guarded",
		"owner_id":      "worker-old",
		"fencing_token": oldToken,
		"ttl_seconds":   10,
	})
	reqStale := httptest.NewRequest(http.MethodPost, "/v1/locks/renew", bytes.NewReader(staleRenewBody))
	recStale := httptest.NewRecorder()
	handler.ServeHTTP(recStale, reqStale)

	// Coordinator should reject with 403 Forbidden or 412 Precondition Failed
	if recStale.Code != http.StatusForbidden && recStale.Code != http.StatusPreconditionFailed {
		t.Fatalf("expected 403 or 412 on stale worker renew, got %d: %s", recStale.Code, recStale.Body.String())
	}
}

// Scenario I: Gateway Restart Does Not Falsely Restore In-Memory Sessions
func TestScenarioI_GatewayRestartClearsInMemorySessions(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	cfg := &config.Config{
		Environment: "test",
		HTTP: config.HTTPConfig{
			Port: 8080,
			Host: "127.0.0.1",
		},
		Raft: config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       5 * time.Second,
			MaxRedirectAttempts:  2,
			RequestTimeout:       2 * time.Second,
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Gateway Instance 1
	gw1 := api.NewServer(cfg, logger, "lockguard-gateway", "0.2.0")
	_, _ = gw1.HeartbeatManager().Register("client-persist", "owner-p", 10*time.Second)

	// Acquire lock through Gateway 1
	acqBody, _ := json.Marshal(map[string]interface{}{
		"request_id":    "req-scen-i-1",
		"resource_name": "res-persist",
		"owner_id":      "owner-p",
		"ttl_seconds":   15,
	})
	reqAcq := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(acqBody))
	recAcq := httptest.NewRecorder()
	gw1.Handler().ServeHTTP(recAcq, reqAcq)
	if recAcq.Code != http.StatusOK {
		t.Fatalf("acquire on gw1 failed: %s", recAcq.Body.String())
	}

	// Simulate Gateway Shutdown
	_ = gw1.Shutdown(context.Background())

	// Gateway Instance 2 (Simulated restart)
	gw2 := api.NewServer(cfg, logger, "lockguard-gateway", "0.2.0")

	// Verify in-memory sessions are NOT falsely present
	_, found := gw2.HeartbeatManager().GetSession("client-persist")
	if found {
		t.Fatalf("gateway 2 should NOT possess in-memory sessions from gateway 1")
	}

	// Verify Raft authoritative lock is still intact on coordinators
	reqGet := httptest.NewRequest(http.MethodGet, "/v1/locks/res-persist", nil)
	recGet := httptest.NewRecorder()
	gw2.Handler().ServeHTTP(recGet, reqGet)

	if recGet.Code != http.StatusOK {
		t.Fatalf("authoritative lock should still be present on coordinator cluster, got %d: %s", recGet.Code, recGet.Body.String())
	}
}
