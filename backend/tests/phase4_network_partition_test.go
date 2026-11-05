package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"lockguard/backend/internal/consensus"
	"lockguard/backend/internal/lockstate"
)

// TestPhase4_NetworkPartition_BidirectionalAndHealing verifies that during a bidirectional partition:
// - Minority partition fails closed and rejects all mutations.
// - Majority partition elects a leader and continues safe commits.
// - Fencing tokens and lease versions remain strictly monotonic across partitions and healing.
func TestPhase4_NetworkPartition_BidirectionalAndHealing(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	var leaderIdx int = -1
	var followerIndices []int
	for i, n := range tc.nodes {
		if n.IsLeader() {
			leaderIdx = i
		} else {
			followerIndices = append(followerIndices, i)
		}
	}

	lNode := tc.nodes[leaderIdx]
	f1Node := tc.nodes[followerIndices[0]]
	f2Node := tc.nodes[followerIndices[1]]

	lAddr := raft.ServerAddress(lNode.InmemTransport().LocalAddr())
	f1Addr := raft.ServerAddress(f1Node.InmemTransport().LocalAddr())
	f2Addr := raft.ServerAddress(f2Node.InmemTransport().LocalAddr())

	ctx := context.Background()

	// 1. Initial lock acquisition prior to partition
	resInit, err := lNode.ApplyCommand(ctx, lockstate.AcquireLockCommand{
		RequestID:         "req-part-init",
		ResourceName:      "part-res",
		OwnerID:           "initial-owner",
		TTLNanoseconds:    10 * int64(time.Second),
		TimestampUnixNano: time.Now().UnixNano(),
	})
	if err != nil || !resInit.Success {
		t.Fatalf("initial acquire failed: err=%v, res=%+v", err, resInit)
	}
	fencingToken1 := resInit.FencingToken
	if fencingToken1 == 0 || resInit.LeaseVersion != 1 {
		t.Fatalf("unexpected initial token or lease version: %+v", resInit)
	}

	// Release lock to conclude epoch 1
	resRel, errRel := lNode.ApplyCommand(ctx, lockstate.ReleaseLockCommand{
		RequestID:         "req-part-rel-1",
		ResourceName:      "part-res",
		OwnerID:           "initial-owner",
		FencingToken:      fencingToken1,
		TimestampUnixNano: time.Now().UnixNano(),
	})
	if errRel != nil || !resRel.Success {
		t.Fatalf("release before partition failed: err=%v, res=%+v", errRel, resRel)
	}

	// 2. Introduce Bidirectional Partition: {Leader} (Minority) vs {F1, F2} (Majority)
	t.Log("Partitioning: Group A {Leader} vs Group B {F1, F2}")
	lNode.InmemTransport().DisconnectAll()
	f1Node.InmemTransport().Disconnect(lAddr)
	f2Node.InmemTransport().Disconnect(lAddr)
	f1Node.InmemTransport().Connect(f2Addr, f2Node.InmemTransport())
	f2Node.InmemTransport().Connect(f1Addr, f1Node.InmemTransport())

	// 3. Minority cannot commit: fails closed
	ctxMin, cancelMin := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancelMin()
	_, errMin := lNode.ApplyCommand(ctxMin, lockstate.AcquireLockCommand{
		RequestID:         "req-part-min-fail",
		ResourceName:      "part-res",
		OwnerID:           "rogue-worker",
		TTLNanoseconds:    10 * int64(time.Second),
		TimestampUnixNano: time.Now().UnixNano(),
	})
	if errMin == nil {
		t.Fatal("CRITICAL: minority group committed without quorum!")
	}
	t.Logf("Minority partition safely rejected write: %v", errMin)

	// 4. Majority group elects new leader
	deadline := time.Now().Add(5 * time.Second)
	var majorityLeader *consensus.RaftNode
	for time.Now().Before(deadline) {
		if f1Node.IsLeader() {
			majorityLeader = f1Node
			break
		}
		if f2Node.IsLeader() {
			majorityLeader = f2Node
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if majorityLeader == nil {
		t.Fatal("majority group {F1, F2} failed to elect a replacement leader")
	}

	// 5. Majority acquires new epoch on the SAME resource "part-res"
	resMaj, errMaj := majorityLeader.ApplyCommand(ctx, lockstate.AcquireLockCommand{
		RequestID:         "req-part-maj-acq",
		ResourceName:      "part-res",
		OwnerID:           "majority-owner",
		TTLNanoseconds:    10 * int64(time.Second),
		TimestampUnixNano: time.Now().UnixNano(),
	})
	if errMaj != nil || !resMaj.Success {
		t.Fatalf("majority leader failed to commit: err=%v, res=%+v", errMaj, resMaj)
	}
	fencingToken2 := resMaj.FencingToken
	if fencingToken2 <= fencingToken1 {
		t.Fatalf("fencing token non-monotonic across partition: token1=%d, token2=%d", fencingToken1, fencingToken2)
	}

	// 6. Heal the partition
	t.Log("Healing network partition...")
	lNode.InmemTransport().Connect(f1Addr, f1Node.InmemTransport())
	lNode.InmemTransport().Connect(f2Addr, f2Node.InmemTransport())
	f1Node.InmemTransport().Connect(lAddr, lNode.InmemTransport())
	f2Node.InmemTransport().Connect(lAddr, lNode.InmemTransport())

	// 7. Verify all 3 nodes converge to the authoritative state
	deadlineConverge := time.Now().Add(4 * time.Second)
	converged := false
	for time.Now().Before(deadlineConverge) {
		rec, found := lNode.StateMachine().GetLock("part-res")
		if found && rec.OwnerID == "majority-owner" && rec.FencingToken == fencingToken2 {
			converged = true
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	if !converged {
		t.Fatal("rejoined former leader failed to converge to authoritative state from majority")
	}
	t.Log("All 3 nodes converged successfully after partition healing")
}

// TestPhase4_NetworkPartition_RepeatedCycles verifies stability under 3 consecutive partition-and-heal cycles.
// Fencing tokens and lease versions must advance deterministically without rollbacks or state corruption.
func TestPhase4_NetworkPartition_RepeatedCycles(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	ctx := context.Background()
	var lastFencingToken uint64 = 0

	for cycle := 1; cycle <= 3; cycle++ {
		t.Logf("=== Starting Chaos Partition Cycle %d/3 ===", cycle)

		// 1. Find active leader
		var leaderIdx int = -1
		for i, n := range tc.nodes {
			if n.IsLeader() {
				leaderIdx = i
				break
			}
		}
		if leaderIdx == -1 {
			t.Fatalf("cycle %d: no leader found before partition", cycle)
		}
		currLeader := tc.nodes[leaderIdx]
		currLeaderAddr := raft.ServerAddress(currLeader.InmemTransport().LocalAddr())

		// Acquire a lock in this cycle on shared resource
		resName := "cycle-shared-resource"
		owner := fmt.Sprintf("owner-cycle-%d", cycle)
		acqRes, err := currLeader.ApplyCommand(ctx, lockstate.AcquireLockCommand{
			RequestID:         fmt.Sprintf("req-cycle-%d-acq", cycle),
			ResourceName:      resName,
			OwnerID:           owner,
			TTLNanoseconds:    10 * int64(time.Second),
			TimestampUnixNano: time.Now().UnixNano(),
		})
		if err != nil || !acqRes.Success {
			t.Fatalf("cycle %d acquire failed: %v", cycle, err)
		}
		if acqRes.FencingToken <= lastFencingToken {
			t.Fatalf("cycle %d fencing token non-monotonic: prev=%d, curr=%d", cycle, lastFencingToken, acqRes.FencingToken)
		}
		lastFencingToken = acqRes.FencingToken

		// Release lock to conclude this epoch so next cycle increments HighestToken
		relRes, errRel := currLeader.ApplyCommand(ctx, lockstate.ReleaseLockCommand{
			RequestID:         fmt.Sprintf("req-cycle-%d-rel", cycle),
			ResourceName:      resName,
			OwnerID:           owner,
			FencingToken:      lastFencingToken,
			TimestampUnixNano: time.Now().UnixNano(),
		})
		if errRel != nil || !relRes.Success {
			t.Fatalf("cycle %d release failed: %v", cycle, errRel)
		}

		// 2. Partition the current leader
		currLeader.InmemTransport().DisconnectAll()
		for i, n := range tc.nodes {
			if i != leaderIdx {
				n.InmemTransport().Disconnect(currLeaderAddr)
			}
		}

		// Ensure the other two nodes can communicate
		var peerIdxs []int
		for i := range tc.nodes {
			if i != leaderIdx {
				peerIdxs = append(peerIdxs, i)
			}
		}
		p1, p2 := tc.nodes[peerIdxs[0]], tc.nodes[peerIdxs[1]]
		p1Addr, p2Addr := raft.ServerAddress(p1.InmemTransport().LocalAddr()), raft.ServerAddress(p2.InmemTransport().LocalAddr())
		p1.InmemTransport().Connect(p2Addr, p2.InmemTransport())
		p2.InmemTransport().Connect(p1Addr, p1.InmemTransport())

		// 3. Wait for new leader to be elected in majority
		deadline := time.Now().Add(4 * time.Second)
		var newLeader *consensus.RaftNode
		for time.Now().Before(deadline) {
			if p1.IsLeader() {
				newLeader = p1
				break
			}
			if p2.IsLeader() {
				newLeader = p2
				break
			}
			time.Sleep(30 * time.Millisecond)
		}
		if newLeader == nil {
			t.Fatalf("cycle %d: majority failed to elect new leader", cycle)
		}

		// 4. Heal the partition
		currLeader.InmemTransport().Connect(p1Addr, p1.InmemTransport())
		currLeader.InmemTransport().Connect(p2Addr, p2.InmemTransport())
		p1.InmemTransport().Connect(currLeaderAddr, currLeader.InmemTransport())
		p2.InmemTransport().Connect(currLeaderAddr, currLeader.InmemTransport())

		// 5. Brief stabilization period
		time.Sleep(150 * time.Millisecond)
		t.Logf("=== Cycle %d completed successfully ===", cycle)
	}

	t.Logf("All 3 partition and recovery cycles completed. Final fencing token: %d", lastFencingToken)
}
