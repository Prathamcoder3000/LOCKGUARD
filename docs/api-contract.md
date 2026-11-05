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
| `ERR_NOT_LEADER` | 503 | Node is not authoritative leader (redirect hint provided). |
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

## 5. Planned Distributed Lock Endpoints (Phase 1 & Phase 2)

### `POST /api/v1/locks/acquire`
**Request:**
```json
{
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
    "resource_name": "database-migration",
    "owner_id": "worker-proc-77",
    "fencing_token": 1001,
    "lease_ttl_seconds": 15,
    "acquired_at": "2026-11-05T01:30:00Z",
    "expires_at": "2026-11-05T01:30:15Z"
  }
}
```

---

### `POST /api/v1/locks/renew`
**Request:**
```json
{
  "resource_name": "database-migration",
  "owner_id": "worker-proc-77",
  "fencing_token": 1001,
  "ttl_seconds": 15
}
```

---

### `POST /api/v1/locks/release`
**Request:**
```json
{
  "resource_name": "database-migration",
  "owner_id": "worker-proc-77",
  "fencing_token": 1001
}
```
