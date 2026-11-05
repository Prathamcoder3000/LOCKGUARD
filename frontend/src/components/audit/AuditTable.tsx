import React from 'react';
import { Eye, ChevronLeft, ChevronRight, Inbox, Clock } from 'lucide-react';
import { AuditEvent } from '../../services/auditService';
import { AuditStatusBadge } from './AuditStatusBadge';

interface AuditTableProps {
  events: AuditEvent[];
  total: number;
  limit: number;
  offset: number;
  onPageChange: (newOffset: number) => void;
  onSelectEvent: (event: AuditEvent) => void;
  isLoading: boolean;
}

export const AuditTable: React.FC<AuditTableProps> = ({
  events,
  total,
  limit,
  offset,
  onPageChange,
  onSelectEvent,
  isLoading,
}) => {
  const currentPage = Math.floor(offset / limit) + 1;
  const totalPages = Math.max(1, Math.ceil(total / limit));

  if (isLoading) {
    return (
      <div className="bg-white rounded-xl border border-slate-200 p-12 text-center shadow-sm">
        <div className="inline-block animate-spin rounded-full h-8 w-8 border-4 border-indigo-600 border-t-transparent mb-3" />
        <p className="text-sm text-slate-500">Querying PostgreSQL audit history...</p>
      </div>
    );
  }

  if (events.length === 0) {
    return (
      <div className="bg-white rounded-xl border border-slate-200 p-12 text-center shadow-sm">
        <Inbox className="mx-auto h-12 w-12 text-slate-300 mb-3" />
        <h4 className="text-base font-semibold text-slate-800">No Audit Events Found</h4>
        <p className="text-xs text-slate-500 max-w-sm mx-auto mt-1">
          No records match your active query filters. Modify or reset your filter criteria.
        </p>
      </div>
    );
  }

  return (
    <div className="bg-white rounded-xl border border-slate-200 shadow-sm overflow-hidden flex flex-col">
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-slate-200 text-left text-xs">
          <thead className="bg-slate-50 font-semibold text-slate-600 uppercase tracking-wider">
            <tr>
              <th className="px-4 py-3">Timestamp</th>
              <th className="px-4 py-3">Event Type</th>
              <th className="px-4 py-3">Resource</th>
              <th className="px-4 py-3">Owner / Client</th>
              <th className="px-4 py-3">Fencing Token</th>
              <th className="px-4 py-3">Lease Version</th>
              <th className="px-4 py-3">Status</th>
              <th className="px-4 py-3 text-right">Actions</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100 bg-white">
            {events.map((ev) => (
              <tr key={ev.event_id || ev.id} className="hover:bg-slate-50/80 transition-colors">
                <td className="px-4 py-3 whitespace-nowrap text-slate-600 font-mono text-[11px]">
                  <div className="flex items-center">
                    <Clock className="w-3.5 h-3.5 mr-1.5 text-slate-400" />
                    {new Date(ev.event_timestamp).toLocaleTimeString()}
                  </div>
                  <div className="text-[10px] text-slate-400 pl-5">
                    {new Date(ev.event_timestamp).toLocaleDateString()}
                  </div>
                </td>
                <td className="px-4 py-3 whitespace-nowrap">
                  <span className="font-semibold text-slate-800 font-mono">{ev.event_type}</span>
                  <span className="block text-[10px] text-slate-400">{ev.event_category}</span>
                </td>
                <td className="px-4 py-3 whitespace-nowrap font-medium text-slate-900">
                  {ev.resource_id || <span className="text-slate-400 italic">&lt;system&gt;</span>}
                </td>
                <td className="px-4 py-3 whitespace-nowrap font-mono text-slate-600 text-[11px]">
                  {ev.owner_id || ev.client_id || <span className="text-slate-400 italic">&lt;none&gt;</span>}
                </td>
                <td className="px-4 py-3 whitespace-nowrap font-mono font-semibold text-indigo-700">
                  {ev.fencing_token > 0 ? ev.fencing_token : '-'}
                </td>
                <td className="px-4 py-3 whitespace-nowrap font-mono text-slate-700">
                  {ev.lease_version > 0 ? `v${ev.lease_version}` : '-'}
                </td>
                <td className="px-4 py-3 whitespace-nowrap">
                  <AuditStatusBadge status={ev.operation_status} />
                </td>
                <td className="px-4 py-3 whitespace-nowrap text-right">
                  <button
                    onClick={() => onSelectEvent(ev)}
                    className="inline-flex items-center px-2 py-1 text-xs font-medium text-indigo-600 hover:text-indigo-900 hover:bg-indigo-50 rounded transition-colors"
                  >
                    <Eye className="w-3.5 h-3.5 mr-1" />
                    Inspect
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {/* Pagination Footer */}
      <div className="px-4 py-3 bg-slate-50 border-t border-slate-200 flex items-center justify-between text-xs text-slate-600">
        <div>
          Showing <span className="font-semibold">{offset + 1}</span> to{' '}
          <span className="font-semibold">{Math.min(offset + limit, total)}</span> of{' '}
          <span className="font-semibold">{total}</span> events
        </div>

        <div className="flex items-center space-x-2">
          <button
            onClick={() => onPageChange(Math.max(0, offset - limit))}
            disabled={offset === 0}
            className="p-1 rounded border border-slate-300 hover:bg-white disabled:opacity-40 disabled:hover:bg-transparent"
          >
            <ChevronLeft className="w-4 h-4" />
          </button>
          <span>
            Page <span className="font-semibold">{currentPage}</span> of{' '}
            <span className="font-semibold">{totalPages}</span>
          </span>
          <button
            onClick={() => onPageChange(offset + limit)}
            disabled={offset + limit >= total}
            className="p-1 rounded border border-slate-300 hover:bg-white disabled:opacity-40 disabled:hover:bg-transparent"
          >
            <ChevronRight className="w-4 h-4" />
          </button>
        </div>
      </div>
    </div>
  );
};

