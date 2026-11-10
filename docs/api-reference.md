# LOCKGUARD — REST API Reference Specification

## Academic Project Title: Fault Tolerant Distributed Lock Service
**System Version:** Phase 6 Final Release  
**Status:** Canonical API Specification  

---

## 1. Overview & Protocol Conventions

LOCKGUARD exposes a RESTful JSON HTTP interface. Operations that modify distributed state are transparently routed through the active Raft leader and replicated across the quorum before confirmation.

### Base URLs
- **API Gateway (Edge ingress):** `http://localhost:8080` (or `https://<domain>:8080`)
- **Direct Coordinator Node 1:** `http://localhost:8001`
- **Direct Coordinator Node 2:** `http://localhost:8002`
- **Direct Coordinator Node 3:** `http://localhost:8003`
- **Protected Resource Service:** `http://localhost:9000`

### Standard Request Headers
| Header | Description | Required |
| :--- | :--- | :--- |
| `Content-Type` | Must be `application/json; charset=utf-8` for POST/PUT | Yes (for body payloads) |
| `Authorization` | Bearer token format: `Bearer <jwt_token>` | Yes (for protected endpoints) |
| `X-Request-ID` | Unique UUID/string for end-to-end request tracing | Optional (Auto-generated if absent) |

### Standard Response Envelope
All success and error responses follow a uniform structured format:
```json
{
  "success": true,
  "data": { ... },
  "timestamp": "2026-11-06T02:30:00.000000Z"
}
```
Error Envelope:
```json
{
  "success": false,
  "error": {
    "code": "ERR_LOCK_ALREADY_HELD",
    "message": "resource is currently locked by another session",
    "details": null
  },
  "timestamp": "2026-11-06T02:30:00.000000Z"
}
```

---

## 2. Authentication & Identity Endpoints

### 2.1 Authenticate / Obtain JWT Token
- **Endpoint:** `POST /v1/auth/login` (also `/api/v1/auth/login`)
- **Auth Required:** No
- **Rate Limit:** 10 req/sec (Burst: 20)

**Request Body:**
```json
{
  "username": "admin",
  "password": "admin_secure_pass_phase5"
}
```

**Response (HTTP 200 OK):**
```json
{
  "success": true,
  "data": {
    "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "expires_at": "2026-11-07T02:30:00Z",
    "role": "admin",
    "owner_id": "admin-system"
  },
  "timestamp": "2026-11-06T02:30:00Z"
}
```

---

## 3. Distributed Lock Lifecycle Endpoints

### 3.1 Acquire Distributed Lock
- **Endpoint:** `POST /v1/locks/acquire` (also `/api/v1/locks/acquire`)
- **Auth Required:** Yes (Role: `admin` or `client`)
- **Quorum Requirement:** Raft Leader Majority Replicated

**Request Body:**
```json
{
  "request_id": "req-9901-acq",
  "resource_name": "db-migration-prod",
  "owner_id": "worker-node-42",
  "ttl_seconds": 15
}
```

**Response (HTTP 200 OK):**
```json
{
  "success": true,
  "data": {
    "resource_name": "db-migration-prod",
    "owner_id": "worker-node-42",
    "fencing_token": 104,
    "lease_version": 1,
    "ttl_nanoseconds": 15000000000,
    "granted_at_unix_nano": 1793932200000000000,
    "expires_at_unix_nano": 1793932215000000000
  },
  "timestamp": "2026-11-06T02:30:00Z"
}
```

**Conflict Response (HTTP 409 Conflict):**
```json
{
  "success": false,
  "error": {
    "code": "ERR_LOCK_ALREADY_HELD",
    "message": "resource db-migration-prod is already locked by owner worker-node-10"
  }
}
```

---

### 3.2 Renew Distributed Lock Lease
- **Endpoint:** `POST /v1/locks/renew` (also `/api/v1/locks/renew`)
- **Auth Required:** Yes (Role: `admin` or `client`)
- **Rule:** Monotonic lease version must match exactly (`lease_version == current + 1`).

**Request Body:**
```json
{
  "request_id": "req-9902-ren",
  "resource_name": "db-migration-prod",
  "owner_id": "worker-node-42",
  "fencing_token": 104,
  "ttl_seconds": 15
}
```

**Response (HTTP 200 OK):**
```json
{
  "success": true,
  "data": {
    "resource_name": "db-migration-prod",
    "owner_id": "worker-node-42",
    "fencing_token": 104,
    "lease_version": 2,
    "ttl_nanoseconds": 15000000000,
    "granted_at_unix_nano": 1793932200000000000,
    "expires_at_unix_nano": 1793932230000000000
  },
  "timestamp": "2026-11-06T02:30:07Z"
}
```

**Mismatched Token / Version Error (HTTP 412 Precondition Failed):**
```json
{
  "success": false,
  "error": {
    "code": "ERR_FENCING_TOKEN_MISMATCH",
    "message": "submitted fencing token does not match active lock lease"
  }
}
```

---

### 3.3 Explicitly Release Lock
- **Endpoint:** `POST /v1/locks/release` (also `/api/v1/locks/release`)
- **Auth Required:** Yes (Role: `admin` or `client`)

**Request Body:**
```json
{
  "request_id": "req-9903-rel",
  "resource_name": "db-migration-prod",
  "owner_id": "worker-node-42",
  "fencing_token": 104
}
```

**Response (HTTP 200 OK):**
```json
{
  "success": true,
  "data": {
    "resource_name": "db-migration-prod",
    "released": true,
    "released_at_unix_nano": 1793932212000000000
  },
  "timestamp": "2026-11-06T02:30:12Z"
}
```

---

### 3.4 Query Active Locks
- **Endpoint:** `GET /v1/locks` (also `/api/v1/locks`)
- **Auth Required:** Yes (Role: `admin`, `client`, or `read-only`)

**Response (HTTP 200 OK):**
```json
{
  "success": true,
  "data": {
    "locks": [
      {
        "resource": "db-migration-prod",
        "owner_id": "worker-node-42",
        "fencing_token": 104,
        "lease_version": 2,
        "expires_at": "2026-11-06T02:30:30Z",
        "ttl_remaining_ms": 13400
      }
    ],
    "count": 1
  }
}
```

---

## 4. Client Session & Heartbeat Endpoints

### 4.1 Register Client Session
- **Endpoint:** `POST /v1/clients/register`
- **Request Body:**
```json
{
  "owner_id": "worker-node-42",
  "session_ttl_seconds": 30
}
```
- **Response (HTTP 200 OK):**
```json
{
  "success": true,
  "data": {
    "session_id": "sess-a810f2c4-91b7",
    "owner_id": "worker-node-42",
    "heartbeat_interval_seconds": 10,
    "expires_at": "2026-11-06T02:30:30Z"
  }
}
```

### 4.2 Submit Session Heartbeat
- **Endpoint:** `POST /v1/clients/heartbeat`
- **Request Body:**
```json
{
  "session_id": "sess-a810f2c4-91b7",
  "owner_id": "worker-node-42"
}
```
- **Response (HTTP 200 OK):**
```json
{
  "success": true,
  "data": {
    "refreshed": true,
    "expires_at": "2026-11-06T02:31:00Z"
  }
}
```

---

## 5. Automated Lease Renewal Endpoints

### 5.1 Start Automated Renewal Loop
- **Endpoint:** `POST /v1/renewal/start`
- **Request Body:**
```json
{
  "resource_name": "db-migration-prod",
  "owner_id": "worker-node-42",
  "session_id": "sess-a810f2c4-91b7",
  "fencing_token": 104,
  "ttl_seconds": 15
}
```
- **Response (HTTP 200 OK):**
```json
{
  "success": true,
  "data": {
    "worker_id": "renew-worker-db-migration-prod",
    "status": "RUNNING",
    "renewal_interval_ms": 7500
  }
}
```

### 5.2 Stop Automated Renewal Loop
- **Endpoint:** `POST /v1/renewal/stop`
- **Request Body:**
```json
{
  "resource_name": "db-migration-prod",
  "owner_id": "worker-node-42"
}
```
- **Response (HTTP 200 OK):**
```json
{
  "success": true,
  "data": {
    "worker_id": "renew-worker-db-migration-prod",
    "status": "STOPPED"
  }
}
```

---

## 6. Audit & Operational Diagnostics Endpoints

### 6.1 Query Audit Event Log
- **Endpoint:** `GET /v1/audit/events?resource=db-migration-prod&limit=10`
- **Auth Required:** Yes (Role: `admin` or `read-only`)
- **Response (HTTP 200 OK):**
```json
{
  "success": true,
  "data": {
    "events": [
      {
        "id": "evt-771",
        "resource": "db-migration-prod",
        "action": "ACQUIRE",
        "owner_id": "worker-node-42",
        "fencing_token": 104,
        "lease_version": 1,
        "node_id": "node-1",
        "created_at": "2026-11-06T02:30:00Z"
      }
    ],
    "count": 1
  }
}
```

### 6.2 System Diagnostics & Health Status
- **Endpoint:** `GET /v1/diagnostics/report`
- **Response (HTTP 200 OK):** Comprehensive report verifying Raft cluster health, PostgreSQL connectivity, unwritten audit buffer count, and memory allocation.

---

## 7. Protected Resource Reference Service

### 7.1 Fenced Write Operation
- **Endpoint:** `POST http://localhost:9000/api/v1/write`
- **Purpose:** Demonstrates server-side validation of monotonic fencing tokens.

**Request Body:**
```json
{
  "resource_name": "db-migration-prod",
  "fencing_token": 104,
  "owner_id": "worker-node-42",
  "payload": "UPDATE users SET state='migrated';"
}
```

**Success Response (HTTP 200 OK):**
```json
{
  "status": "success",
  "resource_name": "db-migration-prod",
  "applied_token": 104,
  "message": "mutation applied safely"
}
```

**Stale Token Rejection (HTTP 409 Conflict):**
```json
{
  "status": "conflict",
  "error": "stale fencing token rejected: token 102 <= highest seen 104",
  "highest_seen_token": 104
}
```

---

## 8. Telemetry & Observability Endpoints

| Path | Method | Description |
| :--- | :--- | :--- |
| `/health` | GET | Liveness probe returning HTTP 200 when process is running |
| `/ready` | GET | Readiness probe returning HTTP 200 only when leader is discovered and operational |
| `/metrics` | GET | Prometheus exposition text format containing gauges, counters, and histograms |
| `/v1/cluster/status` | GET | Cluster topology, leader node ID, active term, and quorum status |
| `/v1/admin/cluster/nodes` | GET | (Admin only) Detailed status and network latency for all Raft nodes |

