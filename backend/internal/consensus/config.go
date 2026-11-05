package consensus

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/hashicorp/raft"
)

// NodeConfig holds all configuration options for a Raft consensus node.
type NodeConfig struct {
	NodeID             string
	BindAddr           string
	AdvertiseAddr      string
	DataDir            string
	ClusterNodes       []string
	HeartbeatTimeout   time.Duration
	ElectionTimeout    time.Duration
	CommitTimeout      time.Duration
	LeaderLeaseTimeout time.Duration
	SnapshotInterval   time.Duration
	SnapshotThreshold  uint64
	RetainSnapshots    int
	Inmem              bool
	LogLevel           string
	Logger             *slog.Logger

	// BootstrapCluster indicates if this node should execute BootstrapCluster if no state exists
	BootstrapCluster bool
	BootstrapServers []raft.Server

	// TLS configuration for peer consensus transport
	EnableTLS   bool
	TLSCertFile string
	TLSKeyFile  string
	TLSCAFile   string
}

// DefaultNodeConfig returns a safe default configuration for testing or local development.
func DefaultNodeConfig(nodeID, bindAddr, advAddr string) NodeConfig {
	return NodeConfig{
		NodeID:             nodeID,
		BindAddr:           bindAddr,
		AdvertiseAddr:      advAddr,
		DataDir:            fmt.Sprintf("./data/raft-%s", nodeID),
		HeartbeatTimeout:   1000 * time.Millisecond,
		ElectionTimeout:    1000 * time.Millisecond,
		CommitTimeout:      50 * time.Millisecond,
		LeaderLeaseTimeout: 500 * time.Millisecond,
		SnapshotInterval:   120 * time.Second,
		SnapshotThreshold:  8192,
		RetainSnapshots:    2,
		LogLevel:           "info",
		Logger:             slog.Default(),
	}
}

// ParseClusterServers converts a list of cluster node strings into raft.Server definitions.
// Supported formats:
// 1. "id=host:port" (e.g. "coordinator-a=coordinator-a:7000")
// 2. "host:port" (ID is inferred as host part or whole string)
func ParseClusterServers(nodes []string) ([]raft.Server, error) {
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no cluster nodes specified")
	}

	servers := make([]raft.Server, 0, len(nodes))
	seenIDs := make(map[string]bool)
	seenAddrs := make(map[string]bool)

	for _, raw := range nodes {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}

		var id, addr string
		if strings.Contains(trimmed, "=") {
			parts := strings.SplitN(trimmed, "=", 2)
			id = strings.TrimSpace(parts[0])
			addr = strings.TrimSpace(parts[1])
		} else {
			addr = trimmed
			// In docker networks or hosts, use hostname before colon as ID if available
			if idx := strings.Index(addr, ":"); idx != -1 {
				id = addr[:idx]
			} else {
				id = addr
			}
		}

		if id == "" || addr == "" {
			return nil, fmt.Errorf("invalid cluster node format %q", raw)
		}
		if seenIDs[id] {
			return nil, fmt.Errorf("duplicate server ID %q in cluster nodes", id)
		}
		if seenAddrs[addr] {
			return nil, fmt.Errorf("duplicate server address %q in cluster nodes", addr)
		}

		seenIDs[id] = true
		seenAddrs[addr] = true
		servers = append(servers, raft.Server{
			ID:       raft.ServerID(id),
			Address:  raft.ServerAddress(addr),
			Suffrage: raft.Voter,
		})
	}

	return servers, nil
}

