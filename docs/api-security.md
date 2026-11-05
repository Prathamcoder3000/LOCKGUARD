# LockGuard API Security & Defense-in-Depth Specification

**Document Version:** 1.0.0  
**Phase:** 5 (Security Hardening, Performance Optimization & Production Readiness)  
**Classification:** API Security Engineering Guide  
**System Title:** Fault Tolerant Distributed Lock Service (LOCKGUARD)

---

## 1. Scope & Objectives

The LockGuard API Gateway is the central ingress for distributed lock clients, monitoring agents, and administrative operators. This specification details the API hardening measures implemented across middleware, request validation, headers, and rate limiting to prevent common attack vectors including DoS, payload exhaustion, content sniffing, and brute-force attacks.

---

## 2. Production Security Headers

The `SecurityHeaders` middleware (`backend/internal/middleware/security.go`) injects the following security headers on all HTTP responses:

```http
Strict-Transport-Security: max-age=31536000; includeSubDomains
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
X-XSS-Protection: 1; mode=block
Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'
Referrer-Policy: strict-origin-when-cross-origin
```

### Purpose of Headers:
1. **`Strict-Transport-Security` (HSTS):** Enforces HTTPS communication for one full year, preventing man-in-the-middle downgrades.
2. **`X-Content-Type-Options: nosniff`:** Prevents browsers and HTTP clients from MIME-sniffing responses away from declared Content-Types.
3. **`X-Frame-Options: DENY`:** Protects administrative dashboard interfaces against UI clickjacking by prohibiting iframe rendering.
4. **`Content-Security-Policy` (CSP):** Restricts script, stylesheet, and connection origins strictly to the origin server.

---

## 3. Request Body Size Limiting (DoS Mitigation)

Unbounded HTTP request body consumption is a primary vector for memory exhaustion and Denial-of-Service (DoS). LockGuard implements a multi-layer body size limiter:

### 3.1 Implementation Details
- **Default Cap:** $1 \text{ MB} = 1,048,576 \text{ bytes}$.
- **Pre-check via `Content-Length`:** If the `Content-Length` header exceeds 1MB, the request is aborted immediately before reading the socket.
- **Stream-capping via `http.MaxBytesReader`:** Wraps `r.Body`. If chunked or unstated request streaming attempts to pass more than 1MB, the reader fails with `*http.MaxBytesError`.
- **Response Format:**
  ```json
  {
    "success": false,
    "error": {
      "code": "ERR_PAYLOAD_TOO_LARGE",
      "message": "request body exceeds maximum allowed size (1MB)"
    }
  }
  ```
  HTTP Status: `413 Payload Too Large`.

---

## 4. Content-Type Validation

Mutating HTTP requests (`POST`, `PUT`, `PATCH`) are verified by `ValidateContentType` middleware:
- If a client supplies a non-empty `Content-Type` header that is not `application/json` (e.g., `text/plain`, `multipart/form-data`, `application/x-www-form-urlencoded`), the request is rejected immediately with HTTP 415.
- **Response Format:**
  ```json
  {
    "success": false,
    "error": {
      "code": "ERR_UNSUPPORTED_MEDIA_TYPE",
      "message": "invalid Content-Type: only application/json is accepted for mutating operations"
    }
  }
  ```
- In development/testing environments, bare test requests without explicit content types are accepted for backward compatibility.

---

## 5. Token-Bucket Rate Limiter with Bounded Memory

To prevent connection flooding and API brute-forcing, LockGuard provides an in-memory token-bucket rate limiter (`backend/internal/middleware/ratelimit.go`).

### 5.1 Limiter Architecture
- **Algorithm:** Classic Token Bucket.
- **Parameters:**
  - `rate`: Tokens replenished per second.
  - `capacity`: Maximum burst size.
  - `maxClients`: Hard cap on tracked client IP/token keys (default: 10,000) to prevent unbounded memory growth from spoofed client IPs.
- **TTL Eviction:** Active tracking entries idle for greater than 5 minutes are evicted during periodic sweeps.
- **Breach Response:**
  - Status Code: `429 Too Many Requests`
  - Header: `Retry-After: <seconds>`
  - Body:
    ```json
    {
      "success": false,
      "error": {
        "code": "ERR_RATE_LIMITED",
        "message": "too many requests: rate limit exceeded, please retry later",
        "retry_after": 2
      }
    }
    ```
  - Metric: `lockguard_rate_limit_exceeded_total{endpoint="..."}` incremented.

---

## 6. Centralized Error Envelope & Sanitization

All error responses adhere to the standard LockGuard error envelope:

```json
{
  "success": false,
  "error": {
    "code": "ERR_...",
    "message": "Human-readable sanitized message",
    "details": null
  },
  "timestamp": "2026-11-06T01:00:00.000000Z"
}
```

### Standard Error Code Registry:
- `ERR_UNAUTHORIZED` (401): Missing, malformed, expired, or invalid JWT token.
- `ERR_FORBIDDEN` (403): Insufficient role permissions for route.
- `ERR_OWNER_MISMATCH` (403): Authenticated client subject does not match payload `owner_id`.
- `ERR_PAYLOAD_TOO_LARGE` (413): Request body exceeds 1MB limit.
- `ERR_UNSUPPORTED_MEDIA_TYPE` (415): Non-JSON Content-Type on mutating endpoint.
- `ERR_RATE_LIMITED` (429): Rate limit exceeded.
- `ERR_NOT_LEADER` (307/503): Request directed to follower when leader proxying fails.
- `ERR_QUORUM_UNAVAILABLE` (503): Raft cluster has lost majority ($Q < 2$).
- `ERR_FENCING_TOKEN_MISMATCH` (412): Fencing token stale or mismatch.
- `ERR_LOCK_HELD` (409): Lock currently acquired by another owner.
- `ERR_INTERNAL` (500): Unexpected internal server error (internal stack traces redacted from client).

