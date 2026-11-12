# LOCKGUARD — Comprehensive Source Code Reference Guide

**Project Title:** Fault Tolerant Distributed Lock Service (LOCKGUARD)  
**Academic Course:** Software Design (SD) | Semester V (AY 2026–2027)  
**Institution:** Veermata Jijabai Technological Institute (VESIT)  
**Authors:** Prathamesh Shelar, Chandrabhanu Prusty  
**Supervisor:** Dr. Priya R.L.  
**Reference Version:** 2.0 (Phase 6 Final Release)  

---

## 1. Directory Structure & Key Subsystems

```text
LOCKGUARD/
├── backend/
│   ├── cmd/
│   │   ├── api/main.go                     # API Gateway entrypoint
│   │   └── coordinator/main.go             # Raft Coordinator node entrypoint
│   ├── internal/
│   │   ├── api/
│   │   │   ├── server.go                   # HTTP routing, middleware wiring, reverse proxy, admin endpoints
│   │   │   └── response.go                 # Unified JSON response envelopes & error codes
│   │   ├── audit/
│   │   │   ├── event.go                    # Audit event schema, redaction, and serialization
│   │   │   ├── pipeline.go                 # Non-blocking bounded audit channel, flusher, retry backoff
│   │   │   └── handler.go                  # Audit query HTTP handlers & tenant isolation guards
│   │   ├── auth/
│   │   │   ├── jwt.go                      # Zero-dependency HS256 JWT generator & validator
│   │   │   ├── service.go                  # User authentication service, PBKDF2 salt/hashing, seeded users
│   │   │   └── middleware.go               # Authenticate, RequireRole, and EnforceOwnerIsolation
│   │   ├── consensus/
│   │   │   ├── node.go                     # HashiCorp Raft node wrapper, cluster bootstrap, leadership tracking
│   │   │   ├── fsm_adapter.go              # Raft FSM interface implementation (Apply, Snapshot, Restore)
│   │   │   ├── codec.go                    # Command serialization & envelope validation
│   │   │   └── snapshot.go                 # State snapshot serialization & atomic restore
│   │   ├── coordinator/
│   │   │   ├── handler.go                  # HTTP endpoints for acquire, renew, release, expire, stepdown
│   │   │   └── node.go                     # Coordinator node consensus delegate
│   │   ├── gateway/
│   │   │   └── router.go                   # Dynamic leader discovery, 421 retry proxy, caching
│   │   ├── heartbeat/
│   │   │   └── manager.go                  # Client session lifecycle, keep-alive scrubber, session timeouts
│   │   ├── lockstate/
│   │   │   ├── fsm.go                      # Pure deterministic Finite State Machine (zero dependencies)
│   │   │   ├── commands.go                 # Command structures (Acquire, Renew, Release, Expire)
│   │   │   ├── types.go                    # LockRecord, ResourceState, LockStatus types
│   │   │   └── idempotency.go              # RequestID cache & payload fingerprinting
│   │   ├── middleware/
│   │   │   ├── ratelimit.go                # Bounded-memory token-bucket rate limiter with keyed fingerprints
│   │   │   ├── security.go                 # Security headers (HSTS, CSP, X-Frame-Options, nosniff)
│   │   │   └── logging.go                  # Structured JSON request logging & tracing
│   │   ├── persistence/
│   │   │   ├── postgres_audit_repo.go      # PostgreSQL audit event repository, batch insert, tenant query
│   │   │   ├── migrator.go                 # SQL schema migrator with advisory locking
│   │   │   └── pool.go                     # PostgreSQL connection pool lifecycle
│   │   └── renewal/
│   │       └── manager.go                  # Automated lease renewal background workers
│   └── tests/
│       ├── phase2_integration_test.go      # Gateway, session, and renewal integration tests
│       ├── phase3_audit_test.go            # Audit pipeline & database outage tests
│       ├── phase4_chaos_runner_test.go     # Programmatic fault injection & recovery tests
│       └── phase6_e2e_integration_test.go  # 10 comprehensive end-to-end operational workflows
├── protected-resource/
│   ├── cmd/main.go                         # Protected resource HTTP service entrypoint
│   └── internal/fencing.go                 # FencingGuard: Martin Kleppmann monotonic token validator
├── frontend/
│   ├── src/
│   │   ├── pages/
│   │   │   ├── OverviewPage.tsx            # Cluster overview, topology, subsystem architecture status
│   │   │   ├── ClusterPage.tsx             # Live Raft node telemetry, leader/follower, admin stepdown
│   │   │   ├── LocksPage.tsx               # Distributed lock management, role-aware release/unlock
│   │   │   ├── AuditPage.tsx               # Audit trail view, timeline, multi-tenant isolation
│   │   │   ├── BenchmarksPage.tsx          # Throughput benchmarks, live 10-iteration latency probe
│   │   │   ├── SettingsPage.tsx            # Cluster parameters, masked secrets, admin danger zone
│   │   │   ├── ResiliencePage.tsx          # Chaos telemetry, quorum health, recovery status
│   │   │   └── LoginPage.tsx               # HS256 JWT login with quick-fill academic demo accounts
│   │   ├── components/
│   │   │   ├── Header.tsx                  # Top banner with live API Online/Offline status indicator
│   │   │   └── Sidebar.tsx                 # Role-aware navigation with ADMIN, OPERATOR, AUDITOR badges
│   │   ├── context/
│   │   │   └── AuthContext.tsx             # React authentication provider with localStorage caching
│   │   └── services/
│   │       ├── api.ts                      # Axios HTTP client configured with baseURL and bearer tokens
│   │       ├── clusterService.ts           # Cluster status, lock acquire/renew/release, admin stepdown
│   │       └── resilienceService.ts        # Recovery telemetry and cluster recovery diagnostics
├── deploy/
│   ├── docker/                             # Dockerfiles (api, coordinator, frontend, protected-resource)
│   ├── nginx/nginx.conf                    # Nginx reverse proxy, static asset serving, /api/ rewrite
│   └── prometheus/prometheus.yml           # Prometheus scrape targets configuration
└── docker-compose.yml                      # 9-Service production topology orchestration
```

---

## 2. Key Subsystem Source Code Descriptions

### 2.1 Backend API Server & Gateway (`backend/internal/api/server.go`)
- **Primary Function:** Acts as the central HTTP entry point. Configures the middleware chain (SecurityHeaders -> MaxBodySize -> RateLimiter -> RequestLogger -> CORS -> Recovery), maps routes, injects authentication, dynamically discovers the Raft leader, and routes requests.
- **Admin Operations:** Houses `handleAdminEmergencyUnlock` and `handleAdminStepdown`, protected by `RequireRole(auth.RoleAdmin)`.
- **Audit Interception:** Extracts fencing tokens and lease versions from responses via pooled response recorders (`sync.Pool`) and emits non-blocking audit events to the pipeline.

### 2.2 Deterministic Lock State Machine (`backend/internal/lockstate/fsm.go`)
- **Primary Function:** Pure deterministic finite state machine maintaining all active lock records, resource indexes, and monotonic fencing token counters.
- **Mathematical Determinism:** Completely isolated from host system clocks (`time.Now()` is never invoked). Logical time advances monotonically via `effectiveNow = max(cmd.TimestampUnixNano, s.lastAppliedUnixNano)`.
- **Transitions Implemented:**
  - `AcquireLock`: Sets active lock, initializes `lease_version = 1`, advances `HighestToken = HighestToken + 1`.
  - `RenewLock`: Requires matching owner, session, and fencing token; advances `lease_version = lease_version + 1`; preserves fencing token.
  - `ReleaseLock`: Requires matching owner and token; clears active lock; retains `HighestToken` permanently.
  - `ExpireLock`: Deterministically reclaims timed-out leases; guards against stale version/timestamp races.

### 2.3 Raft Consensus Node (`backend/internal/consensus/node.go`, `fsm_adapter.go`)
- **Primary Function:** Wraps HashiCorp Raft. Implements cluster bootstrap, leader election, log replication, and FileSnapshotStore compaction.
- **Quorum Requirement:** Enforces majority rule ($Q = \lfloor N/2 \rfloor + 1 = 2$ for $N=3$). In an isolated minority, mutations fail closed immediately.
- **Linearizable Reads:** Executes `VerifyLeader()` and `Barrier()` to ensure reads reflect authoritative state without stale leader anomalies.

### 2.4 Downstream Fencing Guard (`protected-resource/internal/fencing.go`)
- **Primary Function:** Implements Martin Kleppmann's fencing token validation algorithm for downstream storage.
- **Semantics:**
  - Token $< \text{HighestToken}$: Stale write. Rejected unconditionally with HTTP 409 Conflict (`ERR_FENCING_TOKEN_STALE`).
  - Token $== \text{HighestToken}$: Legitimate repeated write by same owner. Accepted with HTTP 200. Cross-owner collision rejected with HTTP 409 (`ERR_OWNER_MISMATCH`).
  - Token $> \text{HighestToken}$: New lease epoch. Advances `HighestToken` and rebinds owner.

### 2.5 Asynchronous Audit Pipeline (`backend/internal/audit/pipeline.go`)
- **Primary Function:** Decouples consensus commits from relational database disk I/O.
- **Mechanics:** Bounded in-memory Go channel (capacity: 50,000 events). Enqueueing is non-blocking ($\mathcal{O}(1)$). A background worker batches events (size: 50 or 50ms) and flushes them to PostgreSQL using prepared statements and exponential backoff with jitter (up to 3 retries).
- **Isolation Invariant:** PostgreSQL downtime never degrades Raft lock throughput.

### 2.6 Cryptographic Authentication & RBAC (`backend/internal/auth/jwt.go`, `service.go`)
- **Primary Function:** Implements RFC 7519 HMAC-SHA256 (HS256) JWT generation and constant-time validation with zero external dependencies.
- **Claims Schema:** `sub` (bound OwnerID / user ID), `name` (username), `role` (`ADMIN`, `CLIENT`, `READ_ONLY`), `iss`, `aud`, `iat`, `exp`.
- **Identity Binding:** `EnforceOwnerIsolation` verifies that a `CLIENT` role subject matches the payload `owner_id`, rejecting cross-client operations with HTTP 403.
