package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"lockguard/backend/internal/metrics"
)

type contextKey string

const (
	ClaimsContextKey contextKey = "lockguard_auth_claims"
)

// ClaimsFromContext extracts the authenticated Claims from the request context.
func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(ClaimsContextKey).(*Claims)
	return claims, ok && claims != nil
}

// SecurityAuditCallback notifies an external audit pipeline of authentication failures and authorization denials.
type SecurityAuditCallback func(eventType, actorID, role, endpoint, reason string)

// Middleware provides HTTP authentication and RBAC authorization filters.
type Middleware struct {
	authService   *Service
	metrics       *metrics.Registry
	auditCallback SecurityAuditCallback
}

// NewMiddleware creates an authentication and authorization middleware wrapper.
func NewMiddleware(service *Service, reg *metrics.Registry) *Middleware {
	return &Middleware{
		authService: service,
		metrics:     reg,
	}
}

// SetAuditCallback binds a security audit event callback for 401/403 rejections.
func (m *Middleware) SetAuditCallback(cb SecurityAuditCallback) {
	m.auditCallback = cb
}

// Authenticate verifies the Bearer JWT or static token on incoming requests.
func (m *Middleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			m.recordAuthFailure(r.URL.Path, "missing_header")
			respondAuthError(w, http.StatusUnauthorized, "ERR_UNAUTHORIZED", "missing authorization header")
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			m.recordAuthFailure(r.URL.Path, "invalid_format")
			respondAuthError(w, http.StatusUnauthorized, "ERR_UNAUTHORIZED", "invalid authorization header format (expected 'Bearer <token>')")
			return
		}

		tokenString := strings.TrimSpace(parts[1])
		claims, err := m.authService.ValidateToken(tokenString)
		if err != nil {
			reason := "invalid_token"
			if err == ErrExpiredToken {
				reason = "expired_token"
			}
			m.recordAuthFailure(r.URL.Path, reason)
			respondAuthError(w, http.StatusUnauthorized, "ERR_UNAUTHORIZED", err.Error())
			return
		}

		m.recordAuthSuccess(claims.Role)

		// Inject verified claims into request context
		ctx := context.WithValue(r.Context(), ClaimsContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireRole enforces that the authenticated user possesses one of the allowed RBAC roles.
func (m *Middleware) RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := ClaimsFromContext(r.Context())
			if !ok {
				m.recordAuthFailure(r.URL.Path, "no_claims_in_context")
				respondAuthError(w, http.StatusUnauthorized, "ERR_UNAUTHORIZED", "authentication required")
				return
			}

			hasRole := false
			for _, role := range roles {
				if strings.EqualFold(claims.Role, role) {
					hasRole = true
					break
				}
			}

			if !hasRole {
				m.recordAuthzDenial(r.URL.Path, claims.Subject, claims.Role, "insufficient_role")
				respondAuthError(w, http.StatusForbidden, "ERR_FORBIDDEN", "insufficient role permissions for this endpoint")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// EnforceOwnerIsolation prevents clients from mutating locks or sessions belonging to another client.
// Reads the JSON request body (buffering it so downstream handlers can re-read it) and verifies owner_id.
func (m *Middleware) EnforceOwnerIsolation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := ClaimsFromContext(r.Context())
		if !ok {
			respondAuthError(w, http.StatusUnauthorized, "ERR_UNAUTHORIZED", "authentication required")
			return
		}

		// Admins are exempt from owner isolation restrictions
		if claims.Role == RoleAdmin {
			next.ServeHTTP(w, r)
			return
		}

		// Read-Only role cannot mutate locks
		if claims.Role == RoleReadOnly && (r.Method == http.MethodPost || r.Method == http.MethodDelete || r.Method == http.MethodPut) {
			m.recordAuthzDenial(r.URL.Path, claims.Subject, claims.Role, "read_only_mutation_denied")
			respondAuthError(w, http.StatusForbidden, "ERR_FORBIDDEN", "read-only role cannot execute mutating operations")
			return
		}

		// For CLIENT role on mutating operations: inspect body owner_id
		if claims.Role == RoleClient && (r.Method == http.MethodPost || r.Method == http.MethodDelete) {
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				respondAuthError(w, http.StatusBadRequest, "ERR_BAD_REQUEST", "failed to read request body")
				return
			}
			// Rewind request body for downstream handlers
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

			if len(bodyBytes) > 0 {
				var payload struct {
					OwnerID  string `json:"owner_id"`
					ClientID string `json:"client_id"`
				}
				if err := json.Unmarshal(bodyBytes, &payload); err == nil {
					targetOwner := payload.OwnerID
					if targetOwner == "" {
						targetOwner = payload.ClientID
					}

					if targetOwner != "" && !strings.EqualFold(targetOwner, claims.Subject) {
						m.recordAuthzDenial(r.URL.Path, claims.Subject, claims.Role, "cross_owner_isolation")
						respondAuthError(w, http.StatusForbidden, "ERR_OWNER_MISMATCH", "authenticated client identity mismatch with requested owner ID")
						return
					}
				}
			}
		}

		next.ServeHTTP(w, r)
	})
}

func (m *Middleware) recordAuthSuccess(role string) {
	if m.metrics != nil {
		m.metrics.IncCounter("lockguard_auth_successes_total", map[string]string{
			"role": strings.ToLower(role),
		})
	}
}

func (m *Middleware) recordAuthFailure(endpoint, reason string) {
	if m.metrics != nil {
		m.metrics.IncCounter("lockguard_auth_failures_total", map[string]string{
			"reason": reason,
		})
	}
	if m.auditCallback != nil {
		m.auditCallback("AUTHENTICATION_FAILURE", "", "", endpoint, reason)
	}
}

func (m *Middleware) recordAuthzDenial(endpoint, subject, role, reason string) {
	if m.metrics != nil {
		m.metrics.IncCounter("lockguard_authz_denials_total", map[string]string{
			"role": strings.ToLower(role),
		})
	}
	if m.auditCallback != nil {
		m.auditCallback("AUTHORIZATION_DENIAL", subject, role, endpoint, reason)
	}
}

func respondAuthError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false,
		"error": map[string]interface{}{
			"code":    code,
			"message": message,
		},
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

