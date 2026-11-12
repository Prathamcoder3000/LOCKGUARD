from scripts.docx_helpers import (
    add_heading_1, add_heading_2, add_heading_3, add_paragraph, add_bullet,
    add_callout, add_code_block, add_figure, add_table
)

def build_overview_chapters(doc):
    # Document Control Table
    add_heading_1(doc, "Document Control & Metadata")
    headers = ["Field", "Specification Details"]
    rows = [
        ["Project Title", "LOCKGUARD — Fault Tolerant Distributed Lock Service"],
        ["Document Type", "Comprehensive Academic Technical Cookbook & Operational Manual"],
        ["Version", "2.0 (Phase 6 Final Release — Expanded & Verified Edition)"],
        ["Academic Course", "Software Design (Course Code: CSD501)"],
        ["Target Audience", "Students, Faculty Evaluators, Distributed Systems Engineers, Operators"],
        ["Primary Invariant", "Consistency First — Fail Closed Upon Quorum Loss"],
        ["Authoritative Source", "HashiCorp Raft Consensus Engine & Replicated State Machine (FSM)"],
        ["Secondary Storage", "PostgreSQL 16 Relational Operational Audit Trail (Best-Effort)"],
        ["Host Environments", "Windows 11 / Windows Server 2022 / Ubuntu 22.04 LTS via Docker"],
        ["Submission Date", "12 November 2026"]
    ]
    add_table(doc, headers, rows, [2.0, 4.5])

    add_callout(doc, 
        "Consistency first — fail closed upon quorum loss. A lock operation must never be reported as granted "
        "merely because an API Gateway process is reachable. Every state mutation must be replicated across a strict "
        "Raft majority quorum (2 of 3 nodes) and committed to BoltDB write-ahead logs before returning HTTP 200 OK.",
        "IMPORTANT"
    )

    # Executive Summary & Abstract
    add_heading_1(doc, "1. Executive Summary & Abstract")
    add_paragraph(doc,
        "LOCKGUARD is an enterprise-grade, consensus-driven distributed lock service engineered to resolve critical mutual "
        "exclusion and distributed coordination challenges in modern clustered architectures and microservices. Built upon "
        "the formal foundation of the HashiCorp Raft consensus protocol, LOCKGUARD guarantees strong linearizability, "
        "strictly monotonic fencing tokens (addressing Martin Kleppmann's formal critique of distributed locks), exact "
        "lease-version verification, and safe crash recovery across an odd-numbered coordinator cluster."
    )
    add_paragraph(doc,
        "Unlike naive distributed locks that fail silently under garbage collection pauses, network partitions, and host "
        "clock drift (such as Redis SETNX without fencing or uncoordinated database flags), LOCKGUARD coordinates all state "
        "transitions across a replicated Raft quorum. It combines an in-memory deterministic finite state machine (FSM) with "
        "decoupled, fail-open PostgreSQL operational audit persistence, automated client lease renewal workers, and "
        "defense-in-depth security featuring HMAC-SHA256 JWT authentication and role-based access control."
    )

    # Core Project Objectives
    add_heading_2(doc, "1.1 Academic Project Objectives")
    add_bullet(doc, "Provide provable distributed mutual exclusion across independent client processes and failure domains.", "1. Linearizable Mutual Exclusion: ")
    add_bullet(doc, "Maintain identical lock state transitions across a 3-node HashiCorp Raft consensus cluster.", "2. Replicated State Machine: ")
    add_bullet(doc, "Issue globally ascending 64-bit integer tokens upon every lease grant to protect downstream storage against stale writes.", "3. Monotonic Fencing Tokens: ")
    add_bullet(doc, "Enforce exact generation progression (V_new = V_old + 1) to eliminate phantom renewals.", "4. Exact Lease Versioning: ")
    add_bullet(doc, "Refuse state mutations when majority quorum (< 2 nodes) is lost, preventing split-brain corruption.", "5. Fail-Closed Quorum Safety: ")
    add_bullet(doc, "Isolate Raft consensus commit latencies from relational database disk I/O via a 50,000-event bounded buffer.", "6. Decoupled Audit Logging: ")
    add_bullet(doc, "Enforce zero-trust JWT authentication, role segregation (ADMIN, CLIENT, READ_ONLY), and rate limiting.", "7. Edge Defense-in-Depth: ")

    # System Architecture Chapter
    add_heading_1(doc, "2. System Architecture & Component Decomposition")
    add_paragraph(doc,
        "The LOCKGUARD architecture is partitioned into four decoupled tiers: the Client Tier, the Edge Gateway Tier, "
        "the Distributed Consensus Tier, and the Persistence & Storage Tier. Each tier maintains clear responsibilities "
        "and failure boundaries to maximize operational reliability and throughput."
    )

    add_figure(doc, "cookbook-evidence/diag_system-architecture.png",
        "Figure 2.1 — LOCKGUARD System Architecture & Network Boundary Map",
        "Comprehensive architectural diagram showing client applications, reverse proxy ingress, edge gateway middleware, "
        "3-node Raft consensus cluster with BoltDB logs, PostgreSQL audit store, and downstream protected resource."
    )

    add_heading_2(doc, "2.1 Tier Breakdown and Responsibilities")
    headers_arch = ["Tier / Component", "Internal Port", "Published Host Port", "Core Responsibilities"]
    rows_arch = [
        ["Client Tier (React Dashboard)", "80 (Nginx)", "80 (Host)", "Single-page operator application providing live topology, countdown TTL bars, and audit viewer."],
        ["Edge Ingress (Nginx Proxy)", "80 / 443", "80 (Host)", "Reverse proxy terminating client HTTP connections, routing static assets, and rewriting /api/ calls."],
        ["API Gateway Service (Go)", "8080", "8082 (Host)", "Edge ingress performing JWT authentication, RBAC, token-bucket rate limiting, and dynamic leader discovery."],
        ["Consensus Tier (Node 1)", "8001 / 7000", "Internal Network", "Raft coordinator running in-memory FSM, BoltDB WAL, and snapshot compaction engine."],
        ["Consensus Tier (Node 2)", "8001 / 7000", "Internal Network", "Raft peer maintaining replicated log and voting rights for majority quorum consensus."],
        ["Consensus Tier (Node 3)", "8001 / 7000", "Internal Network", "Raft peer maintaining replicated log and voting rights for majority quorum consensus."],
        ["Protected Resource Service", "9000", "9000 (Host)", "External sensitive storage target validating monotonic fencing tokens and rejecting zombie writes."],
        ["Operational Persistence (PostgreSQL)", "5432", "5435 (127.0.0.1)", "Relational database storing append-only audit event logs, schema migrations, and metadata."],
        ["Observability (Prometheus)", "9090", "9090 (Host)", "Telemetry scraper gathering gauges, counters, and histograms from /v1/metrics endpoints."]
    ]
    add_table(doc, headers_arch, rows_arch, [1.5, 1.0, 1.2, 2.8])

    add_figure(doc, "cookbook-evidence/diag_deployment-architecture.png",
        "Figure 2.2 — Docker Deployment & Container Network Wiring",
        "Multi-container topology showing isolated internal bridges (lockguard-internal and lockguard-raft), "
        "volume persistence bindings, and host port publishing."
    )

    # Technology Stack Chapter
    add_heading_1(doc, "3. Technology Stack & Host Environment Wiring")
    add_paragraph(doc,
        "LOCKGUARD leverages a production-grade technology stack chosen specifically for deterministic concurrency, "
        "low-latency network communication, and distributed consensus reliability:"
    )

    headers_tech = ["Subsystem / Layer", "Technology", "Version", "Technical Justification & Purpose"]
    rows_tech = [
        ["Core Backend Language", "Go (Golang)", "1.22+", "Lightweight goroutines, low-latency garbage collection, strong standard library HTTP primitives."],
        ["Consensus Engine", "HashiCorp Raft", "v1.7.0+", "Production-proven implementation of the Raft consensus protocol with leader election and log replication."],
        ["Consensus Storage", "HashiCorp BoltDB", "v1.3.5+", "Single-file embedded ACID key-value database providing crash-resilient write-ahead logging (WAL)."],
        ["Relational Database", "PostgreSQL", "16-alpine", "ACID-compliant relational database for durable, append-only operational audit trails and query indexes."],
        ["Frontend UI Framework", "React / TypeScript", "18.3 / 5.5", "Type-safe reactive dashboard interface providing real-time operational cluster visualization."],
        ["Frontend Build Tool", "Vite", "6.1+", "Fast modern asset pipeline with Hot Module Replacement and optimized production bundling."],
        ["Reverse Proxy", "Nginx", "1.27-alpine", "High-performance edge reverse proxy handling URL rewriting, gzip compression, and static asset caching."],
        ["Observability Engine", "Prometheus", "v2.54.1", "Standard cloud-native metrics exposition format and time-series monitoring scraper."],
        ["Containerization", "Docker & Compose", "24+ / 2.20+", "Multi-stage reproducible microservice containerization with health probes and isolated networks."]
    ]
    add_table(doc, headers_tech, rows_tech, [1.3, 1.2, 0.9, 3.1])

    add_callout(doc,
        "Authoritative Port Rule: The root docker-compose.yml file is the single source of truth for all published host ports. "
        "Nginx is published on host port 80, the Go API Gateway defaults to host port 8082, the Protected Resource to port 9000, "
        "Prometheus to port 9090, and PostgreSQL to port 5435 bound strictly to localhost (127.0.0.1).",
        "NOTE"
    )
