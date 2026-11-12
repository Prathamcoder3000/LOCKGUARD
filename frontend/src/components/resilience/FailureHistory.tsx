import React from 'react';
import { ScrollText, CheckCircle, ShieldAlert, Clock } from 'lucide-react';
import { FailureEvent } from '../../services/resilienceService';

interface FailureHistoryProps {
  events: FailureEvent[];
}

export const FailureHistory: React.FC<FailureHistoryProps> = ({ events }) => {
  const displayEvents: FailureEvent[] = Array.isArray(events) ? events : [];

  return (
    <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm">
      <div className="flex items-center justify-between pb-3 border-b border-slate-100">
        <div className="flex items-center space-x-2">
          <div className="rounded-lg bg-slate-100 p-2 text-slate-700">
            <ScrollText className="h-4 w-4" />
          </div>
          <div>
            <h3 className="text-base font-bold text-slate-900">Failure &amp; Chaos Audit History</h3>
            <p className="text-xs text-slate-500">
              Audit-backed ledger of failure detection, chaos drills, and recovery reconciliation
            </p>
          </div>
        </div>
        <span className="text-xs text-slate-500 font-mono">
          Showing {displayEvents.length} events
        </span>
      </div>

      <div className="mt-4 overflow-x-auto">
        {displayEvents.length === 0 ? (
          <div className="rounded-lg border border-slate-100 bg-slate-50/70 p-8 text-center">
            <CheckCircle className="mx-auto h-7 w-7 text-emerald-500 mb-2" />
            <p className="text-xs font-semibold text-slate-800">No Failure or Rejection Events Recorded</p>
            <p className="text-[11px] text-slate-500 mt-1">
              Live rejected or failure events persisted in PostgreSQL audit_events will appear here automatically.
            </p>
          </div>
        ) : (
        <table className="w-full text-left text-xs">
          <thead className="bg-slate-50 text-[11px] font-semibold uppercase tracking-wider text-slate-500">
            <tr>
              <th className="px-3 py-2 rounded-l-lg">Event Type</th>
              <th className="px-3 py-2">Category</th>
              <th className="px-3 py-2">Target / Resource</th>
              <th className="px-3 py-2">Node / Actor</th>
              <th className="px-3 py-2">Status</th>
              <th className="px-3 py-2 rounded-r-lg">Recorded At</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100">
            {displayEvents.map((evt) => {
              const isRejected =
                evt.operation_status.includes('REJECTED') ||
                evt.operation_status.includes('FAIL');
              return (
                <tr key={evt.event_id} className="hover:bg-slate-50/60">
                  <td className="px-3 py-2.5 font-mono font-medium text-slate-900">
                    <div className="flex items-center space-x-1.5">
                      {isRejected ? (
                        <ShieldAlert className="h-3.5 w-3.5 text-rose-500 shrink-0" />
                      ) : (
                        <CheckCircle className="h-3.5 w-3.5 text-emerald-500 shrink-0" />
                      )}
                      <span>{evt.event_type}</span>
                    </div>
                  </td>
                  <td className="px-3 py-2.5 text-slate-500 font-mono text-[11px]">
                    {evt.event_category}
                  </td>
                  <td className="px-3 py-2.5 font-mono text-slate-700">
                    {evt.resource_id || '—'}
                  </td>
                  <td className="px-3 py-2.5 font-mono text-slate-600">
                    {evt.owner_id || '—'}
                  </td>
                  <td className="px-3 py-2.5">
                    <span
                      className={`inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-semibold ${
                        isRejected
                          ? 'bg-rose-50 text-rose-700'
                          : 'bg-emerald-50 text-emerald-700'
                      }`}
                    >
                      {evt.operation_status}
                    </span>
                  </td>
                  <td className="px-3 py-2.5 text-slate-400 font-mono text-[11px]">
                    <div className="flex items-center space-x-1">
                      <Clock className="h-3 w-3" />
                      <span>{new Date(evt.recorded_at).toLocaleTimeString()}</span>
                    </div>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
        )}
      </div>
    </div>
  );
};
