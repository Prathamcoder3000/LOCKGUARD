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
  ShieldAlert,
} from 'lucide-react';
import {
  clusterService,
  LockRecord,
  ClientSession,
  RenewalStatus,
} from '../services/clusterService';
import { useAuth } from '../context/AuthContext';
import { auditService, AuditEvent } from '../services/auditService';

export const LocksPage: React.FC = () => {
  const { user } = useAuth();
  const isAdmin = (user?.role || '').toUpperCase() === 'ADMIN';
  const isReadOnly = (user?.role || '').toUpperCase() === 'READ_ONLY';
  const currentOwnerID = user?.owner_id || '';

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
    owner_id: currentOwnerID,
    ttl_seconds: 15,
  });

  // Register Client Form
  const [registerForm, setRegisterForm] = useState({
    client_id: currentOwnerID ? `client-${currentOwnerID}` : '',
    owner_id: currentOwnerID,
    ttl_seconds: 30,
  });

  // Update modal form defaults when user changes
  useEffect(() => {
    if (currentOwnerID) {
      setAcquireForm((prev) => ({ ...prev, owner_id: currentOwnerID }));
      setRegisterForm((prev) => ({
        ...prev,
        client_id: prev.client_id || `client-${currentOwnerID}`,
        owner_id: currentOwnerID,
      }));
    }
  }, [currentOwnerID]);

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

  const [nowMs, setNowMs] = React.useState<number>(() => Date.now());
  const [recentExpirations, setRecentExpirations] = React.useState<AuditEvent[]>([]);
  const expiredBoundaryTriggeredRef = React.useRef<Set<string>>(new Set());

  React.useEffect(() => {
    const timer = setInterval(() => setNowMs(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);

  React.useEffect(() => {
    let active = true;
    auditService
      .getEvents({ event_type: 'LOCK_EXPIRE', limit: 5 })
      .then((res) => {
        if (active) setRecentExpirations(res.events || []);
      })
      .catch(() => {
        // Non-blocking audit strip
      });
    return () => {
      active = false;
    };
  }, [locks]);

  React.useEffect(() => {
    const newlyExpired = locks.filter((lock) => {
      const expiresMs = lock.expires_at_unix_nano / 1000000;
      const key = `${lock.resource_name}:${lock.fencing_token}:${lock.expires_at_unix_nano}`;
      return lock.status !== 'EXPIRED' && nowMs >= expiresMs && !expiredBoundaryTriggeredRef.current.has(key);
    });
    if (newlyExpired.length > 0) {
      newlyExpired.forEach((lock) => {
        expiredBoundaryTriggeredRef.current.add(
          `${lock.resource_name}:${lock.fencing_token}:${lock.expires_at_unix_nano}`
        );
      });
      const refreshTimer = setTimeout(() => {
        fetchData();
      }, 450);
      return () => clearTimeout(refreshTimer);
    }
  }, [locks, nowMs]);

  const handleAcquire = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      const payload = {
        ...acquireForm,
        // For non-admin, strictly enforce own bound owner_id
        owner_id: !isAdmin && currentOwnerID ? currentOwnerID : acquireForm.owner_id,
      };
      await clusterService.acquireLock(payload);
      setShowAcquireModal(false);
      setAcquireForm({ resource_name: '', owner_id: currentOwnerID, ttl_seconds: 15 });
      fetchData();
    } catch (err: any) {
      alert(`Acquire Lock Failed: ${err?.response?.data?.error?.message || err.message}`);
    }
  };

  const handleRelease = async (lock: LockRecord) => {
    const isOwner = currentOwnerID !== '' && lock.owner_id === currentOwnerID;

    if (isAdmin && !isOwner) {
      if (!confirm(`ADMIN EMERGENCY UNLOCK: Force break active lease on "${lock.resource_name}" held by "${lock.owner_id}"?`)) return;
      try {
        await clusterService.adminEmergencyUnlock(lock.resource_name);
        fetchData();
      } catch (err: any) {
        alert(`Emergency Unlock Failed: ${err?.response?.data?.error?.message || err.message}`);
      }
      return;
    }

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

  const STORE_SESSION_KEY = 'lockguard_client_sessions';

  const saveClientSessionSecret = (clientId: string, sessionId?: string) => {
    if (!clientId || !sessionId || sessionId === '[REDACTED]') return;
    try {
      const existing = JSON.parse(localStorage.getItem(STORE_SESSION_KEY) || '{}');
      existing[clientId] = sessionId;
      localStorage.setItem(STORE_SESSION_KEY, JSON.stringify(existing));
    } catch {
      // ignore storage quota errors
    }
  };

  const resolveClientSessionSecret = (clientId: string, fallbackSessionId?: string): string => {
    if (fallbackSessionId && fallbackSessionId !== '[REDACTED]') {
      return fallbackSessionId;
    }
    try {
      const existing = JSON.parse(localStorage.getItem(STORE_SESSION_KEY) || '{}');
      if (existing[clientId] && existing[clientId] !== '[REDACTED]') {
        return existing[clientId];
      }
    } catch {
      // ignore parse errors
    }
    return fallbackSessionId || '';
  };

  const handleRegisterClient = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      const payload = {
        ...registerForm,
        owner_id: !isAdmin && currentOwnerID ? currentOwnerID : registerForm.owner_id,
      };
      const created = await clusterService.registerClient(payload);
      saveClientSessionSecret(created.client_id || payload.client_id, created.session_id);
      setShowRegisterModal(false);
      setRegisterForm({
        client_id: currentOwnerID ? `client-${currentOwnerID}` : '',
        owner_id: currentOwnerID,
        ttl_seconds: 30,
      });
      fetchData();
    } catch (err: any) {
      alert(`Client Registration Failed: ${err?.response?.data?.error?.message || err.message}`);
    }
  };

  const handleHeartbeat = async (s: ClientSession) => {
    try {
      const sid = resolveClientSessionSecret(s.client_id, s.session_id);
      if (!sid || sid === '[REDACTED]') {
        window.alert('Cannot ping heartbeat: secret session_id is only available in the browser session that registered this client. Register a new session after expiry.');
        return;
      }
      await clusterService.sendHeartbeat({ client_id: s.client_id, session_id: sid });
      fetchData();
    } catch (err: any) {
      alert(`Heartbeat Failed: ${err?.response?.data?.error?.message || err.message}`);
    }
  };

  const handleStartRenewal = async (lock: LockRecord) => {
    // Prefer an active session for this owner whose secret session_id is held locally
    const matchingSession =
      sessions.find(
        (s) =>
          s.owner_id === lock.owner_id &&
          s.active &&
          resolveClientSessionSecret(s.client_id, s.session_id) !== '[REDACTED]' &&
          resolveClientSessionSecret(s.client_id, s.session_id) !== ''
      ) || sessions.find((s) => s.owner_id === lock.owner_id && s.active);

    if (!matchingSession) {
      const registerNow = confirm(
        `No active heartbeat session found for owner "${lock.owner_id}". A live session is required for automatic lease renewal. Would you like to register one now?`
      );
      if (registerNow) {
        setRegisterForm({
          client_id: `client-${lock.owner_id}`,
          owner_id: lock.owner_id,
          ttl_seconds: 30,
        });
        setShowRegisterModal(true);
      }
      return;
    }

    const sid = resolveClientSessionSecret(matchingSession.client_id, matchingSession.session_id);
    if (!sid || sid === '[REDACTED]') {
      alert(
        `Active session "${matchingSession.client_id}" belongs to an external client or prior browser session without its secret session_id stored locally. Register a new client session after it expires to control Auto-Renew from this browser.`
      );
      return;
    }

    try {
      await clusterService.startRenewal({
        resource_name: lock.resource_name,
        owner_id: lock.owner_id,
        client_id: matchingSession.client_id,
        session_id: sid,
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
      const worker = renewals.find((r) => r.resource_name === resource_name);
      let sid = worker ? resolveClientSessionSecret(worker.client_id, worker.session_id) : '';
      if (!sid || sid === '[REDACTED]') {
        const activeSession = worker
          ? sessions.find((s) => s.client_id === worker.client_id)
          : sessions.find((s) => s.active);
        if (activeSession) {
          sid = resolveClientSessionSecret(activeSession.client_id, activeSession.session_id);
        }
      }
      await clusterService.stopRenewal(resource_name, sid || undefined);
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
            Authoritative Raft Consensus Leases, Heartbeat Sessions &amp; Automated Renewal Workers
          </p>
        </div>
        <div className="flex items-center space-x-2">
          {!isReadOnly && (
            <>
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
            </>
          )}
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
          <p className="mt-2 text-2xl font-bold text-slate-900">
            {locks.filter((l) => l.status !== 'EXPIRED' && nowMs < l.expires_at_unix_nano / 1000000).length}
          </p>
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
            <span className="text-xs font-medium uppercase text-slate-500">Session Scrubber</span>
            <div className="rounded-lg bg-indigo-50 p-2 text-indigo-600">
              <Activity className="h-4 w-4" />
            </div>
          </div>
          <p className="mt-2 text-sm font-semibold text-slate-900">100ms Tick Rate</p>
          <p className="mt-1 text-xs text-slate-500">Immediate dead-worker termination</p>
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
                  <th className="px-6 py-3">Status</th>
                  <th className="px-6 py-3">Expires At</th>
                  <th className="px-6 py-3 text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-200 text-slate-800">
                {locks.map((lock) => {
                  const expiresDate = new Date(lock.expires_at_unix_nano / 1000000);
                  const isExpired = lock.status === 'EXPIRED' || nowMs >= expiresDate.getTime();
                  const isBeingRenewed = renewals.some((r) => r.resource_name === lock.resource_name && r.active);
                  const isOwner = currentOwnerID !== '' && lock.owner_id === currentOwnerID;

                  return (
                    <tr key={lock.resource_name} className="hover:bg-slate-50">
                      <td className="px-6 py-3.5 font-semibold text-slate-900 flex items-center space-x-1.5">
                        <KeyRound className="h-3.5 w-3.5 text-blue-500" />
                        <span>{lock.resource_name}</span>
                      </td>
                      <td className="px-6 py-3.5">
                        <span className="font-mono text-slate-800">{lock.owner_id}</span>
                        {isOwner && (
                          <span className="ml-1.5 rounded bg-blue-50 px-1.5 py-0.5 text-[10px] font-bold text-blue-700">
                            You
                          </span>
                        )}
                      </td>
                      <td className="px-6 py-3.5 font-mono text-blue-700 font-medium">
                        #{lock.fencing_token}
                      </td>
                      <td className="px-6 py-3.5 font-mono">
                        <span className="rounded bg-slate-100 px-2 py-0.5 font-semibold text-slate-700">
                          v{lock.lease_version}
                        </span>
                      </td>
                      <td className="px-6 py-3.5">
                        {isExpired ? (
                          <span className="inline-flex items-center rounded-full bg-rose-50 px-2.5 py-0.5 text-[11px] font-semibold text-rose-700 border border-rose-200">
                            EXPIRED
                          </span>
                        ) : (
                          <span className="inline-flex items-center rounded-full bg-emerald-50 px-2.5 py-0.5 text-[11px] font-semibold text-emerald-700 border border-emerald-200">
                            HELD
                          </span>
                        )}
                      </td>
                      <td className="px-6 py-3.5">
                        <span className={`inline-flex items-center space-x-1 ${isExpired ? 'text-rose-600 font-semibold' : 'text-slate-600'}`}>
                          <Timer className="h-3 w-3" />
                          <span>{expiresDate.toLocaleTimeString()}</span>
                        </span>
                      </td>
                      <td className="px-6 py-3.5 text-right space-x-2">
                        {/* Auto-Renew Button */}
                        {(isOwner || isAdmin) && !isBeingRenewed && !isExpired && (
                          <button
                            onClick={() => handleStartRenewal(lock)}
                            className="rounded bg-indigo-50 px-2.5 py-1 text-[11px] font-semibold text-indigo-600 hover:bg-indigo-100"
                          >
                            Auto-Renew
                          </button>
                        )}

                        {/* Release / Emergency Unlock */}
                        {isOwner ? (
                          <button
                            onClick={() => handleRelease(lock)}
                            className="rounded bg-rose-50 px-2.5 py-1 text-[11px] font-semibold text-rose-600 hover:bg-rose-100"
                          >
                            Release
                          </button>
                        ) : isAdmin ? (
                          <button
                            onClick={() => handleRelease(lock)}
                            className="inline-flex items-center space-x-1 rounded bg-rose-100 px-2.5 py-1 text-[11px] font-bold text-rose-700 hover:bg-rose-200"
                            title="Admin Emergency Unlock"
                          >
                            <ShieldAlert className="h-3 w-3" />
                            <span>Unlock</span>
                          </button>
                        ) : (
                          <span className="rounded bg-slate-100 px-2 py-0.5 text-[10px] text-slate-500 font-mono">
                            Locked
                          </span>
                        )}
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
                No registered client sessions. Click &quot;Register Session&quot; to begin sending heartbeats.
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
                        {!isReadOnly && (isAdmin || s.owner_id === currentOwnerID) ? (
                          <button
                            onClick={() => handleHeartbeat(s)}
                            className="rounded bg-emerald-50 px-2 py-0.5 text-[11px] font-semibold text-emerald-700 hover:bg-emerald-100"
                          >
                            Ping Heartbeat
                          </button>
                        ) : (
                          <span className="rounded bg-slate-100 px-2 py-0.5 text-[10px] text-slate-500 font-mono">
                            {isReadOnly ? 'Read Only' : 'Other'}
                          </span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </div>

        {/* Section 3: Renewal Workers */}
        <div className="rounded-xl border border-slate-200 bg-white shadow-sm overflow-hidden">
          <div className="border-b border-slate-200 px-6 py-4 flex items-center justify-between">
            <div className="flex items-center space-x-2">
              <Repeat className="h-5 w-5 text-purple-600" />
              <h2 className="text-sm font-semibold text-slate-900">Automated Renewal Workers</h2>
            </div>
            <span className="text-xs text-slate-500">Autonomous Lease Extensions</span>
          </div>

          <div className="overflow-x-auto">
            {renewals.length === 0 ? (
              <div className="p-8 text-center text-xs text-slate-500">
                No active lease renewal workers running. Click &quot;Auto-Renew&quot; on a lock to start one.
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
                        {!isReadOnly && (isAdmin || r.owner_id === currentOwnerID) ? (
                          <button
                            onClick={() => handleStopRenewal(r.resource_name)}
                            className="rounded border border-rose-200 bg-rose-50 px-2 py-0.5 text-[11px] font-semibold text-rose-600 hover:bg-rose-100"
                          >
                            Stop
                          </button>
                        ) : (
                          <span className="rounded bg-slate-100 px-2 py-0.5 text-[10px] text-slate-500 font-mono">
                            {isReadOnly ? 'Read Only' : 'Other'}
                          </span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </div>
      </div>

      {/* Section 4: Recent Lease Expirations (Audit) */}
      <div className="rounded-xl border border-slate-200 bg-white shadow-sm overflow-hidden">
        <div className="border-b border-slate-200 px-6 py-3.5 flex items-center justify-between">
          <div className="flex items-center space-x-2">
            <Timer className="h-4 w-4 text-amber-600" />
            <h2 className="text-sm font-semibold text-slate-900">Recent Lease Expirations (Audit)</h2>
          </div>
          <span className="text-xs text-slate-500">Reaped TTL leases (LOCK_EXPIRE)</span>
        </div>
        {recentExpirations.length === 0 ? (
          <div className="p-5 text-center text-xs text-slate-500">
            No recent TTL lease expirations recorded in the audit log.
          </div>
        ) : (
          <div className="divide-y divide-slate-100">
            {recentExpirations.map((ev) => (
              <div key={ev.event_id} className="px-6 py-2.5 flex flex-wrap items-center justify-between gap-2 text-xs">
                <div className="flex items-center space-x-3">
                  <span className="rounded bg-amber-50 border border-amber-200 px-2 py-0.5 font-mono text-[10px] font-semibold text-amber-800">
                    LOCK_EXPIRE
                  </span>
                  <span className="font-mono font-semibold text-slate-900">{ev.resource_id || '—'}</span>
                  <span className="text-slate-500">
                    owner <span className="font-mono text-slate-700">{ev.owner_id || '—'}</span>
                  </span>
                </div>
                <div className="flex items-center space-x-4 font-mono text-[11px] text-slate-500">
                  <span>token #{ev.fencing_token}</span>
                  <span>v{ev.lease_version}</span>
                  <span>{new Date(ev.event_timestamp).toLocaleTimeString()}</span>
                </div>
              </div>
            ))}
          </div>
        )}
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
                <label className="block text-xs font-medium text-slate-700">
                  Owner ID {!isAdmin && currentOwnerID && <span className="text-slate-400 font-normal">(Bound to session)</span>}
                </label>
                <input
                  type="text"
                  required
                  disabled={!isAdmin && Boolean(currentOwnerID)}
                  placeholder="e.g. worker-node-primary"
                  value={acquireForm.owner_id}
                  onChange={(e) => setAcquireForm({ ...acquireForm, owner_id: e.target.value })}
                  className="mt-1 block w-full rounded-lg border border-slate-300 px-3 py-2 text-xs text-slate-900 focus:border-blue-500 focus:outline-none disabled:bg-slate-100 disabled:text-slate-500"
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
              Establishes client liveness to allow autonomous lease renewals.
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
                <label className="block text-xs font-medium text-slate-700">
                  Owner ID {!isAdmin && currentOwnerID && <span className="text-slate-400 font-normal">(Bound to session)</span>}
                </label>
                <input
                  type="text"
                  required
                  disabled={!isAdmin && Boolean(currentOwnerID)}
                  placeholder="e.g. worker-node-primary"
                  value={registerForm.owner_id}
                  onChange={(e) => setRegisterForm({ ...registerForm, owner_id: e.target.value })}
                  className="mt-1 block w-full rounded-lg border border-slate-300 px-3 py-2 text-xs text-slate-900 focus:border-blue-500 focus:outline-none disabled:bg-slate-100 disabled:text-slate-500"
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
