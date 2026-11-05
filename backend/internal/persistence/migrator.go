package persistence

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

const (
	// AdvisoryLockKey is an arbitrary 64-bit identifier used to serialize migrations.
	AdvisoryLockKey = 74152969
)

// MigrationInfo details applied or pending migration metadata.
type MigrationInfo struct {
	Version   int       `json:"version"`
	Name      string    `json:"name"`
	AppliedAt time.Time `json:"applied_at"`
	Checksum  string    `json:"checksum"`
	Applied   bool      `json:"applied"`
}

// Migrator manages safe execution and tracking of database schema migrations.
type Migrator struct {
	db *Database
	fs embed.FS
}

// NewMigrator initializes a new schema Migrator.
func NewMigrator(db *Database) *Migrator {
	return &Migrator{
		db: db,
		fs: embeddedMigrations,
	}
}

type migrationFile struct {
	version  int
	name     string
	filename string
	content  []byte
	checksum string
}

// RunMigrations applies all pending migrations in ascending order under an advisory lock.
func (m *Migrator) RunMigrations(ctx context.Context) error {
	db := m.db.DB()
	if db == nil {
		return ErrDBUnavailable
	}

	// 1. Acquire dedicated connection from pool so session-level advisory locks
	// are guaranteed to be acquired and released on the exact same backend connection.
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("%w: failed to obtain dedicated connection for migration: %v", ErrMigrationFailed, err)
	}
	defer conn.Close()

	// 2. Acquire advisory lock on this specific connection session
	var lockAcquired bool
	err = conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1);", AdvisoryLockKey).Scan(&lockAcquired)
	if err != nil {
		return fmt.Errorf("%w: failed to request advisory lock: %v", ErrMigrationFailed, err)
	}
	if !lockAcquired {
		// Wait with context timeout for lock if another instance is holding it
		_, err = conn.ExecContext(ctx, "SELECT pg_advisory_lock($1);", AdvisoryLockKey)
		if err != nil {
			return fmt.Errorf("%w: failed to acquire advisory lock: %v", ErrMigrationFailed, err)
		}
	}
	defer func() {
		// Explicitly unlock on the exact same connection session
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1);", AdvisoryLockKey)
	}()

	// 3. Ensure schema_migrations table exists
	initSQL := `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INT PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
			checksum VARCHAR(64) NOT NULL
		);
	`
	if _, err := conn.ExecContext(ctx, initSQL); err != nil {
		return fmt.Errorf("%w: failed to ensure schema_migrations table: %v", ErrMigrationFailed, err)
	}

	// 4. Load applied migrations from database
	applied := make(map[int]MigrationInfo)
	rows, err := conn.QueryContext(ctx, "SELECT version, name, applied_at, checksum FROM schema_migrations;")
	if err != nil {
		return fmt.Errorf("%w: failed to query applied migrations: %v", ErrMigrationFailed, err)
	}
	defer rows.Close()

	for rows.Next() {
		var info MigrationInfo
		if err := rows.Scan(&info.Version, &info.Name, &info.AppliedAt, &info.Checksum); err != nil {
			return fmt.Errorf("%w: failed to scan migration row: %v", ErrMigrationFailed, err)
		}
		info.Applied = true
		applied[info.Version] = info
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("%w: applied migrations rows error: %v", ErrMigrationFailed, err)
	}

	// 5. Discover and sort embedded migration files
	files, err := m.loadMigrationFiles()
	if err != nil {
		return err
	}

	// 6. Verify checksums and apply pending migrations
	for _, mf := range files {
		if existing, ok := applied[mf.version]; ok {
			// Verify checksum matches
			if existing.Checksum != mf.checksum {
				return fmt.Errorf(
					"%w: checksum mismatch for migration %d (%s): recorded %s, current %s",
					ErrMigrationFailed, mf.version, mf.name, existing.Checksum, mf.checksum,
				)
			}
			continue
		}

		// Apply new migration in transaction on the dedicated connection
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("%w: failed to begin migration transaction: %v", ErrMigrationFailed, err)
		}

		if _, err := tx.ExecContext(ctx, string(mf.content)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("%w: migration %d (%s) execution failed: %v", ErrMigrationFailed, mf.version, mf.name, err)
		}

		insertSQL := `
			INSERT INTO schema_migrations (version, name, applied_at, checksum)
			VALUES ($1, $2, $3, $4);
		`
		if _, err := tx.ExecContext(ctx, insertSQL, mf.version, mf.name, time.Now().UTC(), mf.checksum); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("%w: recording migration %d failed: %v", ErrMigrationFailed, mf.version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("%w: committing migration %d failed: %v", ErrMigrationFailed, mf.version, err)
		}
	}

	return nil
}

// GetMigrationStatus returns the status of all known migrations.
func (m *Migrator) GetMigrationStatus(ctx context.Context) ([]MigrationInfo, error) {
	db := m.db.DB()
	if db == nil {
		return nil, ErrDBUnavailable
	}

	applied := make(map[int]MigrationInfo)
	rows, err := db.QueryContext(ctx, `
		SELECT version, name, applied_at, checksum
		FROM schema_migrations
		ORDER BY version ASC;
	`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var info MigrationInfo
			if err := rows.Scan(&info.Version, &info.Name, &info.AppliedAt, &info.Checksum); err == nil {
				info.Applied = true
				applied[info.Version] = info
			}
		}
	}

	files, err := m.loadMigrationFiles()
	if err != nil {
		return nil, err
	}

	result := make([]MigrationInfo, 0, len(files))
	for _, mf := range files {
		if ex, ok := applied[mf.version]; ok {
			result = append(result, ex)
		} else {
			result = append(result, MigrationInfo{
				Version:  mf.version,
				Name:     mf.name,
				Checksum: mf.checksum,
				Applied:  false,
			})
		}
	}

	return result, nil
}

func (m *Migrator) loadMigrationFiles() ([]migrationFile, error) {
	entries, err := m.fs.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("%w: failed to read embedded migrations dir: %v", ErrMigrationFailed, err)
	}

	var files []migrationFile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		filename := entry.Name()
		parts := strings.SplitN(filename, "_", 2)
		if len(parts) < 2 {
			continue
		}

		ver, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}

		name := strings.TrimSuffix(parts[1], ".sql")
		content, err := m.fs.ReadFile(filepath.ToSlash(filepath.Join("migrations", filename)))
		if err != nil {
			return nil, fmt.Errorf("%w: failed to read migration file %s: %v", ErrMigrationFailed, filename, err)
		}

		h := sha256.Sum256(content)
		checksum := hex.EncodeToString(h[:])

		files = append(files, migrationFile{
			version:  ver,
			name:     name,
			filename: filename,
			content:  content,
			checksum: checksum,
		})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].version < files[j].version
	})

	return files, nil
}
