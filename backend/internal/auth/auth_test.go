package auth_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lockguard/backend/internal/auth"
	"lockguard/backend/internal/metrics"
)

func TestAuth_JWTGenerationAndValidation(t *testing.T) {
	secret := "test-secret-key-minimum-32-chars-long!"
	claims := auth.Claims{
		Subject:   "worker-alpha",
		Username:  "worker-user",
		Role:      auth.RoleClient,
		Issuer:    "lockguard",
		Audience:  "lockguard-api",
		IssuedAt:  time.Now().Unix(),
		ExpiresAt: time.Now().Add(1 * time.Hour).Unix(),
	}

	token, err := auth.GenerateJWT(claims, secret)
	if err != nil {
		t.Fatalf("GenerateJWT failed: %v", err)
	}

	parsed, err := auth.ValidateJWT(token, secret)
	if err != nil {
		t.Fatalf("ValidateJWT failed: %v", err)
	}

	if parsed.Subject != "worker-alpha" || parsed.Role != auth.RoleClient {
		t.Fatalf("claims mismatch: %+v", parsed)
	}
}

func TestAuth_RejectTamperedSignature(t *testing.T) {
	secret := "test-secret-key-minimum-32-chars-long!"
	claims := auth.Claims{
		Subject:   "worker-alpha",
		Role:      auth.RoleClient,
		ExpiresAt: time.Now().Add(1 * time.Hour).Unix(),
	}
	token, _ := auth.GenerateJWT(claims, secret)

	tamperedToken := token[:len(token)-5] + "XXXXX"
	_, err := auth.ValidateJWT(tamperedToken, secret)
	if err != auth.ErrInvalidSignature && err != auth.ErrInvalidToken {
		t.Fatalf("expected ErrInvalidSignature, got: %v", err)
	}
}

func TestAuth_RejectExpiredToken(t *testing.T) {
	secret := "test-secret-key-minimum-32-chars-long!"
	claims := auth.Claims{
		Subject:   "worker-expired",
		Role:      auth.RoleClient,
		ExpiresAt: time.Now().Add(-10 * time.Minute).Unix(), // expired 10m ago
	}
	token, _ := auth.GenerateJWT(claims, secret)

	_, err := auth.ValidateJWT(token, secret)
	if err != auth.ErrExpiredToken {
		t.Fatalf("expected ErrExpiredToken, got: %v", err)
	}
}

func TestAuth_Service_AuthenticationAndRBAC(t *testing.T) {
	service := auth.NewService("test-secret-key-minimum-32-chars-long!", 1*time.Hour, "admin-test-token")

	// 1. Valid Admin Login
	adminResp, err := service.Authenticate("admin", "admin_secure_pass_phase5")
	if err != nil {
		t.Fatalf("admin login failed: %v", err)
	}
	if adminResp.User.Role != auth.RoleAdmin {
		t.Fatalf("expected RoleAdmin, got %s", adminResp.User.Role)
	}

	// 2. Valid Client Worker Login
	clientResp, err := service.Authenticate("client_worker", "worker_secure_pass_phase5")
	if err != nil {
		t.Fatalf("client login failed: %v", err)
	}
	if clientResp.User.Role != auth.RoleClient || clientResp.User.OwnerID != "worker-1" {
		t.Fatalf("client user mismatch: %+v", clientResp.User)
	}

	// 3. Invalid Password
	_, errInvalid := service.Authenticate("admin", "wrong_password")
	if errInvalid != auth.ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials, got: %v", errInvalid)
	}

	// 4. Nonexistent User
	_, errNotFound := service.Authenticate("nonexistent_user", "password")
	if errNotFound != auth.ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials, got: %v", errNotFound)
	}
}

func TestAuth_Middleware_EnforceOwnerIsolation(t *testing.T) {
	service := auth.NewService("test-secret-key-minimum-32-chars-long!", 1*time.Hour, "admin-test-token")
	mw := auth.NewMiddleware(service, metrics.DefaultRegistry)

	// Protected handler
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true}`))
	})

	stack := mw.Authenticate(mw.EnforceOwnerIsolation(okHandler))

	// Login as client_worker (Subject = "worker-1")
	clientLogin, _ := service.Authenticate("client_worker", "worker_secure_pass_phase5")
	clientToken := clientLogin.Token

	// Case 1: Client worker mutates a lock with matching owner_id "worker-1" -> MUST SUCCEED (200 OK)
	validBody, _ := json.Marshal(map[string]string{
		"resource_name": "db-table-1",
		"owner_id":      "worker-1",
	})
	reqValid := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(validBody))
	reqValid.Header.Set("Authorization", "Bearer "+clientToken)
	recValid := httptest.NewRecorder()
	stack.ServeHTTP(recValid, reqValid)

	if recValid.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for matching owner, got %d: %s", recValid.Code, recValid.Body.String())
	}

	// Case 2: Client worker attempts to acquire with owner_id "worker-2" (Impersonation) -> MUST REJECT (403 Forbidden)
	impersonateBody, _ := json.Marshal(map[string]string{
		"resource_name": "db-table-1",
		"owner_id":      "worker-2",
	})
	reqImpersonate := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(impersonateBody))
	reqImpersonate.Header.Set("Authorization", "Bearer "+clientToken)
	recImpersonate := httptest.NewRecorder()
	stack.ServeHTTP(recImpersonate, reqImpersonate)

	if recImpersonate.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for cross-client impersonation, got %d: %s", recImpersonate.Code, recImpersonate.Body.String())
	}

	// Case 3: Read-Only user attempts mutating POST -> MUST REJECT (403 Forbidden)
	roLogin, _ := service.Authenticate("auditor", "auditor_secure_pass_phase5")
	reqRO := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(validBody))
	reqRO.Header.Set("Authorization", "Bearer "+roLogin.Token)
	recRO := httptest.NewRecorder()
	stack.ServeHTTP(recRO, reqRO)

	if recRO.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for read-only mutation, got %d", recRO.Code)
	}

	// Case 4: Admin user executing mutation on behalf of worker-2 -> MUST SUCCEED (200 OK)
	adminLogin, _ := service.Authenticate("admin", "admin_secure_pass_phase5")
	reqAdmin := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(impersonateBody))
	reqAdmin.Header.Set("Authorization", "Bearer "+adminLogin.Token)
	recAdmin := httptest.NewRecorder()
	stack.ServeHTTP(recAdmin, reqAdmin)

	if recAdmin.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for admin override, got %d", recAdmin.Code)
	}
}

func TestAuth_RejectNotBeforeViolation(t *testing.T) {
	secret := "test-secret-key-minimum-32-chars-long!"
	claims := auth.Claims{
		Subject:   "worker-alpha",
		Role:      auth.RoleClient,
		NotBefore: time.Now().Add(10 * time.Minute).Unix(), // Not valid for another 10m
		ExpiresAt: time.Now().Add(1 * time.Hour).Unix(),
	}
	token, err := auth.GenerateJWT(claims, secret)
	if err != nil {
		t.Fatalf("GenerateJWT failed: %v", err)
	}

	_, err = auth.ValidateJWT(token, secret)
	if err != auth.ErrNotYetValid {
		t.Fatalf("expected ErrNotYetValid, got: %v", err)
	}
}

func TestAuth_RejectMalformedOrInvalidClaims(t *testing.T) {
	secret := "test-secret-key-minimum-32-chars-long!"

	// 1. Missing Subject
	claimsNoSub := auth.Claims{
		Role:      auth.RoleClient,
		ExpiresAt: time.Now().Add(1 * time.Hour).Unix(),
	}
	tokNoSub, _ := auth.GenerateJWT(claimsNoSub, secret)
	if _, err := auth.ValidateJWT(tokNoSub, secret); err != auth.ErrInvalidClaims {
		t.Errorf("expected ErrInvalidClaims for missing subject, got: %v", err)
	}

	// 2. Invalid Role
	claimsBadRole := auth.Claims{
		Subject:   "attacker",
		Role:      "SUPER_USER_ROOT",
		ExpiresAt: time.Now().Add(1 * time.Hour).Unix(),
	}
	tokBadRole, _ := auth.GenerateJWT(claimsBadRole, secret)
	if _, err := auth.ValidateJWT(tokBadRole, secret); err != auth.ErrInvalidClaims {
		t.Errorf("expected ErrInvalidClaims for invalid role, got: %v", err)
	}

	// 3. Spoofed Issuer
	claimsBadIss := auth.Claims{
		Subject:   "worker-alpha",
		Role:      auth.RoleClient,
		Issuer:    "malicious-auth-server",
		ExpiresAt: time.Now().Add(1 * time.Hour).Unix(),
	}
	tokBadIss, _ := auth.GenerateJWT(claimsBadIss, secret)
	if _, err := auth.ValidateJWT(tokBadIss, secret); err != auth.ErrInvalidClaims {
		t.Errorf("expected ErrInvalidClaims for spoofed issuer, got: %v", err)
	}

	// 4. Spoofed Audience
	claimsBadAud := auth.Claims{
		Subject:   "worker-alpha",
		Role:      auth.RoleClient,
		Audience:  "external-service",
		ExpiresAt: time.Now().Add(1 * time.Hour).Unix(),
	}
	tokBadAud, _ := auth.GenerateJWT(claimsBadAud, secret)
	if _, err := auth.ValidateJWT(tokBadAud, secret); err != auth.ErrInvalidClaims {
		t.Errorf("expected ErrInvalidClaims for spoofed audience, got: %v", err)
	}
}

func TestAuth_ClockSkewTolerance(t *testing.T) {
	secret := "test-secret-key-minimum-32-chars-long!"

	// Future-dated issuedAt within 30 seconds (within 60s tolerance) -> Should PASS
	claimsNearFuture := auth.Claims{
		Subject:   "worker-alpha",
		Role:      auth.RoleClient,
		IssuedAt:  time.Now().Add(30 * time.Second).Unix(),
		ExpiresAt: time.Now().Add(1 * time.Hour).Unix(),
	}
	tokNearFuture, _ := auth.GenerateJWT(claimsNearFuture, secret)
	if _, err := auth.ValidateJWT(tokNearFuture, secret); err != nil {
		t.Errorf("expected clock skew allowance for 30s in future, got error: %v", err)
	}

	// Future-dated issuedAt by 2 minutes (exceeds 60s tolerance) -> Should REJECT
	claimsFarFuture := auth.Claims{
		Subject:   "worker-alpha",
		Role:      auth.RoleClient,
		IssuedAt:  time.Now().Add(120 * time.Second).Unix(),
		ExpiresAt: time.Now().Add(1 * time.Hour).Unix(),
	}
	tokFarFuture, _ := auth.GenerateJWT(claimsFarFuture, secret)
	if _, err := auth.ValidateJWT(tokFarFuture, secret); err != auth.ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken for token issued 120s in future, got: %v", err)
	}
}

