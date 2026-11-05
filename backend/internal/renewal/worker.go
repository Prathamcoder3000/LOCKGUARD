package renewal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"sync"
	"time"

	"lockguard/backend/internal/metrics"
)

type renewalWorker struct {
	mu sync.RWMutex

	resourceName      string
	ownerID           string
	clientID          string
	sessionID         string
	fencingToken      uint64
	leaseVersion      uint64
	expiresAtUnixNano int64
	ttl               time.Duration
	safetyMargin      time.Duration
	retryInitial      time.Duration
	retryMax          time.Duration
	maxAttempts       int

	client             LockClient
	isSessionActive    func(clientID, sessionID string) bool
	metrics            *metrics.Registry
	onWorkerTerminated func(resourceName string, w *renewalWorker)

	ctx       context.Context
	cancel    context.CancelFunc
	status    RenewalStatus
	stopCh    chan struct{}
	closeOnce sync.Once
}

func newRenewalWorker(
	ctx context.Context,
	resourceName, ownerID, clientID, sessionID string,
	fencingToken, leaseVersion uint64,
	expiresAtUnixNano int64,
	ttl, safetyMargin, retryInitial, retryMax time.Duration,
	maxAttempts int,
	client LockClient,
	isSessionActive func(clientID, sessionID string) bool,
	reg *metrics.Registry,
	onTerminated func(resourceName string, w *renewalWorker),
) *renewalWorker {
	workerCtx, cancel := context.WithCancel(ctx)
	if reg == nil {
		reg = metrics.DefaultRegistry
	}
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	if retryInitial <= 0 {
		retryInitial = 100 * time.Millisecond
	}
	if retryMax <= 0 {
		retryMax = 2 * time.Second
	}

	w := &renewalWorker{
		resourceName:       resourceName,
		ownerID:            ownerID,
		clientID:           clientID,
		sessionID:          sessionID,
		fencingToken:       fencingToken,
		leaseVersion:       leaseVersion,
		expiresAtUnixNano:  expiresAtUnixNano,
		ttl:                ttl,
		safetyMargin:       safetyMargin,
		retryInitial:       retryInitial,
		retryMax:           retryMax,
		maxAttempts:        maxAttempts,
		client:             client,
		isSessionActive:    isSessionActive,
		metrics:            reg,
		onWorkerTerminated: onTerminated,
		ctx:                workerCtx,
		cancel:             cancel,
		stopCh:             make(chan struct{}),
	}

	w.status = RenewalStatus{
		ResourceName:      resourceName,
		OwnerID:           ownerID,
		ClientID:          clientID,
		SessionID:         sessionID,
		FencingToken:      fencingToken,
		LeaseVersion:      leaseVersion,
		ExpiresAtUnixNano: expiresAtUnixNano,
		Active:            true,
	}

	return w
}

func (w *renewalWorker) Start() {
	go w.run()
}

func (w *renewalWorker) Stop() {
	w.closeOnce.Do(func() {
		w.cancel()
		close(w.stopCh)
		w.mu.Lock()
		w.status.Active = false
		w.mu.Unlock()
	})
}

func (w *renewalWorker) Status() RenewalStatus {
	w.mu.RLock()
	defer w.mu.RUnlock()
	st := w.status
	return st
}

func (w *renewalWorker) run() {
	defer func() {
		w.mu.Lock()
		w.status.Active = false
		w.mu.Unlock()
		if w.onWorkerTerminated != nil {
			w.onWorkerTerminated(w.resourceName, w)
		}
	}()

	for {
		w.mu.RLock()
		expiresAt := time.Unix(0, w.expiresAtUnixNano)
		ttl := w.ttl
		safety := w.safetyMargin
		w.mu.RUnlock()

		// T_renew = ExpiresAt - TTL/3 - SafetyMargin
		renewWindow := ttl / 3
		renewAt := expiresAt.Add(-renewWindow).Add(-safety)
		waitDuration := time.Until(renewAt)

		w.mu.Lock()
		w.status.NextRenewalAt = renewAt
		w.mu.Unlock()

		if waitDuration > 0 {
			select {
			case <-w.ctx.Done():
				return
			case <-w.stopCh:
				return
			case <-time.After(waitDuration):
			}
		}

		// Check if worker stopped
		select {
		case <-w.ctx.Done():
			return
		case <-w.stopCh:
			return
		default:
		}

		// 1. Session liveness check
		if w.isSessionActive != nil && !w.isSessionActive(w.clientID, w.sessionID) {
			w.mu.Lock()
			w.status.LastAttemptStatus = "cancelled_session_inactive"
			w.status.LastError = "client session expired or closed"
			w.mu.Unlock()
			return
		}

		// 2. Perform Renewal with bounded retry and idempotency preservation
		success := w.executeRenewalWithRetry()
		if !success {
			return
		}
	}
}

func (w *renewalWorker) executeRenewalWithRetry() bool {
	w.mu.Lock()
	fencingToken := w.fencingToken
	currVersion := w.leaseVersion
	resName := w.resourceName
	ownerID := w.ownerID
	ttlNano := w.ttl.Nanoseconds()
	w.status.AttemptCount++
	w.status.LastAttemptTime = time.Now().UTC()
	w.mu.Unlock()

	// Use stable requestID across ambiguous timeout retries to leverage FSM idempotency
	requestID := fmt.Sprintf("renew-%s-%d-%s", resName, currVersion+1, generateRandomHex(8))

	var lastErr error
	for attempt := 1; attempt <= w.maxAttempts; attempt++ {
		select {
		case <-w.ctx.Done():
			return false
		case <-w.stopCh:
			return false
		default:
		}

		// Double-check session is still alive before each retry
		if w.isSessionActive != nil && !w.isSessionActive(w.clientID, w.sessionID) {
			w.mu.Lock()
			w.status.LastAttemptStatus = "cancelled_session_inactive"
			w.status.LastError = "client session expired or closed during retry"
			w.mu.Unlock()
			return false
		}

		req := RenewLockRequest{
			RequestID:         requestID,
			ResourceName:      resName,
			OwnerID:           ownerID,
			FencingToken:      fencingToken,
			TTLNanoseconds:    ttlNano,
			TimestampUnixNano: time.Now().UnixNano(),
		}

		resp, err := w.client.RenewLock(w.ctx, req)
		if err != nil {
			// Network error or timeout (ambiguous outcome)
			lastErr = err
			w.metrics.IncCounter("lockguard_renewal_attempts_total", map[string]string{"status": "network_timeout"})

			if attempt < w.maxAttempts {
				backoff := calculateBackoff(attempt, w.retryInitial, w.retryMax)
				select {
				case <-w.ctx.Done():
					return false
				case <-w.stopCh:
					return false
				case <-time.After(backoff):
					continue
				}
			}
			break
		}

		if resp.Success && resp.Record != nil {
			// Invariant verification:
			// 1. Fencing token MUST remain unchanged
			if resp.Record.FencingToken != fencingToken {
				w.mu.Lock()
				w.status.LastAttemptStatus = "fatal_token_mismatch"
				w.status.LastError = fmt.Sprintf("fencing token changed from %d to %d", fencingToken, resp.Record.FencingToken)
				w.mu.Unlock()
				return false
			}

			// 2. LeaseVersion must be incremented by EXACTLY 1: newLeaseVersion == currVersion + 1
			expectedVersion := currVersion + 1
			if resp.Record.LeaseVersion != expectedVersion {
				// Must not accept version less than or equal to current version
				if resp.Record.LeaseVersion <= currVersion {
					w.mu.Lock()
					w.status.LastAttemptStatus = "fatal_version_not_incremented"
					w.status.LastError = fmt.Sprintf("lease version %d did not increment past %d", resp.Record.LeaseVersion, currVersion)
					w.mu.Unlock()
					return false
				}

				// If response contains a version greater than the exact expected version:
				// Treat as a possible concurrent or stale operation and reconcile with coordinator
				w.mu.Lock()
				w.status.LastAttemptStatus = "reconciling_version_advancement"
				w.status.LastError = fmt.Sprintf("lease version advanced unexpectedly to %d (expected %d); reconciling with coordinator", resp.Record.LeaseVersion, expectedVersion)
				w.mu.Unlock()

				currentLock, found, getErr := w.client.GetLock(w.ctx, resName)
				if getErr != nil || !found || currentLock == nil {
					w.mu.Lock()
					w.status.LastAttemptStatus = "fatal_unexpected_version_advance"
					w.status.LastError = fmt.Sprintf("lock missing or query failed during reconciliation: %v", getErr)
					w.mu.Unlock()
					return false
				}

				if currentLock.OwnerID != ownerID || currentLock.FencingToken != fencingToken {
					w.mu.Lock()
					w.status.LastAttemptStatus = "fatal_superseded_version_advance"
					w.status.LastError = fmt.Sprintf("lock superseded during unexpected version advance: held by %s with token %d", currentLock.OwnerID, currentLock.FencingToken)
					w.mu.Unlock()
					return false
				}

				// Lock held by same owner and token, but advanced past expected: another worker or process renewed concurrently. Fail closed and terminate.
				w.mu.Lock()
				w.status.LastAttemptStatus = "fatal_unexpected_version_advance"
				w.status.LastError = fmt.Sprintf("lock state confirmed unexpected version advance to %d (expected %d); failing closed", currentLock.LeaseVersion, expectedVersion)
				w.mu.Unlock()
				return false
			}

			// Update worker state for next cycle
			w.mu.Lock()
			w.leaseVersion = resp.Record.LeaseVersion
			w.expiresAtUnixNano = resp.Record.ExpiresAtUnixNano
			w.status.LeaseVersion = resp.Record.LeaseVersion
			w.status.ExpiresAtUnixNano = resp.Record.ExpiresAtUnixNano
			w.status.LastAttemptStatus = "success"
			w.status.LastError = ""
			w.mu.Unlock()

			w.metrics.IncCounter("lockguard_renewal_attempts_total", map[string]string{"status": "success"})
			return true
		}

		// Non-success response from coordinator
		w.metrics.IncCounter("lockguard_renewal_attempts_total", map[string]string{"status": "failure"})

		// Check for permanent failures
		code := resp.Code
		if isPermanentFailure(code) {
			w.mu.Lock()
			w.status.LastAttemptStatus = "permanent_failure"
			w.status.LastError = fmt.Sprintf("coordinator rejected renewal: [%s] %s", resp.Code, resp.Message)
			w.mu.Unlock()
			return false
		}

		// Transient failure (e.g. ERR_NOT_LEADER during failover or temporary quorum unavailable)
		lastErr = fmt.Errorf("transient failure [%s]: %s", resp.Code, resp.Message)
		if attempt < w.maxAttempts {
			backoff := calculateBackoff(attempt, w.retryInitial, w.retryMax)
			select {
			case <-w.ctx.Done():
				return false
			case <-w.stopCh:
				return false
			case <-time.After(backoff):
				continue
			}
		}
	}

	w.mu.Lock()
	w.status.LastAttemptStatus = "retry_exhausted"
	if lastErr != nil {
		w.status.LastError = lastErr.Error()
	}
	w.mu.Unlock()
	return false
}

func isPermanentFailure(code string) bool {
	switch code {
	case "ERR_LOCK_NOT_FOUND", "ERR_NOT_OWNER", "ERR_FENCING_TOKEN_STALE",
		"ERR_LEASE_EXPIRED", "ERR_LEASE_VERSION_MISMATCH", "ERR_BAD_REQUEST",
		"ERR_CONFLICT":
		return true
	default:
		return false
	}
}

func calculateBackoff(attempt int, initial, max time.Duration) time.Duration {
	mult := 1 << (attempt - 1)
	backoff := initial * time.Duration(mult)
	if backoff > max {
		backoff = max
	}
	// Add jitter (0-20% of backoff)
	n, err := rand.Int(rand.Reader, big.NewInt(20))
	if err == nil {
		jitter := time.Duration(n.Int64()) * backoff / 100
		backoff += jitter
	}
	return backoff
}

func generateRandomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

