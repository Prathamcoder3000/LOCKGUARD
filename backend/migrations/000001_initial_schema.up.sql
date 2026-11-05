-- ==============================================================================
-- LockGuard Database Migration: 000001_initial_schema.up.sql
-- Purpose: Schema for user management, audit logging, benchmarking, and dashboard configuration.
-- IMPORTANT: Authoritative distributed lock ownership and consensus logs are NEVER
-- stored in PostgreSQL. Lock state is managed exclusively by the Raft coordinator cluster.
-- ==============================================================================

-- 1. Roles and Permissions
CREATE TABLE IF NOT EXISTS roles (
    id SERIAL PRIMARY KEY,
    name VARCHAR(50) NOT NULL UNIQUE,
    description TEXT,
    permissions JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 2. Users Table
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username VARCHAR(100) NOT NULL UNIQUE,
    email VARCHAR(255) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    role VARCHAR(50) NOT NULL REFERENCES roles(name) ON UPDATE CASCADE,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 3. Audit Events Table
CREATE TABLE IF NOT EXISTS audit_events (
    id BIGSERIAL PRIMARY KEY,
    event_type VARCHAR(100) NOT NULL,
    actor_id UUID REFERENCES users(id) ON DELETE SET NULL,
    actor_identifier VARCHAR(255) NOT NULL,
    resource_name VARCHAR(255) NOT NULL,
    action VARCHAR(50) NOT NULL,
    status VARCHAR(50) NOT NULL,
    details JSONB DEFAULT '{}'::jsonb,
    client_ip VARCHAR(45),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_audit_events_created_at ON audit_events(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_events_resource ON audit_events(resource_name);
CREATE INDEX IF NOT EXISTS idx_audit_events_event_type ON audit_events(event_type);

-- 4. Benchmark Runs
CREATE TABLE IF NOT EXISTS benchmark_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title VARCHAR(255) NOT NULL,
    scenario VARCHAR(100) NOT NULL,
    concurrency_level INT NOT NULL,
    total_operations INT NOT NULL,
    target_cluster VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    started_at TIMESTAMP WITH TIME ZONE,
    completed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 5. Benchmark Results
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
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_benchmark_results_run_id ON benchmark_results(benchmark_run_id);

-- 6. Dashboard Preferences
CREATE TABLE IF NOT EXISTS dashboard_preferences (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    theme VARCHAR(30) NOT NULL DEFAULT 'navy-light',
    refresh_rate_seconds INT NOT NULL DEFAULT 5,
    widget_layout JSONB DEFAULT '[]'::jsonb,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 7. System Configuration
CREATE TABLE IF NOT EXISTS system_config (
    config_key VARCHAR(100) PRIMARY KEY,
    config_value JSONB NOT NULL,
    description TEXT,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Seed Default Roles
INSERT INTO roles (name, description, permissions)
VALUES 
    ('admin', 'Full administrative access to LockGuard cluster and configuration', '["*"]'::jsonb),
    ('operator', 'Cluster operations, fault injection, and benchmark runner', '["cluster:view", "cluster:benchmark", "cluster:inject_fault"]'::jsonb),
    ('developer', 'Client application lock consumer and status viewer', '["locks:acquire", "locks:release", "locks:view"]'::jsonb)
ON CONFLICT (name) DO NOTHING;

