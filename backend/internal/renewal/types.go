package renewal

import (
	"context"
	"time"
)

// LockRecord holds lease metadata returned by coordinator operations.
type LockRecord struct {
	ResourceName       string `json:"resource_name"`
	OwnerID            string `json:"owner_id"`
	FencingToken       uint64 `json:"fencing_token"`
	LeaseVersion       uint64 `json:"lease_version"`
	AcquiredAtUnixNano int64  `json:"acquired_at_unix_nano"`
	ExpiresAtUnixNano  int64  `json:"expires_at_unix_nano"`
	TTLNanoseconds     int64  `json:"ttl_nanoseconds"`
}

// RenewLockRequest models renewal parameters sent to the coordinator or gateway.
type RenewLockRequest struct {
	RequestID         string `json:"request_id"`
	ResourceName      string `json:"resource_name"`
	OwnerID           string `json:"owner_id"`
	FencingToken      uint64 `json:"fencing_token"`
	TTLSeconds        int64  `json:"ttl_seconds,omitempty"`
	TTLNanoseconds    int64  `json:"ttl_nanoseconds,omitempty"`
	TimestampUnixNano int64  `json:"timestamp_unix_nano,omitempty"`
}

// RenewLockResponse models the result of a renewal attempt.
type RenewLockResponse struct {
	Success bool        `json:"success"`
	Code    string      `json:"code,omitempty"`
	Message string      `json:"message,omitempty"`
	Record  *LockRecord `json:"record,omitempty"`
}

// LockClient is an interface for performing lock operations (implemented via HTTP or direct coordinator client).
type LockClient interface {
	RenewLock(ctx context.Context, req RenewLockRequest) (*RenewLockResponse, error)
	GetLock(ctx context.Context, resourceName string) (*LockRecord, bool, error)
}

// RenewalStatus tracks the real-time state of a renewal worker.
type RenewalStatus struct {
	ResourceName      string    `json:"resource_name"`
	OwnerID           string    `json:"owner_id"`
	ClientID          string    `json:"client_id"`
	SessionID         string    `json:"session_id"`
	FencingToken      uint64    `json:"fencing_token"`
	LeaseVersion      uint64    `json:"lease_version"`
	ExpiresAtUnixNano int64     `json:"expires_at_unix_nano"`
	NextRenewalAt     time.Time `json:"next_renewal_at"`
	Active            bool      `json:"active"`
	LastAttemptTime   time.Time `json:"last_attempt_time"`
	LastAttemptStatus string    `json:"last_attempt_status"`
	LastError         string    `json:"last_error,omitempty"`
	AttemptCount      int64     `json:"attempt_count"`
}

