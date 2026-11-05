package lockstate

import (
	"math"
	"strings"
)

// applyRenew executes a lease renewal command deterministically.
// Must be called with s.mu held.
func (s *MemoryStateMachine) applyRenew(cmd RenewLockCommand, effectiveNow int64) Result {
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

	// 3. Verify lease has not already expired according to effective logical time
	if effectiveNow >= active.ExpiresAtUnixNano {
		return NewErrorResult(cmd, ErrLeaseExpired)
	}

	// 4. Check for arithmetic overflow on effectiveNow + TTL
	if effectiveNow > math.MaxInt64-cmd.TTLNanoseconds {
		return NewErrorResult(cmd, ErrArithmeticOverflow)
	}

	// 5. Check for lease version counter overflow
	if active.LeaseVersion == math.MaxUint64 {
		return NewErrorResult(cmd, ErrVersionExhausted)
	}

	// 6. All checks passed: extend lease.
	// Invariants:
	// - Renewal preserves the fencing token.
	// - Every successful renewal increments LeaseVersion by exactly 1.
	newVersion := active.LeaseVersion + 1
	newExpiresAt := effectiveNow + cmd.TTLNanoseconds

	active.LeaseVersion = newVersion
	active.RenewedAtUnixNano = effectiveNow
	active.ExpiresAtUnixNano = newExpiresAt
	active.Status = LockStatusHeld

	event := &TransitionEvent{
		Type:              TransitionRenewed,
		ResourceName:      resName,
		OwnerID:           ownerID,
		FencingToken:      active.FencingToken,
		LeaseVersion:      newVersion,
		TimestampUnixNano: effectiveNow,
		ExpiresAtUnixNano: newExpiresAt,
	}

	return Result{
		Success:            true,
		Code:               "OK",
		Message:            "lease renewed successfully",
		ResourceName:       resName,
		OwnerID:            ownerID,
		FencingToken:       active.FencingToken,
		LeaseVersion:       newVersion,
		AcquiredAtUnixNano: active.AcquiredAtUnixNano,
		ExpiresAtUnixNano:  newExpiresAt,
		TimestampUnixNano:  effectiveNow,
		Event:              event,
	}
}
