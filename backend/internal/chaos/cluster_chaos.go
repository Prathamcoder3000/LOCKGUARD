package chaos

import (
	"context"
	"fmt"
	"sync"

	"github.com/hashicorp/raft"
	"lockguard/backend/internal/consensus"
	"lockguard/backend/internal/persistence"
)

// ManagedNode pairs a RaftNode with its server ID and address.
type ManagedNode struct {
	ID       string
	Address  string
	Node     *consensus.RaftNode
	Endpoint string
}

// ClusterChaosManager exposes high-level, reversible cluster fault injections.
type ClusterChaosManager struct {
	mu         sync.RWMutex
	controller *Controller
	nodes      map[string]*ManagedNode
}

// NewClusterChaosManager creates a manager for orchestrating cluster faults.
func NewClusterChaosManager(ctrl *Controller) *ClusterChaosManager {
	return &ClusterChaosManager{
		controller: ctrl,
		nodes:      make(map[string]*ManagedNode),
	}
}

// RegisterNode adds a node to the managed cluster catalog.
func (m *ClusterChaosManager) RegisterNode(node *ManagedNode) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodes[node.ID] = node
}

// PartitionNode isolates a single node completely from all other nodes in the cluster.
// In Raft, isolating a follower tests majority continuity; isolating the leader tests failover.
func (m *ClusterChaosManager) PartitionNode(ctx context.Context, targetNodeID string) (string, func(ctx context.Context) error, error) {
	m.mu.RLock()
	target, exists := m.nodes[targetNodeID]
	if !exists {
		m.mu.RUnlock()
		return "", nil, fmt.Errorf("node %q not found in managed cluster", targetNodeID)
	}

	peers := make([]*ManagedNode, 0, len(m.nodes)-1)
	for id, n := range m.nodes {
		if id != targetNodeID {
			peers = append(peers, n)
		}
	}
	m.mu.RUnlock()

	applyFn := func(ctx context.Context) error {
		// Sever inmem transport connections bidirectionally
		targetTrans := target.Node.InmemTransport()
		if targetTrans == nil {
			return fmt.Errorf("node %s does not support inmem transport partitioning", targetNodeID)
		}

		targetTrans.DisconnectAll()
		for _, peer := range peers {
			pTrans := peer.Node.InmemTransport()
			if pTrans != nil {
				pTrans.Disconnect(raft.ServerAddress(target.ID))
			}
		}
		return nil
	}

	revertFn := func(ctx context.Context) error {
		targetTrans := target.Node.InmemTransport()
		if targetTrans == nil {
			return nil
		}

		for _, peer := range peers {
			pTrans := peer.Node.InmemTransport()
			if pTrans != nil {
				targetTrans.Connect(raft.ServerAddress(peer.ID), pTrans)
				pTrans.Connect(raft.ServerAddress(target.ID), targetTrans)
			}
		}
		return nil
	}

	return m.controller.Inject(
		ctx,
		FaultNetworkPartition,
		targetNodeID,
		map[string]interface{}{"isolated_node": targetNodeID, "peer_count": len(peers)},
		fmt.Sprintf("Isolate node %s from majority peer cluster", targetNodeID),
		applyFn,
		revertFn,
	)
}

// BidirectionalPartition creates two disjoint network partitions (Group A and Group B).
// Nodes within the same group can communicate, but cross-group messages are dropped.
func (m *ClusterChaosManager) BidirectionalPartition(ctx context.Context, groupA, groupB []string) (string, func(ctx context.Context) error, error) {
	m.mu.RLock()
	nodesA := make([]*ManagedNode, 0, len(groupA))
	for _, id := range groupA {
		if n, ok := m.nodes[id]; ok {
			nodesA = append(nodesA, n)
		}
	}
	nodesB := make([]*ManagedNode, 0, len(groupB))
	for _, id := range groupB {
		if n, ok := m.nodes[id]; ok {
			nodesB = append(nodesB, n)
		}
	}
	m.mu.RUnlock()

	applyFn := func(ctx context.Context) error {
		for _, a := range nodesA {
			tA := a.Node.InmemTransport()
			if tA == nil {
				continue
			}
			for _, b := range nodesB {
				tB := b.Node.InmemTransport()
				if tB == nil {
					continue
				}
				tA.Disconnect(raft.ServerAddress(b.ID))
				tB.Disconnect(raft.ServerAddress(a.ID))
			}
		}
		return nil
	}

	revertFn := func(ctx context.Context) error {
		for _, a := range nodesA {
			tA := a.Node.InmemTransport()
			if tA == nil {
				continue
			}
			for _, b := range nodesB {
				tB := b.Node.InmemTransport()
				if tB == nil {
					continue
				}
				tA.Connect(raft.ServerAddress(b.ID), tB)
				tB.Connect(raft.ServerAddress(a.ID), tA)
			}
		}
		return nil
	}

	target := fmt.Sprintf("[%s] vs [%s]", fmt.Sprint(groupA), fmt.Sprint(groupB))
	return m.controller.Inject(
		ctx,
		FaultNetworkPartition,
		target,
		map[string]interface{}{"group_a": groupA, "group_b": groupB},
		"Bidirectional network partition between groups",
		applyFn,
		revertFn,
	)
}

// InjectDatabaseOutage closes and severs a PostgreSQL connection pool, returning a recovery closure.
func (m *ClusterChaosManager) InjectDatabaseOutage(ctx context.Context, db *persistence.Database, reconnectFn func() (*persistence.Database, error)) (string, func(ctx context.Context) error, error) {
	if db == nil {
		return "", nil, fmt.Errorf("database handle is nil")
	}

	applyFn := func(ctx context.Context) error {
		return db.Close()
	}

	revertFn := func(ctx context.Context) error {
		if reconnectFn != nil {
			_, err := reconnectFn()
			return err
		}
		return nil
	}

	return m.controller.Inject(
		ctx,
		FaultDatabaseOutage,
		"postgresql",
		map[string]interface{}{"subsystem": "persistence"},
		"Simulated database connection severance",
		applyFn,
		revertFn,
	)
}
