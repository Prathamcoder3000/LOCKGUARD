package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"lockguard/backend/internal/lockstate"
)

// Scenario 1: Leader Crash and Failover Recovery
// Verifies that when the active Raft leader coordinator crashes:
// 1. Majority quorum detects the failure and elects a new leader.
// 2. Previously committed lock state and fencing tokens are preserved.
// 3. The API Gateway rediscovers the new leader and successfully executes subsequent lock mutations.
func TestPhase4_Scenario1_LeaderCrashAndFailoverRecovery(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	cfg := &config.Config{
		Environment: "test",
		HTTP: config.HTTPConfig{Port: 8080, Host: "127.0.0.1"},
		Raft: config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       50 * time.Millisecond,
			MaxRedirectAttempts:  3,
			RequestTimeout:       2 * time.Second,
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gwServer := api.NewServer(cfg, logger, "lockguard-gateway", "0.4.0")
	handler := gwServer.Handler()

	// 1. Identify current leader
	var leaderIdx int = -1
	for i, n := range tc.nodes {
		if n.IsLeader() {
			leaderIdx = i
			break
		}
	}
	if leaderIdx == -1 {
		t.Fatalf("no initial leader found in cluster")
	}
	leaderNode := tc.nodes[leaderIdx]
	leaderServer := tc.servers[leaderIdx]
	t.Logf("Initial leader is node %d (address %s)", leaderIdx, leaderNode.InmemTransport().LocalAddr())

	// 2. Acquire lock through Gateway on initial leader
	acqBody, _ := json.Marshal(map[string]interface{}{
		"request_id":    "req-p4-scen1-acq",
		"resource_name": "critical-table",
		"owner_id":      "worker-1",
		"ttl_seconds":   20,
	})
	reqAcq := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(acqBody))
	recAcq := httptest.NewRecorder()
	handler.ServeHTTP(recAcq, reqAcq)

	if recAcq.Code != http.StatusOK {
		t.Fatalf("initial lock acquire failed: code=%d body=%s", recAcq.Code, recAcq.Body.String())
	}

	var acqResp struct {
		Success bool `json:"success"`
		Data    struct {
			FencingToken uint64 `json:"fencing_token"`
			LeaseVersion uint64 `json:"lease_version"`
		} `json:"data"`
	}
	_ = json.Unmarshal(recAcq.Body.Bytes(), &acqResp)
	initialToken := acqResp.Data.FencingToken
	if initialToken == 0 || acqResp.Data.LeaseVersion != 1 {
		t.Fatalf("unexpected initial lock acquire data: %+v", acqResp.Data)
	}

	// 3. Crash the active leader
	t.Logf("Crashing leader node %d...", leaderIdx)
	leaderServer.Close()
	leaderNode.InmemTransport().DisconnectAll()
	_ = leaderNode.Close()
	gwServer.Discovery().InvalidateLeader()

	// 4. Wait for remaining 2 nodes (majority quorum 2/3) to elect a new leader
	deadline := time.Now().Add(5 * time.Second)
	var newLeaderIdx int = -1
	for time.Now().Before(deadline) {
		for i, n := range tc.nodes {
			if i != leaderIdx && n.IsLeader() {
				newLeaderIdx = i
				break
			}
		}
		if newLeaderIdx != -1 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	if newLeaderIdx == -1 {
		t.Fatalf("majority cluster failed to elect a replacement leader after leader crash")
	}
	t.Logf("New leader elected: node %d", newLeaderIdx)

	// 5. Verify previously committed lock state is preserved on the new leader
	reqRead := httptest.NewRequest(http.MethodGet, "/v1/locks/critical-table", nil)
	recRead := httptest.NewRecorder()
	handler.ServeHTTP(recRead, reqRead)

	if recRead.Code != http.StatusOK {
		t.Fatalf("failed to read lock state through new leader: code=%d body=%s", recRead.Code, recRead.Body.String())
	}

	var readResp struct {
		Success bool                `json:"success"`
		Data    lockstate.LockRecord `json:"data"`
	}
	_ = json.Unmarshal(recRead.Body.Bytes(), &readResp)
	if readResp.Data.OwnerID != "worker-1" || readResp.Data.FencingToken != initialToken {
		t.Fatalf("committed state lost or corrupted on new leader: %+v", readResp.Data)
	}

	// 6. Renew lock through the new leader: verify fencing token preserved and lease version incremented to 2
	renewBody, _ := json.Marshal(map[string]interface{}{
		"request_id":             "req-p4-scen1-renew",
		"resource_name":          "critical-table",
		"owner_id":               "worker-1",
		"fencing_token":          initialToken,
		"expected_lease_version": 1,
		"ttl_seconds":            20,
	})
	reqRenew := httptest.NewRequest(http.MethodPost, "/v1/locks/renew", bytes.NewReader(renewBody))
	recRenew := httptest.NewRecorder()
	handler.ServeHTTP(recRenew, reqRenew)

	if recRenew.Code != http.StatusOK {
		t.Fatalf("renewal on new leader failed: code=%d body=%s", recRenew.Code, recRenew.Body.String())
	}

	var renewResp struct {
		Success bool `json:"success"`
		Data    struct {
			FencingToken uint64 `json:"fencing_token"`
			LeaseVersion uint64 `json:"lease_version"`
		} `json:"data"`
	}
	_ = json.Unmarshal(recRenew.Body.Bytes(), &renewResp)
	if renewResp.Data.FencingToken != initialToken {
		t.Errorf("fencing token altered on failover renewal: expected %d, got %d", initialToken, renewResp.Data.FencingToken)
	}
	if renewResp.Data.LeaseVersion != 2 {
		t.Errorf("expected lease version 2 after renewal, got %d", renewResp.Data.LeaseVersion)
	}
}

// Scenario 2: Follower Crash and Recovery Catch-Up
// Verifies that a 3-node cluster survives the crash of one follower, continues normal
// mutations on majority quorum (2/3), and upon follower reconnection, the follower catches up.
func TestPhase4_Scenario2_FollowerCrashAndCatchUp(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	// 1. Identify a follower node
	var followerIdx int = -1
	var leaderIdx int = -1
	for i, n := range tc.nodes {
		if n.IsLeader() {
			leaderIdx = i
		} else if followerIdx == -1 {
			followerIdx = i
		}
	}

	followerNode := tc.nodes[followerIdx]
	followerAddr := raft.ServerAddress(tc.nodes[followerIdx].InmemTransport().LocalAddr())
	t.Logf("Crashing follower node %d (%s)...", followerIdx, followerAddr)

	// 2. Disconnect follower
	followerNode.InmemTransport().DisconnectAll()
	for i, n := range tc.nodes {
		if i != followerIdx {
			n.InmemTransport().Disconnect(followerAddr)
		}
	}

	// 3. Execute mutations on the 2 remaining nodes (majority quorum 2/3)
	leaderNode := tc.nodes[leaderIdx]
	ctx := context.Background()

	cmd1 := lockstate.AcquireLockCommand{
		RequestID:         "req-follower-fail-1",
		ResourceName:      "res-survive",
		OwnerID:           "worker-follower-test",
		TTLNanoseconds:    10 * int64(time.Second),
		TimestampUnixNano: time.Now().UnixNano(),
	}
	res1, err := leaderNode.ApplyCommand(ctx, cmd1)
	if err != nil || !res1.Success {
		t.Fatalf("majority failed to commit lock acquire with 1 follower down: err=%v, res=%+v", err, res1)
	}

	// 4. Reconnect the follower
	t.Logf("Reconnecting follower node %d...", followerIdx)
	for i, n := range tc.nodes {
		if i != followerIdx {
			peerAddr := raft.ServerAddress(n.InmemTransport().LocalAddr())
			followerNode.InmemTransport().Connect(peerAddr, n.InmemTransport())
			n.InmemTransport().Connect(followerAddr, followerNode.InmemTransport())
		}
	}

	// 5. Wait for follower catch-up
	deadline := time.Now().Add(4 * time.Second)
	caughtUp := false
	for time.Now().Before(deadline) {
		rec, found := followerNode.StateMachine().GetLock("res-survive")
		if found && rec.OwnerID == "worker-follower-test" {
			caughtUp = true
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	if !caughtUp {
		t.Fatalf("reconnected follower failed to replicate state from leader")
	}
	t.Log("Follower successfully re-synchronized with majority Raft log")

	// 6. Invariant assertion: Verify that an authoritative read on the follower FAILS CLOSED
	// Follower-local state inspection proves replication catch-up, but followers cannot serve
	// linearizable reads because linearizable reads require leader verification with quorum.
	_, _, errLinearizable := followerNode.GetLock("res-survive", true)
	if errLinearizable == nil {
		t.Fatalf("expected authoritative GetLock(linearizable=true) on follower to fail closed with NotLeaderError")
	}
	var notLeaderErr *consensus.NotLeaderError
	if !errors.As(errLinearizable, &notLeaderErr) {
		t.Fatalf("expected NotLeaderError on follower authoritative read, got: %v", errLinearizable)
	}
	t.Log("Confirmed authoritative read on follower correctly fails closed with NotLeaderError")
}

// Scenario 3: Two-Node Failure (Quorum Loss - Fail Closed)
// Verifies that when 2 of 3 nodes are unavailable:
// 1. The minority node CANNOT commit mutations (fails closed).
// 2. The Gateway reports failure.
// 3. Restoring the nodes restores quorum and normal locking resumes.
func TestPhase4_Scenario3_TwoNodeFailure_QuorumLoss_FailClosed(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	// 1. Identify leader and 2 followers
	var leaderIdx int = -1
	var followers []int
	for i, n := range tc.nodes {
		if n.IsLeader() {
			leaderIdx = i
		} else {
			followers = append(followers, i)
		}
	}

	leaderNode := tc.nodes[leaderIdx]
	f1Node := tc.nodes[followers[0]]
	f2Node := tc.nodes[followers[1]]

	f1Addr := raft.ServerAddress(f1Node.InmemTransport().LocalAddr())
	f2Addr := raft.ServerAddress(f2Node.InmemTransport().LocalAddr())
	lAddr := raft.ServerAddress(leaderNode.InmemTransport().LocalAddr())

	// 2. Disconnect BOTH followers -> isolates the leader into minority (1/3)
	t.Log("Disconnecting both followers to induce total quorum loss...")
	f1Node.InmemTransport().DisconnectAll()
	f2Node.InmemTransport().DisconnectAll()
	leaderNode.InmemTransport().Disconnect(f1Addr)
	leaderNode.InmemTransport().Disconnect(f2Addr)

	// 3. Attempt lock mutation on isolated leader: MUST FAIL CLOSED
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	cmd := lockstate.AcquireLockCommand{
		RequestID:         "req-quorum-lost",
		ResourceName:      "res-quorum-lost",
		OwnerID:           "worker-ghost",
		TTLNanoseconds:    10 * int64(time.Second),
		TimestampUnixNano: time.Now().UnixNano(),
	}
	_, errApply := leaderNode.ApplyCommand(ctx, cmd)
	if errApply == nil {
		t.Fatalf("CRITICAL SAFETY VIOLATION: isolated node committed lock mutation without majority quorum!")
	}
	t.Logf("Mutation safely rejected during quorum loss: %v", errApply)

	// 4. Restore connectivity between all 3 nodes
	t.Log("Restoring cluster connectivity...")
	leaderNode.InmemTransport().Connect(f1Addr, f1Node.InmemTransport())
	leaderNode.InmemTransport().Connect(f2Addr, f2Node.InmemTransport())
	f1Node.InmemTransport().Connect(lAddr, leaderNode.InmemTransport())
	f1Node.InmemTransport().Connect(f2Addr, f2Node.InmemTransport())
	f2Node.InmemTransport().Connect(lAddr, leaderNode.InmemTransport())
	f2Node.InmemTransport().Connect(f1Addr, f1Node.InmemTransport())

	// 5. Wait for cluster quorum to reform and a leader to establish
	deadline := time.Now().Add(4 * time.Second)
	var activeLeader *consensus.RaftNode
	for time.Now().Before(deadline) {
		for _, n := range tc.nodes {
			if n.IsLeader() {
				activeLeader = n
				break
			}
		}
		if activeLeader != nil {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	if activeLeader == nil {
		t.Fatalf("cluster failed to re-establish leader after restoring quorum")
	}

	// 6. Normal locking resumes after quorum restoration
	ctxRes := context.Background()
	cmdRes := lockstate.AcquireLockCommand{
		RequestID:         "req-quorum-restored",
		ResourceName:      "res-quorum-restored",
		OwnerID:           "worker-alive",
		TTLNanoseconds:    10 * int64(time.Second),
		TimestampUnixNano: time.Now().UnixNano(),
	}
	res2, errRes := activeLeader.ApplyCommand(ctxRes, cmdRes)
	if errRes != nil || !res2.Success {
		t.Fatalf("failed to acquire lock after quorum restoration: err=%v, res=%+v", errRes, res2)
	}
	t.Log("Normal locking operations verified after quorum recovery")
}

// Scenario 4: Leader Isolation & Deposition
// Verifies that when the current leader is partitioned from followers:
// 1. Followers elect a new leader.
// 2. Old isolated leader cannot commit.
// 3. When partition heals, old leader recognizes higher term and steps down to follower.
func TestPhase4_Scenario4_LeaderIsolationAndDeposition(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	var oldLeaderIdx int = -1
	var followers []int
	for i, n := range tc.nodes {
		if n.IsLeader() {
			oldLeaderIdx = i
		} else {
			followers = append(followers, i)
		}
	}

	oldLeader := tc.nodes[oldLeaderIdx]
	f1 := tc.nodes[followers[0]]
	f2 := tc.nodes[followers[1]]

	oldLeaderAddr := raft.ServerAddress(oldLeader.InmemTransport().LocalAddr())
	f1Addr := raft.ServerAddress(f1.InmemTransport().LocalAddr())
	f2Addr := raft.ServerAddress(f2.InmemTransport().LocalAddr())

	// 1. Isolate the leader from both followers
	t.Logf("Isolating current leader node %d...", oldLeaderIdx)
	oldLeader.InmemTransport().DisconnectAll()
	f1.InmemTransport().Disconnect(oldLeaderAddr)
	f2.InmemTransport().Disconnect(oldLeaderAddr)
	// Ensure f1 and f2 remain connected to each other
	f1.InmemTransport().Connect(f2Addr, f2.InmemTransport())
	f2.InmemTransport().Connect(f1Addr, f1.InmemTransport())

	// 2. Majority partition {f1, f2} must elect a new leader
	deadline := time.Now().Add(5 * time.Second)
	var newLeader *consensus.RaftNode
	for time.Now().Before(deadline) {
		if f1.IsLeader() {
			newLeader = f1
			break
		}
		if f2.IsLeader() {
			newLeader = f2
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	if newLeader == nil {
		t.Fatalf("majority partition {f1, f2} failed to elect a replacement leader")
	}
	t.Log("New leader elected in majority partition")

	// 3. New leader commits a mutation
	cmdNew := lockstate.AcquireLockCommand{
		RequestID:         "req-new-epoch",
		ResourceName:      "shared-counter",
		OwnerID:           "worker-new-epoch",
		TTLNanoseconds:    10 * int64(time.Second),
		TimestampUnixNano: time.Now().UnixNano(),
	}
	resNew, errNew := newLeader.ApplyCommand(context.Background(), cmdNew)
	if errNew != nil || !resNew.Success {
		t.Fatalf("new leader failed to commit: err=%v, res=%+v", errNew, resNew)
	}

	// 4. Old isolated leader MUST FAIL to commit
	ctxFail, cancelFail := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancelFail()
	cmdStale := lockstate.AcquireLockCommand{
		RequestID:         "req-stale-attempt",
		ResourceName:      "shared-counter",
		OwnerID:           "worker-stale-rogue",
		TTLNanoseconds:    10 * int64(time.Second),
		TimestampUnixNano: time.Now().UnixNano(),
	}
	_, errStale := oldLeader.ApplyCommand(ctxFail, cmdStale)
	if errStale == nil {
		t.Fatalf("CRITICAL: isolated former leader committed mutation without quorum!")
	}
	t.Logf("Isolated former leader safely rejected mutation: %v", errStale)

	// 5. Heal partition
	t.Log("Healing partition...")
	oldLeader.InmemTransport().Connect(f1Addr, f1.InmemTransport())
	oldLeader.InmemTransport().Connect(f2Addr, f2.InmemTransport())
	f1.InmemTransport().Connect(oldLeaderAddr, oldLeader.InmemTransport())
	f2.InmemTransport().Connect(oldLeaderAddr, oldLeader.InmemTransport())

	// 6. Old leader must step down and replicate state from new leader
	deadlineCatchUp := time.Now().Add(4 * time.Second)
	steppedDownAndReplicated := false
	for time.Now().Before(deadlineCatchUp) {
		rec, found := oldLeader.StateMachine().GetLock("shared-counter")
		if found && rec.OwnerID == "worker-new-epoch" && !oldLeader.IsLeader() {
			steppedDownAndReplicated = true
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	if !steppedDownAndReplicated {
		t.Fatalf("deposed leader failed to step down or synchronize state after healing")
	}
	t.Log("Former leader safely stepped down to follower and converged to authoritative state")
}
