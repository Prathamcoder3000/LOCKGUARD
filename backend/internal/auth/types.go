package auth

import (
	"errors"
	"strings"
	"time"
)

// Role definitions for Role-Based Access Control (RBAC).
const (
	RoleAdmin    = "ADMIN"
	RoleClient   = "CLIENT"
	RoleReadOnly = "READ_ONLY"
)

var (
	ErrMissingCredentials    = errors.New("missing or empty credentials")
	ErrInvalidCredentials    = errors.New("invalid username or password")
	ErrMissingAuthHeader     = errors.New("missing or malformed Authorization header")
	ErrInvalidToken          = errors.New("invalid or tampered authentication token")
	ErrExpiredToken          = errors.New("authentication token has expired")
	ErrNotYetValid           = errors.New("authentication token is not yet valid (nbf violation)")
	ErrInvalidSignature      = errors.New("token signature verification failed")
	ErrInvalidAlgorithm      = errors.New("unsupported or insecure token algorithm (only HS256 accepted)")
	ErrInvalidClaims         = errors.New("token claims validation failed: missing or invalid subject, role, issuer, or audience")
	ErrForbidden             = errors.New("insufficient permissions for this operation")
	ErrOwnerMismatch         = errors.New("authenticated identity does not match operation owner ID")
	ErrCrossClientViolation  = errors.New("cross-client session or lock access is strictly prohibited")
	ErrReadOnlyMutation      = errors.New("read-only role is not permitted to execute mutating operations")
)

// ClockSkewToleranceSeconds defines the bounded clock-skew allowance for token timestamp validation.
const ClockSkewToleranceSeconds = 60

// Claims models the standard and LockGuard-specific JWT payload.
type Claims struct {
	Subject   string `json:"sub"`           // User ID or Client Owner ID
	Username  string `json:"name"`          // Human-readable username or service identity
	Role      string `json:"role"`          // ADMIN | CLIENT | READ_ONLY
	Issuer    string `json:"iss,omitempty"` // "lockguard"
	Audience  string `json:"aud,omitempty"` // "lockguard-api"
	IssuedAt  int64  `json:"iat"`           // Unix seconds
	ExpiresAt int64  `json:"exp"`           // Unix seconds
	NotBefore int64  `json:"nbf,omitempty"` // Unix seconds (RFC 7519 not-before assertion)
}

// Valid performs standard RFC 7519 time validation, audience, issuer, subject, and role validation.
func (c *Claims) Valid(now time.Time) error {
	nowUnix := now.Unix()

	// 1. Mandatory claims presence
	if strings.TrimSpace(c.Subject) == "" || strings.TrimSpace(c.Role) == "" {
		return ErrInvalidClaims
	}

	// 2. Role validity
	role := strings.ToUpper(strings.TrimSpace(c.Role))
	if role != RoleAdmin && role != RoleClient && role != RoleReadOnly {
		return ErrInvalidClaims
	}

	// 3. Expiration assertion (with strict evaluation)
	if c.ExpiresAt > 0 && nowUnix > c.ExpiresAt {
		return ErrExpiredToken
	}

	// 4. Not-Before assertion (with bounded clock-skew tolerance)
	if c.NotBefore > 0 && nowUnix < (c.NotBefore-ClockSkewToleranceSeconds) {
		return ErrNotYetValid
	}

	// 5. Issued-At assertion (future-dated token prevention with bounded clock skew)
	if c.IssuedAt > 0 && nowUnix < (c.IssuedAt-ClockSkewToleranceSeconds) {
		return ErrInvalidToken
	}

	// 6. Issuer and Audience validation when present
	if c.Issuer != "" && c.Issuer != "lockguard" {
		return ErrInvalidClaims
	}
	if c.Audience != "" && c.Audience != "lockguard-api" {
		return ErrInvalidClaims
	}

	return nil
}

// UserAccount represents a registered system identity.
type UserAccount struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Salt         string    `json:"-"`
	Role         string    `json:"role"`
	OwnerID      string    `json:"owner_id,omitempty"` // For CLIENT role: restricted owner scope
	CreatedAt    time.Time `json:"created_at"`
}

// LoginRequest defines credentials submitted for authentication.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse defines the JWT response upon successful authentication.
type LoginResponse struct {
	Token     string    `json:"token"`
	TokenType string    `json:"token_type"` // "Bearer"
	ExpiresIn int64     `json:"expires_in"` // seconds
	User      UserBrief `json:"user"`
}

// UserBrief provides client-facing user information.
type UserBrief struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	OwnerID  string `json:"owner_id,omitempty"`
}

