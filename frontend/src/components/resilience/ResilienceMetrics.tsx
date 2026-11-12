import React, { useEffect, useState, useCallback } from 'react';
import { BarChart3 } from 'lucide-react';
import { apiClient } from '../../services/api';

interface MetricItem {
  name: string;
  key: string;
  value: string | number;
  unit?: string;
  type: 'gauge' | 'counter';
  description: string;
  status: 'good' | 'neutral' | 'warn';
}

function parsePrometheusMetrics(text: string): Record<string, number> {
  const sums: Record<string, number> = {};
  for (const rawLine of text.split('\n')) {
    const line = rawLine.trim();
    if (!line || line.startsWith('#')) continue;
    const match = line.match(/^([a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{[^}]*\})?\s+([-+]?\d+(?:\.\d+)?)$/);
    if (!match) continue;
    const name = match[1];
    const val = Number(match[2]);
    if (!Number.isNaN(val)) {
      sums[name] = (sums[name] ?? 0) + val;
    }
  }
  return sums;
}

interface ResilienceMetricsProps {
  refreshKey?: number;
}

export const ResilienceMetrics: React.FC<ResilienceMetricsProps> = ({ refreshKey = 0 }) => {
  const [parsed, setParsed] = useState<Record<string, number>>({});
  const [nodesOnline, setNodesOnline] = useState<number | null>(null);
  const [totalNodes, setTotalNodes] = useState<number>(3);
  const [quorumAvail, setQuorumAvail] = useState<number | null>(null);
  const [fetchError, setFetchError] = useState<boolean>(false);

  const loadMetrics = useCallback(async () => {
    try {
      const [metricsRes, clusterRes] = await Promise.allSettled([
        apiClient.get('/metrics', { responseType: 'text', transformResponse: [(data) => data] }),
        apiClient.get('/recovery/cluster'),
      ]);

      let nextParsed: Record<string, number> = {};
      let ok = false;

      if (metricsRes.status === 'fulfilled' && typeof metricsRes.value.data === 'string') {
        nextParsed = parsePrometheusMetrics(metricsRes.value.data);
        setParsed(nextParsed);
        ok = true;
      }

      if (clusterRes.status === 'fulfilled' && clusterRes.value.data?.data) {
        const cl = clusterRes.value.data.data;
        setNodesOnline(Array.isArray(cl.available_nodes) ? cl.available_nodes.length : 0);
        setTotalNodes(typeof cl.total_nodes === 'number' && cl.total_nodes > 0 ? cl.total_nodes : 3);
        setQuorumAvail(cl.quorum_available ? 1 : 0);
        ok = true;
      }

      setFetchError(!ok);
    } catch {
      setFetchError(true);
    }
  }, []);

  useEffect(() => {
    loadMetrics();
    const id = setInterval(loadMetrics, 10000);
    return () => clearInterval(id);
  }, [loadMetrics, refreshKey]);

  const quorumVal = quorumAvail ?? parsed['lockguard_quorum_available'] ?? 0;
  const nodesVal = nodesOnline ?? parsed['lockguard_cluster_nodes_available'] ?? 0;

  const metrics: MetricItem[] = [
    {
      name: 'Raft Quorum Available',
      key: 'lockguard_quorum_available',
      value: fetchError && quorumAvail === null ? '—' : quorumVal,
      type: 'gauge',
      description: '1 if majority quorum is currently formed, 0 if lost',
      status: quorumVal === 1 ? 'good' : 'warn',
    },
    {
      name: 'Cluster Nodes Online',
      key: 'lockguard_cluster_nodes_available',
      value: fetchError && nodesOnline === null ? '—' : nodesVal,
      unit: `/ ${totalNodes}`,
      type: 'gauge',
      description: 'Active coordinator nodes participating in consensus',
      status: nodesVal >= Math.floor(totalNodes / 2) + 1 ? 'good' : 'warn',
    },
    {
      name: 'Leader Changes Total',
      key: 'lockguard_leader_changes_total',
      value: parsed['lockguard_leader_changes_total'] ?? 0,
      type: 'counter',
      description: 'Total leader elections and transitions observed',
      status: 'neutral',
    },
    {
      name: 'Recovery Duration',
      key: 'lockguard_recovery_duration_seconds',
      value: parsed['lockguard_recovery_duration_seconds'] ?? 0,
      unit: 'ms',
      type: 'gauge',
      description: 'Latest recorded subsystem recovery duration',
      status: 'good',
    },
    {
      name: 'Chaos Tests Total',
      key: 'lockguard_chaos_tests_total',
      value: parsed['lockguard_chaos_tests_total'] ?? 0,
      type: 'counter',
      description: 'Executed chaos engineering injection experiments',
      status: 'neutral',
    },
    {
      name: 'Partitions Injected',
      key: 'lockguard_network_partitions_total',
      value: parsed['lockguard_network_partitions_total'] ?? 0,
      type: 'counter',
      description: 'Network split scenarios tested with fail-closed safety',
      status: 'neutral',
    },
    {
      name: 'Snapshot Restores',
      key: 'lockguard_snapshot_restore_total',
      value: parsed['lockguard_snapshot_restore_total'] ?? 0,
      type: 'counter',
      description: 'State machine restores from compacted snapshot',
      status: 'good',
    },
    {
      name: 'Authz Denials Total',
      key: 'lockguard_authz_denials_total',
      value: parsed['lockguard_authz_denials_total'] ?? 0,
      type: 'counter',
      description: 'RBAC & cross-owner authorization rejections (HTTP 403)',
      status: (parsed['lockguard_authz_denials_total'] ?? 0) > 0 ? 'warn' : 'good',
    },
    {
      name: 'Auth Failures Total',
      key: 'lockguard_auth_failures_total',
      value: parsed['lockguard_auth_failures_total'] ?? 0,
      type: 'counter',
      description: 'Unauthenticated, expired, or invalid JWT rejections (HTTP 401)',
      status: (parsed['lockguard_auth_failures_total'] ?? 0) > 0 ? 'warn' : 'good',
    },
  ];

  return (
    <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm">
      <div className="flex items-center justify-between pb-3 border-b border-slate-100">
        <div className="flex items-center space-x-2">
          <div className="rounded-lg bg-emerald-50 p-2 text-emerald-600">
            <BarChart3 className="h-4 w-4" />
          </div>
          <div>
            <h3 className="text-base font-bold text-slate-900">Resilience &amp; Recovery Gauges</h3>
            <p className="text-xs text-slate-500">
              Exported Prometheus metrics monitoring high-availability invariants
            </p>
          </div>
        </div>
        <span className="inline-flex items-center rounded-full bg-slate-100 px-2 py-0.5 font-mono text-[11px] text-slate-600">
          /metrics
        </span>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4 mt-4">
        {metrics.map((m) => (
          <div
            key={m.key}
            className="p-3.5 rounded-lg border border-slate-100 bg-slate-50/50 hover:bg-slate-50 transition-colors"
          >
            <div className="flex items-center justify-between text-xs text-slate-500">
              <span className="truncate font-medium">{m.name}</span>
              <span
                className={`h-2 w-2 rounded-full ${
                  m.status === 'good'
                    ? 'bg-emerald-500'
                    : m.status === 'warn'
                    ? 'bg-amber-500'
                    : 'bg-blue-500'
                }`}
              />
            </div>
            <div className="mt-2 flex items-baseline space-x-1">
              <span className="text-2xl font-bold font-mono text-slate-900">{m.value}</span>
              {m.unit && <span className="text-xs font-mono text-slate-500">{m.unit}</span>}
            </div>
            <div className="mt-1 font-mono text-[10px] text-slate-400 truncate">
              {m.key}
            </div>
            <div className="mt-1 text-[11px] text-slate-500 leading-tight">
              {m.description}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
};
