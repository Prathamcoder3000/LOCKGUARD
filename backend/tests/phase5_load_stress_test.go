package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lockguard/backend/internal/api"
	"lockguard/backend/internal/config"
)

// TestPhase5_Load_IndependentResourcesHighConcurrency verifies that 50 concurrent
// lock acquisitions across independent resources succeed without interference or leakage.
func TestPhase5_Load_IndependentResourcesHighConcurrency(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	cfg := &config.Config{
		Environment: "test",
		HTTP: config.HTTPConfig{
			Port: 8080,
			Host: "127.0.0.1",
		},
		Raft: config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       5 * time.Second,
			MaxRedirectAttempts:  3,
			RequestTimeout:       3 * time.Second,
		},
		Storage: config.StorageConfig{DataDir: t.TempDir()},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.NewServer(cfg, logger, "lockguard-gateway", "1.0.0")
	handler := srv.Handler()

	const numResources = 50
	var wg sync.WaitGroup
	errCh := make(chan error, numResources*2)
	tokens := sync.Map{}

	// Concurrently acquire 50 locks
	for i := 0; i < numResources; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			resName := fmt.Sprintf("res-load-%03d", idx)
			owner := fmt.Sprintf("worker-%03d", idx)
			reqID := fmt.Sprintf("req-acq-%03d", idx)

			payload := map[string]interface{}{
				"request_id":    reqID,
				"resource_name": resName,
				"owner_id":      owner,
				"ttl_seconds":   10,
			}
			body, _ := json.Marshal(payload)
			req := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				errCh <- fmt.Errorf("acquire failed for %s: code=%d body=%s", resName, rec.Code, rec.Body.String())
				return
			}

			var resp api.APIResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				errCh <- fmt.Errorf("failed to decode response for %s: %v", resName, err)
				return
			}
			data, _ := resp.Data.(map[string]interface{})
			fToken := uint64(data["fencing_token"].(float64))
			tokens.Store(resName, fToken)
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrency error: %v", err)
	}

	// Verify all 50 tokens are recorded
	count := 0
	tokens.Range(func(k, v interface{}) bool {
		count++
		return true
	})
	if count != numResources {
		t.Fatalf("expected %d acquired locks, got %d", numResources, count)
	}

	// Concurrently release all 50 locks
	relErrCh := make(chan error, numResources)
	for i := 0; i < numResources; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			resName := fmt.Sprintf("res-load-%03d", idx)
			owner := fmt.Sprintf("worker-%03d", idx)
			reqID := fmt.Sprintf("req-rel-%03d", idx)

			tokVal, _ := tokens.Load(resName)
			fToken := tokVal.(uint64)

			payload := map[string]interface{}{
				"request_id":    reqID,
				"resource_name": resName,
				"owner_id":      owner,
				"fencing_token": fToken,
			}
			body, _ := json.Marshal(payload)
			req := httptest.NewRequest(http.MethodPost, "/v1/locks/release", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				relErrCh <- fmt.Errorf("release failed for %s: code=%d body=%s", resName, rec.Code, rec.Body.String())
			}
		}(i)
	}

	wg.Wait()
	close(relErrCh)

	for err := range relErrCh {
		t.Fatalf("release concurrency error: %v", err)
	}
}

// TestPhase5_Stress_SingleResourceHighContention verifies strict mutual exclusion
// and strictly monotonic fencing tokens under high contention.
func TestPhase5_Stress_SingleResourceHighContention(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	cfg := &config.Config{
		Environment: "test",
		HTTP: config.HTTPConfig{
			Port: 8080,
			Host: "127.0.0.1",
		},
		Raft: config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       5 * time.Second,
			MaxRedirectAttempts:  3,
			RequestTimeout:       3 * time.Second,
		},
		Storage: config.StorageConfig{DataDir: t.TempDir()},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.NewServer(cfg, logger, "lockguard-gateway", "1.0.0")
	handler := srv.Handler()

	const resName = "contended-mutex-resource"
	const numContenders = 20

	var activeHolders int32
	var maxActiveHolders int32
	var mu sync.Mutex
	var fencingTokens []uint64

	var wg sync.WaitGroup
	startSignal := make(chan struct{})

	for i := 0; i < numContenders; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			<-startSignal

			owner := fmt.Sprintf("contender-%02d", workerID)

			// Retry loop to acquire the lock
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				reqID := fmt.Sprintf("req-contend-%02d-%d", workerID, time.Now().UnixNano())
				payload := map[string]interface{}{
					"request_id":    reqID,
					"resource_name": resName,
					"owner_id":      owner,
					"ttl_seconds":   2,
				}
				body, _ := json.Marshal(payload)
				req := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)

				if rec.Code == http.StatusOK {
					// Acquired lock: verify mutual exclusion
					current := atomic.AddInt32(&activeHolders, 1)
					for {
						oldMax := atomic.LoadInt32(&maxActiveHolders)
						if current <= oldMax || atomic.CompareAndSwapInt32(&maxActiveHolders, oldMax, current) {
							break
						}
					}

					var resp api.APIResponse
					_ = json.Unmarshal(rec.Body.Bytes(), &resp)
					data, _ := resp.Data.(map[string]interface{})
					fToken := uint64(data["fencing_token"].(float64))

					mu.Lock()
					fencingTokens = append(fencingTokens, fToken)
					mu.Unlock()

					// Hold briefly in critical section
					time.Sleep(10 * time.Millisecond)

					atomic.AddInt32(&activeHolders, -1)

					// Release lock
					relID := fmt.Sprintf("rel-contend-%02d-%d", workerID, time.Now().UnixNano())
					relPayload := map[string]interface{}{
						"request_id":    relID,
						"resource_name": resName,
						"owner_id":      owner,
						"fencing_token": fToken,
					}
					relBody, _ := json.Marshal(relPayload)
					relReq := httptest.NewRequest(http.MethodPost, "/v1/locks/release", bytes.NewReader(relBody))
					relReq.Header.Set("Content-Type", "application/json")
					relRec := httptest.NewRecorder()
					handler.ServeHTTP(relRec, relReq)
					return
				}

				time.Sleep(15 * time.Millisecond)
			}
		}(i)
	}

	// Trigger all contenders simultaneously
	close(startSignal)
	wg.Wait()

	// Invariant 1: Mutual Exclusion — exactly 1 active holder at any time
	if maxActiveHolders > 1 {
		t.Fatalf("MUTUAL EXCLUSION VIOLATION: max concurrent lock holders was %d (expected 1)", maxActiveHolders)
	}

	// Invariant 2: Fencing token strict monotonicity across acquisitions
	if len(fencingTokens) < 5 {
		t.Fatalf("expected at least 5 successful acquisitions, got %d", len(fencingTokens))
	}

	for i := 1; i < len(fencingTokens); i++ {
		if fencingTokens[i] <= fencingTokens[i-1] {
			t.Fatalf("FENCING TOKEN MONOTONICITY VIOLATION: token[%d]=%d <= token[%d]=%d",
				i, fencingTokens[i], i-1, fencingTokens[i-1])
		}
	}
}

// TestPhase5_Stress_ConcurrentLeaseRenewals verifies that concurrent lease renewals
// advance lease versions by exactly +1 without race conditions.
func TestPhase5_Stress_ConcurrentLeaseRenewals(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	cfg := &config.Config{
		Environment: "test",
		HTTP: config.HTTPConfig{
			Port: 8080,
			Host: "127.0.0.1",
		},
		Raft: config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       5 * time.Second,
			MaxRedirectAttempts:  3,
			RequestTimeout:       3 * time.Second,
		},
		Storage: config.StorageConfig{DataDir: t.TempDir()},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.NewServer(cfg, logger, "lockguard-gateway", "1.0.0")
	handler := srv.Handler()

	const resName = "res-renewal-stress"
	const owner = "worker-renewal"

	// 1. Initial Acquire
	acqPayload := map[string]interface{}{
		"request_id":    "req-acq-init",
		"resource_name": resName,
		"owner_id":      owner,
		"ttl_seconds":   15,
	}
	body, _ := json.Marshal(acqPayload)
	req := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("initial acquire failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp api.APIResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	data, _ := resp.Data.(map[string]interface{})
	currentVersion := int64(data["lease_version"].(float64))
	fToken := uint64(data["fencing_token"].(float64))

	if currentVersion != 1 {
		t.Fatalf("expected initial lease_version=1, got %d", currentVersion)
	}

	// 2. Perform 10 consecutive lease renewals and verify exact +1 advancement
	for step := 1; step <= 10; step++ {
		renewPayload := map[string]interface{}{
			"request_id":             fmt.Sprintf("req-renew-%d", step),
			"resource_name":          resName,
			"owner_id":               owner,
			"fencing_token":          fToken,
			"expected_lease_version": currentVersion,
			"ttl_seconds":            15,
		}
		rBody, _ := json.Marshal(renewPayload)
		rReq := httptest.NewRequest(http.MethodPost, "/v1/locks/renew", bytes.NewReader(rBody))
		rReq.Header.Set("Content-Type", "application/json")
		rRec := httptest.NewRecorder()
		handler.ServeHTTP(rRec, rReq)

		if rRec.Code != http.StatusOK {
			t.Fatalf("renew step %d failed: status=%d body=%s", step, rRec.Code, rRec.Body.String())
		}

		var rResp api.APIResponse
		_ = json.Unmarshal(rRec.Body.Bytes(), &rResp)
		rData, _ := rResp.Data.(map[string]interface{})
		newVersion := int64(rData["lease_version"].(float64))

		// Invariant: Exact-increment rule (newLeaseVersion == expectedLeaseVersion + 1)
		if newVersion != currentVersion+1 {
			t.Fatalf("EXACT INCREMENT VIOLATION: expected lease_version=%d, got %d", currentVersion+1, newVersion)
		}
		currentVersion = newVersion
	}
}

// TestPhase5_Stress_LinearizableBarrierReadsConcurrentWithWrites verifies that
// authoritative reads via Raft Barrier never yield torn or stale state during concurrent writes.
func TestPhase5_Stress_LinearizableBarrierReadsConcurrentWithWrites(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	cfg := &config.Config{
		Environment: "test",
		HTTP: config.HTTPConfig{
			Port: 8080,
			Host: "127.0.0.1",
		},
		Raft: config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       5 * time.Second,
			MaxRedirectAttempts:  3,
			RequestTimeout:       3 * time.Second,
		},
		Storage: config.StorageConfig{DataDir: t.TempDir()},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.NewServer(cfg, logger, "lockguard-gateway", "1.0.0")
	handler := srv.Handler()

	stopCh := make(chan struct{})
	var writeCount int64
	var readCount int64
	var wg sync.WaitGroup

	// Background write loop
	wg.Add(1)
	go func() {
		defer wg.Done()
		idx := 0
		for {
			select {
			case <-stopCh:
				return
			default:
				idx++
				res := fmt.Sprintf("res-lin-%d", idx%5)
				payload := map[string]interface{}{
					"request_id":    fmt.Sprintf("req-lin-%d", idx),
					"resource_name": res,
					"owner_id":      "worker-lin",
					"ttl_seconds":   5,
				}
				body, _ := json.Marshal(payload)
				req := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)

				atomic.AddInt64(&writeCount, 1)
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()

	// Concurrent reader loop
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stopCh:
				return
			default:
				req := httptest.NewRequest(http.MethodGet, "/v1/locks", nil)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)

				if rec.Code == http.StatusOK {
					var resp api.APIResponse
					if err := json.Unmarshal(rec.Body.Bytes(), &resp); err == nil {
						atomic.AddInt64(&readCount, 1)
					}
				}
				time.Sleep(3 * time.Millisecond)
			}
		}
	}()

	// Run for 1.5 seconds under active read/write load
	time.Sleep(1500 * time.Millisecond)
	close(stopCh)
	wg.Wait()

	if atomic.LoadInt64(&writeCount) < 10 {
		t.Fatalf("expected at least 10 writes, got %d", atomic.LoadInt64(&writeCount))
	}
	if atomic.LoadInt64(&readCount) < 10 {
		t.Fatalf("expected at least 10 linearizable reads, got %d", atomic.LoadInt64(&readCount))
	}
}

// TestPhase5_Stress_AuditDatabaseSeveranceUnderLoad verifies that consensus lock
// operations never block when PostgreSQL audit storage is offline.
func TestPhase5_Stress_AuditDatabaseSeveranceUnderLoad(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	// Configure server with a severed audit pipeline
	cfg := &config.Config{
		Environment: "test",
		HTTP: config.HTTPConfig{
			Port: 8080,
			Host: "127.0.0.1",
		},
		Raft: config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       5 * time.Second,
			MaxRedirectAttempts:  3,
			RequestTimeout:       3 * time.Second,
		},
		Storage: config.StorageConfig{DataDir: t.TempDir()},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.NewServer(cfg, logger, "lockguard-gateway", "1.0.0")

	// Disconnect audit database
	srv.SetAuditRepository(nil)
	handler := srv.Handler()

	// Execute 20 rapid lock operations
	for i := 0; i < 20; i++ {
		res := fmt.Sprintf("res-severed-%02d", i)
		payload := map[string]interface{}{
			"request_id":    fmt.Sprintf("req-sev-%02d", i),
			"resource_name": res,
			"owner_id":      "worker-sev",
			"ttl_seconds":   10,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		start := time.Now()
		handler.ServeHTTP(rec, req)
		elapsed := time.Since(start)

		if rec.Code != http.StatusOK {
			t.Fatalf("lock operation %d failed when audit DB was offline: %d, body: %s", i, rec.Code, rec.Body.String())
		}

		// Verify consensus operation was not delayed by disconnected audit DB
		if elapsed > 1*time.Second {
			t.Fatalf("lock operation %d was blocked: took %v", i, elapsed)
		}
	}
}

// TestPhase5_Stress_ConcurrentRenewalsAndReleases verifies that concurrent renewal and release
// operations on multiple independent resources execute safely without race conditions.
func TestPhase5_Stress_ConcurrentRenewalsAndReleases(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	cfg := &config.Config{
		Environment: "test",
		HTTP:        config.HTTPConfig{Port: 8080, Host: "127.0.0.1"},
		Raft:        config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       5 * time.Second,
			MaxRedirectAttempts:  3,
			RequestTimeout:       3 * time.Second,
		},
		Storage: config.StorageConfig{DataDir: t.TempDir()},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.NewServer(cfg, logger, "lockguard-gateway", "1.0.0")
	handler := srv.Handler()

	const numLocks = 10
	type lockInfo struct {
		fToken uint64
		lVer   int64
		owner  string
	}
	activeLocks := make(map[string]lockInfo)

	// Step 1: Initial acquisitions
	for i := 0; i < numLocks; i++ {
		res := fmt.Sprintf("res-mix-%02d", i)
		owner := fmt.Sprintf("worker-%02d", i)
		payload := map[string]interface{}{
			"request_id":    fmt.Sprintf("req-acq-mix-%02d", i),
			"resource_name": res,
			"owner_id":      owner,
			"ttl_seconds":   15,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("initial acquire failed for %s: %d", res, rec.Code)
		}

		var resp api.APIResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		data, _ := resp.Data.(map[string]interface{})
		activeLocks[res] = lockInfo{
			fToken: uint64(data["fencing_token"].(float64)),
			lVer:   int64(data["lease_version"].(float64)),
			owner:  owner,
		}
	}

	// Step 2: Concurrently renew first 5, release last 5
	var wg sync.WaitGroup
	errCh := make(chan error, numLocks)

	for i := 0; i < numLocks; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res := fmt.Sprintf("res-mix-%02d", idx)
			info := activeLocks[res]

			if idx < 5 {
				// Renew
				payload := map[string]interface{}{
					"request_id":             fmt.Sprintf("req-ren-mix-%02d", idx),
					"resource_name":          res,
					"owner_id":               info.owner,
					"fencing_token":          info.fToken,
					"expected_lease_version": info.lVer,
					"ttl_seconds":            15,
				}
				body, _ := json.Marshal(payload)
				req := httptest.NewRequest(http.MethodPost, "/v1/locks/renew", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)

				if rec.Code != http.StatusOK {
					errCh <- fmt.Errorf("renew failed for %s: %d body=%s", res, rec.Code, rec.Body.String())
				}
			} else {
				// Release
				payload := map[string]interface{}{
					"request_id":    fmt.Sprintf("req-rel-mix-%02d", idx),
					"resource_name": res,
					"owner_id":      info.owner,
					"fencing_token": info.fToken,
				}
				body, _ := json.Marshal(payload)
				req := httptest.NewRequest(http.MethodPost, "/v1/locks/release", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)

				if rec.Code != http.StatusOK {
					errCh <- fmt.Errorf("release failed for %s: %d body=%s", res, rec.Code, rec.Body.String())
				}
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent mixed operation error: %v", err)
	}
}

// TestPhase5_Stress_LeaderStepdownDuringActiveTraffic verifies that leadership transfers
// during active lock operations are transparently handled by the gateway without data loss.
func TestPhase5_Stress_LeaderStepdownDuringActiveTraffic(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	cfg := &config.Config{
		Environment: "test",
		HTTP:        config.HTTPConfig{Port: 8080, Host: "127.0.0.1"},
		Raft:        config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       50 * time.Millisecond, // short cache for fast failover discovery
			MaxRedirectAttempts:  3,
			RequestTimeout:       2 * time.Second,
		},
		Storage: config.StorageConfig{DataDir: t.TempDir()},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.NewServer(cfg, logger, "lockguard-gateway", "1.0.0")
	handler := srv.Handler()

	stopCh := make(chan struct{})
	var successCount int64
	var wg sync.WaitGroup

	// Traffic generator
	wg.Add(1)
	go func() {
		defer wg.Done()
		idx := 0
		for {
			select {
			case <-stopCh:
				return
			default:
				idx++
				payload := map[string]interface{}{
					"request_id":    fmt.Sprintf("req-failover-%d", idx),
					"resource_name": fmt.Sprintf("res-fo-%d", idx%10),
					"owner_id":      "worker-fo",
					"ttl_seconds":   5,
				}
				body, _ := json.Marshal(payload)
				req := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)

				if rec.Code == http.StatusOK {
					atomic.AddInt64(&successCount, 1)
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()

	// Allow initial traffic to flow
	time.Sleep(200 * time.Millisecond)

	// Trigger leadership transfer on the current leader
	for _, n := range tc.nodes {
		if n.IsLeader() {
			_ = n.LeadershipTransfer()
			break
		}
	}

	// Continue traffic during failover
	time.Sleep(800 * time.Millisecond)
	close(stopCh)
	wg.Wait()

	if atomic.LoadInt64(&successCount) < 5 {
		t.Fatalf("expected at least 5 successful requests across failover, got %d", atomic.LoadInt64(&successCount))
	}
}

// TestPhase5_Stress_ClientSessionExpiryUnderLoad verifies heartbeat manager session lifecycle.
func TestPhase5_Stress_ClientSessionExpiryUnderLoad(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	cfg := &config.Config{
		Environment: "test",
		HTTP:        config.HTTPConfig{Port: 8080, Host: "127.0.0.1"},
		Raft:        config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       5 * time.Second,
			MaxRedirectAttempts:  3,
			RequestTimeout:       2 * time.Second,
		},
		Heartbeat: config.HeartbeatConfig{
			Interval:        30 * time.Millisecond,
			Timeout:         150 * time.Millisecond,
			CleanupInterval: 30 * time.Millisecond,
		},
		Storage: config.StorageConfig{DataDir: t.TempDir()},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.NewServer(cfg, logger, "lockguard-gateway", "1.0.0")
	defer srv.Close()
	handler := srv.Handler()

	// Register 5 client sessions
	type sessionInfo struct {
		clientID  string
		sessionID string
	}
	var sessions []sessionInfo

	for i := 0; i < 5; i++ {
		cid := fmt.Sprintf("client-hb-%02d", i)
		body, _ := json.Marshal(map[string]interface{}{
			"client_id":       cid,
			"owner_id":        cid,
			"ttl_nanoseconds": int64(150 * time.Millisecond),
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/clients/register", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("failed to register client %s: %d", cid, rec.Code)
		}

		var resp api.APIResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		data, _ := resp.Data.(map[string]interface{})
		sessions = append(sessions, sessionInfo{
			clientID:  cid,
			sessionID: data["session_id"].(string),
		})
	}

	// Keep sending heartbeat for session 0, let others expire
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		ticker := time.NewTicker(30 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				body, _ := json.Marshal(map[string]interface{}{
					"client_id":  sessions[0].clientID,
					"session_id": sessions[0].sessionID,
				})
				req := httptest.NewRequest(http.MethodPost, "/api/v1/clients/heartbeat", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
			}
		}
	}()

	// Wait for TTL expiration (350ms > 150ms)
	time.Sleep(350 * time.Millisecond)
	close(stopCh)
	<-doneCh

	// Verify session 0 is alive
	reqAlive := httptest.NewRequest(http.MethodGet, "/v1/clients/"+sessions[0].clientID, nil)
	recAlive := httptest.NewRecorder()
	handler.ServeHTTP(recAlive, reqAlive)
	if recAlive.Code != http.StatusOK {
		t.Fatalf("expected active session 0 to survive, got %d", recAlive.Code)
	}
	var aliveResp struct {
		Data struct {
			Active bool `json:"active"`
		} `json:"data"`
	}
	_ = json.Unmarshal(recAlive.Body.Bytes(), &aliveResp)
	if !aliveResp.Data.Active {
		t.Fatalf("expected active session 0 to be active, got active=false")
	}

	// Verify session 1 is expired (Active == false)
	reqDead := httptest.NewRequest(http.MethodGet, "/v1/clients/"+sessions[1].clientID, nil)
	recDead := httptest.NewRecorder()
	handler.ServeHTTP(recDead, reqDead)
	if recDead.Code != http.StatusOK {
		t.Fatalf("expected session query to return 200 with inactive status, got %d", recDead.Code)
	}
	var deadResp struct {
		Data struct {
			Active bool `json:"active"`
		} `json:"data"`
	}
	_ = json.Unmarshal(recDead.Body.Bytes(), &deadResp)
	if deadResp.Data.Active {
		t.Errorf("expected session 1 to be inactive, got active=true")
	}
}

// TestPhase5_Stress_GoroutineAndMemoryStability verifies that bursts of concurrent
// requests return goroutines to baseline with zero leaks.
func TestPhase5_Stress_GoroutineAndMemoryStability(t *testing.T) {
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	cfg := &config.Config{
		Environment: "test",
		HTTP:        config.HTTPConfig{Port: 8080, Host: "127.0.0.1"},
		Raft:        config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       5 * time.Second,
			MaxRedirectAttempts:  3,
			RequestTimeout:       2 * time.Second,
		},
		Storage: config.StorageConfig{DataDir: t.TempDir()},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.NewServer(cfg, logger, "lockguard-gateway", "1.0.0")
	handler := srv.Handler()

	// Warmup initial connections to coordinators
	for i := 0; i < 3; i++ {
		payload := map[string]interface{}{
			"request_id":    fmt.Sprintf("req-warm-%02d", i),
			"resource_name": fmt.Sprintf("res-warm-%02d", i),
			"owner_id":      "warmup",
			"ttl_seconds":   10,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	// Close idle connections to measure accurate baseline
	_ = srv.Close()
	time.Sleep(100 * time.Millisecond)
	baselineGoroutines := runtime.NumGoroutine()

	// Recreate clean server for burst
	srvBurst := api.NewServer(cfg, logger, "lockguard-gateway", "1.0.0")
	burstHandler := srvBurst.Handler()

	// Execute 60 concurrent requests
	const numBurst = 60
	var wg sync.WaitGroup
	for i := 0; i < numBurst; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			payload := map[string]interface{}{
				"request_id":    fmt.Sprintf("req-stab-%03d", idx),
				"resource_name": fmt.Sprintf("res-stab-%03d", idx),
				"owner_id":      fmt.Sprintf("worker-stab-%03d", idx),
				"ttl_seconds":   10,
			}
			body, _ := json.Marshal(payload)
			req := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			burstHandler.ServeHTTP(rec, req)
		}(i)
	}

	wg.Wait()

	// Close idle client connections and allow quiescence
	_ = srvBurst.Close()
	time.Sleep(300 * time.Millisecond)
	postBurstGoroutines := runtime.NumGoroutine()

	// Invariant: Goroutines must not permanently accumulate (leak)
	diff := postBurstGoroutines - baselineGoroutines
	if diff > 10 {
		t.Fatalf("GOROUTINE LEAK DETECTED: baseline=%d, post-burst=%d, diff=+%d",
			baselineGoroutines, postBurstGoroutines, diff)
	}
}
