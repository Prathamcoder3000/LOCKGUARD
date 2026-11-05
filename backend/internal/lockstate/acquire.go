package lockstate

import (
	"math"
	"strings"
)

// applyAcquire executes an acquire command deterministically.
// Must be called with s.mu held.
func (s *MemoryStateMachine) applyAcquire(cmd AcquireLockCommand, effectiveNow int64) Result {
	resName := strings.TrimSpace(cmd.ResourceName)
	ownerID := strings.TrimSpace(cmd.OwnerID)

	rs, exists := s.resources[resName]

	// 1. Verify mutual exclusion if an active lock currently exists
	if exists && rs.ActiveLock != nil {
		if effectiveNow < rs.ActiveLock.ExpiresAtUnixNano {
			// Lock is actively held and unexpired.
			// Deterministically reject with ErrLockAlreadyHeld (both for competing owners
			// and for the same owner attempting duplicate acquisition without matching RequestID).
			return NewErrorResult(cmd, ErrLockAlreadyHeld)
		}
		// If effectiveNow >= rs.ActiveLock.ExpiresAtUnixNano, the active lock has expired.
		// The state machine atomically transitions to a new ownership epoch below.
	}

	// 2. Check for fencing token counter overflow
	var currentHighest uint64
	if exists {
		currentHighest = rs.HighestToken
	}
	if currentHighest == math.MaxUint64 {
		return NewErrorResult(cmd, ErrTokenExhausted)
	}

	// 3. Check for arithmetic overflow on effectiveNow + TTL
	if effectiveNow > math.MaxInt64-cmd.TTLNanoseconds {
		return NewErrorResult(cmd, ErrArithmeticOverflow)
	}

	// 4. All checks passed: apply state changes atomically.
	// Invariant: A newly acquired ownership epoch receives a new fencing token and starts LeaseVersion = 1.
	nextToken := currentHighest + 1
	expiresAt := effectiveNow + cmd.TTLNanoseconds

	if !exists {
		rs = &ResourceState{
			ResourceName: resName,
			HighestToken: nextToken,
			ActiveLock:   nil,
		}
		s.resources[resName] = rs
	} else {
		rs.HighestToken = nextToken
	}

	newLock := &LockRecord{
		ResourceName:       resName,
		OwnerID:            ownerID,
		FencingToken:       nextToken,
		LeaseVersion:       1,
		AcquiredAtUnixNano: effectiveNow,
		RenewedAtUnixNano:  effectiveNow,
		ExpiresAtUnixNano:  expiresAt,
		Status:             LockStatusHeld,
	}
	rs.ActiveLock = newLock

	event := &TransitionEvent{
		Type:              TransitionAcquired,
		ResourceName:      resName,
		OwnerID:           ownerID,
		FencingToken:      nextToken,
		LeaseVersion:      1,
		TimestampUnixNano: effectiveNow,
		ExpiresAtUnixNano: expiresAt,
	}

	return Result{
		Success:            true,
		Code:               "OK",
		Message:            "lock acquired successfully",
		ResourceName:       resName,
		OwnerID:            ownerID,
		FencingToken:       nextToken,
		LeaseVersion:       1,
		AcquiredAtUnixNano: effectiveNow,
		ExpiresAtUnixNano:  expiresAt,
		TimestampUnixNano:  effectiveNow,
		Event:              event,
	}
}
