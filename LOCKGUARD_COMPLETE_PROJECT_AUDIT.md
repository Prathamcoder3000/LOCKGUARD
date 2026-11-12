# LOCKGUARD — Complete End-to-End Functional, Logical & Technical Audit Report

**Project:** LOCKGUARD — Fault Tolerant Distributed Lock Service  
**Workspace:** `D:\5TH SEM\SD\PROJECT\LOCKGUARD`  
**Audit Scope:** Full-stack verification across Go backend (`api`, `coordinator-a/b/c`, `protected-resource`), HashiCorp Raft + BoltDB consensus/state storage, PostgreSQL persistence, Prometheus metrics, Nginx gateway, and React/TypeScript dashboard UI.  
**Status Tags Used:** `PASS`, `FAIL`, `PARTIAL`, `NOT TESTED`, `MOCKED`

---

## 1. Executive Summary & Overall Verdict

| Domain | Status | Observed Evidence Summary |
| :--- | :---: | :--- |
| **Build, Vet & Automated Test Suites** | **PASS** | `backend` (`go vet` + `go test -race -count=1`): 16/16 test packages passed; `protected-resource`: 1/1 test package passed (`-race`); `frontend`: `tsc && vite build` transformed 1,678 modules with exit `0`. (Entrypoint/stub packages have `[no test files]` and are exercised via live containers.) |
| **Live Deployment & Health Checks** | **PASS** | All 9 Docker Compose containers running and `(healthy)`; Prometheus scraping `5/5` targets (`up`). Clean post-test runtime state (`locks: []`, `renewals: []`). |
| **Authentication, JWT & RBAC Enforcement** | **PASS** | Unauthenticated access blocked (`401`) on all 12 checked protected routes; audit static token / `X-Client-ID` bypasses fixed (`401`); `READ_ONLY` blocked (`403`) from mutations/admin; `CLIENT` owner isolation enforced (`403`). |
| **Distributed Lock, Fencing, TTL & Auto-Renew** | **PASS** | Mutual exclusion (`409`), idempotent replay (`200`), idempotency conflict (`409`), manual renew (`v1 -> v2`), wrong token (`412`), wrong owner (`403`), TTL min/max validation (`400`), fencing rejection on `protected-resource` (`409`), monotonic tokens after expiry (`1 -> 2`), session redaction (`[REDACTED]`), heartbeat, background auto-renew (`v2 -> v3`, token `1` preserved), and admin unlock (`200`) all verified live. |
| **Raft Consensus, Failover, Quorum Loss & Persistence** | **PASS** | Graceful stepdown (`coordinator-a -> coordinator-b`), single follower loss (`QUORUM` maintained, writes `200`), leader crash auto-reelection (`coordinator-c` in `4.19s`, lock survived), 2-node quorum loss (`QUORUM_LOST` in ~`0.8s`, reads/writes fail closed `503 ERR_QUORUM_UNAVAILABLE`), 3-node full container restart BoltDB durability + monotonic re-acquire (`1 -> 2`), and PostgreSQL outage tolerance (locks `200` during PG outage; `/ready` `503` -> recovers to `200`; `outage_events_persisted=2`, `failed_total=0`). |
| **Frontend End-to-End UI & RBAC Workflows** | **PARTIAL** | All routes (`/`, `/locks`, `/cluster`, `/consensus`, `/resilience`, `/audit`, `/benchmarks`, `/settings`, `/login`, `/404`), Header Notification Bell, Logout, and full `/locks` workflow pass cleanly with zero console errors during healthy runs. Rated `PARTIAL` due to transient `UNKNOWN`/`Probing...` display on `/resilience` at a `1.5s` check under node/quorum outage (plus expected browser `503` console logs when probes return `503`). |
| **Protected Resource & Persistence Feature Completeness** | **PARTIAL** | `protected-resource` fencing API (`200`/`409`) and `/metrics` are verified `PASS`, but no dedicated UI panel triggers `/api/v1/write` directly. PostgreSQL `audit_events` actively persists events, whereas `users` (in-memory demo accounts in `auth.Service`), `benchmark_runs`, `operational_events`, and `system_checkpoints` remain at `0` rows, and `BenchmarksPage` mixes live latency probes with static documented Phase 5 baseline numbers (`MOCKED` baseline display). |

**Overall Production-Prototype Readiness Verdict:** **PASS (Core Distributed Lock, Consensus, Security & Resilience Engine) / PARTIAL (Peripheral DB Tables, `/resilience` Outage Probe UX & Static Benchmark Baselines)**  
All critical correctness, security, RBAC, session-redaction, health-check, quorum-timeout, and UI bugs uncovered during the audit were **patched, regression-tested, rebuilt into the running Docker images, and re-verified against the live 9-container stack**. No open `FAIL` defects remain.

---

## 2. Architecture & Subsystem Verification Matrix

| Subsystem | Key Verified Components / Symbols | Status | Observed Verification Result |
| :--- | :--- | :---: | :--- |
| **API Gateway & Routing** | `backend/internal/api/server.go`, `backend/internal/api/health.go` (`ReadinessHandlerWithDB`, `DatabaseChecker`), `backend/internal/gateway/discovery.go` (`Discover`) | **PASS** | `/health` (`200`), `/ready` dynamically checks `db.Ping(ctx)` (`200 database: ready` when UP; `503 database: unavailable: ...` when PG stopped), concurrent leader discovery bounded by `1000ms` (`probeTimeout`). |
| **Raft Coordinators & Consensus** | `coordinator-a`, `coordinator-b`, `coordinator-c` (`8001` HTTP / host `8001,8002,8003`, Raft port `7000`), `backend/cmd/coordinator/main.go`, `backend/internal/coordinator/handler.go` | **PASS** | Quorum `3/3` healthy, graceful stepdown (`200`), follower loss tolerance (`2/3` votes, Term `21`, Applied `#563`), leader crash reelection in `4.19s`, quorum loss fail-closed (`503 ERR_QUORUM_UNAVAILABLE`). `/metrics` scraped by Prometheus (`up`). |
| **Deterministic Lock State & BoltDB** | `backend/internal/lockstate/{acquire.go,commands.go}` | **PASS** | Linearizable `GET`, fencing token monotonicity (`1 -> 2`), version increment (`v1 -> v2 -> v3`), idempotent replay & conflict detection (`409`), mutual exclusion (`409`), full 3-node coordinator restart BoltDB persistence verified live. |
| **Heartbeat & Auto-Renewal** | `backend/internal/heartbeat/handler.go`, `backend/internal/renewal/{handler.go,manager.go}` (`sanitizeRenewalStatus`) | **PASS** | Session registration (32-char `session_id`), `session_id` redacted to `"[REDACTED]"` on list/status unless `X-Session-ID` matches, heartbeat (`200`), background auto-renewal worker advances lease version (`ver_after=3`, `tok=1`), clean stop & session delete (`200`). |
| **Protected Resource Service** | `protected-resource/cmd/main.go`, `POST /api/v1/write`, `GET /health`, `GET /metrics` | **PASS** | Enforces fencing tokens (`tok 101` accept `200`, same owner `200`, different owner same token `409`, higher `tok 102` accept `200`, stale `tok 101` reject `409`). `/metrics` exposes `lockguard_protected_resource*` counters (`631B`, `200`). |
| **Auth, JWT & RBAC Middleware** | `backend/internal/auth/{service.go,middleware.go}`, `protectClientAndRenewal`, `validateLeaseTTL` | **PASS** | Roles `ADMIN`, `CLIENT`, `READ_ONLY` enforced across lock, client, renewal, audit, and admin APIs. No `/v1/auth/me` endpoint (`PARTIAL` if expected; login uses `/v1/auth/login`). |
| **Audit Pipeline & PostgreSQL** | `backend/internal/audit/{handler.go,pipeline.go}`, `backend/internal/persistence/db.go`, `lockguard-postgres` | **PASS** (Audit) / **PARTIAL** (Extra Tables) | `db_attached=True`; `audit_events` persisted `33` rows cleanly and queued/flushed during PG outage (`persisted_total: 45->47` / `25->27`, `failed_total: 0`, `outage_events_persisted=2`). `operational_events`, `system_checkpoints`, `users`, `benchmark_runs` have `0` rows. |
| **Recovery Diagnostics & Prometheus** | `backend/internal/recovery/diagnostics.go` (`CollectClusterReport`), `/api/v1/metrics`, Prometheus `/api/v1/targets` | **PASS** | Concurrent coordinator probing (`800ms`), clearing leader on quorum loss, Prometheus scraping `5/5` targets (`lockguard-api`, 3x `lockguard-coordinators`, `lockguard-protected-resource`) all `up`. |
| **Nginx & React/TS Dashboard** | `nginx /health`, `nginx /gateway/health`, `frontend/src/*` | **PARTIAL** | Healthy-cluster browsing, auth login/logout, RBAC gating, and full `/locks` lifecycle pass with `0` console/HTTP errors. `/resilience` shows transient `UNKNOWN`/`Probing...` at `1.5s` under degraded quorum. |

---

## 3. Root-Cause Bugs Discovered, Fixed & Re-verified

Every defect found during code inspection and dynamic testing was patched in source, covered by unit/integration tests (`-race`), rebuilt into the container images, and re-verified against the live stack.

| # | Initial Audit Status | File(s) & Symbol(s) Modified | Root Cause Discovered | Fix Applied | Post-Fix Verification (`PASS`) |
| :---: | :---: | :--- | :--- | :--- | :--- |
| **1** | **FAIL** | `backend/cmd/coordinator/main.go` | Coordinator binary did not expose `/metrics`, leaving `lockguard-coordinators` Prometheus targets down. | Registered `/metrics` endpoint backed by `metrics.NewRegistry()`. | Live Prometheus `/api/v1/targets` shows `coordinator-{a,b,c}:8001` = `up` (`5/5` targets healthy). |
| **2** | **FAIL** | `protected-resource/cmd/main.go` | Protected resource service lacked `/metrics` endpoint for Prometheus scraping. | Added `/metrics` handler exposing `lockguard_protected_resource*` counters. | `GET http://localhost:9000/metrics` returns HTTP `200` (`631` bytes); Prometheus target `lockguard-protected-resource` = `up`. |
| **3** | **FAIL** | `backend/internal/renewal/handler.go` (`handleStatus`, `sanitizeRenewalStatus`) | `GET /renewal/status` exposed live `RenewalStatus.SessionID` secrets to list callers. | Added `sanitizeRenewalStatus` to mask `SessionID` as `"[REDACTED]"` unless caller supplies matching `X-Session-ID`. | `TestRenewalHandler_StatusRedactsSessionID` (`go test -race`) + live `GET /renewal/status` (`200`, `redacted=True`). |
| **4** | **FAIL** | `backend/internal/audit/handler.go` | Accepted static tokens (`"admin"`, `"operator"`, `"client:..."`) outside test mode and allowed arbitrary `X-Client-ID` Bearer bypass. | Removed `X-Client-ID` Bearer bypass and gated static tokens behind `isTestEnv := h.cfg == nil \|\| h.cfg.Environment == "" \|\| h.cfg.Environment == "test"`. | `TestServerSecurity_DevelopmentModeEnforcesAuthRBACAndTTL` + live `Bearer admin` -> `401` and `Bearer bogus` + `X-Client-ID` -> `401`. |
| **5** | **FAIL** | `backend/internal/api/health.go` & `backend/internal/api/server.go` (`ReadinessHandlerWithDB`, `DatabaseChecker`) | `/ready` reported hardcoded `"standby_phase0"` instead of checking real PostgreSQL connectivity. | Implemented `db.Ping(ctx)` check (`1s` timeout) with untyped-nil `dbChecker` guard in `server.go`. | `TestReadinessHandlerWithDB` + live `/ready` (`200 database: ready` when UP; `503 database: unavailable: database unavailable: driver: bad connection` when PG stopped). |
| **6** | **FAIL** | `backend/internal/api/server.go` (`handleLockOperation`, `protectClientAndRenewal`, `validateLeaseTTL`) | Partial/bypassable auth when dev headers/config varied; missing RBAC & owner isolation on `/v1/clients*` & `/v1/renewal*`; missing gateway lease TTL bounds validation before coordinator proxy. | Changed auth guard to `if authHeader != "" \|\| s.cfg.Auth.Enabled`; added `protectClientAndRenewal` (JWT + `RoleReadOnly` block + `RoleClient` session/renewal `OwnerID` enforcement across `/v1/*` and `/api/v1/*`); added `validateLeaseTTL` (`MinTTLSec`/`MaxTTLSec`). | `TestServerSecurity_DevelopmentModeEnforcesAuthRBACAndTTL` + live `audit_api_live.py` (`53/53 PASS`), including `ttl_seconds: 99999` -> `400 ERR_BAD_REQUEST` (`exceeds maximum allowed 300s`). |
| **7** | **FAIL** | `backend/internal/gateway/discovery.go` (`Discover`), `backend/internal/recovery/diagnostics.go` (`CollectClusterReport`) | Sequential coordinator probing blocked ~`5s` per dead node during outage; `CollectClusterReport` could retain stale leader info or nil slices on quorum loss. | Switched `Discover` to concurrent probing bounded by `1000ms` (`probeTimeout`) with early cancellation via `leaderSignaled.CompareAndSwap(false, true)`; updated `CollectClusterReport` to probe concurrently (`800ms`), compute fallback `nodeIDFromEndpoint`, skip `GetLeader` below majority, clear leader on quorum loss, and update `metrics.DefaultRegistry`. | `TestCollectClusterReport_UnreachableNodesAndQuorumLoss` (`go test -race`) + live quorum-loss chaos (`QUORUM_LOST` returned in ~`0.8s`; lock reads/writes failed closed `503`). |
| **8** | **FAIL** | `frontend/src/services/{clusterService.ts,resilienceService.ts}`, `frontend/src/pages/{LocksPage,SettingsPage,ClusterPage,ResiliencePage,OverviewPage,BenchmarksPage}.tsx`, `frontend/src/components/{Header,ProtectedRoute}.tsx`, `frontend/src/components/resilience/{NodeStatus,ResilienceMetrics,FailureHistory}.tsx` | Heartbeat/Auto-Renew lacked stored `session_id`; `READ_ONLY` saw mutation buttons; stale telemetry lingered on quorum loss; `ResilienceMetrics` & `FailureHistory` displayed fake/fallback data; Header Bell was unwired; Overview ports & Benchmarks ring buffer label were inaccurate; `ProtectedRoute` role check was case-sensitive. | Persisted `session_id` in `localStorage['lockguard_client_sessions']`; added `isReadOnly` RBAC UI guards; cleared stale state & unwrapped `503` `SystemHealthReport` payloads; wired live `/api/v1/metrics` + `/recovery/cluster` metrics & removed fake Phase 4 fallback events; wired Notification Bell popover (`Cluster Alerts`); updated ports (`8001 (Host 8001/8002/8003)`, Raft `7000`) & capacity text (`10,000`); case-insensitive `'ADMIN'` guard. | `npm run build` (`tsc && vite build`, `0` errors) + live Selenium checks across `auditor`, `client_worker`, `admin` (`ALL UI CHECKS PASSED CLEANLY!`). |

---

## 4. End-to-End Frontend Page & Interactive Control Matrix

Verified live against the rebuilt `frontend` container via Chromium Selenium across `auditor` (`READ_ONLY`), `client_worker` (`CLIENT`), and `admin` (`ADMIN`).

| Route / UI Area | Controls & Data Elements Verified | Role Behavior (`ADMIN` / `CLIENT` / `READ_ONLY`) | Status | Observed Live Result |
| :--- | :--- | :--- | :---: | :--- |
| **`/login`** | Username/password authentication (`POST /api/v1/auth/login`) | Authenticates all roles (`admin`, `client_worker`, `auditor`) | **PASS** | Valid credentials store session & redirect to dashboard; logout via `button[title="Log out"]` clears session cleanly. |
| **`/` (Cluster Overview)** | Live cluster cards, coordinator node ports, Raft port | Visible to authenticated users | **PASS** | Renders live backend cluster state; displays corrected ports `8001 (Host 8001)`/`8002`/`8003` and Raft port `7000`. Zero console errors. |
| **`/locks` (Distributed Locks)** | `Register Session`, `Acquire Lock (5s)`, `Ping Heartbeat`, `Auto-Renew` / `Stop`, `Unlock` (`ADMIN EMERGENCY UNLOCK`) | `ADMIN` & `CLIENT`: session/lock/heartbeat/renewal controls (`ADMIN` has Emergency Unlock); `READ_ONLY`: mutation buttons hidden | **PASS** | Live Selenium flow executed end-to-end: `Register Session` -> `Acquire Lock (5s)` -> `Ping Heartbeat` (0 error alerts) -> `Auto-Renew` (observed `v2`, `success`, `Stop` button) -> `Stop` -> `Unlock` (`ADMIN EMERGENCY UNLOCK`). `auditor` verified without `Acquire Lock`, `Register Session`, `Ping Heartbeat`, `Stop`, or `Emergency Unlock`. |
| **`/cluster` (Coordinator Nodes)** | Leader identity, quorum state, voting count, Raft term & applied index | Live updates during healthy & degraded states | **PASS** | Healthy `3/3` renders accurately; during 1-node outage test, displayed `coordinator-b`, `ESTABLISHED`, `2 of 3 nodes voting`, Term `21`, Applied `#563`; clears stale data on full error. |
| **`/consensus` (Consensus & Raft)** | Raft state, leader/term visualization | Live read-only consensus inspection | **PASS** | Renders cleanly with zero HTTP `>=400` or JS errors during healthy cluster run. |
| **`/resilience` (Resilience & Recovery)** | `NodeStatus`, `ClusterHealth`, `ResilienceMetrics`, `FailureHistory` | Fetches `/api/v1/metrics`, `/recovery/cluster`, `/recovery/diagnostics` | **PARTIAL** | Fake Phase 4 fallback events removed and live metrics wired cleanly in healthy state; however, at a fixed `1.5s` check during live node failure / quorum loss, UI still showed transient `UNKNOWN`/`Probing...` while uncached serialized `LeaderDiscovery` probes finished, and browser DevTools logged expected `503` responses from `/api/v1/ready` and `/api/v1/recovery/diagnostics`. |
| **`/audit` (Audit & Events)** | Event table, owner filters, `Inspect` detail modal | Owner-scoped for `CLIENT`; cluster-wide for `ADMIN`/`READ_ONLY` (per API policy) | **PASS** | Renders persisted PostgreSQL events cleanly; `Inspect` modal opens and closes with zero console errors. |
| **`/benchmarks` (Benchmarks & Stress)** | `Run Live Latency Probe` button, documented baseline reference metrics (`10,000` ring buffer capacity) | Interactive live probe + static baseline cards | **PARTIAL** (`MOCKED` baselines) | `Run Live Latency Probe` genuinely executes 5 live `200` `GET /api/v1/cluster/status` requests; capacity label fixed to `10,000`. Baseline comparison figures remain static documented Phase 5 values and are not stored in PostgreSQL `benchmark_runs`. |
| **`/settings` (System Settings & Admin)** | Admin controls vs role restriction badge | `ADMIN`: full panel; `CLIENT` & `READ_ONLY`: `Restricted` badge without admin controls | **PASS** | Verified `/settings` badge = `Restricted` with no admin controls for both `auditor` and `client_worker`; full view for `admin`. |
| **Header & Error Routes** | Notification Bell (`aria-label="System Notifications"` -> `Cluster Alerts`), Logout button, `/nonexistent-page` | Global navigation shell | **PASS** | Clicking Bell opens `Cluster Alerts (0)` popover with live status; Logout works across roles; `/nonexistent-page` renders `404 — View Not Found`. |
| **Protected Resource UI** | Interactive client panel to write directly to `protected-resource:9000/api/v1/write` | N/A | **PARTIAL** | Backend service verified thoroughly via HTTP/Go tests, but no dedicated dashboard UI panel exists to exercise `/api/v1/write`. |

---

## 5. API, Security, & Distributed Locking Verification Matrix (`53/53 PASS`)

Verified live via `audit_api_live.py` against the updated Docker containers (**`SUMMARY: PASS=53, FAIL=0`**):

| Category | Executed Checks & Exact Observed Responses | Status |
| :--- | :--- | :---: |
| **Health, Readiness & Observability** | `nginx /health` (`200`), `nginx /gateway/health` (`200`), `api /health` (`200`), `api /ready` (`200`, `config: ready`, `coordinator_cluster: ready`, `database: ready`, `leader: http://coordinator-a:8001`), `protected-resource /health` (`200`), `/recovery/{cluster,diagnostics,snapshots,health}` (`200`), `/api/v1/metrics` (`200`, `5749B`), `protected-resource:9000/metrics` (`200`, `631B`), Prometheus `/api/v1/targets` (**`5/5 up`**). | **PASS** |
| **Authentication & Token Integrity** | Login `200` for `admin` (`ADMIN`, `admin-system`), `client_worker` (`CLIENT`, `worker-1`), `client_worker_2` (`CLIENT`, `worker-2`), `auditor` (`READ_ONLY`, `auditor-01`). Invalid password -> `401 ERR_UNAUTHORIZED`. Tampered JWT -> `401`. Static `Bearer admin` -> `401`. Arbitrary bearer + `X-Client-ID` -> `401`. | **PASS** |
| **Unauthenticated Blocking (12 Routes)** | Rejected with `401` on all 12 tested endpoints: `GET /locks`, `POST /locks/acquire`, `POST /locks/release`, `GET /clients`, `POST /clients/register`, `POST /clients/heartbeat`, `GET /renewal/status`, `POST /renewal/start`, `POST /renewal/stop`, `POST /admin/cluster/stepdown`, `POST /admin/locks/emergency-unlock`, `GET /audit/events`. | **PASS** |
| **RBAC & Tenant Isolation** | `READ_ONLY` allowed `GET /locks` (`200`) and denied (`403`) on `acquire`, `admin emergency-unlock`, `clients/register`, `renewal/start`. `CLIENT` (`worker-1`) denied (`403`) on acquiring for `worker-2`, `admin emergency-unlock`, `audit/events?owner_id=worker-2`, `audit/clients/worker-2`, `clients/register` for `worker-2`, and `worker-2` calling `heartbeat` on `worker-1`'s session (`403`). | **PASS** |
| **Lock Lifecycle, Validation & Idempotency** | Acquire (`200`, `tok=1, ver=1`), linearizable `GET` (`200`, `owner=worker-1`), idempotent replay with same `request_id` (`200`), idempotency conflict with same `request_id` & different resource (`409`), mutual exclusion by `worker-2` (`409`). Validation: empty resource (`400`), `ttl_seconds: 99999` (`400 ERR_BAD_REQUEST`), `ttl_seconds: -5` (`400`). | **PASS** |
| **Renewal, Fencing & Protected Resource** | Manual renew (`200`, `ver=2`, extended `8.19s`), wrong fencing token (`412`), wrong owner (`403`). Protected resource `/api/v1/write`: `tok 101` first owner (`200`), same owner (`200`), different owner same `tok 101` (`409`), higher `tok 102` (`200`), stale `tok 101` (`409`). | **PASS** |
| **Sessions, Redaction & Auto-Renew** | Register client returns 32-char `session_id`; `GET /clients` & `GET /renewal/status` redact `session_id` (`"[REDACTED]"`); valid `POST /clients/heartbeat` (`200`); `POST /renewal/start` (`200`) -> auto-renewed to `ver_after=3` keeping `tok=1`; `POST /renewal/stop` (`200`); release (`200`); admin emergency unlock (`200`, follow-up `GET` -> `404`); TTL expiry + re-acquire monotonic token (`tok1=1 -> tok2=2`); `DELETE /clients/{id}` with `X-Session-ID` (`200`). | **PASS** |
| **Audit Persistence** | `db_attached=True`; API audit query returned committed lock events; direct `psql` confirmed rows written in `audit_events`. | **PASS** |

---

## 6. Live Fault-Tolerance, Raft Consensus & Chaos Verification (`chaos_live_verify.py` — Exit `0`)

All 8 live failure-injection scenarios executed against the running 9-container cluster:

| # | Scenario | Method / Trigger | Status | Observed Cluster, API & Durability Behavior |
| :---: | :--- | :--- | :---: | :--- |
| **1** | **Graceful Leader Stepdown** | `POST /admin/cluster/stepdown` | **PASS** | Returned `200`; leadership transferred cleanly (`coordinator-a -> coordinator-b`). |
| **2** | **Single Follower Node Loss** | Stopped `coordinator-a` | **PASS** | `/recovery/cluster` returned `200` (`available_nodes: ["coordinator-b","coordinator-c"]`, `quorum_state: "ESTABLISHED"`, `coordinator-a reachable: false`). Lock writes succeeded (`acquire=200`, `renew=200`, `release=200`). `/cluster` UI showed `coordinator-b`, `ESTABLISHED`, `2 of 3 nodes voting`, Term `21`, Applied `#563`. *(Note: `/resilience` UI at `1.5s` wait still showed initial `UNKNOWN`/`Probing...`.)* |
| **3** | **Follower Rejoin** | Restarted `coordinator-a` | **PASS** | Cluster automatically recovered to `3/3` healthy voting nodes. |
| **4** | **Leader Crash Failover & Lease Survival** | Stopped active leader `coordinator-b` while lock held (`chaos-persist-...`) | **PASS** | Automatic Raft re-election promoted `coordinator-c` in **`4.19s`**; existing lock `chaos-persist-...` survived leader crash intact (`200`, `tok=1`). |
| **5** | **Majority Quorum Loss (Fail-Closed)** | Stopped `coordinator-a` & `coordinator-b` (only `coordinator-c` running) | **PASS** | `/recovery/cluster` returned `200` in ~`0.8s` reporting `quorum_available: false`, `quorum_state: "QUORUM_LOST"`, `current_leader_id: ""`, `coordinator-c role: "Candidate"`. Lock writes & linearizable reads failed closed with **`503 ERR_QUORUM_UNAVAILABLE`**. *(UI `/resilience` snapshot at `1.5s` showed `Probing...`/`UNKNOWN` + browser `503` logs.)* |
| **6** | **Quorum Recovery** | Restarted stopped coordinators | **PASS** | Cluster restored to `3/3` quorum (`coordinator-a` re-elected Leader). |
| **7** | **Full 3-Node Restart & BoltDB Durability** | `docker restart` on all 3 coordinators | **PASS** | Existing lock `chaos-persist-...` persisted across full cluster restart (`200`, `tok=1`); releasing and re-acquiring yielded monotonic `new=2` (`monotonic=True`). |
| **8** | **PostgreSQL Outage & Recovery** | Stopped `lockguard-postgres`, exercised locks, then restarted PG | **PASS** | During PG outage: `/ready` returned **`503`** (`database: unavailable: database unavailable: driver: bad connection`), `/recovery/diagnostics` showed `database_available: False`, while distributed lock operations remained **`200 OK`** (`acquire=200`, `release=200`). After PG restart: `/ready` recovered to **`200`** (`database: ready`), `audit_health` flushed buffered outage events (`outage_events_persisted=2`, `failed_total: 0`; `persisted_total` advanced cleanly across runs). |

---

## 7. Automated Test & Build Verification (Executed After Final Edits)

| Component | Command Executed | Outcome | Coverage / Package Breakdown |
| :--- | :--- | :---: | :--- |
| **Backend Go Suite** | `go vet ./... && go test -race -count=1 ./...` | **PASS** (`exit 0`) | **16 packages passed with `-race`:** `internal/api` (`1.808s`), `internal/audit` (`2.773s`), `internal/auth` (`2.655s`), `internal/chaos` (`2.513s`), `internal/config` (`2.126s`), `internal/consensus` (`4.223s`), `internal/coordinator` (`1.810s`), `internal/gateway` (`1.815s`), `internal/heartbeat` (`3.050s`), `internal/lockstate` (`2.407s`), `internal/metrics` (`2.545s`), `internal/middleware` (`1.655s`), `internal/persistence` (`2.255s`), `internal/recovery` (`1.436s`), `internal/renewal` (`2.072s`), `tests` (`18.410s`). |
| **Backend Non-Test Packages** | `go vet ./... && go test -race -count=1 ./...` | **NOT TESTED** (unit) | `cmd/api`, `cmd/coordinator`, `internal/storage` reported `[no test files]` (`cmd/api` and `cmd/coordinator` were built and verified live in Docker containers). |
| **Protected Resource Suite** | `go vet ./... && go test -race -count=1 ./...` | **PASS** (`exit 0`) | `lockguard/protected-resource/internal` (`1.506s`). Entrypoint package `cmd` reported `[no test files]` (built & verified live via `protected-resource:9000`). |
| **Frontend TypeCheck & Bundle** | `npm run build` (`tsc && vite build`) | **PASS** (`exit 0`) | `1678 modules transformed`; built `dist/assets/index-COjxbWhf.js` (`373.71 kB`) in `9.98s`. |

---

## 8. Remaining Honest Limitations, Partial Coverage & Non-Blocking Findings

1. **`PARTIAL` / `MOCKED` — PostgreSQL Supplemental Schema Tables (`0` Rows Observed):**
   - Database migrations executed cleanly, and `audit_events` actively persisted `33` rows across the live verification run.
   - However, post-test table inspection showed `users = 0`, `benchmark_runs = 0`, `operational_events = 0`, and `system_checkpoints = 0`.
   - Authentication currently relies on in-memory demo credentials in `auth.Service` (`MOCKED` relative to a persistent DB-backed user table), and `BenchmarksPage.tsx` combines `5` real live `/api/v1/cluster/status` latency calls with static documented Phase 5 baseline comparison figures (`MOCKED` baselines) without persisting runs to `benchmark_runs`.
2. **`PARTIAL` — `/resilience` Outage Probe Responsiveness & Console `503` Noise:**
   - Under immediate `1.5s` post-fault browser polling, `/resilience` transiently displays `UNKNOWN`/`Probing...` while uncached coordinator quorum probes serialize behind `LeaderDiscovery.mu`, and browser DevTools logs HTTP `503` responses returned intentionally by `/api/v1/ready` and `/api/v1/recovery/diagnostics` when quorum is lost.
3. **`PARTIAL` — Protected Resource Dashboard UI:**
   - Fencing enforcement on `protected-resource:9000/api/v1/write` works accurately over HTTP and in automated Go/live API tests, but no interactive UI form exists on the React dashboard to trigger `/api/v1/write` directly.
4. **`PARTIAL` — Auth Introspection Route (`/v1/auth/me`):**
   - `/v1/auth/login` issues and authenticates tokens cleanly; no `/v1/auth/me` session-introspection route exists.
5. **`NOT TESTED` (Unit Level) — Entrypoint & Stub Packages:**
   - `backend/cmd/api`, `backend/cmd/coordinator`, `backend/internal/storage`, and `protected-resource/cmd` contain no Go `*_test.go` unit test files, though `cmd/api`, `cmd/coordinator`, and `protected-resource/cmd` were compiled and verified via the live Docker Compose stack.
6. **Final Verified Stack Cleanliness State:**
   - `GET /api/v1/locks` -> `{"count":0,"locks":[]}`
   - `GET /api/v1/renewal/status` -> `{"count":0,"renewals":[]}`
   - All 9 Docker Compose containers remain `(healthy)`.
