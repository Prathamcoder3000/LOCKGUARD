package recovery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lockguard/backend/internal/audit"
	"lockguard/backend/internal/gateway"
	"lockguard/backend/internal/metrics"
)

func TestDiagnosticsQuorumLossCausesUnhealthy(t *testing.T) {
	// Leader discovery is nil -> Raft unavailable
	diag := NewDiagnosticsService("gw-1", "lockguard-api", nil, nil, nil, nil)
	report := diag.CollectReport(context.Background())

	if report.OverallStatus != StatusUnhealthy {
		t.Errorf("expected UNHEALTHY when Raft quorum unavailable, got %s", report.OverallStatus)
	}
	if report.RaftQuorumAvailable {
		t.Error("expected RaftQuorumAvailable to be false")
	}
}

func TestDiagnosticsDatabaseDownCausesDegradedNotUnhealthyWhenRaftLeaderPresent(t *testing.T) {
	// Create mock pipeline
	pipe := audit.NewPipeline(nil, nil, audit.DefaultPipelineConfig())
	diag := NewDiagnosticsService("gw-1", "lockguard-api", nil, nil, nil, pipe)

	report := diag.CollectReport(context.Background())
	// Without discovery, raft is degraded/unhealthy
	if report.OverallStatus != StatusUnhealthy {
		t.Errorf("expected overall UNHEALTHY without discovery, got %s", report.OverallStatus)
	}

	dbHealth := report.Subsystems["database_persistence"]
	if dbHealth.Status != StatusDegraded {
		t.Errorf("expected database status DEGRADED, got %s", dbHealth.Status)
	}
}

func TestDiagnosticsSubsystemsReported(t *testing.T) {
	pipe := audit.NewPipeline(nil, nil, audit.DefaultPipelineConfig())
	diag := NewDiagnosticsService("node-a", "lockguard-test", nil, nil, nil, pipe)
	report := diag.CollectReport(context.Background())

	requiredSubsystems := []string{"raft_consensus", "database_persistence", "schema_migrations", "audit_pipeline"}
	for _, sub := range requiredSubsystems {
		if _, ok := report.Subsystems[sub]; !ok {
			t.Errorf("missing subsystem in diagnostic report: %s", sub)
		}
	}

	if report.NodeID != "node-a" {
		t.Errorf("expected node_id 'node-a', got '%s'", report.NodeID)
	}
	if report.Timestamp.IsZero() {
		t.Error("timestamp must not be zero")
	}
	if time.Since(report.Timestamp) > 5*time.Second {
		t.Error("report timestamp is stale")
	}
}

func TestRecoveryEndpoints_DiagnosticsAndHealth(t *testing.T) {
	diag := NewDiagnosticsService("gw-test", "lockguard-api", nil, nil, nil, nil)
	h := NewHandler(diag)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// 1. Diagnostics endpoint returns 503 when quorum lost
	reqDiag := httptest.NewRequest(http.MethodGet, "/v1/recovery/diagnostics", nil)
	wDiag := httptest.NewRecorder()
	mux.ServeHTTP(wDiag, reqDiag)

	if wDiag.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for diagnostics under quorum loss, got %d", wDiag.Code)
	}

	// 2. Health endpoint returns 503 when quorum lost (fail-closed)
	reqHealth := httptest.NewRequest(http.MethodGet, "/v1/recovery/health", nil)
	wHealth := httptest.NewRecorder()
	mux.ServeHTTP(wHealth, reqHealth)

	if wHealth.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for health under quorum loss, got %d", wHealth.Code)
	}

	// 3. Cluster and Snapshot endpoints
	reqCluster := httptest.NewRequest(http.MethodGet, "/v1/recovery/cluster", nil)
	wCluster := httptest.NewRecorder()
	mux.ServeHTTP(wCluster, reqCluster)

	if wCluster.Code != http.StatusOK {
		t.Errorf("expected 200 OK for cluster status, got %d", wCluster.Code)
	}

	reqSnap := httptest.NewRequest(http.MethodGet, "/v1/recovery/snapshots", nil)
	wSnap := httptest.NewRecorder()
	mux.ServeHTTP(wSnap, reqSnap)

	if wSnap.Code != http.StatusOK {
		t.Errorf("expected 200 OK for snapshot status, got %d", wSnap.Code)
	}
}

func TestCollectClusterReport_UnreachableNodesAndQuorumLoss(t *testing.T) {
	liveSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"node_id":"coordinator-a","state":"Leader","is_leader":true,"leader_id":"coordinator-a"}}`))
	}))
	defer liveSrv.Close()

	deadSrv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL1 := deadSrv1.URL
	deadSrv1.Close()

	deadSrv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL2 := deadSrv2.URL
	deadSrv2.Close()

	disc := gateway.NewLeaderDiscovery([]string{liveSrv.URL, deadURL1, deadURL2}, time.Second, nil, metrics.NewRegistry())
	diag := NewDiagnosticsService("gw-test", "lockguard-api", disc, nil, nil, nil)

	report := diag.CollectClusterReport(context.Background())

	if report.TotalNodes != 3 {
		t.Fatalf("expected 3 total nodes, got %d", report.TotalNodes)
	}
	if report.QuorumAvailable {
		t.Fatalf("expected QuorumAvailable=false when 2 of 3 coordinators are down")
	}
	if report.QuorumState != "QUORUM_LOST" {
		t.Fatalf("expected QuorumState=QUORUM_LOST, got %s", report.QuorumState)
	}
	if report.CurrentLeaderID != "" || report.CurrentLeaderURL != "" {
		t.Fatalf("expected leader cleared on quorum loss, got id=%q url=%q", report.CurrentLeaderID, report.CurrentLeaderURL)
	}
	if report.AvailableNodes == nil || len(report.AvailableNodes) != 1 || report.AvailableNodes[0] != "coordinator-a" {
		t.Fatalf("expected non-nil AvailableNodes [coordinator-a], got %#v", report.AvailableNodes)
	}
	for i, c := range report.Coordinators {
		if strings.TrimSpace(c.NodeID) == "" {
			t.Fatalf("expected non-empty fallback NodeID at index %d for endpoint %s", i, c.Endpoint)
		}
	}

	rawJSON, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("failed to marshal report: %v", err)
	}
	if strings.Contains(string(rawJSON), `"available_nodes":null`) {
		t.Fatalf("expected non-null available_nodes array in JSON, got %s", string(rawJSON))
	}
}

