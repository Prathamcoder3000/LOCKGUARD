# LockGuard Security Architecture & Hardening Specification

**Document Version:** 1.0.0  
**Phase:** 5 (Security Hardening, Performance Optimization & Production Readiness)  
**Classification:** Technical Architecture & Security Specification  
**System Title:** Fault Tolerant Distributed Lock Service (LOCKGUARD)

---

## 1. Executive Summary & Security Objectives

LockGuard provides linearizable, deterministic distributed lock coordination with fencing tokens. In distributed environments where untrusted clients, network adversaries, or buggy actors might attempt unauthorized mutations, impersonate worker identities, tamper with session tokens, or induce denial-of-service conditions, the system must enforce strict security boundaries.

### Core Security Objectives
1. **Zero-Trust Identity Verification:** All clients, operators, and auditors interacting with the gateway must present cryptographically verifiable credentials (HMAC-SHA256 JWT tokens).
2. **Strict RBAC Enforcement:** Operations are partitioned across `ADMIN`, `CLIENT`, and `READ_ONLY` roles. Mutating operations are rejected for read-only identities.
3. **Cross-Client Ownership Isolation:** A client authenticated as identity $A$ cannot acquire, renew, or release locks belonging to identity $B$.
4. **Transport Layer Security (mTLS):** Inter-coordinator Raft consensus communication supports mutual TLS (TLS 1.3) with client certificate verification, preventing eavesdropping and rogue node admission.
5. **Fail-Closed Consensus & Monotonic Fencing:** Security hardening preserves the core invariant that Raft quorum ($N=3, Q=2$) is the sole lock authority, with strictly monotonic fencing tokens.

---

## 2. Threat Model & Adversary Capabilities

LockGuard models the following adversary categories:

| Threat Category | Adversary Profile | Mitigating Mechanism |
|---|---|---|
| **Identity Spoofing** | Rogue client submits lock mutation with someone else's `owner_id`. | `EnforceOwnerIsolation` verifies authenticated JWT `Subject` matches requested `owner_id`. Rejects with 403 `ERR_OWNER_MISMATCH`. |
| **Token Tampering / Forgery** | Attacker modifies JWT payload, claims expiration, or changes `alg` to `"none"`. | Zero-external-dependency HS256 validator with constant-time HMAC comparison and strict algorithm verification. Rejects with 401 `ERR_UNAUTHORIZED`. |
| **Privilege Escalation** | `READ_ONLY` client attempts lock mutations; normal `CLIENT` attempts cluster reconfiguration. | Route-level RBAC filters (`RequireRole`). Rejects with 403 `ERR_FORBIDDEN`. |
| **Payload Flooding / OOM DOS** | Malicious client posts multi-megabyte payloads to consume memory. | `MaxBodySize` enforces a strict 1MB limit (`http.MaxBytesReader` and Content-Length pre-check). Rejects with 413 `ERR_PAYLOAD_TOO_LARGE`. |
| **Endpoint Hammering / Resource Exhaustion** | Client floods endpoints to exhaust connection pools or Raft buffers. | Token-bucket rate limiter with bounded memory tracking (`maxClients: 10000`, 5-min TTL). Rejects with 429 `ERR_RATE_LIMITED`. |
| **Consensus Interception / Rogue Coordinator** | Eavesdropper monitors peer Raft traffic or injects malicious log entries. | Raft peer TLS 1.3 `StreamLayer` with mutual client certificate validation (`RequireAndVerifyClientCert`). |
| **Credential Leakage in Logs** | Operators inspect logs where config dumps or connection strings are printed. | `fmt.Stringer` implementations on `Config`, `DBConfig`, and `AuthConfig` mask all secrets as `[REDACTED]`. |

---

## 3. Cryptographic Authentication: Zero-Dependency HS256 JWT

LockGuard implements standard RFC 7519 HMAC-SHA256 (HS256) token generation and verification using Go's standard library (`crypto/hmac`, `crypto/sha256`, `encoding/base64`, `encoding/json`):

### 3.1 Token Structure
A JWT token consists of three Base64RawURL-encoded components separated by periods:
$$\text{Token} = \text{B64URL}(\text{Header}) \,.\, \text{B64URL}(\text{Claims}) \,.\, \text{B64URL}(\text{Signature})$$

- **Header:** `{"alg": "HS256", "typ": "JWT"}`
- **Claims Schema:**
  - `sub`: Authenticated Subject (Bound `OwnerID` for clients, or user ID for admins).
  - `name`: Human-readable username.
  - `role`: Role string (`ADMIN`, `CLIENT`, or `READ_ONLY`).
  - `iss`: `"lockguard"`
  - `aud`: `"lockguard-api"`
  - `iat`: Unix timestamp of issuance.
  - `exp`: Unix timestamp of expiration.

### 3.2 Security Defenses Implemented in `backend/internal/auth/jwt.go`
1. **Strict Algorithm Enforcement:** The parser strictly decodes the header JSON and validates `header.Algorithm == "HS256"`. Tokens specifying `"none"`, asymmetric keys (`"RS256"`, `"ES256"`), or empty algorithms are rejected immediately with `ErrInvalidAlgorithm`.
2. **Constant-Time Verification:** Signatures are evaluated using `hmac.Equal(expectedSig, givenSig)` to prevent side-channel timing attacks.
3. **Clock Skew Toleration:** An allowance of 60 seconds is provided for clock drift, but expired tokens are rejected with `ErrExpiredToken`.

---

## 4. Role-Based Access Control (RBAC) Matrix

LockGuard partitions API endpoints into three principal roles:

| API Route | Allowed Roles | Mutating? | Description |
|---|---|---|---|
| `POST /v1/auth/login` | Public | Yes | Issues signed JWT token upon valid credentials. |
| `GET /v1/health`, `GET /v1/ready` | Public / All | No | Liveness and readiness probes. |
| `GET /metrics` | Public / All | No | Prometheus telemetry metrics. |
| `GET /v1/cluster/status` | `ADMIN`, `CLIENT`, `READ_ONLY` | No | Authoritative cluster topology status. |
| `GET /v1/locks`, `GET /v1/locks/{name}` | `ADMIN`, `CLIENT`, `READ_ONLY` | No | Linearizable barrier reads of active leases. |
| `POST /v1/locks/acquire` | `ADMIN`, `CLIENT` | Yes | Acquires distributed lock with monotonic fencing token. |
| `POST /v1/locks/renew` | `ADMIN`, `CLIENT` | Yes | Renews lease with exact version increment (+1). |
| `POST /v1/locks/release` | `ADMIN`, `CLIENT` | Yes | Releases active lock matching fencing token. |
| `POST /v1/clients/register` | `ADMIN`, `CLIENT` | Yes | Establishes client heartbeat session. |
| `POST /v1/clients/heartbeat` | `ADMIN`, `CLIENT` | Yes | Refreshes client heartbeat session. |
| `POST /v1/renewal/start` | `ADMIN`, `CLIENT` | Yes | Spawns background automatic renewal worker. |
| `POST /v1/renewal/stop` | `ADMIN`, `CLIENT` | Yes | Cancels renewal worker for specific resource. |
| `GET /v1/admin/cluster/nodes` | `ADMIN` only | No | Administrative cluster membership and configuration. |

---

## 5. Client Isolation & Cross-Client Access Prevention

Under multi-tenant or multi-worker deployments, Client A must never be able to renew or release Client B's lock, nor acquire a lock under Client B's identity.

### Enforcement Mechanism:
In `backend/internal/api/server.go` (`handleLockOperation`) and `backend/internal/auth/middleware.go` (`EnforceOwnerIsolation`):
1. The incoming JWT claims are extracted and validated.
2. For any mutating operation (`POST`, `PUT`, `DELETE`), if `claims.Role == RoleClient`:
   - The request body is inspected for `owner_id`.
   - If `payload.OwnerID != claims.Subject`:
     - The metric `lockguard_security_violations_total{violation_type="cross_client_lock_access"}` is incremented.
     - The metric `lockguard_authz_denials_total{role="client"}` is incremented.
     - The request is immediately rejected with HTTP 403 Forbidden and error code `ERR_OWNER_MISMATCH`.
3. Downstream Raft coordinators also verify fencing tokens and lease owners, providing dual-layer defense.

---

## 6. Seeded Operational Identities

For development, testing, and production bootstrapping, LockGuard provides seeded identities:

| Username | Default Password | Role | Bound OwnerID | Purpose |
|---|---|---|---|---|
| `admin` | `admin_secure_pass_phase5` | `ADMIN` | `admin-system` | Full cluster management and operations. |
| `client_worker` | `worker_secure_pass_phase5` | `CLIENT` | `worker-1` | Primary distributed lock worker client. |
| `client_worker_2` | `worker_secure_pass_phase5` | `CLIENT` | `worker-2` | Secondary distributed lock worker client. |
| `auditor` | `auditor_secure_pass_phase5` | `READ_ONLY` | `auditor-01` | Compliance inspection, audit queries, metrics. |

*In production environments, credentials are passed via secure environment variables or vault integrations, and validated against minimum entropy rules during server startup.*

