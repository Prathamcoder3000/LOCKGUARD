package lockstate

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

// CurrentSnapshotVersion represents the current serialization version of snapshot data.
const CurrentSnapshotVersion uint32 = 1

// SnapshotData is the deterministic state export of the MemoryStateMachine.
type SnapshotData struct {
	Version             uint32              `json:"version"`
	LastAppliedUnixNano int64               `json:"last_applied_unix_nano"`
	Resources           []ResourceState     `json:"resources"`
	IdempotencyRecords  []IdempotencyRecord `json:"idempotency_records"`
}

// ExportSnapshot creates a deep copy snapshot of the current state machine state.
// Map entries are sorted deterministically (by resource name and request ID).
func (s *MemoryStateMachine) ExportSnapshot() (*SnapshotData, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 1. Export resources deterministically sorted by ResourceName
	resSlice := make([]ResourceState, 0, len(s.resources))
	for _, rs := range s.resources {
		resSlice = append(resSlice, rs.Clone())
	}
	sort.Slice(resSlice, func(i, j int) bool {
		return resSlice[i].ResourceName < resSlice[j].ResourceName
	})

	// 2. Export idempotency records deterministically sorted by RequestID
	idemSlice := make([]IdempotencyRecord, 0, len(s.idempotency))
	for _, ir := range s.idempotency {
		recCopy := IdempotencyRecord{
			RequestID:         ir.RequestID,
			Fingerprint:       ir.Fingerprint,
			Result:            ir.Result.Clone(),
			TimestampUnixNano: ir.TimestampUnixNano,
		}
		idemSlice = append(idemSlice, recCopy)
	}
	sort.Slice(idemSlice, func(i, j int) bool {
		return idemSlice[i].RequestID < idemSlice[j].RequestID
	})

	return &SnapshotData{
		Version:             CurrentSnapshotVersion,
		LastAppliedUnixNano: s.lastAppliedUnixNano,
		Resources:           resSlice,
		IdempotencyRecords:  idemSlice,
	}, nil
}

// RestoreSnapshot atomically resets and restores the state machine from a snapshot.
func (s *MemoryStateMachine) RestoreSnapshot(snap *SnapshotData) error {
	if snap == nil {
		return fmt.Errorf("cannot restore nil snapshot: %w", ErrInvalidCommand)
	}
	if snap.Version != CurrentSnapshotVersion {
		return fmt.Errorf("unsupported snapshot version %d (expected %d): %w", snap.Version, CurrentSnapshotVersion, ErrInvalidCommand)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Clear and reconstruct resources
	newResources := make(map[string]*ResourceState, len(snap.Resources))
	for _, rs := range snap.Resources {
		if rs.ResourceName == "" {
			return fmt.Errorf("corrupt snapshot: empty resource name: %w", ErrInternalState)
		}
		cloned := rs.Clone()
		newResources[cloned.ResourceName] = &cloned
	}

	// Clear and reconstruct idempotency records
	newIdempotency := make(map[string]IdempotencyRecord, len(snap.IdempotencyRecords))
	for _, ir := range snap.IdempotencyRecords {
		if ir.RequestID == "" {
			return fmt.Errorf("corrupt snapshot: empty request ID in idempotency record: %w", ErrInternalState)
		}
		clonedResult := ir.Result.Clone()
		if !clonedResult.Success && clonedResult.Code != "" && clonedResult.Error == nil {
			clonedResult.Error = ErrorFromCode(clonedResult.Code)
		}
		newIdempotency[ir.RequestID] = IdempotencyRecord{
			RequestID:         ir.RequestID,
			Fingerprint:       ir.Fingerprint,
			Result:            clonedResult,
			TimestampUnixNano: ir.TimestampUnixNano,
		}
	}

	s.resources = newResources
	s.idempotency = newIdempotency
	s.lastAppliedUnixNano = snap.LastAppliedUnixNano

	return nil
}

// Encode writes the snapshot as JSON to the provided writer.
func (snap *SnapshotData) Encode(w io.Writer) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(snap)
}

// DecodeSnapshot reads and unmarshals snapshot data from a reader.
func DecodeSnapshot(r io.Reader) (*SnapshotData, error) {
	var snap SnapshotData
	if err := json.NewDecoder(r).Decode(&snap); err != nil {
		return nil, fmt.Errorf("failed to decode snapshot: %w", err)
	}
	return &snap, nil
}

