package recovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"lockguard/backend/internal/audit"
	"lockguard/backend/internal/gateway"
	"lockguard/backend/internal/persistence"
)

// System health status constants.
const (
	StatusHealthy   = "HEALTHY"
	StatusDegraded  = "DEGRADED"
	StatusUnhealthy = "UNHEALTHY"
)

// SubsystemHealth reports the operational condition of an individual system component.
type SubsystemHealth struct {
	Name      string                 `json:"name"`
	Status    string                 `json:"status"`
	Message   string                 `json:"message"`
	Details   map[string]interface{} `json:"details,omitempty"`
	CheckedAt time.Time              `json:"checked_at"`
}

// SystemHealthReport aggregates multi-component health diagnostics.
// Crucially, lock authority depends exclusively on Raft consensus; database outages degrade
// audit history persistence but do NOT halt or invalidate authoritative Raft lock operations.
type SystemHealthReport struct {
	OverallStatus       string                     `json:"overall_status"`
	NodeID              string                     `json:"node_id"`
	ServiceName         string                     `json:"service_name"`
	RaftQuorumAvailable bool                       `json:"raft_quorum_available"`
	LeaderAvailable     bool                       `json:"leader_available"`
	DatabaseAvailable   bool                       `json:"database_available"`
	Subsystems          map[string]SubsystemHealth `json:"subsystems"`
	Timestamp           time.Time                  `json:"timestamp"`
}

// DiagnosticsService collects operational indicators across the API gateway, consensus discovery,
// PostgreSQL connection pool, migrations, and the audit pipeline.
type DiagnosticsService struct {
	nodeID      string
	serviceName string
	discovery   *gateway.LeaderDiscovery
	db          *persistence.Database
	migrator    *persistence.Migrator
	pipeline    *audit.Pipeline
}

// NewDiagnosticsService constructs a new DiagnosticsService.
func NewDiagnosticsService(
	nodeID, serviceName string,
	discovery *gateway.LeaderDiscovery,
	db *persistence.Database,
	migrator *persistence.Migrator,
	pipeline *audit.Pipeline,
) *DiagnosticsService {
	return &DiagnosticsService{
		nodeID:      nodeID,
		serviceName: serviceName,
		discovery:   discovery,
		db:          db,
		migrator:    migrator,
		pipeline:    pipeline,
	}
}

// SetDatabase updates the database and migrator handles for diagnostics inspection.
func (d *DiagnosticsService) SetDatabase(db *persistence.Database, migrator *persistence.Migrator) {
	d.db = db
	d.migrator = migrator
}

// CollectReport performs context-bounded health checks across all subsystems and derives the overall system status.
func (d *DiagnosticsService) CollectReport(ctx context.Context) SystemHealthReport {
	now := time.Now().UTC()
	subsystems := make(map[string]SubsystemHealth)

	// 1. Check Raft Consensus Discovery (sole lock authority)
	raftHealth := SubsystemHealth{
		Name:      "raft_consensus",
		Status:    StatusUnhealthy,
		CheckedAt: now,
		Details:   make(map[string]interface{}),
	}
	raftQuorumAvailable := false
	leaderAvailable := false

	if d.discovery != nil {
		leaderURL, err := d.discovery.GetLeader(ctx)
		if err == nil && leaderURL != "" {
			raftQuorumAvailable = true
			leaderAvailable = true
			raftHealth.Status = StatusHealthy
			raftHealth.Message = "active leader reachable: " + leaderURL
			raftHealth.Details["leader_url"] = leaderURL
		} else {
			raftHealth.Status = StatusUnhealthy
			raftHealth.Message = "quorum lost or leader unreachable"
			if err != nil {
				raftHealth.Details["error"] = err.Error()
			}
		}
	} else {
		raftHealth.Status = StatusDegraded
		raftHealth.Message = "leader discovery service not injected"
	}
	subsystems["raft_consensus"] = raftHealth

	// 2. Check Database Storage Engine (audit & operational event persistence)
	dbHealth := SubsystemHealth{
		Name:      "database_persistence",
		Status:    StatusUnhealthy,
		CheckedAt: now,
		Details:   make(map[string]interface{}),
	}
	dbAvailable := false

	if d.db != nil {
		dbStatus := d.db.HealthCheck(ctx)
		dbAvailable = dbStatus.Available
		dbHealth.Details["open_connections"] = dbStatus.OpenConnections
		dbHealth.Details["in_use"] = dbStatus.InUse
		dbHealth.Details["idle"] = dbStatus.Idle
		dbHealth.Details["latency_ms"] = dbStatus.Latency.Milliseconds()

		if dbStatus.Available {
			dbHealth.Status = StatusHealthy
			dbHealth.Message = "postgresql connection pool healthy"
		} else {
			dbHealth.Status = StatusDegraded
			dbHealth.Message = "postgresql connection failed: " + dbStatus.Error
			dbHealth.Details["error"] = dbStatus.Error
		}
	} else {
		dbHealth.Status = StatusDegraded
		dbHealth.Message = "database pool unconfigured"
	}
	subsystems["database_persistence"] = dbHealth

	// 3. Check Migration Status
	migHealth := SubsystemHealth{
		Name:      "schema_migrations",
		Status:    StatusDegraded,
		CheckedAt: now,
		Details:   make(map[string]interface{}),
	}
	if d.migrator != nil && dbAvailable {
		migStatus, err := d.migrator.GetMigrationStatus(ctx)
		if err == nil {
			allApplied := true
			pendingCount := 0
			for _, m := range migStatus {
				if !m.Applied {
					allApplied = false
					pendingCount++
				}
			}
			migHealth.Details["total_migrations"] = len(migStatus)
			migHealth.Details["pending_migrations"] = pendingCount
			if allApplied {
				migHealth.Status = StatusHealthy
				migHealth.Message = "all schema migrations applied"
			} else {
				migHealth.Status = StatusDegraded
				migHealth.Message = "pending schema migrations detected"
			}
		} else {
			migHealth.Status = StatusDegraded
			migHealth.Message = "failed to inspect migration status: " + err.Error()
		}
	} else {
		migHealth.Message = "database offline or migrator unavailable"
	}
	subsystems["schema_migrations"] = migHealth

	// 4. Check Audit Delivery Pipeline
	auditHealth := SubsystemHealth{
		Name:      "audit_pipeline",
		Status:    StatusHealthy,
		CheckedAt: now,
		Details:   make(map[string]interface{}),
	}
	if d.pipeline != nil {
		stats := d.pipeline.Stats()
		auditHealth.Details["queue_size"] = stats.QueueSize
		auditHealth.Details["queue_capacity"] = stats.QueueCapacity
		auditHealth.Details["created_total"] = stats.CreatedTotal
		auditHealth.Details["persisted_total"] = stats.PersistedTotal
		auditHealth.Details["failed_total"] = stats.FailedTotal

		if stats.QueueSize > (stats.QueueCapacity * 9 / 10) {
			auditHealth.Status = StatusDegraded
			auditHealth.Message = "audit queue backlog near capacity"
		} else {
			auditHealth.Status = StatusHealthy
			auditHealth.Message = "audit delivery pipeline operating normally"
		}
	} else {
		auditHealth.Status = StatusDegraded
		auditHealth.Message = "audit pipeline unconfigured"
	}
	subsystems["audit_pipeline"] = auditHealth

	// Overall Status Determination:
	// Rule 1: If Raft quorum is unavailable, overall status is UNHEALTHY (Fail Closed).
	// Rule 2: If Raft quorum is healthy but database or migrations are degraded, overall status is DEGRADED.
	// Rule 3: If all subsystems are healthy, overall status is HEALTHY.
	overall := StatusHealthy
	if !raftQuorumAvailable {
		overall = StatusUnhealthy
	} else if !dbAvailable || migHealth.Status != StatusHealthy || auditHealth.Status != StatusHealthy {
		overall = StatusDegraded
	}

	return SystemHealthReport{
		OverallStatus:       overall,
		NodeID:              d.nodeID,
		ServiceName:         d.serviceName,
		RaftQuorumAvailable: raftQuorumAvailable,
		LeaderAvailable:     leaderAvailable,
		DatabaseAvailable:   dbAvailable,
		Subsystems:          subsystems,
		Timestamp:           now,
	}
}

// CoordinatorNodeStatus captures health and role for an individual coordinator node.
type CoordinatorNodeStatus struct {
	NodeID    string `json:"node_id"`
	Endpoint  string `json:"endpoint"`
	Reachable bool   `json:"reachable"`
	IsLeader  bool   `json:"is_leader"`
	Role      string `json:"role"`
}

// ClusterRecoveryReport provides topology and quorum status for recovery diagnostics.
type ClusterRecoveryReport struct {
	TotalNodes       int                     `json:"total_nodes"`
	AvailableNodes   []string                `json:"available_nodes"`
	CurrentLeaderID  string                  `json:"current_leader_id"`
	CurrentLeaderURL string                  `json:"current_leader_url"`
	QuorumAvailable  bool                    `json:"quorum_available"`
	QuorumState      string                  `json:"quorum_state"` // "ESTABLISHED" or "QUORUM_LOST"
	Coordinators     []CoordinatorNodeStatus `json:"coordinators"`
	GeneratedAt      time.Time               `json:"generated_at"`
}

// SnapshotDiagnosticsReport reports the state of Raft log compaction and snapshot persistence.
type SnapshotDiagnosticsReport struct {
	NodeID              string    `json:"node_id"`
	SnapshotAvailable   bool      `json:"snapshot_available"`
	LastSnapshotIndex   uint64    `json:"last_snapshot_index"`
	LastSnapshotTerm    uint64    `json:"last_snapshot_term"`
	SnapshotThreshold   uint64    `json:"snapshot_threshold"`
	SnapshotInterval    string    `json:"snapshot_interval"`
	SafeSnapshotStorage bool      `json:"safe_snapshot_storage"`
	GeneratedAt         time.Time `json:"generated_at"`
}

// CollectClusterReport queries coordinator endpoints and derives active quorum and topology state.
func (d *DiagnosticsService) CollectClusterReport(ctx context.Context) ClusterRecoveryReport {
	now := time.Now().UTC()
	report := ClusterRecoveryReport{
		GeneratedAt: now,
		QuorumState: "QUORUM_LOST",
	}

	if d.discovery == nil {
		return report
	}

	endpoints := d.discovery.Endpoints()
	report.TotalNodes = len(endpoints)

	var available []string
	var coordStatuses []CoordinatorNodeStatus
	var currentLeaderID string
	var currentLeaderURL string

	leaderURL, err := d.discovery.GetLeader(ctx)
	if err == nil && leaderURL != "" {
		currentLeaderURL = leaderURL
	}

	client := &http.Client{Timeout: 1 * time.Second}

	for _, ep := range endpoints {
		status := CoordinatorNodeStatus{
			Endpoint: ep,
			Role:     "Unknown",
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep+"/v1/cluster/status", nil)
		if err == nil {
			resp, err := client.Do(req)
			if err == nil && resp.StatusCode == http.StatusOK {
				var result struct {
					Success bool `json:"success"`
					Data    struct {
						NodeID   string `json:"node_id"`
						State    string `json:"state"`
						IsLeader bool   `json:"is_leader"`
						LeaderID string `json:"leader_id"`
					} `json:"data"`
				}
				if json.NewDecoder(resp.Body).Decode(&result) == nil && result.Success {
					status.Reachable = true
					status.NodeID = result.Data.NodeID
					status.IsLeader = result.Data.IsLeader
					status.Role = result.Data.State
					available = append(available, status.NodeID)
					if status.IsLeader {
						currentLeaderID = status.NodeID
						currentLeaderURL = ep
					}
				}
				_ = resp.Body.Close()
			} else if resp != nil {
				_ = resp.Body.Close()
			}
		}
		coordStatuses = append(coordStatuses, status)
	}

	report.AvailableNodes = available
	report.Coordinators = coordStatuses
	report.CurrentLeaderID = currentLeaderID
	report.CurrentLeaderURL = currentLeaderURL

	// Majority quorum rule for N nodes: threshold = floor(N/2) + 1
	majorityThreshold := (report.TotalNodes / 2) + 1
	if len(available) >= majorityThreshold && currentLeaderURL != "" {
		report.QuorumAvailable = true
		report.QuorumState = "ESTABLISHED"
	}

	return report
}

// CollectSnapshotReport inspects snapshot metadata from the authoritative cluster leader.
func (d *DiagnosticsService) CollectSnapshotReport(ctx context.Context) SnapshotDiagnosticsReport {
	now := time.Now().UTC()
	report := SnapshotDiagnosticsReport{
		NodeID:              d.nodeID,
		SnapshotThreshold:   8192,
		SnapshotInterval:    "120s",
		SafeSnapshotStorage: true,
		GeneratedAt:         now,
	}

	if d.discovery == nil {
		return report
	}

	leaderURL, err := d.discovery.GetLeader(ctx)
	if err != nil || leaderURL == "" {
		return report
	}

	client := &http.Client{Timeout: 1500 * time.Millisecond}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, leaderURL+"/v1/cluster/status", nil)
	if err != nil {
		return report
	}

	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return report
	}
	defer resp.Body.Close()

	var result struct {
		Success bool `json:"success"`
		Data    struct {
			NodeID        string            `json:"node_id"`
			AppliedIndex  uint64            `json:"applied_index"`
			Stats         map[string]string `json:"stats"`
		} `json:"data"`
	}

	if json.NewDecoder(resp.Body).Decode(&result) == nil && result.Success {
		report.SnapshotAvailable = true
		if snapIdxStr, ok := result.Data.Stats["last_snapshot_index"]; ok {
			var snapIdx uint64
			_, _ = fmt.Sscanf(snapIdxStr, "%d", &snapIdx)
			report.LastSnapshotIndex = snapIdx
		}
		if snapTermStr, ok := result.Data.Stats["last_snapshot_term"]; ok {
			var snapTerm uint64
			_, _ = fmt.Sscanf(snapTermStr, "%d", &snapTerm)
			report.LastSnapshotTerm = snapTerm
		}
	}

	return report
}

