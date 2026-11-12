package lockstate

import "strings"

// GetLock retrieves an active, unexpired lock record for a resource.
func (s *MemoryStateMachine) GetLock(resourceName string) (LockRecord, bool) {
	return s.GetLockAt(resourceName, 0)
}

// GetLockAt retrieves an active, unexpired lock record for a resource at an explicit timestamp.
// When nowUnixNano > 0, leases whose expiration is at or before nowUnixNano are treated as expired.
func (s *MemoryStateMachine) GetLockAt(resourceName string, nowUnixNano int64) (LockRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rs, exists := s.resources[strings.TrimSpace(resourceName)]
	if !exists || rs.ActiveLock == nil {
		return LockRecord{}, false
	}

	if isLeaseExpired(rs.ActiveLock, s.lastAppliedUnixNano, nowUnixNano) {
		return LockRecord{}, false
	}

	return rs.ActiveLock.Clone(), true
}

// ListLocks returns a detached snapshot slice of all currently active locks.
func (s *MemoryStateMachine) ListLocks() []LockRecord {
	return s.ListLocksAt(0, false)
}

// ListLocksAt returns a detached snapshot slice of active locks evaluated at nowUnixNano.
// When includeExpired is true, leases elapsed by logical or wall clock time are included with Status=EXPIRED.
func (s *MemoryStateMachine) ListLocksAt(nowUnixNano int64, includeExpired bool) []LockRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	locks := make([]LockRecord, 0, len(s.resources))
	for _, rs := range s.resources {
		if rs.ActiveLock == nil {
			continue
		}
		rec := rs.ActiveLock.Clone()
		if isLeaseExpired(rs.ActiveLock, s.lastAppliedUnixNano, nowUnixNano) {
			if !includeExpired {
				continue
			}
			rec.Status = LockStatusExpired
		}
		locks = append(locks, rec)
	}
	return locks
}

// ListExpiredLocksAt returns active lock records whose lease has elapsed on or before nowUnixNano.
func (s *MemoryStateMachine) ListExpiredLocksAt(nowUnixNano int64) []LockRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	expired := make([]LockRecord, 0)
	for _, rs := range s.resources {
		if rs.ActiveLock == nil || rs.ActiveLock.Status != LockStatusHeld {
			continue
		}
		if isLeaseExpired(rs.ActiveLock, s.lastAppliedUnixNano, nowUnixNano) {
			expired = append(expired, rs.ActiveLock.Clone())
		}
	}
	return expired
}

func isLeaseExpired(lock *LockRecord, lastAppliedUnixNano, nowUnixNano int64) bool {
	if lock == nil {
		return true
	}
	if lastAppliedUnixNano >= lock.ExpiresAtUnixNano {
		return true
	}
	if nowUnixNano > 0 && nowUnixNano >= lock.ExpiresAtUnixNano {
		return true
	}
	return false
}

// GetResourceState returns a detached copy of the complete state of a resource.
func (s *MemoryStateMachine) GetResourceState(resourceName string) (ResourceState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rs, exists := s.resources[strings.TrimSpace(resourceName)]
	if !exists {
		return ResourceState{}, false
	}

	cloned := rs.Clone()
	if cloned.ActiveLock != nil && s.lastAppliedUnixNano >= cloned.ActiveLock.ExpiresAtUnixNano {
		cloned.ActiveLock.Status = LockStatusExpired
	}
	return cloned, true
}

// LastAppliedTime returns the latest monotonic logical timestamp applied.
func (s *MemoryStateMachine) LastAppliedTime() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastAppliedUnixNano
}
