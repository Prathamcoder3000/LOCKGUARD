package lockstate

import (
	"testing"
)

func TestMemoryStateMachine_WallClockExpirationQueriesPreserveDeterminism(t *testing.T) {
	sm := New()

	acq := AcquireLockCommand{
		RequestID:         "req-exp-q-1",
		ResourceName:      "res-ttl-1",
		OwnerID:           "worker-1",
		TTLNanoseconds:    5_000_000_000, // 5s
		TimestampUnixNano: 100_000_000_000,
	}
	res := sm.Apply(acq)
	if !res.Success || res.FencingToken == 0 {
		t.Fatalf("acquire failed: %+v", res)
	}

	// Deterministic baseline at lastApplied (100s) remains HELD
	if _, ok := sm.GetLock("res-ttl-1"); !ok {
		t.Fatalf("expected GetLock to find unexpired lock at lastApplied")
	}

	// Wall clock before expiration returns active HELD lock
	beforeExp := int64(104_000_000_000)
	rec, ok := sm.GetLockAt("res-ttl-1", beforeExp)
	if !ok || rec.Status != LockStatusHeld {
		t.Fatalf("expected HELD before expiration, got ok=%v status=%s", ok, rec.Status)
	}

	// Wall clock past expiration without any new write still treats lock as expired
	afterExp := int64(106_000_000_000)
	if _, ok := sm.GetLockAt("res-ttl-1", afterExp); ok {
		t.Fatalf("expected GetLockAt to hide expired lock at %d", afterExp)
	}

	activeOnly := sm.ListLocksAt(afterExp, false)
	if len(activeOnly) != 0 {
		t.Fatalf("expected 0 active locks after expiry, got %d", len(activeOnly))
	}

	withExpired := sm.ListLocksAt(afterExp, true)
	if len(withExpired) != 1 || withExpired[0].Status != LockStatusExpired {
		t.Fatalf("expected 1 EXPIRED lock when includeExpired=true, got %+v", withExpired)
	}

	toReap := sm.ListExpiredLocksAt(afterExp)
	if len(toReap) != 1 || toReap[0].ResourceName != "res-ttl-1" {
		t.Fatalf("expected 1 lock ready for reaper, got %+v", toReap)
	}

	// Verify deterministic ListLocks() without wall clock still works on cloned records cleanly
	if cur := sm.ListLocks(); len(cur) != 1 || cur[0].Status != LockStatusHeld {
		t.Fatalf("expected original deterministic ListLocks to be untouched, got %+v", cur)
	}
}
