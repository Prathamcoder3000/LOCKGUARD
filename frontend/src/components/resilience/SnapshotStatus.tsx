import React, { useState } from 'react';
import {
  Database,
  HardDrive,
  CheckCircle2,
  History,
  RotateCcw,
  PlusCircle,
  AlertTriangle,
  RefreshCw,
  XCircle,
} from 'lucide-react';
import {
  SnapshotDiagnosticsReport,
  resilienceService,
  extractResilienceError,
} from '../../services/resilienceService';

interface SnapshotStatusProps {
  snapshot: SnapshotDiagnosticsReport | null;
  isAdmin?: boolean;
  onActionCompleted?: () => Promise<void> | void;
}

export const SnapshotStatus: React.FC<SnapshotStatusProps> = ({
  snapshot,
  isAdmin = false,
  onActionCompleted,
}) => {
  const [confirmRestoreId, setConfirmRestoreId] = useState<string | null>(null);
  const [busyAction, setBusyAction] = useState<'create' | 'restore' | null>(null);
  const [banner, setBanner] = useState<{ kind: 'success' | 'error'; text: string } | null>(null);

  const snapshots = snapshot?.snapshots ?? [];

  const handleCreateSnapshot = async () => {
    setBusyAction('create');
    setBanner(null);
    try {
      const res = await resilienceService.createSnapshot();
      setBanner({ kind: 'success', text: res.message });
      if (onActionCompleted) await onActionCompleted();
    } catch (err: any) {
      setBanner({ kind: 'error', text: extractResilienceError(err, 'Failed to create Raft snapshot') });
    } finally {
      setBusyAction(null);
    }
  };

  const handleConfirmRestore = async (snapshotId: string) => {
    setBusyAction('restore');
    setBanner(null);
    try {
      const res = await resilienceService.restoreSnapshot(snapshotId, true);
      setConfirmRestoreId(null);
      setBanner({ kind: 'success', text: res.message });
      if (onActionCompleted) await onActionCompleted();
    } catch (err: any) {
      setBanner({ kind: 'error', text: extractResilienceError(err, 'Snapshot restore failed') });
    } finally {
      setBusyAction(null);
    }
  };

  return (
    <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 pb-3 border-b border-slate-100">
        <div className="flex items-center space-x-2">
          <div className="rounded-lg bg-indigo-50 p-2 text-indigo-600">
            <HardDrive className="h-4 w-4" />
          </div>
          <div>
            <h3 className="text-base font-bold text-slate-900">Raft Snapshots &amp; Compaction</h3>
            <p className="text-xs text-slate-500">
              State machine compaction, validation, and quorum-wide snapshot restoration
            </p>
          </div>
        </div>
        <div className="flex items-center space-x-2">
          <span className="inline-flex items-center rounded-full bg-emerald-50 px-2.5 py-1 text-xs font-semibold text-emerald-700">
            <CheckCircle2 className="mr-1.5 h-3.5 w-3.5" />
            Storage Healthy
          </span>
          <button
            type="button"
            onClick={handleCreateSnapshot}
            disabled={!isAdmin || busyAction !== null}
            className="inline-flex items-center rounded-lg bg-indigo-600 px-3 py-1.5 text-xs font-semibold text-white hover:bg-indigo-700 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {busyAction === 'create' ? (
              <>
                <RefreshCw className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                Snapshotting...
              </>
            ) : (
              <>
                <PlusCircle className="mr-1.5 h-3.5 w-3.5" />
                Create Snapshot
              </>
            )}
          </button>
        </div>
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

      {banner && (
        <div
          className={`mt-4 rounded-lg border p-3 text-xs flex items-start space-x-2 ${
            banner.kind === 'success'
              ? 'border-emerald-200 bg-emerald-50 text-emerald-800'
              : 'border-rose-200 bg-rose-50 text-rose-800'
          }`}
        >
          {banner.kind === 'success' ? (
            <CheckCircle2 className="h-4 w-4 shrink-0 mt-0.5" />
          ) : (
            <XCircle className="h-4 w-4 shrink-0 mt-0.5" />
          )}
          <span>{banner.text}</span>
        </div>
      )}

      {/* Available Snapshots Table & Restore Controls */}
      <div className="mt-4">
        <h4 className="text-xs font-semibold uppercase tracking-wider text-slate-500 mb-2">
          Available Persisted Snapshots ({snapshots.length})
        </h4>
        {snapshots.length === 0 ? (
          <div className="rounded-lg border border-slate-100 bg-slate-50/70 p-4 text-xs text-slate-600">
            No compacted Raft snapshots exist yet. Use <strong>Create Snapshot</strong> above to capture a validated
            point-in-time snapshot from the current leader.
          </div>
        ) : (
          <div className="overflow-x-auto rounded-lg border border-slate-100">
            <table className="w-full text-left text-xs">
              <thead className="bg-slate-50 text-[11px] font-semibold uppercase tracking-wider text-slate-500">
                <tr>
                  <th className="px-3 py-2">Snapshot ID</th>
                  <th className="px-3 py-2">Index / Term</th>
                  <th className="px-3 py-2">Resources</th>
                  <th className="px-3 py-2">Size</th>
                  <th className="px-3 py-2">Validation</th>
                  <th className="px-3 py-2 text-right">Recovery Action</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100">
                {snapshots.map((snap) => (
                  <React.Fragment key={snap.id}>
                    <tr className="hover:bg-slate-50/60">
                      <td className="px-3 py-2.5 font-mono font-semibold text-slate-900">{snap.id}</td>
                      <td className="px-3 py-2.5 font-mono text-slate-600">
                        idx {snap.index} / term {snap.term}
                      </td>
                      <td className="px-3 py-2.5 font-mono text-slate-600">{snap.resources_count}</td>
                      <td className="px-3 py-2.5 font-mono text-slate-600">{snap.size} B</td>
                      <td className="px-3 py-2.5">
                        {snap.valid ? (
                          <span className="inline-flex items-center rounded-full bg-emerald-50 px-2 py-0.5 text-[11px] font-medium text-emerald-700">
                            Verified
                          </span>
                        ) : (
                          <span className="inline-flex items-center rounded-full bg-rose-50 px-2 py-0.5 text-[11px] font-medium text-rose-700">
                            Corrupt
                          </span>
                        )}
                      </td>
                      <td className="px-3 py-2.5 text-right">
                        <button
                          type="button"
                          onClick={() => setConfirmRestoreId(confirmRestoreId === snap.id ? null : snap.id)}
                          disabled={!isAdmin || !snap.valid || busyAction !== null}
                          className="inline-flex items-center rounded-md border border-indigo-200 bg-indigo-50 px-2.5 py-1 text-[11px] font-semibold text-indigo-700 hover:bg-indigo-100 disabled:opacity-50 disabled:cursor-not-allowed"
                        >
                          <RotateCcw className="mr-1 h-3 w-3" />
                          Restore Snapshot
                        </button>
                      </td>
                    </tr>
                    {confirmRestoreId === snap.id && (
                      <tr className="bg-amber-50/80">
                        <td colSpan={6} className="px-3 py-3">
                          <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2">
                            <div className="flex items-start space-x-2 text-xs text-amber-900">
                              <AlertTriangle className="h-4 w-4 text-amber-600 shrink-0 mt-0.5" />
                              <span>
                                Confirm cluster-wide restore to snapshot{' '}
                                <strong className="font-mono">{snap.id}</strong>? Active in-memory lock leases will be
                                replaced across all Raft coordinators.
                              </span>
                            </div>
                            <div className="flex items-center space-x-2 shrink-0">
                              <button
                                type="button"
                                onClick={() => handleConfirmRestore(snap.id)}
                                disabled={busyAction !== null}
                                className="inline-flex items-center rounded-md bg-rose-600 px-3 py-1 text-xs font-semibold text-white hover:bg-rose-700 disabled:opacity-50"
                              >
                                {busyAction === 'restore' ? 'Restoring...' : 'Confirm Restore'}
                              </button>
                              <button
                                type="button"
                                onClick={() => setConfirmRestoreId(null)}
                                disabled={busyAction !== null}
                                className="rounded-md border border-slate-300 bg-white px-2.5 py-1 text-xs font-medium text-slate-700 hover:bg-slate-50"
                              >
                                Cancel
                              </button>
                            </div>
                          </div>
                        </td>
                      </tr>
                    )}
                  </React.Fragment>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <div className="mt-4 pt-3 border-t border-slate-100 flex items-center justify-between text-xs text-slate-500">
        <div className="flex items-center space-x-2">
          <Database className="h-4 w-4 text-slate-400" />
          <span>
            Underlying Storage Engine: <strong className="text-slate-700">BoltDB + FileSnapshotStore</strong>
          </span>
        </div>
        <div className="flex items-center space-x-1 font-mono text-[11px] text-slate-400">
          <History className="h-3.5 w-3.5" />
          <span>
            Last inspected:{' '}
            {snapshot?.generated_at ? new Date(snapshot.generated_at).toLocaleTimeString() : 'Just now'}
          </span>
        </div>
      </div>
    </div>
  );
};
