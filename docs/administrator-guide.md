# LOCKGUARD — Administrator & Operations Runbook

## Academic Project Title: Fault Tolerant Distributed Lock Service
**System Version:** Phase 6 Final Release  
**Target Audience:** Site Reliability Engineers (SRE), Infrastructure Architects, System Administrators  

---

## 1. Cluster Health & Operational Monitoring

### 1.1 Cluster Health Invariants
A healthy LOCKGUARD 3-node cluster satisfies:
1. **Single Leader Guarantee:** Exactly one node reports `"state": "Leader"`. All other live nodes report `"state": "Follower"`.
2. **Quorum Available:** At least 2 out of 3 nodes are alive and replicating logs ($Q \ge 2$).
3. **Audit Queue Health:** In-memory unwritten audit queue depth remains near 0 under steady state (`audit_queue_depth < 100`).

### 1.2 Prometheus Telemetry Metrics
Monitor these vital gauges in Grafana / Prometheus:
- `raft_peers`: Target value = 3.
- `raft_is_leader`: Binary flag (1 on leader, 0 on followers).
- `lockguard_active_locks`: Total active non-expired locks.
- `lockguard_fencing_token_current`: Strictly increasing 64-bit integer.
- `lockguard_database_migrations_status`: 1 = Up to date, 0 = Pending/Error.
- `lockguard_audit_buffer_capacity`: Percentage buffer saturation.

---

## 2. Maintenance Operations & Procedures

### 2.1 Graceful Leader Step-Down
To perform maintenance on the current Raft leader node without service disruption:
```bash
# Issue an administrative step-down request to the leader
curl -X POST http://localhost:8001/v1/cluster/stepdown \
  -H "Authorization: Bearer <ADMIN_JWT_TOKEN>"
```
The leader immediately abdicates, and the remaining 2 followers elect a new leader within 300ms. Traffic automatically shifts via the API Gateway.

### 2.2 Adding / Removing Cluster Nodes
LOCKGUARD supports dynamic Raft membership changes via HashiCorp Raft configuration APIs:
```bash
# Add a new voter node (Node 4)
curl -X POST http://localhost:8080/v1/admin/cluster/nodes \
  -H "Authorization: Bearer <ADMIN_JWT_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "node_id": "node-4",
    "address": "10.0.1.14:9004"
  }'
```

### 2.3 Raft Log Snapshotting & Compaction
Snapshots occur automatically every 8,192 committed entries. To trigger an immediate manual snapshot before maintenance:
```bash
curl -X POST http://localhost:8080/v1/admin/snapshots/trigger \
  -H "Authorization: Bearer <ADMIN_JWT_TOKEN>"
```
Snapshots are persisted to `data/<node_id>/raft/snapshots/` as atomic gzip-compressed JSON blocks.

---

## 3. Database Maintenance & Audit Retention

### 3.1 PostgreSQL Vacuuming & Indexing
The `audit_events` table accumulates historical log entries. Run regular automated vacuuming:
```sql
-- Weekly maintenance on PostgreSQL host
VACUUM ANALYZE audit_events;

-- Verify index status
REINDEX TABLE CONCURRENTLY audit_events;
```

### 3.2 Audit Log Archival Policy
For compliance, export records older than 90 days to cold storage:
```sql
COPY (
    SELECT * FROM audit_events 
    WHERE created_at < NOW() - INTERVAL '90 days'
) TO '/var/backups/audit_archive_quarterly.csv' WITH CSV HEADER;

DELETE FROM audit_events WHERE created_at < NOW() - INTERVAL '90 days';
```

---

## 4. Disaster Recovery Runbook

### Scenario A: Leader Node Crashed
1. **Diagnosis:** `lockguard-node1` stops responding.
2. **System Behavior:** `node-2` and `node-3` detect heartbeat absence within 300ms, elect `node-2` as new leader, and continue serving requests.
3. **Recovery Action:** Restart container `docker compose restart node1`. Node 1 rejoins as Follower, fetches missing logs via `AppendEntries`, and catches up.

### Scenario B: Complete Quorum Loss (2 Nodes Down)
1. **Diagnosis:** 2 out of 3 nodes crash. The remaining node enters Follower state and rejects all writes with `503 Service Unavailable`.
2. **Safety Rule:** LOCKGUARD prioritizes Consistency over Availability ($CP$). Locks are NOT released or granted while quorum is lost.
3. **Recovery Action:**
   - Restart the failed nodes: `docker compose up -d node1 node2`.
   - As soon as the 2nd node joins, a leader is elected within 300ms.
   - Operations resume with zero state corruption.

### Scenario C: Catastrophic Total Storage Failure (Cold Start)
1. **Diagnosis:** All nodes corrupted or disk wiped.
2. **Recovery Procedure:**
   - Restore the latest verified Raft snapshot file into `data/node1/raft/snapshots/`.
   - Set `RAFT_BOOTSTRAP=true` on Node 1.
   - Start Node 1: It boots from the snapshot and restores all locks and fencing tokens.
   - Start Node 2 and Node 3 with clean storage and `RAFT_BOOTSTRAP=false`: They join and replicate state from Node 1.

---

## 5. Security & Secret Rotation

### 5.1 Rotating JWT Secret Key
1. Update `JWT_SECRET_KEY` in `.env`.
2. Perform rolling restart of the API Gateway containers.
3. All previously issued JWT tokens become invalid; clients must re-authenticate with their credentials.

### 5.2 mTLS Certificate Rotation
1. Place new CA-signed certificate pairs in `/etc/lockguard/certs/`.
2. Execute a rolling restart of coordinator nodes one at a time (Followers first, Leader last after step-down).
3. Zero-downtime certificate renewal is achieved.

