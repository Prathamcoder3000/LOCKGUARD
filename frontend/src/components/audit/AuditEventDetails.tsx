import React from 'react';
import { X, ShieldCheck, Database, Server, Clock, FileText } from 'lucide-react';
import { AuditEvent } from '../../services/auditService';
import { AuditStatusBadge } from './AuditStatusBadge';

interface AuditEventDetailsProps {
  event: AuditEvent | null;
  onClose: () => void;
}

export const AuditEventDetails: React.FC<AuditEventDetailsProps> = ({ event, onClose }) => {
  if (!event) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/50 backdrop-blur-sm p-4">
      <div className="bg-white rounded-xl shadow-2xl max-w-2xl w-full max-h-[90vh] flex flex-col overflow-hidden animate-in fade-in zoom-in-95 duration-150">
        {/* Header */}
        <div className="flex items-center justify-between px-6 py-4 border-b border-slate-100 bg-slate-50">
          <div className="flex items-center space-x-2">
            <ShieldCheck className="w-5 h-5 text-indigo-600" />
            <h3 className="font-semibold text-slate-900 text-base">Audit Record Inspection</h3>
          </div>
          <button
            onClick={onClose}
            className="text-slate-400 hover:text-slate-600 p-1 rounded-lg hover:bg-slate-200 transition-colors"
          >
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* Content */}
        <div className="p-6 overflow-y-auto space-y-6 text-sm">
          {/* Top Status and Type */}
          <div className="flex items-center justify-between p-4 bg-slate-50 rounded-lg border border-slate-200">
            <div>
              <span className="text-xs text-slate-500 block uppercase font-mono">Event Type</span>
              <span className="font-bold text-slate-900 text-base">{event.event_type}</span>
            </div>
            <div className="text-right">
              <span className="text-xs text-slate-500 block uppercase font-mono">Consensus State</span>
              <AuditStatusBadge status={event.operation_status} />
            </div>
          </div>

          {/* Grid attributes */}
          <div className="grid grid-cols-2 gap-4">
            <div>
              <span className="text-xs text-slate-500 flex items-center mb-1">
                <FileText className="w-3.5 h-3.5 mr-1 text-slate-400" /> Event ID (Unique)
              </span>
              <p className="font-mono text-xs text-slate-800 break-all bg-slate-100 p-2 rounded border border-slate-200">
                {event.event_id}
              </p>
            </div>

            <div>
              <span className="text-xs text-slate-500 flex items-center mb-1">
                <FileText className="w-3.5 h-3.5 mr-1 text-slate-400" /> Request ID
              </span>
              <p className="font-mono text-xs text-slate-800 break-all bg-slate-100 p-2 rounded border border-slate-200">
                {event.request_id || '<none>'}
              </p>
            </div>

            <div>
              <span className="text-xs text-slate-500 block mb-1">Resource ID</span>
              <p className="font-semibold text-slate-900">{event.resource_id || '<system>'}</p>
            </div>

            <div>
              <span className="text-xs text-slate-500 block mb-1">Owner / Client ID</span>
              <p className="font-mono text-xs text-slate-800">{event.owner_id || event.client_id || '<unassigned>'}</p>
            </div>

            <div>
              <span className="text-xs text-slate-500 block mb-1">Fencing Token</span>
              <p className="font-mono text-xs font-semibold text-indigo-700">
                {event.fencing_token > 0 ? event.fencing_token : '<none>'}
              </p>
            </div>

            <div>
              <span className="text-xs text-slate-500 block mb-1">Lease Version</span>
              <p className="font-mono text-xs font-semibold text-slate-800">
                {event.lease_version > 0 ? `v${event.lease_version}` : '<none>'}
              </p>
            </div>

            <div>
              <span className="text-xs text-slate-500 flex items-center mb-1">
                <Server className="w-3.5 h-3.5 mr-1 text-slate-400" /> Coordinator Node
              </span>
              <p className="font-medium text-slate-800">{event.coordinator_id || '<unknown>'}</p>
            </div>

            <div>
              <span className="text-xs text-slate-500 flex items-center mb-1">
                <Database className="w-3.5 h-3.5 mr-1 text-slate-400" /> Raft Log Index
              </span>
              <p className="font-mono text-xs text-slate-800">{event.raft_index > 0 ? event.raft_index : '<uncommitted>'}</p>
            </div>

            <div>
              <span className="text-xs text-slate-500 flex items-center mb-1">
                <Clock className="w-3.5 h-3.5 mr-1 text-slate-400" /> Event Timestamp
              </span>
              <p className="text-xs text-slate-700">{new Date(event.event_timestamp).toLocaleString()}</p>
            </div>

            <div>
              <span className="text-xs text-slate-500 flex items-center mb-1">
                <Clock className="w-3.5 h-3.5 mr-1 text-slate-400" /> PostgreSQL Persistence Time
              </span>
              <p className="text-xs text-slate-700">{new Date(event.recorded_at).toLocaleString()}</p>
            </div>
          </div>

          {/* Error code if rejected/failed */}
          {event.error_code && (
            <div className="p-3 bg-rose-50 border border-rose-200 rounded-lg">
              <span className="text-xs font-semibold text-rose-800 uppercase block">Error Classification</span>
              <p className="text-xs text-rose-900 font-mono mt-0.5">{event.error_code}</p>
            </div>
          )}

          {/* Metadata JSON */}
          <div>
            <span className="text-xs font-semibold text-slate-600 block mb-1">Sanitized Metadata (Secrets Redacted)</span>
            <pre className="text-xs font-mono bg-slate-900 text-slate-100 p-3 rounded-lg overflow-x-auto max-h-40">
              {JSON.stringify(event.metadata || {}, null, 2)}
            </pre>
          </div>
        </div>

        {/* Footer */}
        <div className="px-6 py-3 border-t border-slate-100 bg-slate-50 flex justify-end">
          <button
            onClick={onClose}
            className="px-4 py-2 text-xs font-medium bg-slate-200 text-slate-700 hover:bg-slate-300 rounded-lg transition-colors"
          >
            Close
          </button>
        </div>
      </div>
    </div>
  );
};

