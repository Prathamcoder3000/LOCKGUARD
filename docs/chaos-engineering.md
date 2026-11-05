# LOCKGUARD Chaos Engineering Framework

## 1. Overview and Architecture

The **LOCKGUARD Chaos Engineering Framework** (`backend/internal/chaos`) provides controlled, programmatic fault injection directly into the distributed coordination cluster. Designed specifically for distributed consensus verification, the framework allows developers and operators to rigorously stress the Raft state machine, leader election mechanisms, transport layers, and persistence systems under hostile network and node conditions.

### Architectural Goals
- **Deterministic Chaos Orchestration:** Every injection experiment can be parameterized with fixed pseudo-random seeds (`int64`) to reproduce split-brain, network flap, and crash scenarios deterministically.
- **Strict Production Safety Gates:** Chaos operations are forbidden from executing against production environments by compile-time and runtime environmental inspection.
- **Fail-Safe Automatic Reversal:** Every injected fault has a tracked lifecycle (`Inject() -> Active -> Revert()`). In the event of test termination or timeout, `RevertAll()` restores the cluster to standard operational topology.
- **Telemetry & Event Emission:** Injected faults fire registered diagnostic hooks (`FaultEventListener`), streaming real-time status transitions to Prometheus metrics and the operational audit trail.

```
       ┌────────────────────────────────────────────────────────┐
       │               Chaos Controller (Thread-Safe)           │
       │       - Active Faults Registry (sync.RWMutex)          │
       │       - Production Safety Gates                        │
       │       - Admin Authorization Verification               │
       └──────────────────────────┬─────────────────────────────┘
                                  │
       ┌──────────────────────────┼─────────────────────────────┐
       ▼                          ▼                             ▼
┌──────────────────┐    ┌──────────────────┐         ┌─────────────────────┐
│  Transport Split │    │  Process Crash   │         │  Postgres Outage    │
│  (Raft Inmem/TCP)│    │  (Graceful/Hard) │         │  (Backlog Buffering)│
└──────────────────┘    └──────────────────┘         └─────────────────────┘
```

---

## 2. Safety Controls & Security

Fault injection mechanisms represent inherent operational risks if accidentally invoked on production workloads. LockGuard implements a multi-layered defense to enforce strict containment:

1. **Environment Gate (`IsSafeEnvironment`):**
   - The controller interrogates `ENVIRONMENT` and `APP_ENV`.
   - If the environment equals `"production"`, `"prod"`, or `"live"`, all fault injections fail immediately with `ERR_CHAOS_DISABLED_IN_PROD` (`chaos injection strictly prohibited in production environments`).
2. **Administrative Authorization Header:**
   - Any HTTP or internal controller mutation requires an administrative secret matching `LOCKGUARD_ADMIN_TOKEN` or `CHAOS_ADMIN_TOKEN`.
   - Unauthorized attempts return `ERR_CHAOS_UNAUTHORIZED` (`unauthorized chaos invocation`).
3. **Execution Timeouts:**
   - Fault descriptors mandate a maximum duration (`time.Duration`).
   - If the chaos runner or test suite exits prematurely, background timers automatically revert partition rules and network drops.

---

## 3. Fault Taxonomy & Implementation Status Matrix

The chaos framework declares faults across cluster layers. The table below distinguishes between fully operational in-memory fault injectors, invariant-verified properties, and environment-limited types:

| Fault Type | Identifier | Subsystem Affected | Status | Implementation & Invariant Tested |
| :--- | :--- | :--- | :--- | :--- |
| **Process Crash** | `COORDINATOR_PROCESS_CRASH` | Coordinator Node | **Working & Tested** | Injected via coordinator shutdown; verified leader failover and follower reconnect catch-up. |
| **Network Partition** | `COORDINATOR_NETWORK_PARTITION` | Raft Peer Transport | **Working & Tested** | Injected via in-memory transport severance; minority fails closed, majority preserves consensus. |
| **Database Outage** | `POSTGRESQL_CONNECTIVITY_OUTAGE` | PostgreSQL Audit Sink | **Working & Tested** | Injected via database connection closure; audit events buffered in-memory up to 10k capacity, drained upon reconnect. |
| **Delayed Request** | `DELAYED_CLIENT_REQUEST` | Protected Resource | **Working & Tested** | Injected via delayed client mutation bearing stale fencing token; rejected with HTTP 409 Conflict. |
| **Clock Skew** | `CLOCK_SKEW` | Monotonic State Machine | **Invariant Proven** | The state machine uses deterministic monotonic logical timestamps (`effectiveNow = max(cmd.Timestamp, lastApplied)`); wall clock skew cannot alter lease order. |
| **Packet Loss / Jitter** | `COORDINATOR_PACKET_DROP` | Raft Peer Transport | **Environment-Limited** | Declared in taxonomy; in current in-memory test harness, full disconnection is tested rather than probabilistic OS packet drops (requires Linux `netem`/`iptables`). |
| **Disk Pressure** | `DISK_PRESSURE` | BoltDB & Snapshots | **Environment-Limited** | Declared in taxonomy; simulated via test-level mock sinks rather than OS-level cgroup disk throttling. |

> [!NOTE]
> Audit event buffering during database outages operates under the approved Phase 3 best-effort design: in-memory buffers store up to 10,000 events. If an outage exceeds queue capacity or a crash occurs prior to flush, unflushed events are dropped with explicit accounting via `lockguard_audit_events_failed_total`. Raft consensus lock mutations are never blocked.

---

## 4. Programmatic Go API

The chaos framework is exposed via `backend/internal/chaos`:

```go
package chaos

// NewController creates a new chaos controller with safety checks
func NewController(env string, adminToken string) *Controller

// Inject activates a fault descriptor and notifies listeners
func (c *Controller) Inject(ctx context.Context, f FaultDescriptor, authToken string) (*FaultEvent, error)

// Revert reverses an active fault
func (c *Controller) Revert(ctx context.Context, faultID string, authToken string) (*FaultEvent, error)

// RevertAll safely rolls back all active faults
func (c *Controller) RevertAll(ctx context.Context) error
```

### Cluster Orchestration Helpers (`cluster_chaos.go`)
```go
// Sever node-c from the cluster
err := PartitionNode(ctx, ctrl, "coordinator-c", "secret", func(target string, isolated bool) error {
    if isolated {
        transportC.DisconnectAll()
        transportA.Disconnect("coordinator-c:7000")
        transportB.Disconnect("coordinator-c:7000")
    } else {
        transportC.Connect("coordinator-a:7000", transportA)
        transportC.Connect("coordinator-b:7000", transportB)
    }
    return nil
})
```

---

## 5. Automated Chaos Runner

The automated test runner (`backend/tests/phase4_chaos_runner_test.go`) executes comprehensive failure combinations using pseudo-random seeds. Run via:

```bash
cd backend
go test -v -run TestPhase4_Chaos_AutomatedSuite
```

Upon execution, the runner outputs a structured JSON report to `docs/chaos-test-report.json` detailing scenario outcomes, duration, and safety assertions.

