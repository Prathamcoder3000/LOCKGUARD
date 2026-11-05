package lockstate

import "errors"

// Standard Error Codes for LockState Machine
const (
	CodeInvalidCommand       = "ERR_INVALID_COMMAND"
	CodeInvalidResource      = "ERR_INVALID_RESOURCE"
	CodeInvalidOwner         = "ERR_INVALID_OWNER"
	CodeInvalidTTL           = "ERR_INVALID_TTL"
	CodeInvalidTimestamp     = "ERR_INVALID_TIMESTAMP"
	CodeLockAlreadyHeld      = "ERR_LOCK_ALREADY_HELD"
	CodeLockNotFound         = "ERR_LOCK_NOT_FOUND"
	CodeNotOwner             = "ERR_NOT_OWNER"
	CodeFencingTokenMismatch = "ERR_FENCING_TOKEN_MISMATCH"
	CodeLeaseExpired         = "ERR_LEASE_EXPIRED"
	CodeIdempotencyConflict  = "ERR_IDEMPOTENCY_CONFLICT"
	CodeTokenExhausted       = "ERR_TOKEN_EXHAUSTED"
	CodeVersionExhausted     = "ERR_VERSION_EXHAUSTED"
	CodeInvalidExpiration    = "ERR_INVALID_EXPIRATION"
	CodeLeaseVersionMismatch = "ERR_LEASE_VERSION_MISMATCH"
	CodeArithmeticOverflow   = "ERR_ARITHMETIC_OVERFLOW"
	CodeInternalState        = "ERR_INTERNAL_STATE"
)

var (
	// ErrInvalidCommand is returned when an unknown or unsupported command is dispatched.
	ErrInvalidCommand = errors.New("invalid or unsupported command")

	// ErrInvalidResource is returned when the resource name is empty or contains invalid characters.
	ErrInvalidResource = errors.New("resource name cannot be empty")

	// ErrInvalidOwner is returned when the owner ID is empty or invalid.
	ErrInvalidOwner = errors.New("owner ID cannot be empty")

	// ErrInvalidTTL is returned when the lease TTL is zero or negative.
	ErrInvalidTTL = errors.New("lease TTL must be strictly positive")

	// ErrInvalidTimestamp is returned when a command carries a non-positive or missing timestamp.
	ErrInvalidTimestamp = errors.New("command timestamp must be strictly positive Unix nanoseconds")

	// ErrLockAlreadyHeld is returned when a resource is actively held under an unexpired lease.
	ErrLockAlreadyHeld = errors.New("lock is currently held under an active lease")

	// ErrLockNotFound is returned when operating on a resource that has no active lock.
	ErrLockNotFound = errors.New("no active lock found for the specified resource")

	// ErrNotOwner is returned when the requesting owner does not match the active lease owner.
	ErrNotOwner = errors.New("caller is not the active lock owner")

	// ErrFencingTokenMismatch is returned when the supplied fencing token does not match the active token.
	ErrFencingTokenMismatch = errors.New("supplied fencing token does not match active lease token")

	// ErrLeaseExpired is returned when attempting to renew or release an expired lease.
	ErrLeaseExpired = errors.New("lease has already expired")

	// ErrIdempotencyConflict is returned when a RequestID is reused with a differing command payload.
	ErrIdempotencyConflict = errors.New("idempotency conflict: request ID was previously processed with different parameters")

	// ErrTokenExhausted is returned in the extreme edge case that uint64 fencing token space is exhausted.
	ErrTokenExhausted = errors.New("fencing token counter overflow: token space exhausted")

	// ErrVersionExhausted is returned in the extreme edge case that uint64 lease version space is exhausted.
	ErrVersionExhausted = errors.New("lease version counter overflow: version space exhausted")

	// ErrInvalidExpiration is returned when an expire command arrives before the lease expiration time.
	ErrInvalidExpiration = errors.New("cannot expire lease before its expiration timestamp has elapsed")

	// ErrLeaseVersionMismatch is returned when an expire command's expected version or expiration timestamp does not match the active lease.
	ErrLeaseVersionMismatch = errors.New("expected lease version or expiration timestamp does not match active lease")

	// ErrArithmeticOverflow is returned when timestamp plus TTL arithmetic exceeds maximum integer bounds.
	ErrArithmeticOverflow = errors.New("arithmetic overflow: timestamp plus TTL exceeds maximum integer bound")

	// ErrInternalState is returned if an unexpected internal state corruption is detected.
	ErrInternalState = errors.New("internal state machine invariant violation")
)

// ErrorCode maps a sentinel error to its standardized string code.
func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, ErrInvalidCommand):
		return CodeInvalidCommand
	case errors.Is(err, ErrInvalidResource):
		return CodeInvalidResource
	case errors.Is(err, ErrInvalidOwner):
		return CodeInvalidOwner
	case errors.Is(err, ErrInvalidTTL):
		return CodeInvalidTTL
	case errors.Is(err, ErrInvalidTimestamp):
		return CodeInvalidTimestamp
	case errors.Is(err, ErrLockAlreadyHeld):
		return CodeLockAlreadyHeld
	case errors.Is(err, ErrLockNotFound):
		return CodeLockNotFound
	case errors.Is(err, ErrNotOwner):
		return CodeNotOwner
	case errors.Is(err, ErrFencingTokenMismatch):
		return CodeFencingTokenMismatch
	case errors.Is(err, ErrLeaseExpired):
		return CodeLeaseExpired
	case errors.Is(err, ErrIdempotencyConflict):
		return CodeIdempotencyConflict
	case errors.Is(err, ErrTokenExhausted):
		return CodeTokenExhausted
	case errors.Is(err, ErrVersionExhausted):
		return CodeVersionExhausted
	case errors.Is(err, ErrInvalidExpiration):
		return CodeInvalidExpiration
	case errors.Is(err, ErrLeaseVersionMismatch):
		return CodeLeaseVersionMismatch
	case errors.Is(err, ErrArithmeticOverflow):
		return CodeArithmeticOverflow
	default:
		return CodeInternalState
	}
}

// ErrorFromCode maps a standardized error code string back to its sentinel error.
func ErrorFromCode(code string) error {
	switch code {
	case CodeInvalidCommand:
		return ErrInvalidCommand
	case CodeInvalidResource:
		return ErrInvalidResource
	case CodeInvalidOwner:
		return ErrInvalidOwner
	case CodeInvalidTTL:
		return ErrInvalidTTL
	case CodeInvalidTimestamp:
		return ErrInvalidTimestamp
	case CodeLockAlreadyHeld:
		return ErrLockAlreadyHeld
	case CodeLockNotFound:
		return ErrLockNotFound
	case CodeNotOwner:
		return ErrNotOwner
	case CodeFencingTokenMismatch:
		return ErrFencingTokenMismatch
	case CodeLeaseExpired:
		return ErrLeaseExpired
	case CodeIdempotencyConflict:
		return ErrIdempotencyConflict
	case CodeTokenExhausted:
		return ErrTokenExhausted
	case CodeVersionExhausted:
		return ErrVersionExhausted
	case CodeInvalidExpiration:
		return ErrInvalidExpiration
	case CodeLeaseVersionMismatch:
		return ErrLeaseVersionMismatch
	case CodeArithmeticOverflow:
		return ErrArithmeticOverflow
	case CodeInternalState:
		return ErrInternalState
	default:
		if code == "" {
			return nil
		}
		return errors.New(code)
	}
}
