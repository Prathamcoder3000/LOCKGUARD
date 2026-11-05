package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "github.com/lib/pq"
)

// HealthStatus encapsulates connection pool metrics and database latency diagnostics.
type HealthStatus struct {
	Available       bool          `json:"available"`
	Latency         time.Duration `json:"latency_ms"`
	OpenConnections int           `json:"open_connections"`
	InUse           int           `json:"in_use"`
	Idle            int           `json:"idle"`
	WaitCount       int64         `json:"wait_count"`
	WaitDuration    time.Duration `json:"wait_duration_ms"`
	MaxOpenConns    int           `json:"max_open_conns"`
	Error           string        `json:"error,omitempty"`
	CheckedAt       time.Time     `json:"checked_at"`
}

// Database manages connection pooling and query lifecycles for PostgreSQL persistence.
type Database struct {
	cfg *Config
	db  *sql.DB
	mu  sync.RWMutex
}

// Open initializes the PostgreSQL connection pool using the provided configuration.
func Open(cfg *Config) (*Database, error) {
	if cfg == nil {
		return nil, fmt.Errorf("persistence configuration is required")
	}

	db, err := sql.Open("postgres", cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("%w: failed to open sql connection: %v", ErrDBUnavailable, err)
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	return &Database{
		cfg: cfg,
		db:  db,
	}, nil
}

// DB returns the underlying *sql.DB instance.
func (d *Database) DB() *sql.DB {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.db
}

// Ping verifies active connectivity to PostgreSQL within the supplied context timeout.
func (d *Database) Ping(ctx context.Context) error {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.db == nil {
		return ErrDBUnavailable
	}
	if err := d.db.PingContext(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrDBUnavailable, err)
	}
	return nil
}

// HealthCheck executes a lightweight probe and reports pool utilization statistics.
func (d *Database) HealthCheck(ctx context.Context) HealthStatus {
	start := time.Now()
	status := HealthStatus{
		CheckedAt:    start,
		MaxOpenConns: d.cfg.MaxOpenConns,
	}

	d.mu.RLock()
	db := d.db
	d.mu.RUnlock()

	if db == nil {
		status.Available = false
		status.Error = "database connection pool uninitialized"
		return status
	}

	stats := db.Stats()
	status.OpenConnections = stats.OpenConnections
	status.InUse = stats.InUse
	status.Idle = stats.Idle
	status.WaitCount = stats.WaitCount
	status.WaitDuration = stats.WaitDuration

	ctxTimeout, cancel := context.WithTimeout(ctx, d.cfg.ConnTimeout)
	defer cancel()

	if err := db.PingContext(ctxTimeout); err != nil {
		status.Available = false
		status.Error = err.Error()
		status.Latency = time.Since(start)
		return status
	}

	status.Available = true
	status.Latency = time.Since(start)
	return status
}

// WithTx executes a callback within a managed transaction, committing on success and rolling back on error or panic.
func (d *Database) WithTx(ctx context.Context, fn func(tx *sql.Tx) error) (err error) {
	d.mu.RLock()
	db := d.db
	d.mu.RUnlock()

	if db == nil {
		return ErrDBUnavailable
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%w: begin failed: %v", ErrTransactionFailed, err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		} else if err != nil {
			_ = tx.Rollback()
		} else {
			if commitErr := tx.Commit(); commitErr != nil {
				err = fmt.Errorf("%w: commit failed: %v", ErrTransactionFailed, commitErr)
			}
		}
	}()

	err = fn(tx)
	return err
}

// Close gracefully closes the connection pool.
func (d *Database) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.db != nil {
		err := d.db.Close()
		d.db = nil
		return err
	}
	return nil
}

