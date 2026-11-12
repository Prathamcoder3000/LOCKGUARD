package consensus_test

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"lockguard/backend/internal/consensus"
	"lockguard/backend/internal/lockstate"
)

func setupRealThreeNodeTCPCluster(t *testing.T) (map[string]*consensus.RaftNode, map[string]*lockstate.MemoryStateMachine, func()) {
	t.Helper()
	tempBase := t.TempDir()

	addrs := make([]string, 3)
	for i := 0; i < 3; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("failed to allocate TCP port: %v", err)
		}
		addrs[i] = fmt.Sprintf("127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port)
		_ = ln.Close()
	}

	nodeIDs := []string{"coordinator-a", "coordinator-b", "coordinator-c"}
	servers := make([]raft.Server, 3)
	for i := 0; i < 3; i++ {
		servers[i] = raft.Server{
			ID:       raft.ServerID(nodeIDs[i]),
			Address:  raft.ServerAddress(addrs[i]),
			Suffrage: raft.Voter,
		}
	}

	nodes := make(map[string]*consensus.RaftNode, 3)
	sms := make(map[string]*lockstate.MemoryStateMachine, 3)
	for i, id := range nodeIDs {
		sm := lockstate.New()
		sms[id] = sm
		cfg := consensus.NodeConfig{
			NodeID:             id,
			BindAddr:           addrs[i],
			AdvertiseAddr:      addrs[i],
			DataDir:            filepath.Join(tempBase, id),
			Inmem:              false,
			BootstrapCluster:   i == 0,
			BootstrapServers:   servers,
			HeartbeatTimeout:   50 * time.Millisecond,
			ElectionTimeout:    100 * time.Millisecond,
			CommitTimeout:      10 * time.Millisecond,
			LeaderLeaseTimeout: 50 * time.Millisecond,
			LogLevel:           "error",
		}
		node, err := consensus.NewRaftNode(cfg, sm)
		if err != nil {
			for _, n := range nodes {
				_ = n.Close()
			}
			t.Fatalf("failed to create TCP RaftNode %s: %v", id, err)
		}
		nodes[id] = node
	}

	cleanup := func() {
		for _, n := range nodes {
			_ = n.Close()
		}
	}
	return nodes, sms, cleanup
}

func waitLeaderExcluding(t *testing.T, nodes map[string]*consensus.RaftNode, excludeID string, timeout time.Duration) (string, *consensus.RaftNode) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for id, n := range nodes {
			if id != excludeID && n.IsLeader() {
				if err := n.VerifyAuthoritativeRead(500 * time.Millisecond); err == nil {
					return id, n
				}
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for leader (excluding %q)", excludeID)
	return "", nil
}

func TestRaftNode_TCPPartitionIsolateHealAndSnapshotRestore(t *testing.T) {
	nodes, sms, cleanup := setupRealThreeNodeTCPCluster(t)
	defer cleanup()

	leaderID, leader := waitLeaderExcluding(t, nodes, "", 6*time.Second)

	// 1. Acquire initial lock and create snapshot S1
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	baseNow := time.Now().UnixNano()
	res1, err := leader.ApplyCommand(ctx, lockstate.AcquireLockCommand{
		RequestID:         "req-snap-before",
		ResourceName:      "res-snapshot-target",
		OwnerID:           "owner-1",
		TTLNanoseconds:    (30 * time.Second).Nanoseconds(),
		TimestampUnixNano: baseNow,
	})
	if err != nil || !res1.Success {
		t.Fatalf("failed to acquire initial lock: res=%+v err=%v", res1, err)
	}

	if err := leader.Snapshot(); err != nil {
		t.Fatalf("failed to trigger snapshot on leader %s: %v", leaderID, err)
	}

	snaps, err := leader.ListSnapshots()
	if err != nil || len(snaps) == 0 {
		t.Fatalf("expected >=1 listed snapshots on leader, got len=%d err=%v", len(snaps), err)
	}
	if !snaps[0].Valid || snaps[0].ResourcesCount != 1 {
		t.Fatalf("unexpected snapshot metadata: %+v", snaps[0])
	}
	snapID := snaps[0].ID

	// 2. Partition current leader via PartitionableStreamLayer -> triggers failover to another node
	if err := leader.SetPartitioned(true); err != nil {
		t.Fatalf("failed to isolate leader %s: %v", leaderID, err)
	}
	if !leader.IsPartitioned() {
		t.Fatalf("expected IsPartitioned() == true on isolated node")
	}

	newLeaderID, newLeader := waitLeaderExcluding(t, nodes, leaderID, 6*time.Second)
	if err := leader.VerifyAuthoritativeRead(300 * time.Millisecond); err == nil {
		t.Fatalf("isolated former leader %s must reject linearizable read", leaderID)
	}

	// Acquire a second lock on newLeader while former leader is isolated
	res2, err := newLeader.ApplyCommand(ctx, lockstate.AcquireLockCommand{
		RequestID:         "req-during-partition",
		ResourceName:      "res-only-after-snap",
		OwnerID:           "owner-2",
		TTLNanoseconds:    (30 * time.Second).Nanoseconds(),
		TimestampUnixNano: time.Now().UnixNano(),
	})
	if err != nil || !res2.Success {
		t.Fatalf("new leader %s should serve quorum writes while minority is partitioned: res=%+v err=%v", newLeaderID, res2, err)
	}

	// 3. Heal partitioned node and verify catch-up
	if err := leader.SetPartitioned(false); err != nil {
		t.Fatalf("failed to heal node %s: %v", leaderID, err)
	}
	if leader.IsPartitioned() {
		t.Fatalf("expected IsPartitioned() == false after healing")
	}

	// Wait for healed node to replicate res-only-after-snap
	deadline := time.Now().Add(5 * time.Second)
	synced := false
	for time.Now().Before(deadline) {
		if _, found := sms[leaderID].GetLock("res-only-after-snap"); found {
			synced = true
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if !synced {
		t.Fatalf("healed node %s did not catch up replicated entries", leaderID)
	}

	// 4. Reject corrupt snapshot payload cleanly
	meta, raw, _, err := leader.OpenSnapshotRaw(snapID)
	if err != nil {
		t.Fatalf("failed to open snapshot %s from original leader %s: %v", snapID, leaderID, err)
	}
	if err := newLeader.RestoreSnapshotBytes(meta, []byte(`{"bad_json"`), 2*time.Second); err == nil {
		t.Fatalf("expected corrupt snapshot bytes to be rejected")
	}
	_ = raw

	// 5. Restore valid snapshot S1 on active leader -> state rolls back cluster-wide to before res-only-after-snap
	curLeaderID, curLeader := waitLeaderExcluding(t, nodes, "", 4*time.Second)
	if err := curLeader.RestoreSnapshotBytes(meta, raw, 5*time.Second); err != nil {
		t.Fatalf("failed to restore valid snapshot on leader %s: %v", curLeaderID, err)
	}

	deadline = time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		allRestored := true
		for id, sm := range sms {
			_, hasInitial := sm.GetLock("res-snapshot-target")
			_, hasLater := sm.GetLock("res-only-after-snap")
			if !hasInitial || hasLater {
				allRestored = false
				t.Logf("waiting on %s restore sync (hasInitial=%v hasLater=%v)", id, hasInitial, hasLater)
				break
			}
		}
		if allRestored {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("cluster did not converge to restored snapshot state")
}
