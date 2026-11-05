import React from 'react';
import { BarChart3 } from 'lucide-react';

interface MetricItem {
  name: string;
  key: string;
  value: string | number;
  unit?: string;
  type: 'gauge' | 'counter';
  description: string;
  status: 'good' | 'neutral' | 'warn';
}

export const ResilienceMetrics: React.FC = () => {
  const metrics: MetricItem[] = [
    {
      name: 'Raft Quorum Available',
      key: 'lockguard_quorum_available',
      value: 1,
      type: 'gauge',
      description: '1 if majority quorum is currently formed, 0 if lost',
      status: 'good',
    },
    {
      name: 'Cluster Nodes Online',
      key: 'lockguard_cluster_nodes_available',
      value: 3,
      unit: '/ 3',
      type: 'gauge',
      description: 'Active coordinator nodes participating in consensus',
      status: 'good',
    },
    {
      name: 'Leader Changes Total',
      key: 'lockguard_leader_changes_total',
      value: 2,
      type: 'counter',
      description: 'Total leader elections and transitions observed',
      status: 'neutral',
    },
    {
      name: 'Recovery Duration',
      key: 'lockguard_recovery_duration_seconds',
      value: '103',
      unit: 'ms',
      type: 'gauge',
      description: 'Leader failover detection and election duration',
      status: 'good',
    },
    {
      name: 'Chaos Tests Total',
      key: 'lockguard_chaos_tests_total',
      value: 3,
      type: 'counter',
      description: 'Executed chaos engineering injection experiments',
      status: 'neutral',
    },
    {
      name: 'Partitions Injected',
      key: 'lockguard_network_partitions_total',
      value: 1,
      type: 'counter',
      description: 'Network split scenarios tested with fail-closed safety',
      status: 'neutral',
    },
    {
      name: 'Snapshot Restores',
      key: 'lockguard_snapshot_restore_total',
      value: 1,
      type: 'counter',
      description: 'State machine restores from compacted snapshot',
      status: 'good',
    },
    {
      name: 'Stale Token Blocks',
      key: 'lockguard_protected_resource_errors_total',
      value: 1,
      type: 'counter',
      description: 'Protected resource 409 fencing token rejections',
      status: 'good',
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

      <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mt-4">
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
