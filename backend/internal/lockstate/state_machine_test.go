package lockstate

import (
	"errors"
	"math"
	"testing"
)

// ============================================================================
// A. ACQUISITION TESTS
// ============================================================================

func TestAcquireUnlockedResource(t *testing.T) {
	sm := New()
	cmd := makeAcquireCmd("res-1", "worker-A", 10, timeOffset(0))

	res := sm.Apply(cmd)
	if !res.Success {
		t.Fatalf("expected success, got error: %v", res.Error)
	}
	if res.FencingToken != 1 {
		t.Errorf("expected initial fencing token 1, got %d", res.FencingToken)
	}
	if res.LeaseVersion != 1 {
		t.Errorf("expected initial lease version 1, got %d", res.LeaseVersion)
	}
	if res.OwnerID != "worker-A" {
		t.Errorf("expected owner 'worker-A', got %s", res.OwnerID)
	}
	expectedExpiry := timeOffset(10)
	if res.ExpiresAtUnixNano != expectedExpiry {
		t.Errorf("expected expiry %d, got %d", expectedExpiry, res.ExpiresAtUnixNano)
	}

	// Verify query GetLock
	lock, ok := sm.GetLock("res-1")
	if !ok {
		t.Fatalf("expected active lock in GetLock, got false")
	}
	if lock.OwnerID != "worker-A" || lock.FencingToken != 1 || lock.LeaseVersion != 1 {
		t.Errorf("lock mismatch in GetLock: %+v", lock)
	}
}

func TestAcquireActiveResourceBySecondOwnerFails(t *testing.T) {
	sm := New()
	cmd1 := makeAcquireCmd("res-1", "worker-A", 10, timeOffset(0))
	res1 := sm.Apply(cmd1)
	if !res1.Success {
		t.Fatalf("first acquire failed: %v", res1.Error)
	}

	// Second owner attempts acquisition while lease is active
	cmd2 := makeAcquireCmd("res-1", "worker-B", 10, timeOffset(2))
	res2 := sm.Apply(cmd2)
	if res2.Success {
		t.Fatalf("expected second acquire to fail, but succeeded")
	}
	if !errors.Is(res2.Error, ErrLockAlreadyHeld) {
		t.Errorf("expected ErrLockAlreadyHeld, got %v", res2.Error)
	}
	if res2.Code != CodeLockAlreadyHeld {
		t.Errorf("expected code %s, got %s", CodeLockAlreadyHeld, res2.Code)
	}
}

func TestAcquireActiveResourceBySameOwnerWithDifferentRequestIDFails(t *testing.T) {
	sm := New()
	cmd1 := makeAcquireCmd("res-1", "worker-A", 10, timeOffset(0))
	res1 := sm.Apply(cmd1)
	if !res1.Success {
		t.Fatalf("first acquire failed: %v", res1.Error)
	}

	// Same owner attempts second acquisition with a new RequestID
	cmd2 := makeAcquireCmd("res-1", "worker-A", 10, timeOffset(2))
	res2 := sm.Apply(cmd2)
	if res2.Success {
		t.Fatalf("expected second acquire by same owner to fail, but succeeded")
	}
	if !errors.Is(res2.Error, ErrLockAlreadyHeld) {
		t.Errorf("expected ErrLockAlreadyHeld, got %v", res2.Error)
	}

	// Verify fencing token and lease version were NOT incremented
	rs, _ := sm.GetResourceState("res-1")
	if rs.HighestToken != 1 {
		t.Errorf("fencing token must remain 1, got %d", rs.HighestToken)
	}
	if rs.ActiveLock.LeaseVersion != 1 {
		t.Errorf("lease version must remain 1, got %d", rs.ActiveLock.LeaseVersion)
	}
}

func TestAcquireInputValidation(t *testing.T) {
	sm := New()

	// Empty resource
	c1 := AcquireLockCommand{RequestID: "r1", ResourceName: "", OwnerID: "w", TTLNanoseconds: 1000, TimestampUnixNano: 100}
	if err := sm.Apply(c1).Error; !errors.Is(err, ErrInvalidResource) {
		t.Errorf("expected ErrInvalidResource, got %v", err)
	}

	// Empty owner
	c2 := AcquireLockCommand{RequestID: "r2", ResourceName: "res", OwnerID: "  ", TTLNanoseconds: 1000, TimestampUnixNano: 100}
	if err := sm.Apply(c2).Error; !errors.Is(err, ErrInvalidOwner) {
		t.Errorf("expected ErrInvalidOwner, got %v", err)
	}

	// Zero or negative TTL
	c3 := AcquireLockCommand{RequestID: "r3", ResourceName: "res", OwnerID: "w", TTLNanoseconds: 0, TimestampUnixNano: 100}
	if err := sm.Apply(c3).Error; !errors.Is(err, ErrInvalidTTL) {
		t.Errorf("expected ErrInvalidTTL for 0, got %v", err)
	}
	c4 := AcquireLockCommand{RequestID: "r4", ResourceName: "res", OwnerID: "w", TTLNanoseconds: -10, TimestampUnixNano: 100}
	if err := sm.Apply(c4).Error; !errors.Is(err, ErrInvalidTTL) {
		t.Errorf("expected ErrInvalidTTL for negative, got %v", err)
	}

	// Invalid timestamp
	c5 := AcquireLockCommand{RequestID: "r5", ResourceName: "res", OwnerID: "w", TTLNanoseconds: 1000, TimestampUnixNano: 0}
	if err := sm.Apply(c5).Error; !errors.Is(err, ErrInvalidTimestamp) {
		t.Errorf("expected ErrInvalidTimestamp, got %v", err)
	}
}

func TestAcquireAfterLeaseExpirationAdvancesTokenAndGrants(t *testing.T) {
	sm := New()

	// Worker A acquires for 10s at t=0 (expires at t=10, token 1, version 1)
	cmd1 := makeAcquireCmd("res-1", "worker-A", 10, timeOffset(0))
	res1 := sm.Apply(cmd1)
	if !res1.Success || res1.FencingToken != 1 || res1.LeaseVersion != 1 {
		t.Fatalf("first acquire failed: %+v", res1)
	}

	// Worker B acquires at t=12 (after expiration of lease A)
	cmd2 := makeAcquireCmd("res-1", "worker-B", 15, timeOffset(12))
	res2 := sm.Apply(cmd2)
	if !res2.Success {
		t.Fatalf("acquire after expiration failed: %v", res2.Error)
	}
	if res2.FencingToken != 2 {
		t.Errorf("expected fencing token 2 for new epoch, got %d", res2.FencingToken)
	}
	if res2.LeaseVersion != 1 {
		t.Errorf("new ownership epoch must start at LeaseVersion 1, got %d", res2.LeaseVersion)
	}
	if res2.OwnerID != "worker-B" {
		t.Errorf("expected owner 'worker-B', got %s", res2.OwnerID)
	}
}

// ============================================================================
// B. RENEWAL TESTS (TASK 1 & TASK 3 ENHANCED)
// ============================================================================

func TestRenewActiveLock(t *testing.T) {
	sm := New()
	cmd1 := makeAcquireCmd("res-1", "worker-A", 10, timeOffset(0))
	res1 := sm.Apply(cmd1)
	if !res1.Success {
		t.Fatalf("acquire failed: %v", res1.Error)
	}
	if res1.LeaseVersion != 1 {
		t.Fatalf("expected initial version 1, got %d", res1.LeaseVersion)
	}

	// 1. First renewal at t=5 increments version to 2 and preserves fencing token
	cmd2 := makeRenewCmd("res-1", "worker-A", res1.FencingToken, 10, timeOffset(5))
	res2 := sm.Apply(cmd2)
	if !res2.Success {
		t.Fatalf("renewal failed: %v", res2.Error)
	}
	if res2.FencingToken != res1.FencingToken {
		t.Errorf("renewal must preserve token: expected %d, got %d", res1.FencingToken, res2.FencingToken)
	}
	if res2.LeaseVersion != 2 {
		t.Errorf("first renewal must increment version to 2, got %d", res2.LeaseVersion)
	}
	if res2.ExpiresAtUnixNano != timeOffset(15) {
		t.Errorf("expected expiry %d, got %d", timeOffset(15), res2.ExpiresAtUnixNano)
	}

	// 2. Second renewal at t=10 increments version to 3 and preserves fencing token
	cmd3 := makeRenewCmd("res-1", "worker-A", res1.FencingToken, 10, timeOffset(10))
	res3 := sm.Apply(cmd3)
	if !res3.Success {
		t.Fatalf("second renewal failed: %v", res3.Error)
	}
	if res3.FencingToken != res1.FencingToken {
		t.Errorf("second renewal must preserve token: expected %d, got %d", res1.FencingToken, res3.FencingToken)
	}
	if res3.LeaseVersion != 3 {
		t.Errorf("second renewal must increment version to 3, got %d", res3.LeaseVersion)
	}
	if res3.ExpiresAtUnixNano != timeOffset(20) {
		t.Errorf("expected expiry %d, got %d", timeOffset(20), res3.ExpiresAtUnixNano)
	}

	// Check GetLock reflects extended lease and updated version
	lock, ok := sm.GetLock("res-1")
	if !ok || lock.ExpiresAtUnixNano != timeOffset(20) || lock.LeaseVersion != 3 {
		t.Errorf("GetLock did not reflect renewal: %+v", lock)
	}
}

func TestRenewRejections(t *testing.T) {
	sm := New()
	cmd1 := makeAcquireCmd("res-1", "worker-A", 10, timeOffset(0))
	res1 := sm.Apply(cmd1)
	if !res1.Success {
		t.Fatalf("acquire failed: %v", res1.Error)
	}

	// 1. Reject renewal by another owner
	renewDiffOwner := makeRenewCmd("res-1", "worker-B", res1.FencingToken, 10, timeOffset(2))
	if err := sm.Apply(renewDiffOwner).Error; !errors.Is(err, ErrNotOwner) {
		t.Errorf("expected ErrNotOwner, got %v", err)
	}

	// 2. Reject renewal with stale/incorrect token
	renewBadToken := makeRenewCmd("res-1", "worker-A", 999, 10, timeOffset(2))
	if err := sm.Apply(renewBadToken).Error; !errors.Is(err, ErrFencingTokenMismatch) {
		t.Errorf("expected ErrFencingTokenMismatch, got %v", err)
	}

	// 3. Reject renewal on nonexistent resource
	renewNonexistent := makeRenewCmd("res-does-not-exist", "worker-A", 1, 10, timeOffset(2))
	if err := sm.Apply(renewNonexistent).Error; !errors.Is(err, ErrLockNotFound) {
		t.Errorf("expected ErrLockNotFound, got %v", err)
	}

	// 4. Reject renewal after lease expiry
	renewExpired := makeRenewCmd("res-1", "worker-A", res1.FencingToken, 10, timeOffset(12))
	if err := sm.Apply(renewExpired).Error; !errors.Is(err, ErrLeaseExpired) {
		t.Errorf("expected ErrLeaseExpired, got %v", err)
	}

	// Verify failed renewals did not increment LeaseVersion
	rs, _ := sm.GetResourceState("res-1")
	if rs.ActiveLock == nil || rs.ActiveLock.LeaseVersion != 1 {
		t.Errorf("failed renewals must not increment LeaseVersion: expected 1, got %v", rs.ActiveLock)
	}
}

func TestRenew_LeaseVersionOverflow(t *testing.T) {
	sm := New()
	resAcq := sm.Apply(makeAcquireCmd("res-ver-overflow", "worker-A", 10, timeOffset(0)))
	if !resAcq.Success {
		t.Fatalf("acquire failed")
	}

	// Seed lease version directly to MaxUint64
	sm.resources["res-ver-overflow"].ActiveLock.LeaseVersion = math.MaxUint64

	// Renewal attempt must fail safely with ErrVersionExhausted
	renewCmd := makeRenewCmd("res-ver-overflow", "worker-A", resAcq.FencingToken, 10, timeOffset(2))
	res := sm.Apply(renewCmd)
	if res.Success {
		t.Fatalf("expected version overflow failure, got success")
	}
	if !errors.Is(res.Error, ErrVersionExhausted) {
		t.Errorf("expected ErrVersionExhausted, got %v", res.Error)
	}

	// Verify active lock version remains unchanged
	lock, _ := sm.GetLock("res-ver-overflow")
	if lock.LeaseVersion != math.MaxUint64 {
		t.Errorf("lock version altered after failed overflow: %d", lock.LeaseVersion)
	}
}

// ============================================================================
// C. RELEASE TESTS
// ============================================================================

func TestReleaseActiveLock(t *testing.T) {
	sm := New()
	cmd1 := makeAcquireCmd("res-1", "worker-A", 10, timeOffset(0))
	res1 := sm.Apply(cmd1)
	if !res1.Success {
		t.Fatalf("acquire failed: %v", res1.Error)
	}

	// Release active lock at t=2
	cmdRel := makeReleaseCmd("res-1", "worker-A", res1.FencingToken, timeOffset(2))
	resRel := sm.Apply(cmdRel)
	if !resRel.Success {
		t.Fatalf("release failed: %v", resRel.Error)
	}

	// Resource should be unlocked
	if _, ok := sm.GetLock("res-1"); ok {
		t.Errorf("expected resource to be unlocked after release, but GetLock returned true")
	}

	// Fencing counter must be preserved at 1
	rs, _ := sm.GetResourceState("res-1")
	if rs.HighestToken != 1 {
		t.Errorf("expected HighestToken to remain 1, got %d", rs.HighestToken)
	}

	// Subsequent acquisition by worker-B must receive token 2 and restart LeaseVersion at 1
	cmd3 := makeAcquireCmd("res-1", "worker-B", 10, timeOffset(5))
	res3 := sm.Apply(cmd3)
	if !res3.Success || res3.FencingToken != 2 || res3.LeaseVersion != 1 {
		t.Fatalf("subsequent acquire failed: %+v", res3)
	}
}

func TestReleaseRejections(t *testing.T) {
	sm := New()
	cmd1 := makeAcquireCmd("res-1", "worker-A", 10, timeOffset(0))
	res1 := sm.Apply(cmd1)
	if !res1.Success {
		t.Fatalf("acquire failed: %v", res1.Error)
	}

	// Reject release by different owner
	relDiffOwner := makeReleaseCmd("res-1", "worker-B", res1.FencingToken, timeOffset(2))
	if err := sm.Apply(relDiffOwner).Error; !errors.Is(err, ErrNotOwner) {
		t.Errorf("expected ErrNotOwner, got %v", err)
	}

	// Reject release with incorrect token
	relBadToken := makeReleaseCmd("res-1", "worker-A", 999, timeOffset(2))
	if err := sm.Apply(relBadToken).Error; !errors.Is(err, ErrFencingTokenMismatch) {
		t.Errorf("expected ErrFencingTokenMismatch, got %v", err)
	}

	// Reject release after lease expiration
	relExpired := makeReleaseCmd("res-1", "worker-A", res1.FencingToken, timeOffset(12))
	if err := sm.Apply(relExpired).Error; !errors.Is(err, ErrLeaseExpired) {
		t.Errorf("expected ErrLeaseExpired on expired release, got %v", err)
	}
}

// ============================================================================
// D. EXPIRATION TESTS (LEASE VERSION REGRESSIONS)
// ============================================================================

func TestExpireLock(t *testing.T) {
	sm := New()
	cmd1 := makeAcquireCmd("res-1", "worker-A", 10, timeOffset(0)) // Expiry at t=10, token 1, version 1
	res1 := sm.Apply(cmd1)
	if !res1.Success {
		t.Fatalf("acquire failed: %v", res1.Error)
	}

	// 1. Reject premature expiration at t=8
	expEarly := makeExpireCmd("res-1", "worker-A", res1.FencingToken, 1, timeOffset(10), timeOffset(8))
	if err := sm.Apply(expEarly).Error; !errors.Is(err, ErrInvalidExpiration) {
		t.Errorf("expected ErrInvalidExpiration for early expire, got %v", err)
	}

	// 2. Reject expiration with wrong owner
	expWrongOwner := makeExpireCmd("res-1", "worker-B", res1.FencingToken, 1, timeOffset(10), timeOffset(12))
	if err := sm.Apply(expWrongOwner).Error; !errors.Is(err, ErrNotOwner) {
		t.Errorf("expected ErrNotOwner, got %v", err)
	}

	// 3. Reject expiration with wrong token
	expWrongToken := makeExpireCmd("res-1", "worker-A", 999, 1, timeOffset(10), timeOffset(12))
	if err := sm.Apply(expWrongToken).Error; !errors.Is(err, ErrFencingTokenMismatch) {
		t.Errorf("expected ErrFencingTokenMismatch, got %v", err)
	}

	// 4. Reject expiration with wrong lease version
	expWrongVersion := makeExpireCmd("res-1", "worker-A", res1.FencingToken, 99, timeOffset(10), timeOffset(12))
	if err := sm.Apply(expWrongVersion).Error; !errors.Is(err, ErrLeaseVersionMismatch) {
		t.Errorf("expected ErrLeaseVersionMismatch for wrong version, got %v", err)
	}

	// 5. Reject expiration with wrong expected expiry timestamp
	expWrongExpiry := makeExpireCmd("res-1", "worker-A", res1.FencingToken, 1, timeOffset(99), timeOffset(12))
	if err := sm.Apply(expWrongExpiry).Error; !errors.Is(err, ErrLeaseVersionMismatch) {
		t.Errorf("expected ErrLeaseVersionMismatch for wrong expiry, got %v", err)
	}

	// 6. Valid expiration at t=10
	expValid := makeExpireCmd("res-1", "worker-A", res1.FencingToken, 1, timeOffset(10), timeOffset(10))
	resExp := sm.Apply(expValid)
	if !resExp.Success {
		t.Fatalf("valid expiration failed: %v", resExp.Error)
	}

	// Verify resource is now unlocked but token counter preserved
	if _, ok := sm.GetLock("res-1"); ok {
		t.Errorf("lock should not be active after expire")
	}
	rs, _ := sm.GetResourceState("res-1")
	if rs.HighestToken != 1 {
		t.Errorf("expected HighestToken 1, got %d", rs.HighestToken)
	}

	// Next acquire receives token 2 and LeaseVersion 1
	cmd2 := makeAcquireCmd("res-1", "worker-C", 10, timeOffset(15))
	res2 := sm.Apply(cmd2)
	if !res2.Success || res2.FencingToken != 2 || res2.LeaseVersion != 1 {
		t.Fatalf("subsequent acquire failed: %+v", res2)
	}
}

func TestExpire_StaleCommandRejectedAfterRenewal(t *testing.T) {
	sm := New()

	// 1. Worker A acquires at t=0 for 10s (expiry at t=10, token 1, version 1)
	res1 := sm.Apply(makeAcquireCmd("res-1", "worker-A", 10, timeOffset(0)))
	if !res1.Success {
		t.Fatalf("acquire failed")
	}

	// 2. Worker A renews at t=5 for 10s (new expiry at t=15, fencing token 1 preserved, version = 2)
	resRenew := sm.Apply(makeRenewCmd("res-1", "worker-A", res1.FencingToken, 10, timeOffset(5)))
	if !resRenew.Success || resRenew.LeaseVersion != 2 {
		t.Fatalf("renew failed: %+v", resRenew)
	}

	// 3. Stale expiration command targeted at original lease version (version 1, expiry t=10), arriving at t=12
	staleExp := makeExpireCmd("res-1", "worker-A", res1.FencingToken, 1, timeOffset(10), timeOffset(12))
	resStale := sm.Apply(staleExp)
	if resStale.Success {
		t.Fatalf("expected stale expire command to fail, but succeeded")
	}
	if !errors.Is(resStale.Error, ErrLeaseVersionMismatch) {
		t.Errorf("expected ErrLeaseVersionMismatch, got %v", resStale.Error)
	}

	// Verify lock is STILL held by worker-A with version 2 and expiry t=15
	lock, ok := sm.GetLock("res-1")
	if !ok {
		t.Fatalf("lock was incorrectly cleared by stale expiration")
	}
	if lock.OwnerID != "worker-A" || lock.ExpiresAtUnixNano != timeOffset(15) || lock.LeaseVersion != 2 {
		t.Errorf("lock corrupted: %+v", lock)
	}
}

// CRITICAL REGRESSION TEST:
// Worker A renews with exact same expiry timestamp, but LeaseVersion increments from 1 to 2.
// An old expiration command carrying version 1 and the same expiry timestamp MUST be rejected.
func TestExpire_OldExpirationRejectedWhenRenewalRetainsExactSameExpiryTimestamp(t *testing.T) {
	sm := New()

	// 1. Worker A acquires lock at t=0 (10:00) with a 10-second TTL -> expires at t=10 (10:10).
	// Token = 1, LeaseVersion = 1.
	resAcq := sm.Apply(makeAcquireCmd("res-critical", "worker-A", 10, timeOffset(0)))
	if !resAcq.Success {
		t.Fatalf("acquire failed: %v", resAcq.Error)
	}
	if resAcq.LeaseVersion != 1 || resAcq.ExpiresAtUnixNano != timeOffset(10) {
		t.Fatalf("acquire setup mismatch: %+v", resAcq)
	}

	// 2. Worker A renews at t=5 (10:05) with a 5-second TTL.
	// New expiry = 5 + 5 = 10 (10:10) -> EXACT SAME EXPIRATION TIMESTAMP!
	// But LeaseVersion increments to 2. FencingToken remains 1.
	resRenew := sm.Apply(makeRenewCmd("res-critical", "worker-A", resAcq.FencingToken, 5, timeOffset(5)))
	if !resRenew.Success {
		t.Fatalf("renewal failed: %v", resRenew.Error)
	}
	if resRenew.ExpiresAtUnixNano != timeOffset(10) {
		t.Fatalf("expected renewed expiry to equal timeOffset(10), got %d", resRenew.ExpiresAtUnixNano)
	}
	if resRenew.LeaseVersion != 2 {
		t.Fatalf("expected renewed LeaseVersion to be 2, got %d", resRenew.LeaseVersion)
	}

	// 3. Stale expiration command arrives at t=11 (10:11, after expiry).
	// Command specifies: token=1, expectedVersion=1, expectedExpiry=10:10.
	// Notice that expectedExpiry (10:10) matches the current lock's expiry timestamp!
	// Under timestamp-only checking, this command would have improperly succeeded.
	// With LeaseVersion check, it MUST BE REJECTED because version 1 != active version 2!
	staleExp := makeExpireCmd("res-critical", "worker-A", resAcq.FencingToken, 1, timeOffset(10), timeOffset(11))
	resStale := sm.Apply(staleExp)
	if resStale.Success {
		t.Fatalf("CRITICAL FAILURE: stale expiration command for version 1 succeeded against version 2!")
	}
	if !errors.Is(resStale.Error, ErrLeaseVersionMismatch) {
		t.Errorf("expected ErrLeaseVersionMismatch, got %v", resStale.Error)
	}

	// 4. Verify current lease remains intact (still version 2, not cleared)
	rs, exists := sm.GetResourceState("res-critical")
	if !exists || rs.ActiveLock == nil {
		t.Fatalf("current lease was incorrectly cleared by stale expiration")
	}
	if rs.ActiveLock.LeaseVersion != 2 {
		t.Errorf("active lease version corrupted: expected 2, got %d", rs.ActiveLock.LeaseVersion)
	}
}

func TestExpire_ValidAfterRenewalExpires(t *testing.T) {
	sm := New()

	// 1. Worker A acquires at t=0 for 10s (expiry t=10, version 1)
	res1 := sm.Apply(makeAcquireCmd("res-1", "worker-A", 10, timeOffset(0)))
	if !res1.Success {
		t.Fatalf("acquire failed")
	}

	// 2. Worker A renews at t=5 for 10s (new expiry t=15, version 2)
	resRenew := sm.Apply(makeRenewCmd("res-1", "worker-A", res1.FencingToken, 10, timeOffset(5)))
	if !resRenew.Success || resRenew.LeaseVersion != 2 {
		t.Fatalf("renew failed")
	}

	// 3. Valid expiration command targeting version 2 with ExpectedExpiresAtUnixNano = t=15
	// arriving at t=15
	validExp := makeExpireCmd("res-1", "worker-A", res1.FencingToken, 2, timeOffset(15), timeOffset(15))
	resValid := sm.Apply(validExp)
	if !resValid.Success {
		t.Fatalf("valid expiration failed: %v", resValid.Error)
	}

	// Resource must now be cleanly unlocked
	if _, ok := sm.GetLock("res-1"); ok {
		t.Errorf("lock should be unlocked after valid expiration")
	}
}

func TestExpire_DifferentLeaseGenerationRejected(t *testing.T) {
	sm := New()
	res1 := sm.Apply(makeAcquireCmd("res-1", "worker-A", 10, timeOffset(0)))
	if !res1.Success {
		t.Fatalf("acquire failed")
	}

	// Arbitrary fictitious expiration version and timestamp
	badGenCmd := makeExpireCmd("res-1", "worker-A", res1.FencingToken, 999, timeOffset(999), timeOffset(1000))
	res := sm.Apply(badGenCmd)
	if !errors.Is(res.Error, ErrLeaseVersionMismatch) {
		t.Errorf("expected ErrLeaseVersionMismatch, got %v", res.Error)
	}
}

func TestExpire_RejectedLeavesStateUnchanged(t *testing.T) {
	sm := New()
	// Acquire for 30s so lease is active up to t=30
	cmdAcq := makeAcquireCmd("res-1", "worker-A", 30, timeOffset(0))
	resAcq := sm.Apply(cmdAcq)
	if !resAcq.Success {
		t.Fatalf("acquire failed")
	}

	checkStateUnchanged := func(step string) {
		lock, ok := sm.GetLock("res-1")
		if !ok {
			t.Fatalf("[%s] lock missing", step)
		}
		if lock.OwnerID != "worker-A" || lock.FencingToken != 1 || lock.LeaseVersion != 1 || lock.ExpiresAtUnixNano != timeOffset(30) {
			t.Fatalf("[%s] lock state mutated: %+v", step, lock)
		}
		rs, ok := sm.GetResourceState("res-1")
		if !ok || rs.HighestToken != 1 {
			t.Fatalf("[%s] resource state mutated: %+v", step, rs)
		}
	}

	// 1. Wrong owner at t=5
	sm.Apply(makeExpireCmd("res-1", "worker-B", 1, 1, timeOffset(30), timeOffset(5)))
	checkStateUnchanged("wrong-owner")

	// 2. Wrong token at t=6
	sm.Apply(makeExpireCmd("res-1", "worker-A", 99, 1, timeOffset(30), timeOffset(6)))
	checkStateUnchanged("wrong-token")

	// 3. Wrong version at t=7
	sm.Apply(makeExpireCmd("res-1", "worker-A", 1, 99, timeOffset(30), timeOffset(7)))
	checkStateUnchanged("wrong-version")

	// 4. Wrong expected expiry at t=8
	sm.Apply(makeExpireCmd("res-1", "worker-A", 1, 1, timeOffset(5), timeOffset(8)))
	checkStateUnchanged("wrong-expiry")

	// 5. Premature timestamp (t=9 < t=30)
	sm.Apply(makeExpireCmd("res-1", "worker-A", 1, 1, timeOffset(30), timeOffset(9)))
	checkStateUnchanged("premature")
}

func TestExpire_Idempotency(t *testing.T) {
	sm := New()
	resAcq := sm.Apply(makeAcquireCmd("res-1", "worker-A", 10, timeOffset(0)))
	if !resAcq.Success {
		t.Fatalf("acquire failed")
	}

	expCmd := makeExpireCmd("res-1", "worker-A", 1, 1, timeOffset(10), timeOffset(12))

	res1 := sm.Apply(expCmd)
	if !res1.Success {
		t.Fatalf("first expire failed: %v", res1.Error)
	}

	// Replay exact same expire command
	res2 := sm.Apply(expCmd)
	if !res2.Success {
		t.Fatalf("replayed expire failed: %v", res2.Error)
	}
	if res2.FencingToken != res1.FencingToken || res2.OwnerID != res1.OwnerID || res2.LeaseVersion != res1.LeaseVersion {
		t.Errorf("idempotent replay mismatch: %+v vs %+v", res1, res2)
	}

	// Replay with same RequestID but conflicting expected version
	conflictingCmd := ExpireLockCommand{
		RequestID:                 expCmd.RequestID,
		ResourceName:              "res-1",
		ExpectedOwnerID:           "worker-A",
		ExpectedFencingToken:      1,
		ExpectedLeaseVersion:      999, // Conflicting!
		ExpectedExpiresAtUnixNano: timeOffset(10),
		TimestampUnixNano:         timeOffset(12),
	}
	resConflict := sm.Apply(conflictingCmd)
	if !errors.Is(resConflict.Error, ErrIdempotencyConflict) {
		t.Errorf("expected ErrIdempotencyConflict, got %v", resConflict.Error)
	}
}

// ============================================================================
// E. IDEMPOTENCY TESTS
// ============================================================================

func TestIdempotencyAcquire(t *testing.T) {
	sm := New()
	cmd := makeAcquireCmd("res-1", "worker-A", 10, timeOffset(0))

	res1 := sm.Apply(cmd)
	if !res1.Success || res1.FencingToken != 1 || res1.LeaseVersion != 1 {
		t.Fatalf("first apply failed: %+v", res1)
	}

	// Repeat exact same command with same RequestID
	res2 := sm.Apply(cmd)
	if !res2.Success {
		t.Fatalf("second apply failed: %+v", res2)
	}
	if res2.FencingToken != 1 || res2.LeaseVersion != 1 {
		t.Errorf("expected cached fencing token 1 and version 1, got token=%d ver=%d", res2.FencingToken, res2.LeaseVersion)
	}
	if res2.OwnerID != "worker-A" {
		t.Errorf("expected cached owner 'worker-A', got %s", res2.OwnerID)
	}

	// Attempt same RequestID with differing parameter (different TTL)
	conflictingCmd := AcquireLockCommand{
		RequestID:         cmd.RequestID,
		ResourceName:      "res-1",
		OwnerID:           "worker-A",
		TTLNanoseconds:    20 * 1e9, // Changed from 10s to 20s
		TimestampUnixNano: cmd.TimestampUnixNano,
	}
	resConflict := sm.Apply(conflictingCmd)
	if resConflict.Success {
		t.Fatalf("expected conflict failure, got success")
	}
	if !errors.Is(resConflict.Error, ErrIdempotencyConflict) {
		t.Errorf("expected ErrIdempotencyConflict, got %v", resConflict.Error)
	}
	if resConflict.Code != CodeIdempotencyConflict {
		t.Errorf("expected code %s, got %s", CodeIdempotencyConflict, resConflict.Code)
	}
}

func TestIdempotencyRenew(t *testing.T) {
	sm := New()
	resAcq := sm.Apply(makeAcquireCmd("res-1", "worker-A", 10, timeOffset(0)))
	if !resAcq.Success {
		t.Fatalf("acquire failed")
	}

	renewCmd := makeRenewCmd("res-1", "worker-A", resAcq.FencingToken, 10, timeOffset(2))
	res1 := sm.Apply(renewCmd)
	if !res1.Success || res1.LeaseVersion != 2 {
		t.Fatalf("renew failed: %+v", res1)
	}

	// Duplicate renew replay: must NOT increment LeaseVersion twice
	res2 := sm.Apply(renewCmd)
	if !res2.Success {
		t.Fatalf("duplicate renew replay failed")
	}
	if res2.LeaseVersion != 2 {
		t.Errorf("duplicate renew must return cached version 2, got %d", res2.LeaseVersion)
	}
	if res2.ExpiresAtUnixNano != res1.ExpiresAtUnixNano {
		t.Errorf("expected identical expiry on renew replay, got %d vs %d", res1.ExpiresAtUnixNano, res2.ExpiresAtUnixNano)
	}

	// Verify state in state machine remains version 2
	lock, _ := sm.GetLock("res-1")
	if lock.LeaseVersion != 2 {
		t.Errorf("state machine version corrupted by replay: expected 2, got %d", lock.LeaseVersion)
	}

	// Conflicting renew replay
	conflictingRenew := RenewLockCommand{
		RequestID:         renewCmd.RequestID,
		ResourceName:      "res-1",
		OwnerID:           "worker-A",
		FencingToken:      resAcq.FencingToken,
		TTLNanoseconds:    999,
		TimestampUnixNano: renewCmd.TimestampUnixNano,
	}
	resConflict := sm.Apply(conflictingRenew)
	if !errors.Is(resConflict.Error, ErrIdempotencyConflict) {
		t.Errorf("expected ErrIdempotencyConflict on renew conflict, got %v", resConflict.Error)
	}
}

func TestIdempotencyRelease(t *testing.T) {
	sm := New()
	resAcq := sm.Apply(makeAcquireCmd("res-1", "worker-A", 10, timeOffset(0)))
	if !resAcq.Success {
		t.Fatalf("acquire failed")
	}

	releaseCmd := makeReleaseCmd("res-1", "worker-A", resAcq.FencingToken, timeOffset(2))
	res1 := sm.Apply(releaseCmd)
	if !res1.Success {
		t.Fatalf("release failed")
	}

	// Duplicate release replay
	res2 := sm.Apply(releaseCmd)
	if !res2.Success {
		t.Fatalf("duplicate release replay failed")
	}

	// Conflicting release replay
	conflictingRelease := ReleaseLockCommand{
		RequestID:         releaseCmd.RequestID,
		ResourceName:      "res-1",
		OwnerID:           "worker-OTHER",
		FencingToken:      resAcq.FencingToken,
		TimestampUnixNano: releaseCmd.TimestampUnixNano,
	}
	resConflict := sm.Apply(conflictingRelease)
	if !errors.Is(resConflict.Error, ErrIdempotencyConflict) {
		t.Errorf("expected ErrIdempotencyConflict on release conflict, got %v", resConflict.Error)
	}
}

// ============================================================================
// F. FENCING TOKEN TESTS
// ============================================================================

func TestFencingTokensMonotonicityAndIndependence(t *testing.T) {
	sm := New()

	// Sequence of acquisitions, releases, and expirations on res-1
	c1 := sm.Apply(makeAcquireCmd("res-1", "worker-1", 5, timeOffset(0)))
	if c1.FencingToken != 1 || c1.LeaseVersion != 1 {
		t.Errorf("expected token 1 version 1, got token=%d ver=%d", c1.FencingToken, c1.LeaseVersion)
	}

	sm.Apply(makeReleaseCmd("res-1", "worker-1", 1, timeOffset(2)))

	c2 := sm.Apply(makeAcquireCmd("res-1", "worker-2", 5, timeOffset(3)))
	if c2.FencingToken != 2 || c2.LeaseVersion != 1 {
		t.Errorf("expected token 2 version 1, got token=%d ver=%d", c2.FencingToken, c2.LeaseVersion)
	}

	// Expire c2 (acquired at t=3, ttl=5 -> expires at t=8, token 2, version 1)
	sm.Apply(makeExpireCmd("res-1", "worker-2", 2, 1, timeOffset(8), timeOffset(9)))

	c3 := sm.Apply(makeAcquireCmd("res-1", "worker-3", 5, timeOffset(10)))
	if c3.FencingToken != 3 || c3.LeaseVersion != 1 {
		t.Errorf("expected token 3 version 1, got token=%d ver=%d", c3.FencingToken, c3.LeaseVersion)
	}

	// Independent resource res-2 must start at token 1 and LeaseVersion 1
	cRes2 := sm.Apply(makeAcquireCmd("res-2", "worker-X", 5, timeOffset(10)))
	if cRes2.FencingToken != 1 || cRes2.LeaseVersion != 1 {
		t.Errorf("res-2 must start at token 1 version 1, got token=%d ver=%d", cRes2.FencingToken, cRes2.LeaseVersion)
	}
}

func TestFencingTokenOverflowHandling(t *testing.T) {
	sm := New()
	// Seed resource with max uint64 token
	sm.resources["res-overflow"] = &ResourceState{
		ResourceName: "res-overflow",
		HighestToken: math.MaxUint64,
		ActiveLock:   nil,
	}

	cmd := makeAcquireCmd("res-overflow", "worker-overflow", 10, timeOffset(0))
	res := sm.Apply(cmd)
	if res.Success {
		t.Fatalf("expected token overflow failure, got success")
	}
	if !errors.Is(res.Error, ErrTokenExhausted) {
		t.Errorf("expected ErrTokenExhausted, got %v", res.Error)
	}
}

// ============================================================================
// G. DETERMINISM TESTS
// ============================================================================

func TestDeterminismAndLogicalTimePolicy(t *testing.T) {
	// 1. Verify that earlier command timestamps cannot move logical time backwards
	sm := New()
	sm.Apply(makeAcquireCmd("res-1", "w-1", 10, timeOffset(100)))

	if sm.LastAppliedTime() != timeOffset(100) {
		t.Errorf("expected last applied time %d, got %d", timeOffset(100), sm.LastAppliedTime())
	}

	// Submit command with timestamp earlier than lastAppliedTime (t=50 < t=100)
	cEarlier := makeAcquireCmd("res-2", "w-2", 10, timeOffset(50))
	sm.Apply(cEarlier)

	// Logical time must not have moved backwards!
	if sm.LastAppliedTime() != timeOffset(100) {
		t.Errorf("logical time moved backwards: expected %d, got %d", timeOffset(100), sm.LastAppliedTime())
	}

	// 2. State Replay Determinism: Replaying identical commands on fresh state machine
	// produces exact identical lock states and token counters.
	cmds := []Command{
		makeAcquireCmd("res-replay", "owner-1", 10, timeOffset(10)),
		makeRenewCmd("res-replay", "owner-1", 1, 10, timeOffset(15)),
		makeReleaseCmd("res-replay", "owner-1", 1, timeOffset(20)),
		makeAcquireCmd("res-replay", "owner-2", 10, timeOffset(25)),
	}

	sm1 := New()
	sm2 := New()

	var results1, results2 []Result
	for _, c := range cmds {
		results1 = append(results1, sm1.Apply(c))
	}
	for _, c := range cmds {
		results2 = append(results2, sm2.Apply(c))
	}

	for i := range results1 {
		r1 := results1[i]
		r2 := results2[i]
		if r1.Success != r2.Success ||
			r1.FencingToken != r2.FencingToken ||
			r1.LeaseVersion != r2.LeaseVersion ||
			r1.OwnerID != r2.OwnerID ||
			r1.ExpiresAtUnixNano != r2.ExpiresAtUnixNano {
			t.Fatalf("determinism mismatch at command #%d:\nsm1: %+v\nsm2: %+v", i, r1, r2)
		}
	}

	st1, _ := sm1.GetResourceState("res-replay")
	st2, _ := sm2.GetResourceState("res-replay")
	if st1.HighestToken != st2.HighestToken ||
		st1.ActiveLock.OwnerID != st2.ActiveLock.OwnerID ||
		st1.ActiveLock.FencingToken != st2.ActiveLock.FencingToken ||
		st1.ActiveLock.LeaseVersion != st2.ActiveLock.LeaseVersion ||
		st1.ActiveLock.ExpiresAtUnixNano != st2.ActiveLock.ExpiresAtUnixNano {
		t.Fatalf("final state mismatch between sm1 and sm2:\nst1: %+v\nst2: %+v", st1, st2)
	}
}

// ============================================================================
// H. TIMESTAMP & TTL OVERFLOW AUDIT TESTS (TASK 2)
// ============================================================================

func TestArithmeticOverflow_AcquireTTL(t *testing.T) {
	sm := New()

	// TTL is positive, but TTL + timestamp overflows int64
	overflowCmd := AcquireLockCommand{
		RequestID:         nextReqID(),
		ResourceName:      "res-overflow-acq",
		OwnerID:           "worker-overflow",
		TTLNanoseconds:    math.MaxInt64,
		TimestampUnixNano: timeOffset(1),
	}

	res := sm.Apply(overflowCmd)
	if res.Success {
		t.Fatalf("expected overflow error, got success")
	}
	if !errors.Is(res.Error, ErrArithmeticOverflow) {
		t.Errorf("expected ErrArithmeticOverflow, got %v", res.Error)
	}

	// Verify no resource state was created
	if _, exists := sm.GetResourceState("res-overflow-acq"); exists {
		t.Errorf("resource should not exist in map after arithmetic overflow failure")
	}
}

func TestArithmeticOverflow_AcquireTimestamp(t *testing.T) {
	sm := New()

	overflowCmd := AcquireLockCommand{
		RequestID:         nextReqID(),
		ResourceName:      "res-overflow-ts",
		OwnerID:           "worker-overflow",
		TTLNanoseconds:    1000,
		TimestampUnixNano: math.MaxInt64,
	}

	res := sm.Apply(overflowCmd)
	if res.Success {
		t.Fatalf("expected overflow error, got success")
	}
	if !errors.Is(res.Error, ErrArithmeticOverflow) {
		t.Errorf("expected ErrArithmeticOverflow, got %v", res.Error)
	}
}

func TestArithmeticOverflow_RenewTTL(t *testing.T) {
	sm := New()
	resAcq := sm.Apply(makeAcquireCmd("res-renew-overflow", "worker-A", 10, timeOffset(0)))
	if !resAcq.Success {
		t.Fatalf("acquire failed")
	}

	overflowRenew := RenewLockCommand{
		RequestID:         nextReqID(),
		ResourceName:      "res-renew-overflow",
		OwnerID:           "worker-A",
		FencingToken:      resAcq.FencingToken,
		TTLNanoseconds:    math.MaxInt64,
		TimestampUnixNano: timeOffset(2),
	}

	res := sm.Apply(overflowRenew)
	if res.Success {
		t.Fatalf("expected overflow error on renew, got success")
	}
	if !errors.Is(res.Error, ErrArithmeticOverflow) {
		t.Errorf("expected ErrArithmeticOverflow, got %v", res.Error)
	}

	// Verify active lock was NOT altered
	lock, ok := sm.GetLock("res-renew-overflow")
	if !ok || lock.ExpiresAtUnixNano != timeOffset(10) || lock.LeaseVersion != 1 {
		t.Errorf("active lock corrupted after failed renew: %+v", lock)
	}
}

func TestInputValidation_TimestampAndTTL(t *testing.T) {
	sm := New()

	// 1. Acquire with TTL <= 0
	c1 := AcquireLockCommand{RequestID: "r1", ResourceName: "res", OwnerID: "w", TTLNanoseconds: 0, TimestampUnixNano: 100}
	if !errors.Is(sm.Apply(c1).Error, ErrInvalidTTL) {
		t.Errorf("expected ErrInvalidTTL for 0")
	}

	// 2. Acquire with Timestamp <= 0
	c2 := AcquireLockCommand{RequestID: "r2", ResourceName: "res", OwnerID: "w", TTLNanoseconds: 100, TimestampUnixNano: 0}
	if !errors.Is(sm.Apply(c2).Error, ErrInvalidTimestamp) {
		t.Errorf("expected ErrInvalidTimestamp for 0")
	}

	// 3. Renew with TTL <= 0
	c3 := RenewLockCommand{RequestID: "r3", ResourceName: "res", OwnerID: "w", FencingToken: 1, TTLNanoseconds: -1, TimestampUnixNano: 100}
	if !errors.Is(sm.Apply(c3).Error, ErrInvalidTTL) {
		t.Errorf("expected ErrInvalidTTL for negative renew")
	}

	// 4. Renew with Timestamp <= 0
	c4 := RenewLockCommand{RequestID: "r4", ResourceName: "res", OwnerID: "w", FencingToken: 1, TTLNanoseconds: 100, TimestampUnixNano: -5}
	if !errors.Is(sm.Apply(c4).Error, ErrInvalidTimestamp) {
		t.Errorf("expected ErrInvalidTimestamp for negative renew ts")
	}

	// 5. Release with Timestamp <= 0
	c5 := ReleaseLockCommand{RequestID: "r5", ResourceName: "res", OwnerID: "w", FencingToken: 1, TimestampUnixNano: 0}
	if !errors.Is(sm.Apply(c5).Error, ErrInvalidTimestamp) {
		t.Errorf("expected ErrInvalidTimestamp for release")
	}

	// 6. Expire with ExpectedExpiresAt <= 0
	c6 := ExpireLockCommand{RequestID: "r6", ResourceName: "res", ExpectedOwnerID: "w", ExpectedFencingToken: 1, ExpectedLeaseVersion: 1, ExpectedExpiresAtUnixNano: 0, TimestampUnixNano: 100}
	if !errors.Is(sm.Apply(c6).Error, ErrInvalidTimestamp) {
		t.Errorf("expected ErrInvalidTimestamp for expire expected expiry <= 0")
	}

	// 7. Expire with ExpectedLeaseVersion == 0
	c7 := ExpireLockCommand{RequestID: "r7", ResourceName: "res", ExpectedOwnerID: "w", ExpectedFencingToken: 1, ExpectedLeaseVersion: 0, ExpectedExpiresAtUnixNano: 100, TimestampUnixNano: 100}
	if !errors.Is(sm.Apply(c7).Error, ErrLeaseVersionMismatch) {
		t.Errorf("expected ErrLeaseVersionMismatch for expire expected version == 0")
	}

	// 8. Expire with Timestamp <= 0
	c8 := ExpireLockCommand{RequestID: "r8", ResourceName: "res", ExpectedOwnerID: "w", ExpectedFencingToken: 1, ExpectedLeaseVersion: 1, ExpectedExpiresAtUnixNano: 100, TimestampUnixNano: 0}
	if !errors.Is(sm.Apply(c8).Error, ErrInvalidTimestamp) {
		t.Errorf("expected ErrInvalidTimestamp for expire timestamp <= 0")
	}
}

// ============================================================================
// I. ATOMIC STATE TRANSITION AUDIT TESTS (TASK 3)
// ============================================================================

func TestAtomicity_FailedAcquireLeavesStateUnchanged(t *testing.T) {
	sm := New()
	res1 := sm.Apply(makeAcquireCmd("res-atom-acq", "worker-A", 10, timeOffset(0)))
	if !res1.Success {
		t.Fatalf("initial acquire failed")
	}

	// Attempt competing acquire by worker-B
	failedCmd := makeAcquireCmd("res-atom-acq", "worker-B", 10, timeOffset(2))
	resFail := sm.Apply(failedCmd)
	if resFail.Success {
		t.Fatalf("expected acquire to fail")
	}

	// Assert active lock is completely unchanged
	lock, ok := sm.GetLock("res-atom-acq")
	if !ok || lock.OwnerID != "worker-A" || lock.FencingToken != 1 || lock.LeaseVersion != 1 || lock.ExpiresAtUnixNano != timeOffset(10) {
		t.Errorf("lock state altered after failed acquire: %+v", lock)
	}

	// Assert HighestToken is unchanged
	rs, _ := sm.GetResourceState("res-atom-acq")
	if rs.HighestToken != 1 {
		t.Errorf("HighestToken altered: expected 1, got %d", rs.HighestToken)
	}
}

func TestAtomicity_FailedRenewLeavesStateUnchanged(t *testing.T) {
	sm := New()
	res1 := sm.Apply(makeAcquireCmd("res-atom-ren", "worker-A", 10, timeOffset(0)))
	if !res1.Success {
		t.Fatalf("initial acquire failed")
	}

	// Failed renewals: wrong owner, wrong token, overflow
	sm.Apply(makeRenewCmd("res-atom-ren", "worker-B", 1, 10, timeOffset(2)))
	sm.Apply(makeRenewCmd("res-atom-ren", "worker-A", 99, 10, timeOffset(2)))

	lock, ok := sm.GetLock("res-atom-ren")
	if !ok || lock.OwnerID != "worker-A" || lock.FencingToken != 1 || lock.LeaseVersion != 1 || lock.ExpiresAtUnixNano != timeOffset(10) {
		t.Errorf("lock mutated after failed renewal: %+v", lock)
	}
}

func TestAtomicity_FailedReleaseLeavesStateUnchanged(t *testing.T) {
	sm := New()
	res1 := sm.Apply(makeAcquireCmd("res-atom-rel", "worker-A", 10, timeOffset(0)))
	if !res1.Success {
		t.Fatalf("initial acquire failed")
	}

	// Failed releases: wrong owner, wrong token
	sm.Apply(makeReleaseCmd("res-atom-rel", "worker-B", 1, timeOffset(2)))
	sm.Apply(makeReleaseCmd("res-atom-rel", "worker-A", 99, timeOffset(2)))

	lock, ok := sm.GetLock("res-atom-rel")
	if !ok || lock.OwnerID != "worker-A" || lock.FencingToken != 1 || lock.LeaseVersion != 1 || lock.ExpiresAtUnixNano != timeOffset(10) {
		t.Errorf("lock mutated after failed release: %+v", lock)
	}

	// Attempt release after expiry at t=12
	resExpiredRel := sm.Apply(makeReleaseCmd("res-atom-rel", "worker-A", 1, timeOffset(12)))
	if !errors.Is(resExpiredRel.Error, ErrLeaseExpired) {
		t.Errorf("expected ErrLeaseExpired, got %v", resExpiredRel.Error)
	}

	// Even on expired release error, active lock record must remain intact (not cleared to nil)
	// until explicit expiration command or new acquire
	rs, _ := sm.GetResourceState("res-atom-rel")
	if rs.ActiveLock == nil {
		t.Errorf("failed release must NOT clear ActiveLock")
	}
}

func TestLogicalTimePolicy_ValidationVsBusinessFailure(t *testing.T) {
	sm := New()
	if sm.LastAppliedTime() != 0 {
		t.Errorf("expected initial LastAppliedTime 0, got %d", sm.LastAppliedTime())
	}

	// 1. Syntactic validation failure: must NOT update LastAppliedTime and must NOT record in idempotency
	invalidCmd := AcquireLockCommand{
		RequestID:         "req-invalid-1",
		ResourceName:      "", // Invalid empty resource
		OwnerID:           "worker-A",
		TTLNanoseconds:    1000,
		TimestampUnixNano: timeOffset(10),
	}
	resVal := sm.Apply(invalidCmd)
	if resVal.Success || !errors.Is(resVal.Error, ErrInvalidResource) {
		t.Fatalf("expected validation error, got %+v", resVal)
	}
	if sm.LastAppliedTime() != 0 {
		t.Errorf("LastAppliedTime should remain 0 after validation failure, got %d", sm.LastAppliedTime())
	}
	if _, found := sm.idempotency["req-invalid-1"]; found {
		t.Errorf("syntactic validation failure must NOT be recorded in idempotency store")
	}

	// 2. Business operation success: advances LastAppliedTime and records in idempotency
	validCmd := makeAcquireCmd("res-time-test", "worker-A", 10, timeOffset(10))
	resAcq := sm.Apply(validCmd)
	if !resAcq.Success {
		t.Fatalf("valid acquire failed: %v", resAcq.Error)
	}
	if sm.LastAppliedTime() != timeOffset(10) {
		t.Errorf("expected LastAppliedTime %d, got %d", timeOffset(10), sm.LastAppliedTime())
	}

	// 3. Business operation failure (e.g. lock already held):
	// In consensus log replay, every successfully committed log entry updates logical applied time,
	// and records the failure result in the idempotency table for deterministic replay.
	competingCmd := makeAcquireCmd("res-time-test", "worker-B", 10, timeOffset(15))
	resFail := sm.Apply(competingCmd)
	if resFail.Success || !errors.Is(resFail.Error, ErrLockAlreadyHeld) {
		t.Fatalf("expected ErrLockAlreadyHeld, got %+v", resFail)
	}
	if sm.LastAppliedTime() != timeOffset(15) {
		t.Errorf("LastAppliedTime must advance to %d on applied log command, got %d", timeOffset(15), sm.LastAppliedTime())
	}
	if _, found := sm.idempotency[competingCmd.RequestID]; !found {
		t.Errorf("applied business failure must be recorded in idempotency store for deterministic replay")
	}

	// Replay competingCmd: must return identical cached error result without side-effects
	resReplay := sm.Apply(competingCmd)
	if resReplay.Success || !errors.Is(resReplay.Error, ErrLockAlreadyHeld) {
		t.Errorf("replay should return cached ErrLockAlreadyHeld")
	}
}
