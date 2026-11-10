# LOCKGUARD — Limitations, Trade-Offs & Future Scope

## Academic Project Title: Fault Tolerant Distributed Lock Service
**System Version:** Phase 6 Final Release  
**Status:** Architectural Analysis & Future Roadmap  

---

## 1. Architectural Trade-Offs & Design Decisions

Every distributed systems design involves fundamental trade-offs between consistency, availability, latency, and complexity:

### 1.1 Consistency vs. Availability (CAP Paradigm)
- **Trade-off Chosen:** Strong Consistency ($CP$).
- **Rationale:** LOCKGUARD was explicitly built to prevent data corruption in critical downstream systems (e.g. database migrations, sequential financial accounting). In our threat model, a transient service outage (failing closed upon quorum loss) is vastly preferable to a split-brain condition where two independent clients simultaneously believe they own exclusive access to a financial ledger.
- **Consequence:** If two of the three nodes crash, the remaining node refuses writes, rendering the lock service temporarily unavailable until quorum is restored.

### 1.2 In-Memory Consensus FSM vs. Direct Disk Storage
- **Trade-off Chosen:** Replicated In-Memory State Machine backed by durable Raft WAL logs.
- **Rationale:** Evaluating lock state entirely in RAM delivers sub-millisecond query latencies and 25,000+ reads/second per node.
- **Consequence:** Cold-start boot times require replaying uncompacted WAL logs or loading snapshot files into memory.

### 1.3 Asynchronous Operational Audit vs. Synchronous Persistence
- **Trade-off Chosen:** Fail-open asynchronous ring buffer to PostgreSQL.
- **Rationale:** Coupling the Raft consensus commit path to PostgreSQL relational writes would degrade system throughput from ~2,000 acquisitions/sec to ~100 acquisitions/sec.
- **Consequence:** If a catastrophic power outage takes down both coordinator hosts and the database simultaneously, unwritten audit events in the in-memory ring buffer could be lost, even though the core lock state remains durable in the Raft WAL.

---

## 2. Current System Limitations

1. **Single Raft Consensus Group:**
   All lock resources are managed by a single 3-node Raft consensus cluster. While this handles thousands of locks and 2,000+ operations/second, the throughput of the entire cluster is bounded by the network and CPU capacity of the single Raft leader.
2. **Coarse-Grained Resource Locking:**
   LOCKGUARD currently provides discrete mutual exclusion on whole string resource keys. It does not provide hierarchical tree-based locking (like ZooKeeper znodes) or fine-grained row-level intent locks.
3. **Audit Buffer Bounded Saturation:**
   During a prolonged PostgreSQL outage spanning hours under maximum write throughput, the 50,000-event ring buffer will saturate, requiring configured drop-oldest policies to prevent host OOM.
4. **Client SDK Availability:**
   First-class SDK bindings and automatic renewal managers are currently provided for Go. Clients in Python, Java, or Node.js must interact directly with the REST API.
5. **Single-Datacenter Low-Latency Assumption:**
   Standard HashiCorp Raft requires low-latency inter-node round-trip times (RTT < 10ms). Deploying nodes across geographically dispersed continents increases consensus latency proportionally to WAN round trips.

---

## 3. Future Engineering Scope & Roadmap

### 3.1 Multi-Raft Sharding & Partitioning
To scale LOCKGUARD horizontally across millions of concurrent locks:
- Implement a Multi-Raft architecture similar to CockroachDB or TiKV.
- Resources will be partitioned into ranges or hash buckets across independent Raft consensus groups.
- Each 3-node group will manage a subset of resources, achieving linear horizontal scalability as coordinator nodes are added.

### 3.2 Cross-Region Hierarchical Consensus (WAN Support)
- Integrate Flexible Paxos or Hierarchical Raft topologies.
- Deploy local voter quorums within availability zones while maintaining asynchronous read-replicas or non-voting learners across secondary cloud regions.

### 3.3 Multi-Lock Distributed Transactions ($2\text{PC}$ over Raft)
- Introduce atomic multi-lock acquisition: allowing a distributed worker to acquire `resource-A`, `resource-B`, and `resource-C` as a single atomic operation.
- Implement Two-Phase Locking ($2\text{PL}$) and distributed deadlock detection algorithms with wait-die or wound-wait preemption policies.

### 3.4 Real-Time Streaming & Push Notifications (gRPC / Server-Sent Events)
- Supplement the HTTP polling and webhook models with bidirectional gRPC and SSE streams.
- Allow clients to subscribe to `Watch(resource)` and receive instantaneous notifications when a lock is released or expires, eliminating polling latency.

### 3.5 Multi-Language Polyglot SDKs
- Author and publish idiomatic client libraries for:
  - **Python:** `lockguard-py` with `asyncio` context manager (`async with lockguard.acquire("res"): ...`).
  - **Java:** `lockguard-java` with Spring Boot auto-configuration and `AutoCloseable` scopes.
  - **Rust:** `lockguard-rs` with Tokio async primitives.
  - **TypeScript/Node:** `lockguard-node` for microservice workers.

### 3.6 Automated Chaos Injection & Self-Healing Autonomous Agents
- Integrate chaos automation into the administrative dashboard, enabling automated network partition testing, random node kills, and continuous consensus fuzzing in staging environments.

