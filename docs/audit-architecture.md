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
2. **Read-Only Inspection:** The Audit Query API (`GET /v1/audit/events`) is strictly read-only and accepts optional Bearer authentication.
3. **SQL Injection Defense:** All queries in `PostgresAuditRepo` utilize parameterized SQL placeholders (`$1, $2, ...`) without raw string interpolation.

