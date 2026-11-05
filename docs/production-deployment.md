# LockGuard Production Deployment & Hardening Runbook

**Document Version:** 1.0.0  
**Phase:** 5 (Security Hardening, Performance Optimization & Production Readiness)  
**Classification:** DevOps & Production Operations Runbook  
**System Title:** Fault Tolerant Distributed Lock Service (LOCKGUARD)

---

## 1. Production Architecture Overview

In a production environment, LockGuard deploys as a hardened multi-container topology across segmented networks:

```
[ Ingress / Clients ]
        │  (HTTPS / JWT Bearer)
        ▼
[ API Gateway (Port 8080) ]
        │
   ┌────┴──────────────────────────┐  (Private Raft Network)
   ▼                               ▼
[ Coordinator A ] ──mTLS 1.3── [ Coordinator B ] ──mTLS 1.3── [ Coordinator C ]
 (Port 8001/7000)               (Port 8002/7000)               (Port 8003/7000)
        │
        ▼ (Async Audit Queue)
[ PostgreSQL Database (Port 5432) ]
```

---

## 2. Container Security & Least-Privilege Principles

Every container in the LockGuard deployment enforces the following security standards:
1. **Non-Root Execution:** Containers run under dedicated unprivileged system users (`USER lockguard:lockguard`, UID 1000).
2. **Read-Only Root Filesystems:** Storage write operations are strictly restricted to isolated persistent volumes (`/var/lib/lockguard/data`).
3. **No New Privileges:** Docker Compose specifies `security_opt: [no-new-privileges:true]`.
4. **Network Segmentation:**
   - `lockguard-internal`: For Gateway $\leftrightarrow$ Coordinator API communication.
   - `lockguard-raft`: Private isolated bridge network for Raft peer consensus on port 7000 (never bound to host ports).

---

## 3. Production Deployment Checklist

Before deploying LockGuard in a production cluster:

1. **Environment Configuration:**
   - Copy `.env.example` to `.env`.
   - Set `ENVIRONMENT=production`.
   - Generate a high-entropy `JWT_SECRET_KEY` (minimum 32 random characters):
     ```bash
     openssl rand -hex 32
     ```
   - Provide strong database credentials (`DB_PASSWORD`) and set `DB_SSL_MODE=require` or `verify-full`.
2. **mTLS Peer Consensus Setup:**
   - Generate cluster CA and coordinator certificates as documented in `docs/tls-and-certificates.md`.
   - Set `RAFT_ENABLE_TLS=true`, `RAFT_TLS_CERT_FILE`, `RAFT_TLS_KEY_FILE`, `RAFT_TLS_CA_FILE`.
3. **Storage & Volumes:**
   - Ensure coordinator data directories (`/var/lib/lockguard/data`) are mounted on fast SSD or NVMe volumes with reliable write-cache flushing (`fsync`).
4. **Monitoring & Alerts:**
   - Configure Prometheus to scrape `/metrics` on port 9090.
   - Set up alerts for:
     - `lockguard_cluster_quorum_status < 1` (Quorum lost!).
     - `lockguard_security_violations_total > 0` (Attacks or cross-client impersonations detected).
     - `lockguard_rate_limit_exceeded_total` spikes (DoS or rogue client hammering).

---

## 4. Operational Runbook & Health Commands

### Checking Service Health
```bash
# Gateway Liveness Probe
curl -f http://localhost:8080/health

# Cluster Consensus Readiness & Leader Status
curl -f http://localhost:8080/ready
```

### Inspecting Cluster Diagnostics
```bash
# Requires ADMIN Bearer token
curl -H "Authorization: Bearer <ADMIN_JWT_TOKEN>" \
  http://localhost:8080/v1/diagnostics/report
```

### Graceful Node Restart
To restart a coordinator node without disrupting lock service:
1. Verify quorum is healthy ($N=3$, 1 leader, 2 followers).
2. If restarting the current leader, issue a step-down or stop the container.
3. The remaining two nodes will hold quorum ($Q=2$) and elect a new leader in $< 1.5 \text{ seconds}$.
4. Restart the node. It will reconnect, perform catch-up log replication, and re-enter active voting status.

