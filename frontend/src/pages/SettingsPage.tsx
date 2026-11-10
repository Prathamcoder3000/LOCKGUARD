import React, { useState } from 'react';
import { Shield, Server, KeyRound, AlertTriangle, CheckCircle2, ArrowRightLeft, Lock } from 'lucide-react';
import { useAuth } from '../context/AuthContext';
import { clusterService } from '../services/clusterService';

export const SettingsPage: React.FC = () => {
  const { user } = useAuth();
  const isAdmin = (user?.role || '').toUpperCase() === 'ADMIN';

  const [emergencyResource, setEmergencyResource] = useState('');
  const [loading, setLoading] = useState(false);
  const [actionMessage, setActionMessage] = useState<{ type: 'success' | 'error'; text: string } | null>(null);

  const handleEmergencyUnlock = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!emergencyResource.trim()) return;
    if (!confirm(`Force release active lock on resource "${emergencyResource}"?`)) return;

    setLoading(true);
    setActionMessage(null);
    try {
      await clusterService.adminEmergencyUnlock(emergencyResource.trim());
      setActionMessage({
        type: 'success',
        text: `Emergency unlock executed for "${emergencyResource}". Lock released on Raft state machine.`,
      });
      setEmergencyResource('');
    } catch (err: any) {
      setActionMessage({
        type: 'error',
        text: err?.response?.data?.error?.message || err?.message || 'Emergency unlock failed',
      });
    } finally {
      setLoading(false);
    }
  };

  const handleStepdown = async () => {
    if (!confirm('Initiate Raft leadership transfer on the current leader?')) return;
    setLoading(true);
    setActionMessage(null);
    try {
      const res = await clusterService.adminStepdown();
      setActionMessage({
        type: 'success',
        text: res.message || 'Leadership stepdown initiated successfully.',
      });
    } catch (err: any) {
      setActionMessage({
        type: 'error',
        text: err?.response?.data?.error?.message || err?.message || 'Stepdown failed',
      });
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="space-y-6 max-w-5xl">
      {/* Title */}
      <div>
        <h1 className="text-2xl font-bold tracking-tight text-slate-900">System Settings &amp; Administration</h1>
        <p className="text-sm text-slate-500">
          Cluster configuration, role-based authorization parameters, and administrative controls
        </p>
      </div>

      {actionMessage && (
        <div
          className={`rounded-lg p-3.5 text-xs font-medium border flex items-center space-x-2.5 ${
            actionMessage.type === 'success'
              ? 'bg-emerald-50 border-emerald-200 text-emerald-800'
              : 'bg-rose-50 border-rose-200 text-rose-800'
          }`}
        >
          {actionMessage.type === 'success' ? (
            <CheckCircle2 className="h-4 w-4 shrink-0 text-emerald-600" />
          ) : (
            <AlertTriangle className="h-4 w-4 shrink-0 text-rose-600" />
          )}
          <span>{actionMessage.text}</span>
        </div>
      )}

      {/* Authenticated User Session Card */}
      <div className="rounded-xl border border-slate-200 bg-white p-6 shadow-card">
        <div className="flex items-center space-x-2.5 border-b border-slate-100 pb-3">
          <div className="rounded-lg bg-blue-50 p-2 text-blue-600">
            <Shield className="h-5 w-5" />
          </div>
          <div>
            <h2 className="text-sm font-bold text-slate-900">Current Session &amp; Permissions</h2>
            <p className="text-xs text-slate-500">Identity established via JWT HS256 constant-time verification</p>
          </div>
        </div>

        <div className="mt-4 grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4 gap-4 text-xs">
          <div className="p-3 rounded-lg bg-slate-50 border border-slate-100">
            <span className="text-slate-400">Authenticated User</span>
            <p className="mt-1 font-semibold text-slate-900 font-mono">{user?.username || 'user'}</p>
          </div>

          <div className="p-3 rounded-lg bg-slate-50 border border-slate-100">
            <span className="text-slate-400">Assigned Role</span>
            <p className="mt-1 font-bold text-blue-600 capitalize">{user?.role || 'CLIENT'}</p>
          </div>

          <div className="p-3 rounded-lg bg-slate-50 border border-slate-100">
            <span className="text-slate-400">Bound Owner ID</span>
            <p className="mt-1 font-semibold text-slate-900 font-mono">{user?.owner_id || 'System'}</p>
          </div>

          <div className="p-3 rounded-lg bg-slate-50 border border-slate-100">
            <span className="text-slate-400">Access Scope</span>
            <p className="mt-1 font-semibold text-emerald-700">
              {isAdmin ? 'Full Administrative Access' : 'Client Operations & Permitted Records'}
            </p>
          </div>
        </div>
      </div>

      {/* Admin Specific Operations */}
      {isAdmin ? (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
          {/* Emergency Unlock */}
          <div className="rounded-xl border border-slate-200 bg-white p-6 shadow-card">
            <div className="flex items-center space-x-2 border-b border-slate-100 pb-3">
              <Lock className="h-5 w-5 text-rose-600" />
              <h2 className="text-sm font-bold text-slate-900">Emergency Lock Break</h2>
            </div>
            <p className="mt-2 text-xs text-slate-500">
              Administratively release an active lease on the Raft state machine regardless of owner client identity.
            </p>

            <form onSubmit={handleEmergencyUnlock} className="mt-4 space-y-3">
              <div>
                <label className="block text-xs font-medium text-slate-700">Resource Name</label>
                <input
                  type="text"
                  required
                  placeholder="e.g. production-database-lock"
                  value={emergencyResource}
                  onChange={(e) => setEmergencyResource(e.target.value)}
                  className="mt-1 w-full rounded-lg border border-slate-300 px-3 py-2 text-xs focus:border-rose-500 focus:outline-none focus:ring-1 focus:ring-rose-500"
                />
              </div>
              <button
                type="submit"
                disabled={loading || !emergencyResource.trim()}
                className="w-full rounded-lg bg-rose-600 px-4 py-2 text-xs font-semibold text-white shadow-sm hover:bg-rose-700 disabled:opacity-50"
              >
                {loading ? 'Releasing...' : 'Execute Emergency Unlock'}
              </button>
            </form>
          </div>

          {/* Leader Stepdown */}
          <div className="rounded-xl border border-slate-200 bg-white p-6 shadow-card">
            <div className="flex items-center space-x-2 border-b border-slate-100 pb-3">
              <ArrowRightLeft className="h-5 w-5 text-amber-600" />
              <h2 className="text-sm font-bold text-slate-900">Raft Leader Stepdown</h2>
            </div>
            <p className="mt-2 text-xs text-slate-500">
              Gracefully step down the current Raft leader coordinator and initiate an immediate peer re-election.
            </p>

            <div className="mt-6">
              <button
                type="button"
                onClick={handleStepdown}
                disabled={loading}
                className="w-full rounded-lg border border-amber-300 bg-amber-50 px-4 py-2 text-xs font-semibold text-amber-800 shadow-sm hover:bg-amber-100 disabled:opacity-50"
              >
                {loading ? 'Initiating...' : 'Trigger Leadership Transfer'}
              </button>
            </div>
          </div>
        </div>
      ) : (
        <div className="rounded-xl border border-blue-100 bg-blue-50/60 p-5 shadow-card">
          <div className="flex items-start space-x-3">
            <KeyRound className="h-5 w-5 text-blue-600 shrink-0 mt-0.5" />
            <div className="text-xs">
              <p className="font-semibold text-blue-900">Restricted Administrative Controls</p>
              <p className="mt-1 text-blue-700 leading-relaxed">
                Emergency unlock and leadership stepdown are restricted to users with the <code>ADMIN</code> role.
                As an Operator (<code>{user?.role}</code>), you can acquire, renew, and release your own locks in{' '}
                <a href="/locks" className="font-semibold underline">
                  Distributed Locks
                </a>.
              </p>
            </div>
          </div>
        </div>
      )}

      {/* Cluster Topology Configuration Parameters (Read-Only) */}
      <div className="rounded-xl border border-slate-200 bg-white p-6 shadow-card">
        <div className="flex items-center space-x-2 border-b border-slate-100 pb-3">
          <Server className="h-5 w-5 text-slate-700" />
          <h2 className="text-sm font-bold text-slate-900">Cluster Architecture Parameters</h2>
        </div>

        <div className="mt-4 grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 gap-4 text-xs">
          <div className="p-3 rounded-lg bg-slate-50 border border-slate-100">
            <span className="text-slate-400">Consensus Engine</span>
            <p className="mt-1 font-semibold text-slate-800">HashiCorp Raft v3</p>
          </div>

          <div className="p-3 rounded-lg bg-slate-50 border border-slate-100">
            <span className="text-slate-400">Quorum Rule</span>
            <p className="mt-1 font-semibold text-slate-800">2 of 3 Nodes (Majority)</p>
          </div>

          <div className="p-3 rounded-lg bg-slate-50 border border-slate-100">
            <span className="text-slate-400">Safety Invariant</span>
            <p className="mt-1 font-semibold text-blue-700">Consistency First • Fail Closed</p>
          </div>

          <div className="p-3 rounded-lg bg-slate-50 border border-slate-100">
            <span className="text-slate-400">Audit Store</span>
            <p className="mt-1 font-semibold text-slate-800">PostgreSQL 16 (Port 5435)</p>
          </div>

          <div className="p-3 rounded-lg bg-slate-50 border border-slate-100">
            <span className="text-slate-400">Protected Resource</span>
            <p className="mt-1 font-semibold text-slate-800">Port 9000 (Fencing Verified)</p>
          </div>

          <div className="p-3 rounded-lg bg-slate-50 border border-slate-100">
            <span className="text-slate-400">JWT Secret &amp; Keys</span>
            <p className="mt-1 font-semibold text-slate-600 font-mono">•••••••••••••••• (Masked)</p>
          </div>
        </div>
      </div>
    </div>
  );
};

