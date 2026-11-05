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

