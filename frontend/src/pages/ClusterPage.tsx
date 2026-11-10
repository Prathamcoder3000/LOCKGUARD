import React, { useEffect, useState, useCallback } from 'react';
import { Server, Network, CheckCircle2, XCircle, Crown, RefreshCw, ShieldAlert, ArrowRightLeft } from 'lucide-react';
import { resilienceService, ClusterRecoveryReport } from '../services/resilienceService';
import { clusterService } from '../services/clusterService';
import { apiClient } from '../services/api';
import { useAuth } from '../context/AuthContext';

interface RaftLeaderStats {
  applied_index?: string | number;
  commit_index?: string | number;
  last_log_index?: string | number;
  last_log_term?: string | number;
  term?: string | number;
  num_peers?: string | number;
  state?: string;
}

export const ClusterPage: React.FC = () => {
  const { user } = useAuth();
  const isAdmin = user?.role === 'ADMIN';

  const [recovery, setRecovery] = useState<ClusterRecoveryReport | null>(null);
  const [leaderStats, setLeaderStats] = useState<RaftLeaderStats | null>(null);
  const [loading, setLoading] = useState<boolean>(true);
  const [stepdownLoading, setStepdownLoading] = useState<boolean>(false);
  const [message, setMessage] = useState<{ type: 'success' | 'error'; text: string } | null>(null);
  const [lastUpdated, setLastUpdated] = useState<string>('');

  const fetchClusterTelemetry = useCallback(async () => {
    try {
      const [recData, statusRes] = await Promise.allSettled([
        resilienceService.getClusterRecovery(),
        apiClient.get('/cluster/status'),
      ]);

      if (recData.status === 'fulfilled') {
        setRecovery(recData.value);
      }

      if (statusRes.status === 'fulfilled' && statusRes.value.data?.data) {
        const raw = statusRes.value.data.data;
        if (raw.stats) {
          setLeaderStats(raw.stats);
        } else if (raw.term || raw.commit_index) {
          setLeaderStats(raw);
        }
      }

      setLastUpdated(new Date().toLocaleTimeString());
    } catch {
      // Keep existing telemetry on transient error
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchClusterTelemetry();
    const interval = setInterval(fetchClusterTelemetry, 5000);
    return () => clearInterval(interval);
  }, [fetchClusterTelemetry]);

  const handleStepdown = async () => {
    if (!confirm('Initiate Raft leadership transfer on the active leader?')) return;
    setStepdownLoading(true);
    setMessage(null);
    try {
      const res = await clusterService.adminStepdown();
      setMessage({ type: 'success', text: res.message || 'Leadership transfer initiated.' });
      await fetchClusterTelemetry();
    } catch (err: any) {
      setMessage({
        type: 'error',
        text: err?.response?.data?.error?.message || err?.message || 'Failed to trigger stepdown',
      });
    } finally {
      setStepdownLoading(false);
    }
  };

  // Node static topology mappings for display fallback
  const defaultNodes = [
    { id: 'coordinator-a', peer: 'coordinator-a:7000', api: 'http://coordinator-a:8001', data: '/var/lib/lockguard/data/a' },
    { id: 'coordinator-b', peer: 'coordinator-b:7000', api: 'http://coordinator-b:8001', data: '/var/lib/lockguard/data/b' },
    { id: 'coordinator-c', peer: 'coordinator-c:7000', api: 'http://coordinator-c:8001', data: '/var/lib/lockguard/data/c' },
  ];

  return (
    <div className="space-y-6">
      {/* Header Banner */}
      <div className="flex flex-col justify-between gap-4 sm:flex-row sm:items-center">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-slate-900">Coordinator Nodes &amp; Consensus</h1>
          <p className="text-sm text-slate-500">
            3-Node HashiCorp Raft consensus topology, quorum metrics, and authoritative linearizable state
          </p>
        </div>
        <div className="flex items-center space-x-3">
          {lastUpdated && (
            <span className="text-xs text-slate-400">
              Refreshed: <span className="font-mono text-slate-600">{lastUpdated}</span>
            </span>
          )}
          {isAdmin && (
            <button
              onClick={handleStepdown}
              disabled={stepdownLoading || loading}
              className="inline-flex items-center space-x-2 rounded-lg border border-amber-300 bg-amber-50 px-3.5 py-2 text-xs font-semibold text-amber-800 shadow-sm hover:bg-amber-100 disabled:opacity-50"
              title="Step down current leader and initiate election"
            >
              <ArrowRightLeft className={`h-3.5 w-3.5 ${stepdownLoading ? 'animate-spin' : ''}`} />
              <span>Step Down Leader</span>
            </button>
          )}
          <button
            onClick={fetchClusterTelemetry}
            disabled={loading}
            className="inline-flex items-center space-x-2 rounded-lg border border-slate-200 bg-white px-3.5 py-2 text-xs font-medium text-slate-700 shadow-subtle hover:bg-slate-50 disabled:opacity-50"
          >
            <RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin text-blue-600' : 'text-slate-500'}`} />
            <span>Refresh</span>
          </button>
        </div>
      </div>

      {/* Admin Action Notification */}
      {message && (
        <div
          className={`rounded-lg p-3 text-xs font-medium border flex items-center space-x-2 ${
            message.type === 'success'
              ? 'bg-emerald-50 border-emerald-200 text-emerald-800'
              : 'bg-rose-50 border-rose-200 text-rose-800'
          }`}
        >
          {message.type === 'success' ? (
            <CheckCircle2 className="h-4 w-4 shrink-0 text-emerald-600" />
          ) : (
            <ShieldAlert className="h-4 w-4 shrink-0 text-rose-600" />
          )}
          <span>{message.text}</span>
        </div>
      )}

      {/* Quorum Summary KPI Grid */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-card">
          <span className="text-xs font-medium uppercase text-slate-500">Active Raft Leader</span>
          <div className="mt-2 flex items-center space-x-2">
            <Crown className="h-5 w-5 text-amber-500" />
            <p className="text-lg font-bold font-mono text-slate-900">
              {recovery?.current_leader_id || 'Electing...'}
            </p>
          </div>
          <p className="mt-1 text-xs text-slate-500">Sole authoritative mutating coordinator</p>
        </div>

        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-card">
          <span className="text-xs font-medium uppercase text-slate-500">Quorum Health</span>
          <div className="mt-2 flex items-center space-x-2">
            <div
              className={`h-3 w-3 rounded-full ${
                recovery?.quorum_available ? 'bg-emerald-500 animate-pulse' : 'bg-rose-500'
              }`}
            />
            <p className="text-lg font-bold text-slate-900">
              {recovery?.quorum_state || 'ESTABLISHED'}
            </p>
          </div>
          <p className="mt-1 text-xs text-slate-500">
            {recovery ? `${recovery.available_nodes.length} of ${recovery.total_nodes} nodes voting` : '2 of 3 required'}
          </p>
        </div>

        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-card">
          <span className="text-xs font-medium uppercase text-slate-500">Current Raft Term</span>
          <p className="mt-2 text-2xl font-bold font-mono text-slate-900">
            {leaderStats?.term ?? leaderStats?.last_log_term ?? '—'}
          </p>
          <p className="mt-1 text-xs text-slate-500">Monotonically increasing epoch counter</p>
        </div>

        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-card">
          <span className="text-xs font-medium uppercase text-slate-500">Applied Log Index</span>
          <p className="mt-2 text-2xl font-bold font-mono text-blue-600">
            {leaderStats?.applied_index ?? leaderStats?.commit_index ? `#${leaderStats?.applied_index ?? leaderStats?.commit_index}` : '—'}
          </p>
          <p className="mt-1 text-xs text-slate-500">Deterministic FSM state machine index</p>
        </div>
      </div>

      {/* Coordinator Nodes Grid */}
      <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
        {defaultNodes.map((defNode) => {
          const liveNode = recovery?.coordinators?.find((c) => c.node_id === defNode.id);
          const isLeader = liveNode?.is_leader ?? false;
          const isReachable = liveNode?.reachable ?? true;
          const role = liveNode?.role ?? (isLeader ? 'Leader' : 'Follower');

          return (
            <div
              key={defNode.id}
              className={`rounded-xl border bg-white p-5 shadow-card transition-all ${
                isLeader ? 'border-blue-400 ring-2 ring-blue-100 shadow-md' : 'border-slate-200'
              }`}
            >
              <div className="flex items-center justify-between">
                <div className="flex items-center space-x-2">
                  <div
                    className={`rounded-lg p-2 ${
                      isLeader ? 'bg-blue-600 text-white' : 'bg-slate-100 text-slate-600'
                    }`}
                  >
                    <Server className="h-4 w-4" />
                  </div>
                  <div>
                    <h3 className="font-mono text-sm font-bold text-slate-900">{defNode.id}</h3>
                    <span className="text-[10px] text-slate-400 font-mono">
                      {isLeader ? 'Cluster Authoritative Leader' : 'Consensus Follower'}
                    </span>
                  </div>
                </div>

                {isLeader ? (
                  <span className="inline-flex items-center rounded-full bg-blue-50 px-2.5 py-0.5 text-xs font-semibold text-blue-700 border border-blue-200">
                    <Crown className="mr-1 h-3.5 w-3.5 text-amber-500" />
                    Leader
                  </span>
                ) : (
                  <span className="inline-flex items-center rounded-full bg-slate-100 px-2.5 py-0.5 text-xs font-medium text-slate-600">
                    Follower
                  </span>
                )}
              </div>

              <div className="mt-4 space-y-2 border-t border-slate-100 pt-3 text-xs text-slate-600">
                <div className="flex justify-between">
                  <span className="text-slate-400">Raft Role:</span>
                  <span className={`font-semibold capitalize ${isLeader ? 'text-blue-700' : 'text-slate-800'}`}>
                    {role}
                  </span>
                </div>
                <div className="flex justify-between items-center">
                  <span className="text-slate-400">Connectivity:</span>
                  <span
                    className={`inline-flex items-center font-medium ${
                      isReachable ? 'text-emerald-700' : 'text-rose-600'
                    }`}
                  >
                    {isReachable ? (
                      <>
                        <CheckCircle2 className="mr-1 h-3.5 w-3.5 text-emerald-600" />
                        Reachable
                      </>
                    ) : (
                      <>
                        <XCircle className="mr-1 h-3.5 w-3.5 text-rose-600" />
                        Offline
                      </>
                    )}
                  </span>
                </div>
                <div className="flex justify-between">
                  <span className="text-slate-400">Raft Peer Port:</span>
                  <span className="font-mono text-slate-800">{defNode.peer}</span>
                </div>
                <div className="flex justify-between">
                  <span className="text-slate-400">Internal API:</span>
                  <span className="font-mono text-slate-800">{liveNode?.endpoint || defNode.api}</span>
                </div>
                <div className="flex justify-between">
                  <span className="text-slate-400">Persistent BoltDB:</span>
                  <span className="font-mono text-slate-800 text-[11px] truncate max-w-[150px]">{defNode.data}</span>
                </div>
              </div>
            </div>
          );
        })}
      </div>

      {/* Consensus Safety & Invariant Rules */}
      <div className="rounded-xl border border-slate-200 bg-white p-6 shadow-card">
        <div className="flex items-center space-x-2 mb-3">
          <Network className="h-5 w-5 text-indigo-600" />
          <h2 className="text-base font-semibold text-slate-900">Consensus Network &amp; Quorum Invariants</h2>
        </div>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4 text-xs text-slate-600">
          <div className="p-3 rounded-lg bg-slate-50 border border-slate-100">
            <p className="font-semibold text-slate-800 mb-1">Strict Majority Quorum</p>
            <p className="text-slate-500">
              Quorum requires <code>floor(N/2) + 1 = 2</code> votes for any log entry commit. An isolated minority partition fails closed immediately.
            </p>
          </div>
          <div className="p-3 rounded-lg bg-slate-50 border border-slate-100">
            <p className="font-semibold text-slate-800 mb-1">Single Authoritative Leader</p>
            <p className="text-slate-500">
              At most one coordinator holds active leadership per term. Follower write requests are redirected with transparent 421 HTTP status codes.
            </p>
          </div>
          <div className="p-3 rounded-lg bg-slate-50 border border-slate-100">
            <p className="font-semibold text-slate-800 mb-1">Isolated Raft Network</p>
            <p className="text-slate-500">
              Peer transport (port 7000) operates on an isolated bridge network. Client traffic enters exclusively through the REST API gateway.
            </p>
          </div>
        </div>
      </div>
    </div>
  );
};
