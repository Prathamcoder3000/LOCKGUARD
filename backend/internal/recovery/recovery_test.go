package recovery

import (
	"context"
	"testing"
	"time"

	"lockguard/backend/internal/audit"
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

