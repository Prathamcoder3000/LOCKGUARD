# LockGuard Testing Strategy & Verification Guide

## 1. Testing Hierarchy

```
tests/
  ├── unit/             # Fast in-memory unit tests for isolated packages
  ├── integration/      # Multi-component tests (API + Storage + Coordinators)
  ├── concurrency/      # Race condition and mutual exclusion stress tests (-race)
  ├── fault_injection/  # Partition simulation, node kill/restart tests
  └── recovery/         # Snapshot restore and log compaction replay tests
```

---

## 2. Test Execution Commands

### Phase 1A & 1B Consensus & State Machine Tests (Standard & Race Detector)
```powershell
cd "D:\5TH SEM\SD\PROJECT\LOCKGUARD\backend"
go test -v -count=1 ./internal/lockstate
go test -v -race -count=1 ./internal/lockstate

go test -v -count=1 ./internal/consensus
go test -v -race -count=1 ./internal/consensus

go test -v -count=1 ./internal/coordinator
```

### Full Backend Unit Test Suite (56 Passing Tests)
```powershell
cd "D:\5TH SEM\SD\PROJECT\LOCKGUARD\backend"
go test -v -count=1 ./...
```

### Protected Resource Unit Tests
```powershell
cd "D:\5TH SEM\SD\PROJECT\LOCKGUARD\protected-resource"
go test -v -count=1 ./...
```

### Frontend Build & Typecheck
```powershell
cd "D:\5TH SEM\SD\PROJECT\LOCKGUARD\frontend"
npm run build
```

---

## 3. Implemented Test Suites (Phase 1A)

### Lock State Machine Tests (`backend/internal/lockstate/`)
32 test suites verifying 9 core invariant categories:

#### Category A: Lock Acquisition
1. `TestAcquireUnlockedResource`: Uncontended lock acquire succeeds, transitions state to `HELD`, issues initial fencing token (1), starts `LeaseVersion = 1`, and records expiration.
2. `TestAcquireActiveResourceBySecondOwnerFails`: Attempting to acquire an already held unexpired lock returns `ErrLockAlreadyHeld`.
3. `TestAcquireActiveResourceBySameOwnerWithDifferentRequestIDFails`: Same owner attempting second acquire with new RequestID fails while lease is held; version remains 1.
4. `TestAcquireInputValidation`: Rejects empty `ResourceName`, empty `OwnerID`, zero/negative `TTL`, and zero timestamp with specific sentinel errors.
5. `TestAcquireAfterLeaseExpirationAdvancesTokenAndGrants`: Expired lock is automatically reclaimed by a subsequent acquire, issuing an incremented fencing token ($T+1$) and restarting `LeaseVersion = 1`.

#### Category B: Lease Renewal & Versioning
6. `TestRenewActiveLock`: First renewal increments `LeaseVersion` to 2; second renewal increments `LeaseVersion` to 3; preserves fencing token.
7. `TestRenewRejections`: Rejects renewals by non-owners, mismatched tokens, nonexistent resources, and expired leases without incrementing version.
8. `TestRenew_LeaseVersionOverflow`: Safe rejection with `ErrVersionExhausted` when `LeaseVersion == math.MaxUint64`.

#### Category C: Lock Release
9. `TestReleaseActiveLock`: Active lease is cleared to `nil`, transition count updated, `HighestToken` retained, and subsequent acquire restarts `LeaseVersion = 1`.
10. `TestReleaseRejections`: Non-owner, token mismatch, and expired lease release attempts fail closed without mutating state.

#### Category D: Lease Expiration & Stale Version Protection
11. `TestExpireLock`: Explicit expiration command marks lock expired when `effectiveNow >= expiresAt` with matching version and expiry timestamp.
12. `TestExpire_StaleCommandRejectedAfterRenewal`: Stale expiration command targeted at an earlier lease version is rejected with `ErrLeaseVersionMismatch` even though fencing token matches.
13. `TestExpire_OldExpirationRejectedWhenRenewalRetainsExactSameExpiryTimestamp`: Critical regression test: Worker A renews with the exact same expiry timestamp (e.g. 5m TTL at t=5 for a lease originally expiring at t=10); version increments from 1 to 2; an old expiration command specifying version 1 and the matching expiry timestamp is rejected with `ErrLeaseVersionMismatch`.
14. `TestExpire_ValidAfterRenewalExpires`: Expiration command specifying the renewed lease's version and expiration timestamp succeeds after the renewed lease expires.
15. `TestExpire_DifferentLeaseGenerationRejected`: Expire command carrying an arbitrary non-matching lease version is rejected with `ErrLeaseVersionMismatch`.
16. `TestExpire_RejectedLeavesStateUnchanged`: Rejections (wrong owner, wrong token, wrong version, wrong expiry, premature) leave all resource state and active locks completely unchanged.
17. `TestExpire_Idempotency`: Duplicate expiration requests return cached results idempotently; conflicting payloads return `ErrIdempotencyConflict`.

#### Category E: Idempotency
18. `TestIdempotencyAcquire`: Duplicate acquire with identical `RequestID` and payload returns identical cached `Result`, token, and version without side effects; conflicting payload returns `ErrIdempotencyConflict`.
19. `TestIdempotencyRenew`: Duplicate renewal returns cached result without incrementing version twice; conflicting payload returns conflict.
20. `TestIdempotencyRelease`: Duplicate release returns cached result; conflicting payload returns conflict.

#### Category F: Monotonicity & Token Safety
21. `TestFencingTokensMonotonicityAndIndependence`: Multiple acquires across multiple resources yield strictly increasing tokens; each new epoch restarts version at 1; token counters never reset upon release or expiry.
22. `TestFencingTokenOverflowHandling`: Approaching `math.MaxUint64` returns `ErrTokenExhausted` before wrapping around.

#### Category G: Determinism & Replay
23. `TestDeterminismAndLogicalTimePolicy`: Two independent `MemoryStateMachine` instances fed identical command logs produce bit-for-bit identical state, token values, lease versions, and query responses.

#### Category H: Timestamp & TTL Overflow Audit
24. `TestArithmeticOverflow_AcquireTTL`: `TTLNanoseconds = math.MaxInt64` rejected with `ErrArithmeticOverflow` without corrupting resource state.
25. `TestArithmeticOverflow_AcquireTimestamp`: `TimestampUnixNano = math.MaxInt64` rejected with `ErrArithmeticOverflow`.
26. `TestArithmeticOverflow_RenewTTL`: Renewal with `TTLNanoseconds = math.MaxInt64` rejected with `ErrArithmeticOverflow`; active lease intact.
27. `TestInputValidation_TimestampAndTTL`: Boundary validation for non-positive TTLs, timestamps, and expected expiration timestamps / zero versions.

#### Category I: Atomicity & Concurrency (`concurrency_test.go` & atomicity tests)
28. `TestAtomicity_FailedAcquireLeavesStateUnchanged`: Competing acquire rejection leaves active lock, version, and highest token untouched.
29. `TestAtomicity_FailedRenewLeavesStateUnchanged`: Failed renewals leave lease expiry, ownership, and version untouched.
30. `TestAtomicity_FailedReleaseLeavesStateUnchanged`: Failed releases leave active lease untouched (not cleared to nil).
31. `TestLogicalTimePolicy_ValidationVsBusinessFailure`: Syntactic validation errors do not advance logical time or record idempotency; applied business failures advance logical time and record idempotency for deterministic replay.
32. Concurrency stress suites:
    - `TestConcurrentAcquireSingleResource`: 100 concurrent goroutines compete for a single resource; exactly 1 acquires the lock, 99 receive `ErrLockAlreadyHeld`.
    - `TestConcurrentRenewals`: 50 concurrent valid renewals execute without race conditions or data corruption.
    - `TestConcurrentReleaseAndAcquire`: Interleaved release and acquire goroutines maintain mutual exclusion invariants without deadlock.
    - `TestConcurrentMultiResourceStress`: 30 concurrent workers performing randomized acquire, renew, and release operations across 8 resources; verified race-free with `-race`.

---

## 4. Implemented Consensus & Cluster Test Suites (Phase 1B)

### Consensus & Raft Integration Tests (`backend/internal/consensus/`)
1. **Command Codec Tests:**
   - `TestCodec_AcquireCommandRoundTrip`: Verifies versioned envelope wrapping, JSON serialization, and full roundtrip fidelity for `AcquireLockCommand`.
   - `TestCodec_RenewCommandRoundTrip`: Verifies roundtrip fidelity for `RenewLockCommand`.
   - `TestCodec_ReleaseCommandRoundTrip`: Verifies roundtrip fidelity for `ReleaseLockCommand`.
   - `TestCodec_ExpireCommandRoundTrip`: Verifies roundtrip fidelity for `ExpireLockCommand`.
   - `TestCodec_InvalidEnvelopeRejections`: Rejects empty payloads, malformed JSON, unsupported schema versions, and unknown command types.
2. **FSM Adapter & Snapshot Tests:**
   - `TestLockFSM_ApplyAndSnapshotRestore`: Validates that `LockFSM.Apply()` mutates state upon committed log entry, `LockFSM.Snapshot()` serializes deterministic state, and `LockFSM.Restore()` reconstitutes full lock state, lease versions, and idempotency cache.
   - `TestSnapshot_ExportAndRestore` (`lockstate` package): Validates deterministic sorted export, JSON encoding/decoding, and atomic state replacement.
3. **Single-Node Raft Lifecycle Tests:**
   - `TestRaftNode_SingleNodeAcquireRenewRelease`: Tests single-node bootstrap, election to leader, consensus commit for acquire/renew/release, and linearizable reads (`VerifyLeader`).
4. **Three-Node Cluster Tests:**
   - `TestCluster_ThreeNodeElectionAndReplication`: Validates that exactly 1 leader is elected from 3 nodes; follower rejects mutation with `ErrNotLeader` and provides leader hints; leader commits across quorum; state is consistently replicated to all followers.
   - `TestCluster_LeaderFailover`: Validates that shutting down the active leader triggers automatic failover to a new leader among the remaining 2 nodes ($2 \ge 2$), and unexpired locks and versions remain intact.
   - `TestCluster_QuorumLoss_FailClosed`: Validates that partitioning or stopping 2 nodes leaves 1 node without quorum ($1 < 2$); write commands fail closed immediately.
   - `TestCluster_BoltDBPersistenceAndRecovery`: Validates real disk persistence using BoltDB (`raft.db`) and `FileSnapshotStore`. Confirms that committed lock state, fencing tokens, lease versions, and idempotency cache survive full process termination and restart without data loss.

### Coordinator HTTP API Tests (`backend/internal/coordinator/`)
1. `TestCoordinatorHandler_AcquireRenewAndRead`: Validates HTTP endpoints (`/v1/locks/acquire`, `/v1/locks/{name}`, `/v1/cluster/status`), verifying that followers return `421 Misdirected Request` with `X-Raft-Leader-ID` and `X-Raft-Leader-Address` headers, and leaders execute consensus commits.

---

## 5. Implemented Test Suites (Phase 0)

### Backend Tests (`backend/tests/`)
1. `TestDefaultConfig`: Validates default port (8080), environment, default NodeID, and lease constraints.
2. `TestConfigValidationFailure`: Confirms that invalid port ranges (> 65535) or invalid lease boundaries (`MaxTTL < MinTTL`) correctly fail closed with descriptive errors.
3. `TestEnvOverride`: Confirms dynamic configuration loading from OS environment variables.
4. `TestHealthEndpoint`: Confirms `GET /health` returns HTTP 200 OK with valid service envelope.
5. `TestReadinessEndpoint`: Confirms `GET /ready` checks storage directory accessibility and returns 200 OK.
6. `TestMethodNotAllowedOnHealth`: Confirms non-GET methods (e.g., POST) are rejected with HTTP 405 Method Not Allowed.
7. `TestServerRoutingAndClusterStatus`: Confirms `GET /api/v1/cluster/status` returns complete cluster metadata and fail-closed invariants.
8. `TestStandardErrorFormatting`: Confirms structured error JSON envelopes adhere to API contracts.

### Protected Resource Tests (`protected-resource/internal/`)
1. `TestRepeatedSameLeaseWrites`: Verifies that multiple legitimate operations under the same active lease (`token == current.HighestToken` and `owner == current.OwnerID`) are permitted.
2. `TestRejectLowerTokens`: Verifies that any write carrying a token lower than the highest accepted token is rejected with `ErrStaleFencingToken`.
3. `TestAcceptNewerTokens`: Confirms that a higher token advances the state and rebinds owner identity.
4. `TestRejectDifferentOwnerOnSameToken`: Confirms that an unauthorized entity cannot hijack an active token epoch (`ErrOwnerMismatch`).
5. `TestStaleOwnerAfterNewerTokenAccepted`: Validates that once a newer token is accepted, an older owner can never execute writes (even if it was previously legitimate).
6. `TestInputValidation`: Confirms empty resource names, empty owners, and zero tokens are rejected.
7. `TestMultipleIndependentResources`: Verifies isolation between independent named resources.
8. `TestConcurrentRequests`: Verifies thread safety and race freedom under high concurrency (100 parallel same-lease writers and 50 competing advance operations).

---

---

## 6. Implemented Test Suites (Phase 2)

### 6.1 Phase 2 Unit Tests
1. **API Gateway (`backend/internal/gateway/router_test.go`):**
   - `TestGateway_LeaderDiscoveryAndCaching`: Direct leader discovery, TTL caching hit verification, and cache invalidation.
   - `TestGateway_Follower421Redirection`: Automatic 421 response handling, `X-Raft-Leader-ID` parsing, and retry to leader.
   - `TestGateway_QuorumUnavailable`: Fail-closed 503 response when coordinators are down.
2. **Client Heartbeat Manager (`backend/internal/heartbeat/manager_test.go`):**
   - `TestHeartbeatManager_Lifecycle`: Registration, lock attach/detach, heartbeat extension, and explicit session closure.
   - `TestHeartbeatManager_MissedHeartbeatTimeout`: Background scrubber detects missed heartbeats and triggers expiration hooks.
   - `TestHeartbeatManager_ConcurrentHeartbeats`: Concurrency-safe heartbeats under high parallel load.
3. **Automated Lease Renewal (`backend/internal/renewal/manager_test.go`):**
   - `TestRenewalManager_SuccessfulRenewalLifecycle`: Dynamic $T_{\text{renew}}$ scheduling, token preservation, and version increment ($V \to V+1$).
   - `TestRenewalManager_SessionTimeoutCancelsRenewal`: Heartbeat expiry hook cancels associated renewal workers immediately.
   - `TestRenewalManager_AmbiguousTimeoutReusesRequestID`: Preserves original RequestID across ambiguous network timeouts for FSM idempotency.
   - `TestRenewalManager_DuplicateWorkerPrevention`: Exactly 1 worker per resource generation.
4. **Metrics Registry (`backend/internal/metrics/metrics_test.go`):**
   - Counter, gauge, and Prometheus text exposition format validation with concurrent updates.

### 6.2 Phase 2 Integration Test Suite (`backend/tests/phase2_integration_test.go`)
Nine end-to-end scenarios run against real 3-node in-memory Raft coordinator clusters:
- **Scenario A: Transparent Gateway Routing to Leader (`TestScenarioA_TransparentGatewayRoutingToLeader`):** Verifies lock acquisition routed via gateway to leader, returning 200 OK with fencing token $\ge 1$ and `lease_version = 1`.
- **Scenario B: Leader Failover Routing Recovery (`TestScenarioB_LeaderFailoverRoutingRecovery`):** Cache invalidation triggers automatic rediscovery and transparent recovery on leader failover.
- **Scenario C: End-to-End Automatic Lease Renewal (`TestScenarioC_EndToEndAutomaticRenewal`):** Dynamic worker triggers renewal before expiry, verifying fencing token preservation and lease version increment ($1 \to 2$).
- **Scenario D: Missed Heartbeat Session Timeout Stops Renewal (`TestScenarioD_MissedHeartbeatSessionTimeoutStopsRenewal`):** Client inactivity triggers scrubber timeout, halting renewal workers and allowing lock to expire.
- **Scenario E: Quorum Loss Causes Renewal to Fail Closed Safely (`TestScenarioE_QuorumLossFailsClosedSafely`):** Simulates quorum loss, ensuring worker fails closed without state corruption.
- **Scenario F: Ambiguous Timeout Retries with Original RequestID (`TestScenarioF_AmbiguousTimeoutRetriesWithOriginalRequestID`):** Verifies that ambiguous timeouts reuse the original `RequestID` on retry, leveraging FSM idempotency.
- **Scenario G: Concurrent Release and Renewal (`TestScenarioG_ConcurrentReleaseAndRenewal`):** Explicit release causes competing renewal to fail permanently with 404/412 without panic.
- **Scenario H: New Owner Acquisition Cannot Be Renewed by Old Worker (`TestScenarioH_NewOwnerAcquisitionProtectsAgainstOldWorker`):** Subsequent acquisition with higher token prevents stale worker renewals.
- **Scenario I: Gateway Restart Does Not Falsely Restore In-Memory Sessions (`TestScenarioI_GatewayRestartClearsInMemorySessions`):** Gateway restart purges transient sessions while preserving authoritative Raft lock state on coordinators.

---

## 7. Implemented Test Suites (Phase 3)

### 7.1 Persistence Unit Tests (`backend/internal/persistence/persistence_test.go`)
- `TestConfigLoadAndSanitization`: Confirms `DATABASE_*` environment variable parsing, pool bounds, and password redaction in `SanitizedDSN()` and `String()`.
- `TestConfigFallbackLegacyEnvVars`: Verifies backward-compatible fallback to `DB_*` environment variables.
- `TestDatabaseNilConfigError`: Verifies safe failure when initialized with nil configuration.
- `TestHealthCheckUninitialized`: Validates error diagnostics when checking uninitialized pools.

### 7.2 Audit Pipeline Unit Tests (`backend/internal/audit/audit_test.go`)
- `TestEventValidationAndRedaction`: Asserts automatic redaction of `session_id`, `token_secret`, `password`, and `authorization` keys in metadata.
- `TestPipelineEnqueueAndBatchFlush`: Verifies non-blocking enqueue, batch flusher aggregation, and atomic counter updates.
- `TestPipelineBackpressureDropOnFullQueue`: Validates backpressure shedding and failure counting when queue capacity is reached.
- `TestHandlerEndpoints`: Validates `GET /v1/audit/events`, `GET /v1/audit/events/{id}`, and `GET /v1/audit/health`.

### 7.3 Operational Diagnostics Unit Tests (`backend/internal/recovery/recovery_test.go`)
- `TestDiagnosticsQuorumLossCausesUnhealthy`: Validates that leader reachability loss marks system `UNHEALTHY` (Fail Closed).
- `TestDiagnosticsDatabaseDownCausesDegradedNotUnhealthyWhenRaftLeaderPresent`: Asserts that a PostgreSQL failure degrades audit persistence to `DEGRADED` while keeping Raft lock coordination fully operational.
- `TestDiagnosticsSubsystemsReported`: Verifies all 4 subsystems (`raft_consensus`, `database_persistence`, `schema_migrations`, `audit_pipeline`) are reported.

### 7.4 Phase 3 Integration Test Suite (`backend/tests/phase3_audit_test.go`)
- `TestPhase3AuditPersistenceAndPagination`: Validates multi-event persistence, pagination boundary conditions, and filtering by resource.
- `TestPhase3DatabaseOutageIsolation`: Proves that 10 consecutive Raft lock state machine operations succeed completely during an active database failure, and pipeline resumes persistence upon database recovery.
- `TestPhase3DiagnosticsDistinguishesRaftFromDatabase`: Tests real multi-component health evaluations across leader and database combinations.
- `TestPhase3AuditSecretRedactionInAPI`: Verifies that sensitive credentials are never returned over HTTP audit query APIs.
