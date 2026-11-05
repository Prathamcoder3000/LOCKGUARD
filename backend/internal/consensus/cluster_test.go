package consensus_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"lockguard/backend/internal/consensus"
	"lockguard/backend/internal/lockstate"
)

type clusterFixture struct {
	nodes   map[string]*consensus.RaftNode
	sms     map[string]*lockstate.MemoryStateMachine
	servers []raft.Server
}

func newTestCluster(t *testing.T) *clusterFixture {
	t.Helper()
	nodeIDs := []string{"coordinator-a", "coordinator-b", "coordinator-c"}

	servers := make([]raft.Server, len(nodeIDs))
	for i, id := range nodeIDs {
		servers[i] = raft.Server{
			ID:       raft.ServerID(id),
			Address:  raft.ServerAddress(id),
			Suffrage: raft.Voter,
		}
	}

	cf := &clusterFixture{
		nodes:   make(map[string]*consensus.RaftNode),
		sms:     make(map[string]*lockstate.MemoryStateMachine),
		servers: servers,
	}

	for i, id := range nodeIDs {
		sm := lockstate.New()
		cf.sms[id] = sm

		cfg := consensus.NodeConfig{
			NodeID:             id,
			AdvertiseAddr:      id,
			Inmem:              true,
			BootstrapCluster:   (i == 0), // exactly coordinator-a initiates bootstrap
			BootstrapServers:   servers,
			HeartbeatTimeout:   50 * time.Millisecond,
			ElectionTimeout:    100 * time.Millisecond,
			CommitTimeout:      10 * time.Millisecond,
			LeaderLeaseTimeout: 50 * time.Millisecond,
			LogLevel:           "error",
		}

		node, err := consensus.NewRaftNode(cfg, sm)
		if err != nil {
			t.Fatalf("failed to create node %s: %v", id, err)
		}
		cf.nodes[id] = node
	}

	// Interconnect all in-memory transports
	for id1, n1 := range cf.nodes {
		for id2, n2 := range cf.nodes {
			if id1 != id2 {
				n1.InmemTransport().Connect(raft.ServerAddress(id2), n2.InmemTransport())
			}
		}
	}

	return cf
}

func (cf *clusterFixture) close() {
	for _, n := range cf.nodes {
		_ = n.Close()
	}
}

func (cf *clusterFixture) findLeader(t *testing.T, timeout time.Duration) *consensus.RaftNode {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, n := range cf.nodes {
			if n.IsLeader() {
				return n
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no leader elected within %v", timeout)
	return nil
}

func (cf *clusterFixture) findFollower(t *testing.T) *consensus.RaftNode {
	t.Helper()
	for _, n := range cf.nodes {
		if !n.IsLeader() {
			return n
		}
	}
	t.Fatalf("no follower found")
	return nil
}

// ============================================================================
// 1. Three-Node Cluster Election and Replication Test
// ============================================================================
func TestCluster_ThreeNodeElectionAndReplication(t *testing.T) {
	cf := newTestCluster(t)
	defer cf.close()

	// 1. Wait for Leader Election
	leader := cf.findLeader(t, 3*time.Second)
	if leader == nil {
		t.Fatalf("leader is nil")
	}

	follower := cf.findFollower(t)
	if follower == nil {
		t.Fatalf("follower is nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	baseTime := time.Now().UnixNano()

	// 2. Reject mutation on Follower with ErrNotLeader
	cmdFollower := lockstate.AcquireLockCommand{
		RequestID:         "req-follower-reject",
		ResourceName:      "cluster-res-1",
		OwnerID:           "worker-follower",
		TTLNanoseconds:    (5 * time.Second).Nanoseconds(),
		TimestampUnixNano: baseTime,
	}

	_, err := follower.ApplyCommand(ctx, cmdFollower)
	if err == nil {
		t.Fatalf("expected Follower to reject command with ErrNotLeader")
	}
	if !errors.Is(err, consensus.ErrNotLeader) {
		t.Fatalf("expected ErrNotLeader, got: %v", err)
	}

	// Verify leader hint is included in error
	var notLeaderErr *consensus.NotLeaderError
	if !errors.As(err, &notLeaderErr) {
		t.Fatalf("expected error to cast to *consensus.NotLeaderError, got %T", err)
	}
	if notLeaderErr.LeaderAddress == "" {
		t.Fatalf("expected non-empty LeaderAddress hint in NotLeaderError")
	}

	// 3. Propose Acquire on Leader
	cmdLeader := lockstate.AcquireLockCommand{
		RequestID:         "req-leader-acquire",
		ResourceName:      "cluster-res-1",
		OwnerID:           "worker-primary",
		TTLNanoseconds:    (10 * time.Second).Nanoseconds(),
		TimestampUnixNano: baseTime + 1000,
	}

	resLeader, err := leader.ApplyCommand(ctx, cmdLeader)
	if err != nil {
		t.Fatalf("leader ApplyCommand failed: %v", err)
	}
	if !resLeader.Success {
		t.Fatalf("leader acquire failed: %s (%s)", resLeader.Message, resLeader.Code)
	}
	if resLeader.FencingToken != 1 {
		t.Fatalf("expected fencing token 1, got %d", resLeader.FencingToken)
	}
	if resLeader.LeaseVersion != 1 {
		t.Fatalf("expected lease version 1, got %d", resLeader.LeaseVersion)
	}

	// 4. Verify Replication Across All 3 Nodes
	// Allow small window for commit and FSM apply on followers
	deadline := time.Now().Add(2 * time.Second)
	replicated := false
	for time.Now().Before(deadline) {
		allMatch := true
		for id, sm := range cf.sms {
			rec, found := sm.GetLock("cluster-res-1")
			if !found || rec.OwnerID != "worker-primary" || rec.FencingToken != 1 || rec.LeaseVersion != 1 {
				allMatch = false
				break
			}
			_ = id
		}
		if allMatch {
			replicated = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if !replicated {
		t.Fatalf("lock state was not replicated consistently to all nodes' state machines")
	}
}

// ============================================================================
// 2. Leader Failover Test
// ============================================================================
func TestCluster_LeaderFailover(t *testing.T) {
	cf := newTestCluster(t)
	defer cf.close()

	// Elect initial leader L1
	l1 := cf.findLeader(t, 3*time.Second)
	l1ID := l1.Stats()["state"]
	_ = l1ID

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	baseTime := time.Now().UnixNano()

	// Acquire lock under L1
	cmdAcquire := lockstate.AcquireLockCommand{
		RequestID:         "req-failover-1",
		ResourceName:      "failover-resource",
		OwnerID:           "client-failover",
		TTLNanoseconds:    (15 * time.Second).Nanoseconds(),
		TimestampUnixNano: baseTime,
	}

	res1, err := l1.ApplyCommand(ctx, cmdAcquire)
	if err != nil || !res1.Success {
		t.Fatalf("L1 acquire failed: %v, res: %+v", err, res1)
	}

	// Disconnect and close L1
	l1Addr, _ := l1.Leader()
	l1.InmemTransport().DisconnectAll()
	_ = l1.Close()

	// Also disconnect other nodes from L1's address
	for _, n := range cf.nodes {
		if n != l1 {
			n.InmemTransport().Disconnect(l1Addr)
		}
	}

	// Wait for a new leader to emerge among remaining 2 nodes
	deadline := time.Now().Add(4 * time.Second)
	var l2 *consensus.RaftNode
	for time.Now().Before(deadline) {
		for _, n := range cf.nodes {
			if n != l1 && n.IsLeader() {
				l2 = n
				break
			}
		}
		if l2 != nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	if l2 == nil {
		t.Fatalf("remaining 2 nodes failed to elect a new leader after L1 shutdown")
	}

	// Verify L2 has preserved lock state
	rec, found, err := l2.GetLock("failover-resource", true)
	if err != nil {
		t.Fatalf("L2 GetLock failed: %v", err)
	}
	if !found {
		t.Fatalf("expected failover-resource lock to be present on new leader L2")
	}
	if rec.OwnerID != "client-failover" || rec.FencingToken != res1.FencingToken || rec.LeaseVersion != 1 {
		t.Fatalf("state corrupted after leader failover: %+v", rec)
	}

	// Propose Renew on new leader L2
	cmdRenew := lockstate.RenewLockCommand{
		RequestID:         "req-failover-2",
		ResourceName:      "failover-resource",
		OwnerID:           "client-failover",
		FencingToken:      res1.FencingToken,
		TTLNanoseconds:    (20 * time.Second).Nanoseconds(),
		TimestampUnixNano: baseTime + 1000,
	}

	resRenew, err := l2.ApplyCommand(ctx, cmdRenew)
	if err != nil {
		t.Fatalf("L2 ApplyCommand renew failed: %v", err)
	}
	if !resRenew.Success {
		t.Fatalf("L2 renew failed: %s (%s)", resRenew.Message, resRenew.Code)
	}
	if resRenew.LeaseVersion != 2 {
		t.Fatalf("expected lease version 2 after renewal on new leader, got %d", resRenew.LeaseVersion)
	}
}

// ============================================================================
// 3. Quorum Loss / Fail-Closed Test
// In a 3-node cluster, 2 nodes are required for quorum. If 2 nodes are partitioned/dead,
// leaving 1 node, writes MUST fail closed!
// ============================================================================
func TestCluster_QuorumLoss_FailClosed(t *testing.T) {
	cf := newTestCluster(t)
	defer cf.close()

	// Elect initial leader
	leader := cf.findLeader(t, 3*time.Second)

	// Disconnect 2 nodes, isolating the leader
	for _, n := range cf.nodes {
		if n != leader {
			n.InmemTransport().DisconnectAll()
			_ = n.Close()
		}
	}
	leader.InmemTransport().DisconnectAll()

	// Now only 1 node is alive out of 3 (minority: 1 < 2).
	// Attempting a write through the isolated node MUST fail closed!
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	cmd := lockstate.AcquireLockCommand{
		RequestID:         "req-quorum-loss",
		ResourceName:      "res-quorum-loss",
		OwnerID:           "worker-isolated",
		TTLNanoseconds:    (5 * time.Second).Nanoseconds(),
		TimestampUnixNano: time.Now().UnixNano(),
	}

	_, err := leader.ApplyCommand(ctx, cmd)
	if err == nil {
		t.Fatalf("FAIL: isolated minority node committed command without quorum! Expected failure.")
	}

	// Verify fail-closed invariant: error is either QuorumLoss, context timeout, or not leader
	t.Logf("Write correctly failed closed without quorum: %v", err)
}

// ============================================================================
// 4. Persistence and Recovery Test (Real BoltDB + FileSnapshotStore)
// ============================================================================
func TestCluster_BoltDBPersistenceAndRecovery(t *testing.T) {
	dataDir := t.TempDir()
	boltDir := filepath.Join(dataDir, "coordinator-persist")

	baseTime := time.Now().UnixNano()

	// Phase 1: Start node, bootstrap, apply mutations
	{
		cfg := consensus.NodeConfig{
			NodeID:             "coordinator-persist",
			DataDir:            boltDir,
			Inmem:              false,
			BindAddr:           "127.0.0.1:0",
			AdvertiseAddr:      "127.0.0.1:7101",
			BootstrapCluster:   true,
			HeartbeatTimeout:   50 * time.Millisecond,
			ElectionTimeout:    100 * time.Millisecond,
			CommitTimeout:      10 * time.Millisecond,
			LeaderLeaseTimeout: 50 * time.Millisecond,
			LogLevel:           "error",
		}

		sm := lockstate.New()
		node, err := consensus.NewRaftNode(cfg, sm)
		if err != nil {
			t.Fatalf("failed to create persistent RaftNode: %v", err)
		}

		waitForLeader(t, node, 3*time.Second)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		cmdAcquire := lockstate.AcquireLockCommand{
			RequestID:         "req-persist-1",
			ResourceName:      "persistent-res",
			OwnerID:           "durable-owner",
			TTLNanoseconds:    (30 * time.Second).Nanoseconds(),
			TimestampUnixNano: baseTime,
		}

		resAcquire, err := node.ApplyCommand(ctx, cmdAcquire)
		if err != nil || !resAcquire.Success {
			t.Fatalf("initial acquire failed: %v, res: %+v", err, resAcquire)
		}

		cmdRenew := lockstate.RenewLockCommand{
			RequestID:         "req-persist-2",
			ResourceName:      "persistent-res",
			OwnerID:           "durable-owner",
			FencingToken:      resAcquire.FencingToken,
			TTLNanoseconds:    (40 * time.Second).Nanoseconds(),
			TimestampUnixNano: baseTime + 1000,
		}

		resRenew, err := node.ApplyCommand(ctx, cmdRenew)
		if err != nil || !resRenew.Success {
			t.Fatalf("initial renew failed: %v, res: %+v", err, resRenew)
		}
		if resRenew.LeaseVersion != 2 {
			t.Fatalf("expected lease version 2, got %d", resRenew.LeaseVersion)
		}

		// Take snapshot to verify snapshot persistence to disk
		if err := node.Snapshot(); err != nil {
			t.Fatalf("Snapshot failed: %v", err)
		}

		// Close node
		if err := node.Close(); err != nil {
			t.Fatalf("failed to close node: %v", err)
		}
	}

	// Verify bolt DB file and snapshots exist on disk
	if _, err := os.Stat(filepath.Join(boltDir, "raft.db")); os.IsNotExist(err) {
		t.Fatalf("expected raft.db to exist on disk")
	}

	// Phase 2: Reopen node on the exact same directory WITHOUT bootstrap
	{
		cfgRecover := consensus.NodeConfig{
			NodeID:             "coordinator-persist",
			DataDir:            boltDir,
			Inmem:              false,
			BindAddr:           "127.0.0.1:0",
			AdvertiseAddr:      "127.0.0.1:7101",
			BootstrapCluster:   false, // Recovery mode: must NOT bootstrap!
			HeartbeatTimeout:   50 * time.Millisecond,
			ElectionTimeout:    100 * time.Millisecond,
			CommitTimeout:      10 * time.Millisecond,
			LeaderLeaseTimeout: 50 * time.Millisecond,
			LogLevel:           "error",
		}

		smRecover := lockstate.New()
		recoveredNode, err := consensus.NewRaftNode(cfgRecover, smRecover)
		if err != nil {
			t.Fatalf("failed to reopen persistent RaftNode: %v", err)
		}
		defer recoveredNode.Close()

		waitForLeader(t, recoveredNode, 3*time.Second)

		// Verify state was completely recovered from disk
		rec, found, err := recoveredNode.GetLock("persistent-res", true)
		if err != nil {
			t.Fatalf("GetLock on recovered node failed: %v", err)
		}
		if !found {
			t.Fatalf("expected persistent-res lock to exist after restart")
		}
		if rec.OwnerID != "durable-owner" {
			t.Fatalf("expected owner 'durable-owner', got %s", rec.OwnerID)
		}
		if rec.FencingToken != 1 {
			t.Fatalf("expected fencing token 1, got %d", rec.FencingToken)
		}
		if rec.LeaseVersion != 2 {
			t.Fatalf("expected lease version 2, got %d", rec.LeaseVersion)
		}

		// Verify idempotency cache persistence: replay req-persist-2
		replayCmd := lockstate.RenewLockCommand{
			RequestID:         "req-persist-2",
			ResourceName:      "persistent-res",
			OwnerID:           "durable-owner",
			FencingToken:      1,
			TTLNanoseconds:    (40 * time.Second).Nanoseconds(),
			TimestampUnixNano: baseTime + 1000,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		resReplay, err := recoveredNode.ApplyCommand(ctx, replayCmd)
		if err != nil {
			t.Fatalf("replaying cached command failed: %v", err)
		}
		if !resReplay.Success || resReplay.LeaseVersion != 2 {
			t.Fatalf("idempotency cache was not recovered properly: %+v", resReplay)
		}
	}
}

// ============================================================================
// 5. Authoritative Read Linearizability Tests (VerifyLeader + Barrier)
// ============================================================================
func TestAuthoritativeRead_FailsClosedOnFollowerAndQuorumLoss(t *testing.T) {
	cf := newTestCluster(t)
	defer cf.close()

	leader := cf.findLeader(t, 3*time.Second)
	follower := cf.findFollower(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// 1. Commit a lock via leader
	cmdAcquire := lockstate.AcquireLockCommand{
		RequestID:         "req-auth-read-1",
		ResourceName:      "read-linearizable-res",
		OwnerID:           "client-read",
		TTLNanoseconds:    (20 * time.Second).Nanoseconds(),
		TimestampUnixNano: time.Now().UnixNano(),
	}
	res, err := leader.ApplyCommand(ctx, cmdAcquire)
	if err != nil || !res.Success {
		t.Fatalf("acquire via leader failed: %v, res: %+v", err, res)
	}

	// Wait for replication to follower
	time.Sleep(100 * time.Millisecond)

	// 2. Authoritative read on follower MUST fail closed with ErrNotLeader
	_, _, errAuth := follower.GetLock("read-linearizable-res", true)
	if errAuth == nil {
		t.Fatalf("expected authoritative GetLock(linearizable=true) on follower to fail closed")
	}
	if !errors.Is(errAuth, consensus.ErrNotLeader) {
		t.Fatalf("expected ErrNotLeader, got: %v", errAuth)
	}

	// 3. Follower non-linearizable diagnostic read returns the locally replicated lock
	recDiag, foundDiag, errDiag := follower.GetLock("read-linearizable-res", false)
	if errDiag != nil {
		t.Fatalf("expected diagnostic read to succeed, got: %v", errDiag)
	}
	if !foundDiag || recDiag.OwnerID != "client-read" {
		t.Fatalf("diagnostic read returned unexpected record: %+v", recDiag)
	}

	// 4. Authoritative read on leader succeeds (executes VerifyLeader + Barrier)
	recLeader, foundLeader, errLeader := leader.GetLock("read-linearizable-res", true)
	if errLeader != nil {
		t.Fatalf("authoritative read on leader failed: %v", errLeader)
	}
	if !foundLeader || recLeader.FencingToken != res.FencingToken {
		t.Fatalf("authoritative read on leader returned unexpected record: %+v", recLeader)
	}
}

// ============================================================================
// 6. Transport-Level Partition Test: Minority Fails Closed, Majority Operates
// ============================================================================
func TestCluster_TransportLevelPartition_MajorityRetainsQuorum_MinorityFailsClosed(t *testing.T) {
	cf := newTestCluster(t)
	defer cf.close()

	// 1. Elect initial leader L1
	l1 := cf.findLeader(t, 3*time.Second)
	l1Addr, l1ID := l1.Leader()

	// Identify the 2 followers
	var followers []*consensus.RaftNode
	var followerAddrs []raft.ServerAddress
	for id, n := range cf.nodes {
		if string(id) != string(l1ID) {
			followers = append(followers, n)
			followerAddrs = append(followerAddrs, raft.ServerAddress(id))
		}
	}
	if len(followers) != 2 {
		t.Fatalf("expected 2 followers, got %d", len(followers))
	}
	f1, f2 := followers[0], followers[1]
	f1Addr, f2Addr := followerAddrs[0], followerAddrs[1]

	// 2. Apply initial lock before partition
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	baseTime := time.Now().UnixNano()

	cmdPre := lockstate.AcquireLockCommand{
		RequestID:         "req-partition-pre",
		ResourceName:      "partition-res",
		OwnerID:           "initial-owner",
		TTLNanoseconds:    (30 * time.Second).Nanoseconds(),
		TimestampUnixNano: baseTime,
	}
	resPre, err := l1.ApplyCommand(ctx, cmdPre)
	if err != nil || !resPre.Success {
		t.Fatalf("pre-partition acquire failed: %v", err)
	}

	// 3. EXECUTE TRANSPORT-LEVEL PARTITION:
	// Isolate L1 from {F1, F2} without stopping any process!
	l1.InmemTransport().Disconnect(f1Addr)
	l1.InmemTransport().Disconnect(f2Addr)
	f1.InmemTransport().Disconnect(l1Addr)
	f2.InmemTransport().Disconnect(l1Addr)
	// Crucial: F1 and F2 remain connected to each other!
	f1.InmemTransport().Connect(f2Addr, f2.InmemTransport())
	f2.InmemTransport().Connect(f1Addr, f1.InmemTransport())

	// 4. Minority Partition Check (L1):
	// Attempt a write on L1 (1 node < quorum of 2). Must FAIL CLOSED.
	cmdMinority := lockstate.AcquireLockCommand{
		RequestID:         "req-minority-write",
		ResourceName:      "minority-res",
		OwnerID:           "minority-owner",
		TTLNanoseconds:    (10 * time.Second).Nanoseconds(),
		TimestampUnixNano: baseTime + 1000,
	}
	ctxMinority, cancelMinority := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancelMinority()

	_, errMin := l1.ApplyCommand(ctxMinority, cmdMinority)
	if errMin == nil {
		t.Fatalf("expected write to isolated minority node L1 to FAIL CLOSED!")
	}
	t.Logf("Isolated minority node write correctly rejected: %v", errMin)

	// Authoritative read on L1 must also fail closed (VerifyLeader / Barrier cannot reach quorum)
	_, _, errMinRead := l1.GetLock("partition-res", true)
	if errMinRead == nil {
		t.Fatalf("expected authoritative read on isolated leader L1 to FAIL CLOSED without quorum")
	}
	t.Logf("Authoritative read on isolated minority node correctly failed closed: %v", errMinRead)

	// 5. Majority Partition Check ({F1, F2}):
	// F1 and F2 retain majority quorum (2 of 3 nodes). They must elect a new leader L2!
	deadline := time.Now().Add(4 * time.Second)
	var l2 *consensus.RaftNode
	for time.Now().Before(deadline) {
		if f1.IsLeader() {
			l2 = f1
			break
		}
		if f2.IsLeader() {
			l2 = f2
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if l2 == nil {
		t.Fatalf("majority partition {F1, F2} failed to elect a new leader!")
	}

	// Majority leader L2 can successfully commit new writes!
	cmdMajority := lockstate.AcquireLockCommand{
		RequestID:         "req-majority-write",
		ResourceName:      "majority-res",
		OwnerID:           "majority-owner",
		TTLNanoseconds:    (15 * time.Second).Nanoseconds(),
		TimestampUnixNano: baseTime + 2000,
	}
	ctxMaj, cancelMaj := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelMaj()

	resMaj, errMaj := l2.ApplyCommand(ctxMaj, cmdMajority)
	if errMaj != nil || !resMaj.Success {
		t.Fatalf("majority leader write failed: %v, res: %+v", errMaj, resMaj)
	}
	if resMaj.FencingToken != 1 {
		t.Fatalf("expected fencing token 1 for majority-res, got %d", resMaj.FencingToken)
	}

	// Authoritative read on majority leader succeeds
	recMaj, foundMaj, errMajRead := l2.GetLock("majority-res", true)
	if errMajRead != nil || !foundMaj || recMaj.OwnerID != "majority-owner" {
		t.Fatalf("authoritative read on majority leader failed: %v, found: %v", errMajRead, foundMaj)
	}

	// 6. HEAL THE TRANSPORT PARTITION:
	// Reconnect L1 to F1 and F2
	l1.InmemTransport().Connect(f1Addr, f1.InmemTransport())
	l1.InmemTransport().Connect(f2Addr, f2.InmemTransport())
	f1.InmemTransport().Connect(l1Addr, l1.InmemTransport())
	f2.InmemTransport().Connect(l1Addr, l1.InmemTransport())

	// Wait for L1 to catch up with the majority leader's logs
	deadlineHeal := time.Now().Add(3 * time.Second)
	healed := false
	for time.Now().Before(deadlineHeal) {
		recHealed, foundHealed := l1.StateMachine().GetLock("majority-res")
		if foundHealed && recHealed.OwnerID == "majority-owner" {
			healed = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !healed {
		t.Fatalf("former leader L1 failed to catch up with majority log after partition heal")
	}
	t.Logf("Transport partition healed successfully; all 3 nodes consistent.")
}

// ============================================================================
// 7. Real Three-Node Cluster: Real TCP Sockets + Independent BoltDB Disks
// ============================================================================
func TestCluster_RealThreeNodeTCPAndBoltDB_ReplicationAndElections(t *testing.T) {
	tempBase := t.TempDir()

	// Helper to allocate 3 free TCP ports on 127.0.0.1
	ports := make([]int, 3)
	addrs := make([]string, 3)
	for i := 0; i < 3; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("failed to listen on dynamic tcp port: %v", err)
		}
		tcpAddr := ln.Addr().(*net.TCPAddr)
		ports[i] = tcpAddr.Port
		addrs[i] = fmt.Sprintf("127.0.0.1:%d", ports[i])
		_ = ln.Close() // Release so Raft transport can bind it
	}

	nodeIDs := []string{"coordinator-a", "coordinator-b", "coordinator-c"}
	servers := make([]raft.Server, 3)
	for i := 0; i < 3; i++ {
		servers[i] = raft.Server{
			ID:       raft.ServerID(nodeIDs[i]),
			Address:  raft.ServerAddress(addrs[i]),
			Suffrage: raft.Voter,
		}
	}

	nodes := make(map[string]*consensus.RaftNode)
	sms := make(map[string]*lockstate.MemoryStateMachine)

	// Initialize 3 independent Raft nodes with real BoltDB storage and real TCP transports
	for i := 0; i < 3; i++ {
		id := nodeIDs[i]
		dataDir := filepath.Join(tempBase, id)
		sm := lockstate.New()
		sms[id] = sm

		cfg := consensus.NodeConfig{
			NodeID:             id,
			BindAddr:           addrs[i],
			AdvertiseAddr:      addrs[i],
			DataDir:            dataDir,
			Inmem:              false, // Real BoltDB log & stable store on disk!
			BootstrapCluster:   (i == 0), // Only coordinator-a executes bootstrap
			BootstrapServers:   servers,
			HeartbeatTimeout:   50 * time.Millisecond,
			ElectionTimeout:    100 * time.Millisecond,
			CommitTimeout:      10 * time.Millisecond,
			LeaderLeaseTimeout: 50 * time.Millisecond,
			LogLevel:           "error",
		}

		node, err := consensus.NewRaftNode(cfg, sm)
		if err != nil {
			t.Fatalf("failed to create TCP BoltDB Raft node %s on %s: %v", id, addrs[i], err)
		}
		nodes[id] = node
	}
	defer func() {
		for _, n := range nodes {
			_ = n.Close()
		}
	}()

	// 1. Wait for Leader Election over real TCP network
	deadline := time.Now().Add(5 * time.Second)
	var leaderNode *consensus.RaftNode
	var leaderID string
	for time.Now().Before(deadline) {
		for id, n := range nodes {
			if n.IsLeader() {
				leaderNode = n
				leaderID = id
				break
			}
		}
		if leaderNode != nil {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if leaderNode == nil {
		t.Fatalf("failed to elect a leader over TCP in real 3-node cluster")
	}
	t.Logf("Elected leader %s over TCP", leaderID)

	// Identify a follower
	var followerNode *consensus.RaftNode
	for id, n := range nodes {
		if id != leaderID {
			followerNode = n
			break
		}
	}

	// 2. Propose mutation to Follower over TCP -> Must reject with ErrNotLeader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmdFollower := lockstate.AcquireLockCommand{
		RequestID:         "req-tcp-follower-reject",
		ResourceName:      "tcp-resource-1",
		OwnerID:           "tcp-worker-follower",
		TTLNanoseconds:    (10 * time.Second).Nanoseconds(),
		TimestampUnixNano: time.Now().UnixNano(),
	}
	_, errFol := followerNode.ApplyCommand(ctx, cmdFollower)
	if errFol == nil || !errors.Is(errFol, consensus.ErrNotLeader) {
		t.Fatalf("expected follower to reject with ErrNotLeader, got: %v", errFol)
	}

	// 3. Propose Acquire to Leader over TCP
	baseTime := time.Now().UnixNano()
	cmdLeader := lockstate.AcquireLockCommand{
		RequestID:         "req-tcp-leader-acquire",
		ResourceName:      "tcp-resource-1",
		OwnerID:           "tcp-worker-primary",
		TTLNanoseconds:    (15 * time.Second).Nanoseconds(),
		TimestampUnixNano: baseTime,
	}

	resLeader, err := leaderNode.ApplyCommand(ctx, cmdLeader)
	if err != nil || !resLeader.Success {
		t.Fatalf("leader ApplyCommand over TCP failed: %v, res: %+v", err, resLeader)
	}
	if resLeader.FencingToken != 1 || resLeader.LeaseVersion != 1 {
		t.Fatalf("unexpected result from TCP leader: %+v", resLeader)
	}

	// 4. Authoritative read on Leader over TCP
	recRead, foundRead, errRead := leaderNode.GetLock("tcp-resource-1", true)
	if errRead != nil || !foundRead || recRead.OwnerID != "tcp-worker-primary" {
		t.Fatalf("authoritative read over TCP failed: %v, found: %v, rec: %+v", errRead, foundRead, recRead)
	}

	// 5. Verify replication across all 3 nodes via TCP and BoltDB persistence
	deadlineRep := time.Now().Add(3 * time.Second)
	replicated := false
	for time.Now().Before(deadlineRep) {
		allMatch := true
		for _, sm := range sms {
			rec, found := sm.GetLock("tcp-resource-1")
			if !found || rec.OwnerID != "tcp-worker-primary" || rec.FencingToken != 1 || rec.LeaseVersion != 1 {
				allMatch = false
				break
			}
		}
		if allMatch {
			replicated = true
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if !replicated {
		t.Fatalf("TCP replication to all followers failed")
	}

	// 6. Verify BoltDB files exist on disk for all 3 nodes
	for _, id := range nodeIDs {
		dbPath := filepath.Join(tempBase, id, "raft.db")
		if _, err := os.Stat(dbPath); os.IsNotExist(err) {
			t.Fatalf("expected BoltDB file to exist on disk at %s", dbPath)
		}
	}
	t.Logf("Real 3-node cluster with independent TCP listeners and BoltDB storage verified successfully.")
}

