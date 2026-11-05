package consensus

import (
	"fmt"
	"io"
	"log/slog"

	"github.com/hashicorp/raft"
	"lockguard/backend/internal/lockstate"
)

// Ensure LockFSM implements raft.FSM
var _ raft.FSM = (*LockFSM)(nil)

// LockFSM integrates the deterministic Phase 1A MemoryStateMachine into HashiCorp Raft.
type LockFSM struct {
	sm     *lockstate.MemoryStateMachine
	logger *slog.Logger
}

// NewLockFSM creates a new Raft FSM adapter for the deterministic in-memory lock state machine.
func NewLockFSM(sm *lockstate.MemoryStateMachine, logger *slog.Logger) *LockFSM {
	if logger == nil {
		logger = slog.Default()
	}
	return &LockFSM{
		sm:     sm,
		logger: logger,
	}
}

// StateMachine returns the underlying deterministic lock state machine.
func (f *LockFSM) StateMachine() *lockstate.MemoryStateMachine {
	return f.sm
}

// Apply is invoked by HashiCorp Raft when a log entry is committed across quorum.
// It decodes the versioned command envelope and deterministically mutates the state machine.
func (f *LockFSM) Apply(l *raft.Log) interface{} {
	cmd, err := DecodeCommand(l.Data)
	if err != nil {
		f.logger.Error("raft_fsm_decode_failed",
			slog.Uint64("index", l.Index),
			slog.Uint64("term", l.Term),
			slog.String("error", err.Error()),
		)
		return lockstate.NewErrorResult(nil, fmt.Errorf("fsm decode error: %w", err))
	}

	result := f.sm.Apply(cmd)

	f.logger.Debug("raft_fsm_command_applied",
		slog.Uint64("index", l.Index),
		slog.Uint64("term", l.Term),
		slog.String("command_type", string(cmd.Type())),
		slog.String("request_id", cmd.GetRequestID()),
		slog.String("resource", cmd.GetResourceName()),
		slog.Bool("success", result.Success),
		slog.String("code", result.Code),
	)

	return result
}

// Snapshot creates a point-in-time FSMSnapshot for log compaction and follower sync.
func (f *LockFSM) Snapshot() (raft.FSMSnapshot, error) {
	snapData, err := f.sm.ExportSnapshot()
	if err != nil {
		f.logger.Error("raft_fsm_export_snapshot_failed", slog.String("error", err.Error()))
		return nil, fmt.Errorf("failed to export snapshot: %w", err)
	}

	f.logger.Info("raft_fsm_snapshot_created",
		slog.Int("resources_count", len(snapData.Resources)),
		slog.Int("idempotency_count", len(snapData.IdempotencyRecords)),
		slog.Int64("last_applied_nano", snapData.LastAppliedUnixNano),
	)

	return &LockFSMSnapshot{
		snap:   snapData,
		logger: f.logger,
	}, nil
}

// Restore resets the state machine and reconstructs all lock and idempotency state from a snapshot.
func (f *LockFSM) Restore(rc io.ReadCloser) error {
	defer rc.Close()

	snapData, err := lockstate.DecodeSnapshot(rc)
	if err != nil {
		f.logger.Error("raft_fsm_restore_decode_failed", slog.String("error", err.Error()))
		return fmt.Errorf("failed to decode snapshot stream: %w", err)
	}

	if err := f.sm.RestoreSnapshot(snapData); err != nil {
		f.logger.Error("raft_fsm_restore_apply_failed", slog.String("error", err.Error()))
		return fmt.Errorf("failed to apply snapshot state: %w", err)
	}

	f.logger.Info("raft_fsm_restored_from_snapshot",
		slog.Int("resources_count", len(snapData.Resources)),
		slog.Int("idempotency_count", len(snapData.IdempotencyRecords)),
		slog.Int64("last_applied_nano", snapData.LastAppliedUnixNano),
	)

	return nil
}

// Ensure LockFSMSnapshot implements raft.FSMSnapshot
var _ raft.FSMSnapshot = (*LockFSMSnapshot)(nil)

// LockFSMSnapshot holds point-in-time snapshot data ready for persistence to disk.
type LockFSMSnapshot struct {
	snap   *lockstate.SnapshotData
	logger *slog.Logger
}

// Persist writes the snapshot state to the Raft snapshot sink.
func (s *LockFSMSnapshot) Persist(sink raft.SnapshotSink) error {
	if s.snap == nil {
		sink.Cancel()
		return fmt.Errorf("cannot persist nil snapshot")
	}

	if err := s.snap.Encode(sink); err != nil {
		sink.Cancel()
		s.logger.Error("raft_snapshot_persist_failed", slog.String("error", err.Error()))
		return fmt.Errorf("failed to encode snapshot to sink: %w", err)
	}

	if err := sink.Close(); err != nil {
		s.logger.Error("raft_snapshot_sink_close_failed", slog.String("error", err.Error()))
		return fmt.Errorf("failed to close snapshot sink: %w", err)
	}

	s.logger.Info("raft_snapshot_persisted_successfully",
		slog.String("sink_id", sink.ID()),
	)
	return nil
}

// Release drops the reference to the snapshot data.
func (s *LockFSMSnapshot) Release() {
	s.snap = nil
}

