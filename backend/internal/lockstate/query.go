package lockstate

import "strings"

// GetLock retrieves an active, unexpired lock record for a resource.
func (s *MemoryStateMachine) GetLock(resourceName string) (LockRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rs, exists := s.resources[strings.TrimSpace(resourceName)]
	if !exists || rs.ActiveLock == nil {
		return LockRecord{}, false
	}

	if s.lastAppliedUnixNano >= rs.ActiveLock.ExpiresAtUnixNano {
		return LockRecord{}, false
	}

	return rs.ActiveLock.Clone(), true
}

// ListLocks returns a detached snapshot slice of all currently active locks.
func (s *MemoryStateMachine) ListLocks() []LockRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	locks := make([]LockRecord, 0, len(s.resources))
	for _, rs := range s.resources {
		if rs.ActiveLock != nil && s.lastAppliedUnixNano < rs.ActiveLock.ExpiresAtUnixNano {
			locks = append(locks, rs.ActiveLock.Clone())
		}
	}
	return locks
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
