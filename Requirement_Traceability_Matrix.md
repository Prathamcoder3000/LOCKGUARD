# LOCKGUARD — Requirement Traceability Matrix (RTM)

**Project Title:** Fault Tolerant Distributed Lock Service (LOCKGUARD)  
**Academic Course:** Software Design (SD) | Semester V (AY 2026–2027)  
**Institution:** Veermata Jijabai Technological Institute (VESIT)  
**Authors:** Prathamesh Shelar (Roll No. 65), Chandrabhanu Prusty (Roll No. 48)  
**Supervisor:** Dr. Priya R.L.  
**Verification Date:** 10 November 2026  

---

## 1. Traceability Matrix Overview

This matrix provides complete end-to-end traceability from academic requirements and distributed systems invariants to the concrete source code files, functions, API routes, automated tests, cookbook sections, and visual verification evidence.

---

## 2. Requirement Traceability Table

| Req ID | Requirement Description | Implementation File | Function / Component | API Endpoint / UI | Automated Test Case | Cookbook Phase | Visual Evidence Ref | Status |
| :---: | :--- | :--- | :--- | :--- | :--- | :---: | :--- | :---: |
| **REQ-01** | Linearizable Mutual Exclusion | `backend/internal/lockstate/fsm.go` | `Engine.AcquireLock()` | `POST /v1/locks/acquire` | `TestAcquireActiveResourceBySecondOwnerFails` | Phase 9 | `term_06_competing_conflict.png` | **VERIFIED** |
| **REQ-02** | Monotonic Fencing Tokens | `backend/internal/lockstate/fsm.go` | `HighestToken`, `newToken = HighestToken + 1` | `POST /v1/locks/acquire` | `TestFencingTokensMonotonicityAndIndependence` | Phase 12 | `term_12_reacquire_token_monotonic.png` | **VERIFIED** |
| **REQ-03** | Exact Lease Version Advancement | `backend/internal/lockstate/fsm.go` | `Engine.RenewLock()`, `V_new = V_old + 1` | `POST /v1/locks/renew` | `TestRenewActiveLock` | Phase 11 | `term_08_lease_renewal.png` | **VERIFIED** |
| **REQ-04** | Deterministic Expire & Stale Guard | `backend/internal/lockstate/fsm.go` | `Engine.ExpireLock()` | `POST /v1/locks/expire` | `TestExpire_StaleCommandRejectedAfterRenewal` | Phase 8 | `diag_lock-acquisition-flow.png` | **VERIFIED** |
| **REQ-05** | Request Idempotency | `backend/internal/lockstate/idempotency.go`| `checkIdempotency()` | All Lock Mutations | `TestIdempotencyAcquire`, `TestIdempotencyRenew` | Phase 8 | `backend/internal/lockstate/idempotency.go` | **VERIFIED** |
| **REQ-06** | 3-Node Raft Consensus Quorum | `backend/internal/consensus/node.go` | `RaftNode.ApplyCommand()` | Internal Port 7000 / `mTLS` | `TestCluster_ThreeNodeElectionAndReplication` | Phase 14 | `fig_03_cluster_consensus.png` | **VERIFIED** |
| **REQ-07** | Fail-Closed Under Quorum Loss | `backend/internal/consensus/fsm_adapter.go`| Quorum threshold check | `/v1/cluster/status` | `TestCluster_QuorumLoss_FailClosed` | Phase 17 | `diag_failure-recovery-flow.png` | **VERIFIED** |
| **REQ-08** | Durable Raft Log & Snapshots | `backend/internal/consensus/snapshot.go` | `SnapshotData`, `Restore()` | FileSnapshotStore | `TestCluster_BoltDBPersistenceAndRecovery` | Phase 17 | `diag_deployment-architecture.png` | **VERIFIED** |
| **REQ-09** | Follower 421 Redirection | `backend/internal/coordinator/handler.go` | `handleAcquire()` | HTTP 421 with leader headers | `TestCoordinatorHandler_AcquireRenewAndRead` | Phase 14 | `term_04_cluster_status.png` | **VERIFIED** |
| **REQ-10** | Client Heartbeat Sessions | `backend/internal/heartbeat/manager.go` | `Manager.Register()`, `Heartbeat()` | `POST /v1/clients/register` | `TestHeartbeatManager_Lifecycle` | Phase 6 | `diag_authentication-flow.png` | **VERIFIED** |
| **REQ-11** | Automated Lease Renewal Worker | `backend/internal/renewal/manager.go` | `Manager.Start()`, `workerLoop()` | `POST /v1/renewal/start` | `TestRenewalManager_SuccessfulRenewalLifecycle` | Phase 11 | `diag_lock-renewal-flow.png` | **VERIFIED** |
| **REQ-12** | Zero-Dependency HS256 JWT Auth | `backend/internal/auth/jwt.go` | `GenerateToken()`, `ValidateToken()`| `POST /v1/auth/login` | `TestJWT_GenerationAndValidation` | Phase 7 | `fig_01_login_page.png` | **VERIFIED** |
| **REQ-13** | Role-Based Access Control (RBAC)| `backend/internal/auth/middleware.go` | `RequireRole()` | All protected routes | `TestRBAC_AdminAllowedClientDenied` | Phase 7 | `term_03_health_check.png` | **VERIFIED** |
| **REQ-14** | Cross-Client Ownership Isolation | `backend/internal/api/server.go` | `EnforceOwnerIsolation()` | `POST /v1/locks/release` | `TestOwnerIsolation_CrossClientForbidden` | Phase 10 | `term_07_cross_release_denial.png` | **VERIFIED** |
| **REQ-15** | Token Bucket Rate Limiting | `backend/internal/middleware/ratelimit.go`| `TokenBucket.Allow()` | Edge Ingress | `TestRateLimiter_BurstAndThrottle` | Phase 7 | `backend/internal/middleware/ratelimit.go` | **VERIFIED** |
| **REQ-16** | Downstream Storage Fencing | `protected-resource/internal/fencing.go`| `FencingGuard.ValidateAndApply()`| `POST :9000/api/v1/write` | `TestRejectLowerTokens`, `TestAcceptNewerTokens`| Phase 13 | `term_09_protected_write.png` | **VERIFIED** |
| **REQ-17** | Zombie Writer Rejection | `protected-resource/internal/fencing.go`| `ValidateAndApply()` | `POST :9000/api/v1/write` | `TestStaleOwnerAfterNewerTokenAccepted` | Phase 13 | `term_10_stale_token_rejection.png` | **VERIFIED** |
| **REQ-18** | Decoupled Async Audit Pipeline | `backend/internal/audit/pipeline.go` | `Pipeline.RecordEvent()`, `flushBatch()`| `GET /v1/audit/events` | `TestPipelineEnqueueAndBatchFlush` | Phase 15 | `diag_database-audit-flow.png` | **VERIFIED** |
| **REQ-19** | Audit Multi-Tenant Isolation | `backend/internal/persistence/postgres_audit_repo.go` | `GetEvents()` with `(owner_id = $X)` | `GET /v1/audit/events` | `TestPhase3AuditPersistenceAndPagination`| Phase 15 | `fig_11_operator_audit_isolation.png` | **VERIFIED** |
| **REQ-20** | Admin Emergency Unlock | `backend/internal/api/server.go` | `handleAdminEmergencyUnlock()` | `POST /v1/admin/locks/emergency-unlock`| `TestAdminEmergencyUnlock_BreaksLock` | Phase 18 | `term_11_emergency_unlock.png` | **VERIFIED** |
| **REQ-21** | Admin Raft Leader Stepdown | `backend/internal/coordinator/handler.go` | `handleClusterStepdown()` | `POST /v1/admin/cluster/stepdown` | `TestAdminStepdown_TriggersElection` | Phase 18 | `fig_03_cluster_consensus.png` | **VERIFIED** |
| **REQ-22** | Prometheus Telemetry & Metrics | `backend/internal/api/server.go` | `/v1/metrics`, Prometheus handler | `GET /metrics`, `:9090` | `TestMetrics_RegistryExposition` | Phase 16 | `fig_13_prometheus_dashboard.png` | **VERIFIED** |
| **REQ-23** | Operator Dashboard Visibility | `frontend/src/pages/ClusterPage.tsx` | `ClusterPage`, `LocksPage` | UI Dashboard (:80) | `npm run build` | Phase 6 | `fig_02_admin_overview.png` | **VERIFIED** |
| **REQ-24** | Secret Masking on Settings | `frontend/src/pages/SettingsPage.tsx` | `SettingsPage` | `/settings` | Code inspection | Phase 6 | `fig_09_system_settings.png` | **VERIFIED** |
| **REQ-25** | Single Coordinator Crash Recovery| `docker-compose.yml` | `coordinator-c` restart | Docker Compose | `TestCluster_LeaderFailover` | Phase 17 | `term_02_docker_ps.png` | **VERIFIED** |
| **REQ-26** | PostgreSQL Database Outage Isolation| `backend/internal/audit/pipeline.go` | Bounded buffer (50k) retry loop | `docker stop postgres` | `TestPhase3DatabaseOutageIsolation` | Phase 15 | `docs/diagrams/database-audit-flow.mmd` | **VERIFIED** |
| **REQ-27** | Monorepo Orchestration | `docker-compose.yml` | 9-Service Compose Architecture | `docker compose up -d` | `docker compose config` | Phase 5 | `term_02_docker_ps.png` | **VERIFIED** |
