# LockGuard — Fault Tolerant Distributed Lock Service

[![Go](https://img.shields.io/badge/Go-1.27.3-00ADD8?style=flat&logo=go)](https://go.dev)
[![React](https://img.shields.io/badge/React-18.3-61DAFB?style=flat&logo=react)](https://react.dev)
[![Vite](https://img.shields.io/badge/Vite-6.1-646CFF?style=flat&logo=vite)](https://vitejs.dev)
[![TailwindCSS](https://img.shields.io/badge/Tailwind-3.4-38B2AC?style=flat&logo=tailwind-css)](https://tailwindcss.com)
[![Docker](https://img.shields.io/badge/Docker-Compose_v5-2496ED?style=flat&logo=docker)](https://www.docker.com)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

> **Academic Title:** Fault Tolerant Distributed Lock Service  
> **Project Type:** Distributed Systems / Software Engineering  
> **Core Invariant:** `CONSISTENCY FIRST — FAIL CLOSED`

---

## 1. Executive Summary

**LockGuard** is a fault-tolerant distributed coordination service that provides exclusive access to named shared resources across multiple client applications. It employs a three-node Raft consensus cluster to maintain consistent, replicated lock state across failure domains.

The system implements:
- Distributed lock acquisition, lease heartbeats, and Martin Kleppmann's fencing tokens.
- Deterministic, concurrency-safe in-memory state machine with request-level idempotency (`backend/internal/lockstate`).
- Majority quorum replication ($2 \text{ of } 3 \text{ nodes}$) with strictly fail-closed semantics upon quorum loss.
- In-memory replicated state machine backed by durable BoltDB consensus logs and snapshot recovery.
- Independent fencing state verification at external protected resources.
- Operational metadata, user governance, audit logging, and benchmark persistence in PostgreSQL.
- Modern enterprise React operator dashboard with deep-navy styling and real-time cluster probe inspection.

---

## 2. Architectural Overview

```
+-----------------------------------------------------------------------------------+
|                              CLIENT APPLICATIONS                                  |
+-----------------------------------------------------------------------------------+
                                         |
                                         v
+-----------------------------------------------------------------------------------+
|                       NGINX EDGE GATEWAY (:80 / :443)                             |
|          Routes /api/ to Go Gateway  •  Routes / to React SPA Dashboard           |
+-----------------------------------------------------------------------------------+
                 |                                                 |
                 v                                                 v
+---------------------------------+             +-----------------------------------+
|      LOCKGUARD API GATEWAY      |             |         REACT OPERATOR UI         |
|             (:8080)             |             |          (Vite / Tailwind)        |
+---------------------------------+             +-----------------------------------+
                 |
                 +-----------------------+
                 |                       |
                 v                       v
+---------------------------------+    +--------------------------------------------+
|     POSTGRESQL 16 METADATA      |    |      THREE-NODE RAFT COORDINATOR CLUSTER   |
|   (Users, Audits, Benchmarks)   |    |         (Fail Closed Quorum: 2 of 3)       |
|  *No Authoritative Lock State*  |    |  • Coordinator A (:7000 / :8001)           |
+---------------------------------+    |  • Coordinator B (:7000 / :8001)           |
                                       |  • Coordinator C (:7000 / :8001)           |
                                       |  *Private Network: Port 7000 Not Public*   |
                                       +--------------------------------------------+
                                                         |
                                                         v
                                       +--------------------------------------------+
                                       |         PROTECTED RESOURCE SERVICE         |
                                       |                  (:9000)                   |
                                       |      Validates Monotonic Fencing Tokens    |
                                       +--------------------------------------------+
```

---

## 3. Technology Stack

- **Backend:** Go (`go1.27.3`), HashiCorp Raft (`github.com/hashicorp/raft` v1.7.2 + `github.com/hashicorp/raft-boltdb/v2` v2.3.1), structured logging (`log/slog`), HTTP middleware stack.
- **Frontend:** React 18, Vite 6, TypeScript 5.7, Tailwind CSS 3.4, React Router 6, Axios, Lucide React, Recharts.
- **Storage:**
  - *Consensus Tier:* Local BoltDB log store, stable metadata store, and file snapshot storage per coordinator node.
  - *Relational Tier:* PostgreSQL 16 (for audit events, benchmark runs, user management, and dashboard preferences).
  - *Protected Tier:* Independent fencing state guard at the protected resource.
- **Infrastructure:** Docker, Docker Compose, Nginx reverse proxy, Prometheus metrics scraper.

---

## 4. Repository Structure

```
LOCKGUARD/
  ├── backend/                     # Go Coordination Engine & API Gateway
  │   ├── cmd/
  │   │   ├── api/                 # LockGuard REST API & Gateway entry point
  │   │   └── coordinator/         # Coordinator daemon entry point (Nodes A, B, C)
  │   ├── internal/
  │   │   ├── api/                 # Server, handlers, standardized JSON envelopes
  │   │   ├── audit/               # Audit trail generation
  │   │   ├── auth/                # JWT and authentication tokens
  │   │   ├── benchmark/           # Benchmark orchestration
  │   │   ├── cluster/             # Cluster topology definitions
  │   │   ├── config/              # Environment config loading & validation
  │   │   ├── consensus/           # HashiCorp Raft FSM, cluster bootstrap, codecs
  │   │   ├── coordinator/         # Coordinator HTTP handlers & linearizable reads
  │   │   ├── events/              # Event dispatching
  │   │   ├── fencing/             # Fencing token utilities
  │   │   ├── gateway/             # Leader discovery, reverse proxy, 421 retry
  │   │   ├── heartbeat/           # Client session tracking & inactivity scrubber
  │   │   ├── leases/              # High-level lease abstractions
  │   │   ├── locks/               # Lock manager services
  │   │   ├── lockstate/           # Phase 1A deterministic state machine
  │   │   ├── metrics/             # Concurrency-safe Prometheus registry (/metrics)
  │   │   ├── middleware/          # CORS, panic recovery, structured logging
  │   │   ├── recovery/            # Crash recovery & log replay
  │   │   ├── renewal/             # Automated lease renewal scheduler & workers
  │   │   ├── snapshots/           # Snapshot stores
  │   │   ├── state_machine/       # Legacy interfaces
  │   │   └── storage/             # PostgreSQL cold storage layer
  │   └── tests/                   # End-to-end integration test scenarios (A-I)
  │   │   ├── fencing/             # Fencing token counters
  │   │   ├── leases/              # Lease TTL managers
  │   │   ├── locks/               # Lock state logic
  │   │   ├── lockstate/           # Deterministic In-Memory Lock State Machine (Phase 1A)
  │   │   │   ├── types.go         # LockRecord, ResourceState, IdempotencyRecord
  │   │   │   ├── commands.go      # Acquire, Renew, Release, Expire commands & SHA-256 fingerprints
  │   │   │   ├── state_machine.go # StateMachine interface & MemoryStateMachine (sync.RWMutex)
  │   │   │   ├── acquire.go       # Monotonic fencing token acquire logic
  │   │   │   ├── renew.go         # Token-preserving lease renewal logic
  │   │   │   ├── release.go       # Safe explicit release logic
  │   │   │   ├── expire.go        # Deterministic expiration logic
  │   │   │   ├── query.go         # Safe cloned read-only queries
  │   │   │   ├── errors.go        # Sentinel error definitions & codes
  │   │   │   ├── fixtures_test.go # Test timestamps & command builders
  │   │   │   ├── state_machine_test.go # Unit tests (Categories A–G)
  │   │   │   └── concurrency_test.go   # Stress & race tests (Category H)
  │   │   ├── metrics/             # Prometheus metric counters
  │   │   ├── middleware/          # Slog logger, CORS, panic recovery
  │   │   ├── recovery/            # Crash recovery & log replay
  │   │   ├── snapshots/           # Snapshot managers
  │   │   ├── state_machine/       # Replicated FSM integration
  │   │   └── storage/             # PostgreSQL connection pool
  │   ├── migrations/              # PostgreSQL schema migrations
  │   └── tests/                   # Backend unit tests
  │
  ├── frontend/                    # React + Vite + TypeScript Operator Dashboard
  │   └── src/
  │       ├── app/                 # Root App and router declarations
  │       ├── charts/              # Recharts visualizations
  │       ├── components/          # Sidebar, Header, ProtectedRoute
  │       ├── context/             # AuthContext session state
  │       ├── hooks/               # Custom hooks
  │       ├── layouts/             # DashboardLayout (Deep Navy sidebar)
  │       ├── pages/               # Overview, Locks, Cluster, Audit, Benchmarks
  │       ├── services/            # Axios API clients
  │       └── styles/              # Tailwind CSS directives
  │
  ├── protected-resource/          # Target service validating fencing tokens
  │   ├── cmd/                     # Protected resource entry point (:9000)
  │   └── internal/                # Monotonic FencingGuard and unit tests
  │
  ├── deploy/                      # Infrastructure configurations
  │   ├── docker/                  # Dockerfiles for API, Coordinator, Frontend, PR
  │   ├── nginx/                   # Nginx reverse proxy configuration
  │   └── prometheus/              # Prometheus scraping configuration
  │
  ├── tests/                       # Test specifications & suites
  │   ├── unit/
  │   ├── integration/
  │   ├── concurrency/
  │   ├── fault_injection/
  │   └── recovery/
  │
  ├── benchmarks/                  # Benchmark scripts and scenarios
  ├── scripts/                     # Operational automation scripts
  │
  ├── docs/                        # Comprehensive Architecture & Specification Docs
  │   ├── architecture.md          # System architecture, topology, failure domains
  │   ├── technical-specification.md # Raft candidate analysis, state structures, ports
  │   ├── api-contract.md          # REST API payloads, status codes, error catalogue
  │   ├── state-model.md           # State machine lifecycle and mathematical proofs
  │   ├── deployment.md            # Docker Compose & local execution guide
  │   ├── testing.md               # Test hierarchy and execution commands
  │   └── user-guide.md            # Operator guide and client integration pattern
  │
  ├── docker-compose.yml           # 9-service production-ready topology
  ├── .env.example                 # Comprehensive environment template
  ├── .gitignore                   # Binaries, volumes, vendor, keys ignored
  ├── LICENSE                      # Apache-2.0 License
  └── README.md                    # Project documentation
```

---

## 5. Verification & Test Results

### 1. Phase 1A: Lock State Machine Verification (`backend/internal/lockstate`)
All 32 test suites across Categories A–I passed cleanly without data races:

```powershell
go test -v -count=1 ./internal/lockstate
```
```
=== RUN   TestConcurrentAcquireSingleResource
--- PASS: TestConcurrentAcquireSingleResource (0.00s)
=== RUN   TestConcurrentRenewals
--- PASS: TestConcurrentRenewals (0.00s)
=== RUN   TestConcurrentReleaseAndAcquire
--- PASS: TestConcurrentReleaseAndAcquire (0.00s)
=== RUN   TestConcurrentMultiResourceStress
--- PASS: TestConcurrentMultiResourceStress (0.00s)
=== RUN   TestAcquireUnlockedResource
--- PASS: TestAcquireUnlockedResource (0.00s)
=== RUN   TestAcquireActiveResourceBySecondOwnerFails
--- PASS: TestAcquireActiveResourceBySecondOwnerFails (0.00s)
=== RUN   TestAcquireActiveResourceBySameOwnerWithDifferentRequestIDFails
--- PASS: TestAcquireActiveResourceBySameOwnerWithDifferentRequestIDFails (0.00s)
=== RUN   TestAcquireInputValidation
--- PASS: TestAcquireInputValidation (0.00s)
=== RUN   TestAcquireAfterLeaseExpirationAdvancesTokenAndGrants
--- PASS: TestAcquireAfterLeaseExpirationAdvancesTokenAndGrants (0.00s)
=== RUN   TestRenewActiveLock
--- PASS: TestRenewActiveLock (0.00s)
=== RUN   TestRenewRejections
--- PASS: TestRenewRejections (0.00s)
=== RUN   TestRenew_LeaseVersionOverflow
--- PASS: TestRenew_LeaseVersionOverflow (0.00s)
=== RUN   TestReleaseActiveLock
--- PASS: TestReleaseActiveLock (0.00s)
=== RUN   TestReleaseRejections
--- PASS: TestReleaseRejections (0.00s)
=== RUN   TestExpireLock
--- PASS: TestExpireLock (0.00s)
=== RUN   TestExpire_StaleCommandRejectedAfterRenewal
--- PASS: TestExpire_StaleCommandRejectedAfterRenewal (0.00s)
=== RUN   TestExpire_OldExpirationRejectedWhenRenewalRetainsExactSameExpiryTimestamp
--- PASS: TestExpire_OldExpirationRejectedWhenRenewalRetainsExactSameExpiryTimestamp (0.00s)
=== RUN   TestExpire_ValidAfterRenewalExpires
--- PASS: TestExpire_ValidAfterRenewalExpires (0.00s)
=== RUN   TestExpire_DifferentLeaseGenerationRejected
--- PASS: TestExpire_DifferentLeaseGenerationRejected (0.00s)
=== RUN   TestExpire_RejectedLeavesStateUnchanged
--- PASS: TestExpire_RejectedLeavesStateUnchanged (0.00s)
=== RUN   TestExpire_Idempotency
--- PASS: TestExpire_Idempotency (0.00s)
=== RUN   TestIdempotencyAcquire
--- PASS: TestIdempotencyAcquire (0.00s)
=== RUN   TestIdempotencyRenew
--- PASS: TestIdempotencyRenew (0.00s)
=== RUN   TestIdempotencyRelease
--- PASS: TestIdempotencyRelease (0.00s)
=== RUN   TestFencingTokensMonotonicityAndIndependence
--- PASS: TestFencingTokensMonotonicityAndIndependence (0.00s)
=== RUN   TestFencingTokenOverflowHandling
--- PASS: TestFencingTokenOverflowHandling (0.00s)
=== RUN   TestDeterminismAndLogicalTimePolicy
--- PASS: TestDeterminismAndLogicalTimePolicy (0.00s)
=== RUN   TestArithmeticOverflow_AcquireTTL
--- PASS: TestArithmeticOverflow_AcquireTTL (0.00s)
=== RUN   TestArithmeticOverflow_AcquireTimestamp
--- PASS: TestArithmeticOverflow_AcquireTimestamp (0.00s)
=== RUN   TestArithmeticOverflow_RenewTTL
--- PASS: TestArithmeticOverflow_RenewTTL (0.00s)
=== RUN   TestInputValidation_TimestampAndTTL
--- PASS: TestInputValidation_TimestampAndTTL (0.00s)
=== RUN   TestAtomicity_FailedAcquireLeavesStateUnchanged
--- PASS: TestAtomicity_FailedAcquireLeavesStateUnchanged (0.00s)
=== RUN   TestAtomicity_FailedRenewLeavesStateUnchanged
--- PASS: TestAtomicity_FailedRenewLeavesStateUnchanged (0.00s)
=== RUN   TestAtomicity_FailedReleaseLeavesStateUnchanged
--- PASS: TestAtomicity_FailedReleaseLeavesStateUnchanged (0.00s)
=== RUN   TestLogicalTimePolicy_ValidationVsBusinessFailure
--- PASS: TestLogicalTimePolicy_ValidationVsBusinessFailure (0.00s)
PASS
ok      lockguard/backend/internal/lockstate    0.206s
```

Race detector validation:
```powershell
go test -v -race -count=1 ./internal/lockstate
# PASS - 0 data races detected
ok      lockguard/backend/internal/lockstate    1.242s
```

### 2. Backend Server & Config Verification (`backend/tests`)
```
=== RUN   TestDefaultConfig
--- PASS: TestDefaultConfig (0.00s)
=== RUN   TestConfigValidationFailure
--- PASS: TestConfigValidationFailure (0.00s)
=== RUN   TestEnvOverride
--- PASS: TestEnvOverride (0.00s)
=== RUN   TestHealthEndpoint
--- PASS: TestHealthEndpoint (0.00s)
=== RUN   TestReadinessEndpoint
--- PASS: TestReadinessEndpoint (0.00s)
=== RUN   TestMethodNotAllowedOnHealth
--- PASS: TestMethodNotAllowedOnHealth (0.00s)
=== RUN   TestServerRoutingAndClusterStatus
--- PASS: TestServerRoutingAndClusterStatus (0.00s)
=== RUN   TestStandardErrorFormatting
--- PASS: TestStandardErrorFormatting (0.00s)
PASS
ok      lockguard/backend/tests 0.854s
```

### 3. HashiCorp Raft Consensus & Coordinator Verification (`backend/internal/consensus` & `backend/internal/coordinator`)
```
=== RUN   TestCluster_ThreeNodeElectionAndReplication
--- PASS: TestCluster_ThreeNodeElectionAndReplication (0.10s)
=== RUN   TestCluster_LeaderFailover
--- PASS: TestCluster_LeaderFailover (0.18s)
=== RUN   TestCluster_QuorumLoss_FailClosed
    cluster_test.go:344: Write correctly failed closed without quorum: leadership lost while committing log
--- PASS: TestCluster_QuorumLoss_FailClosed (0.14s)
=== RUN   TestCluster_BoltDBPersistenceAndRecovery
--- PASS: TestCluster_BoltDBPersistenceAndRecovery (0.37s)
=== RUN   TestCodec_AcquireCommandRoundTrip
--- PASS: TestCodec_AcquireCommandRoundTrip (0.00s)
=== RUN   TestCodec_RenewCommandRoundTrip
--- PASS: TestCodec_RenewCommandRoundTrip (0.00s)
=== RUN   TestCodec_ReleaseCommandRoundTrip
--- PASS: TestCodec_ReleaseCommandRoundTrip (0.00s)
=== RUN   TestCodec_ExpireCommandRoundTrip
--- PASS: TestCodec_ExpireCommandRoundTrip (0.00s)
=== RUN   TestCodec_InvalidEnvelopeRejections
--- PASS: TestCodec_InvalidEnvelopeRejections (0.00s)
=== RUN   TestLockFSM_ApplyAndSnapshotRestore
--- PASS: TestLockFSM_ApplyAndSnapshotRestore (0.00s)
=== RUN   TestRaftNode_SingleNodeAcquireRenewRelease
--- PASS: TestRaftNode_SingleNodeAcquireRenewRelease (0.06s)
PASS
ok      lockguard/backend/internal/consensus    1.476s

=== RUN   TestCoordinatorHandler_AcquireRenewAndRead
--- PASS: TestCoordinatorHandler_AcquireRenewAndRead (0.07s)
PASS
ok      lockguard/backend/internal/coordinator  0.569s
```

### 4. Protected Resource Verification (`protected-resource/internal`)
```
=== RUN   TestRepeatedSameLeaseWrites
--- PASS: TestRepeatedSameLeaseWrites (0.00s)
=== RUN   TestRejectLowerTokens
--- PASS: TestRejectLowerTokens (0.00s)
=== RUN   TestAcceptNewerTokens
--- PASS: TestAcceptNewerTokens (0.00s)
=== RUN   TestRejectDifferentOwnerOnSameToken
--- PASS: TestRejectDifferentOwnerOnSameToken (0.00s)
=== RUN   TestStaleOwnerAfterNewerTokenAccepted
--- PASS: TestStaleOwnerAfterNewerTokenAccepted (0.00s)
=== RUN   TestInputValidation
--- PASS: TestInputValidation (0.00s)
=== RUN   TestMultipleIndependentResources
--- PASS: TestMultipleIndependentResources (0.00s)
=== RUN   TestConcurrentRequests
--- PASS: TestConcurrentRequests (0.00s)
PASS
ok      lockguard/protected-resource/internal   0.599s
```

### 5. Frontend Build Verification (`frontend/`)
```
vite v6.4.5 building for production...
✓ 1669 modules transformed.
dist/index.html                   0.88 kB │ gzip:  0.51 kB
dist/assets/index-DpY4_pzL.css   30.09 kB │ gzip:  5.64 kB
dist/assets/index-XxF6zmhf.js   314.38 kB │ gzip: 94.74 kB
✓ built in 7.37s
```

### 6. Phase 3 Audit & Persistence Verification (`backend/internal/audit`, `backend/internal/persistence`)
```
=== RUN   TestPhase3AuditPersistenceAndPagination
--- PASS: TestPhase3AuditPersistenceAndPagination (0.12s)
=== RUN   TestPhase3DatabaseOutageIsolation
--- PASS: TestPhase3DatabaseOutageIsolation (0.15s)
=== RUN   TestPhase3DiagnosticsDistinguishesRaftFromDatabase
--- PASS: TestPhase3DiagnosticsDistinguishesRaftFromDatabase (0.01s)
=== RUN   TestPhase3AuditSecretRedactionInAPI
--- PASS: TestPhase3AuditSecretRedactionInAPI (0.00s)
PASS
ok      lockguard/backend/tests   1.996s
```

### 7. Docker Compose Configuration Validation
```powershell
docker compose config
# Exited with code 0 - All 9 services, 4 volumes, and 2 networks validated successfully.
```

---

## 6. Getting Started

### Local Quickstart

1. **Run Full Test Suite (Unit & 9 Integration Scenarios):**
   ```powershell
   cd backend
   go test -v -race -count=1 ./...
   ```

2. **Run Protected Resource Tests:**
   ```powershell
   cd protected-resource
   go test -v -race -count=1 ./...
   ```

3. **Start the Go API Gateway:**
   ```powershell
   cd backend
   go run ./cmd/api
   ```
   Verify at `http://localhost:8080/health` and `http://localhost:8080/metrics`.

4. **Start the Frontend Dashboard:**
   ```powershell
   cd frontend
   npm run dev
   ```
   Open `http://localhost:3000` in your browser.

---

## 7. License

Licensed under the [Apache License, Version 2.0](LICENSE).
