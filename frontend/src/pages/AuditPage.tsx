import React from 'react';
import { ScrollText, Database } from 'lucide-react';

export const AuditPage: React.FC = () => {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight text-slate-900">Audit &amp; Event History</h1>
        <p className="text-sm text-slate-500">
          Immutable historical audit logs persisted to PostgreSQL for governance and compliance
        </p>
      </div>

      <div className="rounded-xl border border-slate-200 bg-white p-6 shadow-card">
        <div className="flex items-center space-x-3 text-slate-600 mb-4">
          <div className="rounded-lg bg-indigo-50 p-2 text-indigo-600">
            <ScrollText className="h-5 w-5" />
          </div>
          <div>
            <h2 className="text-base font-semibold text-slate-900">PostgreSQL Audit Trail</h2>
            <p className="text-xs text-slate-500">Separation of concerns: Audit queries do not impact Raft throughput</p>
          </div>
        </div>

        <div className="rounded-lg border border-dashed border-slate-200 bg-slate-50 p-12 text-center">
          <Database className="mx-auto h-10 w-10 text-slate-400 mb-3" />
          <h3 className="text-sm font-semibold text-slate-800">Database Schema Prepared</h3>
          <p className="mt-1 max-w-md mx-auto text-xs text-slate-500">
            Initial migration `000001_initial_schema.up.sql` defines the `audit_events` table with indexation on resource names, actor identities, and timestamps.
          </p>
        </div>
      </div>
    </div>
  );
};
