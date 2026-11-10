# LOCKGUARD — Installation & Deployment Guide

## Academic Project Title: Fault Tolerant Distributed Lock Service
**System Version:** Phase 6 Final Release  
**Target Environments:** Windows 10/11, macOS, Linux (Debian/Ubuntu/RHEL), Docker, Kubernetes  

---

## 1. System Requirements & Prerequisites

### 1.1 Hardware Specifications
- **Minimum:** 2 CPU Cores, 4 GB RAM, 10 GB SSD/NVMe storage.
- **Recommended (Cluster):** 4+ CPU Cores, 8 GB RAM, Fast SSD storage (low I/O latency for Raft write-ahead logs).

### 1.2 Software Dependencies
| Component | Minimum Version | Verification Command |
| :--- | :--- | :--- |
| **Go** | 1.22+ | `go version` |
| **Node.js** | 18+ LTS | `node -v` |
| **npm** | 9+ | `npm -v` |
| **Docker** | 24+ | `docker --version` |
| **Docker Compose** | 2.20+ | `docker compose version` |
| **PostgreSQL** (Optional for local) | 15+ | `psql --version` |
| **PowerShell** (Windows) | 5.1 / 7+ | `$PSVersionTable.PSVersion` |

---

## 2. Environment Variables Configuration

Copy or create an environment file `.env` in the project root:

```env
# System Environment
ENVIRONMENT=production
LOG_LEVEL=info
LOG_FORMAT=json

# API Gateway Configuration
GATEWAY_PORT=8080
GATEWAY_HOST=0.0.0.0
GATEWAY_COORDINATOR_ENDPOINTS=http://node1:8001,http://node2:8002,http://node3:8003
GATEWAY_REQUEST_TIMEOUT=5s
GATEWAY_LEADER_CACHE_TTL=2s

# Consensus Node 1 (Example for Node 1)
RAFT_NODE_ID=node-1
RAFT_HTTP_PORT=8001
RAFT_PORT=9001
RAFT_STORAGE_PATH=./data/node1/raft
RAFT_BOOTSTRAP=true
RAFT_CLUSTER_NODES=node-1:node1:9001,node-2:node2:9002,node-3:node3:9003

# Security & Authentication
JWT_SECRET_KEY=lockguard_production_secret_key_change_in_real_prod
JWT_EXPIRATION_HOURS=24
API_TOKEN_SALT=admin_secure_pass_phase5

# PostgreSQL Persistence
POSTGRES_HOST=postgres
POSTGRES_PORT=5432
POSTGRES_DB=lockguard
POSTGRES_USER=lockguard
POSTGRES_PASSWORD=lockguard_secure_password
POSTGRES_SSLMODE=disable
POSTGRES_MAX_OPEN_CONNS=25
POSTGRES_MAX_IDLE_CONNS=10

# Protected Resource Service
PROTECTED_RESOURCE_PORT=9000
```

---

## 3. Fast-Track Deployment: Docker Compose (Recommended)

Docker Compose starts the entire cluster topology, database, telemetry, and frontend UI in a single command.

### Step 1: Navigate to Project Root
```powershell
cd "D:\5TH SEM\SD\PROJECT\LOCKGUARD"
```

### Step 2: Validate Docker Compose Configuration
```powershell
docker compose config
```

### Step 3: Build & Start All Services
```powershell
docker compose up --build -d
```

### Step 4: Verify Container Status
```powershell
docker compose ps
```
Expected output shows healthy services:
- `lockguard-postgres` (Port 5435 host -> 5432 container)
- `lockguard-coordinator-a`, `b`, `c` (Internal Raft: 7000, API: 8001)
- `lockguard-api` (Port 8080)
- `lockguard-protected-resource` (Port 9000)
- `lockguard-frontend` (Port 80/3000)
- `lockguard-prometheus` (Port 9090)
- `lockguard-nginx` (Port 80)

---

## 4. Manual Local Bare-Metal Setup (Development)

To run LOCKGUARD natively on Windows without Docker:

### 4.1 Build Backend Binaries
```powershell
# In D:\5TH SEM\SD\PROJECT\LOCKGUARD\backend
go build -o ..\bin\lockguard-api.exe .\cmd\api
go build -o ..\bin\lockguard-coordinator.exe .\cmd\coordinator
go build -o ..\bin\lockguard-protected.exe ..\protected-resource\cmd
```

### 4.2 Start Node 1 (Leader Bootstrap)
```powershell
$env:RAFT_NODE_ID="node-1"
$env:RAFT_HTTP_PORT="8001"
$env:RAFT_PORT="9001"
$env:RAFT_STORAGE_PATH=".\data\node1"
$env:RAFT_BOOTSTRAP="true"
$env:RAFT_CLUSTER_NODES="node-1:127.0.0.1:9001,node-2:127.0.0.1:9002,node-3:127.0.0.1:9003"
.\bin\lockguard-coordinator.exe
```

### 4.3 Start Node 2 and Node 3 (Followers)
Open separate terminals for each node:
```powershell
# Terminal Node 2:
$env:RAFT_NODE_ID="node-2"
$env:RAFT_HTTP_PORT="8002"
$env:RAFT_PORT="9002"
$env:RAFT_STORAGE_PATH=".\data\node2"
$env:RAFT_BOOTSTRAP="false"
$env:RAFT_CLUSTER_NODES="node-1:127.0.0.1:9001,node-2:127.0.0.1:9002,node-3:127.0.0.1:9003"
.\bin\lockguard-coordinator.exe

# Terminal Node 3:
$env:RAFT_NODE_ID="node-3"
$env:RAFT_HTTP_PORT="8003"
$env:RAFT_PORT="9003"
$env:RAFT_STORAGE_PATH=".\data\node3"
$env:RAFT_BOOTSTRAP="false"
$env:RAFT_CLUSTER_NODES="node-1:127.0.0.1:9001,node-2:127.0.0.1:9002,node-3:127.0.0.1:9003"
.\bin\lockguard-coordinator.exe
```

### 4.4 Start API Gateway
```powershell
# Terminal Gateway:
$env:HTTP_PORT="8080"
$env:GATEWAY_COORDINATOR_ENDPOINTS="http://127.0.0.1:8001,http://127.0.0.1:8002,http://127.0.0.1:8003"
.\bin\lockguard-api.exe
```

### 4.5 Start Frontend Dashboard
```powershell
cd "D:\5TH SEM\SD\PROJECT\LOCKGUARD\frontend"
npm install
npm run dev
# Dashboard launches at http://localhost:3000 (or 5173 depending on vite configuration)
```

---

## 5. Deployment Verification & Smoke Test

Execute this PowerShell verification script to confirm cluster readiness:

```powershell
# 1. Test Gateway Health
$health = Invoke-RestMethod -Uri "http://localhost:8080/health"
Write-Host "Gateway Health:" $health.status

# 2. Authenticate
$loginBody = @{ username = "admin"; password = "admin_secure_pass_phase5" } | ConvertTo-Json
$auth = Invoke-RestMethod -Uri "http://localhost:8080/v1/auth/login" -Method Post -Body $loginBody -ContentType "application/json"
$token = $auth.data.token
Write-Host "JWT Token Acquired:" $token.Substring(0, 20)...

# 3. Acquire Lock
$headers = @{ Authorization = "Bearer $token" }
$acqBody = @{
    request_id = "smoke-test-1"
    resource_name = "smoke-resource"
    owner_id = "smoke-tester"
    ttl_seconds = 10
} | ConvertTo-Json
$lock = Invoke-RestMethod -Uri "http://localhost:8080/v1/locks/acquire" -Method Post -Headers $headers -Body $acqBody -ContentType "application/json"
Write-Host "Acquired Lock Fencing Token:" $lock.data.fencing_token
Write-Host "SMOKE TEST PASSED SUCCESSFULLY."
```

