package audit

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"lockguard/backend/internal/persistence"
)

// Event Category constants.
const (
	CategoryLockOperation    = "LOCK_OPERATION"
	CategorySessionLifecycle = "SESSION_LIFECYCLE"
	CategoryRenewalLifecycle = "RENEWAL_LIFECYCLE"
	CategoryConsensus        = "CONSENSUS_TOPOLOGY"
	CategorySystem           = "SYSTEM_OPERATIONAL"
	CategorySecurity         = "SECURITY_AUTH"
	CategoryAdministration   = "ADMINISTRATION"
)

// Event Type constants.
const (
	TypeLockAcquire          = "LOCK_ACQUIRE"
	TypeLockRenew            = "LOCK_RENEW"
	TypeLockRelease          = "LOCK_RELEASE"
	TypeLockExpire           = "LOCK_EXPIRE"
	TypeLockRejected         = "LOCK_REJECTED"
	TypeLockFailed           = "LOCK_FAILED"
	TypeClientRegister       = "CLIENT_REGISTER"
	TypeClientHeartbeat      = "CLIENT_HEARTBEAT"
	TypeSessionExpired       = "SESSION_EXPIRED"
	TypeRenewalStarted       = "RENEWAL_STARTED"
	TypeRenewalStopped       = "RENEWAL_STOPPED"
	TypeLeaderChange         = "LEADER_CHANGE"
	TypeQuorumLoss           = "QUORUM_LOSS"
	TypeSystemStartup        = "SYSTEM_STARTUP"
	TypeSystemShutdown       = "SYSTEM_SHUTDOWN"
	TypeAuthFailure          = "AUTHENTICATION_FAILURE"
	TypeAuthzDenial          = "AUTHORIZATION_DENIAL"
	TypeAdminStepdown        = "ADMIN_STEPDOWN"
	TypeAdminEmergencyUnlock = "ADMIN_EMERGENCY_UNLOCK"
)

// Operation Status constants.
const (
	StatusCommitted = "COMMITTED"
	StatusRejected  = "REJECTED"
	StatusFailed    = "FAILED"
	StatusPending   = "PENDING"
)

// Event represents a structured, validated audit entry.
type Event struct {
	EventID         string                 `json:"event_id"`
	EventType       string                 `json:"event_type"`
	EventCategory   string                 `json:"event_category"`
	RequestID       string                 `json:"request_id"`
	ResourceID      string                 `json:"resource_id"`
	OwnerID         string                 `json:"owner_id"`
	ClientID        string                 `json:"client_id"`
	FencingToken    uint64                 `json:"fencing_token"`
	LeaseVersion    uint64                 `json:"lease_version"`
	OperationStatus string                 `json:"operation_status"`
	ErrorCode       string                 `json:"error_code,omitempty"`
	CoordinatorID   string                 `json:"coordinator_id"`
	RaftIndex       uint64                 `json:"raft_index"`
	EventTimestamp  time.Time              `json:"event_timestamp"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
}

// GenerateEventID produces a secure, unique hexadecimal event identifier.
func GenerateEventID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Validate sanitizes the event, ensuring required fields are populated and zero secrets are retained.
func (e *Event) Validate() error {
	if e.EventID == "" {
		e.EventID = GenerateEventID()
	}
	if strings.TrimSpace(e.EventType) == "" {
		return fmt.Errorf("event_type is required")
	}
	if strings.TrimSpace(e.EventCategory) == "" {
		e.EventCategory = CategoryLockOperation
	}
	if strings.TrimSpace(e.OperationStatus) == "" {
		e.OperationStatus = StatusPending
	}
	if e.EventTimestamp.IsZero() {
		e.EventTimestamp = time.Now().UTC()
	}

	// Sanitize metadata: redact session IDs, bearer tokens, and secrets
	if e.Metadata != nil {
		sanitized := make(map[string]interface{}, len(e.Metadata))
		for k, v := range e.Metadata {
			lowerKey := strings.ToLower(k)
			if strings.Contains(lowerKey, "session_id") ||
				strings.Contains(lowerKey, "token_secret") ||
				strings.Contains(lowerKey, "password") ||
				strings.Contains(lowerKey, "secret") ||
				strings.Contains(lowerKey, "authorization") {
				sanitized[k] = "[REDACTED]"
			} else {
				sanitized[k] = v
			}
		}
		e.Metadata = sanitized
	}

	return nil
}

// ToPersistenceRecord converts the validated Event into a database record.
func (e *Event) ToPersistenceRecord() (*persistence.AuditEventRecord, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}

	var metadataBytes []byte
	if len(e.Metadata) > 0 {
		var err error
		metadataBytes, err = json.Marshal(e.Metadata)
		if err != nil {
			metadataBytes = []byte("{}")
		}
	} else {
		metadataBytes = []byte("{}")
	}

	return &persistence.AuditEventRecord{
		EventID:         e.EventID,
		EventType:       e.EventType,
		EventCategory:   e.EventCategory,
		RequestID:       e.RequestID,
		ResourceID:      e.ResourceID,
		OwnerID:         e.OwnerID,
		ClientID:        e.ClientID,
		FencingToken:    e.FencingToken,
		LeaseVersion:    e.LeaseVersion,
		OperationStatus: e.OperationStatus,
		ErrorCode:       e.ErrorCode,
		CoordinatorID:   e.CoordinatorID,
		RaftIndex:       e.RaftIndex,
		EventTimestamp:  e.EventTimestamp,
		RecordedAt:      time.Now().UTC(),
		Metadata:        metadataBytes,
	}, nil
}

