package benchmark

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

type stubLeaderResolver struct {
	url string
}

func (s *stubLeaderResolver) GetLeader(ctx context.Context) (string, error) {
	return s.url, nil
}

func TestRunner_ExecuteAllScenariosAndHandler(t *testing.T) {
	var acqCount, relCount, listCount atomic.Int64
	mockLeader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/locks/acquire":
			tok := acqCount.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"data": map[string]interface{}{
					"fencing_token": tok,
					"lease_version": 1,
				},
			})
		case "/v1/locks/release":
			relCount.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]interface{}{}})
		case "/v1/locks":
			listCount.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]interface{}{"locks": []interface{}{}, "count": 0}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockLeader.Close()

	runner := NewRunner(&stubLeaderResolver{url: mockLeader.URL}, nil, nil, "test-cluster")

	rec, err := runner.Execute(context.Background(), RunRequest{
		Title:       "Unit Acquire Release",
		Scenario:    ScenarioAcquireReleaseCycle,
		Concurrency: 3,
		Operations:  12,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if rec.Status != "completed" || rec.SuccessCount != 12 || rec.ErrorCount != 0 {
		t.Fatalf("unexpected run summary: %+v", rec)
	}
	if rec.ThroughputRPS <= 0 || rec.MinLatencyMs > rec.P50LatencyMs || rec.P50LatencyMs > rec.P95LatencyMs || rec.P95LatencyMs > rec.P99LatencyMs || rec.P99LatencyMs > rec.MaxLatencyMs {
		t.Fatalf("invalid latency ordering or throughput: %+v", rec)
	}
	if acqCount.Load() != 12 || relCount.Load() != 12 {
		t.Fatalf("expected 12 acquires and 12 releases, got acq=%d rel=%d", acqCount.Load(), relCount.Load())
	}

	// Read barrier scenario via HTTP handler
	h := NewHandler(runner)
	mux := http.NewServeMux()
	h.RegisterRoutesWithAuth(mux, nil, nil)

	reqRun := httptest.NewRequest(http.MethodPost, "/api/v1/benchmarks/run", jsonBody(t, map[string]interface{}{
		"scenario":          ScenarioClusterReadBarrier,
		"concurrency_level": 2,
		"total_operations":  6,
	}))
	recRun := httptest.NewRecorder()
	mux.ServeHTTP(recRun, reqRun)
	if recRun.Code != http.StatusOK {
		t.Fatalf("expected 200 from POST /api/v1/benchmarks/run, got %d: %s", recRun.Code, recRun.Body.String())
	}
	if listCount.Load() != 6 {
		t.Fatalf("expected 6 read barrier requests, got %d", listCount.Load())
	}

	reqList := httptest.NewRequest(http.MethodGet, "/api/v1/benchmarks/runs", nil)
	recList := httptest.NewRecorder()
	mux.ServeHTTP(recList, reqList)
	if recList.Code != http.StatusOK {
		t.Fatalf("expected 200 from GET /api/v1/benchmarks/runs, got %d", recList.Code)
	}
	var listPayload struct {
		Success bool `json:"success"`
		Data    struct {
			Runs               []interface{} `json:"runs"`
			ReferenceBaselines []interface{} `json:"reference_baselines"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recList.Body.Bytes(), &listPayload); err != nil || !listPayload.Success {
		t.Fatalf("failed to parse list runs payload: %v", err)
	}
	if len(listPayload.Data.Runs) != 2 || len(listPayload.Data.ReferenceBaselines) != 3 {
		t.Fatalf("expected 2 runs and 3 reference baselines, got %d runs, %d refs", len(listPayload.Data.Runs), len(listPayload.Data.ReferenceBaselines))
	}
}

func jsonBody(t *testing.T, v interface{}) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	return bytes.NewReader(b)
}
