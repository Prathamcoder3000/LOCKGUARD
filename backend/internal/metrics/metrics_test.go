package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestMetricsRegistry_CounterAndGauge(t *testing.T) {
	reg := NewRegistry()

	// Test Counter
	reg.IncCounter("lockguard_gateway_requests_total", map[string]string{"method": "GET", "status": "200"})
	reg.IncCounter("lockguard_gateway_requests_total", map[string]string{"method": "GET", "status": "200"})
	reg.IncCounter("lockguard_gateway_requests_total", map[string]string{"method": "POST", "status": "200"})

	// Test Gauge
	reg.SetGauge("lockguard_active_client_sessions", nil, 5)
	reg.IncGauge("lockguard_active_client_sessions", nil)
	reg.DecGauge("lockguard_active_client_sessions", nil)

	handler := reg.Handler()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	bodyStr := string(body)

	if !strings.Contains(bodyStr, `lockguard_gateway_requests_total{method="GET",status="200"} 2`) {
		t.Errorf("missing GET 200 metric, got:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, `lockguard_gateway_requests_total{method="POST",status="200"} 1`) {
		t.Errorf("missing POST 200 metric, got:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, `lockguard_active_client_sessions 5`) {
		t.Errorf("missing active sessions gauge, got:\n%s", bodyStr)
	}
}

func TestMetricsRegistry_ConcurrentUpdates(t *testing.T) {
	reg := NewRegistry()
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				reg.IncCounter("lockguard_heartbeat_received_total", map[string]string{"status": "ok"})
				reg.IncGauge("lockguard_renewals_active", nil)
				reg.DecGauge("lockguard_renewals_active", nil)
			}
		}()
	}
	wg.Wait()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	reg.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `lockguard_heartbeat_received_total{status="ok"} 5000`) {
		t.Errorf("expected count 5000 for heartbeat counter, got:\n%s", body)
	}
}

func TestMetricsRegistry_BoundedCardinalityAndGlobalFencingTokenGauge(t *testing.T) {
	reg := NewRegistry()

	// Update global fencing token gauge across multiple resources
	reg.SetCurrentFencingToken(1001)
	reg.SetCurrentFencingToken(1050)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	reg.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()

	// Must be emitted as a single scalar gauge WITHOUT resource labels
	if !strings.Contains(body, "lockguard_fencing_token_current 1050") {
		t.Errorf("expected global scalar gauge without labels, got:\n%s", body)
	}
	if strings.Contains(body, "lockguard_fencing_token_current{") {
		t.Errorf("cardinality violation: fencing token metric contains labels:\n%s", body)
	}
}

