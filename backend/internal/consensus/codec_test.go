package consensus_test

import (
	"testing"
	"time"

	"lockguard/backend/internal/consensus"
	"lockguard/backend/internal/lockstate"
)

func TestCodec_AcquireCommandRoundTrip(t *testing.T) {
	cmd := lockstate.AcquireLockCommand{
		RequestID:         "req-acquire-1",
		ResourceName:      "shared-counter",
		OwnerID:           "node-1",
		TTLNanoseconds:    (5 * time.Second).Nanoseconds(),
		TimestampUnixNano: 1700000000000,
	}

	encoded, err := consensus.EncodeCommand(cmd)
	if err != nil {
		t.Fatalf("EncodeCommand failed: %v", err)
	}

	decoded, err := consensus.DecodeCommand(encoded)
	if err != nil {
		t.Fatalf("DecodeCommand failed: %v", err)
	}

	ac, ok := decoded.(lockstate.AcquireLockCommand)
	if !ok {
		t.Fatalf("expected AcquireLockCommand, got %T", decoded)
	}

	if ac != cmd {
		t.Fatalf("decoded command %+v does not match original %+v", ac, cmd)
	}
}

func TestCodec_RenewCommandRoundTrip(t *testing.T) {
	cmd := lockstate.RenewLockCommand{
		RequestID:         "req-renew-1",
		ResourceName:      "shared-counter",
		OwnerID:           "node-1",
		FencingToken:      1001,
		TTLNanoseconds:    (10 * time.Second).Nanoseconds(),
		TimestampUnixNano: 1700000001000,
	}

	encoded, err := consensus.EncodeCommand(cmd)
	if err != nil {
		t.Fatalf("EncodeCommand failed: %v", err)
	}

	decoded, err := consensus.DecodeCommand(encoded)
	if err != nil {
		t.Fatalf("DecodeCommand failed: %v", err)
	}

	rc, ok := decoded.(lockstate.RenewLockCommand)
	if !ok {
		t.Fatalf("expected RenewLockCommand, got %T", decoded)
	}

	if rc != cmd {
		t.Fatalf("decoded command %+v does not match original %+v", rc, cmd)
	}
}

func TestCodec_ReleaseCommandRoundTrip(t *testing.T) {
	cmd := lockstate.ReleaseLockCommand{
		RequestID:         "req-release-1",
		ResourceName:      "shared-counter",
		OwnerID:           "node-1",
		FencingToken:      1001,
		TimestampUnixNano: 1700000002000,
	}

	encoded, err := consensus.EncodeCommand(cmd)
	if err != nil {
		t.Fatalf("EncodeCommand failed: %v", err)
	}

	decoded, err := consensus.DecodeCommand(encoded)
	if err != nil {
		t.Fatalf("DecodeCommand failed: %v", err)
	}

	rel, ok := decoded.(lockstate.ReleaseLockCommand)
	if !ok {
		t.Fatalf("expected ReleaseLockCommand, got %T", decoded)
	}

	if rel != cmd {
		t.Fatalf("decoded command %+v does not match original %+v", rel, cmd)
	}
}

func TestCodec_ExpireCommandRoundTrip(t *testing.T) {
	cmd := lockstate.ExpireLockCommand{
		RequestID:                 "req-expire-1",
		ResourceName:              "shared-counter",
		ExpectedOwnerID:           "node-1",
		ExpectedFencingToken:      1001,
		ExpectedLeaseVersion:      2,
		ExpectedExpiresAtUnixNano: 1700000005000,
		TimestampUnixNano:         1700000006000,
	}

	encoded, err := consensus.EncodeCommand(cmd)
	if err != nil {
		t.Fatalf("EncodeCommand failed: %v", err)
	}

	decoded, err := consensus.DecodeCommand(encoded)
	if err != nil {
		t.Fatalf("DecodeCommand failed: %v", err)
	}

	ec, ok := decoded.(lockstate.ExpireLockCommand)
	if !ok {
		t.Fatalf("expected ExpireLockCommand, got %T", decoded)
	}

	if ec != cmd {
		t.Fatalf("decoded command %+v does not match original %+v", ec, cmd)
	}
}

func TestCodec_InvalidEnvelopeRejections(t *testing.T) {
	// Empty data
	if _, err := consensus.DecodeCommand(nil); err == nil {
		t.Errorf("expected error for nil data")
	}

	// Corrupted JSON
	if _, err := consensus.DecodeCommand([]byte("{invalid-json")); err == nil {
		t.Errorf("expected error for malformed json")
	}

	// Unsupported schema version
	invalidVer := []byte(`{"schema_version": 99, "command_type": "ACQUIRE_LOCK", "payload": {}}`)
	if _, err := consensus.DecodeCommand(invalidVer); err == nil {
		t.Errorf("expected error for unsupported schema version")
	}

	// Unknown command type
	unknownType := []byte(`{"schema_version": 1, "command_type": "UNKNOWN_ACTION", "payload": {}}`)
	if _, err := consensus.DecodeCommand(unknownType); err == nil {
		t.Errorf("expected error for unknown command type")
	}

	// Nil command encode
	if _, err := consensus.EncodeCommand(nil); err == nil {
		t.Errorf("expected error encoding nil command")
	}
}

