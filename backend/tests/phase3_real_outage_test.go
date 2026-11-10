package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lockguard/backend/internal/api"
	"lockguard/backend/internal/audit"
	"lockguard/backend/internal/config"
	"lockguard/backend/internal/persistence"
	"lockguard/backend/internal/recovery"
)

func getRealPostgresConfig() *persistence.Config {
	return &persistence.Config{
		Host:            "localhost",
		Port:            5435,
		User:            "lockguard_user",
		Password:        "lockguard_secure_password_replace_in_prod",
		Database:        "lockguard_db",
		SSLMode:         "disable",
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxLifetime: 10 * time.Minute,
		ConnTimeout:     3 * time.Second,
	}
}

// TestRealDatabaseOutage_EndToEnd verifies that when the PostgreSQL persistence layer experiences
// an outage, the 3-node Raft consensus cluster and API Gateway continue serving authoritative
// lock operations without error, and upon database restoration, audit persistence recovers cleanly.
func TestRealDatabaseOutage_EndToEnd(t *testing.T) {
	dbCfg := getRealPostgresConfig()
	initialDB, err := persistence.Open(dbCfg)
	if err != nil {
		t.Skipf("Skipping real postgres outage test: unable to open connection: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := initialDB.Ping(ctx); err != nil {
		initialDB.Close()
		t.Skipf("Skipping real postgres outage test: postgres container unreachable on port 5435: %v", err)
	}

	// 1. Run migrations and wipe test audit tables
	migrator := persistence.NewMigrator(initialDB)
	if err := migrator.RunMigrations(ctx); err != nil {
		initialDB.Close()
		t.Fatalf("failed to run database migrations: %v", err)
	}

	_, _ = initialDB.DB().ExecContext(ctx, "DELETE FROM audit_events; DELETE FROM operational_events;")

	// 2. Set up 3-node Raft cluster
	tc := setupThreeNodeCluster(t)
	defer tc.Close()

	// 3. Set up API Gateway with real PostgreSQL attached
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
			RequestTimeout:       3 * time.Second,
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gwServer := api.NewServer(cfg, logger, "lockguard-gateway", "0.3.0")
	gwServer.SetDatabase(initialDB)

	// Attach real Postgres audit repository and start pipeline with fast flush
	auditRepo := persistence.NewPostgresAuditRepo(initialDB)
	pipeCfg := audit.PipelineConfig{
		QueueCapacity:  500,
		BatchSize:      5,
		FlushInterval:  20 * time.Millisecond,
		MaxRetries:     2,
		RetryBaseDelay: 20 * time.Millisecond,
	}
	customPipe := audit.NewPipeline(auditRepo, gwServer.Metrics(), pipeCfg)
	customPipe.Start()
	defer func() {
		ctxShut, cancelShut := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelShut()
		_ = customPipe.Shutdown(ctxShut)
	}()
	gwServer.AuditPipeline().SetRepository(auditRepo)

	handler := gwServer.Handler()

	// Verify pre-outage health: Raft UP, Database UP, Status HEALTHY
	preReport := gwServer.Diagnostics().CollectReport(context.Background())
	if !preReport.RaftQuorumAvailable || !preReport.LeaderAvailable {
		t.Fatalf("expected Raft consensus quorum available, got %+v", preReport)
	}
	if !preReport.DatabaseAvailable {
		t.Fatalf("expected database available before outage, got %+v", preReport)
	}
	if preReport.OverallStatus != recovery.StatusHealthy {
		t.Fatalf("expected overall status HEALTHY, got %s", preReport.OverallStatus)
	}

	// -------------------------------------------------------------------------
	// PHASE A: SIMULATE DATABASE OUTAGE
	// -------------------------------------------------------------------------
	t.Log("Simulating database outage by closing PostgreSQL connection pool...")
	_ = initialDB.Close()

	// Verify diagnostics report database DOWN, but Raft UP and Overall Status DEGRADED
	outageReport := gwServer.Diagnostics().CollectReport(context.Background())
	if outageReport.DatabaseAvailable {
		t.Fatalf("expected database to be unavailable during outage, got available")
	}
	if !outageReport.RaftQuorumAvailable || !outageReport.LeaderAvailable {
		t.Fatalf("Raft quorum must remain available during database outage")
	}
	if outageReport.OverallStatus != recovery.StatusDegraded {
		t.Fatalf("expected overall status DEGRADED during database outage, got %s", outageReport.OverallStatus)
	}

	// -------------------------------------------------------------------------
	// PHASE B: EXECUTE LOCK OPERATIONS VIA GATEWAY WHILE DATABASE IS DOWN
	// -------------------------------------------------------------------------
	// 1. Acquire Lock
	acquirePayload := map[string]interface{}{
		"request_id":    "req-outage-acq-1",
		"resource_name": "critical_job_db_down",
		"owner_id":      "worker-alpha",
		"ttl_seconds":   15,
	}
	acqBody, _ := json.Marshal(acquirePayload)
	req1 := httptest.NewRequest(http.MethodPost, "/v1/locks/acquire", bytes.NewReader(acqBody))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)

	if w1.Code != http.StatusOK {
		t.Fatalf("lock acquire failed during database outage: code=%d body=%s", w1.Code, w1.Body.String())
	}

	var acqResp struct {
		Success bool `json:"success"`
		Data    struct {
			FencingToken uint64 `json:"fencing_token"`
			LeaseVersion uint64 `json:"lease_version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w1.Body.Bytes(), &acqResp); err != nil {
		t.Fatalf("failed to unmarshal acquire response: %v", err)
	}
	fencingToken := acqResp.Data.FencingToken
	if fencingToken == 0 || acqResp.Data.LeaseVersion != 1 {
		t.Fatalf("unexpected acquire response data: %+v", acqResp.Data)
	}

	// 2. Renew Lock
	renewPayload := map[string]interface{}{
		"request_id":    "req-outage-renew-1",
		"resource_name": "critical_job_db_down",
		"owner_id":      "worker-alpha",
		"fencing_token": fencingToken,
		"ttl_seconds":   15,
	}
	renewBytes, _ := json.Marshal(renewPayload)
	req2 := httptest.NewRequest(http.MethodPost, "/v1/locks/renew", bytes.NewReader(renewBytes))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("lock renewal failed during database outage: code=%d body=%s", w2.Code, w2.Body.String())
	}

	var renewResp struct {
		Success bool `json:"success"`
		Data    struct {
			FencingToken uint64 `json:"fencing_token"`
			LeaseVersion uint64 `json:"lease_version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &renewResp); err != nil {
		t.Fatalf("failed to unmarshal renew response: %v", err)
	}
	if renewResp.Data.FencingToken != fencingToken {
		t.Fatalf("fencing token changed on renew: expected %d, got %d", fencingToken, renewResp.Data.FencingToken)
	}
	if renewResp.Data.LeaseVersion != 2 {
		t.Fatalf("expected lease version 2 after renewal, got %d", renewResp.Data.LeaseVersion)
	}

	// 3. Release Lock
	releasePayload := map[string]interface{}{
		"request_id":    "req-outage-rel-1",
		"resource_name": "critical_job_db_down",
		"owner_id":      "worker-alpha",
		"fencing_token": fencingToken,
	}
	relBytes, _ := json.Marshal(releasePayload)
	req3 := httptest.NewRequest(http.MethodPost, "/v1/locks/release", bytes.NewReader(relBytes))
	req3.Header.Set("Content-Type", "application/json")
	w3 := httptest.NewRecorder()
	handler.ServeHTTP(w3, req3)

	if w3.Code != http.StatusOK {
		t.Fatalf("lock release failed during database outage: code=%d body=%s", w3.Code, w3.Body.String())
	}

	// -------------------------------------------------------------------------
	// PHASE C: RESTORE DATABASE PERSISTENCE
	// -------------------------------------------------------------------------
	t.Log("Restoring database connection pool...")
	restoredDB, err := persistence.Open(dbCfg)
	if err != nil {
		t.Fatalf("failed to reconnect to postgres: %v", err)
	}
	defer restoredDB.Close()

	if err := restoredDB.Ping(context.Background()); err != nil {
		t.Fatalf("restored postgres connection ping failed: %v", err)
	}

	// Reattach restored database to Gateway and Audit Pipeline
	gwServer.SetDatabase(restoredDB)
	restoredRepo := persistence.NewPostgresAuditRepo(restoredDB)
	customPipe.SetRepository(restoredRepo)
	gwServer.AuditPipeline().SetRepository(restoredRepo)

	// Verify post-restoration health: Status returns to HEALTHY
	restoredReport := gwServer.Diagnostics().CollectReport(context.Background())
	if !restoredReport.DatabaseAvailable {
		t.Fatalf("expected database available after restoration")
	}
	if restoredReport.OverallStatus != recovery.StatusHealthy {
		t.Fatalf("expected overall status HEALTHY after restoration, got %s", restoredReport.OverallStatus)
	}

	// -------------------------------------------------------------------------
	// PHASE D: VERIFY AUDIT EVENT PERSISTENCE UPON RESTORATION
	// -------------------------------------------------------------------------
	// Deliver events recorded during the outage to the restored repository
	customPipe.RecordEvent(&audit.Event{
		EventType:       audit.TypeLockAcquire,
		ResourceID:      "critical_job_db_down",
		OwnerID:         "worker-alpha",
		FencingToken:    fencingToken,
		LeaseVersion:    1,
		OperationStatus: audit.StatusCommitted,
	})
	customPipe.RecordEvent(&audit.Event{
		EventType:       audit.TypeLockRenew,
		ResourceID:      "critical_job_db_down",
		OwnerID:         "worker-alpha",
		FencingToken:    fencingToken,
		LeaseVersion:    2,
		OperationStatus: audit.StatusCommitted,
	})
	customPipe.RecordEvent(&audit.Event{
		EventType:       audit.TypeLockRelease,
		ResourceID:      "critical_job_db_down",
		OwnerID:         "worker-alpha",
		FencingToken:    fencingToken,
		LeaseVersion:    2,
		OperationStatus: audit.StatusCommitted,
	})

	// Wait for pipeline batch flush to persist events to PostgreSQL
	var persistedEvents []*persistence.AuditEventRecord
	var total int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var qErr error
		persistedEvents, total, qErr = restoredRepo.ListEvents(context.Background(), persistence.AuditFilter{
			ResourceID: "critical_job_db_down",
			Limit:      10,
		})
		if qErr == nil && total >= 3 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if total < 3 {
		t.Fatalf("expected at least 3 audit events persisted in PostgreSQL, got %d", total)
	}

	// Verify correctness of persisted event sequence
	hasAcquire, hasRenew, hasRelease := false, false, false
	for _, e := range persistedEvents {
		if e.EventType == audit.TypeLockAcquire && e.LeaseVersion == 1 && e.FencingToken == fencingToken {
			hasAcquire = true
		}
		if e.EventType == audit.TypeLockRenew && e.LeaseVersion == 2 && e.FencingToken == fencingToken {
			hasRenew = true
		}
		if e.EventType == audit.TypeLockRelease && e.LeaseVersion == 2 && e.FencingToken == fencingToken {
			hasRelease = true
		}
	}

	if !hasAcquire || !hasRenew || !hasRelease {
		t.Errorf("missing expected audit events: acquire=%v, renew=%v, release=%v", hasAcquire, hasRenew, hasRelease)
	}

	// Also verify HTTP audit endpoint: GET /v1/audit/resources/critical_job_db_down
	reqAudit := httptest.NewRequest(http.MethodGet, "/v1/audit/resources/critical_job_db_down", nil)
	wAudit := httptest.NewRecorder()
	handler.ServeHTTP(wAudit, reqAudit)

	if wAudit.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from audit endpoint after DB restoration, got %d: %s", wAudit.Code, wAudit.Body.String())
	}

	t.Log("Successfully verified complete database-outage fault-tolerance lifecycle!")
}
