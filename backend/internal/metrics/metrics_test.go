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

func TestMetricsRegistry_Phase3MetricsBoundedCardinality(t *testing.T) {
	reg := NewRegistry()

	// 1. Audit pipeline metrics
	reg.IncCounter("lockguard_audit_events_created_total", map[string]string{"event_type": "LOCK_ACQUIRE", "status": "COMMITTED"})
	reg.IncCounter("lockguard_audit_events_created_total", map[string]string{"event_type": "LOCK_RELEASE", "status": "COMMITTED"})
	reg.IncCounter("lockguard_audit_events_persisted_total", map[string]string{"status": "success"})
	reg.IncCounter("lockguard_audit_events_failed_total", map[string]string{"reason": "backpressure_drop"})
	reg.SetGauge("lockguard_audit_queue_size", nil, 42)
	reg.SetGauge("lockguard_audit_delivery_latency_seconds", nil, 15)

	// 2. Database persistence & migration metrics
	reg.SetGauge("lockguard_database_health", nil, 1)
	reg.SetGauge("lockguard_database_migrations_status", nil, 1)
	reg.SetGauge("lockguard_database_connections", map[string]string{"state": "open"}, 10)
	reg.SetGauge("lockguard_database_connections", map[string]string{"state": "in_use"}, 2)
	reg.SetGauge("lockguard_database_connections", map[string]string{"state": "idle"}, 8)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	reg.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from metrics handler, got %d", rec.Code)
	}

	body := rec.Body.String()

	// Verify required Phase 3 metrics are present in Prometheus output
	expectedSubstrings := []string{
		`lockguard_audit_events_created_total{event_type="LOCK_ACQUIRE",status="COMMITTED"} 1`,
		`lockguard_audit_events_persisted_total{status="success"} 1`,
		`lockguard_audit_events_failed_total{reason="backpressure_drop"} 1`,
		`lockguard_audit_queue_size 42`,
		`lockguard_audit_delivery_latency_seconds 15`,
		`lockguard_database_health 1`,
		`lockguard_database_migrations_status 1`,
		`lockguard_database_connections{state="open"} 10`,
		`lockguard_database_connections{state="in_use"} 2`,
		`lockguard_database_connections{state="idle"} 8`,
	}

	for _, sub := range expectedSubstrings {
		if !strings.Contains(body, sub) {
			t.Errorf("expected output to contain %q, but was missing.\nFull output:\n%s", sub, body)
		}
	}

	// Verify no high-cardinality labels (resource_id, client_id, event_id, owner_id) are present
	highCardinalityLabels := []string{"resource_id=", "client_id=", "event_id=", "owner_id=", "fencing_token="}
	for _, badLabel := range highCardinalityLabels {
		if strings.Contains(body, badLabel) {
			t.Errorf("critical cardinality leak detected: metric output contains forbidden label %q", badLabel)
		}
	}
}

func TestMetricsRegistry_Phase4ResilienceMetrics(t *testing.T) {
	reg := NewRegistry()

	// Update Phase 4 resilience metrics
	reg.IncChaosTests("COORDINATOR_NETWORK_PARTITION")
	reg.IncCoordinatorFailures("coord-a")
	reg.IncLeaderChanges()
	reg.IncNetworkPartitions("isolated_leader")
	reg.IncRecoveryAttempts("raft")
	reg.IncRecoveryFailures("database")
	reg.IncSnapshotRestore("success")
	reg.SetQuorumAvailable(true)
	reg.SetClusterNodesAvailable(3)
	reg.IncProtectedResourceErrors("stale_fencing_token")
	reg.SetRecoveryDuration("leader_election", 0.45)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	reg.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from metrics handler, got %d", rec.Code)
	}

	body := rec.Body.String()

	expectedSubstrings := []string{
		`lockguard_chaos_tests_total{type="COORDINATOR_NETWORK_PARTITION"} 1`,
		`lockguard_coordinator_failures_total{node_id="coord-a"} 1`,
		`lockguard_leader_changes_total 1`,
		`lockguard_network_partitions_total{topology="isolated_leader"} 1`,
		`lockguard_recovery_attempts_total{subsystem="raft"} 1`,
		`lockguard_recovery_failures_total{subsystem="database"} 1`,
		`lockguard_snapshot_restore_total{status="success"} 1`,
		`lockguard_quorum_available 1`,
		`lockguard_cluster_nodes_available 3`,
		`lockguard_protected_resource_errors_total{error_type="stale_fencing_token"} 1`,
		`lockguard_recovery_duration_seconds{action="leader_election"} 450`,
	}

	for _, sub := range expectedSubstrings {
		if !strings.Contains(body, sub) {
			t.Errorf("expected metric output to contain %q, but was missing.\nFull body:\n%s", sub, body)
		}
	}

	// Verify no high cardinality leaks
	forbidden := []string{"session_id=", "request_id=", "client_id=", "resource_id="}
	for _, bad := range forbidden {
		if strings.Contains(body, bad) {
			t.Errorf("cardinality leak: body contains %q", bad)
		}
	}
}

func TestMetricsRegistry_Phase5SecurityAndPerformanceMetrics(t *testing.T) {
	reg := NewRegistry()

	reg.IncAuthSuccess("client")
	reg.IncAuthFailure("invalid_token")
	reg.IncAuthzDenial("read_only")
	reg.IncRateLimitExceeded("locks_acquire")
	reg.IncHTTPRequest("POST", "2xx")
	reg.SetActiveSessions(12)
	reg.IncSecurityViolation("cross_client_access_attempt")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	reg.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()

	expectedSubstrings := []string{
		`lockguard_auth_successes_total{role="client"} 1`,
		`lockguard_auth_failures_total{reason="invalid_token"} 1`,
		`lockguard_authz_denials_total{role="read_only"} 1`,
		`lockguard_rate_limit_exceeded_total{endpoint="locks_acquire"} 1`,
		`lockguard_http_requests_total{method="POST",status_class="2xx"} 1`,
		`lockguard_active_sessions 12`,
		`lockguard_security_violations_total{violation_type="cross_client_access_attempt"} 1`,
	}

	for _, sub := range expectedSubstrings {
		if !strings.Contains(body, sub) {
			t.Errorf("expected metric output to contain %q, but was missing.\nFull body:\n%s", sub, body)
		}
	}
}

func TestMetricsRegistry_RecoveryDurationReplacesPriorActionAndClampsSubMillisecond(t *testing.T) {
	reg := NewRegistry()

	// Sub-millisecond positive duration should clamp to 1ms instead of truncating to 0ms
	reg.SetRecoveryDuration("leader_election", 0.0002)
	val, lbl := reg.GetLatestGauge("lockguard_recovery_duration_seconds")
	if val != 1 || lbl != `{action="leader_election"}` {
		t.Fatalf("expected 1ms for leader_election, got val=%d lbl=%s", val, lbl)
	}

	// Updating with a different action replaces the prior action series so UI summing doesn't accumulate stale series
	reg.SetRecoveryDuration("node_rejoin", 1.25)
	val, lbl = reg.GetLatestGauge("lockguard_recovery_duration_seconds")
	if val != 1250 || lbl != `{action="node_rejoin"}` {
		t.Fatalf("expected 1250ms for node_rejoin, got val=%d lbl=%s", val, lbl)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	reg.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if strings.Contains(body, `action="leader_election"`) {
		t.Fatalf("expected previous recovery duration series to be replaced, got:\n%s", body)
	}
	if !strings.Contains(body, `lockguard_recovery_duration_seconds{action="node_rejoin"} 1250`) {
		t.Fatalf("expected latest recovery duration in Prometheus output, got:\n%s", body)
	}

	reg.IncAuthzDenial("client")
	reg.IncAuthzDenial("auditor")
	if total := reg.GetCounterTotal("lockguard_authz_denials_total"); total != 2 {
		t.Fatalf("expected summed counter total=2, got %d", total)
	}
}


