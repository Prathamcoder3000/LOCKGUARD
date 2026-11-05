package chaos

import (
	"errors"
	"time"
)

var (
	ErrChaosDisabled       = errors.New("chaos framework is disabled: cannot inject faults in current environment")
	ErrUnauthorized        = errors.New("unauthorized: administrative privileges required for chaos fault injection")
	ErrFaultNotFound       = errors.New("fault not found")
	ErrFaultAlreadyActive  = errors.New("a conflicting fault is already active on the target")
	ErrInvalidFaultTarget  = errors.New("invalid or empty fault target")
	ErrInvalidFaultType    = errors.New("unrecognized or unsupported fault type")
)

// FaultType identifies the controlled failure mode being injected.
type FaultType string

const (
	FaultProcessCrash              FaultType = "COORDINATOR_PROCESS_CRASH"
	FaultNetworkPartition          FaultType = "COORDINATOR_NETWORK_PARTITION"
	FaultNetworkDelay              FaultType = "COORDINATOR_NETWORK_DELAY"
	FaultRequestTimeout            FaultType = "COORDINATOR_REQUEST_TIMEOUT"
	FaultPacketDrop                FaultType = "COORDINATOR_PACKET_DROP"
	FaultDatabaseOutage            FaultType = "POSTGRESQL_CONNECTIVITY_OUTAGE"
	FaultGatewayRestart            FaultType = "API_GATEWAY_RESTART"
	FaultProtectedResourceOutage   FaultType = "PROTECTED_RESOURCE_OUTAGE"
	FaultDelayedRequest            FaultType = "DELAYED_CLIENT_REQUEST"
	FaultDuplicateRequest          FaultType = "DUPLICATE_CLIENT_REQUEST"
)

// FaultDescriptor captures the parameters, lifecycle, and diagnosis of an injected fault.
type FaultDescriptor struct {
	ID          string                 `json:"id"`
	Type        FaultType              `json:"type"`
	Target      string                 `json:"target"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
	InjectedAt  time.Time              `json:"injected_at"`
	RevertedAt  *time.Time             `json:"reverted_at,omitempty"`
	Active      bool                   `json:"active"`
	Description string                 `json:"description"`
}

// FaultEvent represents a structured diagnostic record generated upon injection or recovery.
type FaultEvent struct {
	EventID     string                 `json:"event_id"`
	FaultID     string                 `json:"fault_id"`
	Type        FaultType              `json:"type"`
	Target      string                 `json:"target"`
	Action      string                 `json:"action"` // "INJECTED" or "REVERTED"
	Timestamp   time.Time              `json:"timestamp"`
	Diagnostics map[string]interface{} `json:"diagnostics,omitempty"`
}

// ChaosReport summarizes current active faults and historical chaos executions.
type ChaosReport struct {
	Enabled       bool              `json:"enabled"`
	Environment   string            `json:"environment"`
	ActiveCount   int               `json:"active_count"`
	ActiveFaults  []FaultDescriptor `json:"active_faults"`
	HistoryTotal  int               `json:"history_total"`
	RecentHistory []FaultDescriptor `json:"recent_history"`
	GeneratedAt   time.Time         `json:"generated_at"`
}

