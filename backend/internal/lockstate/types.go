package lockstate

// LockStatus represents the lifecycle state of a lock lease.
type LockStatus string

const (
	LockStatusFree    LockStatus = "FREE"
	LockStatusHeld    LockStatus = "HELD"
	LockStatusExpired LockStatus = "EXPIRED"
)

// LockRecord models an active distributed lease instance.
type LockRecord struct {
	ResourceName       string     `json:"resource_name"`
	OwnerID            string     `json:"owner_id"`
	FencingToken       uint64     `json:"fencing_token"`
	LeaseVersion       uint64     `json:"lease_version"`
	AcquiredAtUnixNano int64      `json:"acquired_at_unix_nano"`
	RenewedAtUnixNano  int64      `json:"renewed_at_unix_nano"`
	ExpiresAtUnixNano  int64      `json:"expires_at_unix_nano"`
	Status             LockStatus `json:"status"`
}

// Clone creates a detached deep copy of the LockRecord to prevent external mutation.
func (lr LockRecord) Clone() LockRecord {
	return LockRecord{
		ResourceName:       lr.ResourceName,
		OwnerID:            lr.OwnerID,
		FencingToken:       lr.FencingToken,
		LeaseVersion:       lr.LeaseVersion,
		AcquiredAtUnixNano: lr.AcquiredAtUnixNano,
		RenewedAtUnixNano:  lr.RenewedAtUnixNano,
		ExpiresAtUnixNano:  lr.ExpiresAtUnixNano,
		Status:             lr.Status,
	}
}

// ResourceState maintains the full historical and active consensus state for a resource.
type ResourceState struct {
	ResourceName string      `json:"resource_name"`
	HighestToken uint64      `json:"highest_token"`
	ActiveLock   *LockRecord `json:"active_lock,omitempty"`
}

// Clone creates a detached deep copy of the ResourceState.
func (rs *ResourceState) Clone() ResourceState {
	if rs == nil {
		return ResourceState{}
	}
	res := ResourceState{
		ResourceName: rs.ResourceName,
		HighestToken: rs.HighestToken,
	}
	if rs.ActiveLock != nil {
		clonedLock := rs.ActiveLock.Clone()
		res.ActiveLock = &clonedLock
	}
	return res
}

// TransitionType represents discrete state transitions applied to a lock.
type TransitionType string

const (
	TransitionAcquired TransitionType = "LOCK_ACQUIRED"
	TransitionRenewed  TransitionType = "LOCK_RENEWED"
	TransitionReleased TransitionType = "LOCK_RELEASED"
	TransitionExpired  TransitionType = "LOCK_EXPIRED"
)

// TransitionEvent records the deterministic result of a state machine transition.
type TransitionEvent struct {
	Type              TransitionType `json:"type"`
	ResourceName      string         `json:"resource_name"`
	OwnerID           string         `json:"owner_id"`
	FencingToken      uint64         `json:"fencing_token"`
	LeaseVersion      uint64         `json:"lease_version,omitempty"`
	TimestampUnixNano int64          `json:"timestamp_unix_nano"`
	ExpiresAtUnixNano int64          `json:"expires_at_unix_nano,omitempty"`
}

// IdempotencyRecord stores the result and canonical fingerprint of a processed command.
type IdempotencyRecord struct {
	RequestID         string   `json:"request_id"`
	Fingerprint       [32]byte `json:"fingerprint"`
	Result            Result   `json:"result"`
	TimestampUnixNano int64    `json:"timestamp_unix_nano"`
}
