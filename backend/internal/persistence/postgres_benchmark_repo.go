package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// BenchmarkRunRecord combines a benchmark_runs row with its optional benchmark_results summary.
type BenchmarkRunRecord struct {
	ID               string          `json:"id"`
	Title            string          `json:"title"`
	Scenario         string          `json:"scenario"`
	ConcurrencyLevel int             `json:"concurrency_level"`
	TotalOperations  int             `json:"total_operations"`
	TargetCluster    string          `json:"target_cluster"`
	Status           string          `json:"status"`
	StartedAt        *time.Time      `json:"started_at,omitempty"`
	CompletedAt      *time.Time      `json:"completed_at,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	ThroughputRPS    float64         `json:"throughput_rps"`
	P50LatencyMs     float64         `json:"p50_latency_ms"`
	P95LatencyMs     float64         `json:"p95_latency_ms"`
	P99LatencyMs     float64         `json:"p99_latency_ms"`
	MinLatencyMs     float64         `json:"min_latency_ms"`
	MaxLatencyMs     float64         `json:"max_latency_ms"`
	SuccessCount     int64           `json:"success_count"`
	ErrorCount       int64           `json:"error_count"`
	MetricsSummary   json.RawMessage `json:"metrics_summary,omitempty"`
}

// BenchmarkRepository defines persistence operations for benchmark runs and results.
type BenchmarkRepository interface {
	CreateRun(ctx context.Context, run *BenchmarkRunRecord) (string, error)
	SaveResult(ctx context.Context, runID string, status string, completedAt time.Time, result *BenchmarkRunRecord) error
	ListRuns(ctx context.Context, limit int) ([]*BenchmarkRunRecord, error)
}

// PostgresBenchmarkRepo implements BenchmarkRepository using PostgreSQL.
type PostgresBenchmarkRepo struct {
	db *Database
}

// NewPostgresBenchmarkRepo constructs a PostgreSQL-backed BenchmarkRepository.
func NewPostgresBenchmarkRepo(db *Database) *PostgresBenchmarkRepo {
	return &PostgresBenchmarkRepo{db: db}
}

// CreateRun inserts a new running benchmark execution row and returns its UUID.
func (r *PostgresBenchmarkRepo) CreateRun(ctx context.Context, run *BenchmarkRunRecord) (string, error) {
	if r == nil || r.db == nil || r.db.DB() == nil {
		return "", ErrDBUnavailable
	}
	if run == nil {
		return "", fmt.Errorf("benchmark run record cannot be nil")
	}

	now := time.Now().UTC()
	startedAt := now
	if run.StartedAt != nil {
		startedAt = *run.StartedAt
	}

	var id string
	err := r.db.DB().QueryRowContext(ctx, `
		INSERT INTO benchmark_runs (
			title, scenario, concurrency_level, total_operations, target_cluster, status, started_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id::text;
	`,
		run.Title, run.Scenario, run.ConcurrencyLevel, run.TotalOperations,
		run.TargetCluster, run.Status, startedAt, now,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("failed to create benchmark run: %w", err)
	}
	run.ID = id
	run.CreatedAt = now
	return id, nil
}

// SaveResult updates a benchmark run's terminal status and records its computed latency/throughput metrics.
func (r *PostgresBenchmarkRepo) SaveResult(ctx context.Context, runID, status string, completedAt time.Time, result *BenchmarkRunRecord) error {
	if r == nil || r.db == nil || r.db.DB() == nil {
		return ErrDBUnavailable
	}
	if runID == "" {
		return fmt.Errorf("runID is required")
	}

	summary := json.RawMessage("{}")
	if result != nil && len(result.MetricsSummary) > 0 {
		summary = result.MetricsSummary
	}

	return r.db.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			UPDATE benchmark_runs
			SET status = $1, completed_at = $2
			WHERE id = $3::uuid;
		`, status, completedAt.UTC(), runID); err != nil {
			return fmt.Errorf("failed to update benchmark run %s: %w", runID, err)
		}

		if result != nil {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO benchmark_results (
					benchmark_run_id, throughput_rps, p50_latency_ms, p95_latency_ms, p99_latency_ms,
					min_latency_ms, max_latency_ms, success_count, error_count, metrics_summary
				) VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10);
			`,
				runID, result.ThroughputRPS, result.P50LatencyMs, result.P95LatencyMs, result.P99LatencyMs,
				result.MinLatencyMs, result.MaxLatencyMs, result.SuccessCount, result.ErrorCount, summary,
			); err != nil {
				return fmt.Errorf("failed to insert benchmark result for %s: %w", runID, err)
			}
		}
		return nil
	})
}

// ListRuns returns recent benchmark runs joined with their persisted metrics ordered newest first.
func (r *PostgresBenchmarkRepo) ListRuns(ctx context.Context, limit int) ([]*BenchmarkRunRecord, error) {
	if r == nil || r.db == nil || r.db.DB() == nil {
		return nil, ErrDBUnavailable
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	rows, err := r.db.DB().QueryContext(ctx, `
		SELECT
			br.id::text,
			br.title,
			br.scenario,
			br.concurrency_level,
			br.total_operations,
			br.target_cluster,
			br.status,
			br.started_at,
			br.completed_at,
			br.created_at,
			COALESCE(res.throughput_rps, 0),
			COALESCE(res.p50_latency_ms, 0),
			COALESCE(res.p95_latency_ms, 0),
			COALESCE(res.p99_latency_ms, 0),
			COALESCE(res.min_latency_ms, 0),
			COALESCE(res.max_latency_ms, 0),
			COALESCE(res.success_count, 0),
			COALESCE(res.error_count, 0),
			COALESCE(res.metrics_summary, '{}'::jsonb)
		FROM benchmark_runs br
		LEFT JOIN LATERAL (
			SELECT throughput_rps, p50_latency_ms, p95_latency_ms, p99_latency_ms,
			       min_latency_ms, max_latency_ms, success_count, error_count, metrics_summary
			FROM benchmark_results r2
			WHERE r2.benchmark_run_id = br.id
			ORDER BY r2.id DESC
			LIMIT 1
		) res ON true
		ORDER BY br.created_at DESC, br.id DESC
		LIMIT $1;
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query benchmark runs: %w", err)
	}
	defer rows.Close()

	runs := make([]*BenchmarkRunRecord, 0, limit)
	for rows.Next() {
		var rec BenchmarkRunRecord
		var startedAt, completedAt sql.NullTime
		var rawSummary []byte

		if err := rows.Scan(
			&rec.ID, &rec.Title, &rec.Scenario, &rec.ConcurrencyLevel, &rec.TotalOperations,
			&rec.TargetCluster, &rec.Status, &startedAt, &completedAt, &rec.CreatedAt,
			&rec.ThroughputRPS, &rec.P50LatencyMs, &rec.P95LatencyMs, &rec.P99LatencyMs,
			&rec.MinLatencyMs, &rec.MaxLatencyMs, &rec.SuccessCount, &rec.ErrorCount, &rawSummary,
		); err != nil {
			return nil, fmt.Errorf("failed to scan benchmark run row: %w", err)
		}
		if startedAt.Valid {
			t := startedAt.Time
			rec.StartedAt = &t
		}
		if completedAt.Valid {
			t := completedAt.Time
			rec.CompletedAt = &t
		}
		if len(rawSummary) > 0 {
			rec.MetricsSummary = json.RawMessage(rawSummary)
		}
		runs = append(runs, &rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("benchmark runs iteration error: %w", err)
	}
	return runs, nil
}
