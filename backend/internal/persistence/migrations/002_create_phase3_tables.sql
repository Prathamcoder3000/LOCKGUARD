-- Migration 002: Create Phase 3 Audit, Operational, and Checkpoint Tables

-- 1. Create audit_events table
CREATE TABLE IF NOT EXISTS audit_events (
    id BIGSERIAL PRIMARY KEY,
    event_id VARCHAR(64) UNIQUE,
    event_type VARCHAR(64) NOT NULL,
    event_category VARCHAR(64) NOT NULL DEFAULT 'LOCK_OPERATION',
    request_id VARCHAR(128),
    resource_id VARCHAR(255),
    owner_id VARCHAR(255),
    client_id VARCHAR(255),
    fencing_token BIGINT NOT NULL DEFAULT 0,
    lease_version BIGINT NOT NULL DEFAULT 0,
    operation_status VARCHAR(32) NOT NULL DEFAULT 'PENDING',
    error_code VARCHAR(64),
    coordinator_id VARCHAR(128),
    raft_index BIGINT NOT NULL DEFAULT 0,
    event_timestamp TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb
);

-- Ensure all required columns exist if audit_events was previously created with legacy schema
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS event_id VARCHAR(64);
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS event_category VARCHAR(64) NOT NULL DEFAULT 'LOCK_OPERATION';
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS request_id VARCHAR(128);
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS resource_id VARCHAR(255);
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS owner_id VARCHAR(255);
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS client_id VARCHAR(255);
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS fencing_token BIGINT NOT NULL DEFAULT 0;
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS lease_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS operation_status VARCHAR(32) NOT NULL DEFAULT 'PENDING';
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS error_code VARCHAR(64);
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS coordinator_id VARCHAR(128);
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS raft_index BIGINT NOT NULL DEFAULT 0;
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS event_timestamp TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP;
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS recorded_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP;
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS metadata JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE UNIQUE INDEX IF NOT EXISTS idx_audit_events_event_id_unique ON audit_events(event_id) WHERE event_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_audit_events_type ON audit_events(event_type);
CREATE INDEX IF NOT EXISTS idx_audit_events_category ON audit_events(event_category);
CREATE INDEX IF NOT EXISTS idx_audit_events_resource ON audit_events(resource_id);
CREATE INDEX IF NOT EXISTS idx_audit_events_owner ON audit_events(owner_id);
CREATE INDEX IF NOT EXISTS idx_audit_events_client ON audit_events(client_id);
CREATE INDEX IF NOT EXISTS idx_audit_events_req_id ON audit_events(request_id);
CREATE INDEX IF NOT EXISTS idx_audit_events_status ON audit_events(operation_status);
CREATE INDEX IF NOT EXISTS idx_audit_events_timestamp ON audit_events(event_timestamp DESC);

-- 2. Create operational_events table
CREATE TABLE IF NOT EXISTS operational_events (
    id BIGSERIAL PRIMARY KEY,
    event_id VARCHAR(64) NOT NULL UNIQUE,
    event_type VARCHAR(64) NOT NULL,
    component VARCHAR(64) NOT NULL,
    node_id VARCHAR(128) NOT NULL,
    status VARCHAR(32) NOT NULL,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_operational_events_type ON operational_events(event_type);
CREATE INDEX IF NOT EXISTS idx_operational_events_component ON operational_events(component);
CREATE INDEX IF NOT EXISTS idx_operational_events_node ON operational_events(node_id);
CREATE INDEX IF NOT EXISTS idx_operational_events_created_at ON operational_events(created_at DESC);

-- 3. Create system_checkpoints table
CREATE TABLE IF NOT EXISTS system_checkpoints (
    id BIGSERIAL PRIMARY KEY,
    checkpoint_type VARCHAR(64) NOT NULL,
    node_id VARCHAR(128) NOT NULL,
    raft_index BIGINT NOT NULL DEFAULT 0,
    raft_term BIGINT NOT NULL DEFAULT 0,
    summary JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_system_checkpoints_type ON system_checkpoints(checkpoint_type, created_at DESC);

