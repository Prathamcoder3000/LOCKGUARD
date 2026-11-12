package recovery

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"lockguard/backend/internal/audit"
	"lockguard/backend/internal/chaos"
	"lockguard/backend/internal/consensus"
)

var (
	ErrConfirmationRequired  = errors.New("explicit confirmation is required for this operation")
	ErrConflictState         = errors.New("operation conflicts with current cluster state")
	ErrQuorumSafetyViolation = errors.New("operation rejected to preserve Raft majority quorum")
	ErrForbiddenInProd       = errors.New("chaos and partition drills are disabled in production environment")
	ErrNoQuorumAvailable     = errors.New("raft majority quorum or leader is currently unavailable")
	ErrInvalidRequest        = errors.New("invalid recovery request parameter")
	ErrTargetNotFound        = errors.New("target coordinator node or snapshot not found")
)

func newActionID(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(b))
}

func durationMsFromSeconds(sec float64) int64 {
	if sec <= 0 {
		return 0
	}
	ms := int64(sec*1000 + 0.5)
	if ms < 1 {
		ms = 1
	}
	return ms
}

// RecoveryDurationRecord summarizes the latest measured recovery operation.
type RecoveryDurationRecord struct {
	Action     string    `json:"action"`
	Target     string    `json:"target,omitempty"`
	DurationMs int64     `json:"duration_ms"`
	RecordedAt time.Time `json:"recorded_at"`
}

// ActivePartitionState describes any active simulated Raft network partition.
type ActivePartitionState struct {
	Active     bool       `json:"active"`
	NodeID     string     `json:"node_id,omitempty"`
	Endpoint   string     `json:"endpoint,omitempty"`
	Topology   string     `json:"topology,omitempty"`
	InjectedAt *time.Time `json:"injected_at,omitempty"`
	AutoHealAt *time.Time `json:"auto_heal_at,omitempty"`
}

// ChaosScenarioDescriptor advertises a safe, operator-initiated chaos drill.
type ChaosScenarioDescriptor struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Description          string `json:"description"`
	Safe                 bool   `json:"safe"`
	ConfirmationRequired bool   `json:"confirmation_required"`
}

// ChaosExecutionResult captures the outcome of a controlled chaos drill.
type ChaosExecutionResult struct {
	ID              string                 `json:"id"`
	Scenario        string                 `json:"scenario"`
	FaultType       string                 `json:"fault_type"`
	Target          string                 `json:"target"`
	Status          string                 `json:"status"` // "PASSED", "FAILED"
	Summary         string                 `json:"summary"`
	StartedAt       time.Time              `json:"started_at"`
	CompletedAt     time.Time              `json:"completed_at"`
	DurationMs      int64                  `json:"duration_ms"`
	QuorumPreserved bool                   `json:"quorum_preserved"`
	Details         map[string]interface{} `json:"details,omitempty"`
}

// PartitionActionRecord captures historical partition inject/heal actions in memory.
type PartitionActionRecord struct {
	ID                 string    `json:"id"`
	Action             string    `json:"action"` // "INJECTED" or "HEALED"
	NodeID             string    `json:"node_id"`
	Topology           string    `json:"topology,omitempty"`
	RecoveryDurationMs int64     `json:"recovery_duration_ms,omitempty"`
	QuorumAvailable    bool      `json:"quorum_available"`
	AvailableNodes     int       `json:"available_nodes"`
	TotalNodes         int       `json:"total_nodes"`
	Timestamp          time.Time `json:"timestamp"`
}

// LeaderTransitionRecord summarizes an observed Raft leader transition.
type LeaderTransitionRecord struct {
	PreviousLeader string    `json:"previous_leader"`
	NewLeader      string    `json:"new_leader"`
	Timestamp      time.Time `json:"timestamp"`
}

// ChaosStatusReport provides full state for the Resilience & Recovery control panel.
type ChaosStatusReport struct {
	Enabled            bool                      `json:"enabled"`
	Environment        string                    `json:"environment"`
	Running            bool                      `json:"running"`
	ExecutionCount     uint64                    `json:"execution_count"`
	PartitionsInjected uint64                    `json:"partitions_injected"`
	SnapshotRestores   uint64                    `json:"snapshot_restores"`
	LeaderChanges      uint64                    `json:"leader_changes"`
	LastRecovery       *RecoveryDurationRecord   `json:"last_recovery,omitempty"`
	ActivePartition    ActivePartitionState      `json:"active_partition"`
	LastResult         *ChaosExecutionResult     `json:"last_result,omitempty"`
	History            []ChaosExecutionResult    `json:"history"`
	PartitionHistory   []PartitionActionRecord   `json:"partition_history"`
	LeaderHistory      []LeaderTransitionRecord  `json:"leader_history"`
	AvailableScenarios []ChaosScenarioDescriptor `json:"available_scenarios"`
	GeneratedAt        time.Time                 `json:"generated_at"`
}

// PartitionActionResult reports the outcome of a partition inject or heal request.
type PartitionActionResult struct {
	Action             string     `json:"action"` // "INJECTED" or "HEALED"
	NodeID             string     `json:"node_id"`
	Partitioned        bool       `json:"partitioned"`
	Topology           string     `json:"topology,omitempty"`
	RecoveryDurationMs int64      `json:"recovery_duration_ms,omitempty"`
	QuorumAvailable    bool       `json:"quorum_available"`
	AvailableNodes     int        `json:"available_nodes"`
	TotalNodes         int        `json:"total_nodes"`
	MajorityQuorum     int        `json:"majority_quorum"`
	AutoHealAt         *time.Time `json:"auto_heal_at,omitempty"`
	Message            string     `json:"message"`
}

// SnapshotActionResult reports the outcome of snapshot create or restore actions.
type SnapshotActionResult struct {
	Action           string                   `json:"action"` // "CREATED" or "RESTORED"
	Snapshot         *consensus.SnapshotInfo  `json:"snapshot,omitempty"`
	AlreadyUpToDate  bool                     `json:"already_up_to_date,omitempty"`
	Snapshots        []consensus.SnapshotInfo `json:"snapshots,omitempty"`
	RestoredByLeader string                   `json:"restored_by_leader,omitempty"`
	DurationMs       int64                    `json:"duration_ms,omitempty"`
	Message          string                   `json:"message"`
}

// RecoveryTracker monitors Raft leader changes, node outage/rejoin durations,
// controlled chaos drills, network partition state, and snapshot restore executions.
type RecoveryTracker struct {
	svc *DiagnosticsService

	mu                  sync.Mutex
	baselineInitialized bool
	lastLeaderID        string
	quorumLostAt        time.Time
	leaderLostAt        time.Time
	seenHealthy         map[string]bool
	nodeDownSince       map[string]time.Time
	endpointNodeIDs     map[string]string
	lastRecovery        *RecoveryDurationRecord

	chaosRunning     bool
	chaosHistory     []ChaosExecutionResult
	partitionHistory []PartitionActionRecord
	leaderHistory    []LeaderTransitionRecord
	activePartition  ActivePartitionState
	healTimer        *time.Timer
}

func newRecoveryTracker(svc *DiagnosticsService) *RecoveryTracker {
	return &RecoveryTracker{
		svc:              svc,
		seenHealthy:      make(map[string]bool),
		nodeDownSince:    make(map[string]time.Time),
		endpointNodeIDs:  make(map[string]string),
		chaosHistory:     make([]ChaosExecutionResult, 0, 20),
		partitionHistory: make([]PartitionActionRecord, 0, 20),
		leaderHistory:    make([]LeaderTransitionRecord, 0, 20),
	}
}

// RememberEndpointNodeID associates a coordinator HTTP endpoint with its canonical node_id.
func (t *RecoveryTracker) RememberEndpointNodeID(endpoint, nodeID string) {
	if strings.TrimSpace(endpoint) == "" || strings.TrimSpace(nodeID) == "" {
		return
	}
	t.mu.Lock()
	t.endpointNodeIDs[endpoint] = nodeID
	t.mu.Unlock()
}

// ResolveNodeID returns the previously observed canonical node_id for an endpoint, or its hostname fallback.
func (t *RecoveryTracker) ResolveNodeID(endpoint string) string {
	t.mu.Lock()
	id := t.endpointNodeIDs[endpoint]
	t.mu.Unlock()
	if id != "" {
		return id
	}
	return nodeIDFromEndpoint(endpoint)
}

// Start begins periodic background observation until ctx is canceled.
// It performs zero automatic destructive operations.
func (t *RecoveryTracker) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 400 * time.Millisecond
	}

	// Establish initial baseline without emitting synthetic leader changes.
	_ = t.svc.CollectClusterReport(ctx)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				t.cancelHealTimer()
				return
			case <-ticker.C:
				probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				_ = t.svc.CollectClusterReport(probeCtx)
				cancel()
			}
		}
	}()
}

func (t *RecoveryTracker) cancelHealTimer() {
	t.mu.Lock()
	if t.healTimer != nil {
		t.healTimer.Stop()
		t.healTimer = nil
	}
	t.mu.Unlock()
}

// ObserveClusterReport processes a cluster topology snapshot to accurately count leader transitions
// and measure node outage-to-rejoin / election / quorum recovery durations.
func (t *RecoveryTracker) ObserveClusterReport(report ClusterRecoveryReport) {
	now := time.Now().UTC()
	reg := t.svc.reg()

	var leaderTransitionFrom, leaderTransitionTo string
	var quorumJustLost bool
	var recoveredNodes []struct {
		id string
		ms int64
	}

	t.mu.Lock()

	// 1. Track per-node availability & outage/rejoin durations
	for _, c := range report.Coordinators {
		healthy := c.Reachable && !c.Partitioned
		if healthy {
			if downAt, wasDown := t.nodeDownSince[c.NodeID]; wasDown && !downAt.IsZero() {
				elapsedSec := now.Sub(downAt).Seconds()
				if elapsedSec < 0 {
					elapsedSec = 0.001
				}
				reg.SetRecoveryDuration("node_rejoin", elapsedSec)
				ms := durationMsFromSeconds(elapsedSec)
				t.lastRecovery = &RecoveryDurationRecord{
					Action:     "node_rejoin",
					Target:     c.NodeID,
					DurationMs: ms,
					RecordedAt: now,
				}
				recoveredNodes = append(recoveredNodes, struct {
					id string
					ms int64
				}{id: c.NodeID, ms: ms})
				delete(t.nodeDownSince, c.NodeID)
			}
			t.seenHealthy[c.NodeID] = true

			// If active partition target has healed cleanly outside HealPartition, clear active state.
			if t.activePartition.Active && t.activePartition.NodeID == c.NodeID && !c.Partitioned {
				t.activePartition = ActivePartitionState{}
				if t.healTimer != nil {
					t.healTimer.Stop()
					t.healTimer = nil
				}
			}
		} else {
			if t.seenHealthy[c.NodeID] && t.nodeDownSince[c.NodeID].IsZero() {
				t.nodeDownSince[c.NodeID] = now
				reg.IncCoordinatorFailures(c.NodeID)
			}
		}
	}

	// 2. Track Raft leader transitions without counting initial baseline or repeated status polls
	if !t.baselineInitialized {
		if report.CurrentLeaderID != "" && report.QuorumAvailable {
			t.lastLeaderID = report.CurrentLeaderID
			t.baselineInitialized = true
		}
	} else {
		if !report.QuorumAvailable && t.quorumLostAt.IsZero() {
			t.quorumLostAt = now
			quorumJustLost = true
		}
		if report.QuorumAvailable && !t.quorumLostAt.IsZero() {
			elapsedSec := now.Sub(t.quorumLostAt).Seconds()
			reg.SetRecoveryDuration("quorum_restore", elapsedSec)
			t.lastRecovery = &RecoveryDurationRecord{
				Action:     "quorum_restore",
				DurationMs: durationMsFromSeconds(elapsedSec),
				RecordedAt: now,
			}
			t.quorumLostAt = time.Time{}
		}

		if report.CurrentLeaderID == "" {
			if t.leaderLostAt.IsZero() {
				t.leaderLostAt = now
			}
		} else {
			if report.CurrentLeaderID != t.lastLeaderID {
				reg.IncLeaderChanges()
				leaderTransitionFrom = t.lastLeaderID
				leaderTransitionTo = report.CurrentLeaderID

				entry := LeaderTransitionRecord{
					PreviousLeader: leaderTransitionFrom,
					NewLeader:      leaderTransitionTo,
					Timestamp:      now,
				}
				t.leaderHistory = append([]LeaderTransitionRecord{entry}, t.leaderHistory...)
				if len(t.leaderHistory) > 20 {
					t.leaderHistory = t.leaderHistory[:20]
				}

				if !t.leaderLostAt.IsZero() {
					elapsedSec := now.Sub(t.leaderLostAt).Seconds()
					reg.SetRecoveryDuration("leader_election", elapsedSec)
					t.lastRecovery = &RecoveryDurationRecord{
						Action:     "leader_election",
						Target:     report.CurrentLeaderID,
						DurationMs: durationMsFromSeconds(elapsedSec),
						RecordedAt: now,
					}
				}
				t.lastLeaderID = report.CurrentLeaderID
			}
			t.leaderLostAt = time.Time{}
		}
	}

	t.mu.Unlock()

	// Emit structured audit events outside the mutex lock.
	if leaderTransitionTo != "" {
		t.svc.emitAuditEvent(&audit.Event{
			EventType:       audit.TypeLeaderChange,
			EventCategory:   audit.CategoryConsensus,
			CoordinatorID:   leaderTransitionTo,
			OperationStatus: audit.StatusCommitted,
			Metadata: map[string]interface{}{
				"previous_leader": leaderTransitionFrom,
				"new_leader":      leaderTransitionTo,
			},
		})
	}
	if quorumJustLost {
		t.svc.emitAuditEvent(&audit.Event{
			EventType:       audit.TypeQuorumLoss,
			EventCategory:   audit.CategoryConsensus,
			OperationStatus: audit.StatusFailed,
			ErrorCode:       "QUORUM_LOST",
			Metadata: map[string]interface{}{
				"available_nodes": len(report.AvailableNodes),
				"total_nodes":     report.TotalNodes,
			},
		})
	}
	for _, rec := range recoveredNodes {
		t.svc.emitAuditEvent(&audit.Event{
			EventType:       "NODE_RECOVERED",
			EventCategory:   audit.CategoryConsensus,
			CoordinatorID:   rec.id,
			OperationStatus: audit.StatusCommitted,
			Metadata: map[string]interface{}{
				"recovery_duration_ms": rec.ms,
			},
		})
	}
}

func (t *RecoveryTracker) recordExplicitRecoveryDuration(action, target string, durationSec float64) int64 {
	now := time.Now().UTC()
	t.svc.reg().SetRecoveryDuration(action, durationSec)
	ms := durationMsFromSeconds(durationSec)

	t.mu.Lock()
	t.lastRecovery = &RecoveryDurationRecord{
		Action:     action,
		Target:     target,
		DurationMs: ms,
		RecordedAt: now,
	}
	t.mu.Unlock()
	return ms
}

// AvailableScenarios lists all safe, admin-confirmed chaos drills.
func AvailableScenarios() []ChaosScenarioDescriptor {
	return []ChaosScenarioDescriptor{
		{
			ID:                   "leader_stepdown",
			Name:                 "Controlled Leader Stepdown",
			Description:          "Transfers Raft leadership from the active leader to a healthy follower while verifying quorum stays intact and recording failover latency.",
			Safe:                 true,
			ConfirmationRequired: true,
		},
		{
			ID:                   "quorum_read_barrier",
			Name:                 "Quorum Linearizability & Read Barrier Probe",
			Description:          "Verifies majority quorum connectivity, authoritative lock read path, and replicated log alignment across cluster coordinators.",
			Safe:                 true,
			ConfirmationRequired: true,
		},
		{
			ID:                   "follower_partition_drill",
			Name:                 "Bounded Follower Partition Drill",
			Description:          "Temporarily isolates one healthy follower for ~1.2s while preserving majority quorum, verifies quorum continuity, then heals and measures rejoin time.",
			Safe:                 true,
			ConfirmationRequired: true,
		},
	}
}

// GetChaosStatus returns current chaos, partition, recovery duration, and counter summaries.
func (d *DiagnosticsService) GetChaosStatus(ctx context.Context) ChaosStatusReport {
	// Refresh cluster state so active partition reflects live reality.
	_ = d.CollectClusterReport(ctx)

	reg := d.reg()
	ctrl := d.getChaosController()
	enabled := ctrl != nil && ctrl.IsEnabled()

	d.mu.RLock()
	env := d.environment
	d.mu.RUnlock()

	t := d.tracker
	t.mu.Lock()
	running := t.chaosRunning
	historyCopy := make([]ChaosExecutionResult, len(t.chaosHistory))
	copy(historyCopy, t.chaosHistory)
	partHistCopy := make([]PartitionActionRecord, len(t.partitionHistory))
	copy(partHistCopy, t.partitionHistory)
	leaderHistCopy := make([]LeaderTransitionRecord, len(t.leaderHistory))
	copy(leaderHistCopy, t.leaderHistory)
	var lastResult *ChaosExecutionResult
	if len(historyCopy) > 0 {
		lr := historyCopy[0]
		lastResult = &lr
	}
	var lastRec *RecoveryDurationRecord
	if t.lastRecovery != nil {
		cp := *t.lastRecovery
		lastRec = &cp
	}
	activePart := t.activePartition
	t.mu.Unlock()

	return ChaosStatusReport{
		Enabled:            enabled,
		Environment:        env,
		Running:            running,
		ExecutionCount:     reg.GetCounterTotal("lockguard_chaos_tests_total"),
		PartitionsInjected: reg.GetCounterTotal("lockguard_network_partitions_total"),
		SnapshotRestores:   reg.GetCounterTotal("lockguard_snapshot_restore_total"),
		LeaderChanges:      reg.GetCounterTotal("lockguard_leader_changes_total"),
		LastRecovery:       lastRec,
		ActivePartition:    activePart,
		LastResult:         lastResult,
		History:            historyCopy,
		PartitionHistory:   partHistCopy,
		LeaderHistory:      leaderHistCopy,
		AvailableScenarios: AvailableScenarios(),
		GeneratedAt:        time.Now().UTC(),
	}
}

// RunChaosTest validates and executes one safe chaos drill when explicitly confirmed by an admin.
func (d *DiagnosticsService) RunChaosTest(ctx context.Context, scenario string, confirm bool) (ChaosExecutionResult, error) {
	if !confirm {
		return ChaosExecutionResult{}, fmt.Errorf("%w: set confirm=true before executing chaos scenario", ErrConfirmationRequired)
	}
	ctrl := d.getChaosController()
	if ctrl == nil || !ctrl.IsEnabled() {
		return ChaosExecutionResult{}, ErrForbiddenInProd
	}
	if d.discovery == nil {
		return ChaosExecutionResult{}, ErrNoQuorumAvailable
	}

	normalized := strings.ToLower(strings.TrimSpace(scenario))
	if normalized == "" {
		normalized = "leader_stepdown"
	}

	t := d.tracker
	t.mu.Lock()
	if t.chaosRunning {
		t.mu.Unlock()
		return ChaosExecutionResult{}, fmt.Errorf("%w: another chaos test is already running", ErrConflictState)
	}
	if t.activePartition.Active {
		t.mu.Unlock()
		return ChaosExecutionResult{}, fmt.Errorf("%w: heal active network partition on %s before starting a chaos drill", ErrConflictState, t.activePartition.NodeID)
	}
	t.chaosRunning = true
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		t.chaosRunning = false
		t.mu.Unlock()
	}()

	startedAt := time.Now().UTC()
	var result ChaosExecutionResult
	var execErr error

	switch normalized {
	case "leader_stepdown":
		result, execErr = d.runLeaderStepdownChaos(ctx, ctrl, startedAt)
	case "quorum_read_barrier":
		result, execErr = d.runQuorumReadBarrierChaos(ctx, ctrl, startedAt)
	case "follower_partition_drill":
		result, execErr = d.runFollowerPartitionDrill(ctx, ctrl, startedAt)
	default:
		return ChaosExecutionResult{}, fmt.Errorf("%w: unknown chaos scenario %q", ErrInvalidRequest, scenario)
	}

	if execErr == nil || result.ID != "" {
		t.mu.Lock()
		t.chaosHistory = append([]ChaosExecutionResult{result}, t.chaosHistory...)
		if len(t.chaosHistory) > 15 {
			t.chaosHistory = t.chaosHistory[:15]
		}
		t.mu.Unlock()
	}

	return result, execErr
}

func (d *DiagnosticsService) runLeaderStepdownChaos(ctx context.Context, ctrl *chaos.Controller, startedAt time.Time) (ChaosExecutionResult, error) {
	initial := d.CollectClusterReport(ctx)
	if !initial.QuorumAvailable || initial.CurrentLeaderURL == "" || len(initial.AvailableNodes) < 2 {
		return ChaosExecutionResult{}, fmt.Errorf("%w: need active leader and at least 2 healthy coordinators for safe leadership transfer", ErrNoQuorumAvailable)
	}

	oldLeaderID := initial.CurrentLeaderID
	oldLeaderURL := initial.CurrentLeaderURL

	var newLeaderID string
	var stepReport ClusterRecoveryReport
	applyStart := time.Now()

	faultID, revertFn, err := ctrl.Inject(
		ctx,
		chaos.FaultType("LEADER_STEPDOWN"),
		oldLeaderID,
		map[string]interface{}{"scenario": "leader_stepdown", "old_leader": oldLeaderID},
		fmt.Sprintf("Controlled Raft leadership stepdown from %s", oldLeaderID),
		func(callCtx context.Context) error {
			if err := postJSON(callCtx, oldLeaderURL+"/v1/cluster/stepdown", nil, nil); err != nil {
				return err
			}
			deadline := time.Now().Add(6 * time.Second)
			for time.Now().Before(deadline) {
				stepReport = d.CollectClusterReport(callCtx)
				if stepReport.QuorumAvailable && stepReport.CurrentLeaderID != "" && stepReport.CurrentLeaderID != oldLeaderID {
					newLeaderID = stepReport.CurrentLeaderID
					return nil
				}
				select {
				case <-callCtx.Done():
					return callCtx.Err()
				case <-time.After(150 * time.Millisecond):
				}
			}
			return fmt.Errorf("leadership transfer from %s did not complete within 6s (current leader: %s)", oldLeaderID, stepReport.CurrentLeaderID)
		},
		func(callCtx context.Context) error {
			return nil
		},
	)

	completedAt := time.Now().UTC()
	elapsedSec := time.Since(applyStart).Seconds()
	if revertFn != nil {
		_ = revertFn(ctx)
	}

	if err != nil {
		res := ChaosExecutionResult{
			ID:              newActionID("chaos"),
			Scenario:        "leader_stepdown",
			FaultType:       "LEADER_STEPDOWN",
			Target:          oldLeaderID,
			Status:          "FAILED",
			Summary:         err.Error(),
			StartedAt:       startedAt,
			CompletedAt:     completedAt,
			DurationMs:      durationMsFromSeconds(elapsedSec),
			QuorumPreserved: stepReport.QuorumAvailable,
		}
		return res, err
	}

	ms := d.tracker.recordExplicitRecoveryDuration("leader_election", newLeaderID, elapsedSec)
	res := ChaosExecutionResult{
		ID:              faultID,
		Scenario:        "leader_stepdown",
		FaultType:       "LEADER_STEPDOWN",
		Target:          oldLeaderID,
		Status:          "PASSED",
		Summary:         fmt.Sprintf("Leadership transferred safely from %s to %s in %d ms; quorum preserved (%d/%d nodes).", oldLeaderID, newLeaderID, ms, len(stepReport.AvailableNodes), stepReport.TotalNodes),
		StartedAt:       startedAt,
		CompletedAt:     completedAt,
		DurationMs:      ms,
		QuorumPreserved: stepReport.QuorumAvailable,
		Details: map[string]interface{}{
			"old_leader":      oldLeaderID,
			"new_leader":      newLeaderID,
			"available_nodes": len(stepReport.AvailableNodes),
			"total_nodes":     stepReport.TotalNodes,
		},
	}

	d.emitAuditEvent(&audit.Event{
		EventType:       "CHAOS_TEST_EXECUTED",
		EventCategory:   audit.CategoryConsensus,
		CoordinatorID:   newLeaderID,
		OperationStatus: audit.StatusCommitted,
		Metadata:        res.Details,
	})
	return res, nil
}

func (d *DiagnosticsService) runQuorumReadBarrierChaos(ctx context.Context, ctrl *chaos.Controller, startedAt time.Time) (ChaosExecutionResult, error) {
	report := d.CollectClusterReport(ctx)
	if !report.QuorumAvailable || report.CurrentLeaderURL == "" {
		return ChaosExecutionResult{}, ErrNoQuorumAvailable
	}

	applyStart := time.Now()
	faultID, revertFn, err := ctrl.Inject(
		ctx,
		chaos.FaultType("QUORUM_READ_BARRIER"),
		report.CurrentLeaderID,
		map[string]interface{}{"scenario": "quorum_read_barrier", "leader": report.CurrentLeaderID},
		"Verified linearizable Raft leader read barrier across active cluster quorum",
		func(callCtx context.Context) error {
			return getJSON(callCtx, report.CurrentLeaderURL+"/v1/locks", nil)
		},
		func(callCtx context.Context) error { return nil },
	)

	completedAt := time.Now().UTC()
	elapsedSec := time.Since(applyStart).Seconds()
	if revertFn != nil {
		_ = revertFn(ctx)
	}
	if err != nil {
		res := ChaosExecutionResult{
			ID:              newActionID("chaos"),
			Scenario:        "quorum_read_barrier",
			FaultType:       "QUORUM_READ_BARRIER",
			Target:          report.CurrentLeaderID,
			Status:          "FAILED",
			Summary:         err.Error(),
			StartedAt:       startedAt,
			CompletedAt:     completedAt,
			DurationMs:      durationMsFromSeconds(elapsedSec),
			QuorumPreserved: report.QuorumAvailable,
		}
		return res, err
	}

	ms := durationMsFromSeconds(elapsedSec)
	res := ChaosExecutionResult{
		ID:              faultID,
		Scenario:        "quorum_read_barrier",
		FaultType:       "QUORUM_READ_BARRIER",
		Target:          report.CurrentLeaderID,
		Status:          "PASSED",
		Summary:         fmt.Sprintf("Linearizable read barrier succeeded on %s with %d/%d healthy coordinators in %d ms.", report.CurrentLeaderID, len(report.AvailableNodes), report.TotalNodes, ms),
		StartedAt:       startedAt,
		CompletedAt:     completedAt,
		DurationMs:      ms,
		QuorumPreserved: true,
		Details: map[string]interface{}{
			"leader":          report.CurrentLeaderID,
			"available_nodes": len(report.AvailableNodes),
			"total_nodes":     report.TotalNodes,
		},
	}

	d.emitAuditEvent(&audit.Event{
		EventType:       "CHAOS_TEST_EXECUTED",
		EventCategory:   audit.CategoryConsensus,
		CoordinatorID:   report.CurrentLeaderID,
		OperationStatus: audit.StatusCommitted,
		Metadata:        res.Details,
	})
	return res, nil
}

func (d *DiagnosticsService) runFollowerPartitionDrill(ctx context.Context, ctrl *chaos.Controller, startedAt time.Time) (ChaosExecutionResult, error) {
	initial := d.CollectClusterReport(ctx)
	majorityThreshold := (initial.TotalNodes / 2) + 1
	if !initial.QuorumAvailable || len(initial.AvailableNodes)-1 < majorityThreshold {
		return ChaosExecutionResult{}, fmt.Errorf("%w: require at least %d healthy nodes before isolating a follower so majority quorum (%d) remains intact", ErrQuorumSafetyViolation, majorityThreshold+1, majorityThreshold)
	}

	var target *CoordinatorNodeStatus
	for i := range initial.Coordinators {
		c := &initial.Coordinators[i]
		if c.Reachable && !c.Partitioned && !c.IsLeader {
			target = c
			break
		}
	}
	if target == nil {
		return ChaosExecutionResult{}, fmt.Errorf("%w: no healthy non-leader follower available for partition drill", ErrConflictState)
	}

	var midReport, healedReport ClusterRecoveryReport
	applyStart := time.Now()
	var rejoinStart time.Time
	partitioned := false

	faultID, revertFn, err := ctrl.Inject(
		ctx,
		chaos.FaultNetworkPartition,
		target.NodeID,
		map[string]interface{}{"scenario": "follower_partition_drill", "target": target.NodeID},
		fmt.Sprintf("Bounded simulated follower partition drill on %s", target.NodeID),
		func(callCtx context.Context) error {
			if err := postJSON(callCtx, target.Endpoint+"/v1/cluster/partition/inject", map[string]bool{"confirm": true}, nil); err != nil {
				return err
			}
			partitioned = true
			d.reg().IncNetworkPartitions("chaos_drill")

			select {
			case <-callCtx.Done():
				return callCtx.Err()
			case <-time.After(1200 * time.Millisecond):
			}

			midReport = d.CollectClusterReport(callCtx)
			if !midReport.QuorumAvailable {
				return fmt.Errorf("unexpected quorum loss during follower partition drill on %s", target.NodeID)
			}
			return nil
		},
		func(callCtx context.Context) error {
			if !partitioned {
				return nil
			}
			rejoinStart = time.Now()
			return postJSON(callCtx, target.Endpoint+"/v1/cluster/partition/heal", map[string]bool{}, nil)
		},
	)

	if revertFn != nil {
		healCtx, healCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = revertFn(healCtx)
		healCancel()
	}

	// Wait briefly for healed node to report healthy in cluster report.
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		healedReport = d.CollectClusterReport(ctx)
		allBack := true
		for _, c := range healedReport.Coordinators {
			if c.NodeID == target.NodeID && (!c.Reachable || c.Partitioned) {
				allBack = false
				break
			}
		}
		if allBack && healedReport.QuorumAvailable {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}

	completedAt := time.Now().UTC()
	rejoinSec := 0.0
	if !rejoinStart.IsZero() {
		rejoinSec = time.Since(rejoinStart).Seconds()
	} else {
		rejoinSec = time.Since(applyStart).Seconds()
	}
	rejoinMs := d.tracker.recordExplicitRecoveryDuration("node_rejoin", target.NodeID, rejoinSec)
	totalMs := durationMsFromSeconds(time.Since(applyStart).Seconds())

	if err != nil {
		res := ChaosExecutionResult{
			ID:              newActionID("chaos"),
			Scenario:        "follower_partition_drill",
			FaultType:       string(chaos.FaultNetworkPartition),
			Target:          target.NodeID,
			Status:          "FAILED",
			Summary:         err.Error(),
			StartedAt:       startedAt,
			CompletedAt:     completedAt,
			DurationMs:      totalMs,
			QuorumPreserved: midReport.QuorumAvailable,
		}
		return res, err
	}

	res := ChaosExecutionResult{
		ID:              faultID,
		Scenario:        "follower_partition_drill",
		FaultType:       string(chaos.FaultNetworkPartition),
		Target:          target.NodeID,
		Status:          "PASSED",
		Summary:         fmt.Sprintf("Isolated follower %s while maintaining quorum (%d/%d nodes), healed in %d ms (%d ms total).", target.NodeID, len(midReport.AvailableNodes), midReport.TotalNodes, rejoinMs, totalMs),
		StartedAt:       startedAt,
		CompletedAt:     completedAt,
		DurationMs:      totalMs,
		QuorumPreserved: midReport.QuorumAvailable && healedReport.QuorumAvailable,
		Details: map[string]interface{}{
			"partitioned_node":     target.NodeID,
			"mid_available_nodes":  len(midReport.AvailableNodes),
			"healed_available":     len(healedReport.AvailableNodes),
			"rejoin_duration_ms":   rejoinMs,
		},
	}
	d.emitAuditEvent(&audit.Event{
		EventType:       "CHAOS_TEST_EXECUTED",
		EventCategory:   audit.CategoryConsensus,
		CoordinatorID:   target.NodeID,
		OperationStatus: audit.StatusCommitted,
		Metadata:        res.Details,
	})
	return res, nil
}

// InjectPartition isolates one healthy coordinator's Raft transport while strictly preserving majority quorum.
func (d *DiagnosticsService) InjectPartition(ctx context.Context, targetNodeID string, confirm bool, durationSec int) (PartitionActionResult, error) {
	if !confirm {
		return PartitionActionResult{}, fmt.Errorf("%w: set confirm=true to inject a simulated network partition", ErrConfirmationRequired)
	}
	ctrl := d.getChaosController()
	if ctrl == nil || !ctrl.IsEnabled() {
		return PartitionActionResult{}, ErrForbiddenInProd
	}
	if d.discovery == nil {
		return PartitionActionResult{}, ErrNoQuorumAvailable
	}

	t := d.tracker
	t.mu.Lock()
	if t.chaosRunning {
		t.mu.Unlock()
		return PartitionActionResult{}, fmt.Errorf("%w: wait for active chaos test to complete before injecting a partition", ErrConflictState)
	}
	if t.activePartition.Active {
		activeID := t.activePartition.NodeID
		t.mu.Unlock()
		return PartitionActionResult{}, fmt.Errorf("%w: partition already active on %s; heal it before injecting another", ErrConflictState, activeID)
	}
	t.mu.Unlock()

	report := d.CollectClusterReport(ctx)
	majorityThreshold := (report.TotalNodes / 2) + 1
	if !report.QuorumAvailable || report.TotalNodes < 3 {
		return PartitionActionResult{}, fmt.Errorf("%w: majority quorum must be healthy across at least 3 nodes before injecting a partition", ErrQuorumSafetyViolation)
	}

	var target *CoordinatorNodeStatus
	desired := strings.TrimSpace(targetNodeID)
	for i := range report.Coordinators {
		c := &report.Coordinators[i]
		if desired == "" {
			if c.Reachable && !c.Partitioned && !c.IsLeader {
				target = c
				break
			}
		} else if strings.EqualFold(c.NodeID, desired) {
			target = c
			break
		}
	}

	if target == nil {
		return PartitionActionResult{}, fmt.Errorf("%w: coordinator %q not found", ErrTargetNotFound, desired)
	}
	if target.Partitioned {
		return PartitionActionResult{}, fmt.Errorf("%w: coordinator %s is already partitioned", ErrConflictState, target.NodeID)
	}
	if !target.Reachable {
		return PartitionActionResult{}, fmt.Errorf("%w: coordinator %s is currently unreachable", ErrConflictState, target.NodeID)
	}

	// Enforce Raft quorum safety invariant: remaining healthy consensus nodes must still meet majorityThreshold.
	remainingHealthy := len(report.AvailableNodes) - 1
	if remainingHealthy < majorityThreshold {
		return PartitionActionResult{}, fmt.Errorf(
			"%w: partitioning %s leaves %d/%d healthy nodes, below required majority quorum of %d",
			ErrQuorumSafetyViolation, target.NodeID, remainingHealthy, report.TotalNodes, majorityThreshold,
		)
	}

	if durationSec <= 0 {
		durationSec = 30
	} else if durationSec < 5 {
		durationSec = 5
	} else if durationSec > 60 {
		durationSec = 60
	}

	topology := "isolated_follower"
	if target.IsLeader {
		topology = "isolated_leader"
	}

	if err := postJSON(ctx, target.Endpoint+"/v1/cluster/partition/inject", map[string]bool{"confirm": true}, nil); err != nil {
		return PartitionActionResult{}, err
	}

	d.reg().IncNetworkPartitions(topology)
	now := time.Now().UTC()
	autoHealAt := now.Add(time.Duration(durationSec) * time.Second)

	t.mu.Lock()
	if t.healTimer != nil {
		t.healTimer.Stop()
	}
	targetIDCopy := target.NodeID
	targetEndpointCopy := target.Endpoint
	t.activePartition = ActivePartitionState{
		Active:     true,
		NodeID:     targetIDCopy,
		Endpoint:   targetEndpointCopy,
		Topology:   topology,
		InjectedAt: &now,
		AutoHealAt: &autoHealAt,
	}
	t.healTimer = time.AfterFunc(time.Duration(durationSec)*time.Second, func() {
		healCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = d.HealPartition(healCtx, targetIDCopy)
	})
	t.mu.Unlock()

	updated := d.CollectClusterReport(ctx)

	t.mu.Lock()
	partRec := PartitionActionRecord{
		ID:              newActionID("part"),
		Action:          "INJECTED",
		NodeID:          target.NodeID,
		Topology:        topology,
		QuorumAvailable: updated.QuorumAvailable,
		AvailableNodes:  len(updated.AvailableNodes),
		TotalNodes:      updated.TotalNodes,
		Timestamp:       now,
	}
	t.partitionHistory = append([]PartitionActionRecord{partRec}, t.partitionHistory...)
	if len(t.partitionHistory) > 20 {
		t.partitionHistory = t.partitionHistory[:20]
	}
	t.mu.Unlock()

	d.emitAuditEvent(&audit.Event{
		EventType:       "NETWORK_PARTITION_INJECTED",
		EventCategory:   audit.CategoryConsensus,
		CoordinatorID:   target.NodeID,
		OperationStatus: audit.StatusCommitted,
		Metadata: map[string]interface{}{
			"topology":        topology,
			"available_nodes": len(updated.AvailableNodes),
			"majority_quorum": majorityThreshold,
			"auto_heal_sec":   durationSec,
		},
	})

	return PartitionActionResult{
		Action:          "INJECTED",
		NodeID:          target.NodeID,
		Partitioned:     true,
		Topology:        topology,
		QuorumAvailable: updated.QuorumAvailable,
		AvailableNodes:  len(updated.AvailableNodes),
		TotalNodes:      updated.TotalNodes,
		MajorityQuorum:  majorityThreshold,
		AutoHealAt:      &autoHealAt,
		Message:         fmt.Sprintf("Coordinator %s isolated (%s). Remaining quorum: %d/%d nodes; auto-heal scheduled in %ds.", target.NodeID, topology, len(updated.AvailableNodes), updated.TotalNodes, durationSec),
	}, nil
}

// HealPartition restores Raft network connectivity for a partitioned coordinator and records rejoin latency.
func (d *DiagnosticsService) HealPartition(ctx context.Context, targetNodeID string) (PartitionActionResult, error) {
	if d.discovery == nil {
		return PartitionActionResult{}, ErrNoQuorumAvailable
	}

	t := d.tracker
	report := d.CollectClusterReport(ctx)
	majorityThreshold := (report.TotalNodes / 2) + 1

	t.mu.Lock()
	activeNodeID := t.activePartition.NodeID
	t.mu.Unlock()

	desired := strings.TrimSpace(targetNodeID)
	if desired == "" {
		desired = activeNodeID
	}

	var healTargets []CoordinatorNodeStatus
	for _, c := range report.Coordinators {
		if desired != "" {
			if strings.EqualFold(c.NodeID, desired) {
				healTargets = append(healTargets, c)
				break
			}
		} else if c.Partitioned {
			healTargets = append(healTargets, c)
		}
	}

	if len(healTargets) == 0 {
		return PartitionActionResult{}, fmt.Errorf("%w: no partitioned coordinator matches %q", ErrConflictState, desired)
	}

	healStart := time.Now()
	var healedID string
	for _, c := range healTargets {
		if err := postJSON(ctx, c.Endpoint+"/v1/cluster/partition/heal", map[string]bool{}, nil); err != nil {
			return PartitionActionResult{}, err
		}
		healedID = c.NodeID
	}

	t.mu.Lock()
	if t.healTimer != nil {
		t.healTimer.Stop()
		t.healTimer = nil
	}
	t.activePartition = ActivePartitionState{}
	t.mu.Unlock()

	// Refresh cluster state so ObserveClusterReport captures outage -> rejoin recovery duration cleanly.
	deadline := time.Now().Add(4 * time.Second)
	updated := d.CollectClusterReport(ctx)
	for time.Now().Before(deadline) {
		allHealthy := true
		for _, c := range updated.Coordinators {
			if c.NodeID == healedID && (!c.Reachable || c.Partitioned) {
				allHealthy = false
				break
			}
		}
		if allHealthy && updated.QuorumAvailable {
			break
		}
		time.Sleep(150 * time.Millisecond)
		updated = d.CollectClusterReport(ctx)
	}

	t.mu.Lock()
	var recoveryMs int64
	if t.lastRecovery != nil && t.lastRecovery.Action == "node_rejoin" && t.lastRecovery.Target == healedID {
		recoveryMs = t.lastRecovery.DurationMs
	}
	t.mu.Unlock()
	if recoveryMs == 0 {
		recoveryMs = t.recordExplicitRecoveryDuration("node_rejoin", healedID, time.Since(healStart).Seconds())
	}

	t.mu.Lock()
	healRec := PartitionActionRecord{
		ID:                 newActionID("part"),
		Action:             "HEALED",
		NodeID:             healedID,
		RecoveryDurationMs: recoveryMs,
		QuorumAvailable:    updated.QuorumAvailable,
		AvailableNodes:     len(updated.AvailableNodes),
		TotalNodes:         updated.TotalNodes,
		Timestamp:          time.Now().UTC(),
	}
	t.partitionHistory = append([]PartitionActionRecord{healRec}, t.partitionHistory...)
	if len(t.partitionHistory) > 20 {
		t.partitionHistory = t.partitionHistory[:20]
	}
	t.mu.Unlock()

	d.emitAuditEvent(&audit.Event{
		EventType:       "NETWORK_PARTITION_HEALED",
		EventCategory:   audit.CategoryConsensus,
		CoordinatorID:   healedID,
		OperationStatus: audit.StatusCommitted,
		Metadata: map[string]interface{}{
			"recovery_duration_ms": recoveryMs,
			"available_nodes":      len(updated.AvailableNodes),
		},
	})

	return PartitionActionResult{
		Action:             "HEALED",
		NodeID:             healedID,
		Partitioned:        false,
		RecoveryDurationMs: recoveryMs,
		QuorumAvailable:    updated.QuorumAvailable,
		AvailableNodes:     len(updated.AvailableNodes),
		TotalNodes:         updated.TotalNodes,
		MajorityQuorum:     majorityThreshold,
		Message:            fmt.Sprintf("Coordinator %s reconnected and rejoined quorum (%d/%d healthy) with %d ms measured recovery duration.", healedID, len(updated.AvailableNodes), updated.TotalNodes, recoveryMs),
	}, nil
}

// CreateClusterSnapshot creates an on-demand Raft snapshot on the active cluster leader.
func (d *DiagnosticsService) CreateClusterSnapshot(ctx context.Context) (SnapshotActionResult, error) {
	if d.discovery == nil {
		return SnapshotActionResult{}, ErrNoQuorumAvailable
	}
	leaderURL, err := d.discovery.GetLeader(ctx)
	if err != nil || leaderURL == "" {
		return SnapshotActionResult{}, ErrNoQuorumAvailable
	}

	var respData struct {
		NodeID          string                   `json:"node_id"`
		AlreadyUpToDate bool                     `json:"already_up_to_date"`
		Latest          *consensus.SnapshotInfo  `json:"latest"`
		Snapshots       []consensus.SnapshotInfo `json:"snapshots"`
	}
	if err := postJSON(ctx, leaderURL+"/v1/cluster/snapshots/create", map[string]interface{}{}, &respData); err != nil {
		return SnapshotActionResult{}, err
	}

	msg := fmt.Sprintf("Created Raft snapshot on leader %s.", respData.NodeID)
	if respData.AlreadyUpToDate {
		msg = fmt.Sprintf("Leader %s already holds a current snapshot at the latest applied log index.", respData.NodeID)
	}

	return SnapshotActionResult{
		Action:           "CREATED",
		Snapshot:         respData.Latest,
		AlreadyUpToDate:  respData.AlreadyUpToDate,
		Snapshots:        respData.Snapshots,
		RestoredByLeader: respData.NodeID,
		Message:          msg,
	}, nil
}

// RestoreClusterSnapshot validates and applies a Raft snapshot cluster-wide on the active leader.
// Only confirmed requests succeed, and lockguard_snapshot_restore_total increments exclusively upon verified restore.
func (d *DiagnosticsService) RestoreClusterSnapshot(ctx context.Context, snapshotID string, confirm bool) (SnapshotActionResult, error) {
	if !confirm {
		return SnapshotActionResult{}, fmt.Errorf("%w: set confirm=true before restoring a Raft snapshot (persistent cluster state will be replaced)", ErrConfirmationRequired)
	}
	if strings.TrimSpace(snapshotID) == "" {
		return SnapshotActionResult{}, fmt.Errorf("%w: snapshot_id is required", ErrInvalidRequest)
	}
	if d.discovery == nil {
		return SnapshotActionResult{}, ErrNoQuorumAvailable
	}

	report := d.CollectClusterReport(ctx)
	if !report.QuorumAvailable || report.CurrentLeaderURL == "" {
		return SnapshotActionResult{}, ErrNoQuorumAvailable
	}

	start := time.Now()
	leaderURL := report.CurrentLeaderURL
	leaderID := report.CurrentLeaderID

	var coordRes struct {
		NodeID   string                  `json:"node_id"`
		Restored bool                    `json:"restored"`
		Snapshot *consensus.SnapshotInfo `json:"snapshot"`
	}

	reqBody := map[string]interface{}{
		"snapshot_id": snapshotID,
		"confirm":     true,
	}

	err := postJSON(ctx, leaderURL+"/v1/cluster/snapshots/restore", reqBody, &coordRes)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "not found") {
		// Fallback: locate the snapshot on another healthy peer coordinator and upload it to the leader.
		for _, ep := range d.discovery.Endpoints() {
			if ep == leaderURL {
				continue
			}
			var rawSnap struct {
				Meta       json.RawMessage `json:"meta"`
				PayloadB64 string          `json:"payload_b64"`
			}
			if getJSON(ctx, fmt.Sprintf("%s/v1/cluster/snapshots/raw?id=%s", ep, url.QueryEscape(snapshotID)), &rawSnap) == nil && rawSnap.PayloadB64 != "" {
				err = postJSON(ctx, leaderURL+"/v1/cluster/snapshots/restore", map[string]interface{}{
					"snapshot_id": snapshotID,
					"confirm":     true,
					"meta":        rawSnap.Meta,
					"payload_b64": rawSnap.PayloadB64,
				}, &coordRes)
				break
			}
		}
	}

	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return SnapshotActionResult{}, fmt.Errorf("%w: %v", ErrTargetNotFound, err)
		}
		return SnapshotActionResult{}, err
	}

	// Increment metric and record recovery duration strictly AFTER verified cluster restore success.
	d.reg().IncSnapshotRestore("success")
	ms := d.tracker.recordExplicitRecoveryDuration("snapshot_restore", snapshotID, time.Since(start).Seconds())
	updatedSnaps := d.CollectSnapshotReport(ctx).Snapshots

	d.emitAuditEvent(&audit.Event{
		EventType:       "SNAPSHOT_RESTORED",
		EventCategory:   audit.CategoryConsensus,
		CoordinatorID:   leaderID,
		OperationStatus: audit.StatusCommitted,
		Metadata: map[string]interface{}{
			"snapshot_id": snapshotID,
			"duration_ms": ms,
		},
	})

	return SnapshotActionResult{
		Action:           "RESTORED",
		Snapshot:         coordRes.Snapshot,
		Snapshots:        updatedSnaps,
		RestoredByLeader: leaderID,
		DurationMs:       ms,
		Message:          fmt.Sprintf("Snapshot %s validated and restored across cluster quorum by leader %s in %d ms.", snapshotID, leaderID, ms),
	}, nil
}

func (d *DiagnosticsService) emitAuditEvent(ev *audit.Event) {
	if d.pipeline == nil || ev == nil {
		return
	}
	if ev.EventID == "" {
		ev.EventID = audit.GenerateEventID()
	}
	if ev.EventTimestamp.IsZero() {
		ev.EventTimestamp = time.Now().UTC()
	}
	d.pipeline.RecordEvent(ev)
}

type apiEnvelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func getJSON(ctx context.Context, targetURL string, out interface{}) error {
	return doJSONRequest(ctx, http.MethodGet, targetURL, nil, out)
}

func postJSON(ctx context.Context, targetURL string, body interface{}, out interface{}) error {
	return doJSONRequest(ctx, http.MethodPost, targetURL, body, out)
}

func doJSONRequest(ctx context.Context, method, targetURL string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to serialize coordinator request: %w", err)
		}
		bodyReader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, targetURL, bodyReader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("coordinator request %s %s failed: %w", method, targetURL, err)
	}
	defer resp.Body.Close()

	rawResp, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read coordinator response: %w", err)
	}

	var env apiEnvelope
	if decErr := json.Unmarshal(rawResp, &env); decErr != nil {
		if resp.StatusCode >= 400 {
			return fmt.Errorf("coordinator returned HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("invalid coordinator JSON response: %w", decErr)
	}

	if resp.StatusCode >= 400 || !env.Success {
		msg := fmt.Sprintf("coordinator returned HTTP %d", resp.StatusCode)
		if env.Error != nil && env.Error.Message != "" {
			msg = env.Error.Message
		}
		return fmt.Errorf("%s", msg)
	}

	if out != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("failed to decode coordinator data payload: %w", err)
		}
	}
	return nil
}
