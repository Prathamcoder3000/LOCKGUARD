package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// PostgresCheckpointRepo implements CheckpointRepository using PostgreSQL.
type PostgresCheckpointRepo struct {
	db *Database
}

// NewPostgresCheckpointRepo constructs a new PostgresCheckpointRepo.
func NewPostgresCheckpointRepo(db *Database) *PostgresCheckpointRepo {
	return &PostgresCheckpointRepo{db: db}
}

// SaveCheckpoint records a cluster checkpoint diagnostic entry.
func (r *PostgresCheckpointRepo) SaveCheckpoint(ctx context.Context, cp *SystemCheckpointRecord) error {
	if cp == nil {
		return fmt.Errorf("checkpoint cannot be nil")
	}

	summary := cp.Summary
	if len(summary) == 0 {
		summary = json.RawMessage("{}")
	}

	createdAt := cp.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	query := `
		INSERT INTO system_checkpoints (
			checkpoint_type, node_id, raft_index, raft_term, summary, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6
		)
		RETURNING id;
	`

	err := r.db.DB().QueryRowContext(
		ctx, query,
		cp.CheckpointType, cp.NodeID, int64(cp.RaftIndex), int64(cp.RaftTerm), summary, createdAt,
	).Scan(&cp.ID)

	if err != nil {
		return fmt.Errorf("failed to save checkpoint: %w", err)
	}

	return nil
}

// GetLatestCheckpoint retrieves the most recent checkpoint for a given type.
func (r *PostgresCheckpointRepo) GetLatestCheckpoint(ctx context.Context, checkpointType string) (*SystemCheckpointRecord, error) {
	query := `
		SELECT id, checkpoint_type, node_id, raft_index, raft_term, summary, created_at
		FROM system_checkpoints
		WHERE checkpoint_type = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1;
	`

	var cp SystemCheckpointRecord
	var rIdx, rTerm int64

	err := r.db.DB().QueryRowContext(ctx, query, checkpointType).Scan(
		&cp.ID, &cp.CheckpointType, &cp.NodeID, &rIdx, &rTerm, &cp.Summary, &cp.CreatedAt,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrEventNotFound
		}
		return nil, fmt.Errorf("failed to get latest checkpoint: %w", err)
	}

	cp.RaftIndex = uint64(rIdx)
	cp.RaftTerm = uint64(rTerm)
	return &cp, nil
}

