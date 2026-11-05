package consensus_test

import (
	"context"
	"testing"
	"time"

	"lockguard/backend/internal/consensus"
	"lockguard/backend/internal/lockstate"
)

func waitForLeader(t *testing.T, node *consensus.RaftNode, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if node.IsLeader() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("node %s failed to become leader within %v", node.Stats()["state"], timeout)
}

func TestRaftNode_SingleNodeAcquireRenewRelease(t *testing.T) {
	cfg := consensus.NodeConfig{
		NodeID:             "node-test-1",
		Inmem:              true,
		BootstrapCluster:   true,
		HeartbeatTimeout:   50 * time.Millisecond,
		ElectionTimeout:    100 * time.Millisecond,
		CommitTimeout:      10 * time.Millisecond,
		LeaderLeaseTimeout: 50 * time.Millisecond,
		LogLevel:           "error",
	}

	sm := lockstate.New()
	node, err := consensus.NewRaftNode(cfg, sm)
	if err != nil {
		t.Fatalf("failed to create RaftNode: %v", err)
	}
	defer node.Close()

	waitForLeader(t, node, 3*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	baseTime := time.Now().UnixNano()

	// 1. Acquire Lock
	cmdAcquire := lockstate.AcquireLockCommand{
		RequestID:         "req-single-1",
		ResourceName:      "res-single-1",
		OwnerID:           "worker-1",
		TTLNanoseconds:    (5 * time.Second).Nanoseconds(),
		TimestampUnixNano: baseTime,
	}

	resAcquire, err := node.ApplyCommand(ctx, cmdAcquire)
	if err != nil {
		t.Fatalf("ApplyCommand Acquire failed: %v", err)
	}
	if !resAcquire.Success {
		t.Fatalf("Acquire failed: %s (%s)", resAcquire.Message, resAcquire.Code)
	}
	if resAcquire.FencingToken != 1 {
		t.Fatalf("expected fencing token 1, got %d", resAcquire.FencingToken)
	}
	if resAcquire.LeaseVersion != 1 {
		t.Fatalf("expected lease version 1, got %d", resAcquire.LeaseVersion)
	}

	// 2. Linearizable Read
	rec, found, err := node.GetLock("res-single-1", true)
	if err != nil {
		t.Fatalf("GetLock failed: %v", err)
	}
	if !found {
		t.Fatalf("expected lock to be found")
	}
	if rec.OwnerID != "worker-1" || rec.FencingToken != 1 || rec.LeaseVersion != 1 {
		t.Fatalf("unexpected lock record: %+v", rec)
	}

	// 3. Renew Lock
	cmdRenew := lockstate.RenewLockCommand{
		RequestID:         "req-single-2",
		ResourceName:      "res-single-1",
		OwnerID:           "worker-1",
		FencingToken:      resAcquire.FencingToken,
		TTLNanoseconds:    (10 * time.Second).Nanoseconds(),
		TimestampUnixNano: baseTime + 1000,
	}

	resRenew, err := node.ApplyCommand(ctx, cmdRenew)
	if err != nil {
		t.Fatalf("ApplyCommand Renew failed: %v", err)
	}
	if !resRenew.Success {
		t.Fatalf("Renew failed: %s (%s)", resRenew.Message, resRenew.Code)
	}
	if resRenew.LeaseVersion != 2 {
		t.Fatalf("expected lease version 2 after renewal, got %d", resRenew.LeaseVersion)
	}
	if resRenew.FencingToken != resAcquire.FencingToken {
		t.Fatalf("fencing token changed on renew: %d vs %d", resRenew.FencingToken, resAcquire.FencingToken)
	}

	// 4. Release Lock
	cmdRelease := lockstate.ReleaseLockCommand{
		RequestID:         "req-single-3",
		ResourceName:      "res-single-1",
		OwnerID:           "worker-1",
		FencingToken:      resAcquire.FencingToken,
		TimestampUnixNano: baseTime + 2000,
	}

	resRelease, err := node.ApplyCommand(ctx, cmdRelease)
	if err != nil {
		t.Fatalf("ApplyCommand Release failed: %v", err)
	}
	if !resRelease.Success {
		t.Fatalf("Release failed: %s", resRelease.Message)
	}

	// 5. Verify Released
	_, foundAfterRel, err := node.GetLock("res-single-1", true)
	if err != nil {
		t.Fatalf("GetLock after release failed: %v", err)
	}
	if foundAfterRel {
		t.Fatalf("expected lock to be unlocked after release")
	}
}

