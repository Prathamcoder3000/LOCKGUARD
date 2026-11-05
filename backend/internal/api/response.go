package api

import (
	"encoding/json"
	"net/http"
	"time"
)

// Standard error codes for centralized LockGuard API errors.
const (
	ErrCodeInternal          = "ERR_INTERNAL"
	ErrCodeBadRequest        = "ERR_BAD_REQUEST"
	ErrCodeNotFound          = "ERR_NOT_FOUND"
	ErrCodeUnauthorized      = "ERR_UNAUTHORIZED"
	ErrCodeForbidden         = "ERR_FORBIDDEN"
	ErrCodeConflict          = "ERR_CONFLICT"
	ErrCodeNotReady          = "ERR_NOT_READY"
	ErrCodeNotLeader         = "ERR_NOT_LEADER"
	ErrCodeQuorumUnavailable = "ERR_QUORUM_UNAVAILABLE"
	ErrCodeLockHeld          = "ERR_LOCK_HELD"
	ErrCodeLeaseExpired      = "ERR_LEASE_EXPIRED"
	ErrCodeFencingTokenStale = "ERR_FENCING_TOKEN_STALE"
	ErrCodeMethodNotAllowed  = "ERR_METHOD_NOT_ALLOWED"
	ErrCodeOwnerMismatch     = "ERR_OWNER_MISMATCH"
	ErrCodeRateLimitExceeded = "ERR_RATE_LIMIT_EXCEEDED"
)

// APIResponse represents the standard success JSON envelope.
type APIResponse struct {
	Success   bool        `json:"success"`
	Data      interface{} `json:"data,omitempty"`
	Timestamp string      `json:"timestamp"`
}

// ErrorResponse represents the standard error JSON envelope.
type ErrorResponse struct {
	Success   bool        `json:"success"`
	Error     ErrorDetail `json:"error"`
	Timestamp string      `json:"timestamp"`
}

// ErrorDetail describes the error reason and optional structured details.
type ErrorDetail struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

// RespondJSON marshals data to JSON and writes it with the provided HTTP status code.
func RespondJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// RespondSuccess formats and writes a successful API response.
func RespondSuccess(w http.ResponseWriter, status int, data interface{}) {
	payload := APIResponse{
		Success:   true,
		Data:      data,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}
	RespondJSON(w, status, payload)
}

// RespondError formats and writes a standardized error API response.
func RespondError(w http.ResponseWriter, status int, code, message string, details interface{}) {
	payload := ErrorResponse{
		Success: false,
		Error: ErrorDetail{
			Code:    code,
			Message: message,
			Details: details,
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}
	RespondJSON(w, status, payload)
}
