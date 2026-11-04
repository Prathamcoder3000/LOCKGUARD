# LockGuard Deployment & Operational Guide

## 1. Prerequisites

- **Docker:** Engine version $\ge$ 20.10, Docker Compose v2.
- **Go:** Go 1.23+ (Installed locally at `D:\DE-Tools\go\bin`).
- **Node.js:** Node 20+ with npm 10+.
- **Host OS:** Windows with PowerShell or Linux/macOS.

---

## 2. Docker Compose Deployment

The complete cluster topology is declared in `docker-compose.yml`.

### Step 1: Prepare Environment File
```powershell
Copy-Item .env.example .env
```

### Step 2: Validate Compose Configuration
```powershell
docker compose config
```

### Step 3: Launch Cluster Services
```powershell
docker compose up -d --build
```

### Step 4: Verify Container Health
```powershell
docker compose ps
```

All 9 services will report `healthy`:
- `lockguard-postgres`
- `lockguard-coordinator-a`
- `lockguard-coordinator-b`
- `lockguard-coordinator-c`
- `lockguard-api`
- `lockguard-protected-resource`
- `lockguard-frontend`
- `lockguard-nginx`
- `lockguard-prometheus`

---

## 3. Local Bare-Metal Development (Without Docker)

### Run Backend API Service
```powershell
cd "D:\5TH SEM\SD\PROJECT\LOCKGUARD\backend"
go run ./cmd/api
```
Listens on `http://localhost:8080`. Health probe: `http://localhost:8080/health`.

### Run Coordinator Nodes
```powershell
# In terminal 1 (Node A)
$env:NODE_ID = "coordinator-a"; $env:COORDINATOR_API_PORT = 8001; go run ./cmd/coordinator

# In terminal 2 (Node B)
$env:NODE_ID = "coordinator-b"; $env:COORDINATOR_API_PORT = 8002; go run ./cmd/coordinator

# In terminal 3 (Node C)
$env:NODE_ID = "coordinator-c"; $env:COORDINATOR_API_PORT = 8003; go run ./cmd/coordinator
```

### Run Protected Resource Service
```powershell
cd "D:\5TH SEM\SD\PROJECT\LOCKGUARD\protected-resource"
go run ./cmd/main.go
```
Listens on `http://localhost:9000`.

### Run Frontend Development Server
```powershell
cd "D:\5TH SEM\SD\PROJECT\LOCKGUARD\frontend"
npm run dev
```
Accessible at `http://localhost:3000`.

---

## 4. Volume Persistence & Data Directories

Each coordinator preserves its state independently to prevent accidental shared-disk race conditions:

| Volume | Mount Path | Purpose |
| :--- | :--- | :--- |
| `coordinator_a_data` | `/var/lib/lockguard/data` | Node A BoltDB log, stable store, and Raft snapshots |
| `coordinator_b_data` | `/var/lib/lockguard/data` | Node B BoltDB log, stable store, and Raft snapshots |
| `coordinator_c_data` | `/var/lib/lockguard/data` | Node C BoltDB log, stable store, and Raft snapshots |
| `postgres_data` | `/var/lib/postgresql/data` | PostgreSQL relational cluster metadata |
