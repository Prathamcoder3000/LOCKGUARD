package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lockguard/backend/internal/config"
	"lockguard/backend/internal/gateway"
	"lockguard/backend/internal/metrics"
	"lockguard/backend/internal/persistence"
)

type memAuditRepo struct {
	mu     sync.Mutex
	events []*persistence.AuditEventRecord
}

func (m *memAuditRepo) InsertEvent(ctx context.Context, event *persistence.AuditEventRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
	return nil
}

func (m *memAuditRepo) InsertBatch(ctx context.Context, events []*persistence.AuditEventRecord) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, events...)
	return len(events), nil
}

func (m *memAuditRepo) GetEventByID(ctx context.Context, eventID string) (*persistence.AuditEventRecord, error) {
	return nil, persistence.ErrEventNotFound
}

func (m *memAuditRepo) ListEvents(ctx context.Context, filter persistence.AuditFilter) ([]*persistence.AuditEventRecord, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.events, len(m.events), nil
}

func (m *memAuditRepo) snapshot() []*persistence.AuditEventRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*persistence.AuditEventRecord, len(m.events))
	copy(out, m.events)
	return out
}

func TestLeaseReaperAndRuntimeConfigAndAuditHooks(t *testing.T) {
	var expiredCalls atomic.Int64
	leaderSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/cluster/status":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"data": map[string]interface{}{
					"node_id":   "coord-a",
					"state":     "Leader",
					"is_leader": true,
					"leader_id": "coord-a",
				},
			})
		case "/v1/locks":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"data": map[string]interface{}{
					"locks": []map[string]interface{}{
						{
							"resource_name":        "reap-res-1",
							"owner_id":             "worker-1",
							"fencing_token":        42,
							"lease_version":        1,
							"expires_at_unix_nano": time.Now().Add(-5 * time.Second).UnixNano(),
							"status":               "EXPIRED",
						},
					},
				},
			})
		case "/v1/locks/expire":
			expiredCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]interface{}{"code": "OK"}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer leaderSrv.Close()

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	cfg.Auth.Enabled = true
	cfg.Gateway.CoordinatorEndpoints = []string{leaderSrv.URL}

	srv := NewServer(cfg, slog.Default(), "lockguard-api-test", "0.1.0")
	srv.discovery = gateway.NewLeaderDiscovery([]string{leaderSrv.URL}, time.Second, nil, metrics.DefaultRegistry)
	repo := &memAuditRepo{}
	srv.auditPipeline.SetRepository(repo)
	srv.auditPipeline.Start()
	defer func() { _ = srv.auditPipeline.Shutdown(context.Background()) }()

	// 1. Trigger lease reaper directly
	srv.reapExpiredLeases(context.Background(), &http.Client{Timeout: 2 * time.Second})
	if expiredCalls.Load() != 1 {
		t.Fatalf("expected reaper to invoke POST /v1/locks/expire once, got %d", expiredCalls.Load())
	}

	// 2. Trigger heartbeat registration + login failure audit capture
	if _, err := srv.HeartbeatManager().Register("client-test-1", "client_worker", 5*time.Second); err != nil {
		t.Fatalf("heartbeat register failed: %v", err)
	}

	reqBadLogin := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader([]byte(`{"username":"admin","password":"wrong"}`)))
	reqBadLogin.Header.Set("Content-Type", "application/json")
	recBadLogin := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recBadLogin, reqBadLogin)
	if recBadLogin.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 from wrong credentials login, got %d", recBadLogin.Code)
	}

	// 3. Verify /api/v1/clients/register now enforces JWT auth when Auth.Enabled=true
	reqUnauthClient := httptest.NewRequest(http.MethodPost, "/api/v1/clients/register", bytes.NewReader([]byte(`{"client_id":"c1","owner_id":"client_worker"}`)))
	reqUnauthClient.Header.Set("Content-Type", "application/json")
	recUnauthClient := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recUnauthClient, reqUnauthClient)
	if recUnauthClient.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 from unauthenticated /api/v1/clients/register, got %d", recUnauthClient.Code)
	}

	// 4. Verify masked runtime config endpoint
	reqCfg := httptest.NewRequest(http.MethodGet, "/api/v1/cluster/config", nil)
	recCfg := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recCfg, reqCfg)
	if recCfg.Code != http.StatusOK {
		t.Fatalf("expected 200 from GET /api/v1/cluster/config, got %d", recCfg.Code)
	}
	var cfgPayload struct {
		Success bool                   `json:"success"`
		Data    map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(recCfg.Body.Bytes(), &cfgPayload); err != nil || !cfgPayload.Success {
		t.Fatalf("invalid /cluster/config payload: %v", err)
	}
	if sec, ok := cfgPayload.Data["security"].(map[string]interface{}); !ok || sec["jwt_secret_key"] != nil {
		t.Fatalf("security config missing or leaked jwt_secret_key: %+v", cfgPayload.Data["security"])
	}

	// Allow async audit pipeline flush
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		evs := repo.snapshot()
		hasExpire, hasReg, hasAuthFail := false, false, false
		for _, e := range evs {
			switch e.EventType {
			case "LOCK_EXPIRE":
				hasExpire = true
			case "CLIENT_REGISTER":
				hasReg = true
			case "AUTHENTICATION_FAILURE":
				hasAuthFail = true
			}
		}
		if hasExpire && hasReg && hasAuthFail {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("expected LOCK_EXPIRE, CLIENT_REGISTER, and AUTHENTICATION_FAILURE events in audit repo, got %+v", repo.snapshot())
}
