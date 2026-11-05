package heartbeat

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"lockguard/backend/internal/config"
	"lockguard/backend/internal/metrics"
)

var (
	ErrInvalidClientParams   = errors.New("client_id and owner_id cannot be empty")
	ErrSessionNotFound       = errors.New("client session not found")
	ErrSessionExpired        = errors.New("client session has expired due to missed heartbeats")
	ErrSessionMismatch       = errors.New("provided session_id does not match active session")
	ErrOwnerMismatch         = errors.New("client is already registered under a different owner")
	ErrSessionAlreadyActive  = errors.New("client session is already active; use heartbeat or terminate existing session")
)

// SessionExpiredCallback is invoked when a session expires or is closed.
type SessionExpiredCallback func(s *ClientSession)

// Manager manages client registrations, heartbeat pings, and session lifecycle.
type Manager struct {
	mu             sync.RWMutex
	sessions       map[string]*ClientSession
	cleanupTicker  time.Duration
	defaultTTL     time.Duration
	metrics        *metrics.Registry
	onExpiredHooks []SessionExpiredCallback

	stopChan chan struct{}
	wg       sync.WaitGroup
}

// NewManager initializes a new client heartbeat manager.
func NewManager(cfg *config.Config, reg *metrics.Registry) *Manager {
	if reg == nil {
		reg = metrics.DefaultRegistry
	}

	ttl := cfg.Heartbeat.Timeout
	if ttl <= 0 {
		ttl = 10 * time.Second
	}

	cleanup := cfg.Heartbeat.CleanupInterval
	if cleanup <= 0 {
		cleanup = 2 * time.Second
	}

	return &Manager{
		sessions:      make(map[string]*ClientSession),
		cleanupTicker: cleanup,
		defaultTTL:    ttl,
		metrics:       reg,
		stopChan:      make(chan struct{}),
	}
}

// OnSessionExpired registers a hook to be triggered when a session is invalidated or timed out.
func (m *Manager) OnSessionExpired(hook SessionExpiredCallback) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onExpiredHooks = append(m.onExpiredHooks, hook)
}

// Start launches the background expiration scrubber.
func (m *Manager) Start() {
	m.wg.Add(1)
	go m.scrubberLoop()
}

// Stop terminates the background scrubber cleanly.
func (m *Manager) Stop() {
	close(m.stopChan)
	m.wg.Wait()
}

func (m *Manager) scrubberLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(m.cleanupTicker)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopChan:
			return
		case <-ticker.C:
			m.scrubExpiredSessions()
		}
	}
}

func (m *Manager) scrubExpiredSessions() {
	var expired []*ClientSession
	now := time.Now().UTC()

	m.mu.Lock()
	for _, s := range m.sessions {
		s.mu.Lock()
		if s.active && now.After(s.expiresAt) {
			s.active = false
			expired = append(expired, s)
			m.metrics.DecGauge("lockguard_active_client_sessions", nil)
			m.metrics.IncCounter("lockguard_heartbeat_timeouts_total", nil)
		}
		s.mu.Unlock()
	}
	hooks := make([]SessionExpiredCallback, len(m.onExpiredHooks))
	copy(hooks, m.onExpiredHooks)
	m.mu.Unlock()

	for _, s := range expired {
		for _, hook := range hooks {
			hook(s)
		}
	}
}

// Register creates or renews a client session.
func (m *Manager) Register(clientID, ownerID string, ttl time.Duration) (*ClientSession, error) {
	cid := strings.TrimSpace(clientID)
	oid := strings.TrimSpace(ownerID)
	if cid == "" || oid == "" {
		return nil, ErrInvalidClientParams
	}

	if ttl <= 0 {
		ttl = m.defaultTTL
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, found := m.sessions[cid]
	if found {
		if existing.OwnerID() != oid {
			return nil, ErrOwnerMismatch
		}
		// If existing session is still active, reject duplicate registration to prevent token harvesting
		if existing.IsActive() {
			return nil, ErrSessionAlreadyActive
		}
		// If existing was expired/deactivated, recreate new session with fresh token
		m.metrics.IncGauge("lockguard_active_client_sessions", nil)
		token := generateSessionToken()
		s := NewClientSession(cid, oid, token, ttl)
		m.sessions[cid] = s
		return s, nil
	}

	token := generateSessionToken()
	s := NewClientSession(cid, oid, token, ttl)
	m.sessions[cid] = s
	m.metrics.IncGauge("lockguard_active_client_sessions", nil)

	return s, nil
}

// Heartbeat refreshes an active client session.
func (m *Manager) Heartbeat(clientID, sessionID string) (*ClientSession, error) {
	cid := strings.TrimSpace(clientID)
	sid := strings.TrimSpace(sessionID)

	m.mu.RLock()
	s, found := m.sessions[cid]
	m.mu.RUnlock()

	if !found {
		m.metrics.IncCounter("lockguard_heartbeat_received_total", map[string]string{"status": "not_found"})
		return nil, ErrSessionNotFound
	}

	if s.SessionID() != sid {
		m.metrics.IncCounter("lockguard_heartbeat_received_total", map[string]string{"status": "mismatch"})
		return nil, ErrSessionMismatch
	}

	if !s.IsActive() {
		m.metrics.IncCounter("lockguard_heartbeat_received_total", map[string]string{"status": "expired"})
		return nil, ErrSessionExpired
	}

	s.Refresh()
	m.metrics.IncCounter("lockguard_heartbeat_received_total", map[string]string{"status": "ok"})
	return s, nil
}

// AttachLock associates a lock resource with a client session.
func (m *Manager) AttachLock(clientID, sessionID, resourceName string) error {
	s, err := m.Heartbeat(clientID, sessionID)
	if err != nil {
		return err
	}
	s.AddLock(resourceName)
	return nil
}

// DetachLock removes a lock resource association from a client session.
func (m *Manager) DetachLock(clientID, sessionID, resourceName string) error {
	m.mu.RLock()
	s, found := m.sessions[clientID]
	m.mu.RUnlock()

	if !found {
		return ErrSessionNotFound
	}
	if s.SessionID() != sessionID {
		return ErrSessionMismatch
	}

	s.RemoveLock(resourceName)
	return nil
}

// CloseSession closes a client session and notifies expiry callbacks.
func (m *Manager) CloseSession(clientID, sessionID string) error {
	cid := strings.TrimSpace(clientID)
	sid := strings.TrimSpace(sessionID)

	m.mu.Lock()
	s, found := m.sessions[cid]
	if !found {
		m.mu.Unlock()
		return ErrSessionNotFound
	}
	if s.SessionID() != sid {
		m.mu.Unlock()
		return ErrSessionMismatch
	}

	wasActive := s.IsActive()
	s.Deactivate()
	if wasActive {
		m.metrics.DecGauge("lockguard_active_client_sessions", nil)
	}

	hooks := make([]SessionExpiredCallback, len(m.onExpiredHooks))
	copy(hooks, m.onExpiredHooks)
	m.mu.Unlock()

	for _, hook := range hooks {
		hook(s)
	}

	return nil
}

// GetSession returns a session by clientID.
func (m *Manager) GetSession(clientID string) (*ClientSession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, found := m.sessions[strings.TrimSpace(clientID)]
	return s, found
}

// ListSessions returns snapshots of all registered sessions.
func (m *Manager) ListSessions() []SessionSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	res := make([]SessionSnapshot, 0, len(m.sessions))
	for _, s := range m.sessions {
		res = append(res, s.Snapshot())
	}
	return res
}

func generateSessionToken() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return fmt.Sprintf("sess-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
