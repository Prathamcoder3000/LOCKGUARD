import React, { useState, useEffect, useCallback } from 'react';
import { Database, ShieldCheck, Activity, RefreshCw, Layers, AlertTriangle } from 'lucide-react';
import {
  auditService,
  AuditEvent,
  AuditFilter,
  AuditHealthStatus,
  DiagnosticsReport,
} from '../services/auditService';
import { AuditFilters } from '../components/audit/AuditFilters';
import { AuditTable } from '../components/audit/AuditTable';
import { AuditEventDetails } from '../components/audit/AuditEventDetails';
import { AuditStatusBadge } from '../components/audit/AuditStatusBadge';

export const AuditPage: React.FC = () => {
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [total, setTotal] = useState<number>(0);
  const [filters, setFilters] = useState<AuditFilter>({ limit: 50, offset: 0 });
  const [selectedEvent, setSelectedEvent] = useState<AuditEvent | null>(null);

  const [auditHealth, setAuditHealth] = useState<AuditHealthStatus | null>(null);
  const [diagnostics, setDiagnostics] = useState<DiagnosticsReport | null>(null);

  const [isLoading, setIsLoading] = useState<boolean>(true);
  const [error, setError] = useState<string | null>(null);
  const [autoRefresh, setAutoRefresh] = useState<boolean>(true);

  const loadData = useCallback(async () => {
    try {
      setError(null);
      const [eventsRes, healthRes, diagRes] = await Promise.allSettled([
        auditService.getEvents(filters),
        auditService.getHealth(),
        auditService.getDiagnostics(),
      ]);

      if (eventsRes.status === 'fulfilled') {
        setEvents(eventsRes.value.events || []);
        setTotal(eventsRes.value.total || 0);
      } else {
        setError('Failed to query PostgreSQL audit trail. Storage may be offline or initializing.');
      }

      if (healthRes.status === 'fulfilled') {
        setAuditHealth(healthRes.value);
      }

      if (diagRes.status === 'fulfilled') {
        setDiagnostics(diagRes.value);
      }
    } catch (err: any) {
      setError(err?.message || 'Error communicating with audit service');
    } finally {
      setIsLoading(false);
    }
  }, [filters]);

  useEffect(() => {
    loadData();
  }, [loadData]);

  // Periodic polling when auto-refresh is active
  useEffect(() => {
    if (!autoRefresh) return;
    const interval = setInterval(() => {
      loadData();
    }, 10000);
    return () => clearInterval(interval);
  }, [autoRefresh, loadData]);

  const handleResetFilters = () => {
    setFilters({ limit: 50, offset: 0 });
  };

  const handlePageChange = (newOffset: number) => {
    setFilters((prev) => ({ ...prev, offset: newOffset }));
  };

  return (
    <div className="space-y-6">
      {/* Page Title & Controls */}
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-slate-900">Operational Audit Trail</h1>
          <p className="text-xs text-slate-500 mt-0.5">
            Append-only, PostgreSQL-backed operational event logs decoupled from Raft consensus throughput
          </p>
        </div>

        <div className="flex items-center space-x-3">
          <label className="flex items-center text-xs text-slate-600 cursor-pointer select-none">
            <input
              type="checkbox"
              checked={autoRefresh}
              onChange={(e) => setAutoRefresh(e.target.checked)}
              className="rounded border-slate-300 text-indigo-600 focus:ring-indigo-500 mr-2"
            />
            Auto-refresh (10s)
          </label>

          <button
            onClick={() => {
              setIsLoading(true);
              loadData();
            }}
            disabled={isLoading}
            className="inline-flex items-center px-3 py-2 text-xs font-semibold rounded-lg bg-indigo-600 text-white hover:bg-indigo-700 transition-colors shadow-sm disabled:opacity-50"
          >
            <RefreshCw className={`w-3.5 h-3.5 mr-1.5 ${isLoading ? 'animate-spin' : ''}`} />
            Refresh
          </button>
        </div>
      </div>

      {/* Error Alert */}
      {error && (
        <div className="p-4 bg-rose-50 border border-rose-200 rounded-xl flex items-center space-x-3 text-rose-800 text-xs">
          <AlertTriangle className="w-5 h-5 text-rose-600 flex-shrink-0" />
          <div>
            <span className="font-semibold block">Audit Subsystem Warning</span>
            <span>{error}</span>
          </div>
        </div>
      )}

      {/* Operational Health Cards */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
        {/* Raft Quorum Card */}
        <div className="bg-white rounded-xl border border-slate-200 p-4 shadow-sm">
          <div className="flex items-center justify-between mb-2">
            <span className="text-xs font-medium text-slate-500 uppercase tracking-wider">Raft Authority</span>
            <ShieldCheck className="w-4 h-4 text-indigo-600" />
          </div>
          <div className="flex items-baseline justify-between">
            <div className="text-lg font-bold text-slate-900">
              {diagnostics?.raft_quorum_available ? 'Quorum Active' : 'Quorum Lost'}
            </div>
            <AuditStatusBadge status={diagnostics?.raft_quorum_available ? 'HEALTHY' : 'UNHEALTHY'} />
          </div>
          <p className="text-[11px] text-slate-400 mt-1">
            Sole authority for lock ownership &amp; versioning
          </p>
        </div>

        {/* PostgreSQL Persistence Card */}
        <div className="bg-white rounded-xl border border-slate-200 p-4 shadow-sm">
          <div className="flex items-center justify-between mb-2">
            <span className="text-xs font-medium text-slate-500 uppercase tracking-wider">PostgreSQL Storage</span>
            <Database className="w-4 h-4 text-emerald-600" />
          </div>
          <div className="flex items-baseline justify-between">
            <div className="text-lg font-bold text-slate-900">
              {auditHealth?.db_attached ? 'Connected' : 'Unavailable'}
            </div>
            <AuditStatusBadge status={auditHealth?.db_attached ? 'UP' : 'DOWN'} />
          </div>
          <p className="text-[11px] text-slate-400 mt-1">
            Persisted: {auditHealth?.stats.persisted_total ?? 0} events
          </p>
        </div>

        {/* Audit Pipeline Buffer Card */}
        <div className="bg-white rounded-xl border border-slate-200 p-4 shadow-sm">
          <div className="flex items-center justify-between mb-2">
            <span className="text-xs font-medium text-slate-500 uppercase tracking-wider">Audit Queue Buffer</span>
            <Layers className="w-4 h-4 text-amber-600" />
          </div>
          <div className="flex items-baseline justify-between">
            <div className="text-lg font-bold text-slate-900 font-mono">
              {auditHealth?.stats.queue_size ?? 0} / {auditHealth?.stats.queue_capacity ?? 10000}
            </div>
            <span className="text-xs font-semibold text-slate-500">
              {auditHealth?.stats.failed_total ?? 0} dropped
            </span>
          </div>
          <p className="text-[11px] text-slate-400 mt-1">
            Non-blocking async delivery with backpressure
          </p>
        </div>

        {/* Total Events Indexed */}
        <div className="bg-white rounded-xl border border-slate-200 p-4 shadow-sm">
          <div className="flex items-center justify-between mb-2">
            <span className="text-xs font-medium text-slate-500 uppercase tracking-wider">Total Matched</span>
            <Activity className="w-4 h-4 text-indigo-600" />
          </div>
          <div className="flex items-baseline justify-between">
            <div className="text-2xl font-bold text-slate-900 font-mono">{total}</div>
            <span className="text-xs text-slate-400">Events</span>
          </div>
          <p className="text-[11px] text-slate-400 mt-1">
            Filtered according to search parameters
          </p>
        </div>
      </div>

      {/* Filter Controls */}
      <AuditFilters
        filters={filters}
        onChange={setFilters}
        onReset={handleResetFilters}
        isLoading={isLoading}
      />

      {/* Audit Event Table */}
      <AuditTable
        events={events}
        total={total}
        limit={filters.limit ?? 50}
        offset={filters.offset ?? 0}
        onPageChange={handlePageChange}
        onSelectEvent={setSelectedEvent}
        isLoading={isLoading}
      />

      {/* Event Details Drawer/Modal */}
      <AuditEventDetails event={selectedEvent} onClose={() => setSelectedEvent(null)} />
    </div>
  );
};

export default AuditPage;
