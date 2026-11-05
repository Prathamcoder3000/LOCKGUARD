package coordinator_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"lockguard/backend/internal/api"
	"lockguard/backend/internal/consensus"
	"lockguard/backend/internal/coordinator"
	"lockguard/backend/internal/lockstate"
)

func TestCoordinatorHandler_AcquireRenewAndRead(t *testing.T) {
	servers := []raft.Server{
		{ID: "node-1", Address: "node-1", Suffrage: raft.Voter},
		{ID: "node-2", Address: "node-2", Suffrage: raft.Voter},
	}

	// Create 2 connected inmem nodes
	sm1 := lockstate.New()
	cfg1 := consensus.NodeConfig{
		NodeID:             "node-1",
		AdvertiseAddr:      "node-1",
		Inmem:              true,
		BootstrapCluster:   true,
		BootstrapServers:   servers,
		HeartbeatTimeout:   50 * time.Millisecond,
		ElectionTimeout:    100 * time.Millisecond,
		CommitTimeout:      10 * time.Millisecond,
		LeaderLeaseTimeout: 50 * time.Millisecond,
		LogLevel:           "error",
	}
	n1, err := consensus.NewRaftNode(cfg1, sm1)
	if err != nil {
		t.Fatalf("failed to create node-1: %v", err)
	}
	defer n1.Close()

	sm2 := lockstate.New()
	cfg2 := consensus.NodeConfig{
		NodeID:             "node-2",
		AdvertiseAddr:      "node-2",
		Inmem:              true,
		BootstrapCluster:   false,
		HeartbeatTimeout:   50 * time.Millisecond,
		ElectionTimeout:    100 * time.Millisecond,
		CommitTimeout:      10 * time.Millisecond,
		LeaderLeaseTimeout: 50 * time.Millisecond,
		LogLevel:           "error",
	}
	n2, err := consensus.NewRaftNode(cfg2, sm2)
	if err != nil {
		t.Fatalf("failed to create node-2: %v", err)
	}
	defer n2.Close()

	n1.InmemTransport().Connect("node-2", n2.InmemTransport())
	n2.InmemTransport().Connect("node-1", n1.InmemTransport())

	// Wait for leader
	deadline := time.Now().Add(3 * time.Second)
	var leaderNode, followerNode *consensus.RaftNode
	var leaderID, followerID string
	for time.Now().Before(deadline) {
		if n1.IsLeader() {
			leaderNode = n1
			leaderID = "node-1"
			followerNode = n2
			followerID = "node-2"
			break
		} else if n2.IsLeader() {
			leaderNode = n2
			leaderID = "node-2"
			followerNode = n1
			followerID = "node-1"
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if leaderNode == nil {
		t.Fatalf("no leader elected")
	}

	hLeader := coordinator.NewHandler(leaderNode, leaderID)
	hFollower := coordinator.NewHandler(followerNode, followerID)

	muxLeader := http.NewServeMux()
	hLeader.RegisterRoutes(muxLeader)

	muxFollower := http.NewServeMux()
	hFollower.RegisterRoutes(muxFollower)

	// 1. Submit Acquire to Follower -> Expect 421 Misdirected Request + Leader hint headers
	acquireBody := map[string]interface{}{
		"request_id":    "req-http-1",
		"resource_name": "http-resource",
		"owner_id":      "client-1",
		"ttl_seconds":   10,
	}
	bodyBytes, _ := json.Marshal(acquireBody)

	reqFollower := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(bodyBytes))
	recFollower := httptest.NewRecorder()
	muxFollower.ServeHTTP(recFollower, reqFollower)

	if recFollower.Code != http.StatusMisdirectedRequest {
		t.Fatalf("expected status 421 Misdirected Request on follower, got %d: %s", recFollower.Code, recFollower.Body.String())
	}
	if recFollower.Header().Get("X-Raft-Leader-ID") != leaderID {
		t.Fatalf("expected X-Raft-Leader-ID header %s, got %s", leaderID, recFollower.Header().Get("X-Raft-Leader-ID"))
	}

	// 2. Submit Acquire to Leader -> Expect 200 OK
	reqLeader := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(bodyBytes))
	recLeader := httptest.NewRecorder()
	muxLeader.ServeHTTP(recLeader, reqLeader)

	if recLeader.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK on leader, got %d: %s", recLeader.Code, recLeader.Body.String())
	}

	var resp api.APIResponse
	if err := json.NewDecoder(recLeader.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode leader response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success true in response")
	}

	// 3. Read Lock via GET /v1/locks/http-resource on Leader
	reqGet := httptest.NewRequest(http.MethodGet, "/v1/locks/http-resource", nil)
	recGet := httptest.NewRecorder()
	muxLeader.ServeHTTP(recGet, reqGet)

	if recGet.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK on GET lock, got %d: %s", recGet.Code, recGet.Body.String())
	}

	// 4. Cluster status endpoint
	reqStatus := httptest.NewRequest(http.MethodGet, "/v1/cluster/status", nil)
	recStatus := httptest.NewRecorder()
	muxLeader.ServeHTTP(recStatus, reqStatus)

	if recStatus.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK on /v1/cluster/status, got %d", recStatus.Code)
	}
}
