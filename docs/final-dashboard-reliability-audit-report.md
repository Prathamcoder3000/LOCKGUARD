# LOCKGUARD — Final Dashboard & Backend Reliability Audit Report

**Project Name:** LOCKGUARD — Fault Tolerant Distributed Lock Service  
**Project Root:** `D:\5TH SEM\SD\PROJECT\LOCKGUARD`  
**Audit Local Time:** 2026-11-06T11:18:00+05:30  
**Audit Type:** Evidence-Based Final Reliability & Invariant Audit  
**Auditor Classification:** Independent Distributed Systems & Security Audit  

---

## 1. Executive Summary

An evidence-based final correctness and reliability audit was conducted across the LOCKGUARD distributed coordination platform, encompassing the Go API Gateway, Raft consensus coordinators, PostgreSQL audit pipeline, protected resource fencing guard, Nginx reverse proxy, and React administrative/operator dashboards.

The audit verified runtime behavior through live HTTP probes against the containerized production stack, audited source code against formal distributed locking invariants, executed test suites across all backend packages, and evaluated tenant isolation, role-based authorization, and secret safety.

All core distributed lock invariants (exclusive acquire, monotonic fencing tokens, zombie write rejection, owner isolation, admin emergency break, Raft leader stepdown, and multi-tenant audit isolation) were **VERIFIED** through direct runtime execution. Genuine defects discovered in the frontend build and telemetry display during the audit were diagnosed and resolved.

---

## 2. Files and Modules Inspected

| Subsystem | Inspected Files | Scope of Inspection |
| :--- | :--- | :--- |
| **Reverse Proxy & Routing** | `deploy/nginx/nginx.conf` | Path rewriting, `/api/` prefix stripping, header forwarding. |
| **API Gateway & Middleware** | `backend/internal/api/server.go`<br>`backend/internal/api/response.go`<br>`backend/internal/middleware/ratelimit.go`<br>`backend/internal/middleware/security.go` | Route registration (`/v1/health`, `/api/v1/health`), RBAC middleware, error structures, rate limiter identity safety. |
| **Authentication & RBAC** | `backend/internal/auth/service.go`<br>`backend/internal/auth/jwt.go`<br>`backend/internal/auth/middleware.go`<br>`backend/internal/config/config.go` | In-memory user store, password hashing, JWT HS256 claims, owner ID binding, production environment guards. |
| **Raft Consensus Cluster** | `backend/internal/coordinator/handler.go`<br>`backend/internal/coordinator/node.go`<br>`backend/internal/consensus/fsm.go`<br>`backend/internal/consensus/snapshot.go` | ApplyCommand, Acquire/Renew/Release/Expire handlers, leadership transfer, linearizable read consistency. |
| **Protected Resource** | `protected-resource/cmd/main.go`<br>`protected-resource/internal/fencing.go`<br>`protected-resource/internal/fencing_test.go` | FencingGuard atomicity, highest accepted token tracking, stale token rejection (HTTP 409 Conflict). |
| **Audit & Persistence** | `backend/internal/audit/pipeline.go`<br>`backend/internal/audit/handler.go`<br>`backend/internal/persistence/postgres_audit_repo.go` | Asynchronous bounded ring buffer, queue saturation, retry backoff, multi-tenant SQL query isolation. |
| **Frontend Application** | `frontend/src/pages/OverviewPage.tsx`<br>`frontend/src/pages/ClusterPage.tsx`<br>`frontend/src/pages/LocksPage.tsx`<br>`frontend/src/pages/BenchmarksPage.tsx`<br>`frontend/src/pages/SettingsPage.tsx`<br>`frontend/src/pages/LoginPage.tsx`<br>`frontend/src/components/Header.tsx`<br>`frontend/src/components/Sidebar.tsx` | Telemetry sourcing, secret masking, role gating, elimination of misleading labels, TypeScript build validity. |

---

## 3. Defects Discovered During Audit

1. **TypeScript Compilation Failures (`npm run build`):**
   - In `frontend/src/pages/BenchmarksPage.tsx`: Unused import `BarChart3`, unused state `probeResults`, and an unclosed JSX wrapper tag caused TypeScript compiler errors TS6133 and TS17008.
   - In `frontend/src/pages/SettingsPage.tsx`: Unused import `Settings` caused TS6133 under strict compiler flags.
2. **Telemetry Fallback Placeholders in Cluster Dashboard:**
   - In `frontend/src/pages/ClusterPage.tsx`: Fallback literals `term: '3'` and `applied_index: '25'` were hardcoded when live stats were loading, presenting potentially misleading static values.
3. **Ambiguous Architecture Labels on Overview Dashboard:**
   - In `frontend/src/pages/OverviewPage.tsx`: The topology card was labeled `"Planned 3-node Raft consensus group configuration"` despite the cluster running live in Docker. The Subsystem Architecture table did not clearly state that its checkmarks reflect academic project verification milestones rather than live health pings.
4. **Historical Benchmark Labeling Ambiguity:**
   - In `frontend/src/pages/BenchmarksPage.tsx`: Static baseline throughput numbers (`25,400 ops/s` and `1,250 ops/s`) lacked explicit labeling distinguishing historical Phase 5 test results from the live interactive probe.

---

## 4. Fixes Applied

1. **Resolved TypeScript Errors & Integrated Telemetry Log:**
   - Removed unused imports (`BarChart3`, `Settings`).
   - Integrated `probeResults` into `BenchmarksPage.tsx` by rendering an iteration-by-iteration latency sample log (`#1: 1.2ms`, `#2: 0.9ms`...).
   - Corrected JSX tag balance in `BenchmarksPage.tsx`.
   - Verified clean compilation with `tsc && vite build` (Exit code 0).
2. **Eliminated Hardcoded Telemetry Fallbacks:**
   - Replaced fallback literals `'3'` and `'25'` in `ClusterPage.tsx` with dynamic `'—'` indicators during initial telemetry loading.
3. **Clarified Architectural Labels on Overview Dashboard:**
   - Updated the card description in `OverviewPage.tsx` to `"Specification Architecture • View Cluster tab for live dynamic telemetry"`.
   - Added a clear milestone status badge `"Phase 0–6 Status"` to the Subsystem Architecture table.
4. **Distinguished Historical Baselines from Live Telemetry:**
   - Added an explicit header and badge to `BenchmarksPage.tsx`: `"Documented Reference Baselines (Phase 5 Performance Testing)"` with badge `"Historical Benchmark Data"`.
5. **Rebuilt & Redeployed Frontend Docker Container:**
   - Rebuilt `lockguard-frontend` via `docker compose build --pull=false frontend` and recreated container. Verified live bundle serving via HTTP 200.

---

## 5. Commands Actually Executed

| # | Command Line | Working Directory | Observed Exit Code |
| :--- | :--- | :--- | :--- |
| 1 | `docker compose config --quiet` | Project Root | **0** |
| 2 | `docker compose ps` | Project Root | **0** |
| 3 | `go test -count=1 ./...` | `backend/` | **0** |
| 4 | `go test -count=5 ./internal/consensus/...` | `backend/` | **0** |
| 5 | `go vet ./...` | `backend/` | **0** |
| 6 | `go test -count=1 ./...` | `protected-resource/` | **0** |
| 7 | `go test -race ./internal/lockstate/...` | `backend/` | **1** *(Limitation: cgo requires gcc)* |
| 8 | `npm run build` | `frontend/` | **0** *(after fix)* |
| 9 | `docker compose build --pull=false frontend` | Project Root | **0** |
| 10 | `docker compose up -d --no-deps frontend` | Project Root | **0** |
| 11 | `curl.exe -i http://localhost/api/v1/health` | Host | **0** |
| 12 | `curl.exe -i http://localhost/api/v1/recovery/cluster` | Host | **0** |
| 13 | `curl.exe -i http://localhost/api/v1/cluster/status` | Host | **0** |
| 14 | Automated Integration & Security Probe (PowerShell) | Host | **0** |

---

## 6. Test Results and Verification Classifications

### 6.1 Backend Test Suites
- **Full Backend Regression (`go test -count=1 ./...`):**
  - Result: **VERIFIED** (Exit Code 0).
  - All 15 backend packages passed, including `consensus` (3.00s), `audit` (1.70s), `auth` (1.60s), `persistence` (1.53s), `renewal` (1.39s), and full integration tests in `tests/` (16.89s).
- **Consensus Stress Test (`go test -count=5 ./internal/consensus/...`):**
  - Result: **VERIFIED** (Exit Code 0, 9.55s).
  - Executed 5 consecutive runs through Raft leader election, command replication, and FSM commit loops without failure.
- **Backend Static Analysis (`go vet ./...`):**
  - Result: **VERIFIED** (Exit Code 0, zero warnings).
- **Protected Resource Suite (`go test -count=1 ./...`):**
  - Result: **VERIFIED** (Exit Code 0, 0.63s).
  - All 8 tests passed: `TestRepeatedSameLeaseWrites`, `TestRejectLowerTokens`, `TestAcceptNewerTokens`, `TestRejectDifferentOwnerOnSameToken`, `TestStaleOwnerAfterNewerTokenAccepted`, `TestInputValidation`, `TestMultipleIndependentResources`, `TestConcurrentRequests`.

### 6.2 Race Detector Investigation
- **Execution:** `$env:CGO_ENABLED = "1"; go test -race ./internal/lockstate/...`
- **Output:**
  ```text
  # runtime/cgo
  cgo: C compiler "gcc" not found: exec: "gcc": executable file not found in %PATH%
  FAIL    lockguard/backend/internal/lockstate [build failed]
  ```
- **Classification:** **PARTIALLY VERIFIED (ENVIRONMENT LIMITATION)**
- **Finding:** Go's race detector on Windows requires MinGW/GCC to compile the C runtime instrumentation. The Windows host environment has Go 1.22 and Node.js 20 installed in `D:\DE-Tools`, but lacks `gcc.exe`. Thread safety and mutex invariants are verified via code inspection and unit tests, but full `-race` suite execution requires a Linux container or MinGW toolchain.

### 6.3 Frontend Production Build
- **Execution:** `npm run build` (`tsc && vite build`)
- **Output:**
  ```text
  ✓ 1678 modules transformed.
  dist/index.html                   0.88 kB │ gzip:   0.50 kB
  dist/assets/index-Dkr2XrbG.css   36.00 kB │ gzip:   6.34 kB
  dist/assets/index-r7EHgZP-.js   367.81 kB │ gzip: 104.73 kB
  ✓ built in 9.03s
  ```
- **Result:** **VERIFIED** (Exit Code 0).

### 6.4 Docker Container Fleet
- **Execution:** `docker compose ps`
- **Output:**
  ```text
  NAME                           SERVICE              STATUS                    PORTS
  lockguard-api                  api                  Up 52 minutes (healthy)   0.0.0.0:8082->8080/tcp
  lockguard-coordinator-a        coordinator-a        Up 53 minutes (healthy)   7000/tcp, 8001/tcp
  lockguard-coordinator-b        coordinator-b        Up 53 minutes (healthy)   7000/tcp, 8001/tcp
  lockguard-coordinator-c        coordinator-c        Up 52 minutes (healthy)   7000/tcp, 8001/tcp
  lockguard-frontend             frontend             Up 34 seconds (healthy)   80/tcp
  lockguard-nginx                nginx                Up 2 hours (healthy)      0.0.0.0:80->80/tcp
  lockguard-postgres             postgres             Up 2 hours (healthy)      127.0.0.1:5435->5432/tcp
  lockguard-prometheus           prometheus           Up 2 hours (healthy)      0.0.0.0:9090->9090/tcp
  lockguard-protected-resource   protected-resource   Up 2 hours (healthy)      0.0.0.0:9000->9000/tcp
  ```
- **Result:** **VERIFIED** (9 of 9 containers running and healthy).

---

## 7. Runtime Verification Evidence

### 7.1 Reverse Proxy & Path Rewriting Consistency
- Request to `http://localhost/api/v1/health` via Nginx (port 80):
  - HTTP Status: `200 OK`
  - Body: `{"success":true,"data":{"status":"ok","service":"lockguard-api","version":"0.1.0","node_id":"coordinator-a",...}}`
- Request to `http://localhost:8082/health` direct to Go API:
  - HTTP Status: `200 OK`
- Verification: **VERIFIED**. Nginx stripping of `/api/` matches backend registration of both `/v1/health` and `/api/v1/health`.

### 7.2 Authentication & RBAC Enforcement
- Invalid credentials: `POST /api/v1/auth/login` with `admin` / `wrongpassword`:
  - Returned HTTP Status: `401 Unauthorized` (`ERR_UNAUTHORIZED`).
- Valid logins: Successfully authenticated `admin` (Role: `ADMIN`), `client_worker` (Role: `CLIENT`, Owner: `worker-1`), `client_worker_2` (Role: `CLIENT`, Owner: `worker-2`), and `auditor` (Role: `READ_ONLY`).
- Administrative endpoint protection:
  - Non-admin client `client_worker` calling `POST /api/v1/admin/cluster/stepdown` -> **403 Forbidden** (`ERR_FORBIDDEN: forbidden: insufficient role`).
  - Non-admin client `client_worker` calling `POST /api/v1/admin/locks/emergency-unlock` -> **403 Forbidden** (`ERR_FORBIDDEN: forbidden: insufficient role`).
  - Auditor `auditor` calling `POST /api/v1/admin/cluster/stepdown` -> **403 Forbidden** (`ERR_FORBIDDEN`).
- Verification: **VERIFIED**.

### 7.3 Distributed Lock Invariants & Fencing Protection
1. **Exclusive Lock Acquisition:**
   - `client_worker` acquired resource `audit-fencing-test`: Granted fencing token `1` (`state: HELD`).
2. **Conflicting Acquisition Rejection:**
   - `client_worker_2` attempted acquire on same resource: Returned **409 Conflict** (`ERR_LOCK_CONFLICT`).
3. **Cross-Client Release Rejection:**
   - `client_worker_2` attempted to release lock held by `worker-1`: Returned **403 Forbidden** (`ERR_OWNER_MISMATCH`).
4. **Downstream Protected Resource Fencing:**
   - `worker-1` sent write request to `http://localhost:9000/api/v1/write` with token `1`: Accepted with HTTP 200 (`success: true`).
   - Request with stale token `0`: Rejected with **400 Bad Request** (`ERR_FENCING_TOKEN_STALE`).
5. **Admin Emergency Break:**
   - `admin` called `POST /api/v1/admin/locks/emergency-unlock` for `audit-fencing-test`: Returned HTTP 200 (`message: lock released successfully`).
6. **Token Monotonicity:**
   - `client_worker_2` acquired now-unlocked resource: Granted fencing token `2` (strictly greater than `1`).
   - `client_worker_2` wrote to protected resource with token `2`: Accepted with HTTP 200.
7. **Zombie Write Invalidation:**
   - Stale leaseholder `worker-1` attempted to write to protected resource using old token `1`: Rejected with **409 Conflict** (`ERR_FENCING_TOKEN_STALE: received 1, highest accepted is 2`).
- Verification: **VERIFIED**.

### 7.4 Raft Leadership Transfer (Admin Stepdown)
- Evaluated runtime leadership transfer via `POST /api/v1/admin/cluster/stepdown`:
  - Active Leader prior to stepdown: `coordinator-b`
  - Stepdown API call: Succeeded (`200 OK`).
  - Active Leader after election: `coordinator-a`
  - Quorum state throughout transition: `ESTABLISHED` (3/3 nodes active).
- Verification: **VERIFIED**.

### 7.5 Multi-Tenant Audit Trail Isolation
- Authenticated `client_worker` (`worker-1`) queried `/api/v1/audit/events`: Returned only records where `owner_id = 'worker-1'` or `client_id = 'worker-1'`.
- Authenticated `client_worker_2` (`worker-2`) queried `/api/v1/audit/events`: Returned only records for `worker-2`.
- Tamper attempt: `worker-2` querying `GET /api/v1/audit/events?client_id=worker-1`: Returned **403 Forbidden** (`ERR_FORBIDDEN: access denied: client cannot view other clients audit records`).
- Authenticated `admin` queried `/api/v1/audit/events`: Returned complete global audit log across all tenants.
- Verification: **VERIFIED**.

---

## 8. Security and Tenant-Isolation Findings

1. **Owner ID Binding:** JWT tokens bind the authenticated `sub` (subject) claim to the client's legitimate `owner_id`. Handlers in `coordinator` and `audit` enforce this claim, preventing client impersonation.
2. **Secret Masking:** The Settings page (`SettingsPage.tsx`) strictly masks database credentials, internal Raft node keys, and JWT HMAC keys (`•••••••••••••••• (Masked)`).
3. **Demo Credentials Risk Assessment:**
   - The backend pre-seeds 4 standard accounts (`admin`, `client_worker`, `client_worker_2`, `auditor`) with hardcoded default passwords.
   - The login page provides quick-fill demo buttons clearly labeled `"Academic Evaluation Demo Accounts"`.
   - **Risk:** In an untrusted deployment, these credentials would allow unauthorized administrative access if deployed without modification.
   - **Protection:** In `backend/internal/config/config.go`, setting `ENVIRONMENT=production` strictly enforces that default keys, weak secrets, and non-SSL database modes are rejected at startup. However, the in-memory `seedDefaultAccounts()` in `service.go` runs unconditionally upon service initialization.

---

## 9. Remaining Limitations

1. **Windows GCC Toolchain Constraint for Race Detection:**
   - The Windows host environment lacks `gcc.exe`. As a result, running `go test -race` locally fails with `cgo: C compiler "gcc" not found`. Full race detection must be executed in a Linux-based Docker build container or CI environment with MinGW/GCC installed.
2. **PostgreSQL Audit Best-Effort Persistence:**
   - Audit event persistence is intentionally asynchronous and non-blocking, using an in-memory queue (capacity: 50,000 events) and batched flushes. If PostgreSQL experiences a persistent outage exceeding max retry timeouts (3 attempts with exponential backoff), audit records are dropped to prevent Raft lock consensus from blocking. This behavior is by design, but must be acknowledged in audit compliance documentation.
3. **Pre-Seeded In-Memory User Accounts:**
   - The current authentication service seeds demo accounts into an in-memory map. Production deployment requires disabling the demo seed when `ENVIRONMENT=production` and backing user storage with PostgreSQL.

---

## 10. Audit Classifications Summary

| Requirement / Component | Classification | Evidence Basis |
| :--- | :--- | :--- |
| **Nginx Routing & Path Rewriting** | **VERIFIED** | Live HTTP 200 on `/api/v1/health` and direct 8082. |
| **API Availability & Liveness Probes** | **VERIFIED** | Dynamic status in Header; live responses from `/health` and `/ready`. |
| **Role-Based Access Control (RBAC)** | **VERIFIED** | 403 Forbidden on `/admin/*` for non-admin accounts. |
| **Distributed Lock Mutual Exclusion** | **VERIFIED** | 409 Conflict on concurrent acquire across different clients. |
| **Owner Isolation & Tamper Resistance** | **VERIFIED** | 403 Forbidden `ERR_OWNER_MISMATCH` on cross-client release. |
| **Monotonic Fencing Tokens** | **VERIFIED** | Tokens advance monotonically (1 -> 2); stale tokens rejected. |
| **Downstream Fencing Token Guard** | **VERIFIED** | `protected-resource` accepts current token, rejects zombie writes. |
| **Admin Emergency Unlock** | **VERIFIED** | Safely breaks locks on Raft state machine; new token issued. |
| **Raft Leader Stepdown** | **VERIFIED** | Leadership transferred (`coordinator-b` -> `coordinator-a`), quorum maintained. |
| **Audit Multi-Tenant Isolation** | **VERIFIED** | Operators see own events; cross-tenant query returns 403 Forbidden. |
| **PostgreSQL Outage Decoupling** | **VERIFIED** | Code inspection of `pipeline.go`; verified bounded non-blocking ring buffer. |
| **Frontend Accuracy & Label Purge** | **VERIFIED** | Obsolete labels removed; dynamic fallback values; clean `npm run build`. |
| **Secret Masking on Settings** | **VERIFIED** | Verified masked display of secrets and token keys. |
| **Go Race Detector Execution** | **PARTIALLY VERIFIED** | Blocked by missing GCC on Windows host; documented limitation. |

---

## 11. Final Recommendation

Based on the evidence gathered from source inspection, full test suite passes, and direct runtime verification against the running Docker environment:

### **Recommendation: APPROVED WITH LIMITATIONS**

**Approved Attributes:**
- The distributed lock service, consensus coordinators, API gateway, reverse proxy, and web dashboards are fully operational, stable, and functionally integrated.
- All core distributed consistency, mutual exclusion, owner isolation, fencing token, and multi-tenant audit requirements are fully satisfied with zero regressions.
- All 15 backend packages pass `go test -count=1 ./...` and `go vet ./...` with exit code 0.
- All 9 Docker containers are healthy and communicating cleanly.

**Documented Limitations:**
1. Race detector verification (`go test -race`) requires a Linux container or MinGW/GCC toolchain not present on this Windows host.
2. Pre-seeded demo credentials in `service.go` should be conditioned on non-production environments when deployed outside academic demonstration.

