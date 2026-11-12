import React from 'react';
import { Search, Filter, RotateCcw } from 'lucide-react';
import { AuditFilter } from '../../services/auditService';

interface AuditFiltersProps {
  filters: AuditFilter;
  onChange: (filters: AuditFilter) => void;
  onReset: () => void;
  isLoading?: boolean;
}

export const AuditFilters: React.FC<AuditFiltersProps> = ({
  filters,
  onChange,
  onReset,
  isLoading,
}) => {
  const handleInputChange = (field: keyof AuditFilter, value: string) => {
    onChange({
      ...filters,
      [field]: value.trim() === '' ? undefined : value,
      offset: 0, // Reset to page 1 on filter modification
    });
  };

  return (
    <div className="bg-white rounded-xl border border-slate-200 p-4 shadow-sm mb-6">
      <div className="flex items-center justify-between mb-3 border-b border-slate-100 pb-2">
        <div className="flex items-center space-x-2 text-slate-700 font-medium text-sm">
          <Filter className="w-4 h-4 text-indigo-600" />
          <span>Filter Audit Records</span>
        </div>
        <button
          onClick={onReset}
          disabled={isLoading}
          className="inline-flex items-center text-xs text-slate-500 hover:text-slate-800 transition-colors disabled:opacity-50"
        >
          <RotateCcw className="w-3.5 h-3.5 mr-1" />
          Reset Filters
        </button>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-5 gap-3">
        {/* Resource ID */}
        <div>
          <label className="block text-xs font-semibold text-slate-600 mb-1">Resource ID</label>
          <div className="relative">
            <input
              type="text"
              placeholder="e.g. order-processing"
              value={filters.resource_id || ''}
              onChange={(e) => handleInputChange('resource_id', e.target.value)}
              className="w-full text-xs rounded-lg border border-slate-300 px-3 py-2 pl-8 focus:outline-none focus:ring-2 focus:ring-indigo-500"
            />
            <Search className="w-3.5 h-3.5 text-slate-400 absolute left-2.5 top-2.5" />
          </div>
        </div>

        {/* Client ID / Owner ID */}
        <div>
          <label className="block text-xs font-semibold text-slate-600 mb-1">Owner / Client ID</label>
          <input
            type="text"
            placeholder="e.g. worker-node-1"
            value={filters.client_id || filters.owner_id || ''}
            onChange={(e) => {
              const val = e.target.value;
              onChange({
                ...filters,
                client_id: val.trim() === '' ? undefined : val,
                owner_id: val.trim() === '' ? undefined : val,
                offset: 0,
              });
            }}
            className="w-full text-xs rounded-lg border border-slate-300 px-3 py-2 focus:outline-none focus:ring-2 focus:ring-indigo-500"
          />
        </div>

        {/* Event Type */}
        <div>
          <label className="block text-xs font-semibold text-slate-600 mb-1">Event Type</label>
          <select
            value={filters.event_type || ''}
            onChange={(e) => handleInputChange('event_type', e.target.value)}
            className="w-full text-xs rounded-lg border border-slate-300 px-3 py-2 bg-white focus:outline-none focus:ring-2 focus:ring-indigo-500"
          >
            <option value="">All Event Types</option>
            <option value="LOCK_ACQUIRE">LOCK_ACQUIRE</option>
            <option value="LOCK_RENEW">LOCK_RENEW</option>
            <option value="LOCK_RELEASE">LOCK_RELEASE</option>
            <option value="LOCK_EXPIRE">LOCK_EXPIRE</option>
            <option value="CLIENT_REGISTER">CLIENT_REGISTER</option>
            <option value="CLIENT_HEARTBEAT">CLIENT_HEARTBEAT</option>
            <option value="SESSION_EXPIRED">SESSION_EXPIRED</option>
            <option value="RENEWAL_STARTED">RENEWAL_STARTED</option>
            <option value="RENEWAL_STOPPED">RENEWAL_STOPPED</option>
            <option value="AUTHENTICATION_FAILURE">AUTHENTICATION_FAILURE</option>
            <option value="AUTHORIZATION_DENIAL">AUTHORIZATION_DENIAL</option>
            <option value="ADMIN_STEPDOWN">ADMIN_STEPDOWN</option>
            <option value="ADMIN_EMERGENCY_UNLOCK">ADMIN_EMERGENCY_UNLOCK</option>
            <option value="LEADER_CHANGE">LEADER_CHANGE</option>
            <option value="QUORUM_LOSS">QUORUM_LOSS</option>
            <option value="NODE_RECOVERED">NODE_RECOVERED</option>
            <option value="CHAOS_TEST_EXECUTED">CHAOS_TEST_EXECUTED</option>
            <option value="NETWORK_PARTITION_INJECTED">NETWORK_PARTITION_INJECTED</option>
            <option value="NETWORK_PARTITION_HEALED">NETWORK_PARTITION_HEALED</option>
            <option value="SNAPSHOT_RESTORED">SNAPSHOT_RESTORED</option>
            <option value="BENCHMARK_EXECUTED">BENCHMARK_EXECUTED</option>
            <option value="SYSTEM_STARTUP">SYSTEM_STARTUP</option>
            <option value="SYSTEM_SHUTDOWN">SYSTEM_SHUTDOWN</option>
          </select>
        </div>

        {/* Operation Status */}
        <div>
          <label className="block text-xs font-semibold text-slate-600 mb-1">Status</label>
          <select
            value={filters.operation_status || ''}
            onChange={(e) => handleInputChange('operation_status', e.target.value)}
            className="w-full text-xs rounded-lg border border-slate-300 px-3 py-2 bg-white focus:outline-none focus:ring-2 focus:ring-indigo-500"
          >
            <option value="">All Statuses</option>
            <option value="COMMITTED">COMMITTED (Raft Consensus)</option>
            <option value="REJECTED">REJECTED (Lock Conflict / Guard)</option>
            <option value="FAILED">FAILED (Quorum Loss / Outage)</option>
            <option value="PENDING">PENDING</option>
          </select>
        </div>

        {/* Request ID */}
        <div>
          <label className="block text-xs font-semibold text-slate-600 mb-1">Request ID</label>
          <input
            type="text"
            placeholder="Trace request ID"
            value={filters.request_id || ''}
            onChange={(e) => handleInputChange('request_id', e.target.value)}
            className="w-full text-xs rounded-lg border border-slate-300 px-3 py-2 focus:outline-none focus:ring-2 focus:ring-indigo-500"
          />
        </div>
      </div>
    </div>
  );
};

