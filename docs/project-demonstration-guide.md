# LOCKGUARD — Project Demonstration & Academic Defense Guide

## Academic Project Title: Fault Tolerant Distributed Lock Service
**System Version:** Phase 6 Final Release  
**Demonstration Format:** 10 to 15 Minute Live Evaluation Walkthrough  
**Target Audience:** Academic Review Committee, Faculty Examiners, Industry Evaluators  

---

## Demonstration Overview & Setup Checklist

Before initiating the defense presentation:
1. Ensure Docker Desktop is active.
2. In the project root (`D:\5TH SEM\SD\PROJECT\LOCKGUARD`), launch the cluster:
   ```powershell
   docker compose up --build -d
   ```
3. Open two terminal windows:
   - **Terminal 1:** Execution terminal for `curl` / PowerShell commands.
   - **Terminal 2:** Log tailing terminal (`docker compose logs -f gateway node1 node2 node3`).
4. Open the Web Browser to `http://localhost:3000` (React Operator Dashboard).

---

## 15-Step Step-by-Step Demonstration Walkthrough

### Step 1: Cluster Bootstrap & Invariant Verification
- **Objective:** Demonstrate that the 3-node HashiCorp Raft cluster is active, quorum is established, and a single leader is elected.
- **Action (Terminal 1):**
  ```powershell
  Invoke-RestMethod -Uri "http://localhost:8080/v1/cluster/status" | ConvertTo-Json -Depth 4
  ```
- **Expected Output:** HTTP 200 with JSON showing cluster nodes (`node-1`, `node-2`, `node-3`), quorum rule $2/3$, and current leader node.
- **Spoken Script:**
  > *"Good morning, respected examiners. We begin by inspecting the LOCKGUARD cluster status via our API Gateway. As shown in the JSON response, the cluster consists of three independent coordinator nodes. A single leader has been elected through Raft consensus, and the cluster is operating under strict consistency."*

---

### Step 2: Edge Security & JWT Authentication
- **Objective:** Demonstrate HMAC-SHA256 JWT authentication and role-based access control.
- **Action (Terminal 1):**
  ```powershell
  $login = @{ username = "admin"; password = "admin_secure_pass_phase5" } | ConvertTo-Json
  $resp = Invoke-RestMethod -Uri "http://localhost:8080/v1/auth/login" -Method Post -Body $login -ContentType "application/json"
  $global:TOKEN = $resp.data.token
  Write-Host "JWT Token Acquired:" $global:TOKEN.Substring(0, 25)...
  ```
- **Expected Output:** HTTP 200 containing signed JWT token, expiration timestamp, and `admin` role.
- **Spoken Script:**
  > *"To ensure defense-in-depth, all lock operations require authenticated credentials. We authenticate against the Gateway and obtain a signed HMAC-SHA256 JWT token with an assigned role and owner identity."*

---

### Step 3: Distributed Lock Acquisition & Monotonic Fencing Token
- **Objective:** Demonstrate lock acquisition across Raft consensus with monotonic fencing token issuance.
- **Action (Terminal 1):**
  ```powershell
  $headers = @{ Authorization = "Bearer $global:TOKEN" }
  $acq = @{ request_id = "demo-1"; resource_name = "bank-ledger-2026"; owner_id = "worker-alpha"; ttl_seconds = 20 } | ConvertTo-Json
  $lock1 = Invoke-RestMethod -Uri "http://localhost:8080/v1/locks/acquire" -Method Post -Headers $headers -Body $acq -ContentType "application/json"
  $global:TOKEN_VAL = $lock1.data.fencing_token
  $lock1.data | ConvertTo-Json
  ```
- **Expected Output:** HTTP 200 granting lock with `fencing_token: 1`, `lease_version: 1`, and 20s TTL.
- **Spoken Script:**
  > *"Client 'worker-alpha' requests an exclusive lock on 'bank-ledger-2026'. The request is replicated across the Raft quorum. The cluster grants the lock and issues Fencing Token 1 with initial Lease Version 1."*

---

### Step 4: Mutual Exclusion & Lock Contention Conflict
- **Objective:** Prove that a competing client is blocked from acquiring an already held lock.
- **Action (Terminal 1):**
  ```powershell
  $conflict = @{ request_id = "demo-2"; resource_name = "bank-ledger-2026"; owner_id = "worker-beta"; ttl_seconds = 20 } | ConvertTo-Json
  try {
      Invoke-RestMethod -Uri "http://localhost:8080/v1/locks/acquire" -Method Post -Headers $headers -Body $conflict -ContentType "application/json"
  } catch {
      $_.Exception.Response.StatusCode
      $_.ErrorDetails.Message
  }
  ```
- **Expected Output:** HTTP 409 Conflict with error code `ERR_LOCK_ALREADY_HELD`.
- **Spoken Script:**
  > *"A concurrent client 'worker-beta' attempts to acquire the same resource. As mandated by the mutual exclusion invariant, the cluster rejects the request with HTTP 409 Conflict, preserving safety."*

---

### Step 5: Fenced Mutation on Protected Resource
- **Objective:** Demonstrate downstream storage accepting a valid write paired with the active fencing token.
- **Action (Terminal 1):**
  ```powershell
  $write = @{ resource_name = "bank-ledger-2026"; fencing_token = $global:TOKEN_VAL; owner_id = "worker-alpha"; payload = "TRANSFER $500" } | ConvertTo-Json
  Invoke-RestMethod -Uri "http://localhost:9000/api/v1/write" -Method Post -Body $write -ContentType "application/json"
  ```
- **Expected Output:** HTTP 200 OK: `{"status":"success","applied_token":1}`.
- **Spoken Script:**
  > *"Client 'worker-alpha' writes to our protected resource service, supplying Fencing Token 1. The resource verifies that Token 1 is greater than any token previously seen and applies the state transition safely."*

---

### Step 6: Martin Kleppmann Stale Fencing Token Rejection
- **Objective:** Simulate a zombie or delayed client attempting to mutate storage with an outdated token.
- **Action (Terminal 1):**
  ```powershell
  $staleWrite = @{ resource_name = "bank-ledger-2026"; fencing_token = 0; owner_id = "zombie-worker"; payload = "CORRUPT DATA" } | ConvertTo-Json
  try {
      Invoke-RestMethod -Uri "http://localhost:9000/api/v1/write" -Method Post -Body $staleWrite -ContentType "application/json"
  } catch {
      $_.Exception.Response.StatusCode
      $_.ErrorDetails.Message
  }
  ```
- **Expected Output:** HTTP 409 Conflict: `stale fencing token rejected`.
- **Spoken Script:**
  > *"This step demonstrates the core solution to Martin Kleppmann's famous critique of distributed locks. If a stale or partitioned process attempts to write with an older fencing token, the downstream service immediately rejects it, preventing silent data corruption."*

---

### Step 7: Automated Lease Renewal & Exact Version Increment
- **Objective:** Demonstrate lease extension with exact version advancement.
- **Action (Terminal 1):**
  ```powershell
  $renew = @{ request_id = "demo-3"; resource_name = "bank-ledger-2026"; owner_id = "worker-alpha"; fencing_token = $global:TOKEN_VAL; ttl_seconds = 20 } | ConvertTo-Json
  $renResp = Invoke-RestMethod -Uri "http://localhost:8080/v1/locks/renew" -Method Post -Headers $headers -Body $renew -ContentType "application/json"
  $renResp.data | ConvertTo-Json
  ```
- **Expected Output:** HTTP 200 OK showing `lease_version: 2` (incremented exactly by 1) while retaining `fencing_token: 1`.
- **Spoken Script:**
  > *"Client 'worker-alpha' extends its lease. Notice that the fencing token remains 1, but the Lease Version increments from 1 to 2. LOCKGUARD enforces exact version progression to prevent duplicate or phantom renewals."*

---

### Step 8: Explicit Graceful Lock Release
- **Objective:** Demonstrate clean release of the resource.
- **Action (Terminal 1):**
  ```powershell
  $rel = @{ request_id = "demo-4"; resource_name = "bank-ledger-2026"; owner_id = "worker-alpha"; fencing_token = $global:TOKEN_VAL } | ConvertTo-Json
  Invoke-RestMethod -Uri "http://localhost:8080/v1/locks/release" -Method Post -Headers $headers -Body $rel -ContentType "application/json"
  ```
- **Expected Output:** HTTP 200 OK: `{"released": true}`.
- **Spoken Script:**
  > *"The critical section has finished, and 'worker-alpha' releases the lock. The resource is immediately available for subsequent acquisitions, which will receive Fencing Token 2 or higher."*

---

### Step 9: Chaos Injection — Leader Sudden Death
- **Objective:** Kill the active Raft leader node during live operations.
- **Action (Terminal 1):**
  ```powershell
  docker compose stop node1
  ```
- **Expected Output:** Container `lockguard-node1` halts.
- **Spoken Script:**
  > *"We now simulate a severe hardware failure by abruptly terminating Node 1, our active Raft leader."*

---

### Step 10: Automatic Raft Failover & Election (< 300ms)
- **Objective:** Observe follower detecting heartbeat lapse and electing a new leader without human intervention.
- **Action (Terminal 1):**
  ```powershell
  Start-Sleep -Milliseconds 400
  Invoke-RestMethod -Uri "http://localhost:8080/v1/cluster/status" | ConvertTo-Json -Depth 4
  ```
- **Expected Output:** HTTP 200 showing a new leader (Node 2 or Node 3) with an advanced Raft Term.
- **Spoken Script:**
  > *"Within 300 milliseconds, the remaining followers detected the heartbeat timeout, initiated a new Raft election, and elected Node 2 as the new leader. Quorum of 2 out of 3 nodes is satisfied."*

---

### Step 11: Seamless Lock Operations on New Leader
- **Objective:** Verify client requests succeed immediately against the newly elected leader.
- **Action (Terminal 1):**
  ```powershell
  $newAcq = @{ request_id = "demo-5"; resource_name = "bank-ledger-2026"; owner_id = "worker-gamma"; ttl_seconds = 15 } | ConvertTo-Json
  $lock2 = Invoke-RestMethod -Uri "http://localhost:8080/v1/locks/acquire" -Method Post -Headers $headers -Body $newAcq -ContentType "application/json"
  $lock2.data | ConvertTo-Json
  ```
- **Expected Output:** HTTP 200 OK granting the lock with `fencing_token: 2`.
- **Spoken Script:**
  > *"The API Gateway dynamically redirected the request to Node 2. The new lock is granted, and notice that the fencing token has advanced monotonically to 2, proving complete state preservation across failover."*

---

### Step 12: Quorum Loss Simulation (Fail-Closed Safety)
- **Objective:** Demonstrate that the cluster refuses writes when a majority is lost ($CP$ property).
- **Action (Terminal 1):**
  ```powershell
  docker compose stop node2
  # Now only 1 of 3 nodes is alive (Quorum lost)
  $failAcq = @{ request_id = "demo-6"; resource_name = "test-resource"; owner_id = "worker-x"; ttl_seconds = 10 } | ConvertTo-Json
  try {
      Invoke-RestMethod -Uri "http://localhost:8080/v1/locks/acquire" -Method Post -Headers $headers -Body $failAcq -ContentType "application/json"
  } catch {
      Write-Host "Service correctly failed closed:" $_.Exception.Response.StatusCode
  }
  ```
- **Expected Output:** HTTP 500 / 503 error.
- **Spoken Script:**
  > *"With two nodes down, quorum is lost. Rather than permitting a split-brain condition, LOCKGUARD strictly fails closed. Mutations are refused, guaranteeing data consistency."*

---

### Step 13: Cluster Quorum Recovery
- **Objective:** Restart the nodes and observe automatic cluster healing.
- **Action (Terminal 1):**
  ```powershell
  docker compose start node1 node2
  Start-Sleep -Seconds 2
  Invoke-RestMethod -Uri "http://localhost:8080/v1/cluster/status" | ConvertTo-Json -Depth 4
  ```
- **Expected Output:** Cluster reports all 3 nodes healthy and operational.
- **Spoken Script:**
  > *"We restart the offline nodes. They rejoin the cluster, catch up on missing Raft log entries, and the cluster resumes full normal operations."*

---

### Step 14: Database Outage Resilience (Fail-Open Audit Buffer)
- **Objective:** Demonstrate that a PostgreSQL database outage does not block consensus lock operations.
- **Action (Terminal 1):**
  ```powershell
  docker compose stop postgres
  # Acquire lock while PostgreSQL is completely offline
  $dbOffAcq = @{ request_id = "demo-7"; resource_name = "resilient-resource"; owner_id = "worker-alpha"; ttl_seconds = 10 } | ConvertTo-Json
  $resLock = Invoke-RestMethod -Uri "http://localhost:8080/v1/locks/acquire" -Method Post -Headers $headers -Body $dbOffAcq -ContentType "application/json"
  Write-Host "Lock granted despite DB outage! Fencing token:" $resLock.data.fencing_token
  docker compose start postgres
  ```
- **Expected Output:** Lock acquired successfully with HTTP 200. Audit events buffer in memory and flush once PostgreSQL restarts.
- **Spoken Script:**
  > *"To protect high-throughput consensus, LOCKGUARD uses a fail-open audit buffer. Even with PostgreSQL completely stopped, lock acquisitions succeed in milliseconds. Once the database recovers, buffered events are written asynchronously."*

---

### Step 15: Web Dashboard & Concluding Summary
- **Objective:** Walk through the React operator dashboard at `http://localhost:3000`.
- **Action:** Switch to browser window. Point out active locks, cluster topology widget, countdown progress bars, and historical audit entries.
- **Spoken Script:**
  > *"Finally, here is our React Operator Dashboard. We see live cluster topology, real-time TTL countdowns, and the complete audit trail. In conclusion, LOCKGUARD provides strong linearizability, monotonic fencing, fail-closed safety, and operational resilience under real-world distributed failures. We welcome your questions."*

