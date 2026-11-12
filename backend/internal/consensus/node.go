package consensus

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
	"lockguard/backend/internal/lockstate"
)

// SnapshotInfo describes a stored Raft snapshot along with validated FSM summary metadata.
type SnapshotInfo struct {
	ID                string `json:"id"`
	Index             uint64 `json:"index"`
	Term              uint64 `json:"term"`
	Size              int64  `json:"size"`
	ResourcesCount    int    `json:"resources_count"`
	IdempotencyCount  int    `json:"idempotency_count"`
	LastAppliedUnixNs int64  `json:"last_applied_unix_nano"`
	Valid             bool   `json:"valid"`
}

// RaftNode encapsulates a single consensus engine instance backed by HashiCorp Raft.
type RaftNode struct {
	mu              sync.RWMutex
	closed          bool
	cfg             NodeConfig
	logger          *slog.Logger
	hclogLogger     hclog.Logger
	sm              *lockstate.MemoryStateMachine
	fsm             *LockFSM
	raft            *raft.Raft
	transport       raft.Transport
	partitionStream *PartitionableStreamLayer
	logStore        raft.LogStore
	stableStore     raft.StableStore
	snapStore       raft.SnapshotStore
	boltStore       *raftboltdb.BoltStore
	inmemTrans      *raft.InmemTransport
}

// NewRaftNode initializes and starts a RaftNode with the specified configuration and state machine.
func NewRaftNode(cfg NodeConfig, sm *lockstate.MemoryStateMachine) (*RaftNode, error) {
	if cfg.NodeID == "" {
		return nil, fmt.Errorf("Raft NodeID cannot be empty")
	}
	if sm == nil {
		sm = lockstate.New()
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	hclogLevel := hclog.Info
	switch strings.ToLower(cfg.LogLevel) {
	case "debug":
		hclogLevel = hclog.Debug
	case "warn", "warning":
		hclogLevel = hclog.Warn
	case "error":
		hclogLevel = hclog.Error
	}

	hclogLogger := hclog.New(&hclog.LoggerOptions{
		Name:   "raft-" + cfg.NodeID,
		Level:  hclogLevel,
		Output: os.Stdout,
	})

	node := &RaftNode{
		cfg:         cfg,
		logger:      logger,
		hclogLogger: hclogLogger,
		sm:          sm,
		fsm:         NewLockFSM(sm, logger),
	}

	// 1. Initialize Transport
	var trans raft.Transport
	if cfg.Inmem {
		var addr raft.ServerAddress
		if cfg.AdvertiseAddr != "" {
			addr = raft.ServerAddress(cfg.AdvertiseAddr)
		} else {
			addr = raft.ServerAddress(cfg.NodeID)
		}
		_, inmemTrans := raft.NewInmemTransport(addr)
		node.inmemTrans = inmemTrans
		trans = inmemTrans
	} else {
		advAddr, err := net.ResolveTCPAddr("tcp", cfg.AdvertiseAddr)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve advertise address %q: %w", cfg.AdvertiseAddr, err)
		}

		if cfg.EnableTLS {
			tlsConf, err := BuildTLSConfig(cfg.TLSCertFile, cfg.TLSKeyFile, cfg.TLSCAFile)
			if err != nil {
				return nil, fmt.Errorf("failed to initialize Raft TLS configuration: %w", err)
			}
			stream, err := NewTLSStreamLayer(cfg.BindAddr, advAddr, tlsConf)
			if err != nil {
				return nil, fmt.Errorf("failed to create Raft TLS stream layer on %q: %w", cfg.BindAddr, err)
			}
			pStream := NewPartitionableStreamLayer(stream)
			node.partitionStream = pStream
			trans = raft.NewNetworkTransportWithLogger(pStream, 5, 10*time.Second, hclogLogger)
		} else {
			stream, err := NewPlainTCPStreamLayer(cfg.BindAddr, advAddr)
			if err != nil {
				return nil, fmt.Errorf("failed to create Raft TCP stream layer on %q: %w", cfg.BindAddr, err)
			}
			pStream := NewPartitionableStreamLayer(stream)
			node.partitionStream = pStream
			trans = raft.NewNetworkTransportWithLogger(pStream, 5, 10*time.Second, hclogLogger)
		}
	}
	node.transport = trans

	// 2. Initialize Storage (LogStore, StableStore, SnapshotStore)
	if cfg.Inmem {
		inmemStore := raft.NewInmemStore()
		node.logStore = inmemStore
		node.stableStore = inmemStore
		node.snapStore = raft.NewInmemSnapshotStore()
	} else {
		if err := os.MkdirAll(cfg.DataDir, 0750); err != nil {
			return nil, fmt.Errorf("failed to create raft data directory %q: %w", cfg.DataDir, err)
		}

		boltPath := filepath.Join(cfg.DataDir, "raft.db")
		boltStore, err := raftboltdb.NewBoltStore(boltPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open bolt store at %q: %w", boltPath, err)
		}
		node.boltStore = boltStore
		node.logStore = boltStore
		node.stableStore = boltStore

		retain := cfg.RetainSnapshots
		if retain < 1 {
			retain = 2
		}
		snapDir := filepath.Join(cfg.DataDir, "snapshots")
		snapStore, err := raft.NewFileSnapshotStoreWithLogger(snapDir, retain, hclogLogger)
		if err != nil {
			_ = boltStore.Close()
			return nil, fmt.Errorf("failed to create file snapshot store at %q: %w", snapDir, err)
		}
		node.snapStore = snapStore
	}

	// 3. Raft Protocol Configuration
	raftConf := raft.DefaultConfig()
	raftConf.LocalID = raft.ServerID(cfg.NodeID)
	raftConf.Logger = hclogLogger

	if cfg.HeartbeatTimeout > 0 {
		raftConf.HeartbeatTimeout = cfg.HeartbeatTimeout
	}
	if cfg.ElectionTimeout > 0 {
		raftConf.ElectionTimeout = cfg.ElectionTimeout
	}
	if cfg.CommitTimeout > 0 {
		raftConf.CommitTimeout = cfg.CommitTimeout
	}
	if cfg.LeaderLeaseTimeout > 0 {
		raftConf.LeaderLeaseTimeout = cfg.LeaderLeaseTimeout
	}
	if cfg.SnapshotInterval > 0 {
		raftConf.SnapshotInterval = cfg.SnapshotInterval
	}
	if cfg.SnapshotThreshold > 0 {
		raftConf.SnapshotThreshold = cfg.SnapshotThreshold
	}

	// 4. Cluster Bootstrap Evaluation
	hasState, err := raft.HasExistingState(node.logStore, node.stableStore, node.snapStore)
	if err != nil {
		node.cleanup()
		return nil, fmt.Errorf("failed to check for existing Raft state: %w", err)
	}

	if !hasState && cfg.BootstrapCluster {
		var servers []raft.Server
		if len(cfg.BootstrapServers) > 0 {
			servers = cfg.BootstrapServers
		} else if len(cfg.ClusterNodes) > 0 {
			parsedServers, err := ParseClusterServers(cfg.ClusterNodes)
			if err != nil {
				node.cleanup()
				return nil, fmt.Errorf("failed to parse bootstrap cluster servers: %w", err)
			}
			servers = parsedServers
		} else {
			// Single node bootstrap fallback
			var addr raft.ServerAddress
			if cfg.AdvertiseAddr != "" {
				addr = raft.ServerAddress(cfg.AdvertiseAddr)
			} else {
				addr = raft.ServerAddress(trans.LocalAddr())
			}
			servers = []raft.Server{
				{
					ID:       raft.ServerID(cfg.NodeID),
					Address:  addr,
					Suffrage: raft.Voter,
				},
			}
		}

		clusterConfig := raft.Configuration{Servers: servers}
		logger.Info("bootstrapping_raft_cluster",
			slog.String("node_id", cfg.NodeID),
			slog.Int("servers_count", len(servers)),
		)

		if err := raft.BootstrapCluster(raftConf, node.logStore, node.stableStore, node.snapStore, trans, clusterConfig); err != nil {
			node.cleanup()
			return nil, fmt.Errorf("failed to bootstrap Raft cluster: %w", err)
		}
	}

	// 5. Instantiate Raft
	r, err := raft.NewRaft(raftConf, node.fsm, node.logStore, node.stableStore, node.snapStore, trans)
	if err != nil {
		node.cleanup()
		return nil, fmt.Errorf("failed to instantiate Raft: %w", err)
	}
	node.raft = r

	logger.Info("raft_node_initialized",
		slog.String("node_id", cfg.NodeID),
		slog.String("bind_addr", cfg.BindAddr),
		slog.String("advertise_addr", cfg.AdvertiseAddr),
	)

	return node, nil
}

// InmemTransport returns the underlying inmem transport if running in Inmem mode.
func (n *RaftNode) InmemTransport() *raft.InmemTransport {
	return n.inmemTrans
}

// StateMachine returns the underlying deterministic lock state machine.
func (n *RaftNode) StateMachine() *lockstate.MemoryStateMachine {
	return n.sm
}

// State returns the current Raft operational state (Leader, Follower, Candidate, Shutdown).
func (n *RaftNode) State() raft.RaftState {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.closed || n.raft == nil {
		return raft.Shutdown
	}
	return n.raft.State()
}

// IsLeader reports whether this node is the active cluster leader.
func (n *RaftNode) IsLeader() bool {
	return n.State() == raft.Leader
}

// Leader returns the address and server ID of the currently known cluster leader.
func (n *RaftNode) Leader() (raft.ServerAddress, raft.ServerID) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.closed || n.raft == nil {
		return "", ""
	}
	return n.raft.LeaderWithID()
}

// VerifyLeader confirms this node is still the authoritative leader with majority quorum.
// Fails closed if leader status has been lost.
func (n *RaftNode) VerifyLeader() error {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.closed || n.raft == nil {
		return ErrNodeClosed
	}

	if n.raft.State() != raft.Leader {
		addr, id := n.raft.LeaderWithID()
		return &NotLeaderError{LeaderID: string(id), LeaderAddress: string(addr)}
	}

	future := n.raft.VerifyLeader()
	if err := future.Error(); err != nil {
		addr, id := n.raft.LeaderWithID()
		return fmt.Errorf("%w: %v (leader was %s at %s)", ErrNotLeader, err, id, addr)
	}
	return nil
}

// Barrier blocks until all preceding Raft log entries have been applied to the FSM.
func (n *RaftNode) Barrier(timeout time.Duration) error {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.closed || n.raft == nil {
		return ErrNodeClosed
	}

	future := n.raft.Barrier(timeout)
	return future.Error()
}

// ApplyCommand proposes a state-changing lock command through Raft consensus.
// If this node is not the leader, it immediately rejects with NotLeaderError containing leader hints.
func (n *RaftNode) ApplyCommand(ctx context.Context, cmd lockstate.Command) (lockstate.Result, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.closed || n.raft == nil {
		return lockstate.Result{}, ErrNodeClosed
	}

	if n.raft.State() != raft.Leader {
		addr, id := n.raft.LeaderWithID()
		return lockstate.Result{}, &NotLeaderError{
			LeaderID:      string(id),
			LeaderAddress: string(addr),
		}
	}

	encoded, err := EncodeCommand(cmd)
	if err != nil {
		return lockstate.Result{}, fmt.Errorf("failed to encode command: %w", err)
	}

	// Calculate appropriate timeout
	timeout := 5 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > 0 && remaining < timeout {
			timeout = remaining
		}
	}

	applyFuture := n.raft.Apply(encoded, timeout)

	select {
	case <-ctx.Done():
		return lockstate.Result{}, ctx.Err()
	default:
	}

	if err := applyFuture.Error(); err != nil {
		if err == raft.ErrNotLeader {
			addr, id := n.raft.LeaderWithID()
			return lockstate.Result{}, &NotLeaderError{LeaderID: string(id), LeaderAddress: string(addr)}
		}
		if strings.Contains(strings.ToLower(err.Error()), "timeout") {
			return lockstate.Result{}, fmt.Errorf("%w: %v", ErrQuorumLoss, err)
		}
		return lockstate.Result{}, err
	}

	resp := applyFuture.Response()
	result, ok := resp.(lockstate.Result)
	if !ok {
		return lockstate.Result{}, fmt.Errorf("unexpected FSM apply response type: %T", resp)
	}

	return result, nil
}

// VerifyAuthoritativeRead executes the strict linearizable read sequence required for consensus correctness:
// 1. Call HashiCorp Raft VerifyLeader() to confirm active term leadership and majority quorum connectivity.
// 2. Wait for the verify future and check its error (fail closed if deposed or partitioned).
// 3. Call HashiCorp Raft Barrier() to ensure all preceding committed log entries have been applied to the FSM.
// 4. Wait for the barrier future and check its error.
func (n *RaftNode) VerifyAuthoritativeRead(timeout time.Duration) error {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.closed || n.raft == nil {
		return ErrNodeClosed
	}

	if n.raft.State() != raft.Leader {
		addr, id := n.raft.LeaderWithID()
		return &NotLeaderError{LeaderID: string(id), LeaderAddress: string(addr)}
	}

	// Step 1 & 2: Verify active leader authority with a majority quorum of peers
	verifyFuture := n.raft.VerifyLeader()
	if err := verifyFuture.Error(); err != nil {
		addr, id := n.raft.LeaderWithID()
		return fmt.Errorf("%w: leadership verification failed: %v (leader was %s at %s)", ErrNotLeader, err, id, addr)
	}

	// Step 3 & 4: Barrier ensures local FSM has caught up to the latest commit index
	barrierTimeout := timeout
	if barrierTimeout <= 0 {
		barrierTimeout = 2 * time.Second
	}
	barrierFuture := n.raft.Barrier(barrierTimeout)
	if err := barrierFuture.Error(); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "timeout") {
			return fmt.Errorf("%w: read barrier timed out: %v", ErrQuorumLoss, err)
		}
		return fmt.Errorf("read barrier failed: %w", err)
	}

	return nil
}

// GetLock retrieves an active lock record for a resource.
// If linearizable is true, it strictly executes VerifyLeader() followed by Barrier() before reading the FSM.
// If linearizable is false, it performs a local diagnostic read which is explicitly non-authoritative and potentially stale.
func (n *RaftNode) GetLock(resourceName string, linearizable bool) (lockstate.LockRecord, bool, error) {
	return n.GetLockAt(resourceName, linearizable, 0)
}

// GetLockAt retrieves an active lock record evaluated against nowUnixNano (or only lastAppliedUnixNano when 0).
func (n *RaftNode) GetLockAt(resourceName string, linearizable bool, nowUnixNano int64) (lockstate.LockRecord, bool, error) {
	if linearizable {
		if err := n.VerifyAuthoritativeRead(2 * time.Second); err != nil {
			return lockstate.LockRecord{}, false, err
		}
	}
	rec, found := n.sm.GetLockAt(resourceName, nowUnixNano)
	return rec, found, nil
}

// ListLocks returns all active locks across resources.
// If linearizable is true, it strictly executes VerifyLeader() followed by Barrier() before reading the FSM.
// If linearizable is false, it performs a local diagnostic read which is explicitly non-authoritative and potentially stale.
func (n *RaftNode) ListLocks(linearizable bool) ([]lockstate.LockRecord, error) {
	return n.ListLocksAt(linearizable, 0, false)
}

// ListLocksAt returns locks evaluated against nowUnixNano, optionally including expired un-reaped records with Status=EXPIRED.
func (n *RaftNode) ListLocksAt(linearizable bool, nowUnixNano int64, includeExpired bool) ([]lockstate.LockRecord, error) {
	if linearizable {
		if err := n.VerifyAuthoritativeRead(2 * time.Second); err != nil {
			return nil, err
		}
	}
	return n.sm.ListLocksAt(nowUnixNano, includeExpired), nil
}

// ListExpiredLocksAt returns local FSM lock records whose leases have expired as of nowUnixNano.
func (n *RaftNode) ListExpiredLocksAt(nowUnixNano int64) []lockstate.LockRecord {
	return n.sm.ListExpiredLocksAt(nowUnixNano)
}

// GetResourceState returns the complete consensus resource state (active lock and highest token).
// If linearizable is true, it strictly executes VerifyLeader() followed by Barrier() before reading the FSM.
// If linearizable is false, it performs a local diagnostic read which is explicitly non-authoritative and potentially stale.
func (n *RaftNode) GetResourceState(resourceName string, linearizable bool) (lockstate.ResourceState, bool, error) {
	if linearizable {
		if err := n.VerifyAuthoritativeRead(2 * time.Second); err != nil {
			return lockstate.ResourceState{}, false, err
		}
	}
	rs, found := n.sm.GetResourceState(resourceName)
	return rs, found, nil
}

// GetConfiguration returns the active Raft cluster membership configuration.
func (n *RaftNode) GetConfiguration() (raft.Configuration, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.closed || n.raft == nil {
		return raft.Configuration{}, ErrNodeClosed
	}

	future := n.raft.GetConfiguration()
	if err := future.Error(); err != nil {
		return raft.Configuration{}, err
	}
	return future.Configuration(), nil
}

// Snapshot explicitly triggers a Raft log snapshot.
func (n *RaftNode) Snapshot() error {
	n.mu.RLock()
	r := n.raft
	closed := n.closed
	n.mu.RUnlock()
	if closed || r == nil {
		return ErrNodeClosed
	}
	return r.Snapshot().Error()
}

// SetPartitioned toggles simulated Raft transport isolation for this node.
func (n *RaftNode) SetPartitioned(isolated bool) error {
	n.mu.RLock()
	closed := n.closed
	ps := n.partitionStream
	n.mu.RUnlock()

	if closed {
		return ErrNodeClosed
	}
	if ps == nil {
		return fmt.Errorf("simulated network partition is only supported on TCP/TLS stream transports")
	}
	ps.SetIsolated(isolated)
	return nil
}

// IsPartitioned reports whether this node's Raft transport is currently isolated.
func (n *RaftNode) IsPartitioned() bool {
	n.mu.RLock()
	ps := n.partitionStream
	n.mu.RUnlock()
	if ps == nil {
		return false
	}
	return ps.IsIsolated()
}

// ListSnapshots returns metadata and validation status for all persisted Raft snapshots on this node.
func (n *RaftNode) ListSnapshots() ([]SnapshotInfo, error) {
	n.mu.RLock()
	closed := n.closed
	store := n.snapStore
	n.mu.RUnlock()

	if closed || store == nil {
		return nil, ErrNodeClosed
	}

	metas, err := store.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list snapshots: %w", err)
	}

	infos := make([]SnapshotInfo, 0, len(metas))
	for _, meta := range metas {
		info := SnapshotInfo{
			ID:    meta.ID,
			Index: meta.Index,
			Term:  meta.Term,
			Size:  meta.Size,
		}
		if _, rc, err := store.Open(meta.ID); err == nil {
			raw, readErr := io.ReadAll(rc)
			_ = rc.Close()
			if readErr == nil {
				if snapData, decErr := lockstate.DecodeSnapshot(bytes.NewReader(raw)); decErr == nil {
					if valErr := lockstate.New().RestoreSnapshot(snapData); valErr == nil {
						info.Valid = true
						info.ResourcesCount = len(snapData.Resources)
						info.IdempotencyCount = len(snapData.IdempotencyRecords)
						info.LastAppliedUnixNs = snapData.LastAppliedUnixNano
					}
				}
			}
		}
		infos = append(infos, info)
	}

	return infos, nil
}

// OpenSnapshotRaw loads and validates a stored snapshot by ID (or "latest").
func (n *RaftNode) OpenSnapshotRaw(snapshotID string) (*raft.SnapshotMeta, []byte, *SnapshotInfo, error) {
	n.mu.RLock()
	closed := n.closed
	store := n.snapStore
	n.mu.RUnlock()

	if closed || store == nil {
		return nil, nil, nil, ErrNodeClosed
	}

	targetID := strings.TrimSpace(snapshotID)
	if targetID == "" || strings.EqualFold(targetID, "latest") {
		metas, err := store.List()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to list snapshots: %w", err)
		}
		if len(metas) == 0 {
			return nil, nil, nil, fmt.Errorf("no snapshots available on node %q", n.cfg.NodeID)
		}
		targetID = metas[0].ID
	}

	meta, rc, err := store.Open(targetID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("snapshot %q not found: %w", targetID, err)
	}
	raw, readErr := io.ReadAll(rc)
	_ = rc.Close()
	if readErr != nil {
		return nil, nil, nil, fmt.Errorf("failed to read snapshot %q: %w", targetID, readErr)
	}

	snapData, err := lockstate.DecodeSnapshot(bytes.NewReader(raw))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("invalid snapshot %q: %w", targetID, err)
	}
	if err := lockstate.New().RestoreSnapshot(snapData); err != nil {
		return nil, nil, nil, fmt.Errorf("corrupt snapshot %q: %w", targetID, err)
	}

	info := &SnapshotInfo{
		ID:                meta.ID,
		Index:             meta.Index,
		Term:              meta.Term,
		Size:              int64(len(raw)),
		ResourcesCount:    len(snapData.Resources),
		IdempotencyCount:  len(snapData.IdempotencyRecords),
		LastAppliedUnixNs: snapData.LastAppliedUnixNano,
		Valid:             true,
	}

	return meta, raw, info, nil
}

// RestoreSnapshotByID validates and applies an existing local Raft snapshot cluster-wide via raft.Raft.Restore.
func (n *RaftNode) RestoreSnapshotByID(snapshotID string, timeout time.Duration) (*SnapshotInfo, error) {
	meta, raw, info, err := n.OpenSnapshotRaw(snapshotID)
	if err != nil {
		return nil, err
	}
	if err := n.RestoreSnapshotBytes(meta, raw, timeout); err != nil {
		return nil, err
	}
	return info, nil
}

// RestoreSnapshotBytes validates snapshot data and restores the cluster FSM on the current Raft leader.
func (n *RaftNode) RestoreSnapshotBytes(meta *raft.SnapshotMeta, raw []byte, timeout time.Duration) error {
	if meta == nil {
		return fmt.Errorf("snapshot metadata is required")
	}
	snapData, err := lockstate.DecodeSnapshot(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("invalid snapshot payload: %w", err)
	}
	if err := lockstate.New().RestoreSnapshot(snapData); err != nil {
		return fmt.Errorf("corrupt snapshot state: %w", err)
	}

	n.mu.RLock()
	closed := n.closed
	r := n.raft
	n.mu.RUnlock()

	if closed || r == nil {
		return ErrNodeClosed
	}
	if r.State() != raft.Leader {
		addr, id := r.LeaderWithID()
		return &NotLeaderError{LeaderID: string(id), LeaderAddress: string(addr)}
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	// Ensure Size reflects exact payload length before streaming through Raft.
	metaCopy := *meta
	metaCopy.Size = int64(len(raw))

	if err := r.Restore(&metaCopy, bytes.NewReader(raw), timeout); err != nil {
		return fmt.Errorf("raft snapshot restore failed: %w", err)
	}
	if err := r.Barrier(timeout).Error(); err != nil {
		return fmt.Errorf("post-restore barrier failed: %w", err)
	}
	return nil
}

// LeadershipTransfer initiates a Raft leadership transfer to another peer.
func (n *RaftNode) LeadershipTransfer() error {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.closed || n.raft == nil {
		return ErrNodeClosed
	}
	return n.raft.LeadershipTransfer().Error()
}

// Stats returns the raw Raft engine statistics map.
func (n *RaftNode) Stats() map[string]string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.closed || n.raft == nil {
		return map[string]string{"state": "Shutdown"}
	}
	return n.raft.Stats()
}

// AppliedIndex returns the highest log index applied to the FSM.
func (n *RaftNode) AppliedIndex() uint64 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.closed || n.raft == nil {
		return 0
	}
	return n.raft.AppliedIndex()
}

// LastIndex returns the highest log index received by the Raft engine.
func (n *RaftNode) LastIndex() uint64 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.closed || n.raft == nil {
		return 0
	}
	return n.raft.LastIndex()
}

// CommitIndex returns the highest log index committed by Raft quorum.
func (n *RaftNode) CommitIndex() uint64 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.closed || n.raft == nil {
		return 0
	}
	return n.raft.CommitIndex()
}

// CurrentTerm returns the node's current Raft election term.
func (n *RaftNode) CurrentTerm() uint64 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.closed || n.raft == nil {
		return 0
	}
	return n.raft.CurrentTerm()
}

// Config returns a copy of the NodeConfig used to start this Raft node.
func (n *RaftNode) Config() NodeConfig {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.cfg
}

// Close gracefully stops the Raft consensus engine and releases storage/network resources.
func (n *RaftNode) Close() error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.closed {
		return nil
	}
	n.closed = true

	var errs []string

	if n.raft != nil {
		shutdownFuture := n.raft.Shutdown()
		if err := shutdownFuture.Error(); err != nil {
			errs = append(errs, fmt.Sprintf("raft shutdown error: %v", err))
		}
	}

	if closer, ok := n.transport.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("transport close error: %v", err))
		}
	}

	if n.boltStore != nil {
		if err := n.boltStore.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("bolt store close error: %v", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func (n *RaftNode) cleanup() {
	if closer, ok := n.transport.(io.Closer); ok {
		_ = closer.Close()
	}
	if n.boltStore != nil {
		_ = n.boltStore.Close()
	}
}
