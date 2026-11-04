package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"lockguard/backend/internal/config"
)

// PostgresDB manages connection pooling and health checks for PostgreSQL.
// ARCHITECTURAL INVARIANT: Authoritative distributed lock state is strictly
// replicated via Raft and held in the in-memory state machine / Raft log store.
// PostgreSQL is used ONLY for users, roles, audit events, benchmark runs,
// dashboard metadata, and system configuration.
type PostgresDB struct {
	db  *sql.DB
	cfg *config.DBConfig
}

// NewPostgresDB establishes and verifies a connection pool to PostgreSQL.
func NewPostgresDB(cfg *config.DBConfig) (*PostgresDB, error) {
	connStr := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.DBName, cfg.SSLMode,
	)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize postgres connection: %w", err)
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetimeMinutes)

	return &PostgresDB{
		db:  db,
		cfg: cfg,
	}, nil
}

// Ping checks if the PostgreSQL database is reachable.
func (p *PostgresDB) Ping(ctx context.Context) error {
	if p.db == nil {
		return fmt.Errorf("database handle is nil")
	}
	ctxTimeout, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return p.db.PingContext(ctxTimeout)
}

// Close gracefully closes all database connections in the pool.
func (p *PostgresDB) Close() error {
	if p.db != nil {
		return p.db.Close()
	}
	return nil
}

// DB returns the underlying sql.DB reference.
func (p *PostgresDB) DB() *sql.DB {
	return p.db
}
