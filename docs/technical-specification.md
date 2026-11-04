# LockGuard Technical Specification

## 1. Raft Consensus Engine & Library Evaluation

### Candidate Analysis

| Evaluation Metric | HashiCorp Raft (`github.com/hashicorp/raft`) | Etcd Raft (`go.etcd.io/raft/v3`) | Dragonboat (`github.com/lni/dragonboat/v3`) |
| :--- | :--- | :--- | :--- |
| **Leading Status** | **Selected Candidate** | Alternative | Alternative |
| **Current Version** | `v1.7.2` | `v3.5.14` | `v3.3.8` |
| **License** | **MPL-2.0** (Open Source, Library Permissive) | Apache-2.0 | Apache-2.0 |
| **Transport Layer** | Built-in TCP / TLS Stream Layer (`NewNetworkTransport`) | None (Caller must implement transport) | Built-in TCP / Custom |
| **Persistence Integration** | Integrated `raft-boltdb/v2` / `FileSnapshotStore` | None (Caller must implement WAL) | RocksDB / Pebble integration |
| **State Machine Pattern** | Idiomatic `FSM` interface (`Apply`, `Snapshot`, `Restore`) | Custom step loop | Custom state machine |
| **Ecosystem Maturity** | Powers HashiCorp Consul, Nomad, and Vault | Powers etcd, CockroachDB | Powers BitalosDB |
| **Maintenance** | Actively maintained by HashiCorp Core Team | Actively maintained by CNCF | Low activity |

### Decision Summary
`github.com/hashicorp/raft` (paired with `github.com/hashicorp/raft-boltdb/v2`) is selected. It delivers a self-contained, battle-tested Raft implementation with pluggable BoltDB storage and native TCP transport, drastically reducing integration risk while preserving fail-closed invariants.

---

## 2. Network Topology & Port Bindings

| Node / Service | Port | Protocol | Network Scope | Purpose |
| :--- | :--- | :--- | :--- | :--- |
| `coordinator-a` | `7000` | TCP (Raft RPC) | Isolated (`lockguard-raft`) | Peer consensus, heartbeats, log replication |
| `coordinator-a` | `8001` | HTTP / JSON | Internal (`lockguard-internal`) | Coordinator metrics, direct health, cluster info |
| `coordinator-b` | `7000` | TCP (Raft RPC) | Isolated (`lockguard-raft`) | Peer consensus, heartbeats, log replication |
| `coordinator-b` | `8001` | HTTP / JSON | Internal (`lockguard-internal`) | Coordinator metrics, direct health, cluster info |
| `coordinator-c` | `7000` | TCP (Raft RPC) | Isolated (`lockguard-raft`) | Peer consensus, heartbeats, log replication |
| `coordinator-c` | `8001` | HTTP / JSON | Internal (`lockguard-internal`) | Coordinator metrics, direct health, cluster info |
| `api` | `8080` | HTTP / JSON | Internal & Edge | Public REST API Gateway for clients & dashboard |
| `protected-resource` | `9000` | HTTP / JSON | Internal | Simulated critical service validating fencing |
| `frontend` | `80` (or `3000` dev) | HTTP / HTML | Internal | React SPA static asset server |
| `nginx` | `80` | HTTP | Host / Public | Edge reverse proxy routing `/` and `/api/` |
| `postgres` | `5432` | TCP (PostgreSQL) | Internal (`lockguard-internal`) | Relational metadata store (No public port) |
| `prometheus` | `9090` | HTTP | Host / Internal | Metrics scraper and evaluation engine |

---

## 3. Data Structures & Core Types

### Lock State Entry
```go
type LockEntry struct {
    ResourceName string    `json:"resource_name"` // Primary Key (Unique)
    OwnerID      string    `json:"owner_id"`      // Unique ID of acquiring client
    FencingToken uint64    `json:"fencing_token"`  // Monotonically increasing 64-bit int
    LeaseTTL     int       `json:"lease_ttl"`     // Lease duration in seconds
    AcquiredAt   time.Time `json:"acquired_at"`   // UTC acquisition timestamp
    ExpiresAt    time.Time `json:"expires_at"`    // UTC expiration timestamp
}
```

### Raft Log Command Payload
```go
type CommandType string

const (
    CmdAcquireLock CommandType = "ACQUIRE_LOCK"
    CmdRenewLock   CommandType = "RENEW_LOCK"
    CmdReleaseLock CommandType = "RELEASE_LOCK"
)

type RaftCommand struct {
    Type         CommandType `json:"type"`
    ResourceName string      `json:"resource_name"`
    OwnerID      string      `json:"owner_id"`
    LeaseTTLSec  int         `json:"lease_ttl_sec"`
    FencingToken uint64      `json:"fencing_token,omitempty"`
    Timestamp    int64       `json:"timestamp"`
}
```

---

## 4. Replicated State Machine (FSM) Invariants

1. **Deterministic Execution:**
   Every coordinator node applying the same sequence of Raft log commands arrives at the exact same in-memory state.
2. **Lease Expiration Rule:**
   Wall-clock divergence is mitigated by anchoring expiration times to the leader's commit timestamp recorded in `RaftCommand.Timestamp`.
3. **Fencing Token Monotonicity:**
   The state machine maintains a persistent counter `HighestFencingToken`. Each successful `CmdAcquireLock` command atomically increments this counter:
   $$T_{new} = T_{highest} + 1$$
4. **Mutual Exclusion:**
   If `ResourceName` is currently held by `OwnerA` and `Now() < ExpiresAt`, a competing `CmdAcquireLock` by `OwnerB` returns `ERR_LOCK_HELD`.

---

## 5. Configuration & Timing Constants

```
RAFT_HEARTBEAT_TIMEOUT_MS     = 1000ms
RAFT_ELECTION_TIMEOUT_MS      = 1000ms
RAFT_COMMIT_TIMEOUT_MS        = 50ms
RAFT_SNAPSHOT_INTERVAL_SECONDS= 120s
RAFT_SNAPSHOT_THRESHOLD       = 8192 log entries

DEFAULT_LEASE_TTL_SECONDS     = 10s
MIN_LEASE_TTL_SECONDS         = 1s
MAX_LEASE_TTL_SECONDS         = 300s
FENCING_TOKEN_INITIAL         = 1000
```
