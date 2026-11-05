# LockGuard — Durable Audit Event Architecture

**Project:** LOCKGUARD — Fault Tolerant Distributed Lock Service  
**Phase:** Phase 3 — Operational Persistence, Durable Audit & Reliability  
**Classification:** Architecture Specification  

---

## 1. Architectural Philosophy & Isolation Invariant

In distributed lock services, the critical latency path must remain bounded and deterministic. Direct synchronous database writes inside consensus loops introduce non-deterministic I/O stalls, disk flushes, and network latency that degrade Raft performance and risk cluster-wide timeouts.

To safeguard lock throughput and consistency:
1. **Raft FSM is 100% in-memory and deterministic:** Lock state transitions are committed solely through HashiCorp Raft log replication and snapshots.
2. **Audit persistence is completely decoupled and asynchronous:** Audit events are captured at the API Gateway boundary and emitted to an asynchronous delivery pipeline.
3. **Fail-Closed Lock Semantics, Fail-Degraded Audit Semantics:** If PostgreSQL experiences a total outage, lock acquisition, renewal, release, and fencing token validation continue without disruption.

```
+-------------------------------------------------------------------------------+
|                             CLIENT APPLICATION                                |
+---------------------------------------+---------------------------------------+
                                        | (HTTP / REST)
                                        v
+-------------------------------------------------------------------------------+
|                       API GATEWAY (LockGuard Server)                          |
|                                                                               |
|   1. Proxy to Raft Leader                  2. Record Event (Non-blocking)     |
|   +--------------------------+             +-------------------------------+  |
|   |  Reverse-Proxy Router    |             |  Bounded Audit Channel Queue  |  |
|   |  (Leader Discovery, 421) |             |  (Capacity: 10,000 Events)    |  |
|   +-------------+------------+             +---------------+---------------+  |
+-----------------|------------------------------------------|------------------+
                  |                                          |
        (Raft RPC | Strict Quorum)                 (Async    | Worker Flusher)
                  v                                Batch     v
+----------------------------------+         +----------------------------------+
|    RAFT COORDINATOR CLUSTER      |         |     POSTGRESQL AUDIT STORE       |
|                                  |         |                                  |
|  - Sole Authority for Locks      |         |  - Append-Only Operational Log   |
|  - Fencing Token Allocation      |         |  - Query API / Compliance Trail  |
|  - In-Memory State Machine       |         |  - Historical Metadata Storage   |
|  - BoltDB Persistent Log         |         |  - Advisory Lock Migrations      |
+----------------------------------+         +----------------------------------+
```

---

## 2. Audit Pipeline Mechanics

### 2.1 Bounded In-Memory Queue & Backpressure Handling
- **Queue Implementation:** Go buffered channel with fixed capacity (`10,000` items).
- **Non-blocking Dispatch (`RecordEvent`):**
  - When the queue has capacity, the event is enqueued instantly ($\mathcal{O}(1)$ without blocking).
  - If the queue is full (e.g. prolonged PostgreSQL outage under high throughput), the event is dropped.
  - The atomic counter `failedCount` and Prometheus metric `lockguard_audit_events_failed_total{reason="queue_full"}` are incremented.
  - The client's Raft lock operation is **never** delayed or rejected due to audit queue saturation.

### 2.2 Micro-Batching & Database Flushing
- **Worker Loop:** Background goroutine consumes items into an active batch slice.
- **Trigger Conditions:** A batch is flushed to PostgreSQL either when:
  1. The batch reaches `50` events (`BatchSize`).
  2. The flush interval timer (`50ms`) elapses with $\ge 1$ queued event.
- **Bulk Insert:** Uses PostgreSQL multi-row transactions with prepared statements (`InsertBatch`), minimizing network round-trips.

### 2.3 Transient Failure Resilience: Exponential Backoff with Jitter
When a database batch write encounters a transient network disconnect or connection reset:
1. The flusher retries up to `3` times (`MaxRetries`).
2. Delay uses exponential backoff:
   $$\text{Delay} = \text{BaseDelay} \times 2^{\text{attempt}} + \text{RandomJitter}$$
3. Jitter prevents thundering-herd reconnect spikes against the PostgreSQL connection pool.
4. If all retries fail, events are logged, metric `lockguard_audit_events_failed_total{reason="db_insert_failed"}` is recorded, and the pipeline continues to prevent memory leaks.

### 2.4 Graceful Draining on Shutdown
When the gateway receives a termination signal (`SIGTERM`, `SIGINT`):
1. `auditPipeline.Shutdown(ctx)` closes the inbound channel to reject new events.
2. The worker loop drains all remaining buffered events and flushes them to PostgreSQL.
3. Once the queue is completely drained, the database connection pool is closed cleanly.

---

## 3. Event Taxonomy and Data Model

Every audit entry conforms to a structured schema containing sufficient forensic metadata to reconstruct operational history:

### 3.1 Event Categories
- `LOCK_OPERATION`: Mutating lock state changes (`ACQUIRE`, `RENEW`, `RELEASE`, `EXPIRE`).
- `SESSION_LIFECYCLE`: Client registration, heartbeat renewal, and session timeouts.
- `RENEWAL_LIFECYCLE`: Automated background renewal worker start and stop events.
- `CONSENSUS_TOPOLOGY`: Leader failover, node departure, quorum status updates.
- `SYSTEM_OPERATIONAL`: Service startup, shutdown, and diagnostic state reports.

### 3.2 Operation Status
- `COMMITTED`: The command was replicated by Raft quorum and applied to the state machine.
- `REJECTED`: The command was evaluated by the state machine and rejected due to a business invariant (e.g., lock already held, stale fencing token).
- `FAILED`: Request failed prior to or during consensus (e.g., quorum loss, network timeout, coordinator crash).
- `PENDING`: Non-mutating inquiry or operation undergoing processing.

---

## 4. Security & Sanitization Boundaries

Audit logs frequently traverse organizational boundaries for review by operators and compliance auditors. Under LockGuard's security model:

1. **Credential Redaction:** The `Event.Validate()` interceptor recursively sanitizes metadata before enqueueing. Any key containing `session_id`, `token_secret`, `password`, `secret`, or `authorization` is scrubbed to `[REDACTED]`.
2. **Authorization & Cross-Client Isolation:**
   - In production (`Auth.Enabled=true`), all audit endpoints enforce Bearer authentication.
   - Non-admin client sessions (`client:<client_id>`) can ONLY access audit events belonging to their own `client_id`.
   - Cross-client queries (e.g. `client-A` attempting to read `/v1/audit/clients/client-B`, `/v1/audit/events?client_id=client-B`, or an event record by ID belonging to `client-B`) are strictly rejected with HTTP 403 `ERR_FORBIDDEN`.
   - Administrative tokens (`admin`, `operator`, or matching `JWTSecretKey`) possess tenant-wide read authority.
3. **SQL Injection Defense:** All queries in `PostgresAuditRepo` utilize parameterized SQL placeholders (`$1, $2, ...`) without raw string interpolation.

---

## 5. Failure Scenario Analysis & Durability Accounting

Because Raft consensus maintains strict consistency and fail-closed safety for locks while PostgreSQL operates as an asynchronous operational audit store, system designers must rigorously understand failure modes and the accounting boundary between Raft commitments and audit records.

### 5.1 Failure Scenario Matrix

| Failure Mode | Point of Failure | System Behavior & Invariants | Loss Risk | Mitigation / Recovery Mechanism |
| :--- | :--- | :--- | :--- | :--- |
| **1. Application Crash After Raft Commit** | Between Raft FSM commit and Gateway audit enqueue | Lock lease is safely committed in Raft BoltDB log and FSM memory. Gateway process terminates before emitting audit event. | Audit event lost | Idempotent reconciliation on startup or replay from Raft log; Raft state remains 100% consistent. |
| **2. Application Crash After Queue Insertion** | Event resides in in-memory channel queue | Process terminates abruptly (`kill -9`, power loss). In-memory channel buffer is lost. | Unflushed events lost | Graceful shutdown (`SIGTERM`) drains queue; `QueueCapacity` bounded; BoltDB retains consensus truth. |
| **3. Application Crash During Batch Insert** | During `InsertBatch` transaction in PostgreSQL | PostgreSQL transaction rolls back atomically. In-memory batch was in flight. | Batch lost if process dies before retry | ACID transaction guarantees no partial or corrupted rows are ever persisted. |
| **4. Database Outage While Queued** | DB network severed or container stopped | Pipeline worker retries `MaxRetries` with exponential backoff and jitter. If outage exceeds retries, `db_insert_failed` metric is incremented. | Backpressure drop after retry exhaustion | Event channel buffers up to 10,000 events; once DB recovers, subsequent batches persist cleanly. |
| **5. Queue Capacity Exhaustion** | Sustained high throughput during prolonged DB outage | Channel buffer reaches capacity (10,000 items). Incoming events are dropped non-blocking. | Dropped events | Metric `lockguard_audit_events_failed_total{reason="queue_full"}` incremented; Raft lock path NEVER blocks. |
| **6. Shutdown Timeout** | Graceful shutdown context timeout expires | Flush worker attempts to drain queue within `Shutdown(ctx)` context timeout (e.g., 5s). | Events remaining when context cancelled | Production drains fast via micro-batches; timeout can be tuned in configuration. |
| **7. Duplicate Retry** | DB commits batch but network ACK drops; flusher retries | Flusher resends identical batch with unique UUID `event_id`s. | Zero duplicate records | Deduplication via `ON CONFLICT (event_id) DO NOTHING` in PostgreSQL; primary key idempotency. |

### 5.2 Mathematical Durability Accounting Invariant

The audit pipeline enforces strict conservation of event counts across the entire lifecycle:

$$\Delta N_{\text{created}} = \Delta N_{\text{persisted}} + \Delta N_{\text{failed}} + \Delta N_{\text{queue}}$$

Where:
- $N_{\text{created}}$: Total events passed to `RecordEvent` (`lockguard_audit_events_created_total`).
- $N_{\text{persisted}}$: Total events successfully committed to PostgreSQL (`lockguard_audit_events_persisted_total`).
- $N_{\text{failed}}$: Total events dropped due to backpressure or unrecoverable DB errors (`lockguard_audit_events_failed_total`).
- $N_{\text{queue}}$: Current in-memory channel depth (`lockguard_audit_queue_size`).

This metric invariant allows operational dashboards and alerting systems to immediately detect any divergence or drop in audit integrity.

