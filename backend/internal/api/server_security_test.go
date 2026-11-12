package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lockguard/backend/internal/config"
)

type stubDBChecker struct {
	err error
}

func (s stubDBChecker) Ping(ctx context.Context) error {
	return s.err
}

func newSecurityTestConfig(dataDir string) *config.Config {
	return &config.Config{
		Environment: "development",
		Raft:        config.RaftConfig{NodeID: "gw-sec-test"},
		Storage:     config.StorageConfig{DataDir: dataDir},
		Gateway: config.GatewayConfig{
			CoordinatorEndpoints: []string{"http://127.0.0.1:59999"},
			LeaderCacheTTL:       time.Second,
			MaxRedirectAttempts:  1,
			RequestTimeout:       time.Second,
		},
		Lease: config.LeaseConfig{
			DefaultTTLSec: 10,
			MinTTLSec:     1,
			MaxTTLSec:     300,
		},
		Heartbeat: config.HeartbeatConfig{
			Interval:        3 * time.Second,
			Timeout:         10 * time.Second,
			CleanupInterval: 2 * time.Second,
		},
		Renewal: config.RenewalConfig{
			SafetyMargin: 2 * time.Second,
			RetryInitial: 100 * time.Millisecond,
			RetryMax:     2 * time.Second,
			MaxAttempts:  3,
		},
		Auth: config.AuthConfig{
			Enabled:        true,
			JWTSecretKey:   "unit_test_secret_key_minimum_32_bytes_long",
			JWTExpirationH: 1,
		},
	}
}

func loginTestUser(t *testing.T, handler http.Handler, username, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("login failed for %s: status=%d body=%s", username, rr.Code, rr.Body.String())
	}
	var resp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || resp.Data.Token == "" {
		t.Fatalf("failed to parse login token for %s: %v", username, err)
	}
	return resp.Data.Token
}

func TestServerSecurity_DevelopmentModeEnforcesAuthRBACAndTTL(t *testing.T) {
	cfg := newSecurityTestConfig(t.TempDir())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(cfg, logger, "lockguard-api", "1.0.0")
	handler := srv.Handler()

	// 1. Unauthenticated requests must be rejected with 401 even in ENVIRONMENT=development
	unauthCases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/v1/locks", ""},
		{http.MethodPost, "/v1/locks/acquire", `{"resource_name":"r1","owner_id":"worker-1","ttl_seconds":10}`},
		{http.MethodGet, "/v1/clients", ""},
		{http.MethodPost, "/v1/clients/register", `{"client_id":"c1","owner_id":"worker-1","ttl_seconds":15}`},
		{http.MethodPost, "/v1/clients/heartbeat", `{"client_id":"c1","session_id":"s1"}`},
		{http.MethodGet, "/v1/renewal/status", ""},
		{http.MethodPost, "/v1/renewal/start", `{"resource_name":"r1","owner_id":"worker-1","client_id":"c1","session_id":"s1","fencing_token":1,"ttl_seconds":10}`},
		{http.MethodPost, "/v1/renewal/stop", `{"resource_name":"r1","session_id":"s1"}`},
		{http.MethodGet, "/v1/audit/events", ""},
	}

	for _, tc := range unauthCases {
		var bodyReader io.Reader
		if tc.body != "" {
			bodyReader = strings.NewReader(tc.body)
		}
		req := httptest.NewRequest(tc.method, tc.path, bodyReader)
		if tc.body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without token: expected 401, got %d (%s)", tc.method, tc.path, rr.Code, rr.Body.String())
		}
	}

	// 2. Static development/X-Client-ID bypass must be rejected on /v1/audit/events
	for _, hdr := range []string{"Bearer admin", "Bearer operator", "Bearer arbitrary-fake-token"} {
		req := httptest.NewRequest(http.MethodGet, "/v1/audit/events", nil)
		req.Header.Set("Authorization", hdr)
		req.Header.Set("X-Client-ID", "spoofed-client")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("audit bypass %q: expected 401, got %d (%s)", hdr, rr.Code, rr.Body.String())
		}
	}

	adminToken := loginTestUser(t, handler, "admin", "admin_secure_pass_phase5")
	worker1Token := loginTestUser(t, handler, "client_worker", "worker_secure_pass_phase5")
	worker2Token := loginTestUser(t, handler, "client_worker_2", "worker_secure_pass_phase5")
	auditorToken := loginTestUser(t, handler, "auditor", "auditor_secure_pass_phase5")

	// 3. Read-only auditor cannot mutate locks, client sessions, or renewals
	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/v1/locks/acquire", `{"resource_name":"r1","owner_id":"auditor","ttl_seconds":10}`},
		{http.MethodPost, "/v1/clients/register", `{"client_id":"c-aud","owner_id":"auditor","ttl_seconds":15}`},
		{http.MethodPost, "/v1/renewal/start", `{"resource_name":"r1","owner_id":"auditor","client_id":"c-aud","session_id":"s1","fencing_token":1,"ttl_seconds":10}`},
		{http.MethodPost, "/v1/renewal/stop", `{"resource_name":"r1"}`},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", "Bearer "+auditorToken)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("READ_ONLY %s %s: expected 403, got %d (%s)", tc.method, tc.path, rr.Code, rr.Body.String())
		}
	}

	// Read-only auditor CAN read /v1/clients and /v1/renewal/status
	for _, path := range []string{"/v1/clients", "/v1/renewal/status"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+auditorToken)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("READ_ONLY GET %s: expected 200, got %d (%s)", path, rr.Code, rr.Body.String())
		}
	}

	// 4. Client session owner isolation (cross-client register & heartbeat denied; own client-prefixed session allowed)
	reqCross := httptest.NewRequest(http.MethodPost, "/v1/clients/register", strings.NewReader(`{"client_id":"c-steal","owner_id":"worker-2","ttl_seconds":15}`))
	reqCross.Header.Set("Authorization", "Bearer "+worker1Token)
	reqCross.Header.Set("Content-Type", "application/json")
	rrCross := httptest.NewRecorder()
	handler.ServeHTTP(rrCross, reqCross)
	if rrCross.Code != http.StatusForbidden {
		t.Fatalf("cross-owner client register: expected 403, got %d (%s)", rrCross.Code, rrCross.Body.String())
	}

	reqReg := httptest.NewRequest(http.MethodPost, "/api/v1/clients/register", strings.NewReader(`{"client_id":"client-worker-1","owner_id":"worker-1","ttl_seconds":20}`))
	reqReg.Header.Set("Authorization", "Bearer "+worker1Token)
	reqReg.Header.Set("Content-Type", "application/json")
	rrReg := httptest.NewRecorder()
	handler.ServeHTTP(rrReg, reqReg)
	if rrReg.Code != http.StatusOK {
		t.Fatalf("worker-1 register own session failed: status=%d body=%s", rrReg.Code, rrReg.Body.String())
	}
	var regResp struct {
		Data struct {
			SessionID string `json:"session_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rrReg.Body.Bytes(), &regResp)
	if regResp.Data.SessionID == "" {
		t.Fatalf("expected non-empty session_id on register")
	}

	hbPayload, _ := json.Marshal(map[string]string{"client_id": "client-worker-1", "session_id": regResp.Data.SessionID})
	reqHB := httptest.NewRequest(http.MethodPost, "/api/v1/clients/heartbeat", bytes.NewReader(hbPayload))
	reqHB.Header.Set("Authorization", "Bearer "+worker1Token)
	reqHB.Header.Set("Content-Type", "application/json")
	rrHB := httptest.NewRecorder()
	handler.ServeHTTP(rrHB, reqHB)
	if rrHB.Code != http.StatusOK {
		t.Fatalf("worker-1 heartbeat on client-worker-1 failed: status=%d body=%s", rrHB.Code, rrHB.Body.String())
	}

	reqHB2 := httptest.NewRequest(http.MethodPost, "/api/v1/clients/heartbeat", bytes.NewReader(hbPayload))
	reqHB2.Header.Set("Authorization", "Bearer "+worker2Token)
	reqHB2.Header.Set("Content-Type", "application/json")
	rrHB2 := httptest.NewRecorder()
	handler.ServeHTTP(rrHB2, reqHB2)
	if rrHB2.Code != http.StatusForbidden {
		t.Fatalf("worker-2 heartbeat on worker-1 session: expected 403, got %d (%s)", rrHB2.Code, rrHB2.Body.String())
	}

	// 5. Lease TTL validation rejects out-of-bounds TTL before coordinator forwarding
	for _, body := range []string{
		`{"request_id":"req-ttl-max","resource_name":"res-ttl","owner_id":"admin-system","ttl_seconds":99999}`,
		`{"request_id":"req-ttl-neg","resource_name":"res-ttl","owner_id":"admin-system","ttl_seconds":-5}`,
		`{"request_id":"req-ttl-ms","resource_name":"res-ttl","owner_id":"admin-system","ttl_nanoseconds":1000000}`,
	} {
		reqTTL := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", strings.NewReader(body))
		reqTTL.Header.Set("Authorization", "Bearer "+adminToken)
		reqTTL.Header.Set("Content-Type", "application/json")
		rrTTL := httptest.NewRecorder()
		handler.ServeHTTP(rrTTL, reqTTL)
		if rrTTL.Code != http.StatusBadRequest {
			t.Errorf("TTL validation for %s: expected 400, got %d (%s)", body, rrTTL.Code, rrTTL.Body.String())
		}
	}
}

func TestReadinessHandlerWithDB(t *testing.T) {
	cfg := newSecurityTestConfig(t.TempDir())

	hOK := ReadinessHandlerWithDB(cfg, "lockguard-api", "node-1", stubDBChecker{err: nil})
	rrOK := httptest.NewRecorder()
	hOK.ServeHTTP(rrOK, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rrOK.Code != http.StatusOK {
		t.Fatalf("expected 200 OK when DB healthy, got %d (%s)", rrOK.Code, rrOK.Body.String())
	}
	if !strings.Contains(rrOK.Body.String(), `"database":"ready"`) {
		t.Fatalf("expected database=ready, got %s", rrOK.Body.String())
	}

	hDown := ReadinessHandlerWithDB(cfg, "lockguard-api", "node-1", stubDBChecker{err: errors.New("connection refused")})
	rrDown := httptest.NewRecorder()
	hDown.ServeHTTP(rrDown, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rrDown.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when DB unreachable, got %d (%s)", rrDown.Code, rrDown.Body.String())
	}
	if !strings.Contains(rrDown.Body.String(), `"database":"unavailable: connection refused"`) {
		t.Fatalf("expected unavailable database component, got %s", rrDown.Body.String())
	}
}

func TestServerMetrics_AuthFailuresAuthzDenialsAndRBACOnResilience(t *testing.T) {
	cfg := newSecurityTestConfig(t.TempDir())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(cfg, logger, "lockguard-api", "1.0.0")
	handler := srv.Handler()

	adminToken := loginTestUser(t, handler, "admin", "admin_secure_pass_phase5")
	workerToken := loginTestUser(t, handler, "client_worker", "worker_secure_pass_phase5")
	auditorToken := loginTestUser(t, handler, "auditor", "auditor_secure_pass_phase5")

	beforeFailures := srv.metricsReg.GetCounterTotal("lockguard_auth_failures_total")
	beforeDenials := srv.metricsReg.GetCounterTotal("lockguard_authz_denials_total")

	// 1. Successful 200 GET /api/v1/cluster/status must NOT increment auth failure or authz denial counters
	reqStatus := httptest.NewRequest(http.MethodGet, "/api/v1/cluster/status", nil)
	rrStatus := httptest.NewRecorder()
	handler.ServeHTTP(rrStatus, reqStatus)
	if rrStatus.Code != http.StatusOK {
		t.Fatalf("expected 200 on cluster status, got %d", rrStatus.Code)
	}

	// 2. Unauthenticated 401 POST /api/v1/locks increments lockguard_auth_failures_total once (NOT authz_denials)
	reqUnauthLock := httptest.NewRequest(http.MethodPost, "/api/v1/locks", strings.NewReader(`{}`))
	reqUnauthLock.Header.Set("Content-Type", "application/json")
	rrUnauthLock := httptest.NewRecorder()
	handler.ServeHTTP(rrUnauthLock, reqUnauthLock)
	if rrUnauthLock.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on unauthenticated POST /api/v1/locks, got %d", rrUnauthLock.Code)
	}

	// 3. Invalid 400 validation request (admin TTL out of bounds) must NOT touch auth failures or authz denials
	reqBadTTL := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", strings.NewReader(`{"request_id":"r-bad-ttl","resource_name":"r1","owner_id":"admin","ttl_seconds":99999}`))
	reqBadTTL.Header.Set("Authorization", "Bearer "+adminToken)
	reqBadTTL.Header.Set("Content-Type", "application/json")
	rrBadTTL := httptest.NewRecorder()
	handler.ServeHTTP(rrBadTTL, reqBadTTL)
	if rrBadTTL.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on invalid TTL, got %d", rrBadTTL.Code)
	}

	// 4. Non-admin mutations on resilience endpoints must return 403 and increment authz_denials_total once each
	for _, tc := range []struct {
		path  string
		token string
	}{
		{"/api/v1/recovery/chaos/run", workerToken},
		{"/api/v1/recovery/partition/inject", auditorToken},
		{"/api/v1/recovery/snapshots/restore", workerToken},
	} {
		reqForbidden := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(`{"confirm":true}`))
		reqForbidden.Header.Set("Authorization", "Bearer "+tc.token)
		reqForbidden.Header.Set("Content-Type", "application/json")
		rrForbidden := httptest.NewRecorder()
		handler.ServeHTTP(rrForbidden, reqForbidden)
		if rrForbidden.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for non-admin POST %s, got %d (%s)", tc.path, rrForbidden.Code, rrForbidden.Body.String())
		}
	}

	// 5. Admin user passes RBAC (unconfirmed snapshot restore returns 400, not 403)
	reqAdminNoConfirm := httptest.NewRequest(http.MethodPost, "/api/v1/recovery/snapshots/restore", strings.NewReader(`{"snapshot_id":"snap-1","confirm":false}`))
	reqAdminNoConfirm.Header.Set("Authorization", "Bearer "+adminToken)
	reqAdminNoConfirm.Header.Set("Content-Type", "application/json")
	rrAdminNoConfirm := httptest.NewRecorder()
	handler.ServeHTTP(rrAdminNoConfirm, reqAdminNoConfirm)
	if rrAdminNoConfirm.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when admin omits confirm=true, got %d (%s)", rrAdminNoConfirm.Code, rrAdminNoConfirm.Body.String())
	}

	if got := srv.metricsReg.GetCounterTotal("lockguard_auth_failures_total"); got != beforeFailures+1 {
		t.Fatalf("expected lockguard_auth_failures_total delta +1 (got %d vs before=%d)", got, beforeFailures)
	}
	if got := srv.metricsReg.GetCounterTotal("lockguard_authz_denials_total"); got != beforeDenials+3 {
		t.Fatalf("expected lockguard_authz_denials_total delta +3 (got %d vs before=%d)", got, beforeDenials)
	}
}
