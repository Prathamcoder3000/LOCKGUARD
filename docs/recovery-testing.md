# LOCKGUARD Recovery & Resilience Testing Guide

## 1. Overview of Phase 4 Test Suite

The Phase 4 test suite evaluates cluster recovery, state machine invariants, consensus safety, and fault resilience across multiple specialized test packages located under `backend/tests/`.

| Test File | Focus Area | Scenarios Covered |
| :--- | :--- | :--- |
| `phase4_node_failure_test.go` | Coordinator & Leader Failure | Leader crash failover, follower crash catch-up, quorum loss (fail-closed), isolated leader deposition. |
| `phase4_network_partition_test.go` | Transport & Partitioning | Bidirectional partition healing, 3-cycle partition stress, cross-cycle token monotonicity. |
| `phase4_snapshot_disaster_recovery_test.go` | Disaster Recovery & Snapshots | Monotonic token persistence across snapshot restore, corrupt/truncated snapshot rejection. |
| `phase4_protected_resource_fencing_test.go` | End-to-End Fencing Safety | Downstream resource validation, rejection of stale fencing tokens (HTTP 409). |
| `phase4_session_renewal_recovery_test.go` | Renewal & Session Resiliency | Automatic lease renewal through leader change, client heartbeat expiry scrubber. |
| `phase4_database_recovery_test.go` | Storage & Audit Reliability | Decoupled queue buffering, database outage backlog drain, deduplication on conflict. |
| `phase4_chaos_runner_test.go` | Automated Chaos Orchestration | Multi-scenario chaos runner with fixed seeds and JSON report generation. |
| `phase4_benchmark_test.go` | Recovery Benchmarking | Microbenchmarks for election latency, snapshot throughput, and follower catch-up. |

---

## 2. Prerequisites & Environment Setup

### Environment Variables
For Windows PowerShell execution:
```powershell
$env:PATH = "D:\DE-Tools\go\bin;D:\DE-Tools\w64devkit\bin;" + $env:PATH
$env:CGO_ENABLED = "1"
```

### PostgreSQL Test Container
The database recovery test verifies live PostgreSQL transactions on port `5435`:
```bash
docker compose up -d postgres
```

---

## 3. Running Test Suites

### Running All Phase 4 Resilience Tests
```bash
cd backend
go test -v -count=1 ./tests/phase4_...
```

### Running with Race Detector
To ensure thread-safety across concurrent goroutines:
```bash
cd backend
go test -race -count=1 ./tests/phase4_...
```

### Running Specific Scenarios
```bash
# Run Leader Crash Failover scenario
go test -v -run TestPhase4_Scenario1_LeaderCrashAndFailoverRecovery ./tests/

# Run Quorum Loss Fail-Closed scenario
go test -v -run TestPhase4_Scenario3_TwoNodeFailure_QuorumLoss_FailClosed ./tests/

# Run End-to-End Protected Resource Fencing Test
go test -v -run TestPhase4_ProtectedResource_FencingSafety ./tests/

# Run Automated Chaos Runner (Generates docs/chaos-test-report.json)
go test -v -run TestPhase4_Chaos_AutomatedSuite ./tests/
```

### Running Recovery Benchmarks
```bash
cd backend
go test -v -run TestPhase4_Benchmark_RecoveryTimings ./tests/
go test -bench=BenchmarkRecoveryMetrics -benchmem ./tests/
```

---

## 4. Continuous Integration & Chaos Verification

In automated CI/CD pipelines (e.g. GitHub Actions), add the following step:

```yaml
- name: Run Phase 4 Resilience & Chaos Tests
  run: |
    cd backend
    go test -v -race -timeout 10m ./tests/phase4_...
```

The test runner will automatically fail any build where:
- Quorum loss permits a write (violating fail-closed guarantee).
- Fencing tokens fail to increase monotonically across leader transitions.
- A stale leader continues processing commits after isolation.
- Corrupted snapshots trigger unhandled panics.

