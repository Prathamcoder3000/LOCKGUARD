import React from 'react';
import { NavLink } from 'react-router-dom';
import { 
  ShieldCheck, 
  LayoutDashboard, 
  Lock, 
  Server, 
  Layers, 
  ScrollText, 
  Activity, 
  BarChart3, 
  Settings,
  X
} from 'lucide-react';

interface SidebarProps {
  isOpen: boolean;
  onClose: () => void;
}

interface NavItem {
  name: string;
  to: string;
  icon: React.ElementType;
  badge?: string;
}

export const Sidebar: React.FC<SidebarProps> = ({ isOpen, onClose }) => {
  const navItems: NavItem[] = [
    { name: 'Cluster Overview', to: '/', icon: LayoutDashboard },
    { name: 'Distributed Locks', to: '/locks', icon: Lock },
    { name: 'Coordinator Nodes', to: '/cluster', icon: Server, badge: '3 Nodes' },
    { name: 'Consensus & Raft', to: '/consensus', icon: Layers },
    { name: 'Audit & Events', to: '/audit', icon: ScrollText },
    { name: 'Benchmarks & Stress', to: '/benchmarks', icon: BarChart3 },
    { name: 'System Settings', to: '/settings', icon: Settings },
  ];

  return (
    <>
      {/* Mobile Backdrop */}
      {isOpen && (
        <div 
          className="fixed inset-0 z-40 bg-navy-950/70 backdrop-blur-sm lg:hidden"
          onClick={onClose}
        />
      )}

      {/* Sidebar Container */}
      <aside
        className={`fixed inset-y-0 left-0 z-50 flex w-72 flex-col bg-[#0B132B] text-slate-300 transition-transform duration-200 ease-in-out lg:static lg:translate-x-0 ${
          isOpen ? 'translate-x-0' : '-translate-x-0'
        }`}
      >
        {/* Logo and Brand Header */}
        <div className="flex h-16 items-center justify-between border-b border-white/10 px-6">
          <div className="flex items-center space-x-3">
            <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-gradient-to-tr from-blue-600 to-violet-600 shadow-md shadow-blue-500/20">
              <ShieldCheck className="h-6 w-6 text-white" />
            </div>
            <div>
              <span className="text-base font-bold tracking-tight text-white">LOCKGUARD</span>
              <span className="ml-2 rounded bg-blue-500/20 px-1.5 py-0.5 text-[10px] font-semibold text-blue-400">
                v0.1
              </span>
              <p className="text-[10px] font-medium uppercase tracking-wider text-slate-400">
                Consensus First
              </p>
            </div>
          </div>
          <button 
            onClick={onClose}
            className="rounded p-1 text-slate-400 hover:bg-white/5 hover:text-white lg:hidden"
          >
            <X className="h-5 w-5" />
          </button>
        </div>

        {/* Cluster Invariant Banner */}
        <div className="mx-4 mt-4 rounded-lg border border-white/5 bg-white/[0.03] p-3 text-xs">
          <div className="flex items-center space-x-2 text-slate-400 mb-1">
            <Activity className="h-3.5 w-3.5 text-blue-400" />
            <span className="font-semibold uppercase tracking-wider text-[11px] text-slate-300">Core Invariant</span>
          </div>
          <p className="font-mono text-[11px] text-blue-300">
            CONSISTENCY FIRST<br/>FAIL CLOSED
          </p>
          <div className="mt-2 text-[10px] text-slate-400">
            Quorum requirement: 2 of 3 nodes
          </div>
        </div>

        {/* Navigation Links */}
        <div className="mt-4 flex-1 overflow-y-auto px-4 custom-scrollbar">
          <nav className="space-y-1">
            <div className="px-2 pb-2 text-[11px] font-semibold uppercase tracking-wider text-slate-400">
              Coordination Service
            </div>
            {navItems.map((item) => {
              const Icon = item.icon;
              return (
                <NavLink
                  key={item.to}
                  to={item.to}
                  end={item.to === '/'}
                  className={({ isActive }) =>
                    `group flex items-center justify-between rounded-lg px-3 py-2.5 text-sm font-medium transition-all ${
                      isActive
                        ? 'bg-blue-600 text-white shadow-sm shadow-blue-500/20'
                        : 'text-slate-300 hover:bg-white/[0.06] hover:text-white'
                    }`
                  }
                  onClick={() => {
                    if (window.innerWidth < 1024) onClose();
                  }}
                >
                  <div className="flex items-center space-x-3">
                    <Icon className="h-4 w-4 shrink-0 transition-colors" />
                    <span>{item.name}</span>
                  </div>
                  {item.badge && (
                    <span className="rounded bg-white/10 px-1.5 py-0.5 text-[10px] font-medium text-slate-300">
                      {item.badge}
                    </span>
                  )}
                </NavLink>
              );
            })}
          </nav>
        </div>

        {/* Cluster Topology Footer */}
        <div className="border-t border-white/10 p-4">
          <div className="rounded-lg bg-white/[0.02] border border-white/5 p-3">
            <div className="flex items-center justify-between">
              <span className="text-xs font-medium text-slate-400">Cluster Mode</span>
              <span className="inline-flex items-center rounded-full bg-emerald-500/10 px-2 py-0.5 text-[10px] font-medium text-emerald-400">
                <span className="mr-1 h-1.5 w-1.5 rounded-full bg-emerald-400 animate-pulse" />
                3-Node Raft
              </span>
            </div>
            <div className="mt-2 text-[11px] text-slate-400 space-y-0.5 font-mono">
              <div>• node-a : active</div>
              <div>• node-b : active</div>
              <div>• node-c : active</div>
            </div>
          </div>
        </div>
      </aside>
    </>
  );
};

