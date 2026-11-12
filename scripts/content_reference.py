from scripts.docx_helpers import (
    add_heading_1, add_heading_2, add_heading_3, add_paragraph, add_bullet,
    add_callout, add_code_block, add_figure, add_table
)

def build_reference_chapters(doc):
    # =========================================================================
    # CHAPTER 5: REST API SPECIFICATION
    # =========================================================================
    add_heading_1(doc, "Chapter 5 — Authoritative REST API Specification")
    add_paragraph(doc,
        "This chapter provides the formal, production-grade specification for all HTTP/REST endpoints exposed by the LOCKGUARD "
        "API Gateway and downstream services. All API responses adhere to a uniform JSON envelope structure containing success (boolean), "
        "data (payload object), error (string error message if failed), code (machine-readable error constant), and timestamp (RFC 3339)."
    )

    api_summary_headers = ["Endpoint", "Method", "Role / Auth", "Summary / Purpose"]
    api_summary_rows = [
        ["/api/v1/auth/login", "POST", "Public", "Authenticates credentials; issues HS256 JWT bearer token."],
        ["/api/v1/auth/me", "GET", "Authenticated", "Returns authenticated user identity, role, and permission grants."],
        ["/api/v1/health", "GET", "Public", "Gateway and upstream coordinator liveliness and readiness probe."],
        ["/api/v1/cluster/status", "GET", "Public", "Raft consensus topology, term, active leader ID, and voters."],
        ["/api/v1/recovery/cluster", "GET", "Public", "Disaster recovery readiness, active coordinator nodes count."],
        ["/api/v1/locks/acquire", "POST", "CLIENT / ADMIN", "Acquires exclusive lock; issues monotonic fencing token."],
        ["/api/v1/locks/renew", "POST", "CLIENT / ADMIN", "Extends lease TTL; preserves fencing token; advances version."],
        ["/api/v1/locks/release", "POST", "CLIENT / ADMIN", "Releases held lock; clears lease; preserves highest token."],
        ["/api/v1/locks/active", "GET", "Authenticated", "Lists active locks. Supports ?stale=false for linearizable read."],
        ["/api/v1/locks/admin/force-release", "POST", "ADMIN Only", "Break-glass emergency force-unlock requiring audit reason."],
        ["/api/v1/cluster/stepdown", "POST", "ADMIN Only", "Triggers voluntary Raft leadership transfer to another peer."],
        ["/api/v1/audit/logs", "GET", "Authenticated", "Queries audit trail with strict tenant isolation for operators."],
        ["/api/v1/audit/stats", "GET", "Authenticated", "Exposes asynchronous audit pipeline buffer and worker stats."],
        ["/metrics", "GET", "Prometheus", "Prometheus text format exposition for scrapers on port 8082."],
        ["http://localhost:9000/api/v1/data", "POST", "Protected", "Downstream storage write validating monotonic fencing token."]
    ]
    add_table(doc, api_summary_headers, api_summary_rows, [2.0, 0.8, 1.2, 2.5])

    add_heading_2(doc, "5.1 Authentication & Identity Endpoints")
    add_paragraph(doc, "POST /api/v1/auth/login", bold_prefix="Endpoint: ")
    add_paragraph(doc, "Authenticates user credentials against the secure identity directory and issues a signed JSON Web Token (HS256).")
    add_code_block(doc,
        "// Request Payload\n"
        "{\n"
        '  "username": "client_worker",\n'
        '  "password": "worker_secure_pass_phase5"\n'
        "}\n\n"
        "// Success Response (HTTP 200 OK)\n"
        "{\n"
        '  "success": true,\n'
        '  "data": {\n'
        '    "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.ey...",\n'
        '    "expires_at": "2026-11-10T14:30:00Z",\n'
        '    "user": {\n'
        '      "id": "usr-worker-1",\n'
        '      "username": "client_worker",\n'
        '      "role": "CLIENT",\n'
        '      "permissions": ["lock:acquire", "lock:renew", "lock:release", "audit:read_own"]\n'
        "    }\n"
        "  },\n"
        '  "timestamp": "2026-11-10T13:30:00.123456Z"\n'
        "}",
        "HTTP REST Specification"
    )

    add_heading_2(doc, "5.2 Distributed Lock Lifecycle Endpoints")
    add_paragraph(doc, "POST /api/v1/locks/acquire", bold_prefix="Endpoint: ")
    add_paragraph(doc, "Proposes an exclusive lock acquisition command to the Raft consensus leader.")
    add_code_block(doc,
        "// Request Payload\n"
        "{\n"
        '  "resource_name": "demo-critical-res",\n'
        '  "owner_id": "worker-1",\n'
        '  "ttl_seconds": 60\n'
        "}\n\n"
        "// Success Response (HTTP 200 OK)\n"
        "{\n"
        '  "success": true,\n'
        '  "code": "OK",\n'
        '  "message": "lock acquired successfully",\n'
        '  "resource_name": "demo-critical-res",\n'
        '  "owner_id": "worker-1",\n'
        '  "fencing_token": 1,\n'
        '  "lease_version": 1,\n'
        '  "acquired_at_unix_nano": 1794297600000000000,\n'
        '  "expires_at_unix_nano": 1794297660000000000,\n'
        '  "timestamp_unix_nano": 1794297600000000000\n'
        "}\n\n"
        "// Error Response — Conflict (HTTP 409 Conflict)\n"
        "{\n"
        '  "success": false,\n'
        '  "code": "ERR_LOCK_CONFLICT",\n'
        '  "message": "lock on resource demo-critical-res is currently held by worker-1",\n'
        '  "timestamp": "2026-11-10T13:30:05.654321Z"\n'
        "}",
        "HTTP REST Specification"
    )

    add_paragraph(doc, "POST /api/v1/locks/renew", bold_prefix="Endpoint: ")
    add_paragraph(doc, "Extends an active lease TTL while preserving the generational fencing token and incrementing lease_version.")
    add_code_block(doc,
        "// Request Payload\n"
        "{\n"
        '  "resource_name": "demo-critical-res",\n'
        '  "owner_id": "worker-1",\n'
        '  "fencing_token": 1,\n'
        '  "ttl_seconds": 60\n'
        "}\n\n"
        "// Success Response (HTTP 200 OK)\n"
        "{\n"
        '  "success": true,\n'
        '  "code": "OK",\n'
        '  "message": "lease renewed successfully",\n'
        '  "resource_name": "demo-critical-res",\n'
        '  "owner_id": "worker-1",\n'
        '  "fencing_token": 1,\n'
        '  "lease_version": 2,\n'
        '  "expires_at_unix_nano": 1794297720000000000,\n'
        '  "timestamp_unix_nano": 1794297660000000000\n'
        "}",
        "HTTP REST Specification"
    )

    add_paragraph(doc, "POST /api/v1/locks/release", bold_prefix="Endpoint: ")
    add_paragraph(doc, "Releases an active lock, clearing lease ownership while preserving the highest fencing token counter.")
    add_code_block(doc,
        "// Request Payload\n"
        "{\n"
        '  "resource_name": "demo-critical-res",\n'
        '  "owner_id": "worker-1",\n'
        '  "fencing_token": 1\n'
        "}\n\n"
        "// Success Response (HTTP 200 OK)\n"
        "{\n"
        '  "success": true,\n'
        '  "code": "OK",\n'
        '  "message": "lock released successfully",\n'
        '  "resource_name": "demo-critical-res",\n'
        '  "owner_id": "worker-1",\n'
        '  "fencing_token": 1,\n'
        '  "timestamp_unix_nano": 1794297680000000000\n'
        "}",
        "HTTP REST Specification"
    )

    # =========================================================================
    # CHAPTER 6: TROUBLESHOOTING MANUAL
    # =========================================================================
    add_heading_1(doc, "Chapter 6 — Comprehensive Troubleshooting Manual (18 Genuine Issues)")
    add_paragraph(doc,
        "This chapter serves as the definitive field guide and diagnostic reference for operating LOCKGUARD under production, "
        "test, and academic evaluation conditions. It documents exactly 18 genuine distributed systems, network, and container "
        "operational failure scenarios, providing root cause analysis, diagnostic commands, and verified remediation steps."
    )

    troubleshoot_data = [
        ("ISS-01", "Host Port 80 / 8080 / 8082 Conflict with Local Web Server",
         "Nginx or API Gateway fails to bind. Error: bind: address already in use.",
         "IIS, Apache, or another development process is already bound to port 80 or 8080 on the host OS.",
         "Get-Process -Id (Get-NetTCPConnection -LocalPort 80 -ErrorAction SilentlyContinue).OwningProcess",
         "Stop the conflicting service or adjust host port mapping in docker-compose.yml (e.g. 8088:80).",
         "curl.exe http://localhost/api/v1/health returns HTTP 200 OK."),

        ("ISS-02", "PostgreSQL Container Health Check Startup Timeout",
         "API Gateway fails to start with error: 'waiting for postgres: connection refused'.",
         "PostgreSQL container initialization (initdb and table migrations) exceeds default 5-second compose health check threshold.",
         "docker logs lockguard-postgres | Select-String 'database system is ready to accept connections'",
         "Ensure depends_on condition is service_healthy with start_period: 15s in docker-compose.yml.",
         "docker compose ps shows lockguard-postgres as healthy."),

        ("ISS-03", "BoltDB File Lock Contention on Fast Restart",
         "Coordinator container crashes with 'timeout acquiring bolt lock' or 'database locked'.",
         "Previous coordinator process did not release OS flock on bolt.db before container restart.",
         "docker logs lockguard-coordinator-a | Select-String 'bolt'",
         "Wait 5 seconds for kernel flock release or safely restart container with docker restart.",
         "Coordinator logs display 'bolt store initialized successfully'."),

        ("ISS-04", "Raft Quorum Loss Due to Minority Partition ($f \\ge 2$)",
         "HTTP requests to /locks/acquire return HTTP 503 Service Unavailable (ERR_QUORUM_UNAVAILABLE).",
         "Two out of three coordinator nodes are down. Surviving single node cannot reach quorum ($1 < 2$).",
         "Invoke-RestMethod -Uri 'http://localhost/api/v1/cluster/status'",
         "Restart stopped coordinator containers (docker start lockguard-coordinator-b) to re-establish quorum.",
         "Cluster status reports is_leader: true and voters: 3."),

        ("ISS-05", "JWT Bearer Token Expiration or Signing Key Mismatch",
         "Client requests return HTTP 401 Unauthorized with code ERR_UNAUTHORIZED.",
         "Client token has expired past its 1-hour validity window, or JWT_SECRET changed between service restarts.",
         "Inspect token expiry timestamp at jwt.io or test with refreshed token.",
         "Re-authenticate via POST /api/v1/auth/login and include updated Bearer header in requests.",
         "curl with new token returns HTTP 200 OK."),

        ("ISS-06", "Operator Cross-Tenant Release Denial (ERR_OWNER_MISMATCH)",
         "Worker-2 attempts to release lock held by Worker-1, receives HTTP 403 Forbidden.",
         "Strict owner isolation invariant: only the active leaseholder can explicitly release a lock.",
         "curl.exe -i -X POST http://localhost/api/v1/locks/release ...",
         "This is expected and correct behavior. If emergency release is required, invoke admin force-release.",
         "Audit trail records rejected release attempt with security alert level."),

        ("ISS-07", "Competing Lock Acquisition Conflict (HTTP 409 Conflict)",
         "Worker-2 attempts to acquire held resource and receives HTTP 409 Conflict.",
         "Mutual exclusion invariant: a resource can have at most one holder ($|Holders| \\le 1$).",
         "curl.exe -s http://localhost/api/v1/locks/active | ConvertFrom-Json",
         "Client must wait for lease TTL expiration or poll with exponential backoff until freed.",
         "Lock acquisition succeeds once the resource status transitions to FREE."),

        ("ISS-08", "Stale Fencing Token Rejection at Protected Resource",
         "Write to protected storage on port 9000 returns HTTP 409 ERR_STALE_FENCING_TOKEN.",
         "Worker paused during GC; lease expired; competitor acquired newer token. Storage rejected stale write.",
         "curl.exe -i -X POST http://localhost:9000/api/v1/data ...",
         "Worker must detect 409, abort its current stale batch, and re-acquire a fresh lock from coordinator.",
         "Subsequent write with new token succeeds."),

        ("ISS-09", "Unannounced Leader Node Crash Re-Election Delay",
         "In-flight write times out during the 200ms leader failover window.",
         "Active Raft leader crashed. Follower election timers (150ms-300ms) are firing.",
         "docker logs lockguard-coordinator-b | Select-String 'became leader'",
         "Clients should configure retry middleware with 500ms timeout to automatically retry against new leader.",
         "Second request succeeds automatically against newly elected leader."),

        ("ISS-10", "Nginx Reverse Proxy Bad Gateway (HTTP 502)",
         "Nginx returns HTTP 502 Bad Gateway for all /api/v1/ requests.",
         "API Gateway container is restarting or internal DNS resolution failed on Docker bridge network.",
         "docker logs lockguard-nginx | Select-String 'error'",
         "Verify lockguard-api container is running and healthy on lockguard-internal network.",
         "Nginx forwards requests cleanly with sub-millisecond proxy latency."),

        ("ISS-11", "Asynchronous Audit Pipeline Buffer Saturation",
         "High-throughput benchmark causes createdCount to exceed persistedCount significantly.",
         "PostgreSQL disk I/O bottleneck causing in-memory audit channel queue to fill.",
         "Invoke-RestMethod -Uri 'http://localhost/api/v1/audit/stats'",
         "Increase BatchSize to 100 or scale PostgreSQL storage IOPS; consensus remains unblocked.",
         "Queue size drains to 0 once batch flusher catches up."),

        ("ISS-12", "TypeScript Strict Compiler Errors on Frontend Build",
         "npm run build fails with type checking errors in vite build.",
         "Mismatch between API response interfaces and component prop types.",
         "cd frontend; npx tsc --noEmit",
         "Align TypeScript interfaces with backend response structs in src/types/.",
         "npm run build finishes with Exit Code 0 and outputs dist/ folder."),

        ("ISS-13", "Docker Named Volume Permissions Conflict on Linux / WSL2",
         "PostgreSQL or BoltDB container exits with 'permission denied' inside volume mount.",
         "Host UID/GID mismatch between Windows Docker host and Linux container internal user.",
         "docker compose logs lockguard-postgres | Select-String 'permission'",
         "Reset volume ownership using docker run --rm -v lockguard_postgres_data:/data alpine chown -R 999:999 /data.",
         "PostgreSQL starts cleanly and accepts connections."),

        ("ISS-14", "Prometheus Scrape Target Shows DOWN on Port 9090",
         "Prometheus UI targets page shows lockguard-api as red / DOWN.",
         "Prometheus configuration points to incorrect Docker DNS name or metrics port.",
         "Inspect prometheus.yml targets: lockguard-api:8080.",
         "Ensure lockguard-api is attached to lockguard-internal network alongside prometheus.",
         "Prometheus targets page shows state UP in green."),

        ("ISS-15", "System Clock Skew Affecting Client TTL Expectations",
         "Client believes lease has 10 seconds remaining, but server reports lock expired.",
         "Host OS clock drifted relative to coordinator container logical nanosecond clock.",
         "Compare host system time with curl http://localhost/api/v1/health timestamp.",
         "Rely exclusively on coordinator-provided expires_at_unix_nano rather than client wall-clock.",
         "Client synchronizes lease renewal timers with server epoch."),

        ("ISS-16", "Stale Diagnostic Reads vs Authoritative Linearizable Reads",
         "Client queries GET /locks/active and observes briefly stale lock state on follower.",
         "Default read bypassed Raft log read-index confirmation to minimize latency.",
         "Pass query parameter ?stale=false to enforce authoritative linearizable read.",
         "Pass ?stale=false for critical invariant checks; pass ?stale=true for high-frequency dashboards.",
         "Response header X-LockGuard-Read-Consistency reports authoritative-linearizable."),

        ("ISS-17", "Untracked Administrative Force-Unlock Audit Gap",
         "Emergency unlock invoked without providing mandatory audit rationale.",
         "Administrator script bypassed UI and invoked endpoint with empty reason field.",
         "API rejects empty reason with HTTP 400 Bad Request ERR_INVALID_COMMAND.",
         "Provide structured reason string in JSON payload: {'reason': 'Node crashed without release'}.",
         "Audit trail records ACTION_ADMIN_OVERRIDE with user ID and explanation."),

        ("ISS-18", "Cascading Re-Elections Caused by Network Latency Spikes",
         "Cluster repeatedly switches leaders every few seconds under heavy load.",
         "Raft ElectionTimeout (250ms) is too tight for congested virtual network adapter.",
         "docker logs lockguard-coordinator-a | Select-String 'timeout'",
         "Adjust ElectionTimeout to 500ms and HeartbeatTimeout to 200ms in NodeConfig.",
         "Leadership stabilizes with zero spurious elections.")
    ]

    for iss_id, title, sym, cause, diag, fix, ver in troubleshoot_data:
        add_heading_2(doc, f"{iss_id} — {title}")
        add_paragraph(doc, sym, bold_prefix="Symptoms: ")
        add_paragraph(doc, cause, bold_prefix="Root Cause: ")
        add_paragraph(doc, diag, bold_prefix="Diagnostic Command: ")
        add_paragraph(doc, fix, bold_prefix="Step-by-Step Fix: ")
        add_paragraph(doc, ver, bold_prefix="Verification: ")

    # =========================================================================
    # CHAPTER 7: REQUIREMENT TRACEABILITY MATRIX
    # =========================================================================
    add_heading_1(doc, "Chapter 7 — Requirement Traceability Matrix (REQ-01 to REQ-27)")
    add_paragraph(doc,
        "The Requirement Traceability Matrix (RTM) establishes bidirectional traceability between system design requirements, "
        "their exact implementation in the codebase, and the empirical verification evidence collected from the live running cluster."
    )

    rtm_headers = ["Req ID", "Description", "Source File / Module", "Verification Evidence", "Status"]
    rtm_rows = [
        ["REQ-01", "Distributed Mutual Exclusion", "lockstate/acquire.go", "term_05_lock_acquire.png, term_06", "VERIFIED"],
        ["REQ-02", "Monotonic Fencing Tokens", "lockstate/acquire.go", "term_12_reacquire_token_monotonic.png", "VERIFIED"],
        ["REQ-03", "Finite Lease TTL & Expiration", "lockstate/expire.go", "Figure 4.8 (Countdown Timer)", "VERIFIED"],
        ["REQ-04", "Lease Renewal without Token Change", "lockstate/renew.go", "term_08_lease_renewal.png", "VERIFIED"],
        ["REQ-05", "Lease Version Monotonicity", "lockstate/renew.go", "term_08_lease_renewal.png (v1 -> v2)", "VERIFIED"],
        ["REQ-06", "Graceful Explicit Release", "lockstate/release.go", "term_11_emergency_unlock.png", "VERIFIED"],
        ["REQ-07", "Cross-Client Release Denial", "lockstate/release.go", "term_07_cross_release_denial.png", "VERIFIED"],
        ["REQ-08", "Downstream Fencing Validation", "protected-resource/fencing.go", "term_09_protected_write.png", "VERIFIED"],
        ["REQ-09", "Stale Fencing Token Rejection", "protected-resource/fencing.go", "term_10_stale_token_rejection.png", "VERIFIED"],
        ["REQ-10", "HashiCorp Raft Consensus Engine", "consensus/node.go", "term_04_cluster_status.png", "VERIFIED"],
        ["REQ-11", "Durable BoltDB Storage", "consensus/node.go", "docker-compose.yml (bolt_data volumes)", "VERIFIED"],
        ["REQ-12", "Automatic Leader Election", "consensus/node.go", "diag_raft-leader-election.png", "VERIFIED"],
        ["REQ-13", "Quorum Loss Fail-Closed Safety", "coordinator/handler.go", "Phase 17 Verification Table", "VERIFIED"],
        ["REQ-14", "Leader Stepdown Transfer", "coordinator/handler.go", "term_04_cluster_status.png", "VERIFIED"],
        ["REQ-15", "Asynchronous Non-Blocking Audit", "audit/pipeline.go", "diag_database-audit-flow.png", "VERIFIED"],
        ["REQ-16", "PostgreSQL Event Persistence", "persistence/postgres_audit_repo.go", "fig_05_audit_trail.png", "VERIFIED"],
        ["REQ-17", "Tenant-Isolated Audit Queries", "audit/handler.go", "term_13_audit_isolation.png", "VERIFIED"],
        ["REQ-18", "HS256 JWT Authentication", "auth/jwt.go", "term_05_lock_acquire.png (Bearer token)", "VERIFIED"],
        ["REQ-19", "Role-Based Access Control", "middleware/auth.go", "fig_10_operator_locks_view.png", "VERIFIED"],
        ["REQ-20", "Admin Emergency Unlock Override", "coordinator/handler.go", "term_11_emergency_unlock.png", "VERIFIED"],
        ["REQ-21", "Prometheus Telemetry Exposition", "api/server.go", "fig_13_prometheus_dashboard.png", "VERIFIED"],
        ["REQ-22", "Real-Time Telemetry Dashboard", "pages/BenchmarksPage.tsx", "fig_07_benchmarks_metrics.png", "VERIFIED"],
        ["REQ-23", "Nginx Ingress Reverse Proxy", "deploy/nginx/nginx.conf", "term_03_health_check.png", "VERIFIED"],
        ["REQ-24", "Containerized Multi-Node Topology", "docker-compose.yml", "term_02_docker_ps.png (9 containers)", "VERIFIED"],
        ["REQ-25", "Idempotent Command Replays", "lockstate/state_machine.go", "phase6_e2e_integration_test.go", "VERIFIED"],
        ["REQ-26", "Integer Overflow Protection", "lockstate/commands.go", "phase6_e2e_integration_test.go", "VERIFIED"],
        ["REQ-27", "Linearizable State Reads", "coordinator/handler.go", "term_04_cluster_status.png", "VERIFIED"]
    ]
    add_table(doc, rtm_headers, rtm_rows, [0.8, 2.0, 1.8, 1.3, 0.6])

    # =========================================================================
    # CHAPTER 8: VIVA-VOCE EXAMINATION GUIDE
    # =========================================================================
    add_heading_1(doc, "Chapter 8 — Comprehensive University Viva-Voce Examination Guide")
    add_paragraph(doc,
        "This chapter prepares the project authors for rigorous academic and industry evaluation. "
        "It is organized into three distinct tiers: Section A contains 50 rapid-fire foundational questions; "
        "Section B contains 20 in-depth architectural questions; Section C contains 10 complex edge-case failure scenarios."
    )

    add_heading_2(doc, "8.1 Section A: 50 Core Technical & Distributed Systems Q&As")
    qa_50 = [
        ("1. What is a distributed lock?", "A synchronization primitive providing mutual exclusion across multiple independent processes over a network."),
        ("2. Why do single-node mutexes fail across servers?", "They rely on shared memory and unified OS kernel primitives unavailable across networked physical hosts."),
        ("3. What is the CAP theorem?", "In any networked data system, you can guarantee at most two out of Consistency, Availability, and Partition Tolerance."),
        ("4. Which CAP classification does LOCKGUARD satisfy?", "CP (Consistency and Partition Tolerance). It rejects availability when quorum is lost to preserve safety."),
        ("5. What is the PACELC theorem?", "If Partition (P), trade-off Availability (A) vs Consistency (C); Else (E), trade-off Latency (L) vs Consistency (C)."),
        ("6. What consensus algorithm is used?", "HashiCorp Raft, an implementation of the Raft protocol by Ongaro and Ousterhout."),
        ("7. What is the quorum formula for an N-node cluster?", "Quorum Q = floor(N/2) + 1. For N=3, Q=2. For N=5, Q=3."),
        ("8. How many node failures can a 3-node cluster tolerate?", "Exactly f = 1 failure, because 3 - 1 = 2, which satisfies the quorum of 2."),
        ("9. What happens if two nodes fail in a 3-node cluster?", "Quorum is lost (1 < 2). The surviving node fails closed and rejects write operations."),
        ("10. What storage engine persists Raft logs?", "BoltDB, an embedded ACID key-value store using memory-mapped B+ trees."),
        ("11. What is a monotonic fencing token?", "A strictly ascending 64-bit integer issued with each lock acquisition to order mutations."),
        ("12. Who authored the famous paper on fencing tokens?", "Martin Kleppmann in his 2016 critique of distributed lock systems."),
        ("13. Why are time-based leases insufficient alone?", "Stop-the-world GC pauses or network delays can pause a client while its lease expires unnoticed."),
        ("14. How does the protected resource detect stale writes?", "It compares incoming token T against HighestTokenSeen. If T <= HighestTokenSeen, it rejects with 409."),
        ("15. Does lease renewal change the fencing token?", "No. The fencing token represents the ownership epoch; LeaseVersion increments instead."),
        ("16. What is the initial LeaseVersion value?", "Version 1. Each renewal increments it by 1 (1 -> 2 -> 3)."),
        ("17. What happens to fencing tokens when a lock is released?", "HighestToken is preserved. The next acquisition receives HighestToken + 1."),
        ("18. Can an operator release another operator's lock?", "No. The state machine checks active.OwnerID == cmd.OwnerID and returns HTTP 403 Forbidden."),
        ("19. Who can override a held lock without being the owner?", "An administrator using POST /api/v1/locks/admin/force-release with a mandatory audit reason."),
        ("20. What is an idempotency key?", "A client-supplied UUID hashed with command parameters to safely deduplicate network retries."),
        ("21. Why is PostgreSQL decoupled from the Raft consensus path?", "To prevent relational DB latency spikes or outages from blocking linearizable lock commits."),
        ("22. What buffer structure decouples audit logging?", "An in-memory channel buffer (capacity 10,000) drained by background flusher workers."),
        ("23. What batching strategy is used for audit events?", "Micro-batches: flush when batch reaches 50 events or flush interval ticker (50ms) fires."),
        ("24. How is tenant isolation enforced in audit queries?", "Non-admin operators have WHERE owner_id = $owner injected into SQL queries."),
        ("25. What hashing algorithm signs JWT tokens?", "HMAC-SHA256 (HS256) with a 256-bit secret."),
        ("26. What claims are embedded in the LOCKGUARD JWT?", "Subject (user ID), username, role (ADMIN/CLIENT/READ_ONLY), and expiry timestamp."),
        ("27. What role does Nginx play?", "Edge reverse proxy terminating port 80, providing URL routing, gzip compression, and rate limiting."),
        ("28. What port does Nginx listen on?", "Port 80 on the host machine."),
        ("29. What port does the API Gateway listen on inside Docker?", "Port 8080 (mapped to 8082 on the host)."),
        ("30. What port does PostgreSQL run on?", "Port 5432 internally; published to 127.0.0.1:5435 on the host."),
        ("31. What port does the Protected Resource run on?", "Port 9000 on the host."),
        ("32. What port does Prometheus run on?", "Port 9090 on the host."),
        ("33. What are the Raft coordinator internal ports?", "Port 7000 for Raft TCP transport; Port 8001 for HTTP coordinator APIs."),
        ("34. What is a split-brain condition?", "When network partition creates two disconnected sub-clusters that both believe they are the leader."),
        ("35. How does Raft prevent split-brain?", "Majority quorum requirement: an election requires Q = floor(N/2)+1 votes. Two majorities cannot exist."),
        ("36. What is Raft election timeout randomization?", "Timers randomized between 150ms-300ms so one follower times out first and wins without split votes."),
        ("37. What is an AppendEntries RPC?", "Raft message sent by leader to replicate log entries and serve as a heartbeat."),
        ("38. What is a RequestVote RPC?", "Raft message broadcast by a candidate to request ballots from peer nodes during an election."),
        ("39. What is linearizability?", "A strong consistency model where operations appear to take effect instantaneously at a point in physical time."),
        ("40. How does LOCKGUARD support linearizable reads?", "By routing reads through Raft leader confirm-index or passing ?stale=false."),
        ("41. What is a stale read?", "Reading from a follower's local state machine without verifying leader authority (?stale=true)."),
        ("42. What happens if a node's clock jumps forward by 10 minutes?", "Logical nanosecond timestamps in commands prevent state machine corruption."),
        ("43. What Go test flag detects data races?", "-race, which instruments memory accesses using ThreadSanitizer."),
        ("44. What Go test flag disables test caching?", "-count=1, forcing full execution of all unit and integration tests."),
        ("45. What frontend technology stack is used?", "React 18, TypeScript, Vite, Tailwind CSS, Lucide icons."),
        ("46. How does the frontend handle expired tokens?", "Axios interceptor detects HTTP 401 and redirects user to /login."),
        ("47. What is Prometheus pull model?", "Prometheus server periodically scrapes the HTTP /metrics endpoint exposed by targets."),
        ("48. Name three Prometheus metrics exposed by LOCKGUARD.", "lockguard_active_locks, lockguard_lock_acquisitions_total, lockguard_raft_is_leader."),
        ("49. What is chaos engineering?", "Disciplined failure injection to verify that the system recovers gracefully according to design."),
        ("50. What is the primary academic reference for Raft?", "In Search of an Understandable Consensus Algorithm by Diego Ongaro and John Ousterhout (2014).")
    ]

    for q, a in qa_50:
        add_paragraph(doc, f"{q}  —  {a}")

    add_heading_2(doc, "8.2 Section B: 20 Architectural & Implementation Invariant Q&As")
    qa_20 = [
        ("B.1 Why decouple PostgreSQL from the synchronous Raft consensus path?",
         "Relational databases introduce unpredictable latency due to disk flushes, connection pooling, and schema locking. "
         "If PostgreSQL were in the synchronous write path of lock acquisition, a database slowdown would immediately stall "
         "distributed consensus. Raft logs to high-speed BoltDB, while audit records stream asynchronously through a ring buffer."),

        ("B.2 Explain why lease renewal must never modify the fencing token.",
         "The fencing token identifies a generational epoch of exclusive access to downstream storage. "
         "If renewal generated a new token, any in-flight write dispatched by the client before renewal would be rejected "
         "by storage as stale, causing spurious write failures. The fencing token remains static; the LeaseVersion counter increments."),

        ("B.3 How does LOCKGUARD prevent arithmetic overflow on lease expiration?",
         "In lockstate/commands.go and renew.go, the state machine checks: if effectiveNow > math.MaxInt64 - cmd.TTLNanoseconds. "
         "If true, the command is rejected with ErrArithmeticOverflow before any calculation is performed."),

        ("B.4 Describe the idempotency deduplication mechanism.",
         "Clients provide a RequestID. The state machine computes a SHA-256 fingerprint over the command type, resource name, "
         "owner ID, and TTL. It maintains an in-memory cache of past results. If a network replay with the same RequestID and "
         "fingerprint arrives, the cached result is returned without mutating state or incrementing tokens."),

        ("B.5 How does the system handle leadership transfer during rolling upgrades?",
         "An administrator calls POST /api/v1/cluster/stepdown. The active leader executes raft.LeadershipTransfer(), "
         "instructing a qualified follower peer to initiate an election immediately. In-flight client requests are safely redirected."),

        ("B.6 What is the difference between fail-closed and fail-open?",
         "Fail-closed means that upon ambiguity or failure (e.g. quorum loss), the system denies access to preserve safety. "
         "Fail-open means it grants access to preserve availability. Lock services must ALWAYS fail-closed to prevent split-brain writes."),

        ("B.7 How does Nginx path rewriting work in deploy/nginx/nginx.conf?",
         "Nginx matches location /api/v1/ and proxies to http://lockguard_api_upstream/api/v1/. "
         "It preserves original client IP headers via X-Forwarded-For and X-Real-IP, allowing the backend to audit ingress origins."),

        ("B.8 How is secret masking implemented in the administrator UI?",
         "The backend returns masked strings for sensitive configuration parameters unless explicitly requested by an ADMIN. "
         "The frontend component renders password input masks with a toggleable eye icon and enforces role-based hiding."),

        ("B.9 Explain the BoltDB storage architecture used by HashiCorp Raft.",
         "BoltDB uses a single memory-mapped file organized as a B+ tree. Raft uses two distinct BoltDB instances: "
         "raft-log.db stores the append-only consensus log entries, and raft-stable.db stores critical metadata like current term and votedFor."),

        ("B.10 What happens if an operator attempts an administrative action?",
         "The AuthMiddleware checks the claims.Role extracted from the JWT token. If claims.Role != 'ADMIN', "
         "the request is halted immediately with HTTP 403 Forbidden and code ERR_FORBIDDEN before reaching coordinator handlers."),

        ("B.11 Describe the lifecycle of a Raft log entry from proposal to FSM apply.",
         "1. Gateway forwards command to leader. 2. Leader appends entry to local BoltDB. 3. Leader sends AppendEntries RPC to followers. "
         "4. When majority of followers acknowledge append, leader advances commitIndex. 5. Leader applies entry to LockFSM. 6. Result returned to client."),

        ("B.12 How does the Protected Resource handle multi-resource fencing?",
         "FencingGuard stores states in a map[string]ResourceFencingState guarded by sync.RWMutex. "
         "Each resource has an independent HighestToken counter, allowing concurrent independent operations across different resources."),

        ("B.13 What happens if the Raft log grows indefinitely?",
         "Raft implements log compaction via snapshots. When the log exceeds a configured threshold, the state machine serializes "
         "its state to a snapshot file, and BoltDB log entries prior to the snapshot index are truncated."),

        ("B.14 How does LOCKGUARD verify that an expired lock can be safely re-acquired?",
         "When an acquire request arrives for an existing resource, the state machine compares effectiveNow >= active.ExpiresAtUnixNano. "
         "If expired, it appends an expiration event to history, increments HighestToken, and assigns the new leaseholder."),

        ("B.15 Why does the audit pipeline drop events if the queue fills rather than blocking?",
         "Availability of distributed locks is paramount. Dropping audit events under extreme distress is preferable to deadlocking "
         "the entire distributed lock service. Queue drop counters are recorded in Prometheus to trigger operational alerts."),

        ("B.16 What prevents an expired client from releasing a lock?",
         "If a client delays and calls release after its TTL has expired, applyRelease checks effectiveNow >= active.ExpiresAtUnixNano "
         "and rejects the release with ErrLeaseExpired, preventing it from clobbering a newer owner's lease."),

        ("B.17 What is the purpose of Docker bridge networks in this deployment?",
         "It isolates network traffic. The internal Raft consensus traffic on port 7000 is isolated from external ingress, "
         "ensuring external clients cannot inject spoofed AppendEntries or RequestVote packets."),

        ("B.18 How does the frontend maintain real-time lock lease countdowns?",
         "The LocksPage component computes remaining time as Math.max(0, expiresAt - Date.now()) in a 1-second interval effect. "
         "When the countdown reaches 00:00, the status badge dynamically transitions from 'Active' to 'Expired'."),

        ("B.19 How does LOCKGUARD prevent timing attacks on JWT verification?",
         "The token signature verification uses hmac.Equal, which performs a constant-time byte comparison, "
         "preventing attackers from discovering valid signatures via timing discrepancies."),

        ("B.20 What are the benefits of using Go for distributed consensus software?",
         "Go offers static typing, excellent concurrency primitives (goroutines and channels), low memory footprint, "
         "fast startup times, and built-in race detection tools, making it the industry standard for cloud-native infrastructure.")
    ]

    for q, a in qa_20:
        add_heading_3(doc, q)
        add_paragraph(doc, a)

    add_heading_2(doc, "8.3 Section C: 10 Complex Edge-Case & Failure Scenario Q&As")
    qa_10 = [
        ("C.1 Scenario: The Martin Kleppmann GC Pause Problem",
         "Question: A client acquires a lock with 10s TTL. At second 9, it encounters a 15-second stop-the-world JVM or V8 GC pause. "
         "During this pause, the lease expires, and Client 2 acquires the lock and updates storage. Client 1 wakes up and tries to write. What happens?\n\n"
         "Answer: In LOCKGUARD, this failure is completely mitigated. When Client 2 acquired the lock, it received Fencing Token 2, "
         "which it presented to the protected resource. The storage recorded HighestTokenSeen = 2. When Client 1 wakes up, it presents "
         "stale Fencing Token 1. The storage detects Token 1 < 2 and immediately rejects the write with HTTP 409 ERR_STALE_FENCING_TOKEN. "
         "Storage integrity is 100% preserved."),

        ("C.2 Scenario: Asymmetric Network Partition (Leader Isolated from Quorum)",
         "Question: Node A is leader. A network partition isolates Node A from Nodes B and C, but Clients can still reach Node A. What happens?\n\n"
         "Answer: When a client attempts to acquire a lock, Node A attempts to replicate the entry to B and C. "
         "Because A cannot reach a majority quorum (1 out of 3), the commit times out. Meanwhile, Nodes B and C elect Node B as new leader. "
         "Node A recognizes it has lost leadership and steps down. No split-brain writes occur."),

        ("C.3 Scenario: Dual Concurrent Leader Elections (Split Vote)",
         "Question: The leader crashes. Nodes B and C both time out at the exact same instant and vote for themselves. What happens?\n\n"
         "Answer: Both nodes receive 1 vote each, failing to reach the required quorum of 2. Raft resolves this through randomized election "
         "timeouts (150ms-300ms). When the split vote occurs, each node picks a new randomized timer. One timer expires before the other, "
         "allowing that candidate to broadcast RequestVote and secure the majority ballot in the subsequent term."),

        ("C.4 Scenario: Client Hangs Indefinitely While Holding Lock",
         "Question: A worker acquires a critical lock with TTL 300s, starts processing, and deadlocks. How does the system recover?\n\n"
         "Answer: Two recovery paths exist: 1. Passive recovery: After 300 seconds, the coordinator marks the lease expired, allowing another worker "
         "to acquire it. 2. Active break-glass recovery: An administrator executes POST /api/v1/locks/admin/force-release with reason "
         "'Worker deadlocked', immediately freeing the resource and generating an audit alert."),

        ("C.5 Scenario: Catastrophic Host Power Failure (Cold Restart)",
         "Question: The physical server loses power instantly. All containers die ungracefully. How does LOCKGUARD recover upon power restoration?\n\n"
         "Answer: All state transitions committed prior to power failure were durably flushed to BoltDB on disk volumes (bolt_data_a, b, c). "
         "When Docker Compose restarts the containers, Raft replays the BoltDB log files into the in-memory state machine, "
         "re-elects a leader, and resumes serving requests with zero state corruption or lost fencing tokens."),

        ("C.6 Scenario: Delayed Network Packet Replay",
         "Question: A rogue or malfunctioning router delivers a duplicate acquire request packet 30 seconds after the original was processed. What happens?\n\n"
         "Answer: If the request carries the same RequestID and cryptographic fingerprint, the idempotency cache recognizes the duplicate and "
         "returns the previous response without re-executing. If it carries a different RequestID, the resource is already held, "
         "and the state machine rejects it with HTTP 409 Conflict."),

        ("C.7 Scenario: PostgreSQL Completely Down While Lock Traffic Surges",
         "Question: PostgreSQL runs out of disk space and crashes. Hundreds of lock acquisitions arrive per second. What happens to the cluster?\n\n"
         "Answer: Distributed lock operations continue without interruption because Raft and BoltDB are completely decoupled from PostgreSQL. "
         "The in-memory audit channel buffer absorbs events until capacity (10,000) is reached. If the database remains offline, "
         "new audit events are safely dropped while metrics counters increment, ensuring lock availability is maintained."),

        ("C.8 Scenario: Clock Drift Between Coordinator Nodes",
         "Question: Coordinator B's physical clock runs 5 seconds faster than Coordinator A's clock. Does this corrupt consensus?\n\n"
         "Answer: No. Raft consensus does not rely on synchronized wall clocks for safety or ordering. Ordering is determined "
         "strictly by Raft Term and Log Index numbers. Leases use the leader's local monotonic nanosecond clock when proposing commands, "
         "ensuring all followers apply identical expiration thresholds."),

        ("C.9 Scenario: Malicious Client Attempts Fencing Token Spoofing",
         "Question: A rogue client observes Fencing Token 1 on the network and attempts to send write requests to the protected resource with Token 999. What happens?\n\n"
         "Answer: In production architectures, downstream storage validates that the client presents a signed bearer ticket or cryptographically "
         "verifies the token epoch. Even in our reference implementation, accepting Token 999 permanently advances HighestTokenSeen to 999, "
         "which would invalidate any subsequent writes from legitimate clients with lower tokens, immediately triggering security alerts."),

        ("C.10 Scenario: Simultaneous Expiration and Explicit Release",
         "Question: A release request arrives at the exact nanosecond that a lease expires. How is the race condition resolved?\n\n"
         "Answer: The state machine executes all commands sequentially under an exclusive sync.Mutex lock. Whichever command is "
         "processed first determines the state. If release processes first, it clears the lock and records TransitionReleased. "
         "If expiration processes first, release finds the lease already expired and returns ErrLeaseExpired without error.")
    ]

    for q, a in qa_10:
        add_heading_3(doc, q)
        add_paragraph(doc, a)

    # =========================================================================
    # CHAPTER 9: LIMITATIONS, CAP TRADE-OFFS & FUTURE SCOPE
    # =========================================================================
    add_heading_1(doc, "Chapter 9 — Limitations, CAP Trade-Offs & Future Scope")
    add_paragraph(doc,
        "An honest engineering analysis requires evaluating the theoretical trade-offs, practical boundaries, and scalability limits "
        "of the implemented architecture."
    )

    add_heading_2(doc, "9.1 CAP & PACELC Theoretical Trade-Offs")
    add_paragraph(doc,
        "Under Brewer's CAP Theorem, LOCKGUARD is explicitly designed as a CP (Consistent / Partition Tolerant) system. "
        "Under the PACELC theorem, it is classified as PC/EC: during a network partition, it chooses Consistency over Availability; "
        "in normal operation, it prioritizes Consistency over Latency. "
        "This means that when a network partition separates coordinator nodes such that no majority quorum can be formed, "
        "the service rejects mutations (fail-closed). This is the only acceptable trade-off for a distributed lock service, "
        "as sacrificing consistency would cause split-brain and catastrophic storage corruption."
    )

    add_heading_2(doc, "9.2 Current System Limitations")
    add_bullet(doc, "Single Raft Group Throughput: All resources share a single Raft consensus group. Write throughput is bounded by single-leader BoltDB disk sync IOPS (~2,000–5,000 ops/sec).", "Limitation 1: ")
    add_bullet(doc, "Local In-Memory Audit Buffer Capacity: The audit pipeline buffer holds 10,000 events. Prolonged database outages exceeding this capacity result in event shedding.", "Limitation 2: ")
    add_bullet(doc, "Single Datacenter Network Assumption: Raft heartbeats (100ms) and election timeouts (250ms) are optimized for low-latency LAN networks (<5ms RTT) and require retuning for WAN deployments.", "Limitation 3: ")

    add_heading_2(doc, "9.3 Future Scope & Engineering Roadmap")
    add_bullet(doc, "Multi-Raft Horizontal Partitioning: Shard resources across independent Raft consensus groups using consistent hashing, enabling linear scalability to 100,000+ locks.", "Roadmap Item 1: ")
    add_bullet(doc, "Distributed Deadlock Detection: Implement wait-for-graph (WFG) analysis with cycle detection algorithms (Chandy-Misra-Haas) to detect multi-resource client deadlocks.", "Roadmap Item 2: ")
    add_bullet(doc, "Mutual TLS (mTLS) Inter-Node Encryption: Enforce hardware-backed TLS certificates on Raft transport port 7000 to protect inter-coordinator traffic from eavesdropping.", "Roadmap Item 3: ")
    add_bullet(doc, "Kafka Audit Streaming: Replace direct PostgreSQL batching with Apache Kafka topic ingestion for planet-scale event sourcing and compliance auditing.", "Roadmap Item 4: ")

    # =========================================================================
    # CHAPTER 10: CONCLUSION & REFERENCES
    # =========================================================================
    add_heading_1(doc, "Chapter 10 — Project Conclusion & Academic References")
    add_paragraph(doc,
        "LOCKGUARD successfully demonstrates the design, rigorous implementation, and empirical verification of a fault-tolerant "
        "distributed lock service. By combining HashiCorp Raft consensus with monotonic 64-bit fencing tokens, the system provides "
        "provably safe mutual exclusion that survives leader crashes, network partitions, and asynchronous client garbage collection pauses. "
        "The complete decoupling of relational audit logging preserves low-latency consensus, while comprehensive telemetry and "
        "modern React dashboards deliver enterprise-grade observability and administrative governance."
    )

    add_callout(doc,
        "Academic Sign-Off & Verification Statement:\n"
        "This technical cookbook and the accompanying LOCKGUARD codebase represent the original, verified work of Group 13 "
        "(Prathamesh Shelar, Roll No. 65 and Chandrabhanu Prusty, Roll No. 48) submitted for the Software Design Course (CSD501), "
        "Department of Computer Engineering, Veermata Jijabai Technological Institute (VESIT), under the guidance of Dr. Priya R.L., "
        "Academic Year 2026–2027.",
        "IMPORTANT"
    )

    add_heading_2(doc, "Formal Academic References")
    refs = [
        "1. Ongaro, D., & Ousterhout, J. (2014). In search of an understandable consensus algorithm. In 2014 USENIX Annual Technical Conference (USENIX ATC 14) (pp. 305-319).",
        "2. Kleppmann, M. (2016). How to do distributed locking. Martin Kleppmann's Technical Blog, University of Cambridge.",
        "3. Lamport, L. (1978). Time, clocks, and the ordering of events in a distributed system. Communications of the ACM, 21(7), 558-565.",
        "4. Brewer, E. A. (2000). Towards robust distributed systems. In ACM Symposium on Principles of Distributed Computing (PODC) (Vol. 7).",
        "5. Abadi, D. J. (2012). Consistency tradeoffs in modern distributed database system design: CAP is only part of the story. Computer, 45(2), 37-42.",
        "6. Burrows, M. (2006). The Chubby lock service for loosely-coupled distributed systems. In 7th USENIX Symposium on Operating Systems Design and Implementation (OSDI 06) (pp. 335-350).",
        "7. Jones, M., Bradley, J., & Sakimura, N. (2015). JSON Web Token (JWT). RFC 7519, Internet Engineering Task Force (IETF)."
    ]
    for r in refs:
        add_paragraph(doc, r)
