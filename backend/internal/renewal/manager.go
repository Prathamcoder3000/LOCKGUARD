package renewal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"lockguard/backend/internal/config"
	"lockguard/backend/internal/heartbeat"
	"lockguard/backend/internal/metrics"
)

var (
	ErrInvalidRenewalParams  = errors.New("resource_name, owner_id, and client_id cannot be empty")
	ErrWorkerNotFound        = errors.New("renewal worker not found for resource")
	ErrStaleWorkerGeneration = errors.New("cannot start renewal for stale worker generation")
	ErrUnauthorizedSession   = errors.New("unauthorized: session invalid, inactive, or owner mismatch")
	ErrSessionMismatch       = errors.New("unauthorized: session_id does not match active renewal worker")
	ErrLockNotFound          = errors.New("cannot start renewal: lock not found")
	ErrNotLockOwner          = errors.New("cannot start renewal: lock owned by another client")
	ErrFencingTokenMismatch  = errors.New("cannot start renewal: fencing token does not match active lock generation")
	ErrLeaseExpired          = errors.New("cannot start renewal: lock lease has already expired")
)

// Manager coordinates automated lease renewal workers across distributed locks.
type Manager struct {
	mu        sync.RWMutex
	workers   map[string]*renewalWorker
	client    LockClient
	heartbeat *heartbeat.Manager
	metrics   *metrics.Registry

	safetyMargin time.Duration
	retryInitial time.Duration
	retryMax     time.Duration
	maxAttempts  int

	ctx    context.Context
	cancel context.CancelFunc
}

// NewManager initializes a new lease renewal manager.
func NewManager(cfg *config.Config, client LockClient, hb *heartbeat.Manager, reg *metrics.Registry) *Manager {
	if reg == nil {
		reg = metrics.DefaultRegistry
	}

	ctx, cancel := context.WithCancel(context.Background())

	m := &Manager{
		workers:      make(map[string]*renewalWorker),
		client:       client,
		heartbeat:    hb,
		metrics:      reg,
		safetyMargin: cfg.Renewal.SafetyMargin,
		retryInitial: cfg.Renewal.RetryInitial,
		retryMax:     cfg.Renewal.RetryMax,
		maxAttempts:  cfg.Renewal.MaxAttempts,
		ctx:          ctx,
		cancel:       cancel,
	}

	// Wire heartbeat session expiry to immediately terminate associated renewal workers
	if hb != nil {
		hb.OnSessionExpired(func(s *heartbeat.ClientSession) {
			m.StopAllForSession(s.SessionID())
		})
	}

	return m
}

// StartRenewal launches a single renewal worker for a leased resource.
func (m *Manager) StartRenewal(
	resourceName, ownerID, clientID, sessionID string,
	fencingToken, leaseVersion uint64,
	expiresAtUnixNano int64,
	ttl time.Duration,
) error {
	res := strings.TrimSpace(resourceName)
	owner := strings.TrimSpace(ownerID)
	client := strings.TrimSpace(clientID)

	if res == "" || owner == "" || client == "" {
		return ErrInvalidRenewalParams
	}

	sid := strings.TrimSpace(sessionID)

	// 1. Validate heartbeat session ownership and activity if heartbeat manager is present
	if m.heartbeat != nil {
		if sid == "" {
			return ErrUnauthorizedSession
		}
		s, found := m.heartbeat.GetSession(client)
		if !found || !s.IsActive() || s.SessionID() != sid || s.OwnerID() != owner {
			return ErrUnauthorizedSession
		}
		_ = m.heartbeat.AttachLock(client, sid, res)
	}

	// 2. Validate authoritative current lock state if client is present
	if m.client != nil {
		currentLock, found, err := m.client.GetLock(m.ctx, res)
		if err != nil {
			return fmt.Errorf("failed to verify authoritative lock state: %w", err)
		}
		if !found || currentLock == nil {
			return ErrLockNotFound
		}
		if currentLock.OwnerID != "" && currentLock.OwnerID != owner {
			return ErrNotLockOwner
		}
		if currentLock.FencingToken != 0 && currentLock.FencingToken != fencingToken {
			return ErrFencingTokenMismatch
		}
		if currentLock.LeaseVersion > leaseVersion {
			return ErrStaleWorkerGeneration
		}
		if currentLock.ExpiresAtUnixNano > 0 && currentLock.ExpiresAtUnixNano <= time.Now().UnixNano() {
			return ErrLeaseExpired
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Generation-aware worker replacement and duplicate prevention
	if existing, found := m.workers[res]; found {
		st := existing.Status()
		if st.Active {
			// Reject stale generations where fencing token or lease version is older
			if fencingToken < st.FencingToken {
				return ErrStaleWorkerGeneration
			}
			if fencingToken == st.FencingToken && leaseVersion < st.LeaseVersion {
				return ErrStaleWorkerGeneration
			}
			if fencingToken == st.FencingToken && leaseVersion == st.LeaseVersion && st.OwnerID == owner {
				// Exact duplicate worker already actively managing this generation
				return nil
			}
		}
		// Existing worker is inactive or strictly superseded by newer generation: stop it cleanly
		existing.Stop()
		delete(m.workers, res)
		m.metrics.DecGauge("lockguard_renewals_active", nil)
	}

	isSessionActiveFn := func(cID, sID string) bool {
		if m.heartbeat == nil {
			return true
		}
		s, found := m.heartbeat.GetSession(cID)
		if !found {
			return false
		}
		return s.IsActive() && s.SessionID() == sID
	}

	onTerminated := func(resource string, w *renewalWorker) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if current, ok := m.workers[resource]; ok && current == w {
			delete(m.workers, resource)
			m.metrics.DecGauge("lockguard_renewals_active", nil)
		}
	}

	worker := newRenewalWorker(
		m.ctx,
		res, owner, client, sessionID,
		fencingToken, leaseVersion,
		expiresAtUnixNano,
		ttl, m.safetyMargin, m.retryInitial, m.retryMax,
		m.maxAttempts,
		m.client,
		isSessionActiveFn,
		m.metrics,
		onTerminated,
	)

	m.workers[res] = worker
	m.metrics.IncGauge("lockguard_renewals_active", nil)
	worker.Start()

	return nil
}

// StopRenewal cleanly terminates the renewal worker for a specific resource, verifying session authorization.
func (m *Manager) StopRenewal(resourceName, sessionID string) error {
	res := strings.TrimSpace(resourceName)
	sid := strings.TrimSpace(sessionID)

	m.mu.Lock()
	worker, found := m.workers[res]
	if !found {
		m.mu.Unlock()
		return ErrWorkerNotFound
	}

	// Validate authorization: if worker is protected by a sessionID, caller MUST provide the exact matching sessionID
	if worker.sessionID != "" && worker.sessionID != sid {
		m.mu.Unlock()
		return ErrSessionMismatch
	}

	delete(m.workers, res)
	m.metrics.DecGauge("lockguard_renewals_active", nil)
	m.mu.Unlock()

	worker.Stop()
	return nil
}

// StopAllForSession halts all workers registered to a given session ID.
func (m *Manager) StopAllForSession(sessionID string) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return
	}

	m.mu.Lock()
	var toStop []*renewalWorker
	for res, w := range m.workers {
		if w.sessionID == sid {
			toStop = append(toStop, w)
			delete(m.workers, res)
			m.metrics.DecGauge("lockguard_renewals_active", nil)
		}
	}
	m.mu.Unlock()

	for _, w := range toStop {
		w.Stop()
	}
}

// GetStatus returns the current state of a resource's renewal worker.
func (m *Manager) GetStatus(resourceName string) (RenewalStatus, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	w, found := m.workers[strings.TrimSpace(resourceName)]
	if !found {
		return RenewalStatus{}, false
	}
	return w.Status(), true
}

// ListStatus returns snapshots of all active renewal workers.
func (m *Manager) ListStatus() []RenewalStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	res := make([]RenewalStatus, 0, len(m.workers))
	for _, w := range m.workers {
		res = append(res, w.Status())
	}
	return res
}

// Shutdown stops all renewal workers and cancels the root context.
func (m *Manager) Shutdown() {
	m.cancel()
	m.mu.Lock()
	workers := make([]*renewalWorker, 0, len(m.workers))
	for _, w := range m.workers {
		workers = append(workers, w)
	}
	m.workers = make(map[string]*renewalWorker)
	m.mu.Unlock()

	for _, w := range workers {
		w.Stop()
	}
}

