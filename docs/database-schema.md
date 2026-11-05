# LockGuard — Database Schema and Migrations Specification

**Project:** LOCKGUARD — Fault Tolerant Distributed Lock Service  
**Phase:** Phase 3 — Operational Persistence, Durable Audit & Reliability  
**Classification:** Database Architecture & Schema Reference  

---

## 1. Storage Scope and Invariant Rules

LockGuard utilizes PostgreSQL 16 exclusively for operational event logging, audit trails, and historical telemetry.

> [!CAUTION]
> **PostgreSQL does NOT coordinate distributed locks.**
> Active lock records, lease timers, fencing tokens, and idempotency records are stored exclusively in Raft in-memory state machines and replicated via BoltDB (`raft.db`, `stable.db`). PostgreSQL tables MUST NEVER be queried or updated to determine live lock ownership.

---

## 2. Table Schemas

### 2.1 `schema_migrations`
Tracks versioned migration files applied to the database, ensuring schema reproducibility.

```sql
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INT PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    checksum VARCHAR(64) NOT NULL
);
```

| Column | Type | Constraints | Description |
| :--- | :--- | :--- | :--- |
| `version` | `INT` | `PRIMARY KEY` | Sequential migration sequence number (e.g. 1, 2) |
| `name` | `VARCHAR(255)` | `NOT NULL` | Descriptive name of migration |
| `applied_at` | `TIMESTAMPTZ` | `DEFAULT CURRENT_TIMESTAMP` | Timestamp when migration was executed |
| `checksum` | `VARCHAR(64)` | `NOT NULL` | SHA-256 hash of the migration SQL content |

---

### 2.2 `audit_events`
Append-only operational audit log recording lock operations, state machine evaluations, and consensus transitions.

```sql
CREATE TABLE IF NOT EXISTS audit_events (
    id BIGSERIAL PRIMARY KEY,
    event_id VARCHAR(64) UNIQUE NOT NULL,
    event_type VARCHAR(64) NOT NULL,
    event_category VARCHAR(64) NOT NULL DEFAULT 'LOCK_OPERATION',
    request_id VARCHAR(128),
    resource_id VARCHAR(255),
    owner_id VARCHAR(255),
    client_id VARCHAR(255),
    fencing_token BIGINT NOT NULL DEFAULT 0,
    lease_version BIGINT NOT NULL DEFAULT 0,
    operation_status VARCHAR(32) NOT NULL DEFAULT 'PENDING',
    error_code VARCHAR(64),
    coordinator_id VARCHAR(128),
    raft_index BIGINT NOT NULL DEFAULT 0,
    event_timestamp TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb
);
```

#### Indexes:
- `idx_audit_events_event_id_unique` ON `(event_id)` (Unique constraint for deduplication)
- `idx_audit_events_type` ON `(event_type)`
- `idx_audit_events_category` ON `(event_category)`
- `idx_audit_events_resource` ON `(resource_id)`
- `idx_audit_events_owner` ON `(owner_id)`
- `idx_audit_events_client` ON `(client_id)`
- `idx_audit_events_req_id` ON `(request_id)`
- `idx_audit_events_status` ON `(operation_status)`
- `idx_audit_events_timestamp` ON `(event_timestamp DESC)`

---

### 2.3 `operational_events`
Stores non-lock operational events including coordinator election changes, gateway failovers, and connection state alterations.

```sql
CREATE TABLE IF NOT EXISTS operational_events (
    id BIGSERIAL PRIMARY KEY,
    event_id VARCHAR(64) UNIQUE NOT NULL,
    event_type VARCHAR(64) NOT NULL,
    component VARCHAR(64) NOT NULL,
    node_id VARCHAR(128) NOT NULL,
    status VARCHAR(32) NOT NULL,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

#### Indexes:
- `idx_operational_events_type` ON `(event_type)`
- `idx_operational_events_component` ON `(component)`
- `idx_operational_events_node` ON `(node_id)`
- `idx_operational_events_created_at` ON `(created_at DESC)`

---

### 2.4 `system_checkpoints`
Stores operational checkpoint metadata (Raft index references, terms) for diagnostic comparisons.

```sql
CREATE TABLE IF NOT EXISTS system_checkpoints (
    id BIGSERIAL PRIMARY KEY,
    checkpoint_type VARCHAR(64) NOT NULL,
    node_id VARCHAR(128) NOT NULL,
    raft_index BIGINT NOT NULL DEFAULT 0,
    raft_term BIGINT NOT NULL DEFAULT 0,
    summary JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

#### Indexes:
- `idx_system_checkpoints_type` ON `(checkpoint_type, created_at DESC)`

---

## 3. Migration Runner & Concurrency Locking

Schema migrations are embedded directly into the Go binary (`//go:embed migrations/*.sql`) and coordinated automatically by `backend/internal/persistence/migrator.go`.

### 3.1 Distributed Migration Advisory Lock
To ensure multiple Gateway instances starting concurrently do not run race-condition migrations:
1. Prior to inspecting `schema_migrations`, the instance acquires a PostgreSQL 64-bit advisory lock:
   ```sql
   SELECT pg_advisory_lock(74152969);
   ```
2. Migrations are executed in numeric order inside dedicated transactions.
3. Upon completion or failure, the lock is released:
   ```sql
   SELECT pg_advisory_unlock(74152969);
   ```

### 3.2 Checksum Verification & Tamper Detection
Each SQL file's byte content is hashed via SHA-256. If a migration version was already recorded in `schema_migrations` with a different checksum, the migration runner halts immediately with `ErrMigrationFailed`, alerting operators to unauthorized schema drift.

