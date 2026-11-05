import React, { useEffect, useState } from 'react';
import { Menu, Bell, Shield, CheckCircle2, AlertCircle, LogOut } from 'lucide-react';
import { useAuth } from '../context/AuthContext';
import { clusterService, HealthStatus } from '../services/clusterService';

interface HeaderProps {
  onOpenSidebar: () => void;
}

export const Header: React.FC<HeaderProps> = ({ onOpenSidebar }) => {
  const { user, logout } = useAuth();
  const [health, setHealth] = useState<HealthStatus | null>(null);
  const [isOnline, setIsOnline] = useState<boolean | null>(null);

  useEffect(() => {
    let isMounted = true;
    const checkConnection = async () => {
      try {
        const data = await clusterService.getHealth();
        if (isMounted) {
          setHealth(data);
          setIsOnline(true);
        }
      } catch {
        if (isMounted) {
          setIsOnline(false);
        }
      }
    };

    checkConnection();
    const interval = setInterval(checkConnection, 10000);
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
              <span className="font-medium text-emerald-700">API Connected</span>
              {health?.version && (
                <span className="font-mono text-slate-400">v{health.version}</span>
              )}
            </>
          ) : isOnline === false ? (
            <>
              <AlertCircle className="h-3.5 w-3.5 text-amber-500" />
              <span className="font-medium text-amber-700">API Offline (Phase 0)</span>
            </>
          ) : (
            <span className="text-slate-400">Connecting...</span>
          )}
        </div>

        {/* Notifications */}
        <button className="relative rounded-lg p-2 text-slate-500 hover:bg-slate-100 hover:text-slate-700">
          <Bell className="h-4 w-4" />
          <span className="absolute right-1.5 top-1.5 h-2 w-2 rounded-full bg-blue-600" />
        </button>

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

