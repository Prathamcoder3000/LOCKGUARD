package persistence

import "errors"

var (
	// ErrDBUnavailable indicates PostgreSQL is unreachable or connection timed out.
	ErrDBUnavailable = errors.New("database unavailable")

	// ErrDuplicateEvent indicates an event with this unique event_id already exists.
	ErrDuplicateEvent = errors.New("duplicate event: event_id already exists")

	// ErrEventNotFound indicates the requested audit or operational event does not exist.
	ErrEventNotFound = errors.New("event not found")

	// ErrInvalidFilter indicates supplied query filter parameters are malformed.
	ErrInvalidFilter = errors.New("invalid query filter")

	// ErrMigrationFailed indicates a database migration failed to apply.
	ErrMigrationFailed = errors.New("database schema migration failed")

	// ErrTransactionFailed indicates a transactional database operation could not be completed.
	ErrTransactionFailed = errors.New("database transaction failed")
)

