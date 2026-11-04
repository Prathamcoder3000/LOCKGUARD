-- LockGuard Database Migration: 000001_initial_schema.down.sql
DROP TABLE IF EXISTS system_config;
DROP TABLE IF EXISTS dashboard_preferences;
DROP TABLE IF EXISTS benchmark_results;
DROP TABLE IF EXISTS benchmark_runs;
DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS roles;
