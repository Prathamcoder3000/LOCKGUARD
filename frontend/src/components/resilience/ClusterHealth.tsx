import React from 'react';
import { ShieldCheck, AlertTriangle, XCircle, CheckCircle2, RefreshCw } from 'lucide-react';
import { SystemHealthReport, ClusterRecoveryReport } from '../../services/resilienceService';

interface ClusterHealthProps {
  health: SystemHealthReport | null;
  cluster: ClusterRecoveryReport | null;
  loading: boolean;
  onRefresh: () => void;
}

export const ClusterHealth: React.FC<ClusterHealthProps> = ({
  health,
  cluster,
  loading,
  onRefresh,
}) => {
  const isHealthy = health?.overall_status === 'HEALTHY';
  const isDegraded = health?.overall_status === 'DEGRADED';
  const quorumAvailable = cluster ? cluster.quorum_available : health?.raft_quorum_available;

  return (
    <div className="rounded-xl border border-slate-200 bg-white p-6 shadow-sm">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between pb-4 border-b border-slate-100 gap-4">
        <div className="flex items-center space-x-3">
          <div
            className={`flex h-12 w-12 items-center justify-center rounded-xl ${
              isHealthy
                ? 'bg-emerald-50 text-emerald-600'
                : isDegraded
                ? 'bg-amber-50 text-amber-600'
                : 'bg-rose-50 text-rose-600'
            }`}
          >
            {isHealthy ? (
              <ShieldCheck className="h-6 w-6" />
            ) : isDegraded ? (
              <AlertTriangle className="h-6 w-6" />
            ) : (
              <XCircle className="h-6 w-6" />
            )}
          </div>
          <div>
            <div className="flex items-center space-x-2">
              <h2 className="text-lg font-bold text-slate-900">Cluster Health &amp; Quorum</h2>
              <span
                className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-semibold ${
                  isHealthy
                    ? 'bg-emerald-100 text-emerald-800'
                    : isDegraded
                    ? 'bg-amber-100 text-amber-800'
                    : 'bg-rose-100 text-rose-800'
                }`}
              >
                {health?.overall_status || 'UNKNOWN'}
              </span>
            </div>
            <p className="text-xs text-slate-500">
              Raft majority consensus invariant: Fail-closed on loss of majority
            </p>
          </div>
        </div>

        <button
          onClick={onRefresh}
          disabled={loading}
          className="inline-flex items-center space-x-2 rounded-lg border border-slate-200 px-3 py-1.5 text-xs font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-50"
        >
          <RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin text-blue-600' : ''}`} />
          <span>Refresh</span>
        </button>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4 mt-6">
        {/* Quorum Status Card */}
        <div className="rounded-lg border border-slate-100 bg-slate-50 p-4">
          <div className="flex items-center justify-between">
            <span className="text-xs font-medium text-slate-500">Quorum Requirement</span>
            <span
              className={`inline-flex items-center text-xs font-bold ${
                quorumAvailable ? 'text-emerald-600' : 'text-rose-600'
              }`}
            >
              {quorumAvailable ? (
                <>
                  <CheckCircle2 className="h-3.5 w-3.5 mr-1" />
                  Quorum Satisfied
                </>
              ) : (
                <>
                  <XCircle className="h-3.5 w-3.5 mr-1" />
                  Quorum Lost (Fail-Closed)
                </>
              )}
            </span>
          </div>
          <div className="mt-2">
            <div className="text-2xl font-bold font-mono text-slate-900">
              {cluster ? `${cluster.available_nodes.length} / ${cluster.total_nodes}` : '—'}
            </div>
            <p className="text-[11px] text-slate-500 mt-1">
              Minimum 2 of 3 nodes active to form consensus
            </p>
          </div>
        </div>

        {/* Current Leader Card */}
        <div className="rounded-lg border border-slate-100 bg-slate-50 p-4">
          <div className="flex items-center justify-between">
            <span className="text-xs font-medium text-slate-500">Authoritative Leader</span>
            <span className="inline-flex items-center text-xs font-bold text-blue-600">
              {cluster?.current_leader_id ? 'Active Leader' : 'Election in progress'}
            </span>
          </div>
          <div className="mt-2">
            <div className="text-lg font-bold font-mono text-slate-900 truncate">
              {cluster?.current_leader_id || health?.node_id || 'None (No Quorum)'}
            </div>
            <p className="text-[11px] text-slate-500 mt-1 truncate">
              {cluster?.current_leader_url || 'Raft election required'}
            </p>
          </div>
        </div>

        {/* Persistence & Audit Card */}
        <div className="rounded-lg border border-slate-100 bg-slate-50 p-4">
          <div className="flex items-center justify-between">
            <span className="text-xs font-medium text-slate-500">Audit &amp; Database</span>
            <span
              className={`inline-flex items-center text-xs font-bold ${
                health?.database_available ? 'text-emerald-600' : 'text-amber-600'
              }`}
            >
              {health?.database_available ? (
                <>
                  <CheckCircle2 className="h-3.5 w-3.5 mr-1" />
                  Online
                </>
              ) : (
                <>
                  <AlertTriangle className="h-3.5 w-3.5 mr-1" />
                  Degraded Backlog
                </>
              )}
            </span>
          </div>
          <div className="mt-2">
            <div className="text-2xl font-bold font-mono text-slate-900">
              {health?.database_available ? 'PostgreSQL 16' : 'Buffer Retrying'}
            </div>
            <p className="text-[11px] text-slate-500 mt-1">
              Async decoupled audit buffer preserves lock mutations
            </p>
          </div>
        </div>
      </div>

      {/* Subsystem Details */}
      {health?.subsystems && Object.keys(health.subsystems).length > 0 && (
        <div className="mt-6 border-t border-slate-100 pt-4">
          <h3 className="text-xs font-semibold uppercase tracking-wider text-slate-500 mb-3">
            Subsystem Health Diagnostics
          </h3>
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-3">
            {Object.entries(health.subsystems).map(([key, sub]) => (
              <div
                key={key}
                className="rounded-lg border border-slate-100 bg-slate-50/50 p-3 text-xs"
              >
                <div className="flex items-center justify-between mb-1">
                  <span className="font-semibold text-slate-800 capitalize">{sub.name || key}</span>
                  <span
                    className={`text-[10px] font-bold px-1.5 py-0.5 rounded ${
                      sub.status === 'HEALTHY'
                        ? 'bg-emerald-100 text-emerald-800'
                        : sub.status === 'DEGRADED'
                        ? 'bg-amber-100 text-amber-800'
                        : 'bg-rose-100 text-rose-800'
                    }`}
                  >
                    {sub.status}
                  </span>
                </div>
                <p className="text-slate-500 text-[11px] truncate">{sub.message}</p>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
};

