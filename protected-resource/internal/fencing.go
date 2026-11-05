package internal

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

var (
	ErrStaleFencingToken = errors.New("stale fencing token: token is lower than highest accepted token for this resource")
	ErrOwnerMismatch     = errors.New("fencing token owner mismatch: active token was established by another owner")
	ErrInvalidToken      = errors.New("invalid fencing token: token must be strictly positive")
	ErrEmptyResource     = errors.New("resource name cannot be empty")
	ErrEmptyOwner        = errors.New("owner ID cannot be empty")
)

// ResourceFencingState holds the accepted fencing token and owner identity for a resource.
type ResourceFencingState struct {
	HighestToken uint64 `json:"highest_token"`
	OwnerID      string `json:"owner_id"`
}

// FencingGuard maintains the highest accepted fencing token and owner identity per resource.
//
// PRECISE SEMANTICS:
// 1. Token Monotonicity:
//    - If token < HighestToken: The request belongs to a superseded lease epoch. REJECT unconditionally.
// 2. Same-Lease Repeated Operations:
//    - If token == HighestToken:
//      - If ownerID == state.OwnerID: Legitimate repeated write within the active lease. ACCEPT.
//      - If ownerID != state.OwnerID: Token hijack or cross-owner conflict attempt. REJECT.
// 3. New Lease Epoch:
//    - If token > HighestToken:
//      - Advances HighestToken to token and assigns OwnerID to the new leaseholder. ACCEPT.
//      - Once advanced, older tokens (< token) are permanently invalidated for ALL owners,
//        ensuring a stale owner can never write after a newer fencing token has been accepted.
// 4. Atomicity & Thread Safety:
//    - All state inspections and updates occur under an exclusive mutex lock, guaranteeing
//      atomic transitions even under high concurrency.
type FencingGuard struct {
	mu     sync.RWMutex
	states map[string]ResourceFencingState
}

// NewFencingGuard initializes a new FencingGuard instance.
func NewFencingGuard() *FencingGuard {
	return &FencingGuard{
		states: make(map[string]ResourceFencingState),
	}
}

// ValidateAndApply checks the incoming fencing token and owner against the recorded state.
// If valid, state is atomically updated/maintained and nil is returned.
func (f *FencingGuard) ValidateAndApply(resourceName string, token uint64, ownerID string) error {
	trimmedResource := strings.TrimSpace(resourceName)
	if trimmedResource == "" {
		return ErrEmptyResource
	}

	trimmedOwner := strings.TrimSpace(ownerID)
	if trimmedOwner == "" {
		return ErrEmptyOwner
	}

	if token == 0 {
		return ErrInvalidToken
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	current, exists := f.states[trimmedResource]
	if !exists {
		// First lease epoch observed for this resource
		f.states[trimmedResource] = ResourceFencingState{
			HighestToken: token,
			OwnerID:      trimmedOwner,
		}
		return nil
	}

	// Case 1: Token is strictly lower than highest accepted token (Stale epoch)
	if token < current.HighestToken {
		return fmt.Errorf("%w: received %d, highest accepted is %d", ErrStaleFencingToken, token, current.HighestToken)
	}

	// Case 2: Token is identical to current highest accepted token (Same lease epoch)
	if token == current.HighestToken {
		if current.OwnerID != trimmedOwner {
			return fmt.Errorf("%w: token %d was accepted for owner '%s', rejected for '%s'", ErrOwnerMismatch, token, current.OwnerID, trimmedOwner)
		}
		// Same owner, same token -> legitimate repeated write within current active lease
		return nil
	}

	// Case 3: Token is strictly higher than current highest accepted token (Newer lease epoch)
	f.states[trimmedResource] = ResourceFencingState{
		HighestToken: token,
		OwnerID:      trimmedOwner,
	}
	return nil
}

// GetFencingState returns the current fencing state for a resource.
func (f *FencingGuard) GetFencingState(resourceName string) (ResourceFencingState, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	state, ok := f.states[strings.TrimSpace(resourceName)]
	return state, ok
}

// HighestToken returns the highest recorded fencing token for a resource (for backwards compatibility).
func (f *FencingGuard) HighestToken(resourceName string) (uint64, bool) {
	state, ok := f.GetFencingState(resourceName)
	if !ok {
		return 0, false
	}
	return state.HighestToken, true
}
