package consensus_test

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"lockguard/backend/internal/consensus"
	"lockguard/backend/internal/lockstate"
)

// mockSnapshotSink implements raft.SnapshotSink for testing
type mockSnapshotSink struct {
	bytes.Buffer
	cancelled bool
	closed    bool
}

func (m *mockSnapshotSink) ID() string { return "test-snap-1" }
func (m *mockSnapshotSink) Cancel() error {
	m.cancelled = true
	return nil
}
func (m *mockSnapshotSink) Close() error {
	m.closed = true
	return nil
}

func TestLockFSM_ApplyAndSnapshotRestore(t *testing.T) {
	sm1 := lockstate.New()
	fsm1 := consensus.NewLockFSM(sm1, nil)

	// Create and encode an Acquire command
	cmdAcquire := lockstate.AcquireLockCommand{
		RequestID:         "req-fsm-1",
		ResourceName:      "res-fsm-alpha",
		OwnerID:           "node-fsm",
		TTLNanoseconds:    (10 * time.Second).Nanoseconds(),
		TimestampUnixNano: 1000000,
	}
	encodedAcquire, err := consensus.EncodeCommand(cmdAcquire)
	if err != nil {
		t.Fatalf("EncodeCommand failed: %v", err)
	}

	// Apply via FSM
	resInter := fsm1.Apply(&raft.Log{
		Index: 1,
		Term:  1,
		Type:  raft.LogCommand,
		Data:  encodedAcquire,
	})

	res, ok := resInter.(lockstate.Result)
	if !ok {
		t.Fatalf("expected lockstate.Result, got %T", resInter)
	}
	if !res.Success {
		t.Fatalf("expected successful acquire, got %v", res.Message)
	}
	if res.FencingToken == 0 {
		t.Fatalf("expected positive fencing token, got 0")
	}

	// Apply Renew via FSM
	cmdRenew := lockstate.RenewLockCommand{
		RequestID:         "req-fsm-2",
		ResourceName:      "res-fsm-alpha",
		OwnerID:           "node-fsm",
		FencingToken:      res.FencingToken,
		TTLNanoseconds:    (15 * time.Second).Nanoseconds(),
		TimestampUnixNano: 2000000,
	}
	encodedRenew, err := consensus.EncodeCommand(cmdRenew)
	if err != nil {
		t.Fatalf("EncodeCommand failed: %v", err)
	}

	resInterRenew := fsm1.Apply(&raft.Log{
		Index: 2,
		Term:  1,
		Type:  raft.LogCommand,
		Data:  encodedRenew,
	})
	resRenew := resInterRenew.(lockstate.Result)
	if !resRenew.Success {
		t.Fatalf("expected successful renew, got %v", resRenew.Message)
	}
	if resRenew.LeaseVersion != 2 {
		t.Fatalf("expected lease version 2, got %d", resRenew.LeaseVersion)
	}

	// Create snapshot
	snapObj, err := fsm1.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}

	sink := &mockSnapshotSink{}
	if err := snapObj.Persist(sink); err != nil {
		t.Fatalf("Persist failed: %v", err)
	}
	snapObj.Release()

	if !sink.closed {
		t.Fatalf("expected sink to be closed")
	}
	if sink.cancelled {
		t.Fatalf("expected sink not to be cancelled")
	}

	// Restore into fresh FSM
	sm2 := lockstate.New()
	fsm2 := consensus.NewLockFSM(sm2, nil)

	readCloser := io.NopCloser(&sink.Buffer)
	if err := fsm2.Restore(readCloser); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	// Check restored state in sm2
	rs, found := sm2.GetResourceState("res-fsm-alpha")
	if !found {
		t.Fatalf("expected res-fsm-alpha to be found")
	}
	if rs.ActiveLock == nil {
		t.Fatalf("expected active lock to be non-nil")
	}
	if rs.ActiveLock.LeaseVersion != 2 {
		t.Fatalf("expected lease version 2, got %d", rs.ActiveLock.LeaseVersion)
	}
	if rs.ActiveLock.FencingToken != res.FencingToken {
		t.Fatalf("expected fencing token %d, got %d", res.FencingToken, rs.ActiveLock.FencingToken)
	}
}
