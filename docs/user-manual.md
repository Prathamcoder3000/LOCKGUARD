# LOCKGUARD — User & Developer Manual

## Academic Project Title: Fault Tolerant Distributed Lock Service
**System Version:** Phase 6 Final Release  
**Target Audience:** Distributed Systems Developers, Microservice Architects, DevOps Operators  

---

## 1. Introduction & Core Concepts

LOCKGUARD provides distributed mutual exclusion for mission-critical operations such as database schema migrations, leader-worker task assignments, scheduled cron execution, and serial financial accounting.

### Key Concepts
- **Resource:** A unique string identifying the critical section (e.g. `billing-cron-job`, `warehouse-stock-1044`).
- **Owner ID:** A unique string identifying the worker process instance holding the lock (e.g. `worker-pod-89dfb`).
- **Fencing Token:** A monotonically increasing unsigned 64-bit integer issued by the consensus cluster on every grant. Crucial for guarding downstream databases against delayed or zombie processes.
- **Lease Version:** An exact sequence counter incremented on every lease renewal (+1).
- **TTL (Time to Live):** The safety timeout after which the cluster automatically frees the lock if the holding process crashes or disconnects.

---

## 2. Using the Web Dashboard

### 2.1 Access & Authentication
1. Open your browser and navigate to `http://localhost:3000`.
2. Enter your credentials on the Login screen:
   - **Username:** `admin` (or your client username)
   - **Password:** `admin_secure_pass_phase5`
3. Click **Sign In**. The JWT token will be securely stored in memory for your session.

### 2.2 Live Cluster & Lock Monitoring
- **Cluster Status Card:** Displays the current Raft Leader node, active Term, and quorum health status across all nodes.
- **Active Locks Table:** Displays all currently held locks, their resource names, owning client identities, current fencing tokens, and real-time countdown of remaining TTL.
- **Lock Acquisition Panel:** Allows operators to manually acquire locks with custom TTLs for maintenance or testing.
- **Audit History Feed:** Live stream of recent cluster events (Acquisitions, Renewals, Releases, Expirations) with filterable columns.

---

## 3. Distributed Lock Integration Patterns (Developer Guide)

### 3.1 Correct Pattern: The Fenced Mutation Pattern
Distributed locks without fencing tokens are vulnerable to GC pauses or network delays. Always pass the `fencing_token` to your downstream service:

```
[ Worker A ] -------- Acquire Lock --------> [ LOCKGUARD ]
             <--- Granted (Token = 5) ------
[ Worker A ]
  |-- Worker pauses (GC/Network) for 20s...
  |-- Lock expires on LOCKGUARD!
  |                                        [ LOCKGUARD ]
  |                     [ Worker B ] <--- Granted (Token = 6)
  |                     [ Worker B ] ---- Mutate DB (Token=6) ----> [ Database ]
  |                                                                 (Highest = 6)
  |-- Worker A wakes up!
  |-- Worker A attempts mutation with Stale Token = 5
  +-------------------------------------- Mutate DB (Token=5) ----> [ Database ]
                                                                    REJECTED (409)!
```

### 3.2 Go Code Example
```go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type LockResponse struct {
	Success bool `json:"success"`
	Data    struct {
		FencingToken uint64 `json:"fencing_token"`
		ResourceName string `json:"resource_name"`
	} `json:"data"`
}

func main() {
	client := &http.Client{Timeout: 5 * time.Second}

	// 1. Acquire Lock
	payload := []byte(`{
		"resource_name": "inventory-reconciliation",
		"owner_id": "batch-worker-1",
		"ttl_seconds": 30
	}`)
	req, _ := http.NewRequest("POST", "http://localhost:8080/v1/locks/acquire", bytes.NewBuffer(payload))
	req.Header.Set("Authorization", "Bearer <YOUR_JWT_TOKEN>")
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		fmt.Printf("Failed to acquire lock: %v\n", err)
		return
	}
	defer resp.Body.Close()

	var lock LockResponse
	json.NewDecoder(resp.Body).Decode(&lock)
	fmt.Printf("Acquired lock with Fencing Token: %d\n", lock.Data.FencingToken)

	// 2. Perform Mutation on Protected Storage with Fencing Token
	mutatePayload := fmt.Sprintf(`{
		"resource_name": "inventory-reconciliation",
		"fencing_token": %d,
		"owner_id": "batch-worker-1",
		"payload": "UPDATE stock SET count = count - 1"
	}`, lock.Data.FencingToken)

	mutReq, _ := http.NewRequest("POST", "http://localhost:9000/api/v1/write", bytes.NewBuffer([]byte(mutatePayload)))
	mutReq.Header.Set("Content-Type", "application/json")
	mutResp, err := client.Do(mutReq)
	if err != nil || mutResp.StatusCode != http.StatusOK {
		fmt.Printf("Mutation rejected! Possible stale fencing token.\n")
		return
	}
	fmt.Println("Critical section executed safely.")

	// 3. Release Lock Gracefully
	releasePayload := fmt.Sprintf(`{
		"resource_name": "inventory-reconciliation",
		"owner_id": "batch-worker-1",
		"fencing_token": %d
	}`, lock.Data.FencingToken)
	relReq, _ := http.NewRequest("POST", "http://localhost:8080/v1/locks/release", bytes.NewBuffer([]byte(releasePayload)))
	relReq.Header.Set("Authorization", "Bearer <YOUR_JWT_TOKEN>")
	relReq.Header.Set("Content-Type", "application/json")
	client.Do(relReq)
	fmt.Println("Lock released cleanly.")
}
```

---

## 4. Troubleshooting Common Errors

| Error Code | HTTP Status | Root Cause | Solution |
| :--- | :--- | :--- | :--- |
| `ERR_LOCK_ALREADY_HELD` | `409 Conflict` | Another active process holds this resource lock. | Back off and retry with exponential jitter, or wait for the existing TTL to expire. |
| `ERR_STALE_FENCING_TOKEN` | `409 Conflict` | Your process took too long; another worker acquired the lock in the interim. | Abort the current transaction. Do not overwrite downstream storage. |
| `ERR_LEASE_VERSION_MISMATCH` | `412 Precond Failed` | The renewal request used a version that does not equal current version + 1. | Query current lock state before resubmitting renewal. |
| `ERR_RATE_LIMIT_EXCEEDED` | `429 Too Many Requests` | Client exceeded maximum requests per second (200 rps, burst 400). | Throttle client request generation; inspect for runaway loops. |
| `ERR_UNAUTHORIZED` | `401 Unauthorized` | Missing, expired, or invalid JWT token. | Re-authenticate at `/v1/auth/login` to obtain a fresh token. |
| `ERR_FORBIDDEN` | `403 Forbidden` | Authenticated client has `read-only` role and cannot mutate locks. | Contact administrator to grant `client` or `admin` role. |

