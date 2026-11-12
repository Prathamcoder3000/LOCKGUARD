import React, { useEffect, useState } from 'react';
import { 
  ShieldCheck, 
  Server, 
  Activity, 
  RefreshCw, 
  AlertTriangle, 
  CheckCircle, 
  Database, 
  Layers,
  FileCheck
} from 'lucide-react';
import { clusterService, HealthStatus, ReadinessStatus, ClusterStatus } from '../services/clusterService';
import { resilienceService, ClusterRecoveryReport } from '../services/resilienceService';

export const OverviewPage: React.FC = () => {
  const [health, setHealth] = useState<HealthStatus | null>(null);
  const [readiness, setReadiness] = useState<ReadinessStatus | null>(null);
  const [clusterInfo, setClusterInfo] = useState<ClusterStatus | null>(null);
  const [clusterRecovery, setClusterRecovery] = useState<ClusterRecoveryReport | null>(null);
  const [loading, setLoading] = useState<boolean>(true);
  const [error, setError] = useState<string | null>(null);
  const [lastUpdated, setLastUpdated] = useState<string>('');

  const fetchStatus = async () => {
    setLoading(true);
    setError(null);
    try {
      const [h, r, c, rec] = await Promise.allSettled([
        clusterService.getHealth(),
        clusterService.getReadiness(),
        clusterService.getClusterStatus(),
        resilienceService.getClusterRecovery(),
      ]);

      if (h.status === 'fulfilled') setHealth(h.value);
      if (r.status === 'fulfilled') setReadiness(r.value);
      if (c.status === 'fulfilled') setClusterInfo(c.value);
      if (rec.status === 'fulfilled') setClusterRecovery(rec.value);

      if (h.status === 'rejected' && r.status === 'rejected') {
        setError('Backend API is currently unreachable. Check container health or network configuration.');
      }
      setLastUpdated(new Date().toLocaleTimeString());
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to fetch cluster status');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchStatus();
    const timer = setInterval(fetchStatus, 10000);
    return () => clearInterval(timer);
  }, []);

  return (
    <div className="space-y-6">
      {/* Top Banner */}
      <div className="flex flex-col justify-between gap-4 sm:flex-row sm:items-center">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-slate-900">
            Cluster Overview &amp; Coordination State
          </h1>
          <p className="text-sm text-slate-500">
            LockGuard Fault-Tolerant Distributed Coordination Service • Active Consensus
          </p>
        </div>
        <div className="flex items-center space-x-3">
          {lastUpdated && (
            <span className="text-xs text-slate-400">
              Refreshed: <span className="font-mono text-slate-600">{lastUpdated}</span>
            </span>
          )}
          <button
            onClick={fetchStatus}
            disabled={loading}
            className="inline-flex items-center space-x-2 rounded-lg border border-slate-200 bg-white px-3.5 py-2 text-xs font-medium text-slate-700 shadow-subtle hover:bg-slate-50 disabled:opacity-50"
          >
            <RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin text-blue-600' : 'text-slate-500'}`} />
            <span>Refresh Probes</span>
          </button>
        </div>
      </div>

      {/* Backend Offline Warning Banner if applicable */}
      {error && (
        <div className="rounded-xl border border-amber-200 bg-amber-50 p-4 text-amber-800 shadow-subtle">
          <div className="flex items-start space-x-3">
            <AlertTriangle className="h-5 w-5 shrink-0 text-amber-600 mt-0.5" />
            <div className="text-sm">
              <p className="font-semibold">Backend Service Standby</p>
              <p className="mt-0.5 text-amber-700">{error}</p>
              <div className="mt-2 rounded bg-amber-100/70 px-3 py-1.5 font-mono text-xs text-amber-900">
                Service: lockguard-api (Docker Gateway on port 8080 / Nginx 80)
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Primary Status Cards Grid */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {/* Consensus Topology */}
        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-card">
          <div className="flex items-center justify-between">
            <span className="text-xs font-semibold uppercase tracking-wider text-slate-500">Cluster Quorum</span>
            <div className="rounded-lg bg-blue-50 p-2 text-blue-600">
              <Server className="h-4 w-4" />
            </div>
          </div>
          <p className="mt-2 text-2xl font-bold text-slate-900">
            {clusterRecovery
              ? `${clusterRecovery.available_nodes?.length ?? 0} of ${clusterRecovery.total_nodes || 3}`
              : '2 of 3'}
          </p>
          <div className="mt-1 flex items-center text-xs font-medium text-slate-500">
            <span>
              {clusterRecovery?.current_leader_id
                ? `Active Leader: ${clusterRecovery.current_leader_id}`
                : clusterRecovery && !clusterRecovery.quorum_available
                ? 'Quorum Lost (Fail-Closed)'
                : 'Majority required to commit'}
            </span>
          </div>
        </div>

        {/* Core Design Principle */}
        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-card">
          <div className="flex items-center justify-between">
            <span className="text-xs font-semibold uppercase tracking-wider text-slate-500">Design Model</span>
            <div className="rounded-lg bg-violet-50 p-2 text-violet-600">
              <ShieldCheck className="h-4 w-4" />
            </div>
          </div>
          <p className="mt-2 text-lg font-bold text-violet-950 truncate">Fail Closed</p>
          <div className="mt-1 flex items-center text-xs font-medium text-slate-500">
            <span>Consistency prioritized over availability</span>
          </div>
        </div>

        {/* API Liveness */}
        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-card">
          <div className="flex items-center justify-between">
            <span className="text-xs font-semibold uppercase tracking-wider text-slate-500">API Gateway</span>
            <div className="rounded-lg bg-emerald-50 p-2 text-emerald-600">
              <Activity className="h-4 w-4" />
            </div>
          </div>
          <p className="mt-2 text-2xl font-bold text-slate-900">
            {health?.status === 'ok' ? 'Healthy' : error ? 'Offline' : 'Standby'}
          </p>
          <div className="mt-1 flex items-center text-xs font-medium text-emerald-600">
            <CheckCircle className="mr-1 h-3.5 w-3.5" />
            <span>Probe: GET /health</span>
          </div>
        </div>

        {/* PostgreSQL Metadata */}
        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-card">
          <div className="flex items-center justify-between">
            <span className="text-xs font-semibold uppercase tracking-wider text-slate-500">Storage Division</span>
            <div className="rounded-lg bg-slate-100 p-2 text-slate-700">
              <Database className="h-4 w-4" />
            </div>
          </div>
          <p className="mt-2 text-lg font-bold text-slate-900">Dual-Tier</p>
          <div className="mt-1 flex items-center text-xs font-medium text-slate-500">
            <span>Raft log (locks) + SQL (audit)</span>
          </div>
        </div>
      </div>

      {/* Cluster Nodes & Consensus Details */}
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
        {/* Three Coordinator Nodes Card */}
        <div className="rounded-xl border border-slate-200 bg-white p-6 shadow-card lg:col-span-2">
          <div className="flex items-center justify-between border-b border-slate-100 pb-4">
            <div>
              <h2 className="text-base font-semibold text-slate-900">Coordinator Cluster Topology</h2>
              <p className="text-xs text-slate-500">
                Live Dynamic Raft Leadership &bull;{' '}
                {clusterRecovery?.current_leader_id
                  ? `Authoritative Leader: ${clusterRecovery.current_leader_id}`
                  : 'Probing active cluster leader...'}
              </p>
            </div>
            <span
              className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${
                clusterRecovery?.quorum_available
                  ? 'bg-emerald-50 text-emerald-700'
                  : 'bg-amber-50 text-amber-700'
              }`}
            >
              {clusterRecovery?.quorum_available
                ? `Quorum Active (${clusterRecovery.available_nodes?.length ?? 0}/${clusterRecovery.total_nodes || 3})`
                : clusterRecovery
                ? 'Quorum Degraded'
                : 'Raft v1.7 Consensus'}
            </span>
          </div>

          <div className="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-3">
            {(clusterRecovery?.coordinators && clusterRecovery.coordinators.length > 0
              ? clusterRecovery.coordinators
              : [
                  { node_id: 'coordinator-a', endpoint: 'http://coordinator-a:8001', reachable: false, is_leader: false, role: 'Probing...' },
                  { node_id: 'coordinator-b', endpoint: 'http://coordinator-b:8001', reachable: false, is_leader: false, role: 'Probing...' },
                  { node_id: 'coordinator-c', endpoint: 'http://coordinator-c:8001', reachable: false, is_leader: false, role: 'Probing...' },
                ]
            ).map((node) => {
              const roleLabel = node.partitioned
                ? 'Partitioned (Isolated)'
                : node.is_leader
                ? 'Leader (Authoritative)'
                : node.reachable
                ? node.role || 'Follower'
                : 'Unreachable';
              return (
                <div
                  key={node.node_id}
                  className={`rounded-lg border p-4 transition-colors ${
                    node.is_leader && !node.partitioned
                      ? 'border-blue-300 bg-blue-50/40'
                      : node.partitioned
                      ? 'border-amber-300 bg-amber-50/40'
                      : 'border-slate-200 bg-slate-50/70'
                  }`}
                >
                  <div className="flex items-center justify-between">
                    <span className="font-mono text-xs font-bold text-slate-800">{node.node_id}</span>
                    <span
                      className={`inline-flex h-2 w-2 rounded-full ${
                        node.partitioned
                          ? 'bg-amber-500'
                          : node.is_leader
                          ? 'bg-blue-600'
                          : node.reachable
                          ? 'bg-emerald-500'
                          : 'bg-rose-500'
                      }`}
                    />
                  </div>
                  <div className="mt-2 space-y-1 text-xs text-slate-600">
                    <div>
                      Raft Port: <span className="font-mono text-slate-900">{node.raft_bind_addr || ':7000'}</span>
                    </div>
                    <div>
                      API Endpoint: <span className="font-mono text-slate-900 truncate block">{node.endpoint}</span>
                    </div>
                    <div className="pt-1 flex items-center justify-between text-[11px]">
                      <span
                        className={`font-semibold ${
                          node.is_leader && !node.partitioned
                            ? 'text-blue-700'
                            : node.partitioned
                            ? 'text-amber-700'
                            : node.reachable
                            ? 'text-emerald-700'
                            : 'text-rose-600'
                        }`}
                      >
                        {roleLabel}
                      </span>
                      {typeof node.applied_index === 'number' && node.reachable && (
                        <span className="font-mono text-slate-500">idx #{node.applied_index}</span>
                      )}
                    </div>
                  </div>
                </div>
              );
            })}
          </div>

          {/* Architectural Invariants Table */}
          <div className="mt-6 border-t border-slate-100 pt-4">
            <h3 className="text-xs font-semibold uppercase tracking-wider text-slate-500 mb-3">
              Cluster Safety &amp; Quorum Invariants
            </h3>
            <div className="space-y-2 text-xs">
              <div className="flex items-start space-x-2 rounded-md bg-slate-50 p-2.5 border border-slate-100">
                <CheckCircle className="h-4 w-4 text-blue-600 shrink-0 mt-0.5" />
                <div>
                  <span className="font-semibold text-slate-800">Single Leader per Term:</span>
                  <span className="text-slate-600 ml-1">
                    At most one leader can be elected and authoritative per Raft term.
                  </span>
                </div>
              </div>
              <div className="flex items-start space-x-2 rounded-md bg-slate-50 p-2.5 border border-slate-100">
                <CheckCircle className="h-4 w-4 text-blue-600 shrink-0 mt-0.5" />
                <div>
                  <span className="font-semibold text-slate-800">Quorum Requirement:</span>
                  <span className="text-slate-600 ml-1">
                    Every mutation (lock acquire, renew, release) requires acknowledgement from at least 2 nodes.
                  </span>
                </div>
              </div>
              <div className="flex items-start space-x-2 rounded-md bg-slate-50 p-2.5 border border-slate-100">
                <CheckCircle className="h-4 w-4 text-violet-600 shrink-0 mt-0.5" />
                <div>
                  <span className="font-semibold text-slate-800">Minority Partition Fail-Closed:</span>
                  <span className="text-slate-600 ml-1">
                    An isolated minority node strictly rejects writes and drops leadership immediately.
                  </span>
                </div>
              </div>
            </div>
          </div>
        </div>

        {/* Subsystems & Implementation Roadmap */}
        <div className="rounded-xl border border-slate-200 bg-white p-6 shadow-card">
          <div className="flex items-center justify-between border-b border-slate-100 pb-4">
            <div className="flex items-center space-x-2">
              <Layers className="h-4 w-4 text-blue-600" />
              <h2 className="text-base font-semibold text-slate-900">Subsystem Architecture Milestones</h2>
            </div>
            <span className="text-[10px] font-medium text-slate-400 uppercase tracking-wider">Phase 0–6 Status</span>
          </div>

          <div className="mt-4 space-y-3">
            {[
              { name: 'Monorepo & Base Probes', phase: 'Phase 0', status: 'Verified', color: 'emerald' },
              { name: 'Deterministic Lock State Machine', phase: 'Phase 1A', status: 'Verified', color: 'emerald' },
              { name: '3-Node HashiCorp Raft Consensus', phase: 'Phase 1B', status: 'Verified', color: 'emerald' },
              { name: 'Gateway, Sessions & Auto-Renewal', phase: 'Phase 2', status: 'Verified', color: 'emerald' },
              { name: 'PostgreSQL Audit & Snapshots', phase: 'Phase 3', status: 'Verified', color: 'emerald' },
              { name: 'Protected Resource & Chaos Recovery', phase: 'Phase 4', status: 'Verified', color: 'emerald' },
              { name: 'Security Hardening & RBAC (HS256)', phase: 'Phase 5', status: 'Verified', color: 'emerald' },
              { name: 'End-to-End Validation & Handover', phase: 'Phase 6', status: 'Verified', color: 'emerald' },
            ].map((subsystem) => (
              <div key={subsystem.name} className="flex items-center justify-between text-xs">
                <div>
                  <p className="font-medium text-slate-800">{subsystem.name}</p>
                  <span className="text-[10px] text-slate-400">{subsystem.phase}</span>
                </div>
                <span className={`rounded-full px-2 py-0.5 text-[10px] font-semibold ${
                  subsystem.color === 'emerald'
                    ? 'bg-emerald-50 text-emerald-700 border border-emerald-200'
                    : 'bg-slate-100 text-slate-600'
                }`}>
                  {subsystem.status}
                </span>
              </div>
            ))}
          </div>

          <div className="mt-6 rounded-lg bg-blue-50/60 border border-blue-100 p-3 text-xs text-blue-800">
            <p className="font-semibold">Authoritative Storage Note</p>
            <p className="mt-1 text-[11px] text-blue-700">
              Distributed lock ownership is committed strictly to Raft consensus logs. PostgreSQL stores only metadata, users, and audit logs.
            </p>
          </div>
        </div>
      </div>

      {/* Live Probe Response Inspector */}
      <div className="rounded-xl border border-slate-200 bg-white p-6 shadow-card">
        <div className="flex items-center justify-between border-b border-slate-100 pb-3">
          <div className="flex items-center space-x-2">
            <FileCheck className="h-4 w-4 text-slate-600" />
            <h2 className="text-sm font-semibold text-slate-900">Live Backend Response Payload</h2>
          </div>
          <span className="font-mono text-xs text-slate-400">GET /api/v1/cluster/status</span>
        </div>
        <div className="mt-3">
          {clusterInfo ? (
            <pre className="rounded-lg bg-slate-900 p-4 font-mono text-xs text-blue-300 overflow-x-auto">
              {JSON.stringify(clusterInfo, null, 2)}
            </pre>
          ) : readiness ? (
            <pre className="rounded-lg bg-slate-900 p-4 font-mono text-xs text-emerald-400 overflow-x-auto">
              {JSON.stringify(readiness, null, 2)}
            </pre>
          ) : (
            <div className="rounded-lg bg-slate-50 border border-dashed border-slate-200 p-6 text-center text-xs text-slate-500">
              When the Go API backend is running, live JSON probe telemetry from the cluster will render here.
            </div>
          )}
        </div>
      </div>
    </div>
  );
};
