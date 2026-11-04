# LockGuard — Fault Tolerant Distributed Lock Service

[![Go](https://img.shields.io/badge/Go-1.27.3-00ADD8?style=flat&logo=go)](https://go.dev)
[![React](https://img.shields.io/badge/React-18.3-61DAFB?style=flat&logo=react)](https://react.dev)
[![Vite](https://img.shields.io/badge/Vite-6.1-646CFF?style=flat&logo=vite)](https://vitejs.dev)
[![TailwindCSS](https://img.shields.io/badge/Tailwind-3.4-38B2AC?style=flat&logo=tailwind-css)](https://tailwindcss.com)
[![Docker](https://img.shields.io/badge/Docker-Compose_v5-2496ED?style=flat&logo=docker)](https://www.docker.com)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

> **Academic Title:** Fault Tolerant Distributed Lock Service  
> **Project Type:** Distributed Systems / Software Engineering  
> **Core Invariant:** `CONSISTENCY FIRST — FAIL CLOSED`

---

## 1. Executive Summary

**LockGuard** is a fault-tolerant distributed coordination service that provides exclusive access to named shared resources across multiple client applications. It employs a three-node Raft consensus cluster to maintain consistent, replicated lock state across failure domains.

The system implements:
- Distributed lock acquisition, lease heartbeats, and Martin Kleppmann's fencing tokens.
- Majority quorum replication ($2 \text{ of } 3 \text{ nodes}$) with strictly fail-closed semantics upon quorum loss.
- In-memory replicated state machine backed by durable BoltDB consensus logs and snapshot recovery.
- Independent fencing state verification at external protected resources.
- Operational metadata, user governance, audit logging, and benchmark persistence in PostgreSQL.
- Modern enterprise React operator dashboard with deep-navy styling and real-time cluster probe inspection.

---

## 2. Architectural Overview

```
+-----------------------------------------------------------------------------------+
|                              CLIENT APPLICATIONS                                  |
+-----------------------------------------------------------------------------------+
                                         |
                                         v
+-----------------------------------------------------------------------------------+
|                       NGINX EDGE GATEWAY (:80 / :443)                             |
|          Routes /api/ to Go Gateway  •  Routes / to React SPA Dashboard           |
+-----------------------------------------------------------------------------------+
                 |                                                 |
                 v                                                 v
+---------------------------------+             +-----------------------------------+
|      LOCKGUARD API GATEWAY      |             |         REACT OPERATOR UI         |
|             (:8080)             |             |          (Vite / Tailwind)        |
+---------------------------------+             +-----------------------------------+
                 |
                 +-----------------------+
                 |                       |
                 v                       v
+---------------------------------+    +--------------------------------------------+
|     POSTGRESQL 16 METADATA      |    |      THREE-NODE RAFT COORDINATOR CLUSTER   |
|   (Users, Audits, Benchmarks)   |    |         (Fail Closed Quorum: 2 of 3)       |
|  *No Authoritative Lock State*  |    |  • Coordinator A (:7000 / :8001)           |
+---------------------------------+    |  • Coordinator B (:7000 / :8001)           |
                                       |  • Coordinator C (:7000 / :8001)           |
                                       |  *Private Network: Port 7000 Not Public*   |
                                       +--------------------------------------------+
                                                         |
                                                         v
                                       +--------------------------------------------+
                                       |         PROTECTED RESOURCE SERVICE         |
                                       |                  (:9000)                   |
                                       |      Validates Monotonic Fencing Tokens    |
                                       +--------------------------------------------+
```

---

## 3. Technology Stack

- **Backend:** Go (`go1.27.3`), HashiCorp Raft (`github.com/hashicorp/raft` v1.7.2 candidate), structured logging (`log/slog`), HTTP middleware stack.
- **Frontend:** React 18, Vite 6, TypeScript 5.7, Tailwind CSS 3.4, React Router 6, Axios, Lucide React, Recharts.
- **Storage:**
  - *Consensus Tier:* Local BoltDB log store, stable metadata store, and file snapshot storage per coordinator node.
  - *Relational Tier:* PostgreSQL 16 (for audit events, benchmark runs, user management, and dashboard preferences).
  - *Protected Tier:* Independent fencing state guard at the protected resource.
- **Infrastructure:** Docker, Docker Compose, Nginx reverse proxy, Prometheus metrics scraper.

---

## 4. Repository Structure

```
LOCKGUARD/
  ├── backend/                     # Go Coordination Engine & API Gateway
  │   ├── cmd/
  │   │   ├── api/                 # LockGuard REST API entry point
  │   │   └── coordinator/         # Coordinator daemon entry point (Nodes A, B, C)
  │   ├── internal/
  │   │   ├── api/                 # Server, handlers, standardized JSON envelopes
  │   │   ├── audit/               # Audit trail generation
  │   │   ├── auth/                # JWT and authentication tokens
  │   │   ├── benchmark/           # Benchmark orchestration
  │   │   ├── cluster/             # Cluster topology definitions
  │   │   ├── config/              # Environment config loading & validation
  │   │   ├── consensus/           # Raft consensus wrappers
  │   │   ├── events/              # Event dispatching
  │   │   ├── fencing/             # Fencing token counters
  │   │   ├── leases/              # Lease TTL managers
  │   │   ├── locks/               # Lock state logic
  │   │   ├── metrics/             # Prometheus metric counters
  │   │   ├── middleware/          # Slog logger, CORS, panic recovery
  │   │   ├── recovery/            # Crash recovery & log replay
  │   │   ├── snapshots/           # Snapshot managers
  │   │   ├── state_machine/       # Replicated FSM
  │   │   └── storage/             # PostgreSQL connection pool
  │   ├── migrations/              # PostgreSQL schema migrations
  │   └── tests/                   # Backend unit tests
  │
  ├── frontend/                    # React + Vite + TypeScript Operator Dashboard
  │   └── src/
  │       ├── app/                 # Root App and router declarations
  │       ├── charts/              # Recharts visualizations
  │       ├── components/          # Sidebar, Header, ProtectedRoute
  │       ├── context/             # AuthContext session state
  │       ├── hooks/               # Custom hooks
  │       ├── layouts/             # DashboardLayout (Deep Navy sidebar)
  │       ├── pages/               # Overview, Locks, Cluster, Audit, Benchmarks
  │       ├── services/            # Axios API clients
  │       └── styles/              # Tailwind CSS directives
  │
  ├── protected-resource/          # Target service validating fencing tokens
  │   ├── cmd/                     # Protected resource entry point (:9000)
  │   └── internal/                # Monotonic FencingGuard and unit tests
  │
  ├── deploy/                      # Infrastructure configurations
  │   ├── docker/                  # Dockerfiles for API, Coordinator, Frontend, PR
  │   ├── nginx/                   # Nginx reverse proxy configuration
  │   └── prometheus/              # Prometheus scraping configuration
  │
  ├── tests/                       # Test specifications & suites
  │   ├── unit/
  │   ├── integration/
  │   ├── concurrency/
  │   ├── fault_injection/
  │   └── recovery/
  │
  ├── benchmarks/                  # Benchmark scripts and scenarios
  ├── scripts/                     # Operational automation scripts
  │
  ├── docs/                        # Comprehensive Architecture & Specification Docs
  │   ├── architecture.md          # System architecture, topology, failure domains
  │   ├── technical-specification.md # Raft candidate analysis, state structures, ports
  │   ├── api-contract.md          # REST API payloads, status codes, error catalogue
  │   ├── state-model.md           # State machine lifecycle and mathematical proofs
  │   ├── deployment.md            # Docker Compose & local execution guide
  │   ├── testing.md               # Test hierarchy and execution commands
  │   └── user-guide.md            # Operator guide and client integration pattern
  │
  ├── docker-compose.yml           # 9-service production-ready topology
  ├── .env.example                 # Comprehensive environment template
  ├── .gitignore                   # Binaries, volumes, vendor, keys ignored
  ├── LICENSE                      # Apache-2.0 License
  └── README.md                    # Project documentation
```

---

## 5. Verification & Test Results (Phase 0)

All acceptance criteria for Phase 0 have been verified:

### 1. Backend Verification (`backend/tests`)
```
=== RUN   TestDefaultConfig
--- PASS: TestDefaultConfig (0.00s)
=== RUN   TestConfigValidationFailure
--- PASS: TestConfigValidationFailure (0.00s)
=== RUN   TestEnvOverride
--- PASS: TestEnvOverride (0.00s)
=== RUN   TestHealthEndpoint
--- PASS: TestHealthEndpoint (0.00s)
=== RUN   TestReadinessEndpoint
--- PASS: TestReadinessEndpoint (0.00s)
=== RUN   TestMethodNotAllowedOnHealth
--- PASS: TestMethodNotAllowedOnHealth (0.00s)
=== RUN   TestServerRoutingAndClusterStatus
--- PASS: TestServerRoutingAndClusterStatus (0.00s)
=== RUN   TestStandardErrorFormatting
--- PASS: TestStandardErrorFormatting (0.00s)
PASS
ok      lockguard/backend/tests 0.854s
```

### 2. Protected Resource Verification (`protected-resource/internal`)
```
=== RUN   TestFencingGuardMonotonicity
--- PASS: TestFencingGuardMonotonicity (0.00s)
PASS
ok      lockguard/protected-resource/internal   0.704s
```

### 3. Frontend Build Verification (`frontend/`)
```
vite v6.4.5 building for production...
✓ 1664 modules transformed.
dist/index.html                   0.88 kB │ gzip:  0.50 kB
dist/assets/index-CgOVcSWx.css   24.32 kB │ gzip:  4.88 kB
dist/assets/index-DIiHD52j.js   272.20 kB │ gzip: 87.34 kB
✓ built in 6.27s
```

### 4. Docker Compose Verification
```powershell
docker compose config
# Exited with code 0 - All 9 services, 4 volumes, and 2 networks validated successfully.
```

---

## 6. Getting Started

### Local Quickstart

1. **Start the Go API:**
   ```powershell
   cd backend
   go run ./cmd/api
   ```
   Verify at `http://localhost:8080/health`.

2. **Start the Frontend Dashboard:**
   ```powershell
   cd frontend
   npm run dev
   ```
   Open `http://localhost:3000` in your browser.

3. **Or launch the entire cluster with Docker Compose:**
   ```powershell
   docker compose up -d --build
   ```

---

## 7. License

Licensed under the [Apache License, Version 2.0](LICENSE).
