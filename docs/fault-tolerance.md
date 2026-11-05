# LOCKGUARD Fault Tolerance & Consensus Architecture

## 1. Quorum Architecture & Invariants

LOCKGUARD implements distributed locking over a three-node HashiCorp Raft consensus cluster (`coordinator-a`, `coordinator-b`, `coordinator-c`).

### Mathematical Quorum Invariant
For a cluster of size $N = 3$, the quorum $Q$ required to commit any state transition is:
$$Q = \left\lfloor \frac{N}{2} \right\rfloor + 1 = \left\lfloor \frac{3}{2} \right\rfloor + 1 = 2$$

Under Raft consensus:
- Fault tolerance capacity: $F = \left\lfloor \frac{N-1}{2} \right\rfloor = 1$. The cluster survives the simultaneous crash or isolation of any single coordinator.
- **Fail-Closed Guarantee:** If two coordinators fail or become partitioned ($N_{\text{active}} = 1 < Q$), the cluster enters a fail-closed posture. The isolated coordinator cannot achieve majority agreement, cannot renew leases, cannot issue new locks, and rejects all state-modifying requests with `ErrNotLeader` / `503 Service Unavailable`.

```
     ┌─────────────────┐       ┌─────────────────┐
     │  Coordinator A  │◄─────►│  Coordinator B  │  [QUORUM FORMED: 2/3]
     │    (Leader)     │       │   (Follower)    │  Mutations Committed
     └────────┬────────┘       └─────────────────┘
              │                         ▲
           X  │ Partition Severed    X  │ Partition Severed
              ▼                         ▼
     ┌─────────────────┐
     │  Coordinator C  │  [ISOLATED MINORITY: 1/3]
     │   (Follower)    │  FAILS CLOSED — Rejects All Write Operations
     └─────────────────┘
```

---

## 2. Authoritative Read Linearizability & Follower Read Semantics

A primary hazard in distributed coordination is the "stale leader" phenomenon (where a leader is partitioned from the majority and serves stale reads before realizing it has been deposed).

LockGuard enforces strict read semantics across all lock status lookups (`GetLock`, `ListLocks`, `GetResourceState`):

### Authoritative Linearizable Reads (`linearizable = true`)
1. **VerifyLeader():** The coordinator transmits Raft heartbeat probes to all peers and requires acknowledgment from a majority before proceeding.
2. **Barrier():** The coordinator executes a Raft barrier command. The barrier waits until all preceding committed entries in the Raft log have been fully applied to the in-memory state machine.
3. **FSM Read:** Only after `VerifyLeader()` and `Barrier()` succeed does the coordinator read the state machine.
4. **Follower Behavior (Fail-Closed):** If an authoritative read is invoked on a Follower node, it **fails closed immediately** returning `NotLeaderError`. Followers cannot verify quorum leadership independently.

### Explicit Diagnostic Reads (`linearizable = false`)
- Bypasses `VerifyLeader()` and `Barrier()`.
- Reads local in-memory FSM state directly without quorum confirmation.
- **Explicitly non-authoritative and potentially stale.** Reserved for internal health probes, metrics scrapers, and operational diagnostics.

### Follower Catch-Up Verification
- In integration and recovery tests, follower catch-up is evaluated by inspecting local FSM state to verify that the Raft log replication engine has successfully converged with the leader's commit index.
- Follower local state inspection confirms *replication convergence*, not linearizability.

---

## 3. Leader Election Dynamics

- **Heartbeat & Election Timers:** Coordinators use randomized election timeouts ($1000\text{ms} - 2000\text{ms}$) with a heartbeat interval of $200\text{ms}$.
- **Candidate Transition:** When followers miss heartbeat intervals, they increment their term and transition to Candidate.
- **Vote Validation:** A voter only grants its vote if:
  1. The candidate's term is greater than the voter's current term.
  2. The voter has not already voted for another candidate in this term.
  3. The candidate's log is at least as up-to-date as the voter's own log (evaluated by `(lastLogTerm, lastLogIndex)`).

---

## 4. Log Reconciliation & Catch-Up

When a severed or crashed follower rejoins the cluster:
1. The leader detects the follower's lagged log index via `AppendEntries` rejection.
2. The leader decrements `nextIndex` for that follower until finding the point of log convergence.
3. If the missing entries are still present in the leader's Write-Ahead Log (BoltDB), the leader replicates all missing entries sequentially.
4. If the missing entries have been truncated due to log compaction, the leader transmits a complete Snapshot stream.
5. The follower applies the snapshot atomically to its local FSM, jumps to the snapshot index, and resumes streaming live log entries.

---

## 5. Network Partition Recovery

In a network partition where the leader is isolated with a minority ($1$ node):
- The majority partition ($2$ nodes) elects a new leader with an incremented term ($\text{term} + 1$).
- The isolated leader continues attempting heartbeats, which fail. Any incoming client mutations on the isolated leader fail because it cannot achieve a quorum of $2$ nodes.
- When the partition heals:
  - The isolated leader receives an `AppendEntries` or heartbeat from the new leader bearing a higher term.
  - The old leader immediately steps down to Follower status, updates its local term, invalidates its cached leadership state, and reconciles its uncommitted logs against the authoritative leader.

