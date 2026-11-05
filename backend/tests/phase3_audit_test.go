package tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"lockguard/backend/internal/audit"
	"lockguard/backend/internal/config"
	"lockguard/backend/internal/gateway"
	"lockguard/backend/internal/lockstate"
	"lockguard/backend/internal/metrics"
	"lockguard/backend/internal/persistence"
	"lockguard/backend/internal/recovery"
)

// inMemoryAuditRepo provides a thread-safe repository implementation for integration tests.
type inMemoryAuditRepo struct {
	mu        sync.Mutex
	events    []*persistence.AuditEventRecord
	failBatch bool
}

func (m *inMemoryAuditRepo) InsertEvent(ctx context.Context, event *persistence.AuditEventRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.events {
		if e.EventID == event.EventID {
			return persistence.ErrDuplicateEvent
		}
	}
	event.ID = int64(len(m.events) + 1)
	m.events = append(m.events, event)
	return nil
}

func (m *inMemoryAuditRepo) InsertBatch(ctx context.Context, events []*persistence.AuditEventRecord) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failBatch {
		return 0, persistence.ErrDBUnavailable
	}
	count := 0
	for _, e := range events {
		isDup := false
		for _, ex := range m.events {
			if ex.EventID == e.EventID {
				isDup = true
				break
			}
		}
		if !isDup {
			e.ID = int64(len(m.events) + 1)
			m.events = append(m.events, e)
			count++
		}
	}
	return count, nil
}

func (m *inMemoryAuditRepo) GetEventByID(ctx context.Context, eventID string) (*persistence.AuditEventRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.events {
		if e.EventID == eventID {
			return e, nil
		}
	}
	return nil, persistence.ErrEventNotFound
}

func (m *inMemoryAuditRepo) ListEvents(ctx context.Context, filter persistence.AuditFilter) ([]*persistence.AuditEventRecord, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var matched []*persistence.AuditEventRecord
	for _, e := range m.events {
		if filter.EventType != "" && e.EventType != filter.EventType {
			continue
		}
		if filter.ResourceID != "" && e.ResourceID != filter.ResourceID {
			continue
		}
		if filter.OwnerID != "" && e.OwnerID != filter.OwnerID {
			continue
		}
		if filter.ClientID != "" && e.ClientID != filter.ClientID {
			continue
		}
		if filter.OperationStatus != "" && e.OperationStatus != filter.OperationStatus {
			continue
		}
		matched = append(matched, e)
	}

	total := len(matched)
	start := filter.Offset
	if start > total {
		start = total
	}
	end := start + filter.Limit
	if end > total {
		end = total
	}

	return matched[start:end], total, nil
}

// TestPhase3AuditPersistenceAndPagination validates audit event insertion, filtering, and pagination.
func TestPhase3AuditPersistenceAndPagination(t *testing.T) {
	repo := &inMemoryAuditRepo{}
	pipe := audit.NewPipeline(repo, metrics.NewRegistry(), audit.DefaultPipelineConfig())
	pipe.Start()
	defer pipe.Shutdown(context.Background())

	// Persist 15 audit events across two resources
	for i := 1; i <= 15; i++ {
		res := "res-alpha"
		if i%2 == 0 {
			res = "res-beta"
		}
		ok := pipe.RecordEvent(&audit.Event{
			EventID:         audit.GenerateEventID(),
			EventType:       audit.TypeLockAcquire,
			EventCategory:   audit.CategoryLockOperation,
			ResourceID:      res,
			OwnerID:         "worker-1",
			ClientID:        "client-1",
			FencingToken:    uint64(1000 + i),
			LeaseVersion:    1,
			OperationStatus: audit.StatusCommitted,
			CoordinatorID:   "coordinator-a",
			EventTimestamp:  time.Now().UTC(),
		})
		if !ok {
			t.Fatalf("failed to record event %d", i)
		}
	}

	// Wait for pipeline batch flusher
	time.Sleep(120 * time.Millisecond)

	// 1. Query all events with pagination: limit 5, offset 0
	ctx := context.Background()
	events, total, err := repo.ListEvents(ctx, persistence.AuditFilter{Limit: 5, Offset: 0})
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}
	if total != 15 {
		t.Errorf("expected total 15 events, got %d", total)
	}
	if len(events) != 5 {
		t.Errorf("expected 5 events in page, got %d", len(events))
	}

	// 2. Query page 2: limit 5, offset 5
	events2, total2, err := repo.ListEvents(ctx, persistence.AuditFilter{Limit: 5, Offset: 5})
	if err != nil {
		t.Fatalf("ListEvents page 2 failed: %v", err)
	}
	if total2 != 15 || len(events2) != 5 {
		t.Errorf("expected 5 events in page 2, got %d", len(events2))
	}

	// Verify no overlap between page 1 and page 2
	for _, e1 := range events {
		for _, e2 := range events2 {
			if e1.EventID == e2.EventID {
				t.Errorf("found duplicate event between pages: %s", e1.EventID)
			}
		}
	}

	// 3. Filter by ResourceID "res-beta" (should match 7 items)
	betaEvents, betaTotal, err := repo.ListEvents(ctx, persistence.AuditFilter{ResourceID: "res-beta", Limit: 50})
	if err != nil {
		t.Fatalf("filter by resource failed: %v", err)
	}
	if betaTotal != 7 || len(betaEvents) != 7 {
		t.Errorf("expected 7 res-beta events, got total=%d len=%d", betaTotal, len(betaEvents))
	}
}

// TestPhase3DatabaseOutageIsolation proves that Raft lock operations and state transitions
// proceed completely unaffected when PostgreSQL is completely down.
func TestPhase3DatabaseOutageIsolation(t *testing.T) {
	// Configure simulated database outage in repository
	repo := &inMemoryAuditRepo{failBatch: true}
	reg := metrics.NewRegistry()
	cfg := audit.PipelineConfig{
		QueueCapacity:  50,
		BatchSize:      2,
		FlushInterval:  10 * time.Millisecond,
		MaxRetries:     1,
		RetryBaseDelay: 5 * time.Millisecond,
	}

	pipe := audit.NewPipeline(repo, reg, cfg)
	pipe.Start()
	defer pipe.Shutdown(context.Background())

	// Initialize deterministic lock state machine
	sm := lockstate.New()

	// Perform 10 Raft state-machine lock operations during active DB outage
	for i := 1; i <= 10; i++ {
		cmd := lockstate.AcquireLockCommand{
			RequestID:         audit.GenerateEventID(),
			ResourceName:      "critical-database-cluster",
			OwnerID:           "primary-worker",
			TTLNanoseconds:    int64(10 * time.Second),
			TimestampUnixNano: time.Now().UnixNano(),
		}
		// Reset lock for test cycle if held
		if i > 1 {
			_ = sm.Apply(lockstate.ReleaseLockCommand{
				RequestID:         audit.GenerateEventID(),
				ResourceName:      "critical-database-cluster",
				OwnerID:           "primary-worker",
				FencingToken:      uint64(i - 1),
				TimestampUnixNano: time.Now().UnixNano(),
			})
		}

		res := sm.Apply(cmd)
		if !res.Success {
			t.Fatalf("lock acquisition failed during DB outage: %v", res.Error)
		}
		if res.FencingToken == 0 {
			t.Fatalf("invalid lock record during DB outage")
		}

		// Asynchronously pipe audit event
		pipe.RecordEvent(&audit.Event{
			EventID:         audit.GenerateEventID(),
			EventType:       audit.TypeLockAcquire,
			ResourceID:      cmd.ResourceName,
			OwnerID:         cmd.OwnerID,
			FencingToken:    res.FencingToken,
			LeaseVersion:    res.LeaseVersion,
			OperationStatus: audit.StatusCommitted,
		})
	}

	// Confirm all 10 lock acquisitions in the state machine succeeded
	active := sm.ListLocks()
	if len(active) != 1 {
		t.Fatalf("expected 1 active lock in state machine, got %d", len(active))
	}
	if active[0].ResourceName != "critical-database-cluster" {
		t.Errorf("unexpected lock resource: %s", active[0].ResourceName)
	}

	// Allow pipeline to attempt flush
	time.Sleep(100 * time.Millisecond)

	stats := pipe.Stats()
	if stats.CreatedTotal != 10 {
		t.Errorf("expected 10 created events, got %d", stats.CreatedTotal)
	}
	// Under DB outage, persisted events are 0 and failed events increment
	if stats.PersistedTotal != 0 {
		t.Errorf("expected 0 persisted events during DB outage, got %d", stats.PersistedTotal)
	}
	if stats.FailedTotal == 0 {
		t.Error("expected failed count to reflect persistent DB failure")
	}

	// Now restore database connectivity
	repo.mu.Lock()
	repo.failBatch = false
	repo.mu.Unlock()

	// New events after DB recovery persist successfully
	ok := pipe.RecordEvent(&audit.Event{
		EventID:         audit.GenerateEventID(),
		EventType:       audit.TypeLockRenew,
		ResourceID:      "critical-database-cluster",
		OperationStatus: audit.StatusCommitted,
	})
	if !ok {
		t.Fatal("failed to record event after DB recovery")
	}

	time.Sleep(50 * time.Millisecond)

	statsAfter := pipe.Stats()
	if statsAfter.PersistedTotal < 1 {
		t.Errorf("expected at least 1 persisted event after recovery, got %d", statsAfter.PersistedTotal)
	}
}

// TestPhase3DiagnosticsDistinguishesRaftFromDatabase verifies that Raft quorum loss causes
// UNHEALTHY status while a database outage only degrades operational audit visibility.
func TestPhase3DiagnosticsDistinguishesRaftFromDatabase(t *testing.T) {
	// Scenario 1: Raft discovery has active leader, DB is down
	leaderSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success":true,"data":{"node_id":"coordinator-a","is_leader":true,"state":"Leader"}}`))
	}))
	defer leaderSrv.Close()

	discovery := gateway.NewLeaderDiscovery(
		[]string{leaderSrv.URL},
		10*time.Second,
		leaderSrv.Client(),
		nil,
	)

	diag := recovery.NewDiagnosticsService(
		"gateway-1",
		"lockguard-api",
		discovery,
		nil, // DB is unconfigured/down
		nil,
		nil,
	)

	report := diag.CollectReport(context.Background())
	if !report.RaftQuorumAvailable || !report.LeaderAvailable {
		t.Errorf("expected Raft quorum available with healthy leader, got quorum=%v leader=%v",
			report.RaftQuorumAvailable, report.LeaderAvailable)
	}
	if report.DatabaseAvailable {
		t.Error("expected DatabaseAvailable to be false when DB is nil")
	}
	// Crucial invariant: Service status is DEGRADED, NOT UNHEALTHY, because Raft consensus is healthy!
	if report.OverallStatus != recovery.StatusDegraded {
		t.Errorf("expected DEGRADED when only DB is down and Raft is active, got %s", report.OverallStatus)
	}

	// Scenario 2: Raft leader unreachable -> Overall status MUST be UNHEALTHY (Fail Closed)
	unreachableDiscovery := gateway.NewLeaderDiscovery(
		[]string{"http://127.0.0.1:59999"},
		100*time.Millisecond,
		&http.Client{Timeout: 50 * time.Millisecond},
		nil,
	)

	diagQuorumLost := recovery.NewDiagnosticsService(
		"gateway-1",
		"lockguard-api",
		unreachableDiscovery,
		nil,
		nil,
		nil,
	)

	reportLost := diagQuorumLost.CollectReport(context.Background())
	if reportLost.OverallStatus != recovery.StatusUnhealthy {
		t.Errorf("expected UNHEALTHY when Raft leader unreachable, got %s", reportLost.OverallStatus)
	}
}

// TestPhase3AuditSecretRedactionInAPI confirms that session IDs and secrets are never returned in audit queries.
func TestPhase3AuditSecretRedactionInAPI(t *testing.T) {
	repo := &inMemoryAuditRepo{}
	ev := &audit.Event{
		EventID:         "sec-audit-1",
		EventType:       audit.TypeLockAcquire,
		ResourceID:      "finance-ledger",
		OwnerID:         "accountant-1",
		OperationStatus: audit.StatusCommitted,
		Metadata: map[string]interface{}{
			"session_id":    "sensitive-session-token-xyz",
			"password":      "secret123",
			"authorization": "Bearer eyJhbGci...",
			"safe_field":    "allowed_public_val",
		},
	}
	_ = ev.Validate()
	rec, _ := ev.ToPersistenceRecord()
	_ = repo.InsertEvent(context.Background(), rec)

	h := audit.NewHandler(repo, nil, &config.Config{})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/v1/audit/events/sec-audit-1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	body := w.Body.String()
	if body == "" {
		t.Fatal("empty response body")
	}

	forbiddenStrings := []string{
		"sensitive-session-token-xyz",
		"secret123",
		"Bearer eyJhbGci...",
	}
	for _, forbidden := range forbiddenStrings {
		if containsString(body, forbidden) {
			t.Fatalf("security violation: raw secret %q leaked in audit API response: %s", forbidden, body)
		}
	}

	if !containsString(body, "[REDACTED]") {
		t.Errorf("expected [REDACTED] in audit payload, got %s", body)
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && len(substr) > 0 && searchSubstr(s, substr))
}

func searchSubstr(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (s[:len(sub)] == sub || searchSubstr(s[1:], sub)))
}
