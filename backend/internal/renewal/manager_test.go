package renewal

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lockguard/backend/internal/config"
	"lockguard/backend/internal/heartbeat"
	"lockguard/backend/internal/metrics"
)

type mockLockClient struct {
	mu           sync.Mutex
	renewFunc    func(ctx context.Context, req RenewLockRequest) (*RenewLockResponse, error)
	getLockFunc  func(ctx context.Context, resourceName string) (*LockRecord, bool, error)
	renewCalls   int32
	lastReqID    string
	recordedReqs []RenewLockRequest
}

func (m *mockLockClient) RenewLock(ctx context.Context, req RenewLockRequest) (*RenewLockResponse, error) {
	atomic.AddInt32(&m.renewCalls, 1)
	m.mu.Lock()
	m.lastReqID = req.RequestID
	m.recordedReqs = append(m.recordedReqs, req)
	fn := m.renewFunc
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, req)
	}
	return &RenewLockResponse{
		Success: true,
		Record: &LockRecord{
			ResourceName:       req.ResourceName,
			OwnerID:            req.OwnerID,
			FencingToken:       req.FencingToken,
			LeaseVersion:       2,
			ExpiresAtUnixNano:  time.Now().Add(10 * time.Second).UnixNano(),
			TTLNanoseconds:     req.TTLNanoseconds,
			AcquiredAtUnixNano: time.Now().UnixNano(),
		},
	}, nil
}

func (m *mockLockClient) GetLock(ctx context.Context, resourceName string) (*LockRecord, bool, error) {
	m.mu.Lock()
	fn := m.getLockFunc
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, resourceName)
	}
	return &LockRecord{
		ResourceName:       resourceName,
		ExpiresAtUnixNano:  time.Now().Add(10 * time.Minute).UnixNano(),
		AcquiredAtUnixNano: time.Now().UnixNano(),
	}, true, nil
}

func TestRenewalManager_SuccessfulRenewalLifecycle(t *testing.T) {
	cfg := &config.Config{
		Renewal: config.RenewalConfig{
			SafetyMargin: 10 * time.Millisecond,
			RetryInitial: 10 * time.Millisecond,
			RetryMax:     50 * time.Millisecond,
			MaxAttempts:  3,
		},
	}

	reg := metrics.NewRegistry()
	hbMgr := heartbeat.NewManager(&config.Config{
		Heartbeat: config.HeartbeatConfig{Timeout: 10 * time.Second, CleanupInterval: 50 * time.Millisecond},
	}, reg)
	hbMgr.Start()
	defer hbMgr.Stop()

	sess, err := hbMgr.Register("worker-client-1", "alice", 10*time.Second)
	if err != nil {
		t.Fatalf("failed to register heartbeat session: %v", err)
	}

	var currentVersion uint64 = 1
	var fencingToken uint64 = 5005

	client := &mockLockClient{}
	client.renewFunc = func(ctx context.Context, req RenewLockRequest) (*RenewLockResponse, error) {
		if req.FencingToken != fencingToken {
			t.Errorf("expected fencing token %d, got %d", fencingToken, req.FencingToken)
		}
		currentVersion++
		newExpires := time.Now().Add(100 * time.Millisecond).UnixNano()
		return &RenewLockResponse{
			Success: true,
			Record: &LockRecord{
				ResourceName:       req.ResourceName,
				OwnerID:            req.OwnerID,
				FencingToken:       req.FencingToken,
				LeaseVersion:       currentVersion,
				ExpiresAtUnixNano:  newExpires,
				TTLNanoseconds:     req.TTLNanoseconds,
				AcquiredAtUnixNano: time.Now().UnixNano(),
			},
		}, nil
	}

	mgr := NewManager(cfg, client, hbMgr, reg)
	defer mgr.Shutdown()

	// Initial expiration in 60ms; with TTL=60ms, window = 20ms, safety = 10ms -> renewAt ~ 30ms
	initialExpiry := time.Now().Add(60 * time.Millisecond).UnixNano()
	err = mgr.StartRenewal("table-orders", "alice", "worker-client-1", sess.SessionID(), fencingToken, 1, initialExpiry, 60*time.Millisecond)
	if err != nil {
		t.Fatalf("failed to start renewal: %v", err)
	}

	// Wait for at least 2 renewals to execute
	time.Sleep(120 * time.Millisecond)

	calls := atomic.LoadInt32(&client.renewCalls)
	if calls < 1 {
		t.Fatalf("expected at least 1 renewal attempt, got %d", calls)
	}

	st, found := mgr.GetStatus("table-orders")
	if !found {
		t.Fatalf("worker status not found")
	}
	if st.FencingToken != fencingToken {
		t.Errorf("fencing token mismatch: %d != %d", st.FencingToken, fencingToken)
	}
	if st.LeaseVersion <= 1 {
		t.Errorf("lease version should have incremented beyond 1, got %d", st.LeaseVersion)
	}
}

func TestRenewalManager_SessionTimeoutCancelsRenewal(t *testing.T) {
	cfg := &config.Config{
		Renewal: config.RenewalConfig{
			SafetyMargin: 10 * time.Millisecond,
			RetryInitial: 10 * time.Millisecond,
			RetryMax:     50 * time.Millisecond,
			MaxAttempts:  2,
		},
	}

	reg := metrics.NewRegistry()
	hbMgr := heartbeat.NewManager(&config.Config{
		Heartbeat: config.HeartbeatConfig{Timeout: 50 * time.Millisecond, CleanupInterval: 20 * time.Millisecond},
	}, reg)
	hbMgr.Start()
	defer hbMgr.Stop()

	sess, _ := hbMgr.Register("worker-client-2", "bob", 50*time.Millisecond)

	client := &mockLockClient{}
	mgr := NewManager(cfg, client, hbMgr, reg)
	defer mgr.Shutdown()

	// Schedule renewal far in the future
	futureExpiry := time.Now().Add(500 * time.Millisecond).UnixNano()
	_ = mgr.StartRenewal("table-payments", "bob", "worker-client-2", sess.SessionID(), 100, 1, futureExpiry, 500*time.Millisecond)

	// Allow heartbeat session to expire
	time.Sleep(100 * time.Millisecond)

	// Worker should have been halted and removed
	_, found := mgr.GetStatus("table-payments")
	if found {
		t.Fatalf("worker should have been terminated after heartbeat session expired")
	}
}

func TestRenewalManager_AmbiguousTimeoutReusesRequestID(t *testing.T) {
	cfg := &config.Config{
		Renewal: config.RenewalConfig{
			SafetyMargin: 5 * time.Millisecond,
			RetryInitial: 10 * time.Millisecond,
			RetryMax:     30 * time.Millisecond,
			MaxAttempts:  3,
		},
	}

	reg := metrics.NewRegistry()
	hbMgr := heartbeat.NewManager(&config.Config{
		Heartbeat: config.HeartbeatConfig{Timeout: 10 * time.Second, CleanupInterval: 100 * time.Millisecond},
	}, reg)
	hbMgr.Start()
	defer hbMgr.Stop()

	sess, _ := hbMgr.Register("worker-client-3", "charlie", 10*time.Second)

	client := &mockLockClient{}
	var attemptCount int32
	client.renewFunc = func(ctx context.Context, req RenewLockRequest) (*RenewLockResponse, error) {
		att := atomic.AddInt32(&attemptCount, 1)
		if att == 1 {
			// First attempt simulates network timeout
			return nil, fmt.Errorf("context deadline exceeded")
		}
		// Second attempt succeeds
		return &RenewLockResponse{
			Success: true,
			Record: &LockRecord{
				ResourceName:       req.ResourceName,
				OwnerID:            req.OwnerID,
				FencingToken:       req.FencingToken,
				LeaseVersion:       2,
				ExpiresAtUnixNano:  time.Now().Add(500 * time.Millisecond).UnixNano(),
				TTLNanoseconds:     req.TTLNanoseconds,
				AcquiredAtUnixNano: time.Now().UnixNano(),
			},
		}, nil
	}

	mgr := NewManager(cfg, client, hbMgr, reg)
	defer mgr.Shutdown()

	immediateExpiry := time.Now().Add(20 * time.Millisecond).UnixNano()
	_ = mgr.StartRenewal("table-ledger", "charlie", "worker-client-3", sess.SessionID(), 777, 1, immediateExpiry, 20*time.Millisecond)

	time.Sleep(80 * time.Millisecond)

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.recordedReqs) < 2 {
		t.Fatalf("expected at least 2 attempts, got %d", len(client.recordedReqs))
	}

	// Idempotency check: First and second attempts MUST have the exact same RequestID!
	firstReqID := client.recordedReqs[0].RequestID
	secondReqID := client.recordedReqs[1].RequestID
	if firstReqID != secondReqID {
		t.Errorf("ambiguous timeout retry did NOT preserve RequestID: %s != %s", firstReqID, secondReqID)
	}
}

func TestRenewalManager_DuplicateWorkerPrevention(t *testing.T) {
	cfg := &config.Config{
		Renewal: config.RenewalConfig{
			SafetyMargin: 10 * time.Millisecond,
			RetryInitial: 10 * time.Millisecond,
			RetryMax:     50 * time.Millisecond,
			MaxAttempts:  2,
		},
	}

	reg := metrics.NewRegistry()
	hbMgr := heartbeat.NewManager(&config.Config{
		Heartbeat: config.HeartbeatConfig{Timeout: 10 * time.Second, CleanupInterval: 100 * time.Millisecond},
	}, reg)
	hbMgr.Start()
	defer hbMgr.Stop()

	sess, _ := hbMgr.Register("worker-client-4", "dave", 10*time.Second)

	client := &mockLockClient{}
	mgr := NewManager(cfg, client, hbMgr, reg)
	defer mgr.Shutdown()

	expiry := time.Now().Add(10 * time.Second).UnixNano()
	// Start worker 1
	_ = mgr.StartRenewal("shared-doc", "dave", "worker-client-4", sess.SessionID(), 99, 1, expiry, 10*time.Second)

	// Call StartRenewal again with same parameters
	_ = mgr.StartRenewal("shared-doc", "dave", "worker-client-4", sess.SessionID(), 99, 1, expiry, 10*time.Second)

	statuses := mgr.ListStatus()
	if len(statuses) != 1 {
		t.Fatalf("expected exactly 1 active worker, got %d", len(statuses))
	}
}

// Test exact one-version increment succeeds
func TestRenewalWorker_ExactOneVersionIncrementSucceeds(t *testing.T) {
	cfg := &config.Config{
		Renewal: config.RenewalConfig{
			SafetyMargin: 5 * time.Millisecond,
			RetryInitial: 5 * time.Millisecond,
			RetryMax:     10 * time.Millisecond,
			MaxAttempts:  2,
		},
	}

	reg := metrics.NewRegistry()
	client := &mockLockClient{}
	client.renewFunc = func(ctx context.Context, req RenewLockRequest) (*RenewLockResponse, error) {
		return &RenewLockResponse{
			Success: true,
			Record: &LockRecord{
				ResourceName:      req.ResourceName,
				OwnerID:           req.OwnerID,
				FencingToken:      req.FencingToken,
				LeaseVersion:      2, // exactly 1 + 1
				ExpiresAtUnixNano: time.Now().Add(500 * time.Millisecond).UnixNano(),
			},
		}, nil
	}

	mgr := NewManager(cfg, client, nil, reg)
	defer mgr.Shutdown()

	err := mgr.StartRenewal("res-exact", "alice", "client-1", "sess-1", 100, 1, time.Now().Add(15*time.Millisecond).UnixNano(), 15*time.Millisecond)
	if err != nil {
		t.Fatalf("failed to start renewal: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	st, found := mgr.GetStatus("res-exact")
	if !found {
		t.Fatalf("worker not found")
	}
	if st.LeaseVersion != 2 {
		t.Errorf("expected lease version 2, got %d", st.LeaseVersion)
	}
	if st.LastAttemptStatus != "success" {
		t.Errorf("expected success status, got %s", st.LastAttemptStatus)
	}
}

// Test unexpected multi-version advancement triggers reconciliation and terminates if superseded
func TestRenewalWorker_UnexpectedMultiVersionAdvancementTerminates(t *testing.T) {
	cfg := &config.Config{
		Renewal: config.RenewalConfig{
			SafetyMargin: 5 * time.Millisecond,
			RetryInitial: 5 * time.Millisecond,
			RetryMax:     10 * time.Millisecond,
			MaxAttempts:  2,
		},
	}

	reg := metrics.NewRegistry()
	client := &mockLockClient{}
	// Mock returns LeaseVersion: 5 (jumping past expected version 2)
	client.renewFunc = func(ctx context.Context, req RenewLockRequest) (*RenewLockResponse, error) {
		return &RenewLockResponse{
			Success: true,
			Record: &LockRecord{
				ResourceName:      req.ResourceName,
				OwnerID:           req.OwnerID,
				FencingToken:      req.FencingToken,
				LeaseVersion:      5,
				ExpiresAtUnixNano: time.Now().Add(500 * time.Millisecond).UnixNano(),
			},
		}, nil
	}
	// Reconcile GetLock: on initial start it matches, but on renewal reconciliation it confirms supersession
	var getLockCallCount int32
	client.getLockFunc = func(ctx context.Context, resourceName string) (*LockRecord, bool, error) {
		calls := atomic.AddInt32(&getLockCallCount, 1)
		if calls == 1 {
			// Initial StartRenewal verification: valid lock held by alice
			return &LockRecord{
				ResourceName:      resourceName,
				OwnerID:           "alice",
				FencingToken:      100,
				LeaseVersion:      1,
				ExpiresAtUnixNano: time.Now().Add(500 * time.Millisecond).UnixNano(),
			}, true, nil
		}
		// Subsequent reconciliation: lock superseded by another worker with newer token
		return &LockRecord{
			ResourceName:      resourceName,
			OwnerID:           "another-worker",
			FencingToken:      200,
			LeaseVersion:      5,
			ExpiresAtUnixNano: time.Now().Add(500 * time.Millisecond).UnixNano(),
		}, true, nil
	}

	mgr := NewManager(cfg, client, nil, reg)
	defer mgr.Shutdown()

	err := mgr.StartRenewal("res-jump", "alice", "client-1", "sess-1", 100, 1, time.Now().Add(15*time.Millisecond).UnixNano(), 15*time.Millisecond)
	if err != nil {
		t.Fatalf("failed to start renewal: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Worker should have detected supersession and terminated
	st, found := mgr.GetStatus("res-jump")
	if found && st.Active {
		t.Errorf("worker should have terminated on unexpected version advancement, status: %+v", st)
	}
}

// Test rejecting stale generations
func TestRenewalManager_StaleGenerationRejected(t *testing.T) {
	cfg := &config.Config{
		Renewal: config.RenewalConfig{
			SafetyMargin: 10 * time.Millisecond,
			RetryInitial: 10 * time.Millisecond,
			RetryMax:     50 * time.Millisecond,
			MaxAttempts:  2,
		},
	}

	reg := metrics.NewRegistry()
	client := &mockLockClient{}
	mgr := NewManager(cfg, client, nil, reg)
	defer mgr.Shutdown()

	future := time.Now().Add(10 * time.Second).UnixNano()
	// Start with fencing token 100, version 2
	err := mgr.StartRenewal("res-stale-gen", "alice", "client-1", "sess-1", 100, 2, future, 10*time.Second)
	if err != nil {
		t.Fatalf("initial start failed: %v", err)
	}

	// Stale fencing token 90 < 100
	errOldToken := mgr.StartRenewal("res-stale-gen", "alice", "client-1", "sess-1", 90, 3, future, 10*time.Second)
	if errOldToken != ErrStaleWorkerGeneration {
		t.Errorf("expected ErrStaleWorkerGeneration for older fencing token, got %v", errOldToken)
	}

	// Stale lease version 1 < 2 with same fencing token
	errOldVersion := mgr.StartRenewal("res-stale-gen", "alice", "client-1", "sess-1", 100, 1, future, 10*time.Second)
	if errOldVersion != ErrStaleWorkerGeneration {
		t.Errorf("expected ErrStaleWorkerGeneration for older lease version, got %v", errOldVersion)
	}
}

// Regression Test:
// Client A acquires -> session expires -> lease expires -> Client B acquires ->
// Client A delayed worker rejected -> Client B lock remains intact ->
// Client A worker termination does not delete Client B's worker!
func TestRenewalManager_SuccessiveGenerationsRegression(t *testing.T) {
	cfg := &config.Config{
		Renewal: config.RenewalConfig{
			SafetyMargin: 5 * time.Millisecond,
			RetryInitial: 5 * time.Millisecond,
			RetryMax:     10 * time.Millisecond,
			MaxAttempts:  2,
		},
	}

	reg := metrics.NewRegistry()
	client := &mockLockClient{}
	mgr := NewManager(cfg, client, nil, reg)
	defer mgr.Shutdown()

	now := time.Now()

	// 1. Client A starts renewal (epoch 1: token 100, version 1)
	errA := mgr.StartRenewal("critical-table", "client-a-owner", "client-a", "sess-a", 100, 1, now.Add(20*time.Millisecond).UnixNano(), 20*time.Millisecond)
	if errA != nil {
		t.Fatalf("start client A renewal failed: %v", errA)
	}

	// 2. Client B acquires new epoch (epoch 2: token 200, version 1)
	errB := mgr.StartRenewal("critical-table", "client-b-owner", "client-b", "sess-b", 200, 1, now.Add(500*time.Millisecond).UnixNano(), 500*time.Millisecond)
	if errB != nil {
		t.Fatalf("start client B renewal failed: %v", errB)
	}

	// Verify worker belongs to Client B
	stB, found := mgr.GetStatus("critical-table")
	if !found || stB.OwnerID != "client-b-owner" || stB.FencingToken != 200 {
		t.Fatalf("expected active worker to be client-b with token 200, got: %+v", stB)
	}

	// 3. Delayed attempt from Client A with stale epoch 1 is rejected
	errDelayedA := mgr.StartRenewal("critical-table", "client-a-owner", "client-a", "sess-a", 100, 1, now.Add(500*time.Millisecond).UnixNano(), 500*time.Millisecond)
	if errDelayedA != ErrStaleWorkerGeneration {
		t.Errorf("expected ErrStaleWorkerGeneration for delayed Client A, got: %v", errDelayedA)
	}

	// 4. Ensure Client B's worker is still active and intact
	time.Sleep(30 * time.Millisecond) // Allow any delayed termination goroutine to complete
	stAfter, foundAfter := mgr.GetStatus("critical-table")
	if !foundAfter {
		t.Fatalf("Client B worker was erroneously unregistered by terminating Client A worker!")
	}
	if stAfter.OwnerID != "client-b-owner" || stAfter.FencingToken != 200 {
		t.Errorf("Client B worker corrupted! %+v", stAfter)
	}
}

// Test authorization in StopRenewal
func TestRenewalManager_AuthorizationStopRenewal(t *testing.T) {
	cfg := &config.Config{
		Renewal: config.RenewalConfig{
			SafetyMargin: 10 * time.Millisecond,
			RetryInitial: 10 * time.Millisecond,
			RetryMax:     50 * time.Millisecond,
			MaxAttempts:  2,
		},
	}

	reg := metrics.NewRegistry()
	client := &mockLockClient{}
	mgr := NewManager(cfg, client, nil, reg)
	defer mgr.Shutdown()

	future := time.Now().Add(10 * time.Second).UnixNano()
	_ = mgr.StartRenewal("auth-res", "owner-1", "client-1", "correct-sess-123", 50, 1, future, 10*time.Second)

	// Stop with empty session ID must be rejected when worker has a session ID
	errEmpty := mgr.StopRenewal("auth-res", "")
	if errEmpty != ErrSessionMismatch {
		t.Errorf("expected ErrSessionMismatch for empty session ID, got %v", errEmpty)
	}

	// Stop with wrong session ID
	errWrong := mgr.StopRenewal("auth-res", "wrong-session-xyz")
	if errWrong != ErrSessionMismatch {
		t.Errorf("expected ErrSessionMismatch, got %v", errWrong)
	}

	// Stop with correct session ID
	errRight := mgr.StopRenewal("auth-res", "correct-sess-123")
	if errRight != nil {
		t.Errorf("expected nil error with correct session ID, got %v", errRight)
	}
}

func TestRenewalStart_AuthorizationGuarantees(t *testing.T) {
	cfg := &config.Config{
		Renewal: config.RenewalConfig{
			SafetyMargin: 10 * time.Millisecond,
			RetryInitial: 10 * time.Millisecond,
			RetryMax:     50 * time.Millisecond,
			MaxAttempts:  2,
		},
	}
	reg := metrics.NewRegistry()
	hbMgr := heartbeat.NewManager(&config.Config{
		Heartbeat: config.HeartbeatConfig{Timeout: 10 * time.Second, CleanupInterval: 100 * time.Millisecond},
	}, reg)
	hbMgr.Start()
	defer hbMgr.Stop()

	sessAlice, _ := hbMgr.Register("client-alice", "alice", 10*time.Second)

	client := &mockLockClient{}
	mgr := NewManager(cfg, client, hbMgr, reg)
	defer mgr.Shutdown()

	future := time.Now().Add(10 * time.Second).UnixNano()

	// 1. Lock not found in authoritative state
	client.getLockFunc = func(ctx context.Context, resourceName string) (*LockRecord, bool, error) {
		return nil, false, nil
	}
	errNotFound := mgr.StartRenewal("missing-res", "alice", "client-alice", sessAlice.SessionID(), 10, 1, future, 10*time.Second)
	if errNotFound != ErrLockNotFound {
		t.Errorf("expected ErrLockNotFound, got %v", errNotFound)
	}

	// 2. Lock belongs to another owner
	client.getLockFunc = func(ctx context.Context, resourceName string) (*LockRecord, bool, error) {
		return &LockRecord{
			ResourceName:      resourceName,
			OwnerID:           "legitimate-owner",
			FencingToken:      10,
			LeaseVersion:      1,
			ExpiresAtUnixNano: future,
		}, true, nil
	}
	errNotOwner := mgr.StartRenewal("hijack-res", "alice", "client-alice", sessAlice.SessionID(), 10, 1, future, 10*time.Second)
	if errNotOwner != ErrNotLockOwner {
		t.Errorf("expected ErrNotLockOwner when trying to renew another client's lock, got %v", errNotOwner)
	}

	// 3. Fencing token mismatch
	client.getLockFunc = func(ctx context.Context, resourceName string) (*LockRecord, bool, error) {
		return &LockRecord{
			ResourceName:      resourceName,
			OwnerID:           "alice",
			FencingToken:      25, // generation 25
			LeaseVersion:      1,
			ExpiresAtUnixNano: future,
		}, true, nil
	}
	errTokenMismatch := mgr.StartRenewal("token-res", "alice", "client-alice", sessAlice.SessionID(), 20, 1, future, 10*time.Second)
	if errTokenMismatch != ErrFencingTokenMismatch {
		t.Errorf("expected ErrFencingTokenMismatch for stale token generation, got %v", errTokenMismatch)
	}

	// 4. Stale lease version
	client.getLockFunc = func(ctx context.Context, resourceName string) (*LockRecord, bool, error) {
		return &LockRecord{
			ResourceName:      resourceName,
			OwnerID:           "alice",
			FencingToken:      25,
			LeaseVersion:      3, // current authoritative version is 3
			ExpiresAtUnixNano: future,
		}, true, nil
	}
	errStaleVersion := mgr.StartRenewal("version-res", "alice", "client-alice", sessAlice.SessionID(), 25, 2, future, 10*time.Second)
	if errStaleVersion != ErrStaleWorkerGeneration {
		t.Errorf("expected ErrStaleWorkerGeneration for stale lease version, got %v", errStaleVersion)
	}

	// 5. Lock lease already expired
	past := time.Now().Add(-1 * time.Second).UnixNano()
	client.getLockFunc = func(ctx context.Context, resourceName string) (*LockRecord, bool, error) {
		return &LockRecord{
			ResourceName:      resourceName,
			OwnerID:           "alice",
			FencingToken:      25,
			LeaseVersion:      1,
			ExpiresAtUnixNano: past,
		}, true, nil
	}
	errExpired := mgr.StartRenewal("expired-res", "alice", "client-alice", sessAlice.SessionID(), 25, 1, past, 10*time.Second)
	if errExpired != ErrLeaseExpired {
		t.Errorf("expected ErrLeaseExpired, got %v", errExpired)
	}

	// 6. Session owner mismatch
	client.getLockFunc = func(ctx context.Context, resourceName string) (*LockRecord, bool, error) {
		return &LockRecord{
			ResourceName:      resourceName,
			OwnerID:           "bob",
			FencingToken:      50,
			LeaseVersion:      1,
			ExpiresAtUnixNano: future,
		}, true, nil
	}
	// Alice attempts to use her session to renew for owner "bob"
	errOwnerMismatch := mgr.StartRenewal("cross-owner-res", "bob", "client-alice", sessAlice.SessionID(), 50, 1, future, 10*time.Second)
	if errOwnerMismatch != ErrUnauthorizedSession {
		t.Errorf("expected ErrUnauthorizedSession for session owner mismatch, got %v", errOwnerMismatch)
	}
}

