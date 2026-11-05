import React, { useState, useEffect, useCallback } from 'react';
import {
  Lock,
  Plus,
  Shield,
  Activity,
  RefreshCw,
  Users,
  Repeat,
  AlertTriangle,
  CheckCircle2,
  XCircle,
  Timer,
  KeyRound,
} from 'lucide-react';
import {
  clusterService,
  LockRecord,
  ClientSession,
  RenewalStatus,
} from '../services/clusterService';

export const LocksPage: React.FC = () => {
  const [locks, setLocks] = useState<LockRecord[]>([]);
  const [sessions, setSessions] = useState<ClientSession[]>([]);
  const [renewals, setRenewals] = useState<RenewalStatus[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Form Modals State
  const [showAcquireModal, setShowAcquireModal] = useState(false);
  const [showRegisterModal, setShowRegisterModal] = useState(false);

  // Acquire Lock Form
  const [acquireForm, setAcquireForm] = useState({
    resource_name: '',
    owner_id: '',
    ttl_seconds: 15,
  });

  // Register Client Form
  const [registerForm, setRegisterForm] = useState({
    client_id: '',
    owner_id: '',
    ttl_seconds: 30,
  });

  const fetchData = useCallback(async () => {
    try {
      setError(null);
      const [locksData, sessionsData, renewalsData] = await Promise.all([
        clusterService.getLocks().catch(() => []),
        clusterService.getClientSessions().catch(() => []),
        clusterService.getRenewals().catch(() => []),
      ]);
      setLocks(locksData);
      setSessions(sessionsData);
      setRenewals(renewalsData);
    } catch (err: any) {
      setError(err?.response?.data?.error?.message || err?.message || 'Failed to fetch cluster lock telemetry');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchData();
    const timer = setInterval(fetchData, 3000);
    return () => clearInterval(timer);
  }, [fetchData]);

  const handleAcquire = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await clusterService.acquireLock(acquireForm);
      setShowAcquireModal(false);
      setAcquireForm({ resource_name: '', owner_id: '', ttl_seconds: 15 });
      fetchData();
    } catch (err: any) {
      alert(`Acquire Lock Failed: ${err?.response?.data?.error?.message || err.message}`);
    }
  };

  const handleRelease = async (lock: LockRecord) => {
    if (!confirm(`Release lock on "${lock.resource_name}" held by "${lock.owner_id}"?`)) return;
    try {
      await clusterService.releaseLock({
        resource_name: lock.resource_name,
        owner_id: lock.owner_id,
        fencing_token: lock.fencing_token,
      });
      fetchData();
    } catch (err: any) {
      alert(`Release Failed: ${err?.response?.data?.error?.message || err.message}`);
    }
  };

  const handleRegisterClient = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await clusterService.registerClient(registerForm);
      setShowRegisterModal(false);
      setRegisterForm({ client_id: '', owner_id: '', ttl_seconds: 30 });
      fetchData();
    } catch (err: any) {
      alert(`Client Registration Failed: ${err?.response?.data?.error?.message || err.message}`);
    }
  };

  const handleHeartbeat = async (s: ClientSession) => {
    try {
      await clusterService.sendHeartbeat({ client_id: s.client_id, session_id: s.session_id });
      fetchData();
    } catch (err: any) {
      alert(`Heartbeat Failed: ${err?.response?.data?.error?.message || err.message}`);
    }
  };

  const handleStartRenewal = async (lock: LockRecord) => {
    // Look up an active session for this owner or default client
    const matchingSession = sessions.find((s) => s.owner_id === lock.owner_id && s.active);
    const clientID = matchingSession ? matchingSession.client_id : `client-${lock.owner_id}`;
    const sessionID = matchingSession ? matchingSession.session_id : 'standalone-session';

    try {
      await clusterService.startRenewal({
        resource_name: lock.resource_name,
        owner_id: lock.owner_id,
        client_id: clientID,
        session_id: sessionID,
        fencing_token: lock.fencing_token,
        lease_version: lock.lease_version,
        expires_at_unix_nano: lock.expires_at_unix_nano,
        ttl_seconds: 15,
      });
      fetchData();
    } catch (err: any) {
      alert(`Start Renewal Failed: ${err?.response?.data?.error?.message || err.message}`);
    }
  };

  const handleStopRenewal = async (resource_name: string) => {
    try {
      await clusterService.stopRenewal(resource_name);
      fetchData();
    } catch (err: any) {
      alert(`Stop Renewal Failed: ${err?.response?.data?.error?.message || err.message}`);
    }
  };

  return (
    <div className="space-y-6">
      {/* Top Header */}
      <div className="flex flex-col justify-between gap-4 sm:flex-row sm:items-center">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-slate-900">Distributed Lock Manager</h1>
          <p className="text-sm text-slate-500">
            Phase 2: Transparent Raft Gateway, Heartbeat Sessions &amp; Automated Renewal Workers
          </p>
        </div>
        <div className="flex items-center space-x-2">
          <button
            onClick={() => setShowRegisterModal(true)}
            className="inline-flex items-center space-x-1.5 rounded-lg border border-slate-300 bg-white px-3 py-2 text-xs font-semibold text-slate-700 hover:bg-slate-50 shadow-sm"
          >
            <Users className="h-4 w-4 text-slate-500" />
            <span>Register Session</span>
          </button>
          <button
            onClick={() => setShowAcquireModal(true)}
            className="inline-flex items-center space-x-1.5 rounded-lg bg-blue-600 px-3.5 py-2 text-xs font-semibold text-white hover:bg-blue-700 shadow-sm transition"
          >
            <Plus className="h-4 w-4" />
            <span>Acquire Lock</span>
          </button>
          <button
            onClick={fetchData}
            title="Refresh Telemetry"
            className="rounded-lg border border-slate-200 bg-white p-2 text-slate-600 hover:bg-slate-50"
          >
            <RefreshCw className={`h-4 w-4 ${loading ? 'animate-spin' : ''}`} />
          </button>
        </div>
      </div>

      {error && (
        <div className="flex items-center space-x-2 rounded-lg bg-amber-50 p-4 border border-amber-200 text-amber-800 text-sm">
          <AlertTriangle className="h-5 w-5 flex-shrink-0 text-amber-600" />
          <span>{error}</span>
        </div>
      )}

      {/* KPI Cards */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm">
          <div className="flex items-center justify-between">
            <span className="text-xs font-medium uppercase text-slate-500">Active Leases</span>
            <div className="rounded-lg bg-blue-50 p-2 text-blue-600">
              <Lock className="h-4 w-4" />
            </div>
          </div>
          <p className="mt-2 text-2xl font-bold text-slate-900">{locks.length}</p>
          <p className="mt-1 text-xs text-slate-500">Replicated across Raft quorum</p>
        </div>

        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm">
          <div className="flex items-center justify-between">
            <span className="text-xs font-medium uppercase text-slate-500">Client Sessions</span>
            <div className="rounded-lg bg-emerald-50 p-2 text-emerald-600">
              <Users className="h-4 w-4" />
            </div>
          </div>
          <p className="mt-2 text-2xl font-bold text-slate-900">
            {sessions.filter((s) => s.active).length}
          </p>
          <p className="mt-1 text-xs text-slate-500">Actively sending heartbeats</p>
        </div>

        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm">
          <div className="flex items-center justify-between">
            <span className="text-xs font-medium uppercase text-slate-500">Renewal Workers</span>
            <div className="rounded-lg bg-purple-50 p-2 text-purple-600">
              <Repeat className="h-4 w-4" />
            </div>
          </div>
          <p className="mt-2 text-2xl font-bold text-slate-900">
            {renewals.filter((r) => r.active).length}
          </p>
          <p className="mt-1 text-xs text-slate-500">Automated deadline schedulers</p>
        </div>

        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm">
          <div className="flex items-center justify-between">
            <span className="text-xs font-medium uppercase text-slate-500">Gateway Routing</span>
            <div className="rounded-lg bg-indigo-50 p-2 text-indigo-600">
              <Activity className="h-4 w-4" />
            </div>
          </div>
          <p className="mt-2 text-sm font-semibold text-slate-900">Transparent 421 Redir</p>
          <p className="mt-1 text-xs text-slate-500">Reverse proxy to Raft leader</p>
        </div>
      </div>

      {/* Section 1: Active Distributed Locks */}
      <div className="rounded-xl border border-slate-200 bg-white shadow-sm overflow-hidden">
        <div className="border-b border-slate-200 px-6 py-4 flex items-center justify-between">
          <div className="flex items-center space-x-2">
            <Shield className="h-5 w-5 text-blue-600" />
            <h2 className="text-sm font-semibold text-slate-900">Authoritative Distributed Locks</h2>
          </div>
          <span className="text-xs text-slate-500">In-Memory FSM Replicated State</span>
        </div>

        <div className="overflow-x-auto">
          {locks.length === 0 ? (
            <div className="p-8 text-center text-xs text-slate-500">
              No active locks found. Use &quot;Acquire Lock&quot; above to acquire a lease.
            </div>
          ) : (
            <table className="min-w-full divide-y divide-slate-200 text-left text-xs">
              <thead className="bg-slate-50 text-slate-600 uppercase font-medium">
                <tr>
                  <th className="px-6 py-3">Resource Name</th>
                  <th className="px-6 py-3">Current Owner</th>
                  <th className="px-6 py-3">Fencing Token</th>
                  <th className="px-6 py-3">Lease Version</th>
                  <th className="px-6 py-3">Expires At</th>
                  <th className="px-6 py-3 text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-200 text-slate-800">
                {locks.map((lock) => {
                  const expiresDate = new Date(lock.expires_at_unix_nano / 1000000);
                  const isExpired = Date.now() > expiresDate.getTime();
                  const isBeingRenewed = renewals.some((r) => r.resource_name === lock.resource_name && r.active);

                  return (
                    <tr key={lock.resource_name} className="hover:bg-slate-50">
                      <td className="px-6 py-3.5 font-semibold text-slate-900 flex items-center space-x-1.5">
                        <KeyRound className="h-3.5 w-3.5 text-blue-500" />
                        <span>{lock.resource_name}</span>
                      </td>
                      <td className="px-6 py-3.5">{lock.owner_id}</td>
                      <td className="px-6 py-3.5 font-mono text-blue-700 font-medium">
                        #{lock.fencing_token}
                      </td>
                      <td className="px-6 py-3.5 font-mono">
                        <span className="rounded bg-slate-100 px-2 py-0.5 font-semibold text-slate-700">
                          v{lock.lease_version}
                        </span>
                      </td>
                      <td className="px-6 py-3.5">
                        <span className={`inline-flex items-center space-x-1 ${isExpired ? 'text-rose-600' : 'text-slate-600'}`}>
                          <Timer className="h-3 w-3" />
                          <span>{expiresDate.toLocaleTimeString()}</span>
                        </span>
                      </td>
                      <td className="px-6 py-3.5 text-right space-x-2">
                        {!isBeingRenewed && (
                          <button
                            onClick={() => handleStartRenewal(lock)}
                            className="rounded bg-indigo-50 px-2.5 py-1 text-[11px] font-semibold text-indigo-600 hover:bg-indigo-100"
                          >
                            Auto-Renew
                          </button>
                        )}
                        <button
                          onClick={() => handleRelease(lock)}
                          className="rounded bg-rose-50 px-2.5 py-1 text-[11px] font-semibold text-rose-600 hover:bg-rose-100"
                        >
                          Release
                        </button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          )}
        </div>
      </div>

      {/* Grid: Client Sessions & Renewal Workers */}
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        {/* Section 2: Client Heartbeat Sessions */}
        <div className="rounded-xl border border-slate-200 bg-white shadow-sm overflow-hidden">
          <div className="border-b border-slate-200 px-6 py-4 flex items-center justify-between">
            <div className="flex items-center space-x-2">
              <Users className="h-5 w-5 text-emerald-600" />
              <h2 className="text-sm font-semibold text-slate-900">Client Heartbeat Sessions</h2>
            </div>
            <span className="text-xs text-slate-500">TTL Inactivity Scrubber</span>
          </div>

          <div className="overflow-x-auto">
            {sessions.length === 0 ? (
              <div className="p-8 text-center text-xs text-slate-500">
                No registered client sessions.
              </div>
            ) : (
              <table className="min-w-full divide-y divide-slate-200 text-left text-xs">
                <thead className="bg-slate-50 text-slate-600 uppercase font-medium">
                  <tr>
                    <th className="px-4 py-2.5">Client ID</th>
                    <th className="px-4 py-2.5">Owner ID</th>
                    <th className="px-4 py-2.5">TTL</th>
                    <th className="px-4 py-2.5">Status</th>
                    <th className="px-4 py-2.5 text-right">Ping</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-200">
                  {sessions.map((s) => (
                    <tr key={s.client_id} className="hover:bg-slate-50">
                      <td className="px-4 py-2.5 font-medium text-slate-900">{s.client_id}</td>
                      <td className="px-4 py-2.5 text-slate-600">{s.owner_id}</td>
                      <td className="px-4 py-2.5 text-slate-600">{s.ttl_seconds}s</td>
                      <td className="px-4 py-2.5">
                        {s.active ? (
                          <span className="inline-flex items-center space-x-1 text-emerald-700 font-semibold">
                            <CheckCircle2 className="h-3 w-3" />
                            <span>Active</span>
                          </span>
                        ) : (
                          <span className="inline-flex items-center space-x-1 text-rose-600 font-semibold">
                            <XCircle className="h-3 w-3" />
                            <span>Expired</span>
                          </span>
                        )}
                      </td>
                      <td className="px-4 py-2.5 text-right">
                        <button
                          disabled={!s.active}
                          onClick={() => handleHeartbeat(s)}
                          className="rounded border border-slate-200 px-2 py-0.5 text-[11px] font-medium text-slate-700 hover:bg-slate-100 disabled:opacity-40"
                        >
                          Ping
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </div>

        {/* Section 3: Automatic Renewal Workers */}
        <div className="rounded-xl border border-slate-200 bg-white shadow-sm overflow-hidden">
          <div className="border-b border-slate-200 px-6 py-4 flex items-center justify-between">
            <div className="flex items-center space-x-2">
              <Repeat className="h-5 w-5 text-purple-600" />
              <h2 className="text-sm font-semibold text-slate-900">Automated Renewal Workers</h2>
            </div>
            <span className="text-xs text-slate-500">T_renew Dynamic Trigger</span>
          </div>

          <div className="overflow-x-auto">
            {renewals.length === 0 ? (
              <div className="p-8 text-center text-xs text-slate-500">
                No active lease renewal workers running.
              </div>
            ) : (
              <table className="min-w-full divide-y divide-slate-200 text-left text-xs">
                <thead className="bg-slate-50 text-slate-600 uppercase font-medium">
                  <tr>
                    <th className="px-4 py-2.5">Resource</th>
                    <th className="px-4 py-2.5">Version</th>
                    <th className="px-4 py-2.5">Next Renewal</th>
                    <th className="px-4 py-2.5">Last Status</th>
                    <th className="px-4 py-2.5 text-right">Action</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-200">
                  {renewals.map((r) => (
                    <tr key={r.resource_name} className="hover:bg-slate-50">
                      <td className="px-4 py-2.5 font-medium text-slate-900">{r.resource_name}</td>
                      <td className="px-4 py-2.5 font-mono text-indigo-600">v{r.lease_version}</td>
                      <td className="px-4 py-2.5 text-slate-600">
                        {r.next_renewal_at ? new Date(r.next_renewal_at).toLocaleTimeString() : 'Immediate'}
                      </td>
                      <td className="px-4 py-2.5">
                        <span className={`inline-block rounded px-2 py-0.5 text-[10px] font-semibold ${
                          r.last_attempt_status === 'success'
                            ? 'bg-emerald-50 text-emerald-700'
                            : 'bg-amber-50 text-amber-700'
                        }`}>
                          {r.last_attempt_status || 'scheduled'}
                        </span>
                      </td>
                      <td className="px-4 py-2.5 text-right">
                        <button
                          onClick={() => handleStopRenewal(r.resource_name)}
                          className="rounded border border-rose-200 bg-rose-50 px-2 py-0.5 text-[11px] font-semibold text-rose-600 hover:bg-rose-100"
                        >
                          Stop
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </div>
      </div>

      {/* Acquire Modal */}
      {showAcquireModal && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/50 p-4 backdrop-blur-sm">
          <div className="w-full max-w-md rounded-2xl bg-white p-6 shadow-xl">
            <h3 className="text-base font-semibold text-slate-900">Acquire Distributed Lock</h3>
            <p className="mt-1 text-xs text-slate-500">
              Routes through API Gateway directly to authoritative Raft leader.
            </p>

            <form onSubmit={handleAcquire} className="mt-4 space-y-4">
              <div>
                <label className="block text-xs font-medium text-slate-700">Resource Name</label>
                <input
                  type="text"
                  required
                  placeholder="e.g. database-orders-sync"
                  value={acquireForm.resource_name}
                  onChange={(e) => setAcquireForm({ ...acquireForm, resource_name: e.target.value })}
                  className="mt-1 block w-full rounded-lg border border-slate-300 px-3 py-2 text-xs text-slate-900 focus:border-blue-500 focus:outline-none"
                />
              </div>

              <div>
                <label className="block text-xs font-medium text-slate-700">Owner ID</label>
                <input
                  type="text"
                  required
                  placeholder="e.g. worker-node-primary"
                  value={acquireForm.owner_id}
                  onChange={(e) => setAcquireForm({ ...acquireForm, owner_id: e.target.value })}
                  className="mt-1 block w-full rounded-lg border border-slate-300 px-3 py-2 text-xs text-slate-900 focus:border-blue-500 focus:outline-none"
                />
              </div>

              <div>
                <label className="block text-xs font-medium text-slate-700">Lease TTL (Seconds)</label>
                <input
                  type="number"
                  min="1"
                  max="300"
                  required
                  value={acquireForm.ttl_seconds}
                  onChange={(e) => setAcquireForm({ ...acquireForm, ttl_seconds: parseInt(e.target.value, 10) || 15 })}
                  className="mt-1 block w-full rounded-lg border border-slate-300 px-3 py-2 text-xs text-slate-900 focus:border-blue-500 focus:outline-none"
                />
              </div>

              <div className="mt-6 flex justify-end space-x-2">
                <button
                  type="button"
                  onClick={() => setShowAcquireModal(false)}
                  className="rounded-lg border border-slate-300 px-4 py-2 text-xs font-medium text-slate-700 hover:bg-slate-50"
                >
                  Cancel
                </button>
                <button
                  type="submit"
                  className="rounded-lg bg-blue-600 px-4 py-2 text-xs font-semibold text-white hover:bg-blue-700"
                >
                  Confirm Acquire
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* Register Client Session Modal */}
      {showRegisterModal && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/50 p-4 backdrop-blur-sm">
          <div className="w-full max-w-md rounded-2xl bg-white p-6 shadow-xl">
            <h3 className="text-base font-semibold text-slate-900">Register Client Heartbeat Session</h3>
            <p className="mt-1 text-xs text-slate-500">
              Tracks client liveness and cancels renewal on missed heartbeats.
            </p>

            <form onSubmit={handleRegisterClient} className="mt-4 space-y-4">
              <div>
                <label className="block text-xs font-medium text-slate-700">Client ID</label>
                <input
                  type="text"
                  required
                  placeholder="e.g. client-alpha"
                  value={registerForm.client_id}
                  onChange={(e) => setRegisterForm({ ...registerForm, client_id: e.target.value })}
                  className="mt-1 block w-full rounded-lg border border-slate-300 px-3 py-2 text-xs text-slate-900 focus:border-blue-500 focus:outline-none"
                />
              </div>

              <div>
                <label className="block text-xs font-medium text-slate-700">Owner ID</label>
                <input
                  type="text"
                  required
                  placeholder="e.g. worker-node-primary"
                  value={registerForm.owner_id}
                  onChange={(e) => setRegisterForm({ ...registerForm, owner_id: e.target.value })}
                  className="mt-1 block w-full rounded-lg border border-slate-300 px-3 py-2 text-xs text-slate-900 focus:border-blue-500 focus:outline-none"
                />
              </div>

              <div>
                <label className="block text-xs font-medium text-slate-700">Heartbeat Timeout (Seconds)</label>
                <input
                  type="number"
                  min="5"
                  max="120"
                  required
                  value={registerForm.ttl_seconds}
                  onChange={(e) => setRegisterForm({ ...registerForm, ttl_seconds: parseInt(e.target.value, 10) || 30 })}
                  className="mt-1 block w-full rounded-lg border border-slate-300 px-3 py-2 text-xs text-slate-900 focus:border-blue-500 focus:outline-none"
                />
              </div>

              <div className="mt-6 flex justify-end space-x-2">
                <button
                  type="button"
                  onClick={() => setShowRegisterModal(false)}
                  className="rounded-lg border border-slate-300 px-4 py-2 text-xs font-medium text-slate-700 hover:bg-slate-50"
                >
                  Cancel
                </button>
                <button
                  type="submit"
                  className="rounded-lg bg-emerald-600 px-4 py-2 text-xs font-semibold text-white hover:bg-emerald-700"
                >
                  Register Session
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
};
