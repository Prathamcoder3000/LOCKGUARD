# LockGuard Distributed State Model

## 1. State Lifecycle Diagram

```mermaid
stateDiagram-v2
    [*] --> Free: System Initialization

    Free --> Held: Acquire Lock (Quorum Replicated)
    note right of Held
      Atomic Actions:
      - Assign owner_id
      - Fencing token: T_new = T_prev + 1
      - Set expires_at = commit_time + TTL
    end note

    Held --> Held: Renew Lease (Leader Heartbeat Commit)
    note right of Held
      Must match owner_id & fencing_token
      Advances expires_at
    end note

    Held --> Free: Explicit Release (Owner Requested)
    Held --> Expired: Lease Timeout (Now > expires_at)

    Expired --> Free: Sweep / Reclaim
    Expired --> Held: New Acquire Request (T_new = T_prev + 1)

    state Partition_Failure {
      Held --> Quorum_Loss_Fail_Closed: Minority Partition Disconnect
      note right of Quorum_Loss_Fail_Closed
        Coordinator loses quorum (< 2 nodes).
        Node steps down as leader.
        All mutation attempts rejected.
      end note
    }
```

---

## 2. Lock State Definitions

| State | Condition | Mutability |
| :--- | :--- | :--- |
| **`FREE`** | No active lease exists for resource name. | Any client may attempt acquisition. |
| **`HELD`** | Active lease held by `owner_id`; `Now() < expires_at`. | Only matching `(owner_id, fencing_token)` can renew or release. |
| **`EXPIRED`** | Lease elapsed (`Now() >= expires_at`) without renewal. | Mutual exclusion terminates; immediately available for new acquire. |
| **`FAIL_CLOSED`** | Cluster quorum (< 2 nodes) unavailable. | All state transitions strictly frozen; writes rejected. |

---

## 3. Mathematical Monotonicity Proof

Let $S_k$ denote the replicated state machine at log index $k$.
Let $F(S_k)$ be the highest fencing token issued by state $S_k$.

### Invariant 1: Strictly Monotonic Fencing Tokens
$$\forall k \ge 0, \quad F(S_{k+1}) > F(S_k)$$

### Invariant 2: Mutual Exclusion
At any logical state $S_k$, for any named resource $R$:
$$|\{c \in \text{Clients} \mid \text{HoldsLock}(c, R, S_k)\}| \le 1$$

### Invariant 3: Quorum Intersection
Let $Q_1$ and $Q_2$ be any two majorities of cluster nodes $N = \{A, B, C\}$ ($|Q_1| \ge 2, |Q_2| \ge 2$).
By the Pigeonhole Principle:
$$Q_1 \cap Q_2 \neq \emptyset$$
Therefore, no two leaders can commit divergent states in the same Raft term.
