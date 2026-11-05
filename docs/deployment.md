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

### Run Coordinator Nodes (Local 3-Node Cluster)
```powershell
# Terminal 1: Coordinator A (Designated Bootstrap Leader)
$env:NODE_ID="coordinator-a"; $env:RAFT_BIND_ADDR="127.0.0.1:7001"; $env:RAFT_ADVERTISE_ADDR="127.0.0.1:7001"; $env:RAFT_CLUSTER_NODES="coordinator-a=127.0.0.1:7001,coordinator-b=127.0.0.1:7002,coordinator-c=127.0.0.1:7003"; $env:COORDINATOR_API_PORT=8001; $env:DATA_DIR="./data/coordinator-a"; $env:BOOTSTRAP_LEADER="coordinator-a"; go run ./cmd/coordinator

# Terminal 2: Coordinator B
$env:NODE_ID="coordinator-b"; $env:RAFT_BIND_ADDR="127.0.0.1:7002"; $env:RAFT_ADVERTISE_ADDR="127.0.0.1:7002"; $env:RAFT_CLUSTER_NODES="coordinator-a=127.0.0.1:7001,coordinator-b=127.0.0.1:7002,coordinator-c=127.0.0.1:7003"; $env:COORDINATOR_API_PORT=8002; $env:DATA_DIR="./data/coordinator-b"; $env:BOOTSTRAP_LEADER="coordinator-a"; go run ./cmd/coordinator

# Terminal 3: Coordinator C
$env:NODE_ID="coordinator-c"; $env:RAFT_BIND_ADDR="127.0.0.1:7003"; $env:RAFT_ADVERTISE_ADDR="127.0.0.1:7003"; $env:RAFT_CLUSTER_NODES="coordinator-a=127.0.0.1:7001,coordinator-b=127.0.0.1:7002,coordinator-c=127.0.0.1:7003"; $env:COORDINATOR_API_PORT=8003; $env:DATA_DIR="./data/coordinator-c"; $env:BOOTSTRAP_LEADER="coordinator-a"; go run ./cmd/coordinator
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

---

## 5. Phase 2 Gateway & Heartbeat Operational Configuration

When running the API Gateway in Phase 2, configure coordinator HTTP endpoints and heartbeat intervals:

```powershell
$env:COORDINATOR_ENDPOINTS="http://localhost:8001,http://localhost:8002,http://localhost:8003"
$env:GATEWAY_LEADER_CACHE_TTL_MS="5000"
$env:GATEWAY_MAX_REDIRECT_ATTEMPTS="2"
$env:HEARTBEAT_INTERVAL_MS="3000"
$env:HEARTBEAT_TIMEOUT_MS="10000"
$env:HEARTBEAT_CLEANUP_INTERVAL_MS="2000"
$env:RENEWAL_SAFETY_MARGIN_MS="2000"
$env:RENEWAL_RETRY_INITIAL_MS="100"
$env:RENEWAL_RETRY_MAX_MS="2000"
$env:RENEWAL_MAX_ATTEMPTS="3"
go run ./cmd/api
```

---

## 6. Phase 3 PostgreSQL Persistence & Audit Operational Configuration

Configure the persistence connection pool and audit event pipeline:

```powershell
$env:DATABASE_HOST="localhost"
$env:DATABASE_PORT="5432"
$env:DATABASE_NAME="lockguard_db"
$env:DATABASE_USER="lockguard_user"
$env:DATABASE_PASSWORD="lockguard_password"
$env:DATABASE_SSLMODE="disable"
$env:DATABASE_MAX_OPEN_CONNS="20"
$env:DATABASE_MAX_IDLE_CONNS="10"
$env:DATABASE_CONN_MAX_LIFETIME="30m"
$env:DATABASE_CONN_TIMEOUT="5s"
```

The gateway automatically connects to PostgreSQL, applies pending schema migrations under an advisory lock (`pg_advisory_lock`), and initializes the asynchronous audit batch pipeline. If PostgreSQL is offline at startup, the gateway remains operational with status `DEGRADED`, allowing Raft lock operations to continue without interruption.

