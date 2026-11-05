package lockstate

import "strings"

// applyRelease executes an explicit lock release command deterministically.
// Must be called with s.mu held.
func (s *MemoryStateMachine) applyRelease(cmd ReleaseLockCommand, effectiveNow int64) Result {
	resName := strings.TrimSpace(cmd.ResourceName)
	ownerID := strings.TrimSpace(cmd.OwnerID)

	rs, exists := s.resources[resName]
	if !exists || rs.ActiveLock == nil {
		return NewErrorResult(cmd, ErrLockNotFound)
	}

	active := rs.ActiveLock

	// 1. Verify owner identity
	if active.OwnerID != ownerID {
		return NewErrorResult(cmd, ErrNotOwner)
	}

	// 2. Verify fencing token matches active epoch
	if active.FencingToken != cmd.FencingToken {
		return NewErrorResult(cmd, ErrFencingTokenMismatch)
	}

	// 3. If the lease already expired prior to explicit release arrival, fail closed without mutating state
	if effectiveNow >= active.ExpiresAtUnixNano {
		return NewErrorResult(cmd, ErrLeaseExpired)
	}

	// 4. Clear active ownership while strictly preserving HighestToken
	releasedToken := active.FencingToken
	rs.ActiveLock = nil

	event := &TransitionEvent{
		Type:              TransitionReleased,
		ResourceName:      resName,
		OwnerID:           ownerID,
		FencingToken:      releasedToken,
		TimestampUnixNano: effectiveNow,
	}

	return Result{
		Success:           true,
		Code:              "OK",
		Message:           "lock released successfully",
		ResourceName:      resName,
		OwnerID:           ownerID,
		FencingToken:      releasedToken,
		TimestampUnixNano: effectiveNow,
		Event:             event,
	}
}
