package tests

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"lockguard/backend/internal/consensus"
	"lockguard/backend/internal/lockstate"
)

// BenchmarkRecoveryMetrics benchmarks distributed system recovery performance under controlled conditions.
func BenchmarkRecoveryMetrics(b *testing.B) {
	b.ReportAllocs()

	// 1. Snapshot Creation Benchmark
	b.Run("Snapshot_Creation", func(b *testing.B) {
		sm := lockstate.New()
		for i := 0; i < 500; i++ {
			sm.Apply(lockstate.AcquireLockCommand{
				RequestID:         "bench-req",
				ResourceName:      "bench-resource",
				OwnerID:           "bench-worker",
				TTLNanoseconds:    10 * int64(time.Second),
				TimestampUnixNano: time.Now().UnixNano(),
			})
		}
		fsm := consensus.NewLockFSM(sm, nil)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			snap, err := fsm.Snapshot()
			if err != nil {
				b.Fatalf("snapshot failed: %v", err)
			}
			sink := &mockSnapshotSink{Buffer: &bytes.Buffer{}}
			_ = snap.Persist(sink)
		}
	})

	// 2. Snapshot Restoration Benchmark
	b.Run("Snapshot_Restoration", func(b *testing.B) {
		sm := lockstate.New()
		for i := 0; i < 500; i++ {
			sm.Apply(lockstate.AcquireLockCommand{
				RequestID:         "bench-req",
				ResourceName:      "bench-resource",
				OwnerID:           "bench-worker",
				TTLNanoseconds:    10 * int64(time.Second),
				TimestampUnixNano: time.Now().UnixNano(),
			})
		}
		fsm := consensus.NewLockFSM(sm, nil)
		snap, _ := fsm.Snapshot()
		var buf bytes.Buffer
		_ = snap.Persist(&mockSnapshotSink{Buffer: &buf})
		snapBytes := buf.Bytes()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			freshSM := lockstate.New()
			freshFSM := consensus.NewLockFSM(freshSM, nil)
			rc := io.NopCloser(bytes.NewReader(snapBytes))
			if err := freshFSM.Restore(rc); err != nil {
				b.Fatalf("restore failed: %v", err)
			}
		}
	})
}

// TestPhase4_Benchmark_RecoveryTimings executes single-pass latency measurements of key recovery operations
// and logs the exact durations for documentation in the Phase 4 report.
func TestPhase4_Benchmark_RecoveryTimings(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	// 1. Measure Leader Failure Detection & Replacement Time
	var leaderIdx int = -1
	for i, n := range tc.nodes {
		if n.IsLeader() {
			leaderIdx = i
			break
		}
	}

	startFailover := time.Now()
	tc.servers[leaderIdx].Close()
	tc.nodes[leaderIdx].InmemTransport().DisconnectAll()
	_ = tc.nodes[leaderIdx].Close()

	deadline := time.Now().Add(5 * time.Second)
	var newLeader *consensus.RaftNode
	for time.Now().Before(deadline) {
		for i, n := range tc.nodes {
			if i != leaderIdx && n.IsLeader() {
				newLeader = n
				break
			}
		}
		if newLeader != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	electionDuration := time.Since(startFailover)
	if newLeader == nil {
		t.Fatalf("failover election timed out")
	}
	t.Logf("[BENCHMARK] Leader Failure Detection & Election Duration: %v (%d ms)",
		electionDuration, electionDuration.Milliseconds())

	// 2. Measure Post-Failover First Mutation Commitment Time
	startMutation := time.Now()
	res, err := newLeader.ApplyCommand(context.Background(), lockstate.AcquireLockCommand{
		RequestID:         "bench-post-failover",
		ResourceName:      "bench-failover-res",
		OwnerID:           "bench-worker",
		TTLNanoseconds:    10 * int64(time.Second),
		TimestampUnixNano: time.Now().UnixNano(),
	})
	mutationDuration := time.Since(startMutation)
	if err != nil || !res.Success {
		t.Fatalf("post-failover mutation failed: %v", err)
	}
	t.Logf("[BENCHMARK] Post-Failover First Mutation Latency: %v (%d ms)",
		mutationDuration, mutationDuration.Milliseconds())

	// 3. Measure Snapshot Creation and Restoration Timings
	sm := lockstate.New()
	for i := 0; i < 1000; i++ {
		sm.Apply(lockstate.AcquireLockCommand{
			RequestID:         "bench-bulk",
			ResourceName:      "bulk-res",
			OwnerID:           "worker-bulk",
			TTLNanoseconds:    10 * int64(time.Second),
			TimestampUnixNano: time.Now().UnixNano(),
		})
	}
	fsm := consensus.NewLockFSM(sm, nil)

	startSnap := time.Now()
	snap, err := fsm.Snapshot()
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	var snapBuf bytes.Buffer
	_ = snap.Persist(&mockSnapshotSink{Buffer: &snapBuf})
	snapDuration := time.Since(startSnap)
	t.Logf("[BENCHMARK] Snapshot Creation (1,000 items, %d bytes): %v (%d µs)",
		snapBuf.Len(), snapDuration, snapDuration.Microseconds())

	startRestore := time.Now()
	freshSM := lockstate.New()
	freshFSM := consensus.NewLockFSM(freshSM, nil)
	rc := io.NopCloser(bytes.NewReader(snapBuf.Bytes()))
	if err := freshFSM.Restore(rc); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	restoreDuration := time.Since(startRestore)
	t.Logf("[BENCHMARK] Snapshot Restoration (1,000 items): %v (%d µs)",
		restoreDuration, restoreDuration.Microseconds())

	// 4. Measure Follower Catch-Up Time across inmem transport
	follower := tc.nodes[(leaderIdx+1)%3]
	if follower == newLeader {
		follower = tc.nodes[(leaderIdx+2)%3]
	}
	fAddr := raft.ServerAddress(follower.InmemTransport().LocalAddr())

	// Temporarily disconnect follower
	follower.InmemTransport().DisconnectAll()
	newLeader.InmemTransport().Disconnect(fAddr)

	// Apply 5 mutations on new leader while follower is disconnected
	for i := 0; i < 5; i++ {
		_, _ = newLeader.ApplyCommand(context.Background(), lockstate.AcquireLockCommand{
			RequestID:         "bench-catchup",
			ResourceName:      "bench-catchup-res",
			OwnerID:           "worker-catchup",
			TTLNanoseconds:    10 * int64(time.Second),
			TimestampUnixNano: time.Now().UnixNano(),
		})
	}

	// Reconnect and measure catch-up time
	startCatchup := time.Now()
	follower.InmemTransport().Connect(raft.ServerAddress(newLeader.InmemTransport().LocalAddr()), newLeader.InmemTransport())
	newLeader.InmemTransport().Connect(fAddr, follower.InmemTransport())

	deadlineCatchup := time.Now().Add(3 * time.Second)
	caughtUp := false
	for time.Now().Before(deadlineCatchup) {
		rec, found := follower.StateMachine().GetLock("bench-catchup-res")
		if found && rec.OwnerID == "worker-catchup" {
			caughtUp = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	catchupDuration := time.Since(startCatchup)
	if !caughtUp {
		t.Fatalf("follower catch-up timed out")
	}
	t.Logf("[BENCHMARK] Follower Catch-Up Duration (5 entries): %v (%d ms)",
		catchupDuration, catchupDuration.Milliseconds())
}

