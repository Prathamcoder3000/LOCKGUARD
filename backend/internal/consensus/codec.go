package consensus

import (
	"encoding/json"
	"errors"
	"fmt"

	"lockguard/backend/internal/lockstate"
)

// CurrentSchemaVersion is the Raft log command schema version.
const CurrentSchemaVersion uint32 = 1

var (
	ErrNilCommand               = errors.New("cannot encode nil command")
	ErrInvalidEnvelope          = errors.New("invalid log entry envelope")
	ErrUnsupportedSchemaVersion = errors.New("unsupported command schema version")
	ErrUnknownCommandType       = errors.New("unknown command type")
)

// CommandEnvelope provides versioning and type dispatch for Raft log entries.
type CommandEnvelope struct {
	SchemaVersion uint32                `json:"schema_version"`
	CommandType   lockstate.CommandType `json:"command_type"`
	Payload       json.RawMessage       `json:"payload"`
}

// EncodeCommand wraps a typed lockstate.Command into a versioned JSON envelope.
func EncodeCommand(cmd lockstate.Command) ([]byte, error) {
	if cmd == nil {
		return nil, ErrNilCommand
	}

	payload, err := json.Marshal(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal command payload: %w", err)
	}

	env := CommandEnvelope{
		SchemaVersion: CurrentSchemaVersion,
		CommandType:   cmd.Type(),
		Payload:       payload,
	}

	return json.Marshal(env)
}

// DecodeCommand unpacks a versioned JSON envelope into a validated lockstate.Command.
func DecodeCommand(data []byte) (lockstate.Command, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty data", ErrInvalidEnvelope)
	}

	var env CommandEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidEnvelope, err)
	}

	if env.SchemaVersion != CurrentSchemaVersion {
		return nil, fmt.Errorf("%w: %d (expected %d)", ErrUnsupportedSchemaVersion, env.SchemaVersion, CurrentSchemaVersion)
	}

	var cmd lockstate.Command
	switch env.CommandType {
	case lockstate.CmdAcquireLock:
		var c lockstate.AcquireLockCommand
		if err := json.Unmarshal(env.Payload, &c); err != nil {
			return nil, fmt.Errorf("failed to unmarshal AcquireLockCommand: %w", err)
		}
		cmd = c

	case lockstate.CmdRenewLock:
		var c lockstate.RenewLockCommand
		if err := json.Unmarshal(env.Payload, &c); err != nil {
			return nil, fmt.Errorf("failed to unmarshal RenewLockCommand: %w", err)
		}
		cmd = c

	case lockstate.CmdReleaseLock:
		var c lockstate.ReleaseLockCommand
		if err := json.Unmarshal(env.Payload, &c); err != nil {
			return nil, fmt.Errorf("failed to unmarshal ReleaseLockCommand: %w", err)
		}
		cmd = c

	case lockstate.CmdExpireLock:
		var c lockstate.ExpireLockCommand
		if err := json.Unmarshal(env.Payload, &c); err != nil {
			return nil, fmt.Errorf("failed to unmarshal ExpireLockCommand: %w", err)
		}
		cmd = c

	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownCommandType, env.CommandType)
	}

	if err := cmd.Validate(); err != nil {
		return nil, fmt.Errorf("decoded command failed validation: %w", err)
	}

	return cmd, nil
}

