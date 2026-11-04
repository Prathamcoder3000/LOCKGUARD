import React from 'react';
import { Link } from 'react-router-dom';
import { ShieldAlert, ArrowLeft } from 'lucide-react';

export const NotFoundPage: React.FC = () => {
  return (
    <div className="flex min-h-[60vh] flex-col items-center justify-center text-center">
      <div className="rounded-2xl bg-slate-100 p-4 text-slate-500 mb-4">
        <ShieldAlert className="h-10 w-10 text-slate-700" />
      </div>
      <h1 className="text-xl font-bold text-slate-900">404 — View Not Found</h1>
      <p className="mt-1 text-sm text-slate-500 max-w-sm">
        The requested dashboard route does not exist in the LockGuard cluster control plane.
      </p>
      <Link
        to="/"
        className="mt-6 inline-flex items-center space-x-2 rounded-lg bg-blue-600 px-4 py-2 text-xs font-semibold text-white shadow-subtle hover:bg-blue-700"
      >
        <ArrowLeft className="h-4 w-4" />
        <span>Return to Cluster Overview</span>
      </Link>
    </div>
  );
};
