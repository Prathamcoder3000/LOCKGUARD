package internal

import (
	"errors"
	"sync"
)

var (
	ErrStaleFencingToken = errors.New("stale fencing token: token must be strictly greater than highest observed token")
)

// FencingGuard maintains the highest observed fencing token for each resource.
// INVARIANT: Martin Kleppmann's fencing token algorithm:
// The storage/resource rejects any write whose fencing token <= highest token already seen.
type FencingGuard struct {
	mu           sync.RWMutex
	highestToken map[string]uint64
}

// NewFencingGuard initializes a new FencingGuard instance.
func NewFencingGuard() *FencingGuard {
	return &FencingGuard{
		highestToken: make(map[string]uint64),
	}
}

// ValidateAndApply checks if the provided token is strictly greater than the highest seen token.
// If valid, the highest token is updated atomically and nil error is returned.
func (f *FencingGuard) ValidateAndApply(resourceName string, token uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	current, exists := f.highestToken[resourceName]
	if exists && token <= current {
		return ErrStaleFencingToken
	}

	f.highestToken[resourceName] = token
	return nil
}

// HighestToken returns the highest recorded fencing token for a resource.
func (f *FencingGuard) HighestToken(resourceName string) (uint64, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	token, ok := f.highestToken[resourceName]
	return token, ok
}
