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
