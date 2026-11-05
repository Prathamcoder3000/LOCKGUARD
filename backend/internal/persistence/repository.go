package persistence

import (
	"context"
	"encoding/json"
	"time"
)

// AuditEventRecord represents an individual audit log entry in the PostgreSQL store.
type AuditEventRecord struct {
	ID              int64           `json:"id"`
	EventID         string          `json:"event_id"`
	EventType       string          `json:"event_type"`
	EventCategory   string          `json:"event_category"`
	RequestID       string          `json:"request_id"`
	ResourceID      string          `json:"resource_id"`
	OwnerID         string          `json:"owner_id"`
	ClientID        string          `json:"client_id"`
	FencingToken    uint64          `json:"fencing_token"`
	LeaseVersion    uint64          `json:"lease_version"`
	OperationStatus string          `json:"operation_status"`
	ErrorCode       string          `json:"error_code,omitempty"`
	CoordinatorID   string          `json:"coordinator_id"`
	RaftIndex       uint64          `json:"raft_index"`
	EventTimestamp  time.Time       `json:"event_timestamp"`
	RecordedAt      time.Time       `json:"recorded_at"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
}

// AuditFilter encapsulates pagination, filtering, and time-range query criteria.
type AuditFilter struct {
	EventType       string     `json:"event_type,omitempty"`
	EventCategory   string     `json:"event_category,omitempty"`
	ResourceID      string     `json:"resource_id,omitempty"`
	OwnerID         string     `json:"owner_id,omitempty"`
	ClientID        string     `json:"client_id,omitempty"`
	RequestID       string     `json:"request_id,omitempty"`
	OperationStatus string     `json:"operation_status,omitempty"`
	StartTime       *time.Time `json:"start_time,omitempty"`
	EndTime         *time.Time `json:"end_time,omitempty"`
	Limit           int        `json:"limit"`
	Offset          int        `json:"offset"`
}

// OperationalEventRecord represents system lifecycle, failover, and operational diagnostic events.
type OperationalEventRecord struct {
	ID         int64           `json:"id"`
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	Component  string          `json:"component"`
	NodeID     string          `json:"node_id"`
	Status     string          `json:"status"`
	Details    json.RawMessage `json:"details,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// OperationalFilter specifies query options for operational event retrieval.
type OperationalFilter struct {
	EventType string     `json:"event_type,omitempty"`
	Component string     `json:"component,omitempty"`
	NodeID    string     `json:"node_id,omitempty"`
	Status    string     `json:"status,omitempty"`
	StartTime *time.Time `json:"start_time,omitempty"`
	EndTime   *time.Time `json:"end_time,omitempty"`
	Limit     int        `json:"limit"`
	Offset    int        `json:"offset"`
}

// SystemCheckpointRecord tracks operational checkpoints for cluster state references.
type SystemCheckpointRecord struct {
	ID             int64           `json:"id"`
	CheckpointType string          `json:"checkpoint_type"`
	NodeID         string          `json:"node_id"`
	RaftIndex      uint64          `json:"raft_index"`
	RaftTerm       uint64          `json:"raft_term"`
	Summary        json.RawMessage `json:"summary,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

// AuditRepository defines persistence contracts for append-only audit events.
type AuditRepository interface {
	InsertEvent(ctx context.Context, event *AuditEventRecord) error
	InsertBatch(ctx context.Context, events []*AuditEventRecord) (int, error)
	GetEventByID(ctx context.Context, eventID string) (*AuditEventRecord, error)
	ListEvents(ctx context.Context, filter AuditFilter) ([]*AuditEventRecord, int, error)
}

// OperationalEventRepository defines persistence contracts for system operational events.
type OperationalEventRepository interface {
	InsertEvent(ctx context.Context, event *OperationalEventRecord) error
	ListEvents(ctx context.Context, filter OperationalFilter) ([]*OperationalEventRecord, int, error)
}

// CheckpointRepository defines persistence contracts for non-authoritative system checkpoints.
type CheckpointRepository interface {
	SaveCheckpoint(ctx context.Context, cp *SystemCheckpointRecord) error
	GetLatestCheckpoint(ctx context.Context, checkpointType string) (*SystemCheckpointRecord, error)
}

