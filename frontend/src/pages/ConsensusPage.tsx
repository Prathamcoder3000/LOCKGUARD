import React, { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  ArrowRightLeft,
  CheckCircle2,
  Crown,
  Database,
  GitBranch,
  HardDrive,
  RefreshCw,
  Server,
  ShieldAlert,
  ShieldCheck,
} from 'lucide-react';
import { useAuth } from '../context/AuthContext';
import { apiClient } from '../services/api';
import { auditService, AuditEvent } from '../services/auditService';
import { clusterService } from '../services/clusterService';
import {
  ChaosStatusReport,
  ClusterRecoveryReport,
  resilienceService,
  SnapshotDiagnosticsReport,
} from '../services/resilienceService';

export const ConsensusPage: React.FC = () => {
  const { user } = useAuth();
  const isAdmin = (user?.role || '').toUpperCase() === 'ADMIN';

  const [recovery, setRecovery] = useState<ClusterRecoveryReport | null>(null);
  const [chaos, setChaos] = useState<ChaosStatusReport | null>(null);
  const [snapshots, setSnapshots] = useState<SnapshotDiagnosticsReport | null>(null);
  const [leaderEvents, setLeaderEvents] = useState<AuditEvent[]>([]);
  const [loading, setLoading] = useState<boolean>(true);
  const [stepdownLoading, setStepdownLoading] = useState<boolean>(false);
  const [lastUpdated, setLastUpdated] = useState<string>('');
  const [banner, setBanner] = useState<{ type: 'success' | 'error'; text: string } | null>(null);

  const fetchConsensusState = async () => {
    setLoading(true);
    try {
      const [recRes, chaosRes, snapRes, auditRes] = await Promise.allSettled([
        resilienceService.getClusterRecovery(),
        apiClient.get<{ success: boolean; data: ChaosStatusReport }>('/recovery/chaos'),
        resilienceService.getSnapshots(),
        auditService.getEvents({ event_type: 'LEADER_CHANGE', limit: 15 }),
      ]);

      if (recRes.status === 'fulfilled') setRecovery(recRes.value);
      if (chaosRes.status === 'fulfilled') setChaos(chaosRes.value.data.data);
      if (snapRes.status === 'fulfilled') setSnapshots(snapRes.value);
      if (auditRes.status === 'fulfilled') setLeaderEvents(auditRes.value.events || []);

      setLastUpdated(new Date().toLocaleTimeString());
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchConsensusState();
    const interval = setInterval(fetchConsensusState, 8000);
    return () => clearInterval(interval);
  }, []);

  const handleStepdown = async () => {
    if (!confirm('Initiate Raft leadership transfer on the current leader?')) return;
    setStepdownLoading(true);
    setBanner(null);
    try {
      const res = await clusterService.adminStepdown();
      setBanner({
        type: 'success',
        text: res?.message || 'Leader stepdown initiated. New Raft election triggered across quorum.',
      });
      setTimeout(fetchConsensusState, 1200);
    } catch (err: any) {
      setBanner({
        type: 'error',
        text: err?.response?.data?.error?.message || err?.message || 'Leader stepdown failed',
      });
    } finally {
      setStepdownLoading(false);
    }
  };

  const leaderNode = useMemo(
    () =>
      recovery?.coordinators?.find((c) => c.is_leader) ||
      recovery?.coordinators?.find((c) => c.node_id === recovery.current_leader_id),
    [recovery]
  );

  const totalNodes = recovery?.total_nodes || recovery?.coordinators?.length || 3;
  const availableNodes = recovery?.available_nodes?.length ?? recovery?.coordinators?.filter((c) => c.reachable).length ?? 0;
  const requiredQuorum = Math.floor(totalNodes / 2) + 1;

  const leaderCommitIndex = leaderNode?.commit_index ?? leaderNode?.applied_index ?? 0;
  const activeTerm =
    leaderNode?.current_term ??
    recovery?.coordinators?.reduce((max, c) => Math.max(max, c.current_term ?? 0), 0) ??
    0;

  const leaderTransitions = chaos?.leader_history || [];

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex flex-col justify-between gap-4 sm:flex-row sm:items-center">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-slate-900">Consensus &amp; Raft State</h1>
          <p className="text-sm text-slate-500">
            HashiCorp Raft term epochs, quorum majority invariants, log replication alignment, and leadership history
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-2.5">
          {lastUpdated && (
            <span className="text-xs text-slate-400">
              Updated: <span className="font-mono text-slate-600">{lastUpdated}</span>
            </span>
          )}
          <Link
            to="/cluster"
            className="inline-flex items-center space-x-1.5 rounded-lg border border-slate-200 bg-white px-3 py-2 text-xs font-medium text-slate-700 hover:bg-slate-50"
          >
            <Server className="h-3.5 w-3.5 text-slate-500" />
            <span>Coordinator Nodes</span>
          </Link>
          {isAdmin && (
            <button
              onClick={handleStepdown}
              disabled={stepdownLoading || loading}
              className="inline-flex items-center space-x-1.5 rounded-lg border border-amber-300 bg-amber-50 px-3.5 py-2 text-xs font-semibold text-amber-800 shadow-sm hover:bg-amber-100 disabled:opacity-50"
            >
              <ArrowRightLeft className={`h-3.5 w-3.5 ${stepdownLoading ? 'animate-spin' : ''}`} />
              <span>Trigger Stepdown</span>
            </button>
          )}
          <button
            onClick={fetchConsensusState}
            disabled={loading}
            className="inline-flex items-center space-x-1.5 rounded-lg border border-slate-200 bg-white px-3.5 py-2 text-xs font-medium text-slate-700 shadow-subtle hover:bg-slate-50 disabled:opacity-50"
          >
            <RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin text-blue-600' : 'text-slate-500'}`} />
            <span>Refresh</span>
          </button>
        </div>
      </div>

      {banner && (
        <div
          className={`rounded-lg p-3 text-xs font-medium border flex items-center space-x-2 ${
            banner.type === 'success'
              ? 'bg-emerald-50 border-emerald-200 text-emerald-800'
              : 'bg-rose-50 border-rose-200 text-rose-800'
          }`}
        >
          {banner.type === 'success' ? (
            <CheckCircle2 className="h-4 w-4 shrink-0 text-emerald-600" />
          ) : (
            <ShieldAlert className="h-4 w-4 shrink-0 text-rose-600" />
          )}
          <span>{banner.text}</span>
        </div>
      )}

      {/* Consensus KPI Strip */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-card">
          <span className="text-xs font-medium uppercase text-slate-500">Current Leader</span>
          <div className="mt-2 flex items-center space-x-2">
            <Crown className="h-5 w-5 text-amber-500" />
            <p className="text-lg font-bold font-mono text-slate-900">
              {recovery ? recovery.current_leader_id || 'Electing...' : 'Probing...'}
            </p>
          </div>
          <p className="mt-1 text-xs text-slate-500">Sole authoritative Raft log mutator</p>
        </div>

        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-card">
          <span className="text-xs font-medium uppercase text-slate-500">Active Raft Term</span>
          <p className="mt-2 text-2xl font-bold font-mono text-slate-900">{activeTerm > 0 ? activeTerm : '—'}</p>
          <p className="mt-1 text-xs text-slate-500">Monotonic election term epoch</p>
        </div>

        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-card">
          <span className="text-xs font-medium uppercase text-slate-500">Majority Quorum Math</span>
          <p className="mt-2 text-2xl font-bold font-mono text-indigo-700">
            {availableNodes} / {totalNodes}{' '}
            <span className="text-xs font-normal text-slate-500">(need &ge; {requiredQuorum})</span>
          </p>
          <p className="mt-1 text-xs text-slate-500">
            <code>floor({totalNodes}/2) + 1 = {requiredQuorum}</code> votes required ({recovery?.quorum_state || 'UNKNOWN'})
          </p>
        </div>

        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-card">
          <span className="text-xs font-medium uppercase text-slate-500">Leader Commit / Applied</span>
          <p className="mt-2 text-2xl font-bold font-mono text-blue-600">
            {leaderCommitIndex > 0 ? `#${leaderCommitIndex}` : '—'}
          </p>
          <p className="mt-1 text-xs text-slate-500">
            Snapshots stored:{' '}
            <span className="font-mono font-semibold text-slate-700">
              {snapshots?.snapshots?.length ?? (snapshots?.snapshot_available ? 1 : 0)}
            </span>
          </p>
        </div>
      </div>

      {/* Log Replication Alignment Table */}
      <div className="rounded-xl border border-slate-200 bg-white shadow-card overflow-hidden">
        <div className="border-b border-slate-200 px-6 py-4 flex items-center justify-between">
          <div className="flex items-center space-x-2">
            <GitBranch className="h-5 w-5 text-indigo-600" />
            <div>
              <h2 className="text-sm font-semibold text-slate-900">Log Replication &amp; Index Alignment</h2>
              <p className="text-xs text-slate-500">
                Per-coordinator BoltDB Raft commit, applied, and last log indices relative to active leader
              </p>
            </div>
          </div>
          <span className="rounded-full bg-indigo-50 px-2.5 py-0.5 text-[11px] font-mono font-semibold text-indigo-700 border border-indigo-200">
            Fencing Monotonicity Enforced
          </span>
        </div>

        <div className="overflow-x-auto">
          <table className="min-w-full divide-y divide-slate-200 text-left text-xs">
            <thead className="bg-slate-50 text-slate-600 uppercase font-medium">
              <tr>
                <th className="px-6 py-3">Coordinator</th>
                <th className="px-6 py-3">Role / State</th>
                <th className="px-6 py-3">Term</th>
                <th className="px-6 py-3">Commit Index</th>
                <th className="px-6 py-3">Applied Index</th>
                <th className="px-6 py-3">Last Log Index</th>
                <th className="px-6 py-3">Replication Lag</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-200 text-slate-800">
              {(recovery?.coordinators || []).map((c) => {
                const nodeApplied = c.applied_index ?? 0;
                const lag = c.reachable && leaderCommitIndex > 0 ? Math.max(0, leaderCommitIndex - nodeApplied) : null;
                return (
                  <tr key={c.node_id} className="hover:bg-slate-50/70">
                    <td className="px-6 py-3.5 font-mono font-semibold text-slate-900">{c.node_id}</td>
                    <td className="px-6 py-3.5">
                      <span
                        className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-[11px] font-semibold ${
                          c.is_leader
                            ? 'bg-blue-50 text-blue-700 border border-blue-200'
                            : c.reachable
                            ? 'bg-emerald-50 text-emerald-700 border border-emerald-200'
                            : 'bg-rose-50 text-rose-700 border border-rose-200'
                        }`}
                      >
                        {c.is_leader ? 'Leader' : c.reachable ? c.role || 'Follower' : 'Offline'}
                      </span>
                    </td>
                    <td className="px-6 py-3.5 font-mono text-slate-700">{c.current_term ?? '—'}</td>
                    <td className="px-6 py-3.5 font-mono text-slate-700">{c.commit_index ?? '—'}</td>
                    <td className="px-6 py-3.5 font-mono text-blue-700 font-semibold">{c.applied_index ?? '—'}</td>
                    <td className="px-6 py-3.5 font-mono text-slate-700">{c.last_index ?? '—'}</td>
                    <td className="px-6 py-3.5 font-mono">
                      {lag === null ? (
                        <span className="text-slate-400">N/A</span>
                      ) : lag === 0 ? (
                        <span className="text-emerald-700 font-semibold">0 entries (in sync)</span>
                      ) : (
                        <span className="text-amber-700 font-semibold">{lag} entries</span>
                      )}
                    </td>
                  </tr>
                );
              })}
              {(!recovery || recovery.coordinators.length === 0) && (
                <tr>
                  <td colSpan={7} className="px-6 py-6 text-center text-slate-400">
                    Waiting for Raft cluster telemetry...
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Snapshot Compaction Summary + Leader Transition History */}
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        <div className="rounded-xl border border-slate-200 bg-white p-6 shadow-card">
          <div className="flex items-center justify-between border-b border-slate-100 pb-3 mb-4">
            <div className="flex items-center space-x-2">
              <HardDrive className="h-5 w-5 text-emerald-600" />
              <h2 className="text-sm font-semibold text-slate-900">Snapshot &amp; Log Compaction</h2>
            </div>
            <Link to="/resilience" className="text-xs font-medium text-blue-600 hover:underline">
              Manage Snapshots &rarr;
            </Link>
          </div>
          <dl className="grid grid-cols-2 gap-3 text-xs">
            <div className="rounded-lg bg-slate-50 p-3 border border-slate-100">
              <dt className="text-slate-400">Stored Snapshots</dt>
              <dd className="mt-1 font-mono text-lg font-bold text-slate-900">
                {snapshots?.snapshots?.length ?? (snapshots?.snapshot_available ? 1 : 0)}
              </dd>
            </div>
            <div className="rounded-lg bg-slate-50 p-3 border border-slate-100">
              <dt className="text-slate-400">Latest Snapshot Index</dt>
              <dd className="mt-1 font-mono text-lg font-bold text-slate-900">
                {snapshots?.last_snapshot_index ? `#${snapshots.last_snapshot_index}` : 'None'}
              </dd>
            </div>
            <div className="rounded-lg bg-slate-50 p-3 border border-slate-100">
              <dt className="text-slate-400">Snapshot Term / Threshold</dt>
              <dd className="mt-1 font-mono font-semibold text-slate-800">
                T{snapshots?.last_snapshot_term ?? '—'} &middot; {snapshots?.snapshot_threshold ?? '—'} ops
              </dd>
            </div>
            <div className="rounded-lg bg-slate-50 p-3 border border-slate-100">
              <dt className="text-slate-400">BoltDB Snapshot Store</dt>
              <dd className="mt-1 font-mono text-slate-700 truncate">
                {snapshots?.snapshots?.[0]?.id || 'Awaiting threshold / manual trigger'}
              </dd>
            </div>
          </dl>
        </div>

        <div className="rounded-xl border border-slate-200 bg-white p-6 shadow-card">
          <div className="flex items-center justify-between border-b border-slate-100 pb-3 mb-4">
            <div className="flex items-center space-x-2">
              <Database className="h-5 w-5 text-amber-600" />
              <h2 className="text-sm font-semibold text-slate-900">Leader Transition History</h2>
            </div>
            <span className="text-[11px] font-mono text-slate-500">
              Recorded Changes: {chaos?.leader_changes ?? leaderTransitions.length}
            </span>
          </div>

          {leaderTransitions.length > 0 ? (
            <div className="space-y-2 max-h-56 overflow-y-auto pr-1">
              {leaderTransitions.slice(0, 10).map((t, idx) => (
                <div
                  key={`${t.timestamp}-${idx}`}
                  className="flex items-center justify-between rounded-lg border border-slate-100 bg-slate-50 px-3 py-2 text-xs"
                >
                  <div>
                    <span className="font-mono text-slate-600">{t.previous_leader || 'none'}</span>
                    <span className="mx-1.5 text-slate-400">&rarr;</span>
                    <span className="font-mono font-semibold text-blue-700">{t.new_leader}</span>
                    <p className="text-[10px] text-slate-500 mt-0.5">Raft election transition</p>
                  </div>
                  <div className="text-right font-mono text-[10px] text-slate-500">
                    <div>{new Date(t.timestamp).toLocaleTimeString()}</div>
                  </div>
                </div>
              ))}
            </div>
          ) : leaderEvents.length > 0 ? (
            <div className="space-y-2 max-h-56 overflow-y-auto pr-1">
              {leaderEvents.slice(0, 10).map((ev) => (
                <div
                  key={ev.event_id}
                  className="flex items-center justify-between rounded-lg border border-slate-100 bg-slate-50 px-3 py-2 text-xs"
                >
                  <div>
                    <span className="font-mono font-semibold text-blue-700">{ev.coordinator_id}</span>
                    <span className="ml-2 text-slate-500">{ev.event_type}</span>
                  </div>
                  <span className="font-mono text-[10px] text-slate-500">
                    {new Date(ev.event_timestamp).toLocaleTimeString()}
                  </span>
                </div>
              ))}
            </div>
          ) : (
            <div className="rounded-lg border border-dashed border-slate-200 p-5 text-center text-xs text-slate-400">
              No leadership transitions recorded yet in this session. Use &quot;Trigger Stepdown&quot; to test a live Raft election.
            </div>
          )}
        </div>
      </div>

      {/* Safety Invariants */}
      <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-card">
        <div className="flex items-center space-x-2 mb-3">
          <ShieldCheck className="h-4 w-4 text-emerald-600" />
          <h2 className="text-sm font-semibold text-slate-900">Raft Consensus Guarantees</h2>
        </div>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-3 text-xs text-slate-600">
          <div className="p-3 rounded-lg bg-slate-50 border border-slate-100">
            <p className="font-semibold text-slate-800 mb-1">Leader Completeness</p>
            <p className="text-slate-500">Any elected leader possesses every committed lock grant and fencing token in its BoltDB Raft log.</p>
          </div>
          <div className="p-3 rounded-lg bg-slate-50 border border-slate-100">
            <p className="font-semibold text-slate-800 mb-1">Deterministic State Machine</p>
            <p className="text-slate-500">State transitions depend solely on replicated commands and client-supplied deterministic timestamps.</p>
          </div>
          <div className="p-3 rounded-lg bg-slate-50 border border-slate-100">
            <p className="font-semibold text-slate-800 mb-1">Fencing Token Safety</p>
            <p className="text-slate-500">Every grant increments the monotonic fencing token validated by Protected Resource (:9000).</p>
          </div>
        </div>
      </div>
    </div>
  );
};
