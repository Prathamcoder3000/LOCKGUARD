package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"lockguard/backend/internal/consensus"
	"lockguard/backend/internal/gateway"
	"lockguard/backend/internal/metrics"
)

type mockCoordState struct {
	mu          sync.Mutex
	leaderID    string
	reachable   map[string]bool
	partitioned map[string]bool
	snapshots   map[string][]consensus.SnapshotInfo
	restoreCalls int
}

func newMockClusterHarness(t *testing.T) (*mockCoordState, *DiagnosticsService, *metrics.Registry, func()) {
	t.Helper()
	state := &mockCoordState{
		leaderID: "coordinator-b",
		reachable: map[string]bool{
			"coordinator-a": true,
			"coordinator-b": true,
			"coordinator-c": true,
		},
		partitioned: make(map[string]bool),
		snapshots: map[string][]consensus.SnapshotInfo{
			"coordinator-a": {
				{ID: "2-10-1", Index: 10, Term: 2, Size: 256, ResourcesCount: 1, Valid: true},
			},
			"coordinator-b": {
				{ID: "2-10-1", Index: 10, Term: 2, Size: 256, ResourcesCount: 1, Valid: true},
			},
			"coordinator-c": {
				{ID: "2-10-1", Index: 10, Term: 2, Size: 256, ResourcesCount: 1, Valid: true},
			},
		},
	}

	makeServer := func(nodeID string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			state.mu.Lock()
			defer state.mu.Unlock()

			if !state.reachable[nodeID] {
				http.Error(w, "node down", http.StatusServiceUnavailable)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/v1/cluster/status":
				isLeader := state.leaderID == nodeID && !state.partitioned[nodeID]
				role := "Follower"
				if isLeader {
					role = "Leader"
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"success": true,
					"data": map[string]interface{}{
						"node_id":       nodeID,
						"state":         role,
						"is_leader":     isLeader,
						"leader_id":     state.leaderID,
						"partitioned":   state.partitioned[nodeID],
						"applied_index": 12,
						"stats": map[string]string{
							"last_snapshot_index": "10",
							"last_snapshot_term":  "2",
						},
					},
				})
			case "/v1/cluster/stepdown":
				if state.leaderID == "coordinator-b" {
					state.leaderID = "coordinator-a"
				} else {
					state.leaderID = "coordinator-b"
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]string{"node_id": nodeID}})
			case "/v1/locks":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]interface{}{"locks": []interface{}{}}})
			case "/v1/cluster/partition/inject":
				if state.partitioned[nodeID] {
					w.WriteHeader(http.StatusConflict)
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": map[string]string{"message": "already partitioned"}})
					return
				}
				state.partitioned[nodeID] = true
				if state.leaderID == nodeID {
					state.leaderID = "coordinator-a"
					if nodeID == "coordinator-a" {
						state.leaderID = "coordinator-b"
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]interface{}{"node_id": nodeID, "partitioned": true}})
			case "/v1/cluster/partition/heal":
				state.partitioned[nodeID] = false
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]interface{}{"node_id": nodeID, "partitioned": false}})
			case "/v1/cluster/snapshots":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"success": true,
					"data": map[string]interface{}{
						"node_id":   nodeID,
						"is_leader": state.leaderID == nodeID,
						"snapshots": state.snapshots[nodeID],
					},
				})
			case "/v1/cluster/snapshots/create":
				newSnap := consensus.SnapshotInfo{ID: "2-15-2", Index: 15, Term: 2, Size: 300, ResourcesCount: 2, Valid: true}
				state.snapshots[nodeID] = append([]consensus.SnapshotInfo{newSnap}, state.snapshots[nodeID]...)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"success": true,
					"data": map[string]interface{}{
						"node_id":            nodeID,
						"already_up_to_date": false,
						"latest":             newSnap,
						"snapshots":          state.snapshots[nodeID],
					},
				})
			case "/v1/cluster/snapshots/restore":
				var req struct {
					SnapshotID string `json:"snapshot_id"`
					Confirm    bool   `json:"confirm"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !req.Confirm {
					w.WriteHeader(http.StatusBadRequest)
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": map[string]string{"message": "confirmation required"}})
					return
				}
				var matched *consensus.SnapshotInfo
				for _, s := range state.snapshots[nodeID] {
					if s.ID == req.SnapshotID {
						cp := s
						matched = &cp
						break
					}
				}
				if matched == nil {
					w.WriteHeader(http.StatusBadRequest)
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": map[string]string{"message": "snapshot not found"}})
					return
				}
				state.restoreCalls++
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"success": true,
					"data": map[string]interface{}{
						"node_id":  nodeID,
						"restored": true,
						"snapshot": matched,
					},
				})
			default:
				http.NotFound(w, r)
			}
		}))
	}

	sA := makeServer("coordinator-a")
	sB := makeServer("coordinator-b")
	sC := makeServer("coordinator-c")

	reg := metrics.NewRegistry()
	disc := gateway.NewLeaderDiscovery([]string{sA.URL, sB.URL, sC.URL}, 200*time.Millisecond, nil, reg)
	diag := NewDiagnosticsService("gw-test", "lockguard-api", disc, nil, nil, nil)
	diag.SetMetricsRegistry(reg)
	diag.SetEnvironment("development")

	cleanup := func() {
		sA.Close()
		sB.Close()
		sC.Close()
	}
	return state, diag, reg, cleanup
}

func TestRecoveryTracker_LeaderChangesAndRecoveryDuration(t *testing.T) {
	state, diag, reg, cleanup := newMockClusterHarness(t)
	defer cleanup()

	ctx := context.Background()

	// Issue 1: Initial baseline + repeated polls must NOT increment leader_changes_total
	for i := 0; i < 5; i++ {
		rep := diag.CollectClusterReport(ctx)
		if rep.CurrentLeaderID != "coordinator-b" {
			t.Fatalf("expected leader coordinator-b, got %s", rep.CurrentLeaderID)
		}
	}
	if got := reg.GetCounterTotal("lockguard_leader_changes_total"); got != 0 {
		t.Fatalf("expected 0 leader changes on startup/repeated polls, got %d", got)
	}

	// Real leader transition coordinator-b -> coordinator-a increments ONCE, not per subsequent poll
	state.mu.Lock()
	state.leaderID = "coordinator-a"
	state.mu.Unlock()

	for i := 0; i < 4; i++ {
		_ = diag.CollectClusterReport(ctx)
	}
	if got := reg.GetCounterTotal("lockguard_leader_changes_total"); got != 1 {
		t.Fatalf("expected exactly 1 leader change for single transition, got %d", got)
	}

	// Issue 2: Stop follower coordinator-c, wait, recover -> recovery duration > 0 ms
	state.mu.Lock()
	state.reachable["coordinator-c"] = false
	state.mu.Unlock()
	_ = diag.CollectClusterReport(ctx)

	time.Sleep(45 * time.Millisecond)

	state.mu.Lock()
	state.reachable["coordinator-c"] = true
	state.mu.Unlock()
	_ = diag.CollectClusterReport(ctx)

	durMs, lbl := reg.GetLatestGauge("lockguard_recovery_duration_seconds")
	if durMs < 20 {
		t.Fatalf("expected non-zero recovery duration >=20ms after rejoin, got %d ms (%s)", durMs, lbl)
	}
}

func TestRecoveryService_ChaosPartitionSafetyAndSnapshotRestore(t *testing.T) {
	state, diag, reg, cleanup := newMockClusterHarness(t)
	defer cleanup()

	ctx := context.Background()
	h := NewHandler(diag)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// Issue 3: Reject chaos without confirm=true, then execute leader_stepdown & quorum_read_barrier
	if _, err := diag.RunChaosTest(ctx, "leader_stepdown", false); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("expected ErrConfirmationRequired without confirm, got %v", err)
	}

	res1, err := diag.RunChaosTest(ctx, "leader_stepdown", true)
	if err != nil || res1.Status != "PASSED" {
		t.Fatalf("expected leader_stepdown chaos test to pass, got res=%+v err=%v", res1, err)
	}
	if got := reg.GetCounterTotal("lockguard_leader_changes_total"); got != 1 {
		t.Fatalf("expected leader_changes_total=1 after leader_stepdown, got %d", got)
	}

	res2, err := diag.RunChaosTest(ctx, "quorum_read_barrier", true)
	if err != nil || res2.Status != "PASSED" {
		t.Fatalf("expected quorum_read_barrier chaos test to pass, got res=%+v err=%v", res2, err)
	}
	if got := reg.GetCounterTotal("lockguard_chaos_tests_total"); got != 2 {
		t.Fatalf("expected lockguard_chaos_tests_total=2, got %d", got)
	}

	// Issue 4: Partition safety & lifecycle
	if _, err := diag.InjectPartition(ctx, "coordinator-c", false, 30); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("expected ErrConfirmationRequired on unconfirmed partition, got %v", err)
	}

	partRes, err := diag.InjectPartition(ctx, "coordinator-c", true, 30)
	if err != nil || !partRes.Partitioned || !partRes.QuorumAvailable || partRes.AvailableNodes != 2 {
		t.Fatalf("expected safe single-node partition preserving 2/3 quorum, got res=%+v err=%v", partRes, err)
	}
	if got := reg.GetCounterTotal("lockguard_network_partitions_total"); got != 1 {
		t.Fatalf("expected lockguard_network_partitions_total=1, got %d", got)
	}

	// Attempting to partition a 2nd node while coordinator-c is partitioned must fail to protect quorum!
	if _, err := diag.InjectPartition(ctx, "coordinator-b", true, 30); !errors.Is(err, ErrConflictState) && !errors.Is(err, ErrQuorumSafetyViolation) {
		t.Fatalf("expected second simultaneous partition to be blocked by quorum safety, got %v", err)
	}

	healRes, err := diag.HealPartition(ctx, "coordinator-c")
	if err != nil || healRes.Partitioned || healRes.AvailableNodes != 3 {
		t.Fatalf("expected clean heal to 3/3 nodes, got res=%+v err=%v", healRes, err)
	}

	// Simulate external node outage leaving only 2/3 reachable -> InjectPartition must refuse to preserve quorum
	state.mu.Lock()
	state.reachable["coordinator-c"] = false
	state.mu.Unlock()
	if _, err := diag.InjectPartition(ctx, "coordinator-b", true, 30); !errors.Is(err, ErrQuorumSafetyViolation) {
		t.Fatalf("expected ErrQuorumSafetyViolation when only 2/3 nodes are healthy, got %v", err)
	}
	state.mu.Lock()
	state.reachable["coordinator-c"] = true
	state.mu.Unlock()

	// Issue 5: Snapshot restore requires confirm=true and updates counter ONLY on success
	if _, err := diag.RestoreClusterSnapshot(ctx, "2-10-1", false); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("expected ErrConfirmationRequired without confirm, got %v", err)
	}
	if _, err := diag.RestoreClusterSnapshot(ctx, "missing-snap-id", true); !errors.Is(err, ErrTargetNotFound) {
		t.Fatalf("expected ErrTargetNotFound for unknown snapshot, got %v", err)
	}
	if got := reg.GetCounterTotal("lockguard_snapshot_restore_total"); got != 0 {
		t.Fatalf("snapshot_restore_total must remain 0 after rejected/failed restores, got %d", got)
	}

	snapRes, err := diag.RestoreClusterSnapshot(ctx, "2-10-1", true)
	if err != nil || snapRes.Action != "RESTORED" {
		t.Fatalf("expected snapshot restore success, got res=%+v err=%v", snapRes, err)
	}
	if got := reg.GetCounterTotal("lockguard_snapshot_restore_total"); got != 1 {
		t.Fatalf("expected lockguard_snapshot_restore_total=1 after verified restore, got %d", got)
	}
}
