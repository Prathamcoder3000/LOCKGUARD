package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"lockguard/backend/internal/config"
	"lockguard/backend/internal/metrics"
	"lockguard/backend/internal/persistence"
)

type mockAuditRepo struct {
	mu     sync.Mutex
	events []*persistence.AuditEventRecord
}

func (m *mockAuditRepo) InsertEvent(ctx context.Context, event *persistence.AuditEventRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	event.ID = int64(len(m.events) + 1)
	m.events = append(m.events, event)
	return nil
}

func (m *mockAuditRepo) InsertBatch(ctx context.Context, events []*persistence.AuditEventRecord) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, e := range events {
		e.ID = int64(len(m.events) + 1)
		m.events = append(m.events, e)
		count++
	}
	return count, nil
}

func (m *mockAuditRepo) GetEventByID(ctx context.Context, eventID string) (*persistence.AuditEventRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.events {
		if e.EventID == eventID {
			return e, nil
		}
	}
	return nil, persistence.ErrEventNotFound
}

func (m *mockAuditRepo) ListEvents(ctx context.Context, filter persistence.AuditFilter) ([]*persistence.AuditEventRecord, int, error) {
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
		if filter.ClientID != "" && e.ClientID != filter.ClientID {
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

func TestEventValidationAndRedaction(t *testing.T) {
	ev := &Event{
		EventType:       TypeLockAcquire,
		ResourceID:      "database_rw",
		OwnerID:         "worker-1",
		ClientID:        "client-1",
		OperationStatus: StatusCommitted,
		Metadata: map[string]interface{}{
			"session_id":        "secret-uuid-123",
			"token_secret":      "super-secret-key",
			"password":          "adminpass",
			"authorization":     "Bearer eyJhbGciOi...",
			"safe_operation_id": "op-42",
		},
	}

	if err := ev.Validate(); err != nil {
		t.Fatalf("validation failed: %v", err)
	}

	if ev.EventID == "" {
		t.Error("expected generated EventID, got empty")
	}

	if ev.Metadata["session_id"] != "[REDACTED]" {
		t.Errorf("expected session_id to be redacted, got %v", ev.Metadata["session_id"])
	}
	if ev.Metadata["token_secret"] != "[REDACTED]" {
		t.Errorf("expected token_secret to be redacted, got %v", ev.Metadata["token_secret"])
	}
	if ev.Metadata["password"] != "[REDACTED]" {
		t.Errorf("expected password to be redacted, got %v", ev.Metadata["password"])
	}
	if ev.Metadata["authorization"] != "[REDACTED]" {
		t.Errorf("expected authorization to be redacted, got %v", ev.Metadata["authorization"])
	}
	if ev.Metadata["safe_operation_id"] != "op-42" {
		t.Errorf("expected safe field to be preserved, got %v", ev.Metadata["safe_operation_id"])
	}
}

func TestPipelineEnqueueAndBatchFlush(t *testing.T) {
	repo := &mockAuditRepo{}
	reg := metrics.NewRegistry()
	cfg := PipelineConfig{
		QueueCapacity:  100,
		BatchSize:      5,
		FlushInterval:  20 * time.Millisecond,
		MaxRetries:     1,
		RetryBaseDelay: 10 * time.Millisecond,
	}

	pipe := NewPipeline(repo, reg, cfg)
	pipe.Start()

	for i := 0; i < 12; i++ {
		ok := pipe.RecordEvent(&Event{
			EventType:       TypeLockAcquire,
			ResourceID:      "res-1",
			OperationStatus: StatusCommitted,
		})
		if !ok {
			t.Fatalf("failed to record event %d", i)
		}
	}

	// Wait for batches to flush
	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := pipe.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown error: %v", err)
	}

	stats := pipe.Stats()
	if stats.CreatedTotal != 12 {
		t.Errorf("expected 12 created events, got %d", stats.CreatedTotal)
	}
	if stats.PersistedTotal != 12 {
		t.Errorf("expected 12 persisted events, got %d", stats.PersistedTotal)
	}

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.events) != 12 {
		t.Errorf("expected 12 events in repo, got %d", len(repo.events))
	}
}

func TestPipelineBackpressureDropOnFullQueue(t *testing.T) {
	repo := &mockAuditRepo{}
	cfg := PipelineConfig{
		QueueCapacity:  2,
		BatchSize:      10,
		FlushInterval:  1 * time.Hour, // don't flush automatically
		MaxRetries:     0,
		RetryBaseDelay: 10 * time.Millisecond,
	}

	pipe := NewPipeline(repo, nil, cfg)
	// Do not start worker loop so queue fills up

	ok1 := pipe.RecordEvent(&Event{EventType: TypeLockAcquire})
	ok2 := pipe.RecordEvent(&Event{EventType: TypeLockRenew})
	ok3 := pipe.RecordEvent(&Event{EventType: TypeLockRelease})

	if !ok1 || !ok2 {
		t.Error("expected first 2 events to fit in queue")
	}
	if ok3 {
		t.Error("expected 3rd event to be dropped due to queue overflow backpressure")
	}

	stats := pipe.Stats()
	if stats.FailedTotal != 1 {
		t.Errorf("expected 1 failed event, got %d", stats.FailedTotal)
	}
}

func TestHandlerEndpoints(t *testing.T) {
	repo := &mockAuditRepo{}
	_ = repo.InsertEvent(context.Background(), &persistence.AuditEventRecord{
		EventID:         "test-event-123",
		EventType:       TypeLockAcquire,
		EventCategory:   CategoryLockOperation,
		ResourceID:      "order-456",
		ClientID:        "client-999",
		OperationStatus: StatusCommitted,
		RecordedAt:      time.Now().UTC(),
		EventTimestamp:  time.Now().UTC(),
	})

	cfg := &config.Config{}
	h := NewHandler(repo, nil, cfg)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// 1. Test GET /v1/audit/events
	req := httptest.NewRequest(http.MethodGet, "/v1/audit/events?resource_id=order-456", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Events []*persistence.AuditEventRecord `json:"events"`
			Total  int                             `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if !resp.Success || resp.Data.Total != 1 || len(resp.Data.Events) != 1 {
		t.Errorf("expected 1 matched event, got %+v", resp.Data)
	}

	// 2. Test GET /v1/audit/events/{id}
	req2 := httptest.NewRequest(http.MethodGet, "/v1/audit/events/test-event-123", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for event by ID, got %d", w2.Code)
	}

	// 3. Test GET /v1/audit/health
	req3 := httptest.NewRequest(http.MethodGet, "/v1/audit/health", nil)
	w3 := httptest.NewRecorder()
	mux.ServeHTTP(w3, req3)

	if w3.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for health, got %d", w3.Code)
	}
}

func TestAuditAuthorizationAndCrossClientIsolation(t *testing.T) {
	repo := &mockAuditRepo{}
	_ = repo.InsertEvent(context.Background(), &persistence.AuditEventRecord{
		EventID:         "evt-client-A",
		EventType:       TypeLockAcquire,
		EventCategory:   CategoryLockOperation,
		ResourceID:      "res-1",
		ClientID:        "client-A",
		OperationStatus: StatusCommitted,
		RecordedAt:      time.Now().UTC(),
		EventTimestamp:  time.Now().UTC(),
	})
	_ = repo.InsertEvent(context.Background(), &persistence.AuditEventRecord{
		EventID:         "evt-client-B",
		EventType:       TypeLockAcquire,
		EventCategory:   CategoryLockOperation,
		ResourceID:      "res-2",
		ClientID:        "client-B",
		OperationStatus: StatusCommitted,
		RecordedAt:      time.Now().UTC(),
		EventTimestamp:  time.Now().UTC(),
	})

	cfg := &config.Config{
		Auth: config.AuthConfig{
			Enabled:      true,
			JWTSecretKey: "secret-audit-key",
		},
	}
	h := NewHandler(repo, nil, cfg)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// 1. Missing Authorization header returns 401 Unauthorized
	req1 := httptest.NewRequest(http.MethodGet, "/v1/audit/events", nil)
	w1 := httptest.NewRecorder()
	mux.ServeHTTP(w1, req1)
	if w1.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for missing auth header, got %d", w1.Code)
	}

	// 2. Invalid bearer token returns 401 Unauthorized
	req2 := httptest.NewRequest(http.MethodGet, "/v1/audit/events", nil)
	req2.Header.Set("Authorization", "Bearer invalid-token-xyz")
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for invalid token, got %d", w2.Code)
	}

	// 3. Client A querying Client B's events (/v1/audit/clients/client-B) returns 403 Forbidden
	req3 := httptest.NewRequest(http.MethodGet, "/v1/audit/clients/client-B", nil)
	req3.Header.Set("Authorization", "Bearer client:client-A")
	w3 := httptest.NewRecorder()
	mux.ServeHTTP(w3, req3)
	if w3.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for cross-client access to client-B endpoint, got %d: %s", w3.Code, w3.Body.String())
	}

	// 4. Client A querying /v1/audit/events?client_id=client-B returns 403 Forbidden
	req4 := httptest.NewRequest(http.MethodGet, "/v1/audit/events?client_id=client-B", nil)
	req4.Header.Set("Authorization", "Bearer client:client-A")
	w4 := httptest.NewRecorder()
	mux.ServeHTTP(w4, req4)
	if w4.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for cross-client filter parameter, got %d", w4.Code)
	}

	// 5. Client A querying Client B's event by ID returns 403 Forbidden
	req5 := httptest.NewRequest(http.MethodGet, "/v1/audit/events/evt-client-B", nil)
	req5.Header.Set("Authorization", "Bearer client:client-A")
	w5 := httptest.NewRecorder()
	mux.ServeHTTP(w5, req5)
	if w5.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for cross-client event access by ID, got %d", w5.Code)
	}

	// 6. Client A querying its own client endpoint returns 200 OK
	req6 := httptest.NewRequest(http.MethodGet, "/v1/audit/clients/client-A", nil)
	req6.Header.Set("Authorization", "Bearer client:client-A")
	w6 := httptest.NewRecorder()
	mux.ServeHTTP(w6, req6)
	if w6.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for Client A querying own events, got %d: %s", w6.Code, w6.Body.String())
	}

	// 7. Admin token can access any client and any event
	req7 := httptest.NewRequest(http.MethodGet, "/v1/audit/clients/client-B", nil)
	req7.Header.Set("Authorization", "Bearer admin")
	w7 := httptest.NewRecorder()
	mux.ServeHTTP(w7, req7)
	if w7.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for admin accessing client-B endpoint, got %d", w7.Code)
	}

	req8 := httptest.NewRequest(http.MethodGet, "/v1/audit/events/evt-client-B", nil)
	req8.Header.Set("Authorization", "Bearer admin")
	w8 := httptest.NewRecorder()
	mux.ServeHTTP(w8, req8)
	if w8.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for admin accessing event by ID, got %d", w8.Code)
	}
}

