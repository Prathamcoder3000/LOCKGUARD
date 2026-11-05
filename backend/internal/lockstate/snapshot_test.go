package lockstate_test

import (
	"bytes"
	"testing"
	"time"

	"lockguard/backend/internal/lockstate"
)

func TestSnapshot_ExportAndRestore(t *testing.T) {
	sm := lockstate.New()

	baseTime := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC).UnixNano()

	// Apply multiple commands across multiple resources
	cmd1 := lockstate.AcquireLockCommand{
		RequestID:         "req-1",
		ResourceName:      "database-master",
		OwnerID:           "node-alpha",
		TTLNanoseconds:    (10 * time.Second).Nanoseconds(),
		TimestampUnixNano: baseTime,
	}
	res1 := sm.Apply(cmd1)
	if !res1.Success {
		t.Fatalf("expected res1 success, got %v", res1.Message)
	}

	cmd2 := lockstate.AcquireLockCommand{
		RequestID:         "req-2",
		ResourceName:      "analytics-engine",
		OwnerID:           "node-beta",
		TTLNanoseconds:    (20 * time.Second).Nanoseconds(),
		TimestampUnixNano: baseTime + 1000,
	}
	res2 := sm.Apply(cmd2)
	if !res2.Success {
		t.Fatalf("expected res2 success, got %v", res2.Message)
	}

	// Renew database-master
	cmd3 := lockstate.RenewLockCommand{
		RequestID:         "req-3",
		ResourceName:      "database-master",
		OwnerID:           "node-alpha",
		FencingToken:      res1.FencingToken,
		TTLNanoseconds:    (15 * time.Second).Nanoseconds(),
		TimestampUnixNano: baseTime + 2000,
	}
	res3 := sm.Apply(cmd3)
	if !res3.Success {
		t.Fatalf("expected res3 success, got %v", res3.Message)
	}
	if res3.LeaseVersion != 2 {
		t.Fatalf("expected lease version 2 after renew, got %d", res3.LeaseVersion)
	}

	// Also record a failed command to verify idempotency error restore
	cmd4 := lockstate.AcquireLockCommand{
		RequestID:         "req-4",
		ResourceName:      "database-master",
		OwnerID:           "node-gamma",
		TTLNanoseconds:    (10 * time.Second).Nanoseconds(),
		TimestampUnixNano: baseTime + 3000,
	}
	res4 := sm.Apply(cmd4)
	if res4.Success {
		t.Fatalf("expected cmd4 to fail because database-master is held")
	}

	// Export snapshot
	snap, err := sm.ExportSnapshot()
	if err != nil {
		t.Fatalf("ExportSnapshot failed: %v", err)
	}

	if len(snap.Resources) != 2 {
		t.Fatalf("expected 2 resources in snapshot, got %d", len(snap.Resources))
	}
	// Verify deterministic sorting: "analytics-engine" must precede "database-master"
	if snap.Resources[0].ResourceName != "analytics-engine" || snap.Resources[1].ResourceName != "database-master" {
		t.Fatalf("resources not sorted deterministically: %+v", snap.Resources)
	}

	// Encode to JSON buffer
	var buf bytes.Buffer
	if err := snap.Encode(&buf); err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Decode from buffer
	decodedSnap, err := lockstate.DecodeSnapshot(&buf)
	if err != nil {
		t.Fatalf("DecodeSnapshot failed: %v", err)
	}

	// Restore into a fresh state machine
	smFresh := lockstate.New()
	if err := smFresh.RestoreSnapshot(decodedSnap); err != nil {
		t.Fatalf("RestoreSnapshot failed: %v", err)
	}

	// Verify restored state
	rsDB, found := smFresh.GetResourceState("database-master")
	if !found {
		t.Fatalf("database-master not found in restored state")
	}
	if rsDB.ActiveLock == nil {
		t.Fatalf("database-master active lock is nil")
	}
	if rsDB.ActiveLock.OwnerID != "node-alpha" {
		t.Fatalf("expected owner node-alpha, got %s", rsDB.ActiveLock.OwnerID)
	}
	if rsDB.ActiveLock.FencingToken != res1.FencingToken {
		t.Fatalf("expected fencing token %d, got %d", res1.FencingToken, rsDB.ActiveLock.FencingToken)
	}
	if rsDB.ActiveLock.LeaseVersion != 2 {
		t.Fatalf("expected lease version 2, got %d", rsDB.ActiveLock.LeaseVersion)
	}

	// Verify idempotency was restored: replaying cmd1 should yield cached result
	replayRes1 := smFresh.Apply(cmd1)
	if !replayRes1.Success || replayRes1.FencingToken != res1.FencingToken || replayRes1.LeaseVersion != res1.LeaseVersion {
		t.Fatalf("replayed cmd1 does not match original result: %+v vs %+v", replayRes1, res1)
	}

	// Replaying failed cmd4 should return identical cached error
	replayRes4 := smFresh.Apply(cmd4)
	if replayRes4.Success || replayRes4.Code != res4.Code {
		t.Fatalf("replayed cmd4 does not match cached error: %+v vs %+v", replayRes4, res4)
	}

	// Verify LastAppliedTime
	if smFresh.LastAppliedTime() != sm.LastAppliedTime() {
		t.Fatalf("LastAppliedTime mismatch: %d vs %d", smFresh.LastAppliedTime(), sm.LastAppliedTime())
	}
}

func TestSnapshot_CorruptSnapshotDoesNotCorruptExistingState(t *testing.T) {
	sm := lockstate.New()
	baseTime := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC).UnixNano()

	cmd := lockstate.AcquireLockCommand{
		RequestID:         "req-safe-1",
		ResourceName:      "protected-table",
		OwnerID:           "safe-owner",
		TTLNanoseconds:    (60 * time.Second).Nanoseconds(),
		TimestampUnixNano: baseTime,
	}
	res := sm.Apply(cmd)
	if !res.Success {
		t.Fatalf("initial acquire failed: %v", res.Message)
	}

	// 1. Attempt restore with unsupported version
	corruptVerSnap := &lockstate.SnapshotData{
		Version:             999,
		LastAppliedUnixNano: baseTime + 1000,
		Resources:           []lockstate.ResourceState{{ResourceName: "bogus"}},
	}
	if err := sm.RestoreSnapshot(corruptVerSnap); err == nil {
		t.Fatalf("expected error restoring snapshot with invalid version")
	}

	// 2. Attempt restore with empty resource name
	corruptResSnap := &lockstate.SnapshotData{
		Version:             lockstate.CurrentSnapshotVersion,
		LastAppliedUnixNano: baseTime + 1000,
		Resources: []lockstate.ResourceState{
			{ResourceName: ""}, // Empty name -> corrupt
		},
	}
	if err := sm.RestoreSnapshot(corruptResSnap); err == nil {
		t.Fatalf("expected error restoring snapshot with empty resource name")
	}

	// 3. Attempt restore with empty request ID in idempotency record
	corruptIdemSnap := &lockstate.SnapshotData{
		Version:             lockstate.CurrentSnapshotVersion,
		LastAppliedUnixNano: baseTime + 1000,
		Resources: []lockstate.ResourceState{
			{ResourceName: "valid-res"},
		},
		IdempotencyRecords: []lockstate.IdempotencyRecord{
			{RequestID: ""}, // Empty request ID -> corrupt
		},
	}
	if err := sm.RestoreSnapshot(corruptIdemSnap); err == nil {
		t.Fatalf("expected error restoring snapshot with empty idempotency request ID")
	}

	// 4. Verify existing state machine was completely protected and untouched
	rec, found := sm.GetLock("protected-table")
	if !found {
		t.Fatalf("expected protected-table to remain locked after failed restores")
	}
	if rec.OwnerID != "safe-owner" || rec.FencingToken != 1 || rec.LeaseVersion != 1 {
		t.Fatalf("existing state was corrupted by failed restore: %+v", rec)
	}

	// Idempotency cache still works
	cachedRes := sm.Apply(cmd)
	if !cachedRes.Success || cachedRes.FencingToken != 1 {
		t.Fatalf("idempotency cache was damaged: %+v", cachedRes)
	}
}

// 1. Snapshot and restore followed by an identical idempotent command and a conflicting command.
func TestSnapshot_AuditA_Idempotency_IdenticalAndConflictingReplay(t *testing.T) {
	sm := lockstate.New()
	baseTime := time.Date(2026, 11, 6, 1, 0, 0, 0, time.UTC).UnixNano()

	cmd := lockstate.AcquireLockCommand{
		RequestID:         "req-idem-audit-1",
		ResourceName:      "vault-audit",
		OwnerID:           "worker-alpha",
		TTLNanoseconds:    (30 * time.Second).Nanoseconds(),
		TimestampUnixNano: baseTime,
	}
	resOrig := sm.Apply(cmd)
	if !resOrig.Success {
		t.Fatalf("initial acquire failed: %v", resOrig.Message)
	}

	// Export and Restore into smFresh
	snap, err := sm.ExportSnapshot()
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}
	var buf bytes.Buffer
	_ = snap.Encode(&buf)

	decoded, _ := lockstate.DecodeSnapshot(&buf)
	smFresh := lockstate.New()
	if err := smFresh.RestoreSnapshot(decoded); err != nil {
		t.Fatalf("restore failed: %v", err)
	}

	// Replay identical command: must return cached Result exactly
	resIdentical := smFresh.Apply(cmd)
	if !resIdentical.Success || resIdentical.FencingToken != resOrig.FencingToken || resIdentical.OwnerID != resOrig.OwnerID {
		t.Fatalf("expected identical cached result, got %+v vs %+v", resIdentical, resOrig)
	}

	// Replay conflicting command with SAME RequestID but differing payload (different OwnerID)
	cmdConflicting := cmd
	cmdConflicting.OwnerID = "malicious-impersonator"
	resConflict := smFresh.Apply(cmdConflicting)
	if resConflict.Success {
		t.Fatalf("expected conflicting idempotency command to fail, but it succeeded")
	}
	if resConflict.Code != "ERR_IDEMPOTENCY_CONFLICT" {
		t.Fatalf("expected ERR_IDEMPOTENCY_CONFLICT, got %s", resConflict.Code)
	}
}

// 2. Snapshot and restore after lock renewal preserving exact LeaseVersion.
func TestSnapshot_AuditA_LockRenewal_LeaseVersionPreserved(t *testing.T) {
	sm := lockstate.New()
	baseTime := time.Date(2026, 11, 6, 1, 10, 0, 0, time.UTC).UnixNano()

	// Acquire (LeaseVersion = 1)
	resAcq := sm.Apply(lockstate.AcquireLockCommand{
		RequestID:         "req-renew-acq",
		ResourceName:      "renew-audit-res",
		OwnerID:           "worker-renew",
		TTLNanoseconds:    (10 * time.Second).Nanoseconds(),
		TimestampUnixNano: baseTime,
	})
	if !resAcq.Success || resAcq.LeaseVersion != 1 {
		t.Fatalf("expected LeaseVersion 1, got %+v", resAcq)
	}

	// Renew (LeaseVersion = 2)
	resRen := sm.Apply(lockstate.RenewLockCommand{
		RequestID:         "req-renew-ren",
		ResourceName:      "renew-audit-res",
		OwnerID:           "worker-renew",
		FencingToken:      resAcq.FencingToken,
		TTLNanoseconds:    (15 * time.Second).Nanoseconds(),
		TimestampUnixNano: baseTime + 1000,
	})
	if !resRen.Success || resRen.LeaseVersion != 2 {
		t.Fatalf("expected LeaseVersion 2, got %+v", resRen)
	}

	// Snapshot and restore
	snap, _ := sm.ExportSnapshot()
	var buf bytes.Buffer
	_ = snap.Encode(&buf)
	decoded, _ := lockstate.DecodeSnapshot(&buf)

	smFresh := lockstate.New()
	if err := smFresh.RestoreSnapshot(decoded); err != nil {
		t.Fatalf("restore failed: %v", err)
	}

	// Verify restored active lock record
	rec, found := smFresh.GetLock("renew-audit-res")
	if !found || rec.LeaseVersion != 2 || rec.FencingToken != resAcq.FencingToken {
		t.Fatalf("restored lock record mismatch: expected version 2, token %d, got %+v", resAcq.FencingToken, rec)
	}

	// Renew again on smFresh -> LeaseVersion must advance strictly to 3
	resRen2 := smFresh.Apply(lockstate.RenewLockCommand{
		RequestID:         "req-renew-ren2",
		ResourceName:      "renew-audit-res",
		OwnerID:           "worker-renew",
		FencingToken:      resAcq.FencingToken,
		TTLNanoseconds:    (15 * time.Second).Nanoseconds(),
		TimestampUnixNano: baseTime + 2000,
	})
	if !resRen2.Success || resRen2.LeaseVersion != 3 {
		t.Fatalf("expected LeaseVersion 3 on post-restore renewal, got %+v", resRen2)
	}
}

// 3. Snapshot and restore after release, followed by reacquisition.
func TestSnapshot_AuditA_ReleaseAndReacquisition_StrictTokenMonotonicity(t *testing.T) {
	sm := lockstate.New()
	baseTime := time.Date(2026, 11, 6, 1, 20, 0, 0, time.UTC).UnixNano()

	// Acquire Token 1
	sm.Apply(lockstate.AcquireLockCommand{
		RequestID:         "req-rel-1",
		ResourceName:      "released-audit-res",
		OwnerID:           "worker-1",
		TTLNanoseconds:    (10 * time.Second).Nanoseconds(),
		TimestampUnixNano: baseTime,
	})

	// Release Token 1
	sm.Apply(lockstate.ReleaseLockCommand{
		RequestID:         "req-rel-2",
		ResourceName:      "released-audit-res",
		OwnerID:           "worker-1",
		FencingToken:      1,
		TimestampUnixNano: baseTime + 1000,
	})

	// Snapshot and restore
	snap, _ := sm.ExportSnapshot()
	var buf bytes.Buffer
	_ = snap.Encode(&buf)
	decoded, _ := lockstate.DecodeSnapshot(&buf)

	smFresh := lockstate.New()
	if err := smFresh.RestoreSnapshot(decoded); err != nil {
		t.Fatalf("restore failed: %v", err)
	}

	// Confirm unlocked but highest token is 1
	_, isHeld := smFresh.GetLock("released-audit-res")
	if isHeld {
		t.Fatalf("expected resource to be unlocked after restore")
	}
	rState, found := smFresh.GetResourceState("released-audit-res")
	if !found || rState.HighestToken != 1 {
		t.Fatalf("expected preserved HighestToken 1, got %+v", rState)
	}

	// Reacquire on restored engine -> MUST receive Token 2
	resReacq := smFresh.Apply(lockstate.AcquireLockCommand{
		RequestID:         "req-rel-3",
		ResourceName:      "released-audit-res",
		OwnerID:           "worker-2",
		TTLNanoseconds:    (10 * time.Second).Nanoseconds(),
		TimestampUnixNano: baseTime + 2000,
	})
	if !resReacq.Success || resReacq.FencingToken != 2 {
		t.Fatalf("CRITICAL: expected fencing token 2 upon reacquisition, got %+v", resReacq)
	}
}

// 4. Snapshot and restore with multiple resources and different fencing-token histories.
func TestSnapshot_AuditA_MultipleResources_DiverseTokenHistories(t *testing.T) {
	sm := lockstate.New()
	baseTime := time.Date(2026, 11, 6, 1, 30, 0, 0, time.UTC).UnixNano()

	// resA: acquired, released, reacquired -> HighestToken = 2, currently HELD with token 2
	sm.Apply(lockstate.AcquireLockCommand{RequestID: "a1", ResourceName: "resA", OwnerID: "o1", TTLNanoseconds: 1e9, TimestampUnixNano: baseTime})
	sm.Apply(lockstate.ReleaseLockCommand{RequestID: "a2", ResourceName: "resA", OwnerID: "o1", FencingToken: 1, TimestampUnixNano: baseTime + 1})
	sm.Apply(lockstate.AcquireLockCommand{RequestID: "a3", ResourceName: "resA", OwnerID: "o2", TTLNanoseconds: 1e9, TimestampUnixNano: baseTime + 2})

	// resB: acquired, released -> HighestToken = 1, currently FREE
	sm.Apply(lockstate.AcquireLockCommand{RequestID: "b1", ResourceName: "resB", OwnerID: "o1", TTLNanoseconds: 1e9, TimestampUnixNano: baseTime})
	sm.Apply(lockstate.ReleaseLockCommand{RequestID: "b2", ResourceName: "resB", OwnerID: "o1", FencingToken: 1, TimestampUnixNano: baseTime + 1})

	// resC: acquired, renewed, renewed -> HighestToken = 1, LeaseVersion = 3, currently HELD
	sm.Apply(lockstate.AcquireLockCommand{RequestID: "c1", ResourceName: "resC", OwnerID: "o3", TTLNanoseconds: 1e9, TimestampUnixNano: baseTime})
	sm.Apply(lockstate.RenewLockCommand{RequestID: "c2", ResourceName: "resC", OwnerID: "o3", FencingToken: 1, TTLNanoseconds: 1e9, TimestampUnixNano: baseTime + 1})
	sm.Apply(lockstate.RenewLockCommand{RequestID: "c3", ResourceName: "resC", OwnerID: "o3", FencingToken: 1, TTLNanoseconds: 1e9, TimestampUnixNano: baseTime + 2})

	// Snapshot and restore
	snap, _ := sm.ExportSnapshot()
	var buf bytes.Buffer
	_ = snap.Encode(&buf)
	decoded, _ := lockstate.DecodeSnapshot(&buf)

	smFresh := lockstate.New()
	if err := smFresh.RestoreSnapshot(decoded); err != nil {
		t.Fatalf("restore failed: %v", err)
	}

	// Verify resA
	rA, _ := smFresh.GetResourceState("resA")
	if rA.HighestToken != 2 || rA.ActiveLock == nil || rA.ActiveLock.FencingToken != 2 {
		t.Fatalf("resA state mismatch: %+v", rA)
	}

	// Verify resB
	rB, _ := smFresh.GetResourceState("resB")
	if rB.HighestToken != 1 || rB.ActiveLock != nil {
		t.Fatalf("resB state mismatch: %+v", rB)
	}

	// Verify resC
	rC, _ := smFresh.GetResourceState("resC")
	if rC.HighestToken != 1 || rC.ActiveLock == nil || rC.ActiveLock.LeaseVersion != 3 {
		t.Fatalf("resC state mismatch: %+v", rC)
	}

	// Reacquire resB -> must get token 2
	resBNew := smFresh.Apply(lockstate.AcquireLockCommand{RequestID: "b3", ResourceName: "resB", OwnerID: "o9", TTLNanoseconds: 1e9, TimestampUnixNano: baseTime + 10})
	if resBNew.FencingToken != 2 {
		t.Fatalf("expected resB token 2, got %d", resBNew.FencingToken)
	}
}

// 5. Snapshot and restore with logical timestamps testing deterministic time progression.
func TestSnapshot_AuditA_DeterministicLogicalTimestampProgression(t *testing.T) {
	sm := lockstate.New()
	time100 := int64(100_000_000_000)

	sm.Apply(lockstate.AcquireLockCommand{
		RequestID:         "req-ts-1",
		ResourceName:      "ts-res",
		OwnerID:           "worker-ts",
		TTLNanoseconds:    10 * int64(time.Second),
		TimestampUnixNano: time100,
	})

	if sm.LastAppliedTime() != time100 {
		t.Fatalf("expected LastAppliedTime %d, got %d", time100, sm.LastAppliedTime())
	}

	// Snapshot and restore
	snap, _ := sm.ExportSnapshot()
	var buf bytes.Buffer
	_ = snap.Encode(&buf)
	decoded, _ := lockstate.DecodeSnapshot(&buf)

	smFresh := lockstate.New()
	_ = smFresh.RestoreSnapshot(decoded)

	if smFresh.LastAppliedTime() != time100 {
		t.Fatalf("restored LastAppliedTime mismatch: expected %d, got %d", time100, smFresh.LastAppliedTime())
	}

	// Apply command with past timestamp time50 (< time100): LastAppliedTime must remain time100
	time50 := int64(50_000_000_000)
	smFresh.Apply(lockstate.AcquireLockCommand{
		RequestID:         "req-ts-past",
		ResourceName:      "ts-res-past",
		OwnerID:           "worker-past",
		TTLNanoseconds:    10 * int64(time.Second),
		TimestampUnixNano: time50,
	})
	if smFresh.LastAppliedTime() != time100 {
		t.Fatalf("LastAppliedTime should not regress: expected %d, got %d", time100, smFresh.LastAppliedTime())
	}

	// Apply command with future timestamp time200 (> time100): LastAppliedTime must advance to time200
	time200 := int64(200_000_000_000)
	smFresh.Apply(lockstate.AcquireLockCommand{
		RequestID:         "req-ts-future",
		ResourceName:      "ts-res-future",
		OwnerID:           "worker-future",
		TTLNanoseconds:    10 * int64(time.Second),
		TimestampUnixNano: time200,
	})
	if smFresh.LastAppliedTime() != time200 {
		t.Fatalf("LastAppliedTime should advance to %d, got %d", time200, smFresh.LastAppliedTime())
	}
}

// 6. Corrupt or incomplete snapshot rejection without partially mutating existing state.
func TestSnapshot_AuditA_CorruptPayloadRejections_ZeroMutation(t *testing.T) {
	sm := lockstate.New()
	baseTime := time.Date(2026, 11, 6, 2, 0, 0, 0, time.UTC).UnixNano()

	// Initial clean state with active lock
	sm.Apply(lockstate.AcquireLockCommand{
		RequestID:         "req-initial",
		ResourceName:      "vault-secure",
		OwnerID:           "vault-guard",
		TTLNanoseconds:    60 * int64(time.Second),
		TimestampUnixNano: baseTime,
	})

	corruptSnapshots := []*lockstate.SnapshotData{
		// 1. Negative timestamp
		{
			Version:             lockstate.CurrentSnapshotVersion,
			LastAppliedUnixNano: -500,
			Resources:           []lockstate.ResourceState{{ResourceName: "bogus"}},
		},
		// 2. Duplicate resource name
		{
			Version:             lockstate.CurrentSnapshotVersion,
			LastAppliedUnixNano: baseTime,
			Resources: []lockstate.ResourceState{
				{ResourceName: "dup-res", HighestToken: 1},
				{ResourceName: "dup-res", HighestToken: 2},
			},
		},
		// 3. ActiveLock fencing token > HighestToken
		{
			Version:             lockstate.CurrentSnapshotVersion,
			LastAppliedUnixNano: baseTime,
			Resources: []lockstate.ResourceState{
				{
					ResourceName: "mismatch-res",
					HighestToken: 1,
					ActiveLock: &lockstate.LockRecord{
						ResourceName: "mismatch-res",
						FencingToken: 5, // 5 > 1 => corrupt
						LeaseVersion: 1,
					},
				},
			},
		},
		// 4. Duplicate request ID in idempotency records
		{
			Version:             lockstate.CurrentSnapshotVersion,
			LastAppliedUnixNano: baseTime,
			Resources:           []lockstate.ResourceState{{ResourceName: "ok-res", HighestToken: 1}},
			IdempotencyRecords: []lockstate.IdempotencyRecord{
				{RequestID: "dup-req", Result: lockstate.Result{Success: true}},
				{RequestID: "dup-req", Result: lockstate.Result{Success: true}},
			},
		},
	}

	for i, corruptSnap := range corruptSnapshots {
		err := sm.RestoreSnapshot(corruptSnap)
		if err == nil {
			t.Fatalf("corrupt snapshot scenario %d was accepted without error", i)
		}
	}

	// Verify original state remains 100% intact and unmutated
	rec, found := sm.GetLock("vault-secure")
	if !found || rec.OwnerID != "vault-guard" || rec.FencingToken != 1 {
		t.Fatalf("existing state was damaged by rejected restores: %+v", rec)
	}
}

// 7. Old lease-expiration command cannot invalidate a renewed lease after snapshot restoration.
func TestSnapshot_AuditA_StaleExpirationDoesNotInvalidateRenewedLease(t *testing.T) {
	sm := lockstate.New()
	baseTime := time.Date(2026, 11, 6, 2, 30, 0, 0, time.UTC).UnixNano()

	// Acquire Version 1 (expires at baseTime + 10s)
	resAcq := sm.Apply(lockstate.AcquireLockCommand{
		RequestID:         "req-exp-acq",
		ResourceName:      "lease-exp-res",
		OwnerID:           "worker-exp",
		TTLNanoseconds:    (10 * time.Second).Nanoseconds(),
		TimestampUnixNano: baseTime,
	})
	expV1 := resAcq.ExpiresAtUnixNano

	// Renew to Version 2 (expires at baseTime + 25s)
	resRen := sm.Apply(lockstate.RenewLockCommand{
		RequestID:         "req-exp-ren",
		ResourceName:      "lease-exp-res",
		OwnerID:           "worker-exp",
		FencingToken:      resAcq.FencingToken,
		TTLNanoseconds:    (20 * time.Second).Nanoseconds(),
		TimestampUnixNano: baseTime + 5000,
	})
	if !resRen.Success || resRen.LeaseVersion != 2 {
		t.Fatalf("renew failed: %+v", resRen)
	}

	// Snapshot and restore
	snap, _ := sm.ExportSnapshot()
	var buf bytes.Buffer
	_ = snap.Encode(&buf)
	decoded, _ := lockstate.DecodeSnapshot(&buf)

	smFresh := lockstate.New()
	_ = smFresh.RestoreSnapshot(decoded)

	// Now an old ExpireLockCommand generated for Version 1 arrives at time baseTime + 12s
	// (when Version 1 would have expired, but Version 2 is still valid until baseTime + 25s)
	cmdOldExpire := lockstate.ExpireLockCommand{
		RequestID:                  "req-old-expire",
		ResourceName:               "lease-exp-res",
		ExpectedOwnerID:            "worker-exp",
		ExpectedFencingToken:       resAcq.FencingToken,
		ExpectedLeaseVersion:       1, // Old version!
		ExpectedExpiresAtUnixNano:  expV1,
		TimestampUnixNano:          baseTime + 12_000_000_000,
	}

	resOldExpire := smFresh.Apply(cmdOldExpire)
	if resOldExpire.Success {
		t.Fatalf("CRITICAL SAFETY VIOLATION: stale expiration command for Version 1 expired active Version 2 lease!")
	}
	if resOldExpire.Code != "ERR_LEASE_VERSION_MISMATCH" {
		t.Fatalf("expected ERR_LEASE_VERSION_MISMATCH, got %s", resOldExpire.Code)
	}

	// Verify lease is STILL ACTIVE and HELD at Version 2
	rec, found := smFresh.GetLock("lease-exp-res")
	if !found || rec.OwnerID != "worker-exp" || rec.LeaseVersion != 2 {
		t.Fatalf("active lease was incorrectly altered: %+v", rec)
	}
}
