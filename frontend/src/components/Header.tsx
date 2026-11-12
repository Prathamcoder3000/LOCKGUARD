import React, { useEffect, useState } from 'react';
import { Menu, Bell, Shield, CheckCircle2, AlertCircle, LogOut, AlertTriangle } from 'lucide-react';
import { useAuth } from '../context/AuthContext';
import { clusterService, HealthStatus } from '../services/clusterService';
import { apiClient } from '../services/api';

interface HeaderProps {
  onOpenSidebar: () => void;
}

interface SystemAlert {
  id: string;
  severity: 'error' | 'warn';
  title: string;
  detail: string;
}

export const Header: React.FC<HeaderProps> = ({ onOpenSidebar }) => {
  const { user, logout } = useAuth();
  const [health, setHealth] = useState<HealthStatus | null>(null);
  const [isOnline, setIsOnline] = useState<boolean | null>(null);
  const [alerts, setAlerts] = useState<SystemAlert[]>([]);
  const [notifOpen, setNotifOpen] = useState(false);

  const checkConnection = async () => {
    const nextAlerts: SystemAlert[] = [];
    try {
      const data = await clusterService.getHealth();
      setHealth(data);
      setIsOnline(true);
    } catch {
      setIsOnline(false);
      nextAlerts.push({
        id: 'api-offline',
        severity: 'error',
        title: 'API Gateway Unreachable',
        detail: 'Health probe to /health failed.',
      });
    }

    try {
      let report: any = null;
      try {
        const diagRes = await apiClient.get('/recovery/diagnostics');
        report = diagRes.data?.data;
      } catch (diagErr: any) {
        report = diagErr?.response?.data?.data ?? null;
      }

      if (report) {
        if (!report.raft_quorum_available || !report.leader_available) {
          nextAlerts.push({
            id: 'quorum-lost',
            severity: 'error',
            title: 'Raft Quorum Unavailable (Fail Closed)',
            detail: report.subsystems?.raft_consensus?.message || 'Majority consensus cannot be formed.',
          });
        }
        if (!report.database_available) {
          nextAlerts.push({
            id: 'db-down',
            severity: 'warn',
            title: 'PostgreSQL Audit Store Degraded',
            detail: report.subsystems?.database_persistence?.message || 'Audit records buffering in memory.',
          });
        }
        const pipeStats = report.subsystems?.audit_pipeline?.details;
        if (pipeStats && Number(pipeStats.failed_total) > 0) {
          nextAlerts.push({
            id: 'audit-drops',
            severity: 'warn',
            title: 'Audit Buffer Drops Detected',
            detail: `${pipeStats.failed_total} audit event(s) dropped under backpressure.`,
          });
        }
      }
    } catch {
      // diagnostics failure handled above if API offline
    }

    setAlerts(nextAlerts);
  };

  useEffect(() => {
    let isMounted = true;
    const run = async () => {
      if (isMounted) await checkConnection();
    };
    run();
    const interval = setInterval(run, 10000);
    return () => {
      isMounted = false;
      clearInterval(interval);
    };
  }, []);

  return (
    <header className="sticky top-0 z-30 flex h-16 w-full items-center justify-between border-b border-slate-200 bg-white px-6 shadow-subtle">
      <div className="flex items-center space-x-4">
        <button
          onClick={onOpenSidebar}
          className="rounded-lg p-1.5 text-slate-500 hover:bg-slate-100 lg:hidden"
          aria-label="Open sidebar"
        >
          <Menu className="h-6 w-6" />
        </button>
        <div className="hidden sm:flex items-center space-x-2 text-xs font-medium text-slate-500">
          <span>LockGuard Cluster</span>
          <span>/</span>
          <span className="font-semibold text-slate-800">Coordinator Dashboard</span>
        </div>
      </div>

      <div className="flex items-center space-x-4">
        {/* API Liveness Status */}
        <div className="flex items-center space-x-2 rounded-full border border-slate-200 bg-slate-50 px-3 py-1 text-xs">
          {isOnline === true ? (
            <>
              <CheckCircle2 className="h-3.5 w-3.5 text-emerald-600" />
              <span className="font-medium text-emerald-700">API Online</span>
              {health?.version && (
                <span className="font-mono text-slate-400">v{health.version}</span>
              )}
            </>
          ) : isOnline === false ? (
            <>
              <AlertCircle className="h-3.5 w-3.5 text-rose-500" />
              <span className="font-medium text-rose-700">API Offline</span>
            </>
          ) : (
            <span className="text-slate-400">Connecting...</span>
          )}
        </div>

        {/* Notifications */}
        <div className="relative">
          <button
            type="button"
            onClick={() => setNotifOpen((prev) => !prev)}
            aria-label="System Notifications"
            title={alerts.length > 0 ? `${alerts.length} active alert(s)` : 'System Notifications'}
            className="relative rounded-lg p-2 text-slate-500 hover:bg-slate-100 hover:text-slate-700"
          >
            <Bell className="h-4 w-4" />
            {alerts.length > 0 && (
              <span className="absolute right-1.5 top-1.5 h-2 w-2 rounded-full bg-rose-600" />
            )}
          </button>

          {notifOpen && (
            <div className="absolute right-0 mt-2 w-80 rounded-xl border border-slate-200 bg-white shadow-lg z-50 overflow-hidden">
              <div className="flex items-center justify-between border-b border-slate-100 bg-slate-50 px-4 py-2.5">
                <span className="text-xs font-semibold text-slate-800">
                  Cluster Alerts ({alerts.length})
                </span>
                <button
                  type="button"
                  onClick={() => checkConnection()}
                  className="text-[11px] font-medium text-blue-600 hover:text-blue-800"
                >
                  Refresh
                </button>
              </div>
              <div className="max-h-64 overflow-y-auto divide-y divide-slate-100">
                {alerts.length === 0 ? (
                  <div className="px-4 py-5 text-center text-xs text-slate-500">
                    <CheckCircle2 className="mx-auto h-5 w-5 text-emerald-500 mb-1.5" />
                    All coordination subsystems nominal.
                  </div>
                ) : (
                  alerts.map((a) => (
                    <div key={a.id} className="flex items-start space-x-2.5 px-4 py-3 text-xs">
                      <AlertTriangle
                        className={`h-4 w-4 shrink-0 mt-0.5 ${
                          a.severity === 'error' ? 'text-rose-600' : 'text-amber-500'
                        }`}
                      />
                      <div>
                        <p className="font-semibold text-slate-900">{a.title}</p>
                        <p className="text-[11px] text-slate-500 mt-0.5">{a.detail}</p>
                      </div>
                    </div>
                  ))
                )}
              </div>
            </div>
          )}
        </div>

        {/* User Profile */}
        <div className="flex items-center space-x-3 border-l border-slate-200 pl-4">
          <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-blue-50 text-blue-700 border border-blue-200">
            <Shield className="h-4 w-4" />
          </div>
          <div className="hidden md:block text-left text-xs">
            <p className="font-semibold text-slate-800">{user?.username || 'Operator'}</p>
            <p className="capitalize text-slate-500">{user?.role || 'Admin'}</p>
          </div>
          <button
            onClick={logout}
            title="Log out"
            className="rounded p-1.5 text-slate-400 hover:bg-slate-100 hover:text-slate-600"
          >
            <LogOut className="h-4 w-4" />
          </button>
        </div>
      </div>
    </header>
  );
};

