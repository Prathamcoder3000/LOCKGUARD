-- Ensure benchmark tables exist for live benchmark persistence on fresh databases.
CREATE TABLE IF NOT EXISTS benchmark_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title VARCHAR(255) NOT NULL,
    scenario VARCHAR(100) NOT NULL,
    concurrency_level INT NOT NULL,
    total_operations INT NOT NULL,
    target_cluster VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS benchmark_results (
    id BIGSERIAL PRIMARY KEY,
    benchmark_run_id UUID NOT NULL REFERENCES benchmark_runs(id) ON DELETE CASCADE,
    throughput_rps NUMERIC(12, 2) NOT NULL,
    p50_latency_ms NUMERIC(10, 3) NOT NULL,
    p95_latency_ms NUMERIC(10, 3) NOT NULL,
    p99_latency_ms NUMERIC(10, 3) NOT NULL,
    min_latency_ms NUMERIC(10, 3) NOT NULL,
    max_latency_ms NUMERIC(10, 3) NOT NULL,
    success_count BIGINT NOT NULL,
    error_count BIGINT NOT NULL,
    metrics_summary JSONB DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_benchmark_results_run_id ON benchmark_results (benchmark_run_id);
