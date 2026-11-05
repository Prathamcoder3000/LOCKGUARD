package middleware_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lockguard/backend/internal/auth"
	"lockguard/backend/internal/metrics"
	"lockguard/backend/internal/middleware"
)

// TestRateLimiter_RawTokenNotRetained verifies Requirement 1 & 8:
// Raw JWTs or bearer credentials are NEVER retained as rate-limiter map keys.
func TestRateLimiter_RawTokenNotRetained(t *testing.T) {
	limiter := middleware.NewRateLimiter(10.0, 10.0, 100, metrics.DefaultRegistry)
	handler := middleware.RateLimitMiddleware(limiter, "test")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rawJWT := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJzZWNyZXQtdXNlciIsImV4cCI6OTk5OTk5OTk5OX0.dummy-signature-secret-credential"

	req := httptest.NewRequest(http.MethodGet, "/v1/locks", nil)
	req.Header.Set("Authorization", "Bearer "+rawJWT)
	req.RemoteAddr = "10.0.0.1:50000"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	keys := limiter.ClientKeys()
	if len(keys) == 0 {
		t.Fatalf("expected at least 1 tracked key, got 0")
	}

	for _, k := range keys {
		// Strict assertion: Raw token string must NOT appear anywhere in the limiter keys
		if strings.Contains(k, rawJWT) || strings.Contains(k, "secret-user") || strings.Contains(k, "dummy-signature") {
			t.Fatalf("SECURITY VIOLATION: Raw credential retained in limiter key: %q", k)
		}
		// Must be a cryptographic keyed fingerprint
		if !strings.HasPrefix(k, "token_hmac:") {
			t.Fatalf("expected 'token_hmac:' prefix for unverified bearer token, got %q", k)
		}
		// SHA256 hex is 64 characters + 11 ("token_hmac:") = 75 chars
		if len(k) != 75 {
			t.Fatalf("expected bounded key length of 75 chars, got %d for key %q", len(k), k)
		}
	}
}

// TestRateLimiter_ValidatedSubjectIdentityPreference verifies Requirement 2:
// Prefer a validated authenticated subject/owner identity from context where available.
func TestRateLimiter_ValidatedSubjectIdentityPreference(t *testing.T) {
	limiter := middleware.NewRateLimiter(10.0, 10.0, 100, metrics.DefaultRegistry)
	handler := middleware.RateLimitMiddleware(limiter, "test")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Inject verified claims into request context (as done by auth.Middleware after signature verification)
	claims := &auth.Claims{
		Subject: "worker-prod-42",
		Role:    auth.RoleClient,
	}
	ctx := context.WithValue(context.Background(), auth.ClaimsContextKey, claims)

	req := httptest.NewRequest(http.MethodGet, "/v1/locks", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer some-bearer-token")
	req.RemoteAddr = "192.168.1.50:40000"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	keys := limiter.ClientKeys()
	foundSubjectKey := false
	for _, k := range keys {
		if k == "sub:worker-prod-42" {
			foundSubjectKey = true
		}
		if strings.Contains(k, "some-bearer-token") {
			t.Fatalf("raw token leaked in key: %q", k)
		}
	}

	if !foundSubjectKey {
		t.Fatalf("expected key 'sub:worker-prod-42', got keys: %v", keys)
	}
}

// TestRateLimiter_FallbackToDirectConnectionAddress verifies Requirement 4:
// Use the direct connection address as a fallback for unauthenticated traffic.
func TestRateLimiter_FallbackToDirectConnectionAddress(t *testing.T) {
	limiter := middleware.NewRateLimiter(10.0, 10.0, 100, metrics.DefaultRegistry)
	handler := middleware.RateLimitMiddleware(limiter, "test")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/locks", nil)
	req.RemoteAddr = "172.16.0.99:34567"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	keys := limiter.ClientKeys()
	foundIP := false
	for _, k := range keys {
		if k == "ip:172.16.0.99" {
			foundIP = true
		}
	}
	if !foundIP {
		t.Fatalf("expected key 'ip:172.16.0.99', got %v", keys)
	}
}

// TestRateLimiter_DoNotTrustForwardedHeaders verifies Requirement 5 & 7:
// Ensure arbitrary X-Forwarded-For headers cannot manipulate rate limit identity.
func TestRateLimiter_DoNotTrustForwardedHeaders(t *testing.T) {
	limiter := middleware.NewRateLimiter(10.0, 10.0, 100, metrics.DefaultRegistry)
	handler := middleware.RateLimitMiddleware(limiter, "test")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/locks", nil)
	// Attacker attempts to spoof their IP using proxy headers
	req.Header.Set("X-Forwarded-For", "1.1.1.1, 8.8.8.8, 123.123.123.123")
	req.Header.Set("X-Real-IP", "9.9.9.9")
	req.Header.Set("Forwarded", "for=198.51.100.1")
	req.RemoteAddr = "10.20.30.40:54321"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	keys := limiter.ClientKeys()
	for _, k := range keys {
		if strings.Contains(k, "1.1.1.1") || strings.Contains(k, "8.8.8.8") ||
			strings.Contains(k, "9.9.9.9") || strings.Contains(k, "198.51.100.1") {
			t.Fatalf("SECURITY VIOLATION: Spoofed header was accepted as rate limiter key: %q", k)
		}
		if k != "ip:10.20.30.40" {
			t.Fatalf("expected strictly 'ip:10.20.30.40', got %q", k)
		}
	}
}

// TestRateLimiter_IdentityIsolation verifies Requirement 9:
// Different tokens, subjects, and IPs have isolated buckets and do not collide.
func TestRateLimiter_IdentityIsolation(t *testing.T) {
	// 2 requests capacity per client
	limiter := middleware.NewRateLimiter(1.0, 2.0, 100, metrics.DefaultRegistry)
	handler := middleware.RateLimitMiddleware(limiter, "test")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Exhaust Client A (IP: 10.0.0.1)
	for i := 0; i < 2; i++ {
		reqA := httptest.NewRequest(http.MethodGet, "/v1/locks", nil)
		reqA.RemoteAddr = "10.0.0.1:1111"
		recA := httptest.NewRecorder()
		handler.ServeHTTP(recA, reqA)
		if recA.Code != http.StatusOK {
			t.Fatalf("client A request %d failed: %d", i, recA.Code)
		}
	}
	// 3rd request from Client A must be rate limited
	reqABlocked := httptest.NewRequest(http.MethodGet, "/v1/locks", nil)
	reqABlocked.RemoteAddr = "10.0.0.1:1111"
	recABlocked := httptest.NewRecorder()
	handler.ServeHTTP(recABlocked, reqABlocked)
	if recABlocked.Code != http.StatusTooManyRequests {
		t.Fatalf("expected client A to be rate-limited (429), got %d", recABlocked.Code)
	}

	// Client B (IP: 10.0.0.2) must NOT be affected by Client A's exhaustion
	reqB := httptest.NewRequest(http.MethodGet, "/v1/locks", nil)
	reqB.RemoteAddr = "10.0.0.2:2222"
	recB := httptest.NewRecorder()
	handler.ServeHTTP(recB, reqB)
	if recB.Code != http.StatusOK {
		t.Fatalf("client B should be permitted, got %d", recB.Code)
	}

	// Client C (Authenticated Subject: worker-c) must also be isolated
	claimsC := &auth.Claims{Subject: "worker-c", Role: auth.RoleClient}
	ctxC := context.WithValue(context.Background(), auth.ClaimsContextKey, claimsC)
	reqC := httptest.NewRequest(http.MethodGet, "/v1/locks", nil).WithContext(ctxC)
	reqC.RemoteAddr = "10.0.0.1:1111" // Same IP as exhausted client A!
	recC := httptest.NewRecorder()
	handler.ServeHTTP(recC, reqC)
	if recC.Code != http.StatusOK {
		t.Fatalf("authenticated client C on same IP should have isolated limit, got %d", recC.Code)
	}
}

// TestRateLimiter_BoundedStorage verifies Requirement 6 & 9:
// Preserve bounded map capacity and idle-entry eviction under client churn.
func TestRateLimiter_BoundedStorage(t *testing.T) {
	const maxClients = 10
	limiter := middleware.NewRateLimiter(10.0, 10.0, maxClients, metrics.DefaultRegistry)

	// Insert 25 distinct client keys (exceeding maxClients of 10)
	for i := 0; i < 25; i++ {
		key := fmt.Sprintf("ip:192.168.1.%d", i)
		allowed, _ := limiter.Allow(key)
		if !allowed {
			t.Fatalf("expected client %d to be admitted under capacity eviction", i)
		}
	}

	// Invariant: Tracked clients count must NEVER exceed maxClients
	tracked := limiter.TrackedClients()
	if tracked > maxClients {
		t.Fatalf("BOUNDED STORAGE VIOLATION: tracked clients (%d) exceeded maxClients (%d)",
			tracked, maxClients)
	}
}
