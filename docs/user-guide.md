# LockGuard Operator & User Guide

## 1. Introduction

LockGuard coordinates distributed operations across microservices and cluster workers. This guide outlines how operators monitor the cluster using the web dashboard and how client applications safely consume distributed locks.

---

## 2. Accessing the LockGuard Dashboard

1. Start the cluster via Docker Compose or run the local frontend (`npm run dev`).
2. Navigate to `http://localhost:3000` (or `http://localhost` if routed via Nginx).
3. Sign in using the default administrative credentials:
   - **Username:** `cluster-admin`
   - **Password:** `password123`
4. The **Cluster Overview** provides real-time health telemetry:
   - **Cluster Quorum Indicator:** Displays 2 of 3 node quorum status.
   - **Core Invariant Card:** Enforces `CONSISTENCY FIRST — FAIL CLOSED`.
   - **API Gateway Probe:** Actively monitors the `/health` and `/ready` probes.
   - **Live Telemetry JSON:** Displays the real backend payload from `/api/v1/cluster/status`.

---

## 3. Client Application Integration Pattern

To safely mutate shared resources in distributed systems, clients must follow the **Lease + Fencing Protocol**:

### Step 1: Acquire Exclusive Lock
Send a `POST` request to `/api/v1/locks/acquire`:
```bash
curl -X POST http://localhost:8080/api/v1/locks/acquire \
  -H "Content-Type: application/json" \
  -d '{
    "resource_name": "customer-order-9921",
    "owner_id": "worker-instance-04",
    "ttl_seconds": 10
  }'
```
Response provides a lease and a fencing token (e.g. `1045`).

### Step 2: Perform Work with Active Renewal
While processing the operation, the client sends periodic heartbeats to `/api/v1/locks/renew` every $\approx \frac{\text{TTL}}{3}$ seconds (e.g., every 3 seconds for a 10s lease).

### Step 3: Mutate Protected Resource
When sending writes to the shared database or storage service, attach the fencing token:
```bash
curl -X POST http://localhost:9000/api/v1/write \
  -H "Content-Type: application/json" \
  -d '{
    "resource_name": "customer-order-9921",
    "fencing_token": 1045,
    "payload": "Order marked fulfilled"
  }'
```
If an asynchronous pause occurred and another client was issued token `1046`, this write is safely rejected with `409 Conflict`, preserving data integrity.

### Step 4: Release Lock
Upon completion, explicitly release the lease:
```bash
curl -X POST http://localhost:8080/api/v1/locks/release \
  -H "Content-Type: application/json" \
  -d '{
    "resource_name": "customer-order-9921",
    "owner_id": "worker-instance-04",
    "fencing_token": 1045
  }'
```

---

## 4. Troubleshooting & Operational Statuses

| Symptom | Cause | Remediation |
| :--- | :--- | :--- |
| `ERR_NOT_LEADER` | Request sent to a follower coordinator node. | Read the `leader_address` hint in the response and redirect requests directly to the authoritative leader. |
| `ERR_QUORUM_UNAVAILABLE` | 2 or more coordinator nodes are unreachable. | The cluster has failed closed. Restore connectivity to at least 2 nodes to re-establish quorum. |
| `ERR_FENCING_TOKEN_STALE` | Lock lease lapsed and another client acquired the resource. | Discard local state and re-acquire a fresh lock. |
