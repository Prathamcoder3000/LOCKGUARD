package lockstate

import "strings"

// applyExpire executes a deterministic lock expiration command.
// Must be called with s.mu held.
func (s *MemoryStateMachine) applyExpire(cmd ExpireLockCommand, effectiveNow int64) Result {
	resName := strings.TrimSpace(cmd.ResourceName)
	expectedOwner := strings.TrimSpace(cmd.ExpectedOwnerID)

	rs, exists := s.resources[resName]
	if !exists || rs.ActiveLock == nil {
		return NewErrorResult(cmd, ErrLockNotFound)
	}

	active := rs.ActiveLock

	// 1. Prevent old expiration command from expiring a newer owner's lease
	if active.OwnerID != expectedOwner {
		return NewErrorResult(cmd, ErrNotOwner)
	}

	// 2. Prevent expiration with an outdated fencing token
	if active.FencingToken != cmd.ExpectedFencingToken {
		return NewErrorResult(cmd, ErrFencingTokenMismatch)
	}

	// 3. Stale expiration protection: verify expected lease version matches active lease instance
	if active.LeaseVersion != cmd.ExpectedLeaseVersion {
		return NewErrorResult(cmd, ErrLeaseVersionMismatch)
	}

	// 4. Stale expiration protection: verify expected expiration timestamp matches active lease instance
	if active.ExpiresAtUnixNano != cmd.ExpectedExpiresAtUnixNano {
		return NewErrorResult(cmd, ErrLeaseVersionMismatch)
	}

	// 5. Confirm that the lease has actually elapsed according to logical time
	if effectiveNow < active.ExpiresAtUnixNano {
		return NewErrorResult(cmd, ErrInvalidExpiration)
	}

	// 6. Clear active lease while strictly preserving HighestToken history
	expiredToken := active.FencingToken
	expiredVersion := active.LeaseVersion
	rs.ActiveLock = nil

	event := &TransitionEvent{
		Type:              TransitionExpired,
		ResourceName:      resName,
		OwnerID:           expectedOwner,
		FencingToken:      expiredToken,
		LeaseVersion:      expiredVersion,
		TimestampUnixNano: effectiveNow,
	}

	return Result{
		Success:           true,
		Code:              "OK",
		Message:           "lock expired successfully",
		ResourceName:      resName,
		OwnerID:           expectedOwner,
		FencingToken:      expiredToken,
		LeaseVersion:      expiredVersion,
		TimestampUnixNano: effectiveNow,
		Event:             event,
	}
}
