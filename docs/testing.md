# LockGuard Testing Strategy & Verification Guide

## 1. Testing Hierarchy

```
tests/
  ├── unit/             # Fast in-memory unit tests for isolated packages
  ├── integration/      # Multi-component tests (API + Storage + Coordinators)
  ├── concurrency/      # Race condition and mutual exclusion stress tests
  ├── fault_injection/  # Partition simulation, node kill/restart tests
  └── recovery/         # Snapshot restore and log compaction replay tests
```

---

## 2. Test Execution Commands

### Backend Unit Tests
```powershell
cd "D:\5TH SEM\SD\PROJECT\LOCKGUARD\backend"
go test -v ./...
```

### Protected Resource Unit Tests
```powershell
cd "D:\5TH SEM\SD\PROJECT\LOCKGUARD\protected-resource"
go test -v ./...
```

### Frontend Build & Typecheck
```powershell
cd "D:\5TH SEM\SD\PROJECT\LOCKGUARD\frontend"
npm run build
```

---

## 3. Implemented Test Suites (Phase 0)

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

## 4. Planned Test Suites (Phases 1–5)

| Suite | Focus | Invariants Tested |
| :--- | :--- | :--- |
| **Phase 1: Raft Consensus** | Leader election under normal and partitioned conditions. | At most 1 leader per term; quorum requirement of 2/3. |
| **Phase 2: Concurrency** | 100 concurrent clients attempting to acquire same lock. | Exactly 1 client acquires lock; 99 receive `ERR_LOCK_HELD`. |
| **Phase 3: Persistence** | Abrupt kill (`SIGKILL`) of leader during active lease. | New leader elected from remaining 2 nodes; lease intact. |
| **Phase 4: Fault Injection** | Network partition via `iptables` / packet drop. | Minority node immediately fails closed and ceases commits. |
| **Phase 5: Benchmarking** | Throughput & latency under 10k ops/sec. | P99 latency remains bounded; zero fencing token regressions. |

