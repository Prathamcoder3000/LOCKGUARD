import React from 'react';
import { Database, HardDrive, CheckCircle2, History } from 'lucide-react';
import { SnapshotDiagnosticsReport } from '../../services/resilienceService';

interface SnapshotStatusProps {
  snapshot: SnapshotDiagnosticsReport | null;
}

export const SnapshotStatus: React.FC<SnapshotStatusProps> = ({ snapshot }) => {
  return (
    <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm">
      <div className="flex items-center justify-between pb-3 border-b border-slate-100">
        <div className="flex items-center space-x-2">
          <div className="rounded-lg bg-indigo-50 p-2 text-indigo-600">
            <HardDrive className="h-4 w-4" />
          </div>
          <div>
            <h3 className="text-base font-bold text-slate-900">Raft Snapshots &amp; Compaction</h3>
            <p className="text-xs text-slate-500">
              State machine compaction prevents unbounded log growth and accelerates catch-up
            </p>
          </div>
        </div>
        <span className="inline-flex items-center rounded-full bg-emerald-50 px-2.5 py-1 text-xs font-semibold text-emerald-700">
          <CheckCircle2 className="mr-1.5 h-3.5 w-3.5" />
          Storage Healthy
        </span>
      </div>

      <div className="grid grid-cols-2 sm:grid-cols-4 gap-4 mt-4">
        <div className="p-3 rounded-lg bg-slate-50 border border-slate-100">
          <div className="text-xs text-slate-500">Last Snapshot Index</div>
          <div className="text-xl font-bold font-mono text-slate-900 mt-1">
            {snapshot?.last_snapshot_index ?? 0}
          </div>
          <div className="text-[10px] text-slate-400 mt-0.5">Raft committed index</div>
        </div>

        <div className="p-3 rounded-lg bg-slate-50 border border-slate-100">
          <div className="text-xs text-slate-500">Snapshot Term</div>
          <div className="text-xl font-bold font-mono text-slate-900 mt-1">
            {snapshot?.last_snapshot_term ?? 1}
          </div>
          <div className="text-[10px] text-slate-400 mt-0.5">Epoch term of snapshot</div>
        </div>

        <div className="p-3 rounded-lg bg-slate-50 border border-slate-100">
          <div className="text-xs text-slate-500">Compaction Threshold</div>
          <div className="text-xl font-bold font-mono text-slate-900 mt-1">
            {snapshot?.snapshot_threshold ?? 8192}
          </div>
          <div className="text-[10px] text-slate-400 mt-0.5">Entries before snapshot</div>
        </div>

        <div className="p-3 rounded-lg bg-slate-50 border border-slate-100">
          <div className="text-xs text-slate-500">Compaction Interval</div>
          <div className="text-xl font-bold font-mono text-slate-900 mt-1">
            {snapshot?.snapshot_interval ?? '120s'}
          </div>
          <div className="text-[10px] text-slate-400 mt-0.5">Periodic evaluation timer</div>
        </div>
      </div>

      <div className="mt-4 pt-3 border-t border-slate-100 flex items-center justify-between text-xs text-slate-500">
        <div className="flex items-center space-x-2">
          <Database className="h-4 w-4 text-slate-400" />
          <span>Underlying Storage Engine: <strong className="text-slate-700">BoltDB + FileSnapshotStore</strong></span>
        </div>
        <div className="flex items-center space-x-1 font-mono text-[11px] text-slate-400">
          <History className="h-3.5 w-3.5" />
          <span>Last inspected: {snapshot?.generated_at ? new Date(snapshot.generated_at).toLocaleTimeString() : 'Just now'}</span>
        </div>
      </div>
    </div>
  );
};

