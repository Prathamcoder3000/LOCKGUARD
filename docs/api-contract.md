# LockGuard API Contract & Protocol Specification

## 1. Response Envelopes & Conventions

All endpoints adhere to standardized JSON envelopes with RFC3339 timestamps.

### Success Envelope
```json
{
  "success": true,
  "data": { ... },
  "timestamp": "2026-11-05T01:30:00.000000000Z"
}
```

### Error Envelope
```json
{
  "success": false,
  "error": {
    "code": "ERR_LOCK_HELD",
    "message": "Resource 'bank-account-42' is currently locked by another client",
    "details": {
      "resource": "bank-account-42",
      "holder": "client-worker-09",
      "expires_in_ms": 4200
    }
  },
  "timestamp": "2026-11-05T01:30:00.000000000Z"
}
```

---

## 2. Standard Error Codes Registry

| Error Code | HTTP Status | Description |
| :--- | :--- | :--- |
| `ERR_BAD_REQUEST` | 400 | Malformed JSON, missing fields, or invalid schema parameters. |
| `ERR_UNAUTHORIZED` | 401 | Missing or invalid authentication credentials. |
| `ERR_FORBIDDEN` | 403 | Insufficient RBAC role permissions. |
| `ERR_NOT_FOUND` | 404 | Requested lock or resource does not exist. |
| `ERR_METHOD_NOT_ALLOWED` | 405 | HTTP verb not allowed on endpoint. |
| `ERR_LOCK_HELD` | 409 | Lock is actively held by a competing client. |
| `ERR_FENCING_TOKEN_STALE` | 409 | Fencing token is lower than the highest accepted token. |
| `ERR_OWNER_MISMATCH` | 409 | Fencing token was accepted for another owner; cross-owner conflict. |
| `ERR_LEASE_EXPIRED` | 410 | Lease duration elapsed prior to renewal or release. |
| `ERR_NOT_READY` | 503 | Node subsystems or storage unready. |
| `ERR_NOT_LEADER` | 421 / 503 | Node is not authoritative leader (headers `X-Raft-Leader-ID`, `X-Raft-Leader-Address` provided). |
| `ERR_QUORUM_UNAVAILABLE` | 503 | Quorum loss: cluster failed closed. |
| `ERR_INTERNAL` | 500 | Unhandled internal runtime exception. |

---

## 3. Implemented Endpoints (Phase 0)

### `GET /health` & `GET /api/v1/health`
**Description:** Liveness probe to check if the HTTP service process is running.

**Response `200 OK`:**
```json
{
  "success": true,
  "data": {
    "status": "ok",
    "service": "lockguard-api",
    "version": "0.1.0",
    "node_id": "coordinator-a",
    "timestamp": "2026-11-05T01:30:00Z"
  },
  "timestamp": "2026-11-05T01:30:00Z"
}
```

---

### `GET /ready` & `GET /api/v1/ready`
**Description:** Readiness probe checking local storage access and configuration validity.

**Response `200 OK`:**
```json
{
  "success": true,
  "data": {
    "status": "ready",
    "service": "lockguard-api",
    "node_id": "coordinator-a",
    "components": {
      "config": "ready",
      "storage": "ready",
      "database": "standby_phase0"
    },
    "timestamp": "2026-11-05T01:30:00Z"
  },
  "timestamp": "2026-11-05T01:30:00Z"
}
```

---

### `GET /api/v1/cluster/status`
**Description:** Returns cluster configuration, topology, and core invariant definitions.

**Response `200 OK`:**
```json
{
  "success": true,
  "data": {
    "service": "lockguard-api",
    "node_id": "coordinator-a",
    "environment": "development",
    "raft_cluster": [
      "coordinator-a:7000",
      "coordinator-b:7000",
      "coordinator-c:7000"
    ],
    "quorum_rule": "2 of 3 nodes constitute quorum (Fail Closed)",
    "design_principle": "CONSISTENCY FIRST — FAIL CLOSED",
    "storage_path": "./data",
    "subsystems": {
      "raft_consensus": "initialized_phase0",
      "state_machine": "pending_phase1",
      "lease_manager": "pending_phase2",
      "fencing_manager": "pending_phase2",
      "snapshot_manager": "pending_phase3",
      "protected_resource": "pending_phase4",
      "benchmark_module": "pending_phase5"
    },
    "timestamp": "2026-11-05T01:30:00Z"
  },
  "timestamp": "2026-11-05T01:30:00Z"
}
```

---

## 4. Protected Resource Service Endpoints

### `POST /api/v1/write` (Port 9000)
**Description:** Commits a write to the protected resource, requiring a valid fencing token and owner identity.

**Validation Semantics:**
1. **Token Lower than Highest Accepted ($T < T_{highest}$):** Stale write. Rejected unconditionally with HTTP 409 (`ERR_FENCING_TOKEN_STALE`).
2. **Token Equal to Highest Accepted ($T == T_{highest}$):** Same lease epoch.
   - If `owner_id == state.OwnerID`: Valid repeated write under active lease. Accepted with HTTP 200.
   - If `owner_id != state.OwnerID`: Owner mismatch or token collision. Rejected with HTTP 409 (`ERR_OWNER_MISMATCH`).
3. **Token Greater than Highest Accepted ($T > T_{highest}$):** Newer lease epoch.
   - Atomically updates $T_{highest} \leftarrow T$ and active owner $\leftarrow \text{owner\_id}$. Accepted with HTTP 200.
   - Any future write with token $< T$ is permanently invalidated.

**Request:**
```json
{
  "resource_name": "bank-ledger",
  "fencing_token": 1042,
  "owner_id": "worker-proc-77",
  "payload": "Transfer $500 from Account A to Account B"
}
```

**Success `200 OK` (First or Repeated Same-Lease Write):**
```json
{
  "success": true,
  "message": "write committed safely under fencing token verification",
  "applied_token": 1042,
  "owner_id": "worker-proc-77"
}
```

**Error `409 Conflict` (Stale Token):**
```json
{
  "success": false,
  "error": {
    "code": "ERR_FENCING_TOKEN_STALE",
    "message": "stale fencing token: token is lower than highest accepted token for this resource: received 1000, highest accepted is 1042"
  }
}
```

**Error `409 Conflict` (Owner Mismatch on Same Token):**
```json
{
  "success": false,
  "error": {
    "code": "ERR_OWNER_MISMATCH",
    "message": "fencing token owner mismatch: active token was established by another owner"
  }
}
```

---

## 5. Implemented Coordinator Endpoints (Phase 1B — Port 8001)

Each coordinator node exposes direct consensus-backed endpoints under `/v1/`:

### Follower Redirection Semantics
When any mutation (`acquire`, `renew`, `release`, `expire`) is submitted to a Follower:
- **HTTP Status:** `421 Misdirected Request`
- **Headers:**
  - `X-Raft-Leader-ID: <leader_node_id>`
  - `X-Raft-Leader-Address: <leader_raft_address>`
- **Response Body:**
  ```json
  {
    "success": false,
    "error": {
      "code": "ERR_NOT_LEADER",
      "message": "node is not the cluster leader: current leader is coordinator-a at coordinator-a:7000",
      "details": {
        "leader_id": "coordinator-a",
        "leader_address": "coordinator-a:7000"
      }
    },
    "timestamp": "2026-11-05T01:30:00Z"
  }
  ```

### `POST /v1/locks/acquire`
**Request:**
```json
{
  "request_id": "req-uuid-1",
  "resource_name": "database-migration",
  "owner_id": "worker-proc-77",
  "ttl_seconds": 15
}
```
**Success `200 OK`:**
```json
{
  "success": true,
  "data": {
    "success": true,
    "resource_name": "database-migration",
    "owner_id": "worker-proc-77",
    "fencing_token": 1,
    "lease_version": 1,
    "acquired_at_unix_nano": 1730000000000000000,
    "expires_at_unix_nano": 1730000015000000000,
    "timestamp_unix_nano": 1730000000000000000
  },
  "timestamp": "2026-11-05T01:30:00Z"
}
```

### `POST /v1/locks/renew`
**Request:**
```json
{
  "request_id": "req-uuid-2",
  "resource_name": "database-migration",
  "owner_id": "worker-proc-77",
  "fencing_token": 1,
  "ttl_seconds": 15
}
```
**Success `200 OK`:** Returns `lease_version: 2` preserving `fencing_token: 1`.

### `POST /v1/locks/release`
**Request:**
```json
{
  "request_id": "req-uuid-3",
  "resource_name": "database-migration",
  "owner_id": "worker-proc-77",
  "fencing_token": 1
}
```

### `POST /v1/locks/expire`
**Request:**
```json
{
  "request_id": "req-uuid-4",
  "resource_name": "database-migration",
  "expected_owner_id": "worker-proc-77",
  "expected_fencing_token": 1,
  "expected_lease_version": 2,
  "expected_expires_at_unix_nano": 1730000015000000000
}
```

### `GET /v1/locks/{resource_name}` & `GET /v1/locks`
Provides linearizable reads guaranteed through `raft.VerifyLeader()`.

### `GET /v1/cluster/status`
Returns live Raft state, active leader ID/address, commit indices, peer topology, and engine statistics.

---

## 6. Implemented Phase 2 Endpoints (API Gateway Port 8080)

The API Gateway provides a centralized public entry point on port 8080 that dynamically discovers the active Raft leader, proxies lock operations with 421 follow redirection, manages client heartbeat sessions, and orchestrates automated lease renewals.

### Client Heartbeat Manager Endpoints

#### `POST /v1/clients/register`
Registers a new client heartbeat session or re-activates an existing session.

**Request:**
```json
{
  "client_id": "worker-client-01",
  "owner_id": "worker-proc-77",
  "ttl_seconds": 30
}
```

**Success `200 OK`:**
```json
{
  "success": true,
  "data": {
    "client_id": "worker-client-01",
    "owner_id": "worker-proc-77",
    "session_id": "9a1b2c3d4e5f...",
    "ttl_seconds": 30,
    "created_at": "2026-11-05T20:30:00Z",
    "last_heartbeat": "2026-11-05T20:30:00Z",
    "expires_at": "2026-11-05T20:30:30Z",
    "active": true,
    "registered_locks": []
  },
  "timestamp": "2026-11-05T20:30:00Z"
}
```

#### `POST /v1/clients/heartbeat`
Extends the active session lease before expiration.
- Requires valid `client_id` and matching secret `session_id`. Mismatches return `403 Forbidden` (`ERR_FORBIDDEN`).

**Request:**
```json
{
  "client_id": "worker-client-01",
  "session_id": "9a1b2c3d4e5f..."
}
```

**Success `200 OK`:** Returns updated `expires_at` and `last_heartbeat`.

#### `DELETE /v1/clients/{clientID}`
Explicitly closes a client session and triggers immediate cancellation of all associated automated renewal workers.
- Requires matching `X-Session-ID` header or request body parameter. Mismatches return `403 Forbidden` (`ERR_FORBIDDEN`).

#### `GET /v1/clients` & `GET /v1/clients/{clientID}`
Lists active and expired sessions and inspects individual session telemetry.
- **Credential Redaction:** The secret `session_id` is automatically redacted (`"[REDACTED]"`) in responses unless the caller supplies the matching `X-Session-ID` authorization header.

---

### Automated Lease Renewal Manager Endpoints

#### `POST /v1/renewal/start`
Launches an autonomous background renewal worker for an active lock. The worker dynamically calculates renewal deadlines ($T_{\text{renew}} = \text{ExpiresAt} - \text{TTL}/3 - \text{SafetyMargin}$) and submits renewals through the leader with idempotency preservation.
- **Dual-Layer Authorization:**
  1. Validates that the supplied `session_id` exists, is active, and matches `owner_id` (`403 Forbidden` on mismatch).
  2. Queries authoritative Raft leader (`GetLock`) to verify the lock is held by `owner_id`, matches the active `fencing_token`, has not expired, and `lease_version` is not superseded (`403 Forbidden`, `404 Not Found`, or `409 Conflict`).
- Stale or superseded requests are rejected without overwriting active newer generation workers.

**Request:**
```json
{
  "resource_name": "database-migration",
  "owner_id": "worker-proc-77",
  "client_id": "worker-client-01",
  "session_id": "9a1b2c3d4e5f...",
  "fencing_token": 1001,
  "lease_version": 1,
  "expires_at_unix_nano": 1730000015000000000,
  "ttl_seconds": 15
}
```

#### `POST /v1/renewal/stop`
Stops the renewal worker for a specified resource.
- Requires `session_id` in request body or `X-Session-ID` header matching the active worker. Mismatches or omitted credentials return `403 Forbidden` (`ERR_FORBIDDEN`).

**Request:**
```json
{
  "resource_name": "database-migration",
  "session_id": "9a1b2c3d4e5f..."
}
```

#### `GET /v1/renewal/status`
Returns real-time worker execution telemetry (next renewal deadline, last attempt status, attempt counts, errors).

---

### Prometheus Observability Endpoint

#### `GET /metrics`
Emits Prometheus exposition format (`text/plain; version=0.0.4; charset=utf-8`) metrics:
- `lockguard_gateway_requests_total{status, method, path}`
- `lockguard_gateway_leader_resolutions_total{status}`
- `lockguard_active_client_sessions`
- `lockguard_heartbeat_received_total{status}`
- `lockguard_heartbeat_timeouts_total`
- `lockguard_renewal_attempts_total{status}`
- `lockguard_renewals_active`
- `lockguard_fencing_token_current`
- `lockguard_audit_events_created_total{event_type, status}`
- `lockguard_audit_events_persisted_total{status}`
- `lockguard_audit_events_failed_total{reason}`
- `lockguard_audit_queue_size`
- `lockguard_audit_delivery_latency_seconds`
- `lockguard_database_connections`
- `lockguard_database_health`
- `lockguard_database_query_errors_total`
- `lockguard_database_migrations_status`

---

### Phase 3 Durable Audit Query API Endpoints

All audit routes are accessible directly or with the `/api/v1` prefix.

#### `GET /v1/audit/events`
Queries paginated audit events with rich filtering criteria.

**Query Parameters:**
- `event_type` (string): Filter by event type (e.g. `LOCK_ACQUIRE`, `LOCK_RENEW`, `CLIENT_REGISTER`)
- `event_category` (string): Filter by category (`LOCK_OPERATION`, `SESSION_LIFECYCLE`, etc.)
- `resource_id` (string): Filter by locked resource
- `owner_id` (string): Filter by lock owner
- `client_id` (string): Filter by client identifier
- `request_id` (string): Trace specific operation Request ID
- `operation_status` (string): Filter by `COMMITTED`, `REJECTED`, or `FAILED`
- `start_time` / `end_time` (RFC3339 timestamp): Time-range filter
- `limit` (int, default 50, max 200): Page size
- `offset` (int, default 0): Pagination offset

**Response `200 OK`:**
```json
{
  "success": true,
  "data": {
    "events": [
      {
        "id": 104,
        "event_id": "a1f9e2b8c743...",
        "event_type": "LOCK_ACQUIRE",
        "event_category": "LOCK_OPERATION",
        "request_id": "req-99",
        "resource_id": "order-processor",
        "owner_id": "worker-1",
        "client_id": "client-1",
        "fencing_token": 1045,
        "lease_version": 1,
        "operation_status": "COMMITTED",
        "coordinator_id": "coordinator-a",
        "raft_index": 128,
        "event_timestamp": "2026-11-05T22:00:00Z",
        "recorded_at": "2026-11-05T22:00:00.050Z",
        "metadata": {
          "status_code": 200
        }
      }
    ],
    "total": 1,
    "limit": 50,
    "offset": 0
  },
  "timestamp": "2026-11-05T22:00:01Z"
}
```

#### `GET /v1/audit/events/{event_id}`
Retrieves a single audit record by its unique `event_id`. Returns `404 Not Found` if missing.

#### `GET /v1/audit/resources/{resource_id}`
Shortcut endpoint returning paginated events specific to a named resource.

#### `GET /v1/audit/clients/{client_id}`
Shortcut endpoint returning paginated events specific to a client ID.

#### `GET /v1/audit/health`
Returns live audit delivery pipeline metrics:
```json
{
  "success": true,
  "data": {
    "status": "UP",
    "subsystem": "audit_pipeline",
    "stats": {
      "queue_size": 0,
      "queue_capacity": 10000,
      "created_total": 542,
      "persisted_total": 542,
      "failed_total": 0
    },
    "db_attached": true
  },
  "timestamp": "2026-11-05T22:00:00Z"
}
```

#### `GET /v1/recovery/diagnostics`
Comprehensive multi-component operational diagnostics:
```json
{
  "success": true,
  "data": {
    "overall_status": "HEALTHY",
    "node_id": "coordinator-a",
    "service_name": "lockguard-api",
    "raft_quorum_available": true,
    "leader_available": true,
    "database_available": true,
    "subsystems": {
      "raft_consensus": {
        "name": "raft_consensus",
        "status": "HEALTHY",
        "message": "active leader reachable: http://coordinator-a:8001"
      },
      "database_persistence": {
        "name": "database_persistence",
        "status": "HEALTHY",
        "message": "postgresql connection pool healthy"
      },
      "schema_migrations": {
        "name": "schema_migrations",
        "status": "HEALTHY",
        "message": "all schema migrations applied"
      },
      "audit_pipeline": {
        "name": "audit_pipeline",
        "status": "HEALTHY",
        "message": "audit delivery pipeline operating normally"
      }
    }
  },
  "timestamp": "2026-11-05T22:00:00Z"
}
```
