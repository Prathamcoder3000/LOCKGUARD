from scripts.docx_helpers import (
    add_heading_1, add_heading_2, add_heading_3, add_paragraph, add_bullet,
    add_callout, add_code_block, add_figure, add_table
)

def build_phases_1_to_10(doc):
    # ==========================================
    # PHASE 1
    # ==========================================
    add_heading_1(doc, "Phase 1 — Project Introduction & Requirements Verification")
    add_paragraph(doc, "To verify the project scope, functional requirements, and foundational distributed locking invariants against the repository architecture.", bold_prefix="Aim / Objective: ")
    add_paragraph(doc, "Windows 10/11 or Linux operating system, Git, and access to the LOCKGUARD repository root.", bold_prefix="Prerequisites & Requirements: ")
    add_paragraph(doc,
        "Distributed lock services provide mutual exclusion across networked nodes that do not share physical memory or unified system clocks. "
        "Standard single-node locking primitives (such as mutexes) fail in distributed environments due to asynchronous network partitions, "
        "unannounced process crashes, and variable message latencies. LOCKGUARD addresses these challenges by coordinating state transitions "
        "across a replicated Raft consensus quorum ($N=3, Q=2$) ensuring linearizability and crash tolerance.",
        bold_prefix="Core Concept & Theory: "
    )
    add_paragraph(doc, "README.md, docs/final-system-architecture.md, backend/go.mod, docker-compose.yml.", bold_prefix="Files & Components Used: ")

    add_heading_2(doc, "1.1 Step-by-Step Procedure & Exact Commands")
    add_bullet(doc, "Open PowerShell in administrator mode and navigate to the project directory:", "Step 1: ")
    add_code_block(doc, "Set-Location 'D:\\5TH SEM\\SD\\PROJECT\\LOCKGUARD'\nGet-ChildItem -Path .\nGet-Content README.md -TotalCount 40", "PowerShell")
    add_bullet(doc, "Inspect the master module dependencies in backend/go.mod to verify the HashiCorp Raft dependency:", "Step 2: ")
    add_code_block(doc, "Get-Content backend\\go.mod | Select-String 'hashicorp/raft'", "PowerShell")

    add_heading_2(doc, "1.2 Important Source Code: Master Dependencies")
    code_gomod = """module lockguard/backend

go 1.22

require (
    github.com/golang-jwt/jwt/v5 v5.2.1
    github.com/google/uuid v1.6.0
    github.com/hashicorp/raft v1.7.0
    github.com/hashicorp/raft-boltdb/v2 v2.3.1
    github.com/lib/pq v1.10.9
    github.com/prometheus/client_golang v1.19.1
)"""
    add_code_block(doc, code_gomod, "backend/go.mod")
    add_paragraph(doc,
        "Code Explanation: The backend uses Go 1.22 with production-grade dependencies. The core consensus engine is powered by "
        "github.com/hashicorp/raft v1.7.0, with persistent log and stable storage provided by github.com/hashicorp/raft-boltdb/v2. "
        "Audit persistence connects to PostgreSQL via github.com/lib/pq, and metrics exposition uses github.com/prometheus/client_golang. "
        "Notably, JWT authentication is implemented with zero external dependencies in internal/auth/jwt.go using standard library crypto/hmac.",
        italic=True
    )

    add_heading_2(doc, "1.3 Verification & Expected Observation")
    add_paragraph(doc,
        "Expected Observation: The repository root contains the backend, frontend, protected-resource, deploy, and docs folders. "
        "The go.mod file confirms HashiCorp Raft v1.7.0 is correctly configured as the authoritative consensus library."
    )
    add_callout(doc, "Phase 1 Checkpoint: Project structure and master dependencies successfully verified.", "NOTE")

    # ==========================================
    # PHASE 2
    # ==========================================
    add_heading_1(doc, "Phase 2 — System Architecture & Data Flow Inspection")
    add_paragraph(doc, "To trace the complete lifecycle of a distributed lock mutation from edge ingress through consensus replication to audit persistence.", bold_prefix="Aim / Objective: ")
    add_paragraph(doc, "Repository architecture documentation in docs/final-system-architecture.md and rendered Mermaid diagrams.", bold_prefix="Prerequisites & Requirements: ")
    add_paragraph(doc,
        "Every lock operation (acquire, renew, release) must be executed as a replicated state machine command. "
        "The API Gateway receives the client request, verifies the bearer JWT token, resolves the active Raft leader, and forwards the command. "
        "The leader writes the command to its local BoltDB log, dispatches AppendEntries RPCs to follower peers, and waits for majority "
        "quorum acknowledgement ($Q = \\lfloor 3/2 \\rfloor + 1 = 2$). Once committed, the command is applied to the deterministic in-memory FSM.",
        bold_prefix="Core Concept & Theory: "
    )

    add_figure(doc, "cookbook-evidence/diag_lock-acquisition-flow.png",
        "Figure 4.1 — End-to-End Lock Acquisition & Replication Sequence",
        "Sequence diagram illustrating edge rate-limiting, leader forwarding, Raft log replication across quorum, "
        "in-memory FSM application, monotonic token issuance, and asynchronous audit ingestion."
    )

    add_heading_2(doc, "2.1 Step-by-Step Procedure & Verification")
    add_bullet(doc, "Verify the four operational states of the lock finite state machine: FREE, HELD, EXPIRED, and FAIL_CLOSED.", "Step 1: ")
    add_bullet(doc, "Confirm the architectural decoupling: Raft is the sole authoritative state engine; PostgreSQL is non-authoritative.", "Step 2: ")
    add_bullet(doc, "Verify that downstream sensitive storage enforces the Martin Kleppmann monotonic fencing token pattern.", "Step 3: ")
    add_callout(doc, "Phase 2 Checkpoint: System architecture and data flow verified against formal distributed systems requirements.", "NOTE")

    # ==========================================
    # PHASE 3
    # ==========================================
    add_heading_1(doc, "Phase 3 — Development Environment Preparation")
    add_paragraph(doc, "To verify the local Windows host toolchain, including Go, Node.js, npm, Docker, and Docker Compose.", bold_prefix="Aim / Objective: ")
    add_paragraph(doc, "Windows 10/11 host with Docker Desktop running and terminal execution permissions.", bold_prefix="Prerequisites & Requirements: ")
    add_paragraph(doc,
        "A reliable distributed systems test environment requires synchronized container virtualization and native developer runtimes. "
        "Docker Desktop provides the container engine running the 9-node microservice stack on Linux containers (WSL2 backend), "
        "while native Go and Node.js runtimes enable local unit testing, static analysis (go vet), and frontend compilation.",
        bold_prefix="Core Concept & Theory: "
    )

    add_heading_2(doc, "3.1 Step-by-Step Execution Commands")
    add_code_block(doc,
        "$env:PATH = 'D:\\DE-Tools\\go\\bin;D:\\DE-Tools\\nodejs;' + $env:PATH\n"
        "go version\nnode --version\nnpm --version\ndocker --version\ndocker compose version",
        "PowerShell"
    )

    add_figure(doc, "cookbook-evidence/term_01_tool_versions.png",
        "Figure 4.2 — Development Toolchain Version Verification Output",
        "PowerShell terminal capture proving Go 1.22+, Node.js v20+, npm v10+, Docker v27+, and Docker Compose v2.29+ are operational."
    )

    add_heading_2(doc, "3.2 Troubleshooting Common Environment Errors")
    add_bullet(doc, "Cause: Docker Desktop service is stopped. Solution: Launch Docker Desktop from the Start menu and wait for the green 'Engine Running' indicator.", "Docker daemon not responding: ")
    add_bullet(doc, "Cause: Local Go or Node binaries not on system PATH. Solution: Prepend the toolchain directory: $env:PATH = 'D:\\DE-Tools\\go\\bin;' + $env:PATH.", "Command not recognized: ")
    add_callout(doc, "Phase 3 Checkpoint: Development environment verified with zero missing prerequisites.", "NOTE")

    # ==========================================
    # PHASE 4
    # ==========================================
    add_heading_1(doc, "Phase 4 — Project Configuration & Secret Handling")
    add_paragraph(doc, "To configure environment variables, database credentials, JWT secrets, and audit safety parameters.", bold_prefix="Aim / Objective: ")
    add_paragraph(doc, "Phase 3 completed; access to .env.example in the repository root.", bold_prefix="Prerequisites & Requirements: ")
    add_paragraph(doc,
        "Production distributed systems mandate strict separation of configuration from code. In LOCKGUARD, configuration is loaded "
        "from environment variables with safe defaults for development. When running in production mode (ENVIRONMENT=production), "
        "the configuration parser enforces strict entropy checks, rejecting weak passwords, default signing keys, and unencrypted database connections.",
        bold_prefix="Core Concept & Theory: "
    )

    add_heading_2(doc, "4.1 Important Source Code: Production Configuration Guards")
    code_config = """// File: backend/internal/config/config.go
func (c *Config) Validate() []string {
    var errs []string
    if c.Environment == "production" {
        if c.Auth.JWTSecretKey == "lockguard_default_secret_key_change_in_production_min32" {
            errs = append(errs, "production environment requires a secure, non-default JWT_SECRET_KEY")
        }
        if len(c.Auth.JWTSecretKey) < 32 {
            errs = append(errs, "JWT_SECRET_KEY must be at least 32 characters in production")
        }
        if c.Database.Password == "lockguard_password" {
            errs = append(errs, "production environment requires a secure, non-default DB_PASSWORD")
        }
        if c.Database.SSLMode == "disable" {
            errs = append(errs, "production environment requires secure DB_SSL_MODE")
        }
    }
    return errs
}"""
    add_code_block(doc, code_config, "backend/internal/config/config.go")
    add_paragraph(doc,
        "Code Explanation: The Validate() method executes at server bootstrap. If the environment is set to 'production', "
        "the system fails closed if any default secret is detected. Additionally, the Config struct implements fmt.Stringer "
        "to automatically mask all passwords, tokens, and keys as '[REDACTED]' whenever configuration structs are logged.",
        italic=True
    )

    add_heading_2(doc, "4.2 Configuration Procedure")
    add_bullet(doc, "Copy the example environment template to create your local .env file:", "Step 1: ")
    add_code_block(doc, "Copy-Item .env.example .env\nGet-Content .env -TotalCount 25", "PowerShell")
    add_bullet(doc, "Validate the interpolated Docker Compose configuration:", "Step 2: ")
    add_code_block(doc, "docker compose config --quiet", "PowerShell")
    add_callout(doc, "Phase 4 Checkpoint: Configuration and secret safety rules successfully validated.", "NOTE")

    # ==========================================
    # PHASE 5
    # ==========================================
    add_heading_1(doc, "Phase 5 — Docker Container Build & Cluster Startup")
    add_paragraph(doc, "To build all microservice Docker images and start the 9-service containerized topology.", bold_prefix="Aim / Objective: ")
    add_paragraph(doc, "Docker Desktop running with at least 4GB RAM allocated.", bold_prefix="Prerequisites & Requirements: ")
    add_paragraph(doc,
        "Docker Compose orchestrates the full multi-tier topology: three independent Raft coordinator nodes with persistent BoltDB volumes, "
        "the Go API Gateway, the PostgreSQL 16 audit database, the Protected Resource fencing service, the Prometheus scraper, "
        "the React Vite frontend, and the Nginx reverse proxy. Containers communicate over two isolated bridge networks: "
        "'lockguard-raft' (consensus transport) and 'lockguard-internal' (gateway and database communication).",
        bold_prefix="Core Concept & Theory: "
    )

    add_heading_2(doc, "5.1 Execution Commands & Service Startup")
    add_code_block(doc,
        "docker compose build --pull=false\n"
        "docker compose up -d\n"
        "docker compose ps",
        "PowerShell"
    )

    add_figure(doc, "cookbook-evidence/term_02_docker_ps.png",
        "Figure 4.3 — Docker Compose Service Fleet Status Output",
        "Terminal capture showing all 9 containers (api, coordinator-a, coordinator-b, coordinator-c, frontend, "
        "nginx, postgres, prometheus, protected-resource) reporting Up and (healthy)."
    )

    add_callout(doc, "Phase 5 Checkpoint: All 9 microservices compiled, started, and health-verified.", "NOTE")

    # ==========================================
    # PHASE 6
    # ==========================================
    add_heading_1(doc, "Phase 6 — Service Health Verification & Web Dashboard")
    add_paragraph(doc, "To verify reverse proxy routing, execute HTTP health probes, and access the web dashboard.", bold_prefix="Aim / Objective: ")
    add_paragraph(doc, "All 9 Docker containers reporting healthy status on host port 80.", bold_prefix="Prerequisites & Requirements: ")
    add_paragraph(doc,
        "Nginx serves as the single point of entry on port 80. Static web requests are routed directly to the React application, "
        "while API calls matching /api/ are proxied to the Go API Gateway (http://lockguard_api/). "
        "The Go API Gateway registers both /health and /api/v1/health on its ServeMux, ensuring zero routing ambiguity across proxy rewrites.",
        bold_prefix="Core Concept & Theory: "
    )

    add_heading_2(doc, "6.1 Important Source Code: Nginx Ingress & Health Checks")
    code_nginx = """# File: deploy/nginx/nginx.conf
location /api/ {
    proxy_pass http://lockguard_api/;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Request-ID $request_id;
}
location / {
    proxy_pass http://lockguard_frontend;
    proxy_set_header Host $host;
    try_files $uri $uri/ /index.html;
}"""
    add_code_block(doc, code_nginx, "deploy/nginx/nginx.conf")

    add_heading_2(doc, "6.2 Live Health Probe Verification")
    add_code_block(doc, "curl.exe -i -s http://localhost/api/v1/health", "PowerShell")

    add_figure(doc, "cookbook-evidence/term_03_health_check.png",
        "Figure 4.4 — HTTP Liveness Probe Response from API Gateway",
        "Terminal capture showing HTTP 200 OK, JSON response envelope, and node_id identification via Nginx proxy."
    )

    add_figure(doc, "cookbook-evidence/fig_02_admin_overview.png",
        "Figure 4.5 — LOCKGUARD React Operator Dashboard (Overview View)",
        "Live web dashboard at http://localhost/ displaying 'API Online' green pulse badge, quorum status (2 of 3), "
        "consensus topology, milestone verification grid, and live backend JSON telemetry."
    )

    add_callout(doc, "Phase 6 Checkpoint: Ingress proxy and operator dashboard operational with real-time health telemetry.", "NOTE")

    # ==========================================
    # PHASE 7
    # ==========================================
    add_heading_1(doc, "Phase 7 — Authentication & Role-Based Access Control (RBAC)")
    add_paragraph(doc, "To authenticate users, generate cryptographically signed HS256 JWT tokens, and enforce role permissions.", bold_prefix="Aim / Objective: ")
    add_paragraph(doc, "API service running and demo credentials available.", bold_prefix="Prerequisites & Requirements: ")
    add_paragraph(doc,
        "Distributed lock services require zero-trust identity verification. LOCKGUARD implements stateless RFC 7519 "
        "HMAC-SHA256 (HS256) JWT authentication with zero external dependencies using Go's standard library crypto/hmac. "
        "Operations are segregated across three roles: ADMIN (full cluster inspection, stepdown, emergency unlock), "
        "CLIENT (acquire, renew, release owned locks), and READ_ONLY (auditors; query status and events; mutations return 403 Forbidden).",
        bold_prefix="Core Concept & Theory: "
    )

    add_figure(doc, "cookbook-evidence/fig_01_login_page.png",
        "Figure 4.6 — LOCKGUARD Authenticated Access Interface with Demo Accounts",
        "Login screen at http://localhost/login featuring dark navy palette, secure credentials inputs, "
        "and quick-fill buttons for Admin, Operator 1, Operator 2, and Auditor."
    )

    add_heading_2(doc, "7.1 Important Source Code: Zero-Dependency JWT Verification")
    code_jwt = """// File: backend/internal/auth/jwt.go
func (s *TokenService) ValidateToken(tokenString string) (*Claims, error) {
    parts := strings.Split(tokenString, ".")
    if len(parts) != 3 {
        return nil, ErrMalformedToken
    }
    // Verify Header Algorithm is strictly HS256 (prevent 'none' attack)
    headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
    if err != nil { return nil, ErrMalformedToken }
    var header Header
    if err := json.Unmarshal(headerBytes, &header); err != nil || header.Algorithm != "HS256" {
        return nil, ErrInvalidAlgorithm
    }
    // Constant-time signature comparison to prevent timing attacks
    expectedSig := s.computeHMAC(parts[0] + "." + parts[1])
    givenSig, err := base64.RawURLEncoding.DecodeString(parts[2])
    if err != nil || !hmac.Equal(expectedSig, givenSig) {
        return nil, ErrInvalidSignature
    }
    // Claims validation and expiration check
    var claims Claims
    payloadBytes, _ := base64.RawURLEncoding.DecodeString(parts[1])
    _ = json.Unmarshal(payloadBytes, &claims)
    if time.Now().Unix() > claims.ExpiresAt {
        return nil, ErrExpiredToken
    }
    return &claims, nil
}"""
    add_code_block(doc, code_jwt, "backend/internal/auth/jwt.go")
    add_paragraph(doc,
        "Code Explanation: The ValidateToken function prevents the classic 'alg: none' vulnerability by strictly decoding "
        "the header JSON and verifying header.Algorithm == 'HS256'. Signatures are verified using hmac.Equal() which operates "
        "in constant time, preventing side-channel timing attacks that attempt to deduce signature bytes.",
        italic=True
    )

    add_heading_2(doc, "7.2 Seeded Operational Accounts")
    headers_auth = ["Username", "Password", "Assigned Role", "Bound Owner ID", "Operational Purpose"]
    rows_auth = [
        ["admin", "admin_secure_pass_phase5", "ADMIN", "admin-system", "Full administrative control, stepdown, emergency unlock, and global audit."],
        ["client_worker", "worker_secure_pass_phase5", "CLIENT", "worker-1", "Primary distributed lock worker client for acquire, renew, and release."],
        ["client_worker_2", "worker_secure_pass_phase5", "CLIENT", "worker-2", "Secondary distributed lock worker client for mutual exclusion testing."],
        ["auditor", "auditor_secure_pass_phase5", "READ_ONLY", "auditor-01", "Compliance auditor; read-only access to locks, telemetry, and audit trail."]
    ]
    add_table(doc, headers_auth, rows_auth, [1.2, 1.8, 1.0, 1.1, 1.4])
    add_callout(doc, "Phase 7 Checkpoint: Cryptographic authentication and RBAC boundaries fully operational.", "NOTE")

    # ==========================================
    # PHASE 8
    # ==========================================
    add_heading_1(doc, "Phase 8 — Deterministic Lock State Machine & Logical Time")
    add_paragraph(doc, "To verify deterministic state transitions, idempotency handling, and monotonic logical time in the lock state machine.", bold_prefix="Aim / Objective: ")
    add_paragraph(doc, "Go source code in backend/internal/lockstate/.", bold_prefix="Prerequisites & Requirements: ")
    add_paragraph(doc,
        "In replicated state machine consensus, all replicas must transition through identical states given identical logs. "
        "The lockstate.Engine achieves absolute determinism by never invoking time.Now() during command execution. "
        "Instead, logical time is calculated monotonically as: effectiveNow = max(cmd.TimestampUnixNano, s.lastAppliedUnixNano). "
        "This guarantees that lease expirations and grant timestamps remain bit-for-bit identical across all nodes.",
        bold_prefix="Core Concept & Theory: "
    )

    add_heading_2(doc, "8.1 Important Source Code: Deterministic State Engine")
    code_fsm = """// File: backend/internal/lockstate/fsm.go
func (s *Engine) AcquireLock(cmd AcquireLockCommand) Result {
    if err := cmd.Validate(); err != nil {
        return Result{Success: false, Code: ErrCodeInvalidResource, Message: err.Error()}
    }
    // Check idempotency cache to safely handle network retries
    if cached, exists := s.checkIdempotency(cmd.RequestID, cmd.Fingerprint()); exists {
        return cached
    }
    // Deterministic logical time progression
    effectiveNow := cmd.TimestampUnixNano
    if effectiveNow < s.lastAppliedUnixNano {
        effectiveNow = s.lastAppliedUnixNano
    }
    s.lastAppliedUnixNano = effectiveNow

    res, exists := s.resources[cmd.ResourceName]
    if !exists {
        res = &ResourceState{ResourceName: cmd.ResourceName}
        s.resources[cmd.ResourceName] = res
    }
    // Check if actively held by another client
    if res.ActiveLock != nil && effectiveNow < res.ActiveLock.ExpiresAtUnixNano {
        return Result{Success: false, Code: ErrCodeLockAlreadyHeld, Message: "lock is currently held"}
    }
    // Advance strictly monotonic fencing token
    res.HighestToken++
    newToken := res.HighestToken

    res.ActiveLock = &LockRecord{
        ResourceName: cmd.ResourceName,
        OwnerID:      cmd.OwnerID,
        FencingToken: newToken,
        LeaseVersion: 1, // Starts at version 1
        ExpiresAtUnixNano: effectiveNow + cmd.TTLNanoseconds,
    }
    res.History = append(res.History, *res.ActiveLock)
    result := Result{Success: true, FencingToken: newToken, LeaseVersion: 1}
    s.recordIdempotency(cmd.RequestID, cmd.Fingerprint(), result)
    return result
}"""
    add_code_block(doc, code_fsm, "backend/internal/lockstate/fsm.go")
    add_paragraph(doc,
        "Code Explanation: The AcquireLock function executes under exclusive mutex locking. If a lock is held and unexpired, "
        "it rejects the mutation immediately with ErrCodeLockAlreadyHeld. If granted, HighestToken increments strictly by 1, "
        "LeaseVersion initializes to 1, and the result is cached by RequestID to provide idempotency on network replays.",
        italic=True
    )
    add_callout(doc, "Phase 8 Checkpoint: Deterministic state engine invariants and monotonic logical clock verified.", "NOTE")

    # ==========================================
    # PHASE 9
    # ==========================================
    add_heading_1(doc, "Phase 9 — Distributed Lock Acquisition Operation")
    add_paragraph(doc, "To execute a live distributed lock acquisition request through the API Gateway and observe quorum commitment.", bold_prefix="Aim / Objective: ")
    add_paragraph(doc, "Docker cluster running and valid client bearer token acquired.", bold_prefix="Prerequisites & Requirements: ")
    add_paragraph(doc,
        "When a client requests a lock, the API Gateway proxies the request to the authoritative Raft leader. "
        "The leader proposes an AcquireLockCommand to the Raft cluster, replicates it across peers, commits it to BoltDB, "
        "applies it to the state machine, and issues an ascending 64-bit fencing token and Lease Version 1.",
        bold_prefix="Core Concept & Theory: "
    )

    add_heading_2(doc, "9.1 Execution Command & Response Capture")
    add_code_block(doc,
        "$token = (Invoke-RestMethod -Uri 'http://localhost/api/v1/auth/login' -Method Post -Body '{\"username\":\"client_worker\",\"password\":\"worker_secure_pass_phase5\"}' -ContentType 'application/json').data.token\n"
        "$body = '{\"resource_name\":\"demo-critical-res\",\"owner_id\":\"worker-1\",\"ttl_seconds\":60}'\n"
        "Invoke-RestMethod -Uri 'http://localhost/api/v1/locks/acquire' -Method Post -Headers @{Authorization=\"Bearer $token\"} -Body $body -ContentType 'application/json' | ConvertTo-Json -Depth 4",
        "PowerShell"
    )

    add_figure(doc, "cookbook-evidence/term_05_lock_acquire.png",
        "Figure 4.7 — Live HTTP Lock Acquisition Response Output",
        "Terminal capture showing HTTP 200 OK response from API Gateway: resource 'demo-critical-res' acquired by 'worker-1' with fencing_token: 1."
    )

    add_figure(doc, "cookbook-evidence/fig_04_locks_management.png",
        "Figure 4.8 — React Locks Management Dashboard with Active Lease",
        "Web dashboard table displaying the active lock on 'demo-critical-res', owner 'worker-1', active fencing token, "
        "real-time countdown timer, and role-appropriate 'Release' action button."
    )
    add_callout(doc, "Phase 9 Checkpoint: Distributed lock successfully acquired and replicated across Raft quorum.", "NOTE")

    # ==========================================
    # PHASE 10
    # ==========================================
    add_heading_1(doc, "Phase 10 — Mutual Exclusion & Contention Handling")
    add_paragraph(doc, "To verify that a competing client is blocked from acquiring an actively held lock, and cannot tamper with another client's lease.", bold_prefix="Aim / Objective: ")
    add_paragraph(doc, "Active lock on 'demo-critical-res' held by 'worker-1'; separate credentials for 'worker-2'.", bold_prefix="Prerequisites & Requirements: ")
    add_paragraph(doc,
        "Mutual exclusion mandates that at most one client can hold a lock on a named resource at any point in physical time ($|Holders(R, t)| \\le 1$). "
        "Any concurrent acquisition attempt by another owner must be rejected with HTTP 409 Conflict. "
        "Furthermore, cross-client ownership isolation must reject release attempts by unauthorized clients with HTTP 403 Forbidden.",
        bold_prefix="Core Concept & Theory: "
    )

    add_heading_2(doc, "10.1 Execution Commands & Conflict Verifications")
    add_code_block(doc,
        "# Authenticate as secondary worker (worker-2)\n"
        "$w2_tok = (Invoke-RestMethod -Uri 'http://localhost/api/v1/auth/login' -Method Post -Body '{\"username\":\"client_worker_2\",\"password\":\"worker_secure_pass_phase5\"}' -ContentType 'application/json').data.token\n\n"
        "# Attempt 1: Competing acquire on held resource (Expect 409 Conflict)\n"
        "curl.exe -i -s -X POST http://localhost/api/v1/locks/acquire -H \"Authorization: Bearer $w2_tok\" -H \"Content-Type: application/json\" -d '{\"resource_name\":\"demo-critical-res\",\"owner_id\":\"worker-2\",\"ttl_seconds\":60}'\n\n"
        "# Attempt 2: Unauthorized cross-client release attempt (Expect 403 Forbidden)\n"
        "curl.exe -i -s -X POST http://localhost/api/v1/locks/release -H \"Authorization: Bearer $w2_tok\" -H \"Content-Type: application/json\" -d '{\"resource_name\":\"demo-critical-res\",\"owner_id\":\"worker-2\",\"fencing_token\":1}'",
        "PowerShell"
    )

    add_figure(doc, "cookbook-evidence/term_06_competing_conflict.png",
        "Figure 4.9 — Mutual Exclusion Contention Conflict (HTTP 409)",
        "Terminal capture showing lock acquisition conflict: HTTP 409 Conflict with error code ERR_LOCK_CONFLICT."
    )

    add_figure(doc, "cookbook-evidence/term_07_cross_release_denial.png",
        "Figure 4.10 — Cross-Client Release Tamper Rejection (HTTP 403)",
        "Terminal capture showing unauthorized release rejection: HTTP 403 Forbidden with error code ERR_OWNER_MISMATCH."
    )

    add_figure(doc, "cookbook-evidence/fig_10_operator_locks_view.png",
        "Figure 4.11 — Operator Locks View Showing Non-Owned Lock Gating",
        "Web dashboard viewed by worker-1: displays 'Locked (Held by worker-2)' with release buttons disabled for competitor locks."
    )
    add_callout(doc, "Phase 10 Checkpoint: Mutual exclusion and cross-client ownership isolation verified with zero leaks.", "NOTE")
