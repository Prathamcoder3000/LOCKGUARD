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

### Raft Log Command Payload & Codec Envelopes
Commands dispatched through HashiCorp Raft use a strictly versioned envelope (`consensus.CommandEnvelope`):

```go
type CommandEnvelope struct {
    SchemaVersion uint32                `json:"schema_version"` // 1
    CommandType   lockstate.CommandType `json:"command_type"`   // ACQUIRE_LOCK, RENEW_LOCK, RELEASE_LOCK, EXPIRE_LOCK
    Payload       json.RawMessage       `json:"payload"`
}
```

The typed command payloads (`AcquireLockCommand`, `RenewLockCommand`, `ReleaseLockCommand`, `ExpireLockCommand`) are serialized into `Payload` and validated upon decoding before passing into the deterministic state machine.

### Consensus Architecture & FSM Adapter
- **`backend/internal/consensus/codec.go`**: Versioned encoding and decoding with schema enforcement (`SchemaVersion = 1`).
- **`backend/internal/consensus/fsm.go`**: `LockFSM` implements `raft.FSM`:
  - `Apply(*raft.Log) interface{}`: Decodes committed log data and deterministically calls `MemoryStateMachine.Apply()`.
  - `Snapshot() (raft.FSMSnapshot, error)`: Exports sorted, detached `SnapshotData` containing resources, active locks, and idempotency records.
  - `Restore(io.ReadCloser) error`: Atomically replaces state machine state from snapshot stream.
- **`backend/internal/consensus/node.go`**: `RaftNode` manages the HashiCorp Raft instance, TCP network transport, BoltDB log and stable stores (`raft.db`), FileSnapshotStore (`/snapshots`), safe single-pass bootstrap, leader detection, linearizable reads (`VerifyLeader`, `Barrier`), and graceful shutdown.
- **`backend/internal/coordinator/handler.go`**: Direct HTTP endpoints on coordinator nodes providing lock mutation proposals, linearizable reads, cluster inspection, and follower redirection (`421 Misdirected Request` with `X-Raft-Leader-ID` and `X-Raft-Leader-Address` headers).

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

---

## 6. Phase 1A Deterministic State Machine (`backend/internal/lockstate`)

### 6.1 Package Architecture
The `backend/internal/lockstate` package implements the deterministic in-memory locking core:
- **`types.go`**: Core domain types (`LockStatus`, `LockRecord`, `ResourceState`, `TransitionType`, `TransitionEvent`, `IdempotencyRecord`). Deep-copying `.Clone()` methods guarantee state encapsulation.
- **`commands.go`**: Polymorphic `Command` interface, typed command structs (`AcquireLockCommand`, `RenewLockCommand`, `ReleaseLockCommand`, `ExpireLockCommand`), canonical input validation, and SHA-256 payload fingerprinting.
- **`state_machine.go`**: `StateMachine` interface and `MemoryStateMachine` implementation backed by `sync.RWMutex`.
- **`acquire.go`**: Acquisition logic, lease expiry check, monotonic fencing token generation, and idempotency store.
- **`renew.go`**: Renewal logic, owner verification, token verification, and TTL extension without token advancement.
- **`release.go`**: Explicit release logic, active lease clearance, and token retention.
- **`expire.go`**: Expiration verification, premature expiration rejection, and lease reclamation.
- **`query.go`**: Thread-safe, read-only queries returning detached clones (`GetLock`, `ListLocks`, `GetResourceState`, `LastAppliedTime`).
- **`errors.go`**: Strongly typed sentinel errors and HTTP-compatible error code mapping.

### 6.2 Idempotency & SHA-256 Fingerprinting
Every command produces a deterministic, canonical byte fingerprint:
```
SHA-256(Type | ResourceName | OwnerID | LeaseDurationNano | FencingToken | ExpectedOwnerID | ExpectedFencingToken | ExpectedLeaseVersion | ExpectedExpiresAtUnixNano)
```
- A replayed `RequestID` with an identical fingerprint returns the cached `Result` without side effects.
- A replayed `RequestID` with an altered fingerprint is rejected with `ErrIdempotencyConflict`.

### 6.3 Concurrency Control & State Protection
- In-memory lock tables (`map[string]*ResourceState`) and idempotency tables (`map[string]*IdempotencyRecord`) are synchronized via `sync.RWMutex`.
- Mutation methods (`Apply`, `Acquire`, `Renew`, `Release`, `Expire`) obtain a full write lock (`s.mu.Lock()`).
- Query methods (`GetLock`, `ListLocks`, `GetResourceState`, `LastAppliedTime`) acquire a shared read lock (`s.mu.RLock()`) and clone state structures before returning to ensure callers cannot mutate internal records.

### 6.4 Sentinel Error Catalogue
| Sentinel Error | Code | Meaning |
| :--- | :--- | :--- |
| `ErrInvalidCommand` | `ERR_INVALID_COMMAND` | Command failed validation (empty fields, non-positive TTL/timestamp). |
| `ErrLockAlreadyHeld` | `ERR_LOCK_ALREADY_HELD` | Target resource is currently locked under an unexpired lease. |
| `ErrLockNotFound` | `ERR_LOCK_NOT_FOUND` | No active lock lease exists for the requested resource. |
| `ErrNotOwner` | `ERR_NOT_OWNER` | Caller is not the current lease holder. |
| `ErrFencingTokenMismatch` | `ERR_FENCING_TOKEN_MISMATCH` | Provided fencing token does not match active lease token. |
| `ErrLeaseExpired` | `ERR_LEASE_EXPIRED` | Operation attempted on an already expired lease. |
| `ErrIdempotencyConflict` | `ERR_IDEMPOTENCY_CONFLICT` | Replayed `RequestID` carries a conflicting command payload. |
| `ErrTokenExhausted` | `ERR_TOKEN_EXHAUSTED` | Fencing token uint64 counter reached maximum capacity (`math.MaxUint64`). |
| `ErrVersionExhausted` | `ERR_VERSION_EXHAUSTED` | Lease version uint64 counter reached maximum capacity (`math.MaxUint64`). |
| `ErrInvalidExpiration` | `ERR_INVALID_EXPIRATION` | Attempted expiration command is premature (`effectiveNow < expiresAt`). |
| `ErrLeaseVersionMismatch` | `ERR_LEASE_VERSION_MISMATCH` | Expected lease version or expiration timestamp does not match active lease instance. |
| `ErrArithmeticOverflow` | `ERR_ARITHMETIC_OVERFLOW` | Timestamp plus TTL nanoseconds exceeds `math.MaxInt64`. |

### 6.5 Architectural Separation: Phase 1A vs. Phase 1B
- **Phase 1A (Core Engine):** Implements pure deterministic state transitions, idempotency, and query isolation. It does not initiate network consensus, leader election, or disk I/O.
- **Phase 1B (Raft Consensus Layer):** Encapsulated in HashiCorp Raft's `raft.FSM` interface (`Apply(*raft.Log)`, `Snapshot()`, `Restore()`). The Raft consensus leader replicates command byte arrays across coordinator quorum. Once committed by 2 of 3 nodes, the Raft runtime passes the log entry to `FSM.Apply()`, which unmarshals the command and delegates execution directly to the deterministic `MemoryStateMachine`. Authoritative reads enforce linearizability via `VerifyLeader()` and `Barrier()`.
- **Data Boundary:** PostgreSQL remains strictly reserved for relational metadata (users, audit logs, benchmark summaries) and is NEVER in the path of lock acquisition, renewal, or fencing token issuance.

---

## 7. Phase 2 Architecture: API Gateway, Heartbeat & Renewal Engine

### 7.1 Distributed API Gateway (`backend/internal/gateway`)
- **Reverse Proxy Routing:** Transparently forwards all `/v1/locks/*` and `/v1/cluster/status` requests to the current Raft leader.
- **Dynamic Leader Discovery & Caching:** Probes coordinator endpoints via `GET /v1/cluster/status`, resolves active leader, and caches HTTP base URL with configurable TTL (`GATEWAY_LEADER_CACHE_TTL_MS`, default 5s).
- **HTTP 421 Follower Redirection:** Followers reject mutations with HTTP 421 `StatusMisdirectedRequest`, emitting `X-Raft-Leader-ID` and `X-Raft-Leader-Address` hint headers. The Gateway parses these headers, updates its leader cache, and automatically retries the buffered request payload up to `GATEWAY_MAX_REDIRECT_ATTEMPTS` (default 2).
- **Fail-Closed Quorum Loss Protection:** If no leader can be contacted or commit times out, the Gateway fails closed returning HTTP 503 `ERR_QUORUM_UNAVAILABLE`.
- **Header Sanitization:** Strips hop-by-hop headers (`Connection`, `Keep-Alive`, `Proxy-Authenticate`, `TE`, `Trailers`, `Transfer-Encoding`, `Upgrade`) while propagating `X-Request-ID` and client addresses.

### 7.2 Client Heartbeat Manager (`backend/internal/heartbeat`)
- **Session Lifecycle:** Clients register unique sessions bound to an `OwnerID` with TTL. Heartbeat pings extend session expiration ($T_{\text{expires}} = \text{Now} + \text{TTL}$).
- **Inactivity Scrubber:** Background goroutine sweeps sessions at `HEARTBEAT_CLEANUP_INTERVAL_MS` (default 2s). When $T_{\text{now}} > T_{\text{expires}}$, sessions are marked inactive and `lockguard_heartbeat_timeouts_total` is incremented.
- **Automatic Lock Deregistration:** Expiration hooks notify the Renewal Manager to immediately terminate all renewal workers associated with the inactive session's locks, allowing abandoned leases to naturally expire.

### 7.3 Automated Lease Renewal Manager (`backend/internal/renewal`)
- **Dynamic Deadline Scheduling:** Background worker loops calculate trigger times using:
  $$T_{\text{renew}} = \text{ExpiresAt} - \frac{\text{TTL}}{3} - \text{SafetyMargin}$$
- **Invariant Guarantees:** On renewal success, the state machine preserves `FencingToken` and increments `LeaseVersion` ($V \to V+1$). The worker verifies both invariants.
- **Idempotent Timeout Retries:** On network timeouts or commit ambiguities, the renewal worker reuses the exact same `RequestID`, leveraging the FSM's idempotency cache to prevent accidental double-incrementation.
- **Duplicate-Worker Guard:** Exactly one renewal goroutine is permitted per resource; subsequent start calls for an active worker generation return safely without spawning duplicate workers.

### 7.4 Metrics & Observability (`backend/internal/metrics`)
- Exposes standard Prometheus text-format metrics on `/metrics`:
  - `lockguard_gateway_requests_total{status, method, path}`
  - `lockguard_gateway_leader_resolutions_total{status}`
  - `lockguard_active_client_sessions`
  - `lockguard_heartbeat_received_total{status}`
  - `lockguard_heartbeat_timeouts_total`
  - `lockguard_renewal_attempts_total{status}`
  - `lockguard_renewals_active`
  - `lockguard_fencing_token_current`

---

## 8. Phase 3 Architecture: Operational Persistence, Durable Audit & Reliability

### 8.1 PostgreSQL Connection Management & Pool Scaffolding (`backend/internal/persistence`)
- **Configurable Pool Parameters:** Reads `DATABASE_*` / `DB_*` environment variables (`MaxOpenConns=20`, `MaxIdleConns=10`, `ConnMaxLifetime=30m`, `ConnTimeout=5s`).
- **Connection Isolation:** Connection initialization, health checks, and query execution are managed via `Database.WithTx` and context cancellation.
- **Log Sanitization:** Passwords and credentials are automatically scrubbed (`****`) in log messages and string serialization (`SanitizedDSN()`).

### 8.2 Embedded Version-Controlled Migrations (`backend/internal/persistence/migrator.go`)
- **Embed Directives:** Uses `//go:embed migrations/*.sql` to package migration files into the compiled binary.
- **Advisory Lock Protection:** Acquires PostgreSQL advisory lock (`SELECT pg_advisory_lock(74152969)`) to serialize migration execution across concurrent API Gateway instances.
- **Integrity Checksums:** Computes SHA-256 digests for each migration script and compares against `schema_migrations`, halting with `ErrMigrationFailed` if file tampering is detected.

### 8.3 Asynchronous Durable Audit Pipeline (`backend/internal/audit`)
- **Bounded In-Memory Channel:** Buffers up to `10,000` events in a bounded Go channel.
- **Non-Blocking Dispatch:** Enqueueing never stalls HTTP proxy latency or Raft consensus. When queue is saturated, backpressure drops events and increments `lockguard_audit_events_failed_total{reason="queue_full"}`.
- **Micro-Batching:** A background flusher writes records in batches of `50` or every `50ms`.
- **Exponential Backoff with Jitter:** On transient PostgreSQL failures, flusher retries up to 3 times with randomized backoff delays.
- **Graceful Shutdown:** `Shutdown(ctx)` drains remaining queued events to disk before closing the database pool.

### 8.4 Multi-Component Health Diagnostics (`backend/internal/recovery`)
- **Role Differentiation:** Distinguishes between Raft Quorum Availability (sole lock authority) and PostgreSQL Storage (audit history).
- **Graceful Degradation:** A total database outage downgrades service health to `DEGRADED` while lock coordinator operations remain 100% operational and safe. Raft quorum loss immediately transitions status to `UNHEALTHY` (Fail Closed).

### 8.5 Phase 3 Prometheus Metrics
- `lockguard_audit_events_created_total{event_type, status}`
- `lockguard_audit_events_persisted_total{status}`
- `lockguard_audit_events_failed_total{reason}`
- `lockguard_audit_queue_size`
- `lockguard_audit_delivery_latency_seconds`
- `lockguard_database_connections`
- `lockguard_database_health`
- `lockguard_database_query_errors_total`
- `lockguard_database_migrations_status`
