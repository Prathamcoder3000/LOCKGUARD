package benchmark

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"lockguard/backend/internal/audit"
	"lockguard/backend/internal/gateway"
	"lockguard/backend/internal/persistence"
)

var (
	ErrBenchmarkRunning = errors.New("a benchmark run is already in progress")
	ErrNoLeader         = errors.New("raft leader is currently unavailable")
)

const (
	ScenarioAcquireReleaseCycle = "acquire_release_cycle"
	ScenarioContentionAcquire   = "contention_acquire"
	ScenarioClusterReadBarrier  = "cluster_read_barrier"

	maxAllowedConcurrency = 32
	maxAllowedOperations  = 200
)

// RunRequest configures a live bounded benchmark execution against the Raft cluster.
type RunRequest struct {
	Title       string `json:"title"`
	Scenario    string `json:"scenario"`
	Concurrency int    `json:"concurrency_level"`
	Operations  int    `json:"total_operations"`
}

// ReferenceBaseline documents static historical Phase 5 engineering benchmarks separately from live runs.
type ReferenceBaseline struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Scenario    string  `json:"scenario"`
	Category    string  `json:"category"`
	Target      string  `json:"target"`
	Measurement string  `json:"measurement"`
	Baseline    float64 `json:"baseline"`
	Unit        string  `json:"unit"`
	Source      string  `json:"source"`
}

// LeaderResolver abstracts leader lookup for unit testing and gateway integration.
type LeaderResolver interface {
	GetLeader(ctx context.Context) (string, error)
}

// LeaderInvalidator allows the benchmark runner to refresh cached leader routing on HTTP 421.
type LeaderInvalidator interface {
	InvalidateLeader()
	SetLeaderByID(leaderID string) bool
}

// Runner executes live, bounded concurrent Raft lock benchmarks and persists results to PostgreSQL.
type Runner struct {
	mu          sync.Mutex
	running     bool
	discovery   LeaderResolver
	repo        persistence.BenchmarkRepository
	pipeline    *audit.Pipeline
	httpClient  *http.Client
	clusterName string
	memoryRuns  []*persistence.BenchmarkRunRecord
}

// NewRunner constructs a new bounded benchmark Runner.
func NewRunner(discovery LeaderResolver, repo persistence.BenchmarkRepository, pipeline *audit.Pipeline, clusterName string) *Runner {
	if strings.TrimSpace(clusterName) == "" {
		clusterName = "lockguard-raft-cluster"
	}
	return &Runner{
		discovery:   discovery,
		repo:        repo,
		pipeline:    pipeline,
		httpClient:  &http.Client{Timeout: 5 * time.Second},
		clusterName: clusterName,
		memoryRuns:  make([]*persistence.BenchmarkRunRecord, 0, 20),
	}
}

// SetRepository allows late wiring of the BenchmarkRepository once PostgreSQL is initialized.
func (r *Runner) SetRepository(repo persistence.BenchmarkRepository) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.repo = repo
}

// IsRunning returns whether a live benchmark is currently executing.
func (r *Runner) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// ReferenceBaselines returns clearly labeled Phase 5 documented baseline targets (non-live).
func ReferenceBaselines() []ReferenceBaseline {
	return []ReferenceBaseline{
		{
			ID:          "ref-acquire-p99",
			Title:       "Raft Acquire Consensus Target",
			Scenario:    ScenarioAcquireReleaseCycle,
			Category:    "Documented Baseline (Phase 5 Reference)",
			Target:      "< 50 ms p99",
			Measurement: "3-Node BoltDB Quorum Commit",
			Baseline:    18.4,
			Unit:        "ms",
			Source:      "Phase 5 Design Spec",
		},
		{
			ID:          "ref-throughput",
			Title:       "Raft Write Throughput Baseline",
			Scenario:    ScenarioAcquireReleaseCycle,
			Category:    "Documented Baseline (Phase 5 Reference)",
			Target:      ">= 150 ops/sec",
			Measurement: "Sustained Acquire/Release",
			Baseline:    240.0,
			Unit:        "ops/s",
			Source:      "Phase 5 Design Spec",
		},
		{
			ID:          "ref-failover",
			Title:       "Raft Election Failover SLA",
			Scenario:    "leader_failover",
			Category:    "Documented Baseline (Phase 5 Reference)",
			Target:      "< 3000 ms",
			Measurement: "Quorum Loss to New Leader",
			Baseline:    1200.0,
			Unit:        "ms",
			Source:      "Phase 5 Design Spec",
		},
	}
}

// ListRuns returns persisted benchmark runs from PostgreSQL (or recent in-memory fallback runs if PG unavailable).
func (r *Runner) ListRuns(ctx context.Context, limit int) ([]*persistence.BenchmarkRunRecord, string, error) {
	r.mu.Lock()
	repo := r.repo
	mem := make([]*persistence.BenchmarkRunRecord, len(r.memoryRuns))
	copy(mem, r.memoryRuns)
	r.mu.Unlock()

	if repo != nil {
		runs, err := repo.ListRuns(ctx, limit)
		if err == nil {
			return runs, "postgresql", nil
		}
		return mem, "memory_fallback", nil
	}
	return mem, "memory_only", nil
}

// Execute runs a live concurrent benchmark synchronously within strict safety bounds.
func (r *Runner) Execute(ctx context.Context, req RunRequest) (*persistence.BenchmarkRunRecord, error) {
	scenario := normalizeScenario(req.Scenario)
	concurrency := clampInt(req.Concurrency, 4, 1, maxAllowedConcurrency)
	totalOps := clampInt(req.Operations, 40, 1, maxAllowedOperations)
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = fmt.Sprintf("Live %s (%dc x %d ops)", scenario, concurrency, totalOps)
	}

	if r.discovery == nil {
		return nil, ErrNoLeader
	}
	leaderURL, err := r.discovery.GetLeader(ctx)
	if err != nil || leaderURL == "" {
		return nil, ErrNoLeader
	}

	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return nil, ErrBenchmarkRunning
	}
	r.running = true
	repo := r.repo
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
	}()

	startedAt := time.Now().UTC()
	rec := &persistence.BenchmarkRunRecord{
		ID:               fmt.Sprintf("bench-%d", startedAt.UnixNano()),
		Title:            title,
		Scenario:         scenario,
		ConcurrencyLevel: concurrency,
		TotalOperations:  totalOps,
		TargetCluster:    fmt.Sprintf("%s (%s)", r.clusterName, leaderURL),
		Status:           "running",
		StartedAt:        &startedAt,
		CreatedAt:        startedAt,
	}

	var persistedID string
	if repo != nil {
		if id, createErr := repo.CreateRun(ctx, rec); createErr == nil && id != "" {
			persistedID = id
		}
	}

	latenciesMs := make([]float64, totalOps)
	var latMu sync.Mutex
	latIdx := 0

	var nextOp atomic.Int64
	var successCount atomic.Int64
	var errorCount atomic.Int64

	batchPrefix := fmt.Sprintf("bench-%d", startedAt.UnixNano())

	var wg sync.WaitGroup
	benchStart := time.Now()
	for w := 0; w < concurrency; w++ {
		workerID := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if ctx.Err() != nil {
					return
				}
				opNum := int(nextOp.Add(1)) - 1
				if opNum >= totalOps {
					return
				}

				opStart := time.Now()
				opErr := r.runSingleOp(ctx, scenario, batchPrefix, workerID, opNum)
				elapsedMs := float64(time.Since(opStart).Microseconds()) / 1000.0

				if opErr != nil {
					errorCount.Add(1)
				} else {
					successCount.Add(1)
				}

				latMu.Lock()
				if latIdx < len(latenciesMs) {
					latenciesMs[latIdx] = elapsedMs
					latIdx++
				}
				latMu.Unlock()
			}
		}()
	}
	wg.Wait()

	elapsedSec := time.Since(benchStart).Seconds()
	if elapsedSec <= 0 {
		elapsedSec = 0.001
	}

	completedAt := time.Now().UTC()
	executedOps := latIdx
	sample := latenciesMs[:executedOps]
	p50, p95, p99, minL, maxL := computeLatencyStats(sample)
	succ := successCount.Load()
	errs := errorCount.Load()
	rps := round2(float64(executedOps) / elapsedSec)

	summaryMap := map[string]interface{}{
		"duration_ms":       round2(elapsedSec * 1000),
		"leader_url":        leaderURL,
		"executed_ops":      executedOps,
		"scenario":          scenario,
		"persisted_to_db":   persistedID != "",
	}
	summaryBytes, _ := json.Marshal(summaryMap)

	rec.Status = "completed"
	if ctx.Err() != nil {
		rec.Status = "canceled"
	}
	rec.CompletedAt = &completedAt
	rec.ThroughputRPS = rps
	rec.P50LatencyMs = p50
	rec.P95LatencyMs = p95
	rec.P99LatencyMs = p99
	rec.MinLatencyMs = minL
	rec.MaxLatencyMs = maxL
	rec.SuccessCount = succ
	rec.ErrorCount = errs
	rec.MetricsSummary = summaryBytes

	if repo != nil && persistedID != "" {
		_ = repo.SaveResult(context.Background(), persistedID, rec.Status, completedAt, rec)
	}

	r.mu.Lock()
	r.memoryRuns = append([]*persistence.BenchmarkRunRecord{rec}, r.memoryRuns...)
	if len(r.memoryRuns) > 20 {
		r.memoryRuns = r.memoryRuns[:20]
	}
	r.mu.Unlock()

	if r.pipeline != nil {
		r.pipeline.RecordEvent(&audit.Event{
			EventType:       "BENCHMARK_EXECUTED",
			EventCategory:   audit.CategorySystem,
			OperationStatus: audit.StatusCommitted,
			CoordinatorID:   leaderURL,
			EventTimestamp:  completedAt,
			Metadata: map[string]interface{}{
				"benchmark_id":      rec.ID,
				"scenario":          scenario,
				"concurrency_level": concurrency,
				"total_operations":  executedOps,
				"throughput_rps":    rps,
				"p50_latency_ms":    p50,
				"p95_latency_ms":    p95,
				"p99_latency_ms":    p99,
				"success_count":     succ,
				"error_count":       errs,
			},
		})
	}

	return rec, nil
}

func (r *Runner) doLeaderRequest(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	const maxAttempts = 8
	for attempt := 0; attempt < maxAttempts; attempt++ {
		leaderURL, err := r.discovery.GetLeader(ctx)
		if err != nil || leaderURL == "" {
			if attempt+1 == maxAttempts {
				if err != nil {
					return 0, nil, err
				}
				return 0, nil, ErrNoLeader
			}
			select {
			case <-ctx.Done():
				return 0, nil, ctx.Err()
			case <-time.After(75 * time.Millisecond):
			}
			continue
		}

		var bodyReader io.Reader
		if body != nil {
			bodyReader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, leaderURL+path, bodyReader)
		if err != nil {
			return 0, nil, err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := r.httpClient.Do(req)
		if err != nil {
			if inv, ok := r.discovery.(LeaderInvalidator); ok {
				inv.InvalidateLeader()
			}
			if attempt+1 < maxAttempts {
				select {
				case <-ctx.Done():
					return 0, nil, ctx.Err()
				case <-time.After(75 * time.Millisecond):
				}
				continue
			}
			return 0, nil, err
		}

		raw, readErr := io.ReadAll(resp.Body)
		leaderID := resp.Header.Get("X-Raft-Leader-ID")
		_ = resp.Body.Close()

		if resp.StatusCode == http.StatusMisdirectedRequest || resp.StatusCode == http.StatusServiceUnavailable {
			if inv, ok := r.discovery.(LeaderInvalidator); ok {
				if leaderID != "" {
					inv.SetLeaderByID(leaderID)
				} else {
					inv.InvalidateLeader()
				}
			}
			if attempt+1 < maxAttempts {
				select {
				case <-ctx.Done():
					return resp.StatusCode, raw, ctx.Err()
				case <-time.After(75 * time.Millisecond):
				}
				continue
			}
		}

		if readErr != nil {
			return resp.StatusCode, nil, readErr
		}
		return resp.StatusCode, raw, nil
	}
	return 0, nil, ErrNoLeader
}

func (r *Runner) runSingleOp(ctx context.Context, scenario, prefix string, workerID, opNum int) error {
	switch scenario {
	case ScenarioClusterReadBarrier:
		statusCode, _, err := r.doLeaderRequest(ctx, http.MethodGet, "/v1/locks", nil)
		if err != nil {
			return err
		}
		if statusCode >= 400 {
			return fmt.Errorf("read barrier returned HTTP %d", statusCode)
		}
		return nil

	case ScenarioContentionAcquire:
		// 4 shared contended buckets across all concurrent workers; winners cleanly release.
		resName := fmt.Sprintf("%s-contend-%d", prefix, opNum%4)
		ownerID := fmt.Sprintf("bench-worker-%d", workerID)
		token, acquired, err := r.acquireTempLock(ctx, resName, ownerID, fmt.Sprintf("%s-c-%d-%d", prefix, workerID, opNum))
		if acquired {
			_ = r.releaseTempLock(ctx, resName, ownerID, token)
		}
		// 409 Conflict is expected lock contention behavior rather than a transport failure
		if err != nil && !errors.Is(err, errLockContended) {
			return err
		}
		return nil

	default: // ScenarioAcquireReleaseCycle
		resName := fmt.Sprintf("%s-res-%d-%d", prefix, workerID, opNum)
		ownerID := fmt.Sprintf("bench-worker-%d", workerID)
		token, acquired, err := r.acquireTempLock(ctx, resName, ownerID, fmt.Sprintf("%s-ar-%d-%d", prefix, workerID, opNum))
		if err != nil {
			return err
		}
		if acquired {
			if relErr := r.releaseTempLock(ctx, resName, ownerID, token); relErr != nil {
				return relErr
			}
		}
		return nil
	}
}

var errLockContended = errors.New("lock already held during contention")

func (r *Runner) acquireTempLock(ctx context.Context, resource, owner, reqID string) (uint64, bool, error) {
	payload, _ := json.Marshal(map[string]interface{}{
		"request_id":    reqID,
		"resource_name": resource,
		"owner_id":      owner,
		"ttl_seconds":   10,
	})
	statusCode, raw, err := r.doLeaderRequest(ctx, http.MethodPost, "/v1/locks/acquire", payload)
	if err != nil {
		return 0, false, err
	}

	if statusCode == http.StatusConflict {
		return 0, false, errLockContended
	}
	if statusCode >= 400 {
		return 0, false, fmt.Errorf("acquire returned HTTP %d", statusCode)
	}

	var parsed struct {
		Success bool `json:"success"`
		Data    struct {
			FencingToken uint64 `json:"fencing_token"`
			Record       *struct {
				FencingToken uint64 `json:"fencing_token"`
			} `json:"record"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || !parsed.Success {
		return 0, false, fmt.Errorf("invalid acquire response")
	}
	tok := parsed.Data.FencingToken
	if parsed.Data.Record != nil && parsed.Data.Record.FencingToken > 0 {
		tok = parsed.Data.Record.FencingToken
	}
	return tok, true, nil
}

func (r *Runner) releaseTempLock(ctx context.Context, resource, owner string, token uint64) error {
	payload, _ := json.Marshal(map[string]interface{}{
		"request_id":    fmt.Sprintf("rel-%s-%d", resource, time.Now().UnixNano()),
		"resource_name": resource,
		"owner_id":      owner,
		"fencing_token": token,
	})
	statusCode, _, err := r.doLeaderRequest(ctx, http.MethodPost, "/v1/locks/release", payload)
	if err != nil {
		return err
	}
	if statusCode >= 400 {
		return fmt.Errorf("release returned HTTP %d", statusCode)
	}
	return nil
}

func normalizeScenario(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case ScenarioContentionAcquire, "contention":
		return ScenarioContentionAcquire
	case ScenarioClusterReadBarrier, "read_barrier", "read":
		return ScenarioClusterReadBarrier
	default:
		return ScenarioAcquireReleaseCycle
	}
}

func clampInt(val, defVal, minVal, maxVal int) int {
	if val <= 0 {
		return defVal
	}
	if val < minVal {
		return minVal
	}
	if val > maxVal {
		return maxVal
	}
	return val
}

func computeLatencyStats(samples []float64) (p50, p95, p99, minV, maxV float64) {
	if len(samples) == 0 {
		return 0, 0, 0, 0, 0
	}
	sorted := make([]float64, len(samples))
	copy(sorted, samples)
	sort.Float64s(sorted)

	minV = round3(sorted[0])
	maxV = round3(sorted[len(sorted)-1])
	p50 = round3(percentile(sorted, 0.50))
	p95 = round3(percentile(sorted, 0.95))
	p99 = round3(percentile(sorted, 0.99))
	return
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func round3(v float64) float64 {
	return math.Round(v*1000) / 1000
}

// Compile-time guarantee *gateway.LeaderDiscovery satisfies LeaderResolver.
var _ LeaderResolver = (*gateway.LeaderDiscovery)(nil)
