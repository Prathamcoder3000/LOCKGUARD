package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// PostgresOperationalRepo implements OperationalEventRepository using PostgreSQL.
type PostgresOperationalRepo struct {
	db *Database
}

// NewPostgresOperationalRepo constructs a new PostgresOperationalRepo.
func NewPostgresOperationalRepo(db *Database) *PostgresOperationalRepo {
	return &PostgresOperationalRepo{db: db}
}

// InsertEvent persists an operational lifecycle event.
func (r *PostgresOperationalRepo) InsertEvent(ctx context.Context, event *OperationalEventRecord) error {
	if event == nil {
		return fmt.Errorf("operational event cannot be nil")
	}
	if event.EventID == "" {
		return fmt.Errorf("event_id is required")
	}

	details := event.Details
	if len(details) == 0 {
		details = json.RawMessage("{}")
	}

	createdAt := event.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	query := `
		INSERT INTO operational_events (
			event_id, event_type, component, node_id, status, details, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7
		)
		RETURNING id;
	`

	err := r.db.DB().QueryRowContext(
		ctx, query,
		event.EventID, event.EventType, event.Component, event.NodeID,
		event.Status, details, createdAt,
	).Scan(&event.ID)

	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return ErrDuplicateEvent
		}
		return fmt.Errorf("failed to insert operational event: %w", err)
	}

	return nil
}

// ListEvents queries operational events matching filter criteria.
func (r *PostgresOperationalRepo) ListEvents(ctx context.Context, filter OperationalFilter) ([]*OperationalEventRecord, int, error) {
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
	args := make([]interface{}, 0, 8)
	argIdx := 1

	if filter.EventType != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("event_type = $%d", argIdx))
		args = append(args, filter.EventType)
		argIdx++
	}
	if filter.Component != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("component = $%d", argIdx))
		args = append(args, filter.Component)
		argIdx++
	}
	if filter.NodeID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("node_id = $%d", argIdx))
		args = append(args, filter.NodeID)
		argIdx++
	}
	if filter.Status != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, filter.Status)
		argIdx++
	}
	if filter.StartTime != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("created_at >= $%d", argIdx))
		args = append(args, *filter.StartTime)
		argIdx++
	}
	if filter.EndTime != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("created_at <= $%d", argIdx))
		args = append(args, *filter.EndTime)
		argIdx++
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM operational_events WHERE %s", whereSQL)
	var total int
	if err := r.db.DB().QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count operational events: %w", err)
	}

	query := fmt.Sprintf(`
		SELECT id, event_id, event_type, component, node_id, status, details, created_at
		FROM operational_events
		WHERE %s
		ORDER BY created_at DESC, id DESC
		LIMIT $%d OFFSET $%d;
	`, whereSQL, argIdx, argIdx+1)

	queryArgs := append(args, limit, offset)
	rows, err := r.db.DB().QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list operational events: %w", err)
	}
	defer rows.Close()

	records := make([]*OperationalEventRecord, 0, limit)
	for rows.Next() {
		var ev OperationalEventRecord
		if err := rows.Scan(
			&ev.ID, &ev.EventID, &ev.EventType, &ev.Component, &ev.NodeID,
			&ev.Status, &ev.Details, &ev.CreatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("failed to scan operational event row: %w", err)
		}
		records = append(records, &ev)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("rows error: %w", err)
	}

	return records, total, nil
}
