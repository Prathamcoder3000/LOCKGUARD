package consensus

import (
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

// RaftNode encapsulates a single consensus engine instance backed by HashiCorp Raft.
type RaftNode struct {
	mu          sync.RWMutex
	closed      bool
	cfg         NodeConfig
	logger      *slog.Logger
	hclogLogger hclog.Logger
	sm          *lockstate.MemoryStateMachine
	fsm         *LockFSM
	raft        *raft.Raft
	transport   raft.Transport
	logStore    raft.LogStore
	stableStore raft.StableStore
	snapStore   raft.SnapshotStore
	boltStore   *raftboltdb.BoltStore
	inmemTrans  *raft.InmemTransport
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
			trans = raft.NewNetworkTransportWithLogger(stream, 5, 10*time.Second, hclogLogger)
		} else {
			tcpTrans, err := raft.NewTCPTransportWithLogger(
				cfg.BindAddr,
				advAddr,
				5,
				10*time.Second,
				hclogLogger,
			)
			if err != nil {
				return nil, fmt.Errorf("failed to create TCP transport on %q: %w", cfg.BindAddr, err)
			}
			trans = tcpTrans
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
	if linearizable {
		if err := n.VerifyAuthoritativeRead(2 * time.Second); err != nil {
			return lockstate.LockRecord{}, false, err
		}
	}
	rec, found := n.sm.GetLock(resourceName)
	return rec, found, nil
}

// ListLocks returns all active locks across resources.
// If linearizable is true, it strictly executes VerifyLeader() followed by Barrier() before reading the FSM.
// If linearizable is false, it performs a local diagnostic read which is explicitly non-authoritative and potentially stale.
func (n *RaftNode) ListLocks(linearizable bool) ([]lockstate.LockRecord, error) {
	if linearizable {
		if err := n.VerifyAuthoritativeRead(2 * time.Second); err != nil {
			return nil, err
		}
	}
	return n.sm.ListLocks(), nil
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
	defer n.mu.RUnlock()
	if n.closed || n.raft == nil {
		return ErrNodeClosed
	}
	return n.raft.Snapshot().Error()
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
