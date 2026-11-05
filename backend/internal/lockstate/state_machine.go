package lockstate

import (
	"sync"
)

// StateMachine defines the primary contract for the deterministic lock state machine.
type StateMachine interface {
	// Apply processes a state-changing command deterministically and returns a typed Result.
	Apply(cmd Command) Result

	// GetLock retrieves an active lock record for a resource, returning false if unlocked.
	GetLock(resourceName string) (LockRecord, bool)

	// ListLocks returns a snapshot slice of all active lock records across resources.
	ListLocks() []LockRecord

	// GetResourceState returns the full consensus state (active lock + highest fencing token) for a resource.
	GetResourceState(resourceName string) (ResourceState, bool)

	// LastAppliedTime returns the latest monotonic logical timestamp applied to the state machine.
	LastAppliedTime() int64
}

// MemoryStateMachine is a concurrency-safe, deterministic in-memory implementation of StateMachine.
// ARCHITECTURAL GUARANTEE:
// 1. Fully deterministic: identical initial state + identical command sequence => identical final state.
// 2. No independent wall-clock calls: uses effective logical timestamp from commands.
// 3. Concurrency-safe: serialized mutations via sync.RWMutex.
// 4. Zero external consensus dependencies in Phase 1A.
type MemoryStateMachine struct {
	mu                  sync.RWMutex
	resources           map[string]*ResourceState
	idempotency         map[string]IdempotencyRecord
	lastAppliedUnixNano int64
}

// New creates a freshly initialized, deterministic lock state machine.
func New() *MemoryStateMachine {
	return &MemoryStateMachine{
		resources:   make(map[string]*ResourceState),
		idempotency: make(map[string]IdempotencyRecord),
	}
}

// Apply executes a command deterministically under mutual exclusion.
func (s *MemoryStateMachine) Apply(cmd Command) Result {
	if cmd == nil {
		return NewErrorResult(nil, ErrInvalidCommand)
	}

	// 1. Syntactic validation of command fields
	if err := cmd.Validate(); err != nil {
		return NewErrorResult(cmd, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	reqID := cmd.GetRequestID()
	fingerprint := cmd.Fingerprint()

	// 2. Request-level idempotency check
	if record, found := s.idempotency[reqID]; found {
		if record.Fingerprint == fingerprint {
			// Duplicate identical request: return cached original result
			return record.Result.Clone()
		}
		// Duplicate RequestID with differing payload: return idempotency conflict
		return NewErrorResult(cmd, ErrIdempotencyConflict)
	}

	// 3. Monotonic logical time calculation
	// Policy: effectiveNow = max(cmd.TimestampUnixNano, s.lastAppliedUnixNano)
	effectiveNow := cmd.GetTimestampUnixNano()
	if effectiveNow < s.lastAppliedUnixNano {
		effectiveNow = s.lastAppliedUnixNano
	} else {
		s.lastAppliedUnixNano = effectiveNow
	}

	// 4. Command dispatch
	var result Result
	switch typedCmd := cmd.(type) {
	case AcquireLockCommand:
		result = s.applyAcquire(typedCmd, effectiveNow)
	case RenewLockCommand:
		result = s.applyRenew(typedCmd, effectiveNow)
	case ReleaseLockCommand:
		result = s.applyRelease(typedCmd, effectiveNow)
	case ExpireLockCommand:
		result = s.applyExpire(typedCmd, effectiveNow)
	default:
		result = NewErrorResult(cmd, ErrInvalidCommand)
	}

	// 5. Record idempotency entry
	s.idempotency[reqID] = IdempotencyRecord{
		RequestID:         reqID,
		Fingerprint:       fingerprint,
		Result:            result.Clone(),
		TimestampUnixNano: effectiveNow,
	}

	return result
}
