package heartbeat

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lockguard/backend/internal/config"
	"lockguard/backend/internal/metrics"
)

func TestHeartbeatHandler_RedactionAndAuthorization(t *testing.T) {
	cfg := &config.Config{
		Heartbeat: config.HeartbeatConfig{
			Interval:        50 * time.Millisecond,
			Timeout:         5 * time.Second,
			CleanupInterval: 50 * time.Millisecond,
		},
	}

	reg := metrics.NewRegistry()
	mgr := NewManager(cfg, reg)
	mgr.Start()
	defer mgr.Stop()

	handler := NewHandler(mgr)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	// 1. Register a client session
	regPayload, _ := json.Marshal(map[string]interface{}{
		"client_id":   "client-sec-1",
		"owner_id":    "alice",
		"ttl_seconds": 10,
	})
	reqReg := httptest.NewRequest(http.MethodPost, "/v1/clients/register", bytes.NewReader(regPayload))
	recReg := httptest.NewRecorder()
	mux.ServeHTTP(recReg, reqReg)

	if recReg.Code != http.StatusOK {
		t.Fatalf("register failed: %s", recReg.Body.String())
	}

	var regResp struct {
		Data SessionSnapshot `json:"data"`
	}
	_ = json.Unmarshal(recReg.Body.Bytes(), &regResp)
	secretSessionID := regResp.Data.SessionID
	if secretSessionID == "" {
		t.Fatalf("expected non-empty session ID from registration response")
	}

	// 2. Query GET /v1/clients without auth header: session_id MUST be redacted!
	reqList := httptest.NewRequest(http.MethodGet, "/v1/clients", nil)
	recList := httptest.NewRecorder()
	mux.ServeHTTP(recList, reqList)

	if recList.Code != http.StatusOK {
		t.Fatalf("list clients failed: %s", recList.Body.String())
	}

	var listResp struct {
		Data struct {
			Sessions []SessionSnapshot `json:"sessions"`
		} `json:"data"`
	}
	_ = json.Unmarshal(recList.Body.Bytes(), &listResp)
	if len(listResp.Data.Sessions) == 0 {
		t.Fatalf("expected at least 1 session in list")
	}
	for _, s := range listResp.Data.Sessions {
		if s.SessionID != "[REDACTED]" {
			t.Errorf("security violation: unredacted session_id leaked in GET /v1/clients: %s", s.SessionID)
		}
	}

	// 3. Query GET /v1/clients/{id} without auth header: session_id MUST be redacted!
	reqGetID := httptest.NewRequest(http.MethodGet, "/v1/clients/client-sec-1", nil)
	recGetID := httptest.NewRecorder()
	mux.ServeHTTP(recGetID, reqGetID)

	var getResp struct {
		Data SessionSnapshot `json:"data"`
	}
	_ = json.Unmarshal(recGetID.Body.Bytes(), &getResp)
	if getResp.Data.SessionID != "[REDACTED]" {
		t.Errorf("security violation: unredacted session_id leaked in GET /v1/clients/client-sec-1: %s", getResp.Data.SessionID)
	}

	// 4. Query GET /v1/clients/{id} WITH correct X-Session-ID header: reveals session_id
	reqGetAuth := httptest.NewRequest(http.MethodGet, "/v1/clients/client-sec-1", nil)
	reqGetAuth.Header.Set("X-Session-ID", secretSessionID)
	recGetAuth := httptest.NewRecorder()
	mux.ServeHTTP(recGetAuth, reqGetAuth)

	var getAuthResp struct {
		Data SessionSnapshot `json:"data"`
	}
	_ = json.Unmarshal(recGetAuth.Body.Bytes(), &getAuthResp)
	if getAuthResp.Data.SessionID != secretSessionID {
		t.Errorf("expected session_id to be visible to authorized caller: got %s", getAuthResp.Data.SessionID)
	}

	// 5. Negative Test: DELETE /v1/clients/{id} with wrong session_id returns 403
	reqDelWrong := httptest.NewRequest(http.MethodDelete, "/v1/clients/client-sec-1", nil)
	reqDelWrong.Header.Set("X-Session-ID", "forged-session-id")
	recDelWrong := httptest.NewRecorder()
	mux.ServeHTTP(recDelWrong, reqDelWrong)

	if recDelWrong.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden on DELETE with wrong session ID, got %d", recDelWrong.Code)
	}

	// 6. Negative Test: Heartbeat with wrong session_id returns 403
	hbWrongPayload, _ := json.Marshal(map[string]interface{}{
		"client_id":  "client-sec-1",
		"session_id": "wrong-session-xyz",
	})
	reqHbWrong := httptest.NewRequest(http.MethodPost, "/v1/clients/heartbeat", bytes.NewReader(hbWrongPayload))
	recHbWrong := httptest.NewRecorder()
	mux.ServeHTTP(recHbWrong, reqHbWrong)

	if recHbWrong.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden on heartbeat with wrong session ID, got %d", recHbWrong.Code)
	}
}

func TestHeartbeat_RegistrationAndLifecycleSecurity(t *testing.T) {
	cfg := &config.Config{
		Heartbeat: config.HeartbeatConfig{
			Interval:        50 * time.Millisecond,
			Timeout:         100 * time.Millisecond,
			CleanupInterval: 20 * time.Millisecond,
		},
	}

	reg := metrics.NewRegistry()
	mgr := NewManager(cfg, reg)
	mgr.Start()
	defer mgr.Stop()

	handler := NewHandler(mgr)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	// 1. Initial registration with short TTL
	regPayload, _ := json.Marshal(map[string]interface{}{
		"client_id":       "client-dup-test",
		"owner_id":        "alice",
		"ttl_nanoseconds": int64(80 * time.Millisecond),
	})
	reqReg := httptest.NewRequest(http.MethodPost, "/v1/clients/register", bytes.NewReader(regPayload))
	recReg := httptest.NewRecorder()
	mux.ServeHTTP(recReg, reqReg)
	if recReg.Code != http.StatusOK {
		t.Fatalf("initial registration failed: %d", recReg.Code)
	}

	// 2. Duplicate registration while session is active MUST be rejected with 409 Conflict
	reqRegDup := httptest.NewRequest(http.MethodPost, "/v1/clients/register", bytes.NewReader(regPayload))
	recRegDup := httptest.NewRecorder()
	mux.ServeHTTP(recRegDup, reqRegDup)
	if recRegDup.Code != http.StatusConflict {
		t.Errorf("expected 409 Conflict on duplicate registration while session active, got %d", recRegDup.Code)
	}

	// 3. Registration under differing owner MUST be rejected with 409 Conflict
	diffOwnerPayload, _ := json.Marshal(map[string]interface{}{
		"client_id":   "client-dup-test",
		"owner_id":    "mallory",
		"ttl_seconds": 1,
	})
	reqRegDiff := httptest.NewRequest(http.MethodPost, "/v1/clients/register", bytes.NewReader(diffOwnerPayload))
	recRegDiff := httptest.NewRecorder()
	mux.ServeHTTP(recRegDiff, reqRegDiff)
	if recRegDiff.Code != http.StatusConflict {
		t.Errorf("expected 409 Conflict on owner mismatch, got %d", recRegDiff.Code)
	}

	// 4. Session deletion without session ID must be rejected with 403 Forbidden
	reqDelEmpty := httptest.NewRequest(http.MethodDelete, "/v1/clients/client-dup-test", nil)
	recDelEmpty := httptest.NewRecorder()
	mux.ServeHTTP(recDelEmpty, reqDelEmpty)
	if recDelEmpty.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden on DELETE without session ID, got %d", recDelEmpty.Code)
	}

	// 5. Wait for session to expire, then re-registration should succeed with fresh token
	time.Sleep(150 * time.Millisecond)
	reqRegAfterExpiry := httptest.NewRequest(http.MethodPost, "/v1/clients/register", bytes.NewReader(regPayload))
	recRegAfterExpiry := httptest.NewRecorder()
	mux.ServeHTTP(recRegAfterExpiry, reqRegAfterExpiry)
	if recRegAfterExpiry.Code != http.StatusOK {
		t.Errorf("expected 200 OK on registration after session expiry, got %d", recRegAfterExpiry.Code)
	}
}
