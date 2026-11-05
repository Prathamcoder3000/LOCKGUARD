package lockstate

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
	"strings"
)

// CommandType represents the action requested by a command.
type CommandType string

const (
	CmdAcquireLock CommandType = "ACQUIRE_LOCK"
	CmdRenewLock   CommandType = "RENEW_LOCK"
	CmdReleaseLock CommandType = "RELEASE_LOCK"
	CmdExpireLock  CommandType = "EXPIRE_LOCK"
)

// Command represents a strongly typed state transition request.
type Command interface {
	Type() CommandType
	GetRequestID() string
	GetResourceName() string
	GetTimestampUnixNano() int64
	Fingerprint() [32]byte
	Validate() error
}

// Result models the deterministic output of executing a command on the StateMachine.
type Result struct {
	Success            bool             `json:"success"`
	Error              error            `json:"-"`
	Code               string           `json:"code,omitempty"`
	Message            string           `json:"message,omitempty"`
	ResourceName       string           `json:"resource_name,omitempty"`
	OwnerID            string           `json:"owner_id,omitempty"`
	FencingToken       uint64           `json:"fencing_token,omitempty"`
	LeaseVersion       uint64           `json:"lease_version,omitempty"`
	AcquiredAtUnixNano int64            `json:"acquired_at_unix_nano,omitempty"`
	ExpiresAtUnixNano  int64            `json:"expires_at_unix_nano,omitempty"`
	TimestampUnixNano  int64            `json:"timestamp_unix_nano"`
	Event              *TransitionEvent `json:"event,omitempty"`
}

// Clone returns a detached copy of Result.
func (r Result) Clone() Result {
	res := r
	if r.Event != nil {
		ev := *r.Event
		res.Event = &ev
	}
	return res
}

// NewErrorResult builds an error Result.
func NewErrorResult(cmd Command, err error) Result {
	var reqID, resName string
	var ts int64
	if cmd != nil {
		reqID = cmd.GetRequestID()
		resName = cmd.GetResourceName()
		ts = cmd.GetTimestampUnixNano()
	}
	_ = reqID // Included for caller context if needed

	msg := ""
	if err != nil {
		msg = err.Error()
	}

	return Result{
		Success:           false,
		Error:             err,
		Code:              ErrorCode(err),
		Message:           msg,
		ResourceName:      resName,
		TimestampUnixNano: ts,
	}
}

// ============================================================================
// 1. AcquireLockCommand
// ============================================================================

type AcquireLockCommand struct {
	RequestID         string `json:"request_id"`
	ResourceName      string `json:"resource_name"`
	OwnerID           string `json:"owner_id"`
	TTLNanoseconds    int64  `json:"ttl_nanoseconds"`
	TimestampUnixNano int64  `json:"timestamp_unix_nano"`
}

func (c AcquireLockCommand) Type() CommandType           { return CmdAcquireLock }
func (c AcquireLockCommand) GetRequestID() string        { return c.RequestID }
func (c AcquireLockCommand) GetResourceName() string     { return c.ResourceName }
func (c AcquireLockCommand) GetTimestampUnixNano() int64 { return c.TimestampUnixNano }

func (c AcquireLockCommand) Validate() error {
	if strings.TrimSpace(c.RequestID) == "" {
		return ErrInvalidCommand
	}
	if strings.TrimSpace(c.ResourceName) == "" {
		return ErrInvalidResource
	}
	if strings.TrimSpace(c.OwnerID) == "" {
		return ErrInvalidOwner
	}
	if c.TTLNanoseconds <= 0 {
		return ErrInvalidTTL
	}
	if c.TimestampUnixNano <= 0 {
		return ErrInvalidTimestamp
	}
	// Verify that timestamp + TTL does not overflow int64
	if c.TTLNanoseconds > math.MaxInt64-c.TimestampUnixNano {
		return ErrArithmeticOverflow
	}
	return nil
}

func (c AcquireLockCommand) Fingerprint() [32]byte {
	h := sha256.New()
	h.Write([]byte(CmdAcquireLock))
	h.Write([]byte(strings.TrimSpace(c.ResourceName)))
	h.Write([]byte(strings.TrimSpace(c.OwnerID)))
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(c.TTLNanoseconds))
	h.Write(b[:])
	var res [32]byte
	copy(res[:], h.Sum(nil))
	return res
}

// ============================================================================
// 2. RenewLockCommand
// ============================================================================

type RenewLockCommand struct {
	RequestID         string `json:"request_id"`
	ResourceName      string `json:"resource_name"`
	OwnerID           string `json:"owner_id"`
	FencingToken      uint64 `json:"fencing_token"`
	TTLNanoseconds    int64  `json:"ttl_nanoseconds"`
	TimestampUnixNano int64  `json:"timestamp_unix_nano"`
}

func (c RenewLockCommand) Type() CommandType           { return CmdRenewLock }
func (c RenewLockCommand) GetRequestID() string        { return c.RequestID }
func (c RenewLockCommand) GetResourceName() string     { return c.ResourceName }
func (c RenewLockCommand) GetTimestampUnixNano() int64 { return c.TimestampUnixNano }

func (c RenewLockCommand) Validate() error {
	if strings.TrimSpace(c.RequestID) == "" {
		return ErrInvalidCommand
	}
	if strings.TrimSpace(c.ResourceName) == "" {
		return ErrInvalidResource
	}
	if strings.TrimSpace(c.OwnerID) == "" {
		return ErrInvalidOwner
	}
	if c.FencingToken == 0 {
		return ErrFencingTokenMismatch
	}
	if c.TTLNanoseconds <= 0 {
		return ErrInvalidTTL
	}
	if c.TimestampUnixNano <= 0 {
		return ErrInvalidTimestamp
	}
	// Verify that timestamp + TTL does not overflow int64
	if c.TTLNanoseconds > math.MaxInt64-c.TimestampUnixNano {
		return ErrArithmeticOverflow
	}
	return nil
}

func (c RenewLockCommand) Fingerprint() [32]byte {
	h := sha256.New()
	h.Write([]byte(CmdRenewLock))
	h.Write([]byte(strings.TrimSpace(c.ResourceName)))
	h.Write([]byte(strings.TrimSpace(c.OwnerID)))
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], c.FencingToken)
	h.Write(b[:])
	binary.BigEndian.PutUint64(b[:], uint64(c.TTLNanoseconds))
	h.Write(b[:])
	var res [32]byte
	copy(res[:], h.Sum(nil))
	return res
}

// ============================================================================
// 3. ReleaseLockCommand
// ============================================================================

type ReleaseLockCommand struct {
	RequestID         string `json:"request_id"`
	ResourceName      string `json:"resource_name"`
	OwnerID           string `json:"owner_id"`
	FencingToken      uint64 `json:"fencing_token"`
	TimestampUnixNano int64  `json:"timestamp_unix_nano"`
}

func (c ReleaseLockCommand) Type() CommandType           { return CmdReleaseLock }
func (c ReleaseLockCommand) GetRequestID() string        { return c.RequestID }
func (c ReleaseLockCommand) GetResourceName() string     { return c.ResourceName }
func (c ReleaseLockCommand) GetTimestampUnixNano() int64 { return c.TimestampUnixNano }

func (c ReleaseLockCommand) Validate() error {
	if strings.TrimSpace(c.RequestID) == "" {
		return ErrInvalidCommand
	}
	if strings.TrimSpace(c.ResourceName) == "" {
		return ErrInvalidResource
	}
	if strings.TrimSpace(c.OwnerID) == "" {
		return ErrInvalidOwner
	}
	if c.FencingToken == 0 {
		return ErrFencingTokenMismatch
	}
	if c.TimestampUnixNano <= 0 {
		return ErrInvalidTimestamp
	}
	return nil
}

func (c ReleaseLockCommand) Fingerprint() [32]byte {
	h := sha256.New()
	h.Write([]byte(CmdReleaseLock))
	h.Write([]byte(strings.TrimSpace(c.ResourceName)))
	h.Write([]byte(strings.TrimSpace(c.OwnerID)))
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], c.FencingToken)
	h.Write(b[:])
	var res [32]byte
	copy(res[:], h.Sum(nil))
	return res
}

// ============================================================================
// 4. ExpireLockCommand
// ============================================================================

type ExpireLockCommand struct {
	RequestID                 string `json:"request_id"`
	ResourceName              string `json:"resource_name"`
	ExpectedOwnerID           string `json:"expected_owner_id"`
	ExpectedFencingToken      uint64 `json:"expected_fencing_token"`
	ExpectedLeaseVersion      uint64 `json:"expected_lease_version"`
	ExpectedExpiresAtUnixNano int64  `json:"expected_expires_at_unix_nano"`
	TimestampUnixNano         int64  `json:"timestamp_unix_nano"`
}

func (c ExpireLockCommand) Type() CommandType           { return CmdExpireLock }
func (c ExpireLockCommand) GetRequestID() string        { return c.RequestID }
func (c ExpireLockCommand) GetResourceName() string     { return c.ResourceName }
func (c ExpireLockCommand) GetTimestampUnixNano() int64 { return c.TimestampUnixNano }

func (c ExpireLockCommand) Validate() error {
	if strings.TrimSpace(c.RequestID) == "" {
		return ErrInvalidCommand
	}
	if strings.TrimSpace(c.ResourceName) == "" {
		return ErrInvalidResource
	}
	if strings.TrimSpace(c.ExpectedOwnerID) == "" {
		return ErrInvalidOwner
	}
	if c.ExpectedFencingToken == 0 {
		return ErrFencingTokenMismatch
	}
	if c.ExpectedLeaseVersion == 0 {
		return ErrLeaseVersionMismatch
	}
	if c.ExpectedExpiresAtUnixNano <= 0 {
		return ErrInvalidTimestamp
	}
	if c.TimestampUnixNano <= 0 {
		return ErrInvalidTimestamp
	}
	return nil
}

func (c ExpireLockCommand) Fingerprint() [32]byte {
	h := sha256.New()
	h.Write([]byte(CmdExpireLock))
	h.Write([]byte(strings.TrimSpace(c.ResourceName)))
	h.Write([]byte(strings.TrimSpace(c.ExpectedOwnerID)))
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], c.ExpectedFencingToken)
	h.Write(b[:])
	binary.BigEndian.PutUint64(b[:], c.ExpectedLeaseVersion)
	h.Write(b[:])
	binary.BigEndian.PutUint64(b[:], uint64(c.ExpectedExpiresAtUnixNano))
	h.Write(b[:])
	var res [32]byte
	copy(res[:], h.Sum(nil))
	return res
}
