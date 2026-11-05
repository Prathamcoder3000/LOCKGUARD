import React from 'react';
import { Lock, AlertCircle, Plus, Shield } from 'lucide-react';

export const LocksPage: React.FC = () => {
  return (
    <div className="space-y-6">
      <div className="flex flex-col justify-between gap-4 sm:flex-row sm:items-center">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-slate-900">Distributed Lock Manager</h1>
          <p className="text-sm text-slate-500">
            Exclusive resource leasing, fencing tokens, and automatic lease renewal
          </p>
        </div>
        <button
          disabled
          className="inline-flex items-center space-x-2 rounded-lg bg-blue-600 px-4 py-2 text-xs font-semibold text-white opacity-60 cursor-not-allowed shadow-sm"
        >
          <Plus className="h-4 w-4" />
          <span>Acquire Lock (Phase 1)</span>
        </button>
      </div>

      <div className="rounded-xl border border-slate-200 bg-white p-6 shadow-card">
        <div className="flex items-center space-x-3 text-slate-600 mb-4">
          <div className="rounded-lg bg-blue-50 p-2 text-blue-600">
            <Lock className="h-5 w-5" />
          </div>
          <div>
            <h2 className="text-base font-semibold text-slate-900">Active Distributed Leases</h2>
            <p className="text-xs text-slate-500">Authoritative in-memory state machine replicated via Raft</p>
          </div>
        </div>

        <div className="rounded-lg border border-dashed border-slate-200 bg-slate-50 p-12 text-center">
          <Shield className="mx-auto h-10 w-10 text-slate-400 mb-3" />
          <h3 className="text-sm font-semibold text-slate-800">Lock Engine Foundation Initialized</h3>
          <p className="mt-1 max-w-md mx-auto text-xs text-slate-500">
            Actual lock acquisition, lease heartbeat timers, and fencing token generation will be activated in Phase 1 &amp; Phase 2 once the Raft consensus cluster is running.
          </p>
          <div className="mt-4 inline-flex items-center space-x-1.5 rounded-full bg-slate-200/70 px-3 py-1 text-[11px] font-medium text-slate-700">
            <AlertCircle className="h-3.5 w-3.5 text-blue-600" />
            <span>Phase 0: Architecture &amp; Interfaces Ready</span>
          </div>
        </div>
      </div>
    </div>
  );
};

