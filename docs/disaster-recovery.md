# LOCKGUARD Disaster Recovery & Snapshot Compaction

## 1. Overview & Storage Architecture

LockGuard separates consensus and state persistence into dedicated, crash-resilient layers:

- **Write-Ahead Log (WAL):** Persisted using HashiCorp BoltDB (`raft.db`) on dedicated container volumes (`coordinator_a_data`, `coordinator_b_data`, `coordinator_c_data`).
- **Snapshot Store:** Managed by HashiCorp `FileSnapshotStore` retaining the latest $N=2$ stable snapshots.
- **Authoritative In-Memory FSM:** Reconstituted deterministically on startup by replaying the latest snapshot plus all committed WAL log entries since the snapshot index.
- **Audit History Sink:** Asynchronously written to PostgreSQL 16 with decoupled in-memory buffering.

```
       ┌────────────────────────────────────────────────────────┐
       │             HashiCorp Raft Storage Engine              │
       └───────────┬────────────────────────────────┬───────────┘
                   │                                │
                   ▼                                ▼
       ┌───────────────────────┐        ┌───────────────────────┐
       │   BoltDB Log Store    │        │  FileSnapshotStore    │
       │   (raft.db, WAL)      │        │  (Compacted FSM JSON) │
       └───────────────────────┘        └───────────────────────┘
                   │                                │
                   └───────────────┬────────────────┘
                                   ▼
                   ┌────────────────────────────────┐
                   │    LockState FSM (Rebuilt)     │
                   │  - Active Lock Records         │
                   │  - Monotonic Fencing Tokens    │
                   │  - Renewal Lease Versions      │
                   └────────────────────────────────┘
```

---

## 2. Snapshot Creation & Compaction Strategy

To prevent unbounded disk growth and long replay times during recovery:
1. **Trigger Condition:** Raft triggers compaction after `SnapshotThreshold = 8192` log entries or on a scheduled `SnapshotInterval = 120s`.
2. **Atomic Serialization:** The state machine takes an atomic snapshot by holding an `RLock()` on `lockstate.Engine` and serializing:
   - All active lock records (`map[string]*LockRecord`).
   - Highest fencing token per resource (`map[string]uint64`).
   - Monotonic lease generation counters.
3. **Sink Persist:** Raft writes the serialized snapshot stream to a temporary directory, flushes to disk via `fsync()`, and atomically renames the snapshot directory to guarantee ACID durability.

---

## 3. Disaster Recovery & Node Reconstruction

When an entire coordinator node suffers hardware loss or catastrophic process termination:

### Cold Restart Procedure
1. The container daemon restarts the process with persistent volumes attached (`coordinator_a_data`).
2. The coordinator initializes BoltDB and inspects the `FileSnapshotStore`.
3. If a snapshot is found, the engine executes `FSM.Restore()`:
   - Reads the snapshot JSON stream.
   - Restores the active locks and monotonic token table.
   - Overwrites in-memory maps in a single critical section.
4. The node connects to Raft peers and replays WAL entries from the snapshot index up to the latest committed index.
5. Once caught up, the node joins active consensus participation as a follower.

### Fencing Token Monotonicity Preservation
A critical distributed systems invariant verified in Phase 4 is that **fencing tokens must strictly increase across node crashes, cluster restarts, and snapshot restores**:
- If Resource $R$ was previously released at Fencing Token $K=2$, a new acquisition following cold restoration from a snapshot is guaranteed to receive Token $K+1=3$.
- Tokens are never reset or re-used, preventing zombie workers from corrupting storage.

---

## 4. Corrupt Snapshot Handling

If a snapshot file on disk becomes corrupted (e.g. disk bad sectors, truncated writes, flipped bits):
- LockGuard's `FSM.Restore()` decodes with strict JSON stream validation.
- If deserialization fails (e.g. `unexpected EOF` or invalid syntax):
  - The restore function returns a clean `error` without panicking.
  - The node fails to start and aborts, logging a critical error.
  - The cluster preserves fail-closed safety rather than booting with a partial or corrupted state machine.

---

## 5. PostgreSQL Audit Outage & Backlog Recovery

Lock mutations must not be blocked or degraded if the secondary PostgreSQL audit database becomes unavailable:
- **Decoupled Asynchronous Queue:** The coordinator dispatches audit events into an in-memory ring buffer (up to 10,000 entries).
- **Retry Backoff:** A background worker attempts batched flushing (`FlushBatch`) to PostgreSQL. If connection fails, events remain queued.
- **Idempotent Reconciliation:** When PostgreSQL reconnects, the worker drains the backlog. Inserts use `ON CONFLICT (id) DO NOTHING` to guarantee zero duplication if retried after network timeouts.
- **Durability Boundary & Best-Effort Limitations:** In accordance with the approved Phase 3 persistence architecture, audit event delivery is best-effort and fail-degraded. If prolonged database outages saturate the 10,000-event queue or if a process terminates abruptly before in-memory batches are committed to PostgreSQL, events may be lost. Dropped events are explicitly tracked via `lockguard_audit_events_failed_total`. Raft consensus lock mutations are never delayed or rejected due to audit persistence failures.

