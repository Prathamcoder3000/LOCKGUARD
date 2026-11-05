package tests

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lockguard/backend/internal/api"
	"lockguard/backend/internal/auth"
	"lockguard/backend/internal/config"
	"lockguard/backend/internal/middleware"
)

func TestPhase5_Security_JWTAuthentication(t *testing.T) {
	cfg := &config.Config{
		Environment: "test",
		Auth: config.AuthConfig{
			Enabled:      true,
			JWTSecretKey: "test_secret_key_minimum_32_bytes_long_12345",
		},
		Storage: config.StorageConfig{DataDir: t.TempDir()},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.NewServer(cfg, logger, "lockguard-test", "1.0.0")
	handler := srv.Handler()

	// 1. Valid Login
	loginBody, _ := json.Marshal(auth.LoginRequest{
		Username: "client_worker",
		Password: "worker_secure_pass_phase5",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for valid login, got %d: %s", rr.Code, rr.Body.String())
	}

	var loginResp struct {
		Data auth.LoginResponse `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("failed to decode login response: %v", err)
	}

	token := loginResp.Data.Token
	if token == "" || loginResp.Data.User.Role != auth.RoleClient {
		t.Fatalf("invalid login response: %+v", loginResp)
	}

	// 2. Invalid Credentials Login
	badLoginBody, _ := json.Marshal(auth.LoginRequest{
		Username: "client_worker",
		Password: "wrong_password",
	})
	reqBad := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewReader(badLoginBody))
	reqBad.Header.Set("Content-Type", "application/json")
	rrBad := httptest.NewRecorder()
	handler.ServeHTTP(rrBad, reqBad)

	if rrBad.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized for invalid credentials, got %d", rrBad.Code)
	}

	// 3. Expired Token Rejection
	authService := auth.NewService("test_secret_key_minimum_32_bytes_long_12345", 1*time.Hour, "admin-test-token")
	expiredClaims := auth.Claims{
		Subject:   "worker-1",
		Username:  "client_worker",
		Role:      auth.RoleClient,
		IssuedAt:  time.Now().Add(-2 * time.Hour).Unix(),
		ExpiresAt: time.Now().Add(-1 * time.Hour).Unix(),
	}
	expiredToken, err := auth.GenerateJWT(expiredClaims, "test_secret_key_minimum_32_bytes_long_12345")
	if err != nil {
		t.Fatalf("failed to sign expired token: %v", err)
	}
	if _, err := authService.ValidateToken(expiredToken); err != auth.ErrExpiredToken {
		t.Errorf("expected ErrExpiredToken, got %v", err)
	}

	// 4. Algorithm "none" Rejection
	headerJSON := `{"alg":"none","typ":"JWT"}`
	payloadJSON := fmt.Sprintf(`{"sub":"worker-1","role":"CLIENT","exp":%d,"iat":%d}`, time.Now().Add(time.Hour).Unix(), time.Now().Unix())
	hB64 := base64.RawURLEncoding.EncodeToString([]byte(headerJSON))
	pB64 := base64.RawURLEncoding.EncodeToString([]byte(payloadJSON))
	noneToken := fmt.Sprintf("%s.%s.", hB64, pB64)

	if _, err := authService.ValidateToken(noneToken); err != auth.ErrInvalidAlgorithm && err != auth.ErrInvalidSignature {
		t.Errorf("expected error rejecting 'none' algorithm token, got %v", err)
	}

	// 5. Tampered Signature Rejection
	tamperedToken := token + "tampered"
	if _, err := authService.ValidateToken(tamperedToken); err != auth.ErrInvalidSignature {
		t.Errorf("expected ErrInvalidSignature for tampered token, got %v", err)
	}
}

func TestPhase5_Security_RBACAuthorization(t *testing.T) {
	cfg := &config.Config{
		Environment: "test",
		Auth: config.AuthConfig{
			Enabled:      true,
			JWTSecretKey: "test_secret_key_minimum_32_bytes_long_12345",
		},
		Storage: config.StorageConfig{DataDir: t.TempDir()},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.NewServer(cfg, logger, "lockguard-test", "1.0.0")
	handler := srv.Handler()

	// Helper to login and get token
	login := func(user, pass string) string {
		body, _ := json.Marshal(auth.LoginRequest{Username: user, Password: pass})
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		var resp struct {
			Data auth.LoginResponse `json:"data"`
		}
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)
		return resp.Data.Token
	}

	auditorToken := login("auditor", "auditor_secure_pass_phase5")
	clientToken := login("client_worker", "worker_secure_pass_phase5")
	adminToken := login("admin", "admin_secure_pass_phase5")

	if auditorToken == "" || clientToken == "" || adminToken == "" {
		t.Fatalf("failed to retrieve login tokens")
	}

	// 1. Auditor (READ_ONLY) attempting lock acquisition should be rejected with 403 Forbidden
	acquireBody := []byte(`{"resource_name":"res-1","owner_id":"worker-1","ttl_seconds":10}`)
	reqAuditor := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(acquireBody))
	reqAuditor.Header.Set("Authorization", "Bearer "+auditorToken)
	reqAuditor.Header.Set("Content-Type", "application/json")
	rrAuditor := httptest.NewRecorder()
	handler.ServeHTTP(rrAuditor, reqAuditor)

	if rrAuditor.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for READ_ONLY user attempting lock acquire, got %d", rrAuditor.Code)
	}

	// 2. Client role attempting admin route /v1/admin/cluster/nodes should be rejected with 403 Forbidden
	reqClientAdmin := httptest.NewRequest(http.MethodGet, "/v1/admin/cluster/nodes", nil)
	reqClientAdmin.Header.Set("Authorization", "Bearer "+clientToken)
	rrClientAdmin := httptest.NewRecorder()
	handler.ServeHTTP(rrClientAdmin, reqClientAdmin)

	if rrClientAdmin.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for CLIENT role accessing admin route, got %d", rrClientAdmin.Code)
	}

	// 3. Admin accessing admin route /v1/admin/cluster/nodes should be permitted (200 OK)
	reqAdmin := httptest.NewRequest(http.MethodGet, "/v1/admin/cluster/nodes", nil)
	reqAdmin.Header.Set("Authorization", "Bearer "+adminToken)
	rrAdmin := httptest.NewRecorder()
	handler.ServeHTTP(rrAdmin, reqAdmin)

	if rrAdmin.Code != http.StatusOK {
		t.Errorf("expected 200 OK for ADMIN accessing admin route, got %d: %s", rrAdmin.Code, rrAdmin.Body.String())
	}
}

func TestPhase5_Security_CrossClientOwnerIsolation(t *testing.T) {
	// Set up a 3-node cluster
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	cfg := &config.Config{
		Environment: "test",
		HTTP: config.HTTPConfig{
			Port: 8080,
			Host: "127.0.0.1",
		},
		Raft: config.RaftConfig{NodeID: "gateway"},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: tc.Endpoints(),
			LeaderCacheTTL:       5 * time.Second,
			MaxRedirectAttempts:  2,
			RequestTimeout:       2 * time.Second,
		},
		Auth: config.AuthConfig{
			Enabled:      true,
			JWTSecretKey: "test_secret_key_minimum_32_bytes_long_12345",
		},
		Storage: config.StorageConfig{DataDir: t.TempDir()},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.NewServer(cfg, logger, "lockguard-gateway", "1.0.0")
	handler := srv.Handler()

	authService := auth.NewService("test_secret_key_minimum_32_bytes_long_12345", 24*time.Hour, "admin-token")

	// Authenticate Client 1 (worker-1)
	c1Resp, err := authService.Authenticate("client_worker", "worker_secure_pass_phase5")
	if err != nil {
		t.Fatalf("failed client 1 login: %v", err)
	}
	c1Token := c1Resp.Token

	// Authenticate Client 2 (worker-2)
	c2Resp, err := authService.Authenticate("client_worker_2", "worker_secure_pass_phase5")
	if err != nil {
		t.Fatalf("failed client 2 login: %v", err)
	}
	c2Token := c2Resp.Token

	// 1. Client 1 acquires lock on "res-iso-1" with owner_id="worker-1" -> MUST SUCCEED
	body1 := []byte(`{"request_id":"req-iso-1","resource_name":"res-iso-1","owner_id":"worker-1","ttl_seconds":15}`)
	req1 := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(body1))
	req1.Header.Set("Authorization", "Bearer "+c1Token)
	req1.Header.Set("Content-Type", "application/json")
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)

	if rr1.Code != http.StatusOK && rr1.Code != http.StatusCreated {
		t.Fatalf("client 1 acquire failed: status %d, body: %s", rr1.Code, rr1.Body.String())
	}

	// 2. Client 2 tries to impersonate worker-1 (attaching Client 2's token but owner_id="worker-1")
	// MUST BE REJECTED with 403 ERR_OWNER_MISMATCH
	impersonateBody := []byte(`{"request_id":"req-iso-2","resource_name":"res-iso-2","owner_id":"worker-1","ttl_seconds":15}`)
	reqImpersonate := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(impersonateBody))
	reqImpersonate.Header.Set("Authorization", "Bearer "+c2Token)
	reqImpersonate.Header.Set("Content-Type", "application/json")
	rrImpersonate := httptest.NewRecorder()
	handler.ServeHTTP(rrImpersonate, reqImpersonate)

	if rrImpersonate.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for cross-client owner impersonation, got %d", rrImpersonate.Code)
	}
	if !strings.Contains(rrImpersonate.Body.String(), "ERR_OWNER_MISMATCH") {
		t.Errorf("expected ERR_OWNER_MISMATCH in response, got: %s", rrImpersonate.Body.String())
	}

	// 3. Client 2 attempts to release Client 1's lock claiming owner_id="worker-1"
	// MUST BE REJECTED with 403 ERR_OWNER_MISMATCH
	releaseBody := []byte(`{"request_id":"req-iso-3","resource_name":"res-iso-1","owner_id":"worker-1"}`)
	reqRelease := httptest.NewRequest(http.MethodPost, "/v1/locks/release", bytes.NewReader(releaseBody))
	reqRelease.Header.Set("Authorization", "Bearer "+c2Token)
	reqRelease.Header.Set("Content-Type", "application/json")
	rrRelease := httptest.NewRecorder()
	handler.ServeHTTP(rrRelease, reqRelease)

	if rrRelease.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden when Client 2 attempts to release Client 1's lock, got %d", rrRelease.Code)
	}
}

func TestPhase5_Security_APIHardening(t *testing.T) {
	cfg := &config.Config{
		Environment: "test",
		Storage:     config.StorageConfig{DataDir: t.TempDir()},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.NewServer(cfg, logger, "lockguard-test", "1.0.0")
	handler := srv.Handler()

	// 1. Verify Security Headers
	reqStatus := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	rrStatus := httptest.NewRecorder()
	handler.ServeHTTP(rrStatus, reqStatus)

	if rrStatus.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("expected X-Content-Type-Options: nosniff")
	}
	if rrStatus.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("expected X-Frame-Options: DENY")
	}
	if rrStatus.Header().Get("Strict-Transport-Security") == "" {
		t.Errorf("expected Strict-Transport-Security header")
	}
	if rrStatus.Header().Get("Content-Security-Policy") == "" {
		t.Errorf("expected Content-Security-Policy header")
	}

	// 2. Verify Max Body Size Rejection (> 1MB)
	hugeBody := make([]byte, 1024*1024+1024) // 1MB + 1KB
	reqHuge := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(hugeBody))
	reqHuge.Header.Set("Content-Type", "application/json")
	rrHuge := httptest.NewRecorder()
	handler.ServeHTTP(rrHuge, reqHuge)

	if rrHuge.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413 Payload Too Large, got %d", rrHuge.Code)
	}

	// 3. Verify Content-Type Validation (text/plain on POST mutating endpoint)
	reqBadCT := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", strings.NewReader(`{"resource_name":"test"}`))
	reqBadCT.Header.Set("Content-Type", "text/plain")
	rrBadCT := httptest.NewRecorder()
	handler.ServeHTTP(rrBadCT, reqBadCT)

	if rrBadCT.Code != http.StatusUnsupportedMediaType {
		t.Errorf("expected 415 Unsupported Media Type for text/plain, got %d", rrBadCT.Code)
	}

	// 4. Verify Rate Limiter Rejection
	limiter := middleware.NewRateLimiter(5, 5, 100, nil) // Low limit: 5 req/s with burst of 5
	limitedHandler := middleware.RateLimitMiddleware(limiter, "test")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rateLimited := false
	for i := 0; i < 20; i++ {
		reqRL := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
		reqRL.RemoteAddr = "192.168.1.100:12345"
		rrRL := httptest.NewRecorder()
		limitedHandler.ServeHTTP(rrRL, reqRL)
		if rrRL.Code == http.StatusTooManyRequests {
			rateLimited = true
			if rrRL.Header().Get("Retry-After") == "" {
				t.Errorf("expected Retry-After header on 429 response")
			}
			break
		}
	}

	if !rateLimited {
		t.Errorf("expected rate limiter to trigger 429 Too Many Requests under burst")
	}
}
