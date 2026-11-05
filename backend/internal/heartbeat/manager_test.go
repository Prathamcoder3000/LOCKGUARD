package heartbeat

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lockguard/backend/internal/config"
	"lockguard/backend/internal/metrics"
)

func TestHeartbeatManager_Lifecycle(t *testing.T) {
	cfg := &config.Config{
		Heartbeat: config.HeartbeatConfig{
			Interval:        50 * time.Millisecond,
			Timeout:         200 * time.Millisecond,
			CleanupInterval: 50 * time.Millisecond,
		},
	}

	reg := metrics.NewRegistry()
	mgr := NewManager(cfg, reg)
	mgr.Start()
	defer mgr.Stop()

	// 1. Register client
	sess, err := mgr.Register("worker-alpha", "user-1", 200*time.Millisecond)
	if err != nil {
		t.Fatalf("failed to register client: %v", err)
	}
	if !sess.IsActive() {
		t.Fatalf("new session should be active")
	}
	sessionID := sess.SessionID()

	// 2. Attach lock
	err = mgr.AttachLock("worker-alpha", sessionID, "res-db-1")
	if err != nil {
		t.Fatalf("failed to attach lock: %v", err)
	}
	if !sess.HasLock("res-db-1") {
		t.Fatalf("session should track res-db-1")
	}

	// 3. Heartbeat ping
	time.Sleep(50 * time.Millisecond)
	prevExpiry := sess.Snapshot().ExpiresAt
	sessUpdated, err := mgr.Heartbeat("worker-alpha", sessionID)
	if err != nil {
		t.Fatalf("heartbeat failed: %v", err)
	}
	if !sessUpdated.Snapshot().ExpiresAt.After(prevExpiry) {
		t.Errorf("heartbeat should extend expiration time")
	}

	// 4. Detach lock
	err = mgr.DetachLock("worker-alpha", sessionID, "res-db-1")
	if err != nil {
		t.Fatalf("failed to detach lock: %v", err)
	}
	if sess.HasLock("res-db-1") {
		t.Fatalf("session should not have res-db-1 after detach")
	}

	// 5. Explicit Close
	var closedCalled int32
	mgr.OnSessionExpired(func(s *ClientSession) {
		atomic.AddInt32(&closedCalled, 1)
	})

	err = mgr.CloseSession("worker-alpha", sessionID)
	if err != nil {
		t.Fatalf("failed to close session: %v", err)
	}
	if sess.IsActive() {
		t.Fatalf("closed session should not be active")
	}
	if atomic.LoadInt32(&closedCalled) == 0 {
		t.Fatalf("expected close callback to be triggered")
	}
}

func TestHeartbeatManager_MissedHeartbeatTimeout(t *testing.T) {
	cfg := &config.Config{
		Heartbeat: config.HeartbeatConfig{
			Interval:        20 * time.Millisecond,
			Timeout:         60 * time.Millisecond,
			CleanupInterval: 20 * time.Millisecond,
		},
	}

	reg := metrics.NewRegistry()
	mgr := NewManager(cfg, reg)

	var timeoutNotified int32
	mgr.OnSessionExpired(func(s *ClientSession) {
		if s.ClientID() == "worker-beta" {
			atomic.AddInt32(&timeoutNotified, 1)
		}
	})

	mgr.Start()
	defer mgr.Stop()

	sess, err := mgr.Register("worker-beta", "user-2", 60*time.Millisecond)
	if err != nil {
		t.Fatalf("registration failed: %v", err)
	}

	// Wait for expiration without sending heartbeat
	time.Sleep(150 * time.Millisecond)

	if sess.IsActive() {
		t.Fatalf("session should be inactive after timeout")
	}

	if atomic.LoadInt32(&timeoutNotified) == 0 {
		t.Fatalf("expected scrubber to trigger expiration callback")
	}

	// Subsequent heartbeat should fail with expired error
	_, err = mgr.Heartbeat("worker-beta", sess.SessionID())
	if err != ErrSessionExpired {
		t.Fatalf("expected ErrSessionExpired, got %v", err)
	}
}

func TestHeartbeatManager_ConcurrentHeartbeats(t *testing.T) {
	cfg := &config.Config{
		Heartbeat: config.HeartbeatConfig{
			Interval:        100 * time.Millisecond,
			Timeout:         10 * time.Second,
			CleanupInterval: 50 * time.Millisecond,
		},
	}

	reg := metrics.NewRegistry()
	mgr := NewManager(cfg, reg)
	mgr.Start()
	defer mgr.Stop()

	sess, _ := mgr.Register("worker-concurrent", "user-3", 10*time.Second)
	sid := sess.SessionID()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, err := mgr.Heartbeat("worker-concurrent", sid)
				if err != nil {
					t.Errorf("concurrent heartbeat failed: %v", err)
				}
			}
		}()
	}
	wg.Wait()
}

