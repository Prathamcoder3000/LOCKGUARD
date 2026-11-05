# LockGuard — Backup and Disaster Recovery Runbook

**Project:** LOCKGUARD — Fault Tolerant Distributed Lock Service  
**Phase:** Phase 3 — Operational Persistence, Durable Audit & Reliability  
**Classification:** Operational Runbook  

---

## 1. Architectural Authority Principles

In LockGuard, persistent state is partitioned strictly according to operational role and consistency guarantees:

| Subsystem | Storage Engine | Authoritative Scope | Recovery Semantics |
| :--- | :--- | :--- | :--- |
| **Distributed Lock State** | HashiCorp Raft (BoltDB + Raft Snapshots) | Active lock leases, ownership, fencing tokens, lease versions, idempotency | **Strict Raft Quorum Consensus.** State is reconstructed solely from committed Raft logs and snapshots. |
| **Operational Audit Trail** | PostgreSQL 16 (Connection Pool + Relational Tables) | Historical lock events, state transitions, client heartbeats, operational errors | **Append-Only Evidence.** Decoupled from consensus. Never restores or modifies active in-memory lock state. |

> [!IMPORTANT]
> **Raft is the sole authority for distributed locks.** PostgreSQL is an operational evidence store. A restore of an older PostgreSQL backup must NEVER overwrite or alter active lock records or fencing tokens managed by Raft.

---

## 2. PostgreSQL Operational Data Backup Procedures

### 2.1 Logical Backup via `pg_dump`
To create a consistent, transactional backup of audit logs, operational events, and system checkpoints:

```bash
# Set timestamp variable
BACKUP_TIMESTAMP=$(date +%Y%m%d_%H%M%S)
BACKUP_FILE="lockguard_postgres_backup_${BACKUP_TIMESTAMP}.sql.gz"

# Execute pg_dump from within Docker network or host
docker exec -t lockguard-postgres pg_dump \
  -U lockguard_user \
  -d lockguard_db \
  --clean \
  --if-exists \
  --no-owner \
  --no-privileges | gzip > "${BACKUP_FILE}"

echo "Backup written to ${BACKUP_FILE}"
```

### 2.2 Table-Specific Archive (Audit Logs Only)
If exporting only immutable audit records for regulatory compliance:

```bash
docker exec -t lockguard-postgres pg_dump \
  -U lockguard_user \
  -d lockguard_db \
  --table=audit_events \
  --data-only \
  --column-inserts | gzip > "audit_events_archive_${BACKUP_TIMESTAMP}.sql.gz"
```

---

## 3. PostgreSQL Restore Procedures

### 3.1 Point-in-Time Database Restoration

1. **Place Application Gateway in Maintenance Mode (or scale down gateway):**
   ```bash
   docker compose stop api
   ```
   *(Note: The Raft coordinator cluster remains online to maintain lock leases without interruption).*

2. **Restore PostgreSQL Database from Compressed Dump:**
   ```bash
   gunzip -c lockguard_postgres_backup_YYYYMMDD_HHMMSS.sql.gz | \
     docker exec -i lockguard-postgres psql -U lockguard_user -d lockguard_db
   ```

3. **Verify Schema Migration Status:**
   ```bash
   docker exec -it lockguard-postgres psql -U lockguard_user -d lockguard_db -c \
     "SELECT version, name, applied_at, checksum FROM schema_migrations ORDER BY version ASC;"
   ```

4. **Verify Audit Records Integrity:**
   ```bash
   docker exec -it lockguard-postgres psql -U lockguard_user -d lockguard_db -c \
     "SELECT count(*), min(event_timestamp), max(event_timestamp) FROM audit_events;"
   ```

5. **Restart API Gateway:**
   ```bash
   docker compose start api
   ```

---

## 4. Raft Persistent Volume Backup & Snapshot Handling

### 4.1 Raft Storage Anatomy
Each Raft coordinator (`coordinator-a`, `coordinator-b`, `coordinator-c`) maintains an independent storage directory under `/var/lib/lockguard/data`:
- `raft.db`: BoltDB log store containing append-only replicated log entries.
- `stable.db`: BoltDB stable store holding Raft metadata (current term, voted for).
- `snapshots/`: Raft snapshot store containing serialized compact state machine snapshots.

### 4.2 Safe Raft Volume Backup Procedure
Because BoltDB files use memory mapping and file locks, hot copies during high-write consensus may capture torn pages. Follow this procedure:

1. **Identify Follower Node:**
   Query the gateway `/v1/cluster/status` or coordinator endpoints to identify a follower node (e.g., `coordinator-c`).

2. **Stop the Follower Node (Quorum 2 of 3 remains healthy):**
   ```bash
   docker compose stop coordinator-c
   ```

3. **Archive Follower Volume:**
   ```bash
   docker run --rm \
     -v lockguard_coordinator_c_data:/data:ro \
     -v $(pwd):/backup \
     alpine tar czf /backup/coordinator_c_raft_backup_${BACKUP_TIMESTAMP}.tar.gz -C /data .
   ```

4. **Restart Follower Node:**
   ```bash
   docker compose start coordinator-c
   ```
   *Upon startup, the follower rejoins the cluster and catches up on any missed log entries via Raft log replication.*

---

## 5. Recovery Under Operational Failure Scenarios

### 5.1 Scenario 1: Transient Database Outage
- **Observation:** PostgreSQL container stops, network partitions, or disk fills up.
- **Service Behavior:**
  - Raft coordinator cluster continues operating normally.
  - Gateway continues proxying lock acquisitions, renewals, and releases with full linearizable consistency.
  - Asynchronous audit pipeline queues incoming audit records in memory (up to 10,000 events).
  - `/v1/recovery/diagnostics` reports overall status `DEGRADED` (Raft healthy, DB offline).
- **Resolution:**
  - Restart PostgreSQL container: `docker compose start postgres`.
  - The audit pipeline flusher detects reconnection, executes pending batch inserts with exponential backoff and jitter, and clears the volatile queue.

### 5.2 Scenario 2: Coordinator Node Failure / Split Brain
- **Observation:** One coordinator crashes or loses network connectivity.
- **Service Behavior:**
  - The remaining 2 nodes maintain strict quorum ($2 > 3/2$).
  - A new leader is elected within election timeout (1000ms).
  - The API Gateway detects leader transition via HTTP 421 Misdirected Request, invalidates its cache, and transparently routes to the new leader.
- **Resolution:**
  - Restart failed node: `docker compose start coordinator-b`.
  - Node replays missing BoltDB log entries and resumes follower duties.

### 5.3 Scenario 3: Complete Cluster Cold Restart
- **Observation:** Planned host maintenance or whole-cluster power outage.
- **Recovery Order:**
  1. `docker compose up -d postgres` (wait for healthy check).
  2. `docker compose up -d coordinator-a coordinator-b coordinator-c` (coordinators reload BoltDB, elect leader).
  3. `docker compose up -d api` (gateway connects to leader, initializes audit pipeline, verifies migrations).
  4. `docker compose up -d protected-resource frontend nginx prometheus`.

