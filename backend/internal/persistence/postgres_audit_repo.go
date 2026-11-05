package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// PostgresAuditRepo implements AuditRepository using PostgreSQL.
type PostgresAuditRepo struct {
	db *Database
}

// NewPostgresAuditRepo constructs a new PostgresAuditRepo.
func NewPostgresAuditRepo(db *Database) *PostgresAuditRepo {
	return &PostgresAuditRepo{db: db}
}

// InsertEvent persists a single audit event record.
func (r *PostgresAuditRepo) InsertEvent(ctx context.Context, event *AuditEventRecord) error {
	if event == nil {
		return fmt.Errorf("audit event cannot be nil")
	}
	if event.EventID == "" {
		return fmt.Errorf("event_id is required")
	}

	metadata := event.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage("{}")
	}

	recordedAt := event.RecordedAt
	if recordedAt.IsZero() {
		recordedAt = time.Now().UTC()
	}

	query := `
		INSERT INTO audit_events (
			event_id, event_type, event_category, request_id, resource_id,
			owner_id, client_id, fencing_token, lease_version, operation_status,
			error_code, coordinator_id, raft_index, event_timestamp, recorded_at, metadata
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10,
			$11, $12, $13, $14, $15, $16
		)
		RETURNING id;
	`

	err := r.db.DB().QueryRowContext(
		ctx, query,
		event.EventID, event.EventType, event.EventCategory, event.RequestID, event.ResourceID,
		event.OwnerID, event.ClientID, int64(event.FencingToken), int64(event.LeaseVersion), event.OperationStatus,
		event.ErrorCode, event.CoordinatorID, int64(event.RaftIndex), event.EventTimestamp, recordedAt, metadata,
	).Scan(&event.ID)

	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) {
			if pqErr.Code == "23505" { // unique_violation
				return ErrDuplicateEvent
			}
		}
		return fmt.Errorf("failed to insert audit event: %w", err)
	}

	return nil
}

// InsertBatch persists a batch of audit events atomically using a transaction.
// Returns the count of successfully inserted records.
func (r *PostgresAuditRepo) InsertBatch(ctx context.Context, events []*AuditEventRecord) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}

	var insertedCount int
	err := r.db.WithTx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO audit_events (
				event_id, event_type, event_category, request_id, resource_id,
				owner_id, client_id, fencing_token, lease_version, operation_status,
				error_code, coordinator_id, raft_index, event_timestamp, recorded_at, metadata
			) VALUES (
				$1, $2, $3, $4, $5,
				$6, $7, $8, $9, $10,
				$11, $12, $13, $14, $15, $16
			)
			ON CONFLICT (event_id) DO NOTHING
			RETURNING id;
		`)
		if err != nil {
			return err
		}
		defer stmt.Close()

		for _, ev := range events {
			if ev == nil || ev.EventID == "" {
				continue
			}
			metadata := ev.Metadata
			if len(metadata) == 0 {
				metadata = json.RawMessage("{}")
			}
			recordedAt := ev.RecordedAt
			if recordedAt.IsZero() {
				recordedAt = time.Now().UTC()
			}

			var id int64
			err := stmt.QueryRowContext(
				ctx,
				ev.EventID, ev.EventType, ev.EventCategory, ev.RequestID, ev.ResourceID,
				ev.OwnerID, ev.ClientID, int64(ev.FencingToken), int64(ev.LeaseVersion), ev.OperationStatus,
				ev.ErrorCode, ev.CoordinatorID, int64(ev.RaftIndex), ev.EventTimestamp, recordedAt, metadata,
			).Scan(&id)

			if err == nil {
				ev.ID = id
				insertedCount++
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		return nil
	})

	if err != nil {
		return insertedCount, fmt.Errorf("batch insert failed: %w", err)
	}

	return insertedCount, nil
}

// GetEventByID retrieves an audit event record by its unique event_id.
func (r *PostgresAuditRepo) GetEventByID(ctx context.Context, eventID string) (*AuditEventRecord, error) {
	if strings.TrimSpace(eventID) == "" {
		return nil, ErrEventNotFound
	}

	query := `
		SELECT id, event_id, event_type, event_category, request_id, resource_id,
		       owner_id, client_id, fencing_token, lease_version, operation_status,
		       error_code, coordinator_id, raft_index, event_timestamp, recorded_at, metadata
		FROM audit_events
		WHERE event_id = $1
		LIMIT 1;
	`

	var ev AuditEventRecord
	var fToken, lVer, rIdx int64

	err := r.db.DB().QueryRowContext(ctx, query, eventID).Scan(
		&ev.ID, &ev.EventID, &ev.EventType, &ev.EventCategory, &ev.RequestID, &ev.ResourceID,
		&ev.OwnerID, &ev.ClientID, &fToken, &lVer, &ev.OperationStatus,
		&ev.ErrorCode, &ev.CoordinatorID, &rIdx, &ev.EventTimestamp, &ev.RecordedAt, &ev.Metadata,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrEventNotFound
		}
		return nil, fmt.Errorf("failed to get audit event by ID: %w", err)
	}

	ev.FencingToken = uint64(fToken)
	ev.LeaseVersion = uint64(lVer)
	ev.RaftIndex = uint64(rIdx)

	return &ev, nil
}

// ListEvents queries audit events based on filter conditions, pagination, and time bounds.
func (r *PostgresAuditRepo) ListEvents(ctx context.Context, filter AuditFilter) ([]*AuditEventRecord, int, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	whereClauses := []string{"1=1"}
	args := make([]interface{}, 0, 10)
	argIdx := 1

	if filter.EventType != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("event_type = $%d", argIdx))
		args = append(args, filter.EventType)
		argIdx++
	}
	if filter.EventCategory != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("event_category = $%d", argIdx))
		args = append(args, filter.EventCategory)
		argIdx++
	}
	if filter.ResourceID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("resource_id = $%d", argIdx))
		args = append(args, filter.ResourceID)
		argIdx++
	}
	if filter.OwnerID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("owner_id = $%d", argIdx))
		args = append(args, filter.OwnerID)
		argIdx++
	}
	if filter.ClientID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("client_id = $%d", argIdx))
		args = append(args, filter.ClientID)
		argIdx++
	}
	if filter.RequestID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("request_id = $%d", argIdx))
		args = append(args, filter.RequestID)
		argIdx++
	}
	if filter.OperationStatus != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("operation_status = $%d", argIdx))
		args = append(args, filter.OperationStatus)
		argIdx++
	}
	if filter.StartTime != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("event_timestamp >= $%d", argIdx))
		args = append(args, *filter.StartTime)
		argIdx++
	}
	if filter.EndTime != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("event_timestamp <= $%d", argIdx))
		args = append(args, *filter.EndTime)
		argIdx++
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	// 1. Get total count
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM audit_events WHERE %s", whereSQL)
	var total int
	if err := r.db.DB().QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count audit events: %w", err)
	}

	// 2. Query paginated rows
	query := fmt.Sprintf(`
		SELECT id, event_id, event_type, event_category, request_id, resource_id,
		       owner_id, client_id, fencing_token, lease_version, operation_status,
		       error_code, coordinator_id, raft_index, event_timestamp, recorded_at, metadata
		FROM audit_events
		WHERE %s
		ORDER BY event_timestamp DESC, id DESC
		LIMIT $%d OFFSET $%d;
	`, whereSQL, argIdx, argIdx+1)

	queryArgs := append(args, limit, offset)

	rows, err := r.db.DB().QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list audit events: %w", err)
	}
	defer rows.Close()

	records := make([]*AuditEventRecord, 0, limit)
	for rows.Next() {
		var ev AuditEventRecord
		var fToken, lVer, rIdx int64

		if err := rows.Scan(
			&ev.ID, &ev.EventID, &ev.EventType, &ev.EventCategory, &ev.RequestID, &ev.ResourceID,
			&ev.OwnerID, &ev.ClientID, &fToken, &lVer, &ev.OperationStatus,
			&ev.ErrorCode, &ev.CoordinatorID, &rIdx, &ev.EventTimestamp, &ev.RecordedAt, &ev.Metadata,
		); err != nil {
			return nil, 0, fmt.Errorf("failed to scan audit event row: %w", err)
		}

		ev.FencingToken = uint64(fToken)
		ev.LeaseVersion = uint64(lVer)
		ev.RaftIndex = uint64(rIdx)
		records = append(records, &ev)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("rows error: %w", err)
	}

	return records, total, nil
}
