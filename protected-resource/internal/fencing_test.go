package internal

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// TestRepeatedSameLeaseWrites confirms that multiple operations under the same active lease are allowed.
func TestRepeatedSameLeaseWrites(t *testing.T) {
	guard := NewFencingGuard()
	res := "bank-account-42"
	owner := "worker-node-1"
	token := uint64(100)

	// First write should succeed and establish epoch 100
	if err := guard.ValidateAndApply(res, token, owner); err != nil {
		t.Fatalf("first write failed: %v", err)
	}

	// Repeated writes under the exact same lease must succeed
	for i := 2; i <= 5; i++ {
		if err := guard.ValidateAndApply(res, token, owner); err != nil {
			t.Fatalf("repeated write #%d failed unexpectedly: %v", i, err)
		}
	}

	state, ok := guard.GetFencingState(res)
	if !ok || state.HighestToken != token || state.OwnerID != owner {
		t.Fatalf("unexpected state after repeated writes: %+v", state)
	}
}

// TestRejectLowerTokens confirms that any token lower than the highest accepted token is rejected.
func TestRejectLowerTokens(t *testing.T) {
	guard := NewFencingGuard()
	res := "ledger-01"

	if err := guard.ValidateAndApply(res, 200, "client-a"); err != nil {
		t.Fatalf("initial write failed: %v", err)
	}

	// Any token < 200 must be rejected as stale
	staleTokens := []uint64{1, 50, 100, 199}
	for _, st := range staleTokens {
		err := guard.ValidateAndApply(res, st, "client-a")
		if !errors.Is(err, ErrStaleFencingToken) {
			t.Errorf("expected ErrStaleFencingToken for token %d, got %v", st, err)
		}
	}
}

// TestAcceptNewerTokens confirms that a strictly higher token from a new leaseholder succeeds.
func TestAcceptNewerTokens(t *testing.T) {
	guard := NewFencingGuard()
	res := "partition-key"

	// Lease 1
	if err := guard.ValidateAndApply(res, 100, "owner-alpha"); err != nil {
		t.Fatalf("lease 1 write failed: %v", err)
	}

	// Lease 2 (higher token, new owner)
	if err := guard.ValidateAndApply(res, 105, "owner-beta"); err != nil {
		t.Fatalf("lease 2 write failed: %v", err)
	}

	state, ok := guard.GetFencingState(res)
	if !ok || state.HighestToken != 105 || state.OwnerID != "owner-beta" {
		t.Fatalf("state was not advanced correctly: %+v", state)
	}
}

// TestRejectDifferentOwnerOnSameToken confirms that an entity cannot hijack an existing token epoch.
func TestRejectDifferentOwnerOnSameToken(t *testing.T) {
	guard := NewFencingGuard()
	res := "mutex-resource"

	if err := guard.ValidateAndApply(res, 500, "legitimate-owner"); err != nil {
		t.Fatalf("initial write failed: %v", err)
	}

	// Different owner presenting same token 500 must fail
	err := guard.ValidateAndApply(res, 500, "imposter-owner")
	if !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("expected ErrOwnerMismatch for imposter, got %v", err)
	}
}

// TestStaleOwnerAfterNewerTokenAccepted rigorously tests that an old owner cannot write after a new lease is active.
func TestStaleOwnerAfterNewerTokenAccepted(t *testing.T) {
	guard := NewFencingGuard()
	res := "critical-file"

	// 1. Owner A acquires lease with token 100 and performs work
	if err := guard.ValidateAndApply(res, 100, "owner-A"); err != nil {
		t.Fatalf("owner A initial write failed: %v", err)
	}
	if err := guard.ValidateAndApply(res, 100, "owner-A"); err != nil {
		t.Fatalf("owner A second write failed: %v", err)
	}

	// 2. Owner A experiences GC pause / network delay. Lease expires in coordinator.
	// 3. Coordinator issues new lease with token 101 to Owner B.
	// 4. Owner B writes to protected resource with token 101
	if err := guard.ValidateAndApply(res, 101, "owner-B"); err != nil {
		t.Fatalf("owner B write failed: %v", err)
	}

	// 5. Stale Owner A wakes up from pause and attempts delayed write with token 100
	err := guard.ValidateAndApply(res, 100, "owner-A")
	if !errors.Is(err, ErrStaleFencingToken) {
		t.Fatalf("critical safety violation: stale owner A write with token 100 succeeded or wrong error: %v", err)
	}

	// 6. Stale Owner A attempts to spoof token 101
	err = guard.ValidateAndApply(res, 101, "owner-A")
	if !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("expected ErrOwnerMismatch for owner A spoofing token 101, got %v", err)
	}

	// 7. Legitimate Owner B can continue writing safely under token 101
	if err := guard.ValidateAndApply(res, 101, "owner-B"); err != nil {
		t.Fatalf("owner B repeated write failed: %v", err)
	}
}

// TestInputValidation verifies bad parameter handling.
func TestInputValidation(t *testing.T) {
	guard := NewFencingGuard()

	if err := guard.ValidateAndApply("", 100, "owner-1"); !errors.Is(err, ErrEmptyResource) {
		t.Errorf("expected ErrEmptyResource, got %v", err)
	}

	if err := guard.ValidateAndApply("res", 0, "owner-1"); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}

	if err := guard.ValidateAndApply("res", 100, "   "); !errors.Is(err, ErrEmptyOwner) {
		t.Errorf("expected ErrEmptyOwner, got %v", err)
	}
}

// TestMultipleIndependentResources verifies namespace isolation across resources.
func TestMultipleIndependentResources(t *testing.T) {
	guard := NewFencingGuard()

	if err := guard.ValidateAndApply("res-A", 50, "owner-1"); err != nil {
		t.Fatalf("res-A write failed: %v", err)
	}

	// res-B can start at a lower token 10 with a different owner
	if err := guard.ValidateAndApply("res-B", 10, "owner-2"); err != nil {
		t.Fatalf("res-B write failed: %v", err)
	}

	tokA, _ := guard.HighestToken("res-A")
	tokB, _ := guard.HighestToken("res-B")

	if tokA != 50 || tokB != 10 {
		t.Fatalf("expected 50 and 10, got %d and %d", tokA, tokB)
	}
}

// TestConcurrentRequests verifies race-safety under high concurrent load.
func TestConcurrentRequests(t *testing.T) {
	guard := NewFencingGuard()
	res := "concurrent-res"
	token := uint64(500)
	owner := "worker-pool"

	// Establish epoch 500
	if err := guard.ValidateAndApply(res, token, owner); err != nil {
		t.Fatalf("epoch establishment failed: %v", err)
	}

	// Run 100 concurrent writers for the same active lease
	var wg sync.WaitGroup
	errCh := make(chan error, 100)

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if err := guard.ValidateAndApply(res, token, owner); err != nil {
				errCh <- fmt.Errorf("goroutine %d failed: %w", idx, err)
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent same-lease write failed: %v", err)
	}

	// Concurrently race newer tokens
	var advanceWg sync.WaitGroup
	for i := 1; i <= 50; i++ {
		advanceWg.Add(1)
		go func(offset int) {
			defer advanceWg.Done()
			nextToken := token + uint64(offset)
			nextOwner := fmt.Sprintf("worker-%d", offset)
			_ = guard.ValidateAndApply(res, nextToken, nextOwner)
		}(i)
	}

	advanceWg.Wait()

	// Final state must have highest token between 501 and 550
	finalState, ok := guard.GetFencingState(res)
	if !ok || finalState.HighestToken < 501 || finalState.HighestToken > 550 {
		t.Fatalf("unexpected final state: %+v", finalState)
	}
}
