import React from 'react';

interface AuditStatusBadgeProps {
  status: string;
}

export const AuditStatusBadge: React.FC<AuditStatusBadgeProps> = ({ status }) => {
  const norm = status?.toUpperCase() || 'UNKNOWN';

  switch (norm) {
    case 'COMMITTED':
    case 'HEALTHY':
    case 'UP':
    case 'SUCCESS':
      return (
        <span className="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium bg-emerald-100 text-emerald-800 border border-emerald-200">
          <span className="w-1.5 h-1.5 mr-1.5 rounded-full bg-emerald-600 animate-pulse" />
          {norm}
        </span>
      );
    case 'REJECTED':
    case 'DEGRADED':
      return (
        <span className="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium bg-amber-100 text-amber-800 border border-amber-200">
          <span className="w-1.5 h-1.5 mr-1.5 rounded-full bg-amber-600" />
          {norm}
        </span>
      );
    case 'FAILED':
    case 'UNHEALTHY':
    case 'DOWN':
    case 'ERROR':
      return (
        <span className="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium bg-rose-100 text-rose-800 border border-rose-200">
          <span className="w-1.5 h-1.5 mr-1.5 rounded-full bg-rose-600" />
          {norm}
        </span>
      );
    default:
      return (
        <span className="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium bg-slate-100 text-slate-700 border border-slate-200">
          <span className="w-1.5 h-1.5 mr-1.5 rounded-full bg-slate-400" />
          {norm}
        </span>
      );
  }
};

