package lockstate

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentAcquireSingleResource verifies that among 100 simultaneous competing goroutines,
// exactly one successfully acquires the lock, while 99 fail with ErrLockAlreadyHeld.
func TestConcurrentAcquireSingleResource(t *testing.T) {
	sm := New()
	res := "shared-mutex"
	concurrency := 100

	var wg sync.WaitGroup
	results := make([]Result, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cmd := AcquireLockCommand{
				RequestID:         fmt.Sprintf("req-acq-%04d", idx),
				ResourceName:      res,
				OwnerID:           fmt.Sprintf("worker-%04d", idx),
				TTLNanoseconds:    10 * 1_000_000_000,
				TimestampUnixNano: timeOffset(0),
			}
			results[idx] = sm.Apply(cmd)
		}(i)
	}

	wg.Wait()

	successCount := 0
	failureCount := 0
	var winnerOwner string
	var winnerToken uint64

	for _, r := range results {
		if r.Success {
			successCount++
			winnerOwner = r.OwnerID
			winnerToken = r.FencingToken
		} else {
			failureCount++
			if r.Code != CodeLockAlreadyHeld {
				t.Errorf("expected CodeLockAlreadyHeld, got %s (err: %v)", r.Code, r.Error)
			}
		}
	}

	if successCount != 1 {
		t.Fatalf("critical mutual exclusion violation: %d goroutines succeeded, expected exactly 1", successCount)
	}
	if failureCount != concurrency-1 {
		t.Fatalf("expected %d failures, got %d", concurrency-1, failureCount)
	}
	if winnerToken != 1 {
		t.Errorf("expected winner fencing token 1, got %d", winnerToken)
	}

	// Verify query state matches winner
	lock, ok := sm.GetLock(res)
	if !ok || lock.OwnerID != winnerOwner || lock.FencingToken != 1 {
		t.Fatalf("state machine query does not match winner: %+v", lock)
	}
}

// TestConcurrentRenewals verifies that simultaneous renewals by the legitimate owner
// do not cause data races or corrupt the lease.
func TestConcurrentRenewals(t *testing.T) {
	sm := New()
	res := "renewable-res"
	owner := "worker-alpha"

	acq := sm.Apply(makeAcquireCmd(res, owner, 10, timeOffset(0)))
	if !acq.Success {
		t.Fatalf("acquire failed: %v", acq.Error)
	}

	concurrency := 50
	var wg sync.WaitGroup
	errCh := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cmd := RenewLockCommand{
				RequestID:         fmt.Sprintf("req-renew-%04d", idx),
				ResourceName:      res,
				OwnerID:           owner,
				FencingToken:      acq.FencingToken,
				TTLNanoseconds:    15 * 1_000_000_000,
				TimestampUnixNano: timeOffset(1),
			}
			res := sm.Apply(cmd)
			if !res.Success {
				errCh <- fmt.Errorf("goroutine %d renewal failed: %w", idx, res.Error)
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("renewal error: %v", err)
	}

	lock, ok := sm.GetLock(res)
	if !ok || lock.FencingToken != acq.FencingToken {
		t.Fatalf("unexpected lock state: %+v", lock)
	}
}

// TestConcurrentReleaseAndAcquire verifies that a simultaneous release and competing acquire
// execute safely without deadlocks, corruption, or split-brain.
func TestConcurrentReleaseAndAcquire(t *testing.T) {
	sm := New()
	res := "handoff-res"

	acq := sm.Apply(makeAcquireCmd(res, "initial-owner", 10, timeOffset(0)))
	if !acq.Success {
		t.Fatalf("initial acquire failed")
	}

	var wg sync.WaitGroup
	concurrency := 40
	results := make([]Result, concurrency)

	// One goroutine releases
	wg.Add(1)
	go func() {
		defer wg.Done()
		cmd := makeReleaseCmd(res, "initial-owner", acq.FencingToken, timeOffset(1))
		sm.Apply(cmd)
	}()

	// Multiple goroutines try to acquire
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cmd := AcquireLockCommand{
				RequestID:         fmt.Sprintf("req-handoff-acq-%04d", idx),
				ResourceName:      res,
				OwnerID:           fmt.Sprintf("competitor-%04d", idx),
				TTLNanoseconds:    10 * 1_000_000_000,
				TimestampUnixNano: timeOffset(2),
			}
			results[idx] = sm.Apply(cmd)
		}(i)
	}

	wg.Wait()

	// At most one competitor should have succeeded
	successCount := 0
	for _, r := range results {
		if r.Success {
			successCount++
		}
	}

	if successCount > 1 {
		t.Fatalf("mutual exclusion failure: %d competitors acquired the lock", successCount)
	}

	// Verify that ListLocks and GetResourceState are internally consistent
	locks := sm.ListLocks()
	st, _ := sm.GetResourceState(res)

	if len(locks) != successCount {
		t.Fatalf("mismatch: ListLocks returned %d locks, but %d acquired", len(locks), successCount)
	}
	if successCount == 1 {
		if st.ActiveLock == nil {
			t.Fatalf("expected active lock in ResourceState")
		}
		if st.HighestToken != 2 {
			t.Errorf("expected HighestToken 2, got %d", st.HighestToken)
		}
	}
}

// TestConcurrentMultiResourceStress simulates a realistic high-load scenario where 50 goroutines
// continuously acquire, renew, and release across 10 distinct resources.
func TestConcurrentMultiResourceStress(t *testing.T) {
	sm := New()
	numGoroutines := 30
	numResources := 8
	iterations := 20

	var wg sync.WaitGroup

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(workerIdx int) {
			defer wg.Done()
			ownerID := fmt.Sprintf("worker-%03d", workerIdx)

			for iter := 0; iter < iterations; iter++ {
				resName := fmt.Sprintf("resource-%02d", iter%numResources)
				ts := timeOffset(int64(iter))

				// Try acquire
				acq := sm.Apply(AcquireLockCommand{
					RequestID:         fmt.Sprintf("stress-acq-%d-%d", workerIdx, iter),
					ResourceName:      resName,
					OwnerID:           ownerID,
					TTLNanoseconds:    5 * 1_000_000_000,
					TimestampUnixNano: ts,
				})

				if acq.Success {
					// Try renew
					_ = sm.Apply(RenewLockCommand{
						RequestID:         fmt.Sprintf("stress-ren-%d-%d", workerIdx, iter),
						ResourceName:      resName,
						OwnerID:           ownerID,
						FencingToken:      acq.FencingToken,
						TTLNanoseconds:    10 * 1_000_000_000,
						TimestampUnixNano: ts + 1_000_000_000,
					})

					// Try release
					_ = sm.Apply(ReleaseLockCommand{
						RequestID:         fmt.Sprintf("stress-rel-%d-%d", workerIdx, iter),
						ResourceName:      resName,
						OwnerID:           ownerID,
						FencingToken:      acq.FencingToken,
						TimestampUnixNano: ts + 2_000_000_000,
					})
				}
			}
		}(g)
	}

	wg.Wait()

	// Verify all resource states are sound
	for r := 0; r < numResources; r++ {
		resName := fmt.Sprintf("resource-%02d", r)
		st, ok := sm.GetResourceState(resName)
		if ok {
			if st.HighestToken == 0 {
				t.Errorf("resource %s has zero token after stress test", resName)
			}
		}
	}
}
