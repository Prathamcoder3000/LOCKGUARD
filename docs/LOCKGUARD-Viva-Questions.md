# LOCKGUARD — Comprehensive Viva-Voce Examination Preparation

## Academic Project Title: Fault Tolerant Distributed Lock Service
**System Version:** Phase 6 Final Release  
**Target:** Academic Viva-Voce Defense, Technical Oral Evaluation, Faculty Interviews  

---

# Part 1: 50 Short Technical Questions & Answers

### Distributed Systems & Consensus
1. **What is distributed mutual exclusion?**  
   It is a coordination primitive guaranteeing that among a set of independent processes communicating over an asynchronous network, at most one process executes within a critical section at any given time.
2. **What is the CAP Theorem, and where does LOCKGUARD sit?**  
   The CAP theorem states a distributed system can guarantee at most two of Consistency, Availability, and Partition Tolerance ($P$). LOCKGUARD chooses $CP$: upon partition, it refuses writes in the minority partition to preserve linearizable consistency.
3. **What is the FLP Impossibility Theorem?**  
   Fischer, Lynch, and Paterson (1985) proved that in an asynchronous network, no deterministic consensus protocol can guarantee termination in the presence of even a single unannounced fail-stop crash.
4. **How does Raft overcome FLP Impossibility?**  
   Raft uses randomized election timers (typically 150ms–300ms) to introduce non-determinism, preventing split votes and guaranteeing practical liveness.
5. **What is a quorum in Raft?**  
   A strict majority of nodes: $Q = \lfloor N/2 \rfloor + 1$. In a 3-node cluster, $Q = 2$. In a 5-node cluster, $Q = 3$.
6. **Why must a Raft cluster have an odd number of nodes?**  
   An odd number provides optimal fault tolerance per node cost. A 3-node cluster tolerates 1 failure ($\lfloor (3-1)/2 \rfloor = 1$). A 4-node cluster also tolerates only 1 failure ($\lfloor (4-1)/2 \rfloor = 1$) but requires 3 votes for quorum, increasing overhead without adding fault tolerance.
7. **What is linearizability?**  
   Strong consistency guarantee requiring every read and write to appear to take effect instantaneously at a discrete point in physical time between its invocation and response.
8. **What are the three core RPCs in Raft?**  
   `RequestVote` (used by candidates to solicit votes during election), `AppendEntries` (used by leader for log replication and heartbeats), and `InstallSnapshot` (used to stream state snapshots to lagging followers).
9. **What is a Raft Term?**  
   A logical epoch counter incremented monotonically with each election. Terms act as a logical clock enabling nodes to detect and discard obsolete information from stale leaders.
10. **What is the State Machine Safety Invariant in Raft?**  
    If a server has applied a log entry at a given index to its state machine, no other server will ever apply a different log entry for the same index.

### Lock Mechanics & Invariants
11. **What is a Fencing Token?**  
    A strictly increasing 64-bit integer issued on every lock acquisition, used by downstream storage to reject delayed writes from stale lock holders.
12. **Why are simple Redis locks (like `SETNX`) flawed for distributed locking?**  
    They rely solely on wall-clock TTLs. If a process experiences a GC pause or network delay exceeding the TTL, another process acquires the lock, leading to concurrent mutations and data corruption.
13. **What is Martin Kleppmann's critique of Redlock?**  
    Kleppmann proved that Redlock lacks monotonic fencing tokens and makes dangerous synchrony assumptions about network delays and clock drift, making it unsafe for mutual exclusion.
14. **What is the difference between a Lock and a Lease?**  
    A lock is held until explicitly freed; if the holder crashes, deadlock results. A lease is time-bounded by a TTL; if the holder crashes, the lease expires automatically.
15. **What is Lease Versioning in LOCKGUARD?**  
    An exact counter ($V_{\text{new}} = V_{\text{old}} + 1$) incremented on every renewal, preventing out-of-order renewals or duplicate extensions.
16. **Why does LOCKGUARD use exact version increment rather than `>=`?**  
    Requiring exact $+1$ progression ensures that every renewal observes the immediate prior state, detecting any concurrent race conditions or stale operations.
17. **What happens when a lock expires?**  
    The lock record is marked freed. However, the highest issued fencing token is permanently retained to ensure subsequent acquisitions receive higher tokens.
18. **Can a client renew a lock after it has expired?**  
    No. Renewal of an expired lock fails with `ERR_LOCK_NOT_FOUND` or `ERR_LOCK_NOT_OWNED`.
19. **What is clock drift cushion?**  
    A safety margin (500ms) subtracted from TTL on the client side, ensuring clients stop mutating storage before the server-side lease formally lapses.
20. **Can two clients acquire different resources concurrently?**  
    Yes. Mutual exclusion is scoped per resource name. `resource-A` and `resource-B` are completely independent.

### Go Language & Concurrency
21. **Why was Go selected for LOCKGUARD?**  
    Go offers lightweight goroutines, low-latency garbage collection, strong standard library HTTP primitives, type safety, and the industry-standard HashiCorp Raft implementation.
22. **What is the purpose of `sync.RWMutex` in the FSM?**  
    It permits multiple concurrent read operations (queries) while guaranteeing exclusive access during state mutations (Apply).
23. **Why must the FSM `Apply` method be deterministic?**  
    Because every node in the Raft cluster executes the identical sequence of committed log entries. Any non-determinism (e.g. using `time.Now()` inside `Apply` rather than the command timestamp) would cause state drift across replicas.
24. **How does Go's race detector work?**  
    `go test -race` instruments memory accesses at compile time with ThreadSanitizer (TSan), recording read/write operations to detect unsynchronized concurrent access.
25. **What is a bounded channel in Go?**  
    A channel created with a buffer capacity (e.g. `make(chan Event, 50000)`). Senders only block when the buffer is full.
26. **What is graceful shutdown in Go HTTP servers?**  
    Using `srv.Shutdown(ctx)` to stop accepting new connections while waiting for in-flight requests to complete before terminating.
27. **What is the purpose of `atomic.Uint64`?**  
    It provides lock-free, hardware-level atomic operations (Compare-and-Swap, Add) without mutex contention.
28. **How does LOCKGUARD handle panics in HTTP handlers?**  
    A recovery middleware intercepts runtime panics with `recover()`, logs the stack trace, and returns HTTP 500, preventing server crashes.
29. **What is the difference between `log/slog` and standard `log` in Go?**  
    `slog` provides structured logging (key-value pairs) in JSON or text format, facilitating automated ingestion by log management systems.
30. **What is the purpose of `context.Context`?**  
    It carries request deadlines, cancellation signals, and trace values across API boundaries and goroutines.

### Networking & Security
31. **What is mutual TLS (mTLS)?**  
    A security protocol where both client and server authenticate each other using X.509 digital certificates before establishing an encrypted TLS connection.
32. **Where is mTLS used in LOCKGUARD?**  
    For inter-node Raft consensus transport (ports 9001, 9002, 9003), preventing rogue nodes from joining the consensus group.
33. **What is a JWT (JSON Web Token)?**  
    A compact, URL-safe standard (RFC 7519) consisting of a Header, Payload (claims), and cryptographic Signature.
34. **What signing algorithm does LOCKGUARD use for JWT?**  
    HMAC-SHA256 (`HS256`), verifying authenticity with a server-side secret key.
35. **What are the three RBAC roles in LOCKGUARD?**  
    `admin` (all operations including step-down), `client` (acquire, renew, release owned locks), and `read-only` (status and audit inspection only).
36. **What is a token bucket rate limiter?**  
    An algorithm where tokens are added to a bucket at a fixed rate up to a maximum burst capacity. Each request consumes one token; if empty, the request is throttled (HTTP 429).
37. **Why does LOCKGUARD avoid storing raw bearer tokens as rate limiter keys?**  
    Storing raw bearer tokens risks exposing credentials in memory dumps or logs. LOCKGUARD uses the authenticated subject or a SHA-256 keyed fingerprint.
38. **What is CORS?**  
    Cross-Origin Resource Sharing is an HTTP-header mechanism allowing a server to indicate any origins other than its own from which a browser should permit loading resources.
39. **What security headers does the API Gateway inject?**  
    `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `X-XSS-Protection: 1; mode=block`, and strict CORS headers.
40. **How does LOCKGUARD protect against request body exhaustion attacks?**  
    A middleware enforces `http.MaxBytesReader` with a 1 MB limit, rejecting oversized payloads with HTTP 413.

### Persistence & Storage
41. **Why does LOCKGUARD decouple audit logging from Raft consensus?**  
    Synchronously writing audit logs to PostgreSQL inside the Raft commit path would reduce lock acquisition throughput to database write latency (~20ms). Decoupling via a memory buffer preserves sub-2ms consensus latencies.
42. **What does "Fail-Open" mean for the audit pipeline?**  
    If PostgreSQL crashes, the consensus cluster continues acquiring and releasing locks normally. Audit events buffer in memory with retry backoff rather than blocking the system.
43. **What is the purpose of database migrations?**  
    To version-control and apply relational schema changes (tables, indexes) automatically and deterministically at service startup.
44. **What database indexes exist on `audit_events`?**  
    Indexes on `resource`, `owner_id`, `fencing_token`, and `created_at` for fast querying.
45. **What is BoltDB?**  
    An embedded, single-file ACID key-value database written in Go based on B+ trees, used by HashiCorp Raft to store logs and cluster metadata.
46. **What is Raft log compaction?**  
    The process of snapshotting current state to disk and deleting earlier committed log entries to prevent unbounded disk growth.
47. **How does a newly joined node synchronize state?**  
    The leader streams the latest point-in-time snapshot via `InstallSnapshot`, followed by any subsequent `AppendEntries`.
48. **What is connection pooling in PostgreSQL?**  
    Reusing a bounded set of database connections across requests to avoid the latency and memory overhead of creating new TCP connections per query.
49. **What is transaction isolation in PostgreSQL?**  
    ACID rules controlling visibility of uncommitted data. LOCKGUARD uses `READ COMMITTED` for audit event batch insertions.
50. **What is a cold-start disaster recovery?**  
    Bootstrapping the consensus cluster from an archival snapshot file on disk when all existing runtime instances have suffered catastrophic failure.

---

# Part 2: 20 Medium Architectural Questions & Answers

51. **Explain the end-to-end lifecycle of a lock acquisition request.**  
    Client submits `POST /v1/locks/acquire` to Gateway -> Gateway authenticates JWT, checks rate limits, and proxies to active Raft Leader -> Leader serializes `CmdAcquire` into Raft log -> Leader sends `AppendEntries` to Followers -> Quorum ($2/3$) acks -> Leader commits entry -> Leader applies command to deterministic FSM -> FSM increments fencing token and sets lease -> Result returned to Gateway -> Gateway responds HTTP 200 to client.
52. **How does LOCKGUARD handle asymmetric network partitions?**  
    If Node 1 (Leader) is partitioned from Node 2 and Node 3: Node 2 and Node 3 cannot hear heartbeats, trigger an election, and elect Node 2 as new Leader (quorum 2 of 3). Any writes sent to Node 1 fail because Node 1 cannot achieve quorum. When the partition heals, Node 1 discovers higher Term from Node 2, steps down to Follower, and updates its log.
53. **Why is Redis Redlock considered dangerous for safety-critical distributed mutual exclusion?**  
    Redlock assumes synchronized physical clocks across Redis nodes. If a node's clock jumps forward due to NTP adjustments, a lease expires prematurely. Furthermore, without monotonic fencing tokens, delayed processes overwrite valid mutations.
54. **Describe the role of the Protected Resource in verifying fencing tokens.**  
    The protected resource tracks `highest_seen_token` for each resource in memory or database. When a client performs a write, it must provide its token. If $\text{token} > \text{highest\_seen}$, the write succeeds and `highest_seen` updates. If $\text{token} \le \text{highest\_seen}$, the resource returns HTTP 409 Conflict.
55. **How does the API Gateway maintain high availability and discover the current leader?**  
    The Gateway maintains an in-memory leader cache with a short TTL (2 seconds). If an HTTP forward fails or receives an HTTP 421/503 indicating a leadership step-down, the Gateway queries coordinator `/v1/cluster/status` endpoints, invalidates its cache, and routes to the new leader.
56. **Explain the difference between fail-open and fail-closed paradigms in LOCKGUARD.**  
    - *Consensus/Lock State:* Strict **fail-closed** (Consistency over Availability). If quorum is lost, no writes are permitted.
    - *Operational Audit Logging:* **Fail-open** (Availability over Consistency). If PostgreSQL is down, consensus operations proceed while audit records buffer in memory.
57. **Why must the fencing token counter never be reset when a lock is released?**  
    Resetting the token to 0 on release would cause a subsequent acquisition to receive token 1. If an earlier worker with token 1 woke up, the protected resource could not differentiate the stale worker from the new worker. Monotonic growth across the lifetime of the resource is mandatory.
58. **Explain the exact lease version advancement invariant ($V_{\text{new}} = V_{\text{old}} + 1$).**  
    If a client holds version 1 and submits a renewal claiming version 3, or if two concurrent threads try to renew version 1, exact verification ensures only the thread presenting version 1 -> 2 succeeds. Any duplicate or out-of-order renewal is rejected.
59. **How does LOCKGUARD handle client process crashes during an active lock?**  
    The lock is protected by a TTL (e.g. 15s). If the client crashes, its renewal worker halts. When physical time passes $T_{\text{expires}}$, the coordinator or background expiration engine frees the lock, permitting subsequent clients to acquire it without deadlock.
60. **How does the Heartbeat Manager distinguish between client liveliness and lock leases?**  
    A client registers a session with a session TTL (e.g. 30s) and submits periodic heartbeats every 10s. The session represents client process vitality. Lock leases represent specific resource claims with their own TTLs. If a session expires, all locks associated with that session can be automatically preempted.
61. **Why is snapshotting critical for Raft consensus performance?**  
    Without snapshots, the Raft write-ahead log would grow indefinitely, consuming all disk space and causing recovering nodes to replay millions of historical entries on startup. Snapshots compact state into a single point-in-time image.
62. **What mechanisms prevent split-brain in LOCKGUARD?**  
    1. Raft strict majority quorum requirement ($Q = \lfloor N/2 \rfloor + 1$). Two majorities cannot exist simultaneously in an odd-numbered cluster.  
    2. Monotonic terms: nodes reject RPCs from stale terms.
63. **Explain the rate limiting strategy used on login endpoints vs global lock endpoints.**  
    Login endpoints are restricted to 10 rps with burst 20 to thwart brute-force password guessing. General lock endpoints allow 200 rps with burst 400 to support high-throughput microservices.
64. **How does LOCKGUARD prevent credential leakage in logs and rate limit keys?**  
    The rate limiter uses HMAC-SHA256 or SHA-256 keyed fingerprints of incoming credentials. Audit logs and middleware request loggers redact `Authorization` headers.
65. **Describe how the consensus FSM handles time deterministically.**  
    The FSM does not call `time.Now()` inside `Apply()`. Instead, the Leader timestamps the command when proposing it to Raft, embedding `TimestampUnixNano` into the log entry. Replicas evaluate expirations against this embedded consensus timestamp.
66. **What is the difference between Leader Election and Quorum Loss?**  
    In Leader Election, 2 of 3 nodes are alive, elect a leader, and continue operating normally. In Quorum Loss, 2 of 3 nodes crash, leaving only 1 node; that node cannot obtain 2 votes, enters Follower state, and rejects all mutations.
67. **How does LOCKGUARD ensure zero race conditions during concurrent acquire requests?**  
    Lock acquisitions are serialized through the Raft log. Even if two requests arrive at the leader simultaneously, they receive discrete log indexes ($i$ and $i+1$). The FSM applies them sequentially, granting the first and rejecting the second.
68. **Explain the memory management strategy of the bounded audit ring buffer.**  
    The channel is initialized with capacity 50,000. If the worker cannot flush to PostgreSQL fast enough and the buffer fills, a configurable policy either drops oldest non-critical events or pauses ingestion with an error counter, preventing memory exhaustion (OOM).
69. **How does LOCKGUARD validate that a client releasing a lock is the legitimate owner?**  
    The release command requires both matching `OwnerID` and matching `FencingToken`. A rogue client cannot release a lock it does not hold.
70. **What role does the React operator dashboard play in system governance?**  
    It provides operational transparency, allowing operators to visualize active locks, monitor cluster topology, inspect countdown TTL timers, and audit all historical lock events.

---

# Part 3: 10 Comprehensive Scenario-Based Questions & Answers

71. **Scenario 1: Long Stop-the-World GC Stall**  
    *Question:* Worker A acquires lock on `order-table` (TTL 10s, Fencing Token 40). Worker A enters a 25-second Java GC pause. What happens?  
    *Answer:* After 10s, Worker A's lease expires. Worker B acquires `order-table` (Fencing Token 41) and completes a mutation. At second 25, Worker A wakes up and attempts to write to the database with Token 40. The database / protected resource evaluates $40 \le 41$, rejects Worker A with HTTP 409 Conflict, and prevents data corruption.
72. **Scenario 2: Network Partition Isolating the Leader**  
    *Question:* In a 3-node cluster (N1=Leader, N2=Follower, N3=Follower), a network switch failure isolates N1 from N2, N3, and the Gateway. Detail the cluster's recovery.  
    *Answer:* N2 and N3 detect heartbeat timeout, advance term, and N2 is elected Leader by quorum of 2. The Gateway discovers N2 and routes client traffic to N2. If an internal client attempts to send a write to N1, N1 cannot commit the write because it cannot reach a quorum of 2 nodes. When the partition heals, N1 sees N2's higher term, reverts to Follower, and updates its log.
73. **Scenario 3: Coordinator Node Storage Disk Crash**  
    *Question:* The SSD backing Node 1 crashes and is replaced with a blank disk. How does Node 1 recover?  
    *Answer:* Node 1 is started with an empty storage path. It joins the cluster as a Follower. Leader Node 2 detects that Node 1's last log index is 0. Since earlier logs have been compacted, Node 2 invokes `InstallSnapshot`, streaming the latest compressed state snapshot to Node 1. Node 1 restores the FSM into memory and then streams recent `AppendEntries`, catching up to live traffic without downtime.
74. **Scenario 4: Concurrent Lease Renewal and Expiration Race**  
    *Question:* A renewal request arrives at the Leader at the exact millisecond a lock's TTL expires. How is the tie resolved?  
    *Answer:* The Raft log commits commands sequentially. If the renewal command is committed with a timestamp $T \le \text{ExpiresAt}$, the lease is extended. If the expiration occurred earlier in the log, the renewal encounters an expired or absent lock and returns `ERR_LOCK_NOT_FOUND`. There is no ambiguous state because the Raft log provides a total linear ordering.
75. **Scenario 5: Complete PostgreSQL Outage During High-Throughput Locking**  
    *Question:* PostgreSQL goes down for 30 minutes during peak hours. What is the impact on distributed locking?  
    *Answer:* Consensus locking operations continue at full speed with sub-2ms latencies because the consensus path does not wait for PostgreSQL. The audit worker catches DB connection errors, logs warnings, increments `lockguard_audit_failures_total`, and retains events in the bounded buffer with exponential retry backoff. When PostgreSQL restarts, the worker drains the buffer into the database.
76. **Scenario 6: Malicious Client Infiltration (mTLS & RBAC)**  
    *Question:* An attacker gains access to the private network and attempts to execute Raft `RequestVote` RPCs to take over leadership. What happens?  
    *Answer:* The attacker's TCP connection is terminated during the TLS handshake because the coordinator requires mutual TLS with a client certificate signed by the internal cluster Certificate Authority. Even on the HTTP gateway, mutations are blocked without an `admin` or `client` JWT signed with the secret key.
77. **Scenario 7: Client Clock Skew of +10 Seconds**  
    *Question:* A client's local system clock is set 10 seconds into the future due to misconfigured NTP. Does this compromise lock safety?  
    *Answer:* No. Lock expiration and validity are calculated exclusively on the coordinator nodes using the Raft consensus command timestamp. The client's local clock skew cannot accelerate or delay server-side lease expiration. Furthermore, the 500ms client drift cushion adds an additional safety margin.
78. **Scenario 8: Catastrophic 2-Node Crash (Quorum Depletion)**  
    *Question:* A power surge takes down Node 1 and Node 2 simultaneously. Can the surviving Node 3 serve read or write requests?  
    *Answer:* Node 3 cannot serve writes because any write requires majority quorum ($2/3$), so it fails closed with HTTP 503. For linearizable reads, Node 3 also refuses to serve stale state because it cannot verify whether another leader was elected. The system prioritizes Consistency over Availability.
79. **Scenario 9: Rapid Client Re-Acquisition Fencing Invariant**  
    *Question:* Client A acquires lock (Token 10), releases it, and Client A immediately acquires it again. What token is issued?  
    *Answer:* Client A receives Token 11. Even though the owner is identical and the release was clean, every acquisition strictly increments the resource's monotonic token counter ($\tau \leftarrow \tau + 1$) to guarantee that each lease cycle has a globally unique, strictly ascending identity.
80. **Scenario 10: Dynamic Leadership Step-Down During Planned OS Upgrade**  
    *Question:* An operator needs to reboot the server hosting the current Raft Leader for security patching. What is the procedure?  
    *Answer:* The operator issues `POST /v1/cluster/stepdown` to the leader. The leader abdicates, and the remaining 2 followers elect a new leader within 300ms. The API Gateway transparently shifts client connections to the new leader. The operator patches and reboots the former leader, which rejoins safely as a Follower.

