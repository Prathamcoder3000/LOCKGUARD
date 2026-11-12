package recovery

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"lockguard/backend/internal/audit"
	"lockguard/backend/internal/chaos"
	"lockguard/backend/internal/consensus"
	"lockguard/backend/internal/gateway"
	"lockguard/backend/internal/metrics"
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
	environment string
	discovery   *gateway.LeaderDiscovery
	db          *persistence.Database
	migrator    *persistence.Migrator
	pipeline    *audit.Pipeline
	metricsReg  *metrics.Registry
	chaosCtrl   *chaos.Controller
	tracker     *RecoveryTracker
	mu          sync.RWMutex
}

// NewDiagnosticsService constructs a new DiagnosticsService.
func NewDiagnosticsService(
	nodeID, serviceName string,
	discovery *gateway.LeaderDiscovery,
	db *persistence.Database,
	migrator *persistence.Migrator,
	pipeline *audit.Pipeline,
) *DiagnosticsService {
	d := &DiagnosticsService{
		nodeID:      nodeID,
		serviceName: serviceName,
		environment: "development",
		discovery:   discovery,
		db:          db,
		migrator:    migrator,
		pipeline:    pipeline,
		metricsReg:  metrics.DefaultRegistry,
	}
	d.tracker = newRecoveryTracker(d)
	d.initChaosController()
	return d
}

// SetDatabase updates the database and migrator handles for diagnostics inspection.
func (d *DiagnosticsService) SetDatabase(db *persistence.Database, migrator *persistence.Migrator) {
	d.mu.Lock()
	d.db = db
	d.migrator = migrator
	d.mu.Unlock()
}

// SetMetricsRegistry binds the authoritative Prometheus registry used by recovery tracking and chaos tests.
func (d *DiagnosticsService) SetMetricsRegistry(reg *metrics.Registry) {
	if reg == nil {
		return
	}
	d.mu.Lock()
	d.metricsReg = reg
	d.initChaosControllerLocked()
	d.mu.Unlock()
}

// SetEnvironment configures deployment environment ("development", "staging", "production").
// Non-production environments permit controlled, admin-confirmed resilience drills.
func (d *DiagnosticsService) SetEnvironment(env string) {
	d.mu.Lock()
	if strings.TrimSpace(env) != "" {
		d.environment = strings.TrimSpace(env)
	}
	d.initChaosControllerLocked()
	d.mu.Unlock()
}

func (d *DiagnosticsService) initChaosController() {
	d.mu.Lock()
	d.initChaosControllerLocked()
	d.mu.Unlock()
}

func (d *DiagnosticsService) initChaosControllerLocked() {
	isProd := strings.EqualFold(d.environment, "production") || strings.EqualFold(d.environment, "prod")
	ctrl := chaos.NewController(chaos.Config{
		Enabled:     !isProd,
		Environment: d.environment,
		Logger:      slog.Default(),
		Metrics:     d.regLocked(),
	})
	d.chaosCtrl = ctrl
}

func (d *DiagnosticsService) regLocked() *metrics.Registry {
	if d.metricsReg != nil {
		return d.metricsReg
	}
	return metrics.DefaultRegistry
}

func (d *DiagnosticsService) reg() *metrics.Registry {
	d.mu.RLock()
	r := d.metricsReg
	d.mu.RUnlock()
	if r != nil {
		return r
	}
	return metrics.DefaultRegistry
}

func (d *DiagnosticsService) getChaosController() *chaos.Controller {
	d.mu.RLock()
	ctrl := d.chaosCtrl
	d.mu.RUnlock()
	return ctrl
}

// StartMonitor starts background polling to detect Raft leader changes and node outage/recovery durations.
func (d *DiagnosticsService) StartMonitor(ctx context.Context, interval time.Duration) {
	if d.tracker != nil {
		d.tracker.Start(ctx, interval)
	}
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
	NodeID            string `json:"node_id"`
	Endpoint          string `json:"endpoint"`
	Reachable         bool   `json:"reachable"`
	IsLeader          bool   `json:"is_leader"`
	Partitioned       bool   `json:"partitioned"`
	AppliedIndex      uint64 `json:"applied_index,omitempty"`
	CommitIndex       uint64 `json:"commit_index,omitempty"`
	LastIndex         uint64 `json:"last_index,omitempty"`
	CurrentTerm       uint64 `json:"current_term,omitempty"`
	RaftBindAddr      string `json:"raft_bind_addr,omitempty"`
	RaftAdvertiseAddr string `json:"raft_advertise_addr,omitempty"`
	DataDir           string `json:"data_dir,omitempty"`
	Role              string `json:"role"`
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
	NodeID              string                   `json:"node_id"`
	LeaderID            string                   `json:"leader_id,omitempty"`
	SnapshotAvailable   bool                     `json:"snapshot_available"`
	LastSnapshotIndex   uint64                   `json:"last_snapshot_index"`
	LastSnapshotTerm    uint64                   `json:"last_snapshot_term"`
	SnapshotThreshold   uint64                   `json:"snapshot_threshold"`
	SnapshotInterval    string                   `json:"snapshot_interval"`
	SafeSnapshotStorage bool                     `json:"safe_snapshot_storage"`
	Snapshots           []consensus.SnapshotInfo `json:"snapshots"`
	GeneratedAt         time.Time                `json:"generated_at"`
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

	available := make([]string, 0, len(endpoints))
	coordStatuses := make([]CoordinatorNodeStatus, len(endpoints))
	var currentLeaderID string
	var currentLeaderURL string

	probeTimeout := 800 * time.Millisecond
	client := &http.Client{Timeout: probeTimeout}

	var wg sync.WaitGroup
	for i, ep := range endpoints {
		wg.Add(1)
		go func(idx int, endpoint string) {
			defer wg.Done()

			initialNodeID := nodeIDFromEndpoint(endpoint)
			if d.tracker != nil {
				initialNodeID = d.tracker.ResolveNodeID(endpoint)
			}
			status := CoordinatorNodeStatus{
				NodeID:   initialNodeID,
				Endpoint: endpoint,
				Role:     "Unknown",
			}

			probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
			defer cancel()

			req, reqErr := http.NewRequestWithContext(probeCtx, http.MethodGet, endpoint+"/v1/cluster/status", nil)
			if reqErr == nil {
				resp, doErr := client.Do(req)
				if doErr == nil {
					if resp.StatusCode == http.StatusOK {
						var result struct {
							Success bool `json:"success"`
							Data    struct {
								NodeID            string `json:"node_id"`
								State             string `json:"state"`
								IsLeader          bool   `json:"is_leader"`
								LeaderID          string `json:"leader_id"`
								Partitioned       bool   `json:"partitioned"`
								AppliedIndex      uint64 `json:"applied_index"`
								CommitIndex       uint64 `json:"commit_index"`
								LastIndex         uint64 `json:"last_index"`
								CurrentTerm       uint64 `json:"current_term"`
								RaftBindAddr      string `json:"raft_bind_addr"`
								RaftAdvertiseAddr string `json:"raft_advertise_addr"`
								DataDir           string `json:"data_dir"`
							} `json:"data"`
						}
						if json.NewDecoder(resp.Body).Decode(&result) == nil && result.Success {
							status.Reachable = true
							if result.Data.NodeID != "" {
								status.NodeID = result.Data.NodeID
								if d.tracker != nil {
									d.tracker.RememberEndpointNodeID(endpoint, result.Data.NodeID)
								}
							}
							status.Partitioned = result.Data.Partitioned
							status.AppliedIndex = result.Data.AppliedIndex
							status.CommitIndex = result.Data.CommitIndex
							status.LastIndex = result.Data.LastIndex
							status.CurrentTerm = result.Data.CurrentTerm
							status.RaftBindAddr = result.Data.RaftBindAddr
							status.RaftAdvertiseAddr = result.Data.RaftAdvertiseAddr
							status.DataDir = result.Data.DataDir
							if status.Partitioned {
								status.IsLeader = false
								status.Role = "Partitioned"
							} else {
								status.IsLeader = result.Data.IsLeader
								status.Role = result.Data.State
							}
						}
					}
					_ = resp.Body.Close()
				}
			}

			coordStatuses[idx] = status
		}(i, ep)
	}
	wg.Wait()

	for _, st := range coordStatuses {
		if st.Reachable && !st.Partitioned {
			available = append(available, st.NodeID)
			if st.IsLeader {
				currentLeaderID = st.NodeID
				currentLeaderURL = st.Endpoint
			}
		}
	}

	// Majority quorum rule for N nodes: threshold = floor(N/2) + 1
	majorityThreshold := (report.TotalNodes / 2) + 1
	if len(available) >= majorityThreshold && currentLeaderURL == "" && d.discovery != nil {
		if leaderURL, err := d.discovery.GetLeader(ctx); err == nil && leaderURL != "" {
			currentLeaderURL = leaderURL
			for _, st := range coordStatuses {
				if st.Endpoint == leaderURL && st.Reachable && !st.Partitioned {
					currentLeaderID = st.NodeID
					break
				}
			}
		}
	}

	report.AvailableNodes = available
	report.Coordinators = coordStatuses
	report.CurrentLeaderID = currentLeaderID
	report.CurrentLeaderURL = currentLeaderURL

	if len(available) >= majorityThreshold && currentLeaderURL != "" {
		report.QuorumAvailable = true
		report.QuorumState = "ESTABLISHED"
	} else {
		// Fail closed: do not present an active authoritative leader when quorum is lost
		report.QuorumAvailable = false
		report.QuorumState = "QUORUM_LOST"
		report.CurrentLeaderID = ""
		report.CurrentLeaderURL = ""
	}

	reg := d.reg()
	reg.SetQuorumAvailable(report.QuorumAvailable)
	reg.SetClusterNodesAvailable(len(available))
	if d.tracker != nil {
		d.tracker.ObserveClusterReport(report)
	}

	return report
}

func nodeIDFromEndpoint(endpoint string) string {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return endpoint
}

// CollectSnapshotReport inspects snapshot metadata and restorable snapshots from the authoritative cluster leader.
func (d *DiagnosticsService) CollectSnapshotReport(ctx context.Context) SnapshotDiagnosticsReport {
	now := time.Now().UTC()
	report := SnapshotDiagnosticsReport{
		NodeID:              d.nodeID,
		SnapshotThreshold:   8192,
		SnapshotInterval:    "120s",
		SafeSnapshotStorage: true,
		Snapshots:           []consensus.SnapshotInfo{},
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
			NodeID       string            `json:"node_id"`
			AppliedIndex uint64            `json:"applied_index"`
			Stats        map[string]string `json:"stats"`
		} `json:"data"`
	}

	if json.NewDecoder(resp.Body).Decode(&result) == nil && result.Success {
		report.LeaderID = result.Data.NodeID
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
		report.SnapshotAvailable = report.LastSnapshotIndex > 0
	}

	// Collect restorable snapshot entries from the leader (and peer fallback if leader has none yet).
	snaps := d.fetchNodeSnapshots(ctx, client, leaderURL)
	if len(snaps) == 0 {
		for _, ep := range d.discovery.Endpoints() {
			if ep == leaderURL {
				continue
			}
			if peerSnaps := d.fetchNodeSnapshots(ctx, client, ep); len(peerSnaps) > 0 {
				snaps = peerSnaps
				break
			}
		}
	}
	if len(snaps) > 0 {
		report.Snapshots = snaps
		report.SnapshotAvailable = true
		if report.LastSnapshotIndex == 0 {
			report.LastSnapshotIndex = snaps[0].Index
			report.LastSnapshotTerm = snaps[0].Term
		}
	}

	return report
}

func (d *DiagnosticsService) fetchNodeSnapshots(ctx context.Context, client *http.Client, endpoint string) []consensus.SnapshotInfo {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/v1/cluster/snapshots", nil)
	if err != nil {
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Snapshots []consensus.SnapshotInfo `json:"snapshots"`
		} `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&payload) != nil || !payload.Success {
		return nil
	}
	return payload.Data.Snapshots
}

