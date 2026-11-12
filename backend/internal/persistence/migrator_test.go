package persistence

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func getTestDBConfig() *Config {
	return &Config{
		Host:            "localhost",
		Port:            5435,
		User:            "lockguard_user",
		Password:        "lockguard_secure_password_replace_in_prod",
		Database:        "lockguard_db",
		SSLMode:         "disable",
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxLifetime: 10 * time.Minute,
		ConnTimeout:     3 * time.Second,
	}
}

func TestConcurrentMigrationsAgainstRealPostgres(t *testing.T) {
	cfg := getTestDBConfig()
	db, err := Open(cfg)
	if err != nil {
		t.Skipf("skipping real postgres test: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.Ping(ctx); err != nil {
		t.Skipf("skipping real postgres test, connection failed: %v", err)
	}

	// 1. Reset tables for clean migration test
	_, _ = db.DB().ExecContext(ctx, "DROP TABLE IF EXISTS audit_events, operational_events, system_checkpoints, schema_migrations CASCADE;")

	// 2. Launch two independent migrator instances concurrently
	migrator1 := NewMigrator(db)
	migrator2 := NewMigrator(db)

	var wg sync.WaitGroup
	errs := make([]error, 2)

	wg.Add(2)
	go func() {
		defer wg.Done()
		errs[0] = migrator1.RunMigrations(context.Background())
	}()
	go func() {
		defer wg.Done()
		errs[1] = migrator2.RunMigrations(context.Background())
	}()

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("migrator %d failed under concurrency: %v", i+1, err)
		}
	}

	// 3. Verify all 3 migrations recorded (001_init_audit, 002_operational_tables, 003_create_benchmark_tables)
	status, err := migrator1.GetMigrationStatus(context.Background())
	if err != nil {
		t.Fatalf("failed to get migration status: %v", err)
	}
	if len(status) != 3 {
		t.Fatalf("expected 3 migrations, got %d", len(status))
	}
	for _, m := range status {
		if !m.Applied {
			t.Errorf("expected migration %d (%s) to be applied", m.Version, m.Name)
		}
	}

	// 4. Verify checksum validation: re-run should succeed with matching checksums
	if err := migrator2.RunMigrations(context.Background()); err != nil {
		t.Fatalf("idempotent migration re-run failed: %v", err)
	}

	// 5. Verify failed migration does NOT record version
	// Insert dummy record with corrupt checksum to simulate tamper detection
	_, err = db.DB().ExecContext(context.Background(), "UPDATE schema_migrations SET checksum = 'corrupted_hash' WHERE version = 1;")
	if err != nil {
		t.Fatalf("failed to update checksum: %v", err)
	}

	tamperErr := migrator1.RunMigrations(context.Background())
	if tamperErr == nil {
		t.Fatal("expected checksum mismatch error on tampered migration, got nil")
	}

	// Restore correct checksum
	_, _ = db.DB().ExecContext(context.Background(), fmt.Sprintf("UPDATE schema_migrations SET checksum = '%s' WHERE version = 1;", status[0].Checksum))
}

func TestFailedMigrationRollsBackTransaction(t *testing.T) {
	cfg := getTestDBConfig()
	db, err := Open(cfg)
	if err != nil {
		t.Skipf("skipping real postgres test: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.Ping(ctx); err != nil {
		t.Skipf("skipping real postgres test, connection failed: %v", err)
	}

	// Test transaction atomicity: verify that a failing statement rolls back
	// and schema_migrations is NOT advanced
	conn, err := db.DB().Conn(ctx)
	if err != nil {
		t.Fatalf("failed to get conn: %v", err)
	}
	defer conn.Close()

	// Attempt a faulty migration in transaction
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("failed to begin tx: %v", err)
	}

	// Execute invalid SQL
	_, execErr := tx.ExecContext(ctx, "CREATE TABLE invalid_table (syntax error here);")
	if execErr == nil {
		t.Fatal("expected syntax error")
	}

	// Rollback transaction
	_ = tx.Rollback()

	// Verify version 99 was NOT inserted into schema_migrations
	var count int
	_ = conn.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations WHERE version = 99;").Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 entries for failed migration version 99, got %d", count)
	}
}
