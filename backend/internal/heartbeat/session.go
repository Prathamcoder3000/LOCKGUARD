package heartbeat

import (
	"sync"
	"time"
)

// SessionSnapshot is a concurrency-safe, serializable view of a ClientSession.
type SessionSnapshot struct {
	ClientID        string    `json:"client_id"`
	OwnerID         string    `json:"owner_id"`
	SessionID       string    `json:"session_id"`
	TTLSeconds      int64     `json:"ttl_seconds"`
	CreatedAt       time.Time `json:"created_at"`
	LastHeartbeat   time.Time `json:"last_heartbeat"`
	ExpiresAt       time.Time `json:"expires_at"`
	Active          bool      `json:"active"`
	RegisteredLocks []string  `json:"registered_locks"`
}

// ClientSession manages heartbeats and lock associations for a registered client.
type ClientSession struct {
	mu              sync.RWMutex
	clientID        string
	ownerID         string
	sessionID       string
	ttl             time.Duration
	createdAt       time.Time
	lastHeartbeat   time.Time
	expiresAt       time.Time
	active          bool
	registeredLocks map[string]struct{}
}

// NewClientSession creates a new ClientSession.
func NewClientSession(clientID, ownerID, sessionID string, ttl time.Duration) *ClientSession {
	now := time.Now().UTC()
	return &ClientSession{
		clientID:        clientID,
		ownerID:         ownerID,
		sessionID:       sessionID,
		ttl:             ttl,
		createdAt:       now,
		lastHeartbeat:   now,
		expiresAt:       now.Add(ttl),
		active:          true,
		registeredLocks: make(map[string]struct{}),
	}
}

// ClientID returns the client identifier.
func (s *ClientSession) ClientID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.clientID
}

// OwnerID returns the associated owner identity.
func (s *ClientSession) OwnerID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ownerID
}

// SessionID returns the unique session token.
func (s *ClientSession) SessionID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionID
}

// IsActive returns true if the session has not been closed or expired.
func (s *ClientSession) IsActive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active && time.Now().Before(s.expiresAt)
}

// Refresh extends the lease expiration by the session TTL.
func (s *ClientSession) Refresh() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.lastHeartbeat = now
	s.expiresAt = now.Add(s.ttl)
}

// Deactivate marks the session as inactive.
func (s *ClientSession) Deactivate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = false
}

// AddLock associates a held lock resource with this session.
func (s *ClientSession) AddLock(resourceName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registeredLocks[resourceName] = struct{}{}
}

// RemoveLock removes a held lock resource association.
func (s *ClientSession) RemoveLock(resourceName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.registeredLocks, resourceName)
}

// HasLock checks if a resource is tracked by this session.
func (s *ClientSession) HasLock(resourceName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.registeredLocks[resourceName]
	return exists
}

// Locks returns a copy of all resources registered to this session.
func (s *ClientSession) Locks() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	locks := make([]string, 0, len(s.registeredLocks))
	for l := range s.registeredLocks {
		locks = append(locks, l)
	}
	return locks
}

// Snapshot returns a point-in-time serializable copy of the session state.
func (s *ClientSession) Snapshot() SessionSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	locks := make([]string, 0, len(s.registeredLocks))
	for l := range s.registeredLocks {
		locks = append(locks, l)
	}
	return SessionSnapshot{
		ClientID:        s.clientID,
		OwnerID:         s.ownerID,
		SessionID:       s.sessionID,
		TTLSeconds:      int64(s.ttl.Seconds()),
		CreatedAt:       s.createdAt,
		LastHeartbeat:   s.lastHeartbeat,
		ExpiresAt:       s.expiresAt,
		Active:          s.active && time.Now().Before(s.expiresAt),
		RegisteredLocks: locks,
	}
}

