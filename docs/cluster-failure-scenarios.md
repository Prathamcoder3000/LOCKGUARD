# LOCKGUARD Cluster Failure Scenarios & Verification Evidence

This document provides detailed failure analyses, protocol traces, and verification results for all 10 catastrophic failure scenarios evaluated during Phase 4 testing.

---

## Scenario 1: Leader Crash & Failover Recovery

- **Initial State:** Three-node cluster active. `coordinator-a` is elected Leader. Client acquires lock on `res-scenario-1` with Token $1$, Version $1$.
- **Failure Injected:** `coordinator-a` suffers an ungraceful crash (shutdown).
- **Cluster Dynamics:**
  - `coordinator-b` and `coordinator-c` miss heartbeats.
  - Election timeout elapses (~100ms in inmem test harness, ~1000ms in Docker).
  - `coordinator-b` transitions to Candidate, increments term, requests votes, and receives vote from `coordinator-c` (satisfying $2/3$ quorum).
  - `coordinator-b` becomes authoritative Leader.
- **Verification Assertions:**
  - Client queries `res-scenario-1` through new leader: state is intact.
  - Client renews lock: renewal succeeds; `LeaseVersion` increments strictly to $2$; `FencingToken` remains $1$.
- **Test File:** `backend/tests/phase4_node_failure_test.go:TestPhase4_Scenario1_LeaderCrashAndFailoverRecovery`
- **Result:** PASS (1.24s).

---

## Scenario 2: Follower Crash & Log Catch-Up

- **Initial State:** Cluster active with Leader `coordinator-a` and Follower `coordinator-b`.
- **Failure Injected:** Follower `coordinator-b` crashes while `coordinator-a` and `coordinator-c` maintain majority quorum.
- **Cluster Dynamics:**
  - Leader executes $5$ subsequent lock mutations (`catchup-res-0` through `catchup-res-4`).
  - Quorum is $2/3$ (`coordinator-a` + `coordinator-c`), so all mutations commit successfully.
  - `coordinator-b` restarts and reconnects.
  - Leader identifies log lag, transmits missing log entries, and follower applies them to its local FSM.
- **Verification Assertions:**
  - Follower log replication catch-up confirmed: diagnostic local FSM inspection on rejoined `coordinator-b` confirms all $5$ locks are fully synchronized with exact tokens.
  - Authoritative read (`linearizable=true`) on the follower correctly fails closed with `NotLeaderError` (confirming that followers cannot serve linearizable reads without majority leader verification).
- **Test File:** `backend/tests/phase4_node_failure_test.go:TestPhase4_Scenario2_FollowerCrashAndCatchUp`
- **Result:** PASS (0.12s).

---

## Scenario 3: Two-Node Failure, Quorum Loss & Fail-Closed Behavior

- **Initial State:** Three-node cluster with active Leader `coordinator-a`.
- **Failure Injected:** Both followers (`coordinator-b` and `coordinator-c`) crash simultaneously. Only `coordinator-a` remains alive.
- **Cluster Dynamics:**
  - Active nodes = $1 < Q=2$.
  - Incoming lock acquire request arrives for `res-fail-closed`.
  - Leader attempts to replicate entry; Raft future times out or returns quorum failure error.
  - Coordinator fails closed, returning error to caller.
- **Healing:**
  - `coordinator-b` restarts.
  - Cluster regains quorum ($2/3$). Leader is re-elected and subsequent acquisitions immediately succeed.
- **Test File:** `backend/tests/phase4_node_failure_test.go:TestPhase4_Scenario3_TwoNodeFailure_QuorumLoss_FailClosed`
- **Result:** PASS (0.18s).

---

## Scenario 4: Isolated Leader Deposition & Term Advancement

- **Initial State:** `coordinator-a` is Leader at Term $1$.
- **Failure Injected:** `coordinator-a` is network-partitioned from `coordinator-b` and `coordinator-c`.
- **Cluster Dynamics:**
  - `coordinator-b` and `coordinator-c` form a majority partition, hold an election, and elect `coordinator-b` as Leader at Term $2$.
  - Old leader `coordinator-a` attempts to acquire a lock: fails closed (cannot replicate to majority).
  - Majority partition acquires lock on `partition-res` with Token $1$.
  - Network heals: `coordinator-a` discovers Term $2$ messages from `coordinator-b`.
  - `coordinator-a` immediately abdicates leadership and steps down to Follower.
- **Test File:** `backend/tests/phase4_node_failure_test.go:TestPhase4_Scenario4_LeaderIsolationAndDeposition`
- **Result:** PASS (0.22s).

---

## Scenario 5: Bidirectional Network Partition & Split-Brain Prevention

- **Initial State:** Three-node cluster active. Lock acquired on `part-res`.
- **Failure Injected:** Network splits into Partition 1 (`coordinator-a`, Leader) and Partition 2 (`coordinator-b`, `coordinator-c`, Followers).
- **Cluster Dynamics:**
  - Partition 2 detects leader loss and elects `coordinator-b`.
  - Client attempts mutation on Partition 1: rejected / times out.
  - Client mutates on Partition 2: succeeds (2 nodes satisfy quorum).
  - Partition heals: Partition 1 adopts Partition 2's authoritative logs.
- **Verification Assertions:**
  - Zero split-brain state detected. All nodes converge to identical FSM hash.
- **Test File:** `backend/tests/phase4_network_partition_test.go:TestPhase4_NetworkPartition_BidirectionalAndHealing`
- **Result:** PASS (0.24s).

---

## Scenario 6: Repeated Network Partition & Healing Cycles

- **Initial State:** Cluster active.
- **Failure Injected:** Injected $3$ consecutive cycles of:
  1. Severing one follower.
  2. Performing lock acquire, renew, release mutations.
  3. Reconnecting severed follower and awaiting log sync.
- **Verification Assertions:**
  - Across all 3 cycles, fencing tokens on `cycle-shared-resource` advanced monotonically: $1 \rightarrow 2 \rightarrow 3$.
  - No stale token or duplicate token was ever emitted.
- **Test File:** `backend/tests/phase4_network_partition_test.go:TestPhase4_NetworkPartition_RepeatedCycles`
- **Result:** PASS (0.87s).

---

## Scenario 7: Leader Change During Active Client Renewal

- **Initial State:** Gateway client manages an auto-renewing lease worker for `res-leader-switch`.
- **Failure Injected:** Authoritative leader changes while renewal worker is in its renewal interval.
- **Cluster Dynamics:**
  - Old leader returns HTTP 421 Misdirected Request with `LeaderID` pointing to the new leader.
  - Gateway renewal worker intercepts 421, updates cached leader endpoint, and retries request.
  - New leader processes renewal: increments `LeaseVersion` strictly by $+1$ while preserving original `FencingToken`.
- **Test File:** `backend/tests/phase4_session_renewal_recovery_test.go:TestPhase4_Session_LeaderChangeDuringRenewal`
- **Result:** PASS (0.41s).

---

## Scenario 8: Client Heartbeat Timeout & Automatic Lease Scrubber

- **Initial State:** Client session registered with $100\text{ms}$ heartbeat interval and $200\text{ms}$ TTL.
- **Failure Injected:** Client process halts and stops sending heartbeats.
- **Cluster Dynamics:**
  - Background scrubber executes periodic cleanup ($50\text{ms}$ interval).
  - Scrubber identifies session timestamp has expired.
  - Session is transitioned to inactive. All locks held by the expired session are released.
- **Test File:** `backend/tests/phase4_session_renewal_recovery_test.go:TestPhase4_Session_HeartbeatTimeoutAndWorkerTermination`
- **Result:** PASS (0.25s).

---

## Scenario 9: Protected Resource Fencing Rejection of Stale Token

- **Initial State:** Downstream protected resource API running at `/v1/data/mutate`.
- **Failure Injected:**
  1. Worker 1 acquires lock on `bank-account-4491` (Fencing Token $1$).
  2. Worker 1 writes to protected resource with Token $1$: ACCEPTED ($200\text{ OK}$).
  3. Worker 1 suffers long GC pause or network delay. Lock expires and is acquired by Worker 2 (Fencing Token $2$).
  4. Worker 2 writes to protected resource with Token $2$: ACCEPTED ($200\text{ OK}$).
  5. Delayed Worker 1 awakens and attempts write with Token $1$.
- **Verification Assertions:**
  - Protected resource compares incoming token against `highestObservedToken` ($2$).
  - Request rejected with HTTP $409\text{ Conflict}$ and error code `ERR_FENCING_TOKEN_STALE`.
  - Downstream state is fully protected against out-of-order zombie writes.
- **Test File:** `backend/tests/phase4_protected_resource_fencing_test.go:TestPhase4_ProtectedResource_FencingSafety`
- **Result:** PASS (0.10s).

---

## Scenario 10: PostgreSQL Outage, Backlog Buffering & Deduplicated Recovery

- **Initial State:** Coordinator processing locks while streaming audit events to PostgreSQL.
- **Failure Injected:** PostgreSQL connection severed (simulated database outage).
- **Cluster Dynamics:**
  - Raft consensus and state machine continue processing lock acquisitions without interruption.
  - Audit events accumulate in asynchronous memory buffer ($100$ events).
  - PostgreSQL connectivity restored.
  - Worker flushes backlog using batched `INSERT INTO audit_events ... ON CONFLICT (id) DO NOTHING`.
- **Verification Assertions:**
  - All $100$ events persisted durably. Zero duplicate rows created.
- **Test File:** `backend/tests/phase4_database_recovery_test.go:TestPhase4_Database_OutageBacklogRecoveryAndDeduplication`
- **Result:** PASS (0.48s).

