package chaos

import (
	"context"
	"strings"
	"testing"

	"lockguard/backend/internal/metrics"
)

func TestChaosSafetyGates(t *testing.T) {
	// 1. Production environment must strictly disable chaos even if Enabled=true
	prodCtrl := NewController(Config{
		Enabled:     true,
		Environment: "production",
		AdminToken:  "super-secret",
	})

	if prodCtrl.IsEnabled() {
		t.Error("expected chaos controller to be disabled in production")
	}

	err := prodCtrl.SetEnabled(true)
	if err == nil || !strings.Contains(err.Error(), "safety violation") {
		t.Errorf("expected safety violation when enabling in production, got: %v", err)
	}

	// 2. Attempting to inject faults on disabled controller returns ErrChaosDisabled
	_, _, errInject := prodCtrl.Inject(context.Background(), FaultProcessCrash, "coord-a", nil, "test", nil, nil)
	if errInject != ErrChaosDisabled {
		t.Errorf("expected ErrChaosDisabled, got: %v", errInject)
	}
}

func TestChaosAuthorization(t *testing.T) {
	ctrl := NewController(Config{
		Enabled:     true,
		Environment: "test",
		AdminToken:  "chaos-token-123",
	})

	// Valid tokens
	if err := ctrl.Authorize("chaos-token-123"); err != nil {
		t.Errorf("expected chaos-token-123 to be authorized: %v", err)
	}
	if err := ctrl.Authorize("admin"); err != nil {
		t.Errorf("expected 'admin' to be authorized: %v", err)
	}

	// Invalid token
	if err := ctrl.Authorize("invalid-token"); err != ErrUnauthorized {
		t.Errorf("expected ErrUnauthorized, got: %v", err)
	}
}

func TestChaosFaultLifecycleAndReporting(t *testing.T) {
	reg := metrics.NewRegistry()
	ctrl := NewController(Config{
		Enabled:     true,
		Environment: "test",
		AdminToken:  "test-token",
		Metrics:     reg,
	})

	var eventLog []FaultEvent
	ctrl.RegisterListener(func(ev FaultEvent) {
		eventLog = append(eventLog, ev)
	})

	applied := false
	reverted := false

	applyFn := func(ctx context.Context) error {
		applied = true
		return nil
	}
	revertFn := func(ctx context.Context) error {
		reverted = true
		return nil
	}

	faultID, revClosure, err := ctrl.Inject(
		context.Background(),
		FaultNetworkPartition,
		"coordinator-a",
		map[string]interface{}{"peers": 2},
		"Simulate partition of coordinator-a",
		applyFn,
		revertFn,
	)

	if err != nil {
		t.Fatalf("failed to inject fault: %v", err)
	}
	if !applied {
		t.Error("expected applyFn to be executed")
	}
	if faultID == "" {
		t.Error("expected non-empty faultID")
	}

	report := ctrl.GetReport()
	if report.ActiveCount != 1 {
		t.Errorf("expected 1 active fault, got %d", report.ActiveCount)
	}

	// Revert the fault
	if err := revClosure(context.Background()); err != nil {
		t.Fatalf("failed to revert fault: %v", err)
	}
	if !reverted {
		t.Error("expected revertFn to be executed")
	}

	postReport := ctrl.GetReport()
	if postReport.ActiveCount != 0 {
		t.Errorf("expected 0 active faults after revert, got %d", postReport.ActiveCount)
	}
	if postReport.HistoryTotal != 1 {
		t.Errorf("expected 1 history record, got %d", postReport.HistoryTotal)
	}

	// Check event audit log
	if len(eventLog) != 2 {
		t.Fatalf("expected 2 audit events (INJECTED, REVERTED), got %d", len(eventLog))
	}
	if eventLog[0].Action != "INJECTED" || eventLog[1].Action != "REVERTED" {
		t.Errorf("unexpected event actions: %v, %v", eventLog[0].Action, eventLog[1].Action)
	}
}

func TestChaosRevertAll(t *testing.T) {
	ctrl := NewController(Config{
		Enabled:     true,
		Environment: "test",
	})

	f1Rev, f2Rev := false, false

	_, _, _ = ctrl.Inject(context.Background(), FaultProcessCrash, "node-1", nil, "c1", nil, func(ctx context.Context) error {
		f1Rev = true
		return nil
	})
	_, _, _ = ctrl.Inject(context.Background(), FaultNetworkDelay, "node-2", nil, "c2", nil, func(ctx context.Context) error {
		f2Rev = true
		return nil
	})

	rep := ctrl.GetReport()
	if rep.ActiveCount != 2 {
		t.Fatalf("expected 2 active faults, got %d", rep.ActiveCount)
	}

	errs := ctrl.RevertAll(context.Background())
	if len(errs) > 0 {
		t.Fatalf("revert all returned errors: %v", errs)
	}

	if !f1Rev || !f2Rev {
		t.Errorf("expected both faults to be reverted: f1=%v, f2=%v", f1Rev, f2Rev)
	}

	repAfter := ctrl.GetReport()
	if repAfter.ActiveCount != 0 {
		t.Errorf("expected 0 active faults, got %d", repAfter.ActiveCount)
	}
}
