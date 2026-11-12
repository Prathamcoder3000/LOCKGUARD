package renewal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lockguard/backend/internal/config"
	"lockguard/backend/internal/heartbeat"
	"lockguard/backend/internal/metrics"
)

func TestRenewalHandler_StatusRedactsSessionID(t *testing.T) {
	cfg := &config.Config{
		Renewal: config.RenewalConfig{
			SafetyMargin: 50 * time.Millisecond,
			RetryInitial: 10 * time.Millisecond,
			RetryMax:     50 * time.Millisecond,
			MaxAttempts:  3,
		},
	}

	reg := metrics.NewRegistry()
	hbMgr := heartbeat.NewManager(&config.Config{
		Heartbeat: config.HeartbeatConfig{Timeout: 10 * time.Second, CleanupInterval: 50 * time.Millisecond},
	}, reg)
	hbMgr.Start()
	defer hbMgr.Stop()

	sess, err := hbMgr.Register("worker-client-redaction", "alice", 10*time.Second)
	if err != nil {
		t.Fatalf("failed to register session: %v", err)
	}

	client := &mockLockClient{}
	mgr := NewManager(cfg, client, hbMgr, reg)
	defer mgr.Shutdown()

	future := time.Now().Add(10 * time.Minute).UnixNano()
	if err := mgr.StartRenewal("res-redact-1", "alice", "worker-client-redaction", sess.SessionID(), 101, 1, future, 10*time.Second); err != nil {
		t.Fatalf("StartRenewal failed: %v", err)
	}

	h := NewHandler(mgr)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// 1. List status without matching X-Session-ID must redact session_id
	reqList := httptest.NewRequest(http.MethodGet, "/v1/renewal/status", nil)
	rrList := httptest.NewRecorder()
	mux.ServeHTTP(rrList, reqList)
	if rrList.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rrList.Code)
	}

	var listResp struct {
		Success bool `json:"success"`
		Data    struct {
			Renewals []RenewalStatus `json:"renewals"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rrList.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal list status: %v", err)
	}
	if len(listResp.Data.Renewals) != 1 || listResp.Data.Renewals[0].SessionID != "[REDACTED]" {
		t.Fatalf("expected redacted session_id in list response, got %+v", listResp.Data.Renewals)
	}

	// 2. Single status without X-Session-ID must redact session_id
	reqOne := httptest.NewRequest(http.MethodGet, "/v1/renewal/status?resource=res-redact-1", nil)
	rrOne := httptest.NewRecorder()
	mux.ServeHTTP(rrOne, reqOne)
	if rrOne.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rrOne.Code)
	}

	var singleResp struct {
		Success bool          `json:"success"`
		Data    RenewalStatus `json:"data"`
	}
	if err := json.Unmarshal(rrOne.Body.Bytes(), &singleResp); err != nil {
		t.Fatalf("unmarshal single status: %v", err)
	}
	if singleResp.Data.SessionID != "[REDACTED]" {
		t.Fatalf("expected redacted session_id without header, got %q", singleResp.Data.SessionID)
	}

	// 3. Single status with matching X-Session-ID reveals session_id
	reqAuth := httptest.NewRequest(http.MethodGet, "/v1/renewal/status?resource=res-redact-1", nil)
	reqAuth.Header.Set("X-Session-ID", sess.SessionID())
	rrAuth := httptest.NewRecorder()
	mux.ServeHTTP(rrAuth, reqAuth)
	if rrAuth.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rrAuth.Code)
	}

	var authResp struct {
		Success bool          `json:"success"`
		Data    RenewalStatus `json:"data"`
	}
	if err := json.Unmarshal(rrAuth.Body.Bytes(), &authResp); err != nil {
		t.Fatalf("unmarshal authenticated status: %v", err)
	}
	if authResp.Data.SessionID != sess.SessionID() {
		t.Fatalf("expected exact session_id when X-Session-ID matches, got %q", authResp.Data.SessionID)
	}
}
